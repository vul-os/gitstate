// Package analytics — geo_test.go
// DB-free unit tests for the GeoResolver: graceful with no mmdb configured, a
// graceful no-op for a bogus path, and private/loopback IPs always resolving to
// "unknown" (empty). These never require a real MaxMind database.
package analytics

import (
	"net"
	"testing"
)

func TestGeoResolverNoDatabase(t *testing.T) {
	// Empty path → resolver must be usable and always return "".
	g := NewGeoResolver("")
	c, r, ci := g.Lookup(net.ParseIP("8.8.8.8"))
	if c != "" || r != "" || ci != "" {
		t.Fatalf("no-DB resolver returned (%q,%q,%q), want all empty", c, r, ci)
	}
	if err := g.Close(); err != nil {
		t.Fatalf("Close on unopened resolver: %v", err)
	}
}

func TestGeoResolverMissingFileGraceful(t *testing.T) {
	// A non-existent path must not panic or error — just resolve to "".
	g := NewGeoResolver("/nonexistent/path/to/geo.mmdb")
	c, r, ci := g.Lookup(net.ParseIP("1.1.1.1"))
	if c != "" || r != "" || ci != "" {
		t.Fatalf("missing-file resolver returned (%q,%q,%q), want all empty", c, r, ci)
	}
}

func TestGeoResolverPrivateAndInvalidIPs(t *testing.T) {
	// Even if a DB were present, these IPs must never resolve to a location.
	g := NewGeoResolver("")
	for _, ip := range []string{
		"10.1.2.3",    // private
		"192.168.0.1", // private
		"172.16.5.5",  // private
		"127.0.0.1",   // loopback
		"::1",         // loopback v6
		"169.254.1.1", // link-local
		"0.0.0.0",     // unspecified
		"fe80::1",     // link-local v6
	} {
		c, r, ci := g.Lookup(net.ParseIP(ip))
		if c != "" || r != "" || ci != "" {
			t.Errorf("Lookup(%s) = (%q,%q,%q), want unknown/empty", ip, c, r, ci)
		}
	}

	// nil IP is tolerated.
	if c, r, ci := g.Lookup(nil); c != "" || r != "" || ci != "" {
		t.Errorf("Lookup(nil) = (%q,%q,%q), want empty", c, r, ci)
	}
}
