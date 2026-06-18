// Package analytics is a thin service layer over store's git-analytics
// aggregates. It supplies sane defaults (default range = last 9 months),
// validates and normalises inputs, and computes the derived averages in pure
// Go so they are unit-testable without a database.
//
// Every store call runs inside db.WithOrg so the org_isolation RLS policy
// enforces the tenancy boundary (decisions A2/S1).
package analytics

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// DefaultRangeMonths is the look-back window used when the caller does not
// supply an explicit from/to filter. Mirrors gitrack's "full recent history"
// default while bounding the scan.
const DefaultRangeMonths = 9

// Service wraps the store aggregates with defaults and validation.
type Service struct {
	db *db.DB
}

// New constructs a Service bound to a DB pool.
func New(database *db.DB) *Service {
	return &Service{db: database}
}

// ── Filter parsing & defaulting (pure) ────────────────────────────────────────

// Filter is the validated, defaulted analytics filter the service operates on.
type Filter struct {
	From   time.Time
	To     time.Time
	RepoID string
	Author string
}

// FilterInput is the raw, untrusted filter as received from query parameters.
// All fields are strings exactly as they arrive on the wire.
type FilterInput struct {
	From   string // RFC3339 or YYYY-MM-DD
	To     string // RFC3339 or YYYY-MM-DD
	RepoID string
	Author string
}

// ParseFilter validates and normalises a FilterInput into a Filter, applying the
// default range (last DefaultRangeMonths months ending now) when from/to are
// absent. `now` is injected so the defaulting logic is deterministic in tests.
//
// Rules:
//   - from/to accept RFC3339 timestamps or bare YYYY-MM-DD dates.
//   - An unpar. sable date is an error (callers surface a 400).
//   - When neither from nor to is given, the window is [now-9mo, now].
//   - When only one bound is given, the other defaults (from→9mo earlier; to→now).
//   - If from > to, the bounds are swapped (lenient — the user clearly meant a range).
func ParseFilter(in FilterInput, now time.Time) (Filter, error) {
	var f Filter
	f.RepoID = strings.TrimSpace(in.RepoID)
	f.Author = strings.TrimSpace(in.Author)

	var err error
	if s := strings.TrimSpace(in.From); s != "" {
		if f.From, err = parseDate(s); err != nil {
			return Filter{}, fmt.Errorf("invalid 'from' date %q: %w", s, err)
		}
	}
	if s := strings.TrimSpace(in.To); s != "" {
		if f.To, err = parseDate(s); err != nil {
			return Filter{}, fmt.Errorf("invalid 'to' date %q: %w", s, err)
		}
	}

	f = applyDefaults(f, now)

	// Lenient swap when the user inverted the bounds.
	if f.From.After(f.To) {
		f.From, f.To = f.To, f.From
	}
	return f, nil
}

// applyDefaults fills missing bounds with the default 9-month window.
func applyDefaults(f Filter, now time.Time) Filter {
	switch {
	case f.From.IsZero() && f.To.IsZero():
		f.To = now
		f.From = now.AddDate(0, -DefaultRangeMonths, 0)
	case f.From.IsZero():
		f.From = f.To.AddDate(0, -DefaultRangeMonths, 0)
	case f.To.IsZero():
		f.To = now
	}
	return f
}

// parseDate accepts RFC3339 or YYYY-MM-DD (interpreted as UTC midnight).
func parseDate(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("expected RFC3339 or YYYY-MM-DD")
}

// NormalizeBucket maps an arbitrary bucket string to a supported value.
// Anything other than "week" collapses to "day".
func NormalizeBucket(bucket string) string {
	if strings.EqualFold(strings.TrimSpace(bucket), "week") {
		return "week"
	}
	return "day"
}

// toStoreFilter projects a service Filter onto the store's AnalyticsFilter.
func (f Filter) toStoreFilter() store.AnalyticsFilter {
	return store.AnalyticsFilter{
		From:   f.From,
		To:     f.To,
		RepoID: f.RepoID,
		Author: f.Author,
	}
}

// ── Derived averages (pure) ───────────────────────────────────────────────────

// Averages are the ratios derived from the raw totals. Kept separate from the
// store struct so they can be computed and tested without a database.
type Averages struct {
	CommitsPerActiveDay    float64 `json:"commitsPerActiveDay"`
	CommitsPerContributor  float64 `json:"commitsPerContributor"`
	LinesPerCommit         float64 `json:"linesPerCommit"`
}

