// Package store — analytics_events_test.go
// Integration round-trip for the global analytics_events write/read path:
// InsertAnalyticsEvent → the super-admin aggregates (by-country, by-kind,
// kind-by-day, recent feed, online-now). Skips cleanly when DATABASE_URL is
// unset. The table is global (no RLS), so no org context is needed; the test
// tags its rows with a unique ip_hash and deletes them at the end so the shared
// DB stays clean.
package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAnalyticsEventRoundTrip(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping analytics_events integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	// Unique marker so we can find + clean up exactly our rows.
	marker := fmt.Sprintf("test-hash-%d", time.Now().UnixNano())
	defer func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM analytics_events WHERE ip_hash = $1`, marker)
	}()

	// Insert a small, known set: 2 signups + 1 login, all from ZA.
	events := []AnalyticsEvent{
		{Kind: "signup", Path: "/signup", Method: "POST", Status: 200, IPHash: marker, Country: "ZA", Region: "Gauteng", City: "Johannesburg", UserAgent: "test-agent"},
		{Kind: "signup", Path: "/signup", Method: "POST", Status: 200, IPHash: marker, Country: "ZA", Region: "Gauteng", City: "Johannesburg"},
		{Kind: "login", Path: "/login", Method: "POST", Status: 200, IPHash: marker, Country: "ZA", Region: "Western Cape", City: "Cape Town"},
	}
	for i, e := range events {
		if err := InsertAnalyticsEvent(ctx, pool, e); err != nil {
			t.Fatalf("InsertAnalyticsEvent[%d]: %v", i, err)
		}
	}

	// By-kind aggregate must include our 2 signups + 1 login (counts may be
	// higher if the shared DB has other recent rows — assert "at least").
	kinds, err := AnalyticsCountsByKind(ctx, pool, 1)
	if err != nil {
		t.Fatalf("AnalyticsCountsByKind: %v", err)
	}
	if got := kindCount(kinds, "signup"); got < 2 {
		t.Errorf("signup count = %d, want >= 2", got)
	}
	if got := kindCount(kinds, "login"); got < 1 {
		t.Errorf("login count = %d, want >= 1", got)
	}

	// By-country must include ZA with >= 3.
	countries, err := AnalyticsEventsByCountry(ctx, pool, 1, 50)
	if err != nil {
		t.Fatalf("AnalyticsEventsByCountry: %v", err)
	}
	if got := countryCount(countries, "ZA"); got < 3 {
		t.Errorf("ZA country count = %d, want >= 3", got)
	}

	// kind-by-day for signup must have at least 2 today.
	days, err := AnalyticsKindByDay(ctx, pool, "signup", 1)
	if err != nil {
		t.Fatalf("AnalyticsKindByDay: %v", err)
	}
	var signupToday int
	for _, d := range days {
		signupToday += d.Count
	}
	if signupToday < 2 {
		t.Errorf("signup-by-day total = %d, want >= 2", signupToday)
	}

	// recent feed must surface one of our rows with geo intact.
	recent, err := AnalyticsRecentEvents(ctx, pool, 100)
	if err != nil {
		t.Fatalf("AnalyticsRecentEvents: %v", err)
	}
	var foundGeo bool
	for _, e := range recent {
		if e.Country == "ZA" && e.City == "Johannesburg" {
			foundGeo = true
			break
		}
	}
	if !foundGeo {
		t.Error("recent feed did not surface our ZA/Johannesburg event with geo intact")
	}

	// online-now counts distinct ip_hash in the last 5 min — our marker is one.
	online, err := AnalyticsOnlineNow(ctx, pool)
	if err != nil {
		t.Fatalf("AnalyticsOnlineNow: %v", err)
	}
	if online < 1 {
		t.Errorf("online-now = %d, want >= 1 (our marker hash)", online)
	}
}

func kindCount(rows []KindCount, kind string) int {
	for _, r := range rows {
		if r.Kind == kind {
			return r.Count
		}
	}
	return 0
}

func countryCount(rows []CountryCount, cc string) int {
	for _, r := range rows {
		if r.Country == cc {
			return r.Count
		}
	}
	return 0
}
