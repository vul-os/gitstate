// Package store — analytics_events.go
// Read/write helpers for the GLOBAL analytics_events table (migration 022): the
// privacy-first product-analytics feed behind the super-admin console. The table
// is NOT org-scoped — it is written by the capture middleware and read ONLY via
// the BYPASSRLS admin pool by the super-admin console, never on the org-scoped
// /api surface. The raw IP is never stored: only the salted ip_hash plus coarse
// geo (country/region/city).
//
// These reads are intentionally pool-direct (no RLS context), matching admin.go
// and S2's "audited service path, not ambient bypass" intent.
package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AnalyticsEvent is one row of the analytics_events table. UserID / OrgID are
// pointers so that NULL (anonymous / cross-org) events can be inserted without
// inventing a sentinel uuid. The raw IP must never reach this struct — only the
// precomputed salted hash in IPHash.
type AnalyticsEvent struct {
	Kind      string  // signup | login | login_failed | logout | pageview | api
	UserID    *string // NULL when anonymous
	OrgID     *string // NULL when not org-scoped
	Path      string
	Method    string
	Status    int
	IPHash    string // sha256(ip + salt); raw IP NEVER stored
	Country   string // ISO-3166 alpha-2, "" when unknown
	Region    string
	City      string
	UserAgent string
}

// ── Aggregate row types ───────────────────────────────────────────────────────

// CountryCount is one row of the events-by-country aggregate.
type CountryCount struct {
	Country string
	Count   int
}

// KindCount is one row of the events-by-kind aggregate.
type KindCount struct {
	Kind  string
	Count int
}

// EventDay is one day of a daily analytics time-series.
type EventDay struct {
	Day   time.Time
	Count int
}

// RecentEvent is one row of the live activity feed.
type RecentEvent struct {
	Kind      string
	Path      string
	Method    string
	Status    int
	Country   string
	Region    string
	City      string
	UserAgent string
	CreatedAt time.Time
}

// ── Write path ────────────────────────────────────────────────────────────────

// InsertAnalyticsEvent inserts one analytics event. Designed to be called off
// the request path (the capture middleware runs it in a goroutine), so it takes
// its own context rather than borrowing the request's. Empty geo / hash columns
// are stored as-is ("" rather than NULL) which keeps the aggregates simple.
func InsertAnalyticsEvent(ctx context.Context, pool *pgxpool.Pool, e AnalyticsEvent) error {
	const q = `
		INSERT INTO analytics_events
			(kind, user_id, org_id, path, method, status, ip_hash, country, region, city, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`
	_, err := pool.Exec(ctx, q,
		e.Kind, e.UserID, e.OrgID, e.Path, e.Method, e.Status,
		e.IPHash, e.Country, e.Region, e.City, e.UserAgent,
	)
	return err
}

// ── Read path (super-admin aggregates over the bypass pool) ───────────────────

// AnalyticsEventsByCountry returns the top-N countries by event count over the
// last nDays, skipping the "" (unknown) bucket so the map/table shows only
// resolved geo.
func AnalyticsEventsByCountry(ctx context.Context, pool *pgxpool.Pool, nDays, limit int) ([]CountryCount, error) {
	const q = `
		SELECT country, COUNT(*)
		FROM analytics_events
		WHERE created_at >= now() - ($1 * interval '1 day')
		  AND country <> ''
		GROUP BY country
		ORDER BY COUNT(*) DESC, country ASC
		LIMIT $2`
	rows, err := pool.Query(ctx, q, nDays, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CountryCount
	for rows.Next() {
		var c CountryCount
		if err := rows.Scan(&c.Country, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AnalyticsCountsByKind returns the event count per kind over the last nDays,
// ordered most-frequent first.
func AnalyticsCountsByKind(ctx context.Context, pool *pgxpool.Pool, nDays int) ([]KindCount, error) {
	const q = `
		SELECT kind, COUNT(*)
		FROM analytics_events
		WHERE created_at >= now() - ($1 * interval '1 day')
		GROUP BY kind
		ORDER BY COUNT(*) DESC, kind ASC`
	rows, err := pool.Query(ctx, q, nDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []KindCount
	for rows.Next() {
		var k KindCount
		if err := rows.Scan(&k.Kind, &k.Count); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// AnalyticsKindByDay returns a daily count time-series for one event kind over
// the last nDays (e.g. "signup" or "login"). Days with zero events are absent.
func AnalyticsKindByDay(ctx context.Context, pool *pgxpool.Pool, kind string, nDays int) ([]EventDay, error) {
	const q = `
		SELECT date_trunc('day', created_at) AS day, COUNT(*)
		FROM analytics_events
		WHERE kind = $1
		  AND created_at >= now() - ($2 * interval '1 day')
		GROUP BY day
		ORDER BY day`
	rows, err := pool.Query(ctx, q, kind, nDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []EventDay
	for rows.Next() {
		var d EventDay
		if err := rows.Scan(&d.Day, &d.Count); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// AnalyticsRecentEvents returns the most recent limit events for the live
// activity feed, newest first.
func AnalyticsRecentEvents(ctx context.Context, pool *pgxpool.Pool, limit int) ([]RecentEvent, error) {
	const q = `
		SELECT kind, COALESCE(path,''), COALESCE(method,''), COALESCE(status,0),
		       country, region, city, COALESCE(user_agent,''), created_at
		FROM analytics_events
		ORDER BY created_at DESC
		LIMIT $1`
	rows, err := pool.Query(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []RecentEvent
	for rows.Next() {
		var e RecentEvent
		if err := rows.Scan(&e.Kind, &e.Path, &e.Method, &e.Status,
			&e.Country, &e.Region, &e.City, &e.UserAgent, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AnalyticsOnlineNow returns the number of distinct ip_hash values seen in the
// last 5 minutes — a coarse, privacy-preserving "online now" gauge (it counts
// salted hashes, never raw IPs). Blank hashes are excluded.
func AnalyticsOnlineNow(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT ip_hash)
		FROM analytics_events
		WHERE ip_hash <> ''
		  AND created_at >= now() - interval '5 minutes'`).Scan(&n)
	return n, err
}