// DeriveAverages computes the dashboard averages from raw summary totals,
// guarding against division by zero (returns 0 for a ratio whose denominator
// is 0). lines = additions + deletions (total churn), matching gitrack.
func DeriveAverages(totalCommits, activeDays, contributors int, additions, deletions int64) Averages {
	return Averages{
		CommitsPerActiveDay:   safeDiv(float64(totalCommits), float64(activeDays)),
		CommitsPerContributor: safeDiv(float64(totalCommits), float64(contributors)),
		LinesPerCommit:        safeDiv(float64(additions+deletions), float64(totalCommits)),
	}
}

func safeDiv(num, den float64) float64 {
	if den == 0 {
		return 0
	}
	return num / den
}

// ── Service methods (DB-backed) ───────────────────────────────────────────────

// SummaryResult is the full summary payload: raw totals plus derived averages.
type SummaryResult struct {
	store.AnalyticsSummary
	Averages Averages `json:"averages"`
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
}

// Summary returns totals and averages for the org over the filtered window.
func (s *Service) Summary(ctx context.Context, orgID string, f Filter) (*SummaryResult, error) {
	sf := f.toStoreFilter()
	var sum store.AnalyticsSummary
	if err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		sum, err = sf.Summary(ctx, tx)
		return err
	}); err != nil {
		return nil, fmt.Errorf("analytics.Summary: %w", err)
	}
	return &SummaryResult{
		AnalyticsSummary: sum,
		Averages: DeriveAverages(sum.TotalCommits, sum.ActiveDays, sum.Contributors,
			sum.Additions, sum.Deletions),
		From: f.From,
		To:   f.To,
	}, nil
}

// Heatmap returns commits-per-day over the window.
func (s *Service) Heatmap(ctx context.Context, orgID string, f Filter) ([]store.DayCount, error) {
	sf := f.toStoreFilter()
	var out []store.DayCount
	if err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		out, err = sf.Heatmap(ctx, tx)
		return err
	}); err != nil {
		return nil, fmt.Errorf("analytics.Heatmap: %w", err)
	}
	return out, nil
}

// CommitsOverTime returns commit counts bucketed by day/week over the window.
func (s *Service) CommitsOverTime(ctx context.Context, orgID string, f Filter, bucket string) ([]store.DayCount, error) {
	sf := f.toStoreFilter()
	b := NormalizeBucket(bucket)
	var out []store.DayCount
	if err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		out, err = sf.CommitsOverTime(ctx, tx, b)
		return err
	}); err != nil {
		return nil, fmt.Errorf("analytics.CommitsOverTime: %w", err)
	}
	return out, nil
}

// Contributors returns the leaderboard for the window.
func (s *Service) Contributors(ctx context.Context, orgID string, f Filter) ([]store.Contributor, error) {
	sf := f.toStoreFilter()
	var out []store.Contributor
	if err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		out, err = sf.Contributors(ctx, tx)
		return err
	}); err != nil {
		return nil, fmt.Errorf("analytics.Contributors: %w", err)
	}
	return out, nil
}

// RepoStats returns the per-repo table for the window.
func (s *Service) RepoStats(ctx context.Context, orgID string, f Filter) ([]store.RepoStat, error) {
	sf := f.toStoreFilter()
	var out []store.RepoStat
	if err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		out, err = sf.RepoStats(ctx, tx)
		return err
	}); err != nil {
		return nil, fmt.Errorf("analytics.RepoStats: %w", err)
	}
	return out, nil
}

// CommitsOnDay returns the per-day drill-down. The repo/author filters are
// honoured; the date window of f is ignored (the day bounds replace it).
func (s *Service) CommitsOnDay(ctx context.Context, orgID string, f Filter, day time.Time) ([]store.DayCommit, error) {
	sf := f.toStoreFilter()
	var out []store.DayCommit
	if err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		out, err = sf.CommitsOnDay(ctx, tx, day)
		return err
	}); err != nil {
		return nil, fmt.Errorf("analytics.CommitsOnDay: %w", err)
	}
	return out, nil
}

// ParseDay parses a YYYY-MM-DD path parameter for the drill-down endpoint.
func ParseDay(s string) (time.Time, error) {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid day %q: expected YYYY-MM-DD", s)
	}
	return t.UTC(), nil
}
