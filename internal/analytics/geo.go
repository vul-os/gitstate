// Package analytics — geo.go
// Coarse, privacy-first IP→geo resolution for the super-admin analytics console.
//
// A GeoResolver wraps an optional local MaxMind/db-ip .mmdb database (city or
// country granularity). It is constructed cheaply and opens the database LAZILY
// on first lookup (sync.Once) so a missing/blank GEOIP_DB_PATH never breaks
// startup. Everything here is read-only and degrades gracefully: a missing
// database, an unparsable IP, or a private/loopback address all resolve to the
// empty string for country/region/city — the resolver never returns an error
// and never panics. The *Reader from maxminddb v2 is safe for concurrent use,
// so a single resolver is shared across all requests.
package analytics

import (
	"net"
	"net/netip"
	"sync"

	maxminddb "github.com/oschwald/maxminddb-golang/v2"
)

// GeoResolver resolves an IP to coarse geo (country / region / city) using an
// optional local mmdb database. The zero value is not usable — construct one
// with NewGeoResolver. A resolver with an empty path resolves everything to "".
type GeoResolver struct {
	path string

	once   sync.Once
	reader *maxminddb.Reader // nil when the path is empty or the DB failed to open
}

// NewGeoResolver returns a resolver bound to the mmdb at dbPath. The database is
// NOT opened here — it is opened lazily on the first Lookup so that a
// missing/blank path or an unreadable file never affects process startup. Pass
// "" (e.g. an unset GEOIP_DB_PATH) to get a resolver that always returns "".
func NewGeoResolver(dbPath string) *GeoResolver {
	return &GeoResolver{path: dbPath}
}

// geoCity is the subset of the MaxMind / db-ip record we care about. The schema
// matches both the GeoLite2/GeoIP2 City and db-ip City layouts; country-only
// databases simply leave subdivisions/city absent, which decodes to "".
type geoCity struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
	Subdivisions []struct {
		Names map[string]string `maxminddb:"names"`
	} `maxminddb:"subdivisions"`
	City struct {
		Names map[string]string `maxminddb:"names"`
	} `maxminddb:"city"`
}

// load opens the mmdb exactly once. Any failure (blank path, missing file,
// corrupt database) leaves reader nil so every Lookup returns "".
func (g *GeoResolver) load() {
	g.once.Do(func() {
		if g.path == "" {
			return
		}
		r, err := maxminddb.Open(g.path)
		if err != nil {
			// Graceful: log-free, reader stays nil → all lookups return "".
			return
		}
		g.reader = r
	})
}

// Lookup resolves ip to coarse geo. It returns ("", "", "") — never an error —
// for any of: no database configured, an invalid/unspecified IP, a private,
// loopback, or link-local address (no meaningful geo), or an IP absent from the
// database. The english ("en") name is preferred for region/city.
func (g *GeoResolver) Lookup(ip net.IP) (country, region, city string) {
	g.load()
	if g.reader == nil || ip == nil {
		return "", "", ""
	}

	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return "", "", ""
	}
	addr = addr.Unmap()
	// Skip addresses that can never have public geo. This also keeps us from
	// ever hashing+geolocating internal traffic.
	if !addr.IsValid() || addr.IsUnspecified() || addr.IsLoopback() ||
		addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() {
		return "", "", ""
	}

	var rec geoCity
	if err := g.reader.Lookup(addr).Decode(&rec); err != nil {
		return "", "", ""
	}

	country = rec.Country.ISOCode
	if len(rec.Subdivisions) > 0 {
		region = pickName(rec.Subdivisions[0].Names)
	}
	city = pickName(rec.City.Names)
	return country, region, city
}

// Close releases the underlying database handle if one was opened. Safe to call
// on a resolver whose database never opened.
func (g *GeoResolver) Close() error {
	if g.reader != nil {
		return g.reader.Close()
	}
	return nil
}

// pickName returns the english name when present, otherwise any available name,
// otherwise "".
func pickName(names map[string]string) string {
	if names == nil {
		return ""
	}
	if n, ok := names["en"]; ok {
		return n
	}
	for _, n := range names {
		return n
	}
	return ""
}
