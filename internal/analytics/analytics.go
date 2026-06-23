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
	"math"
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

// allTimeFloor is the lower bound for an unbounded ("All time") query — old enough
// to cover any real git history (git dates from ~2005) without admitting year-0001
// stray rows.
var allTimeFloor = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// applyDefaults fills missing bounds. No bounds at all = "All time"; only one bound
// given falls back to the default 9-month window relative to it.
func applyDefaults(f Filter, now time.Time) Filter {
	switch {
	case f.From.IsZero() && f.To.IsZero():
		// The frontend's "All" preset sends empty dates (its other presets send
		// explicit windows). Treat that as all-time, NOT the last 9 months —
		// otherwise "All time" silently hides everything older (e.g. repos to 2020).
		f.To = now
		f.From = allTimeFloor
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
// Supported buckets are "day", "week", and "month"; anything else collapses to
// "day". "month" lets the UI render multi-year ranges (e.g. "All time") without
// thousands of daily points.
func NormalizeBucket(bucket string) string {
	switch strings.ToLower(strings.TrimSpace(bucket)) {
	case "week":
		return "week"
	case "month":
		return "month"
	default:
		return "day"
	}
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
	CommitsPerActiveDay   float64 `json:"commitsPerActiveDay"`
	CommitsPerContributor float64 `json:"commitsPerContributor"`
	LinesPerCommit        float64 `json:"linesPerCommit"`
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

// ── PR / issue / agent derived metrics (pure) ─────────────────────────────────

// Rate returns part/whole as a 0..1 fraction, guarding against a zero whole.
func Rate(part, whole int) float64 {
	return safeDiv(float64(part), float64(whole))
}

// Pct returns part/whole as a 0..100 percentage, rounded to one decimal place,
// guarding against a zero whole. Used for the agent-share headline and merge-rate.
func Pct(part, whole int) float64 {
	if whole == 0 {
		return 0
	}
	return round1(float64(part) / float64(whole) * 100)
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}

// Percentile computes the p-th percentile (0 ≤ p ≤ 100) of the given samples
// using linear interpolation between closest ranks (the "C = 1" / NumPy default
// method). The input is copied and sorted, so the caller's slice is untouched.
// An empty input returns 0.
//
// Used for PR lead-time P50 / P90 (hours). Computing this in Go (rather than SQL
// percentile_cont) keeps it unit-testable without a database and matches the
// dashboard's "lead time" definition exactly.
func Percentile(samples []float64, p float64) float64 {
	n := len(samples)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return samples[0]
	}
	if p <= 0 {
		return minFloat(samples)
	}
	if p >= 100 {
		return maxFloat(samples)
	}
	s := make([]float64, n)
	copy(s, samples)
	sortFloats(s)

	// rank in [0, n-1] using linear interpolation.
	rank := (p / 100) * float64(n-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	if lo == hi {
		return s[lo]
	}
	frac := rank - float64(lo)
	return s[lo] + frac*(s[hi]-s[lo])
}

func minFloat(s []float64) float64 {
	m := s[0]
	for _, v := range s[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

func maxFloat(s []float64) float64 {
	m := s[0]
	for _, v := range s[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

// sortFloats is a tiny insertion sort — sample sets here are small (≤ a few
// hundred PRs per window) so this avoids importing sort for one call site.
func sortFloats(s []float64) {
	for i := 1; i < len(s); i++ {
		v := s[i]
		j := i - 1
		for j >= 0 && s[j] > v {
			s[j+1] = s[j]
			j--
		}
		s[j+1] = v
	}
}

// ── Service methods (DB-backed) ───────────────────────────────────────────────

// SummaryResult is the full summary payload: raw totals plus derived averages.
type SummaryResult struct {
	store.AnalyticsSummary
	Averages Averages  `json:"averages"`
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

// CommitsByContributor returns a per-contributor commit-count timeline for the
// top-N contributors over the window, bucketed by day/week/month. Each series is
// 0-filled across the shared bucket axis so the lines align. topN defaults to 5
// when non-positive; includeOther appends an "Everyone else" aggregate line.
func (s *Service) CommitsByContributor(ctx context.Context, orgID string, f Filter, bucket string, topN int, includeOther bool) ([]store.ContributorSeries, error) {
	sf := f.toStoreFilter()
	b := NormalizeBucket(bucket)
	if topN <= 0 {
		topN = 5
	}
	var out []store.ContributorSeries
	if err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		out, err = sf.CommitsByContributor(ctx, tx, orgID, b, topN, includeOther)
		return err
	}); err != nil {
		return nil, fmt.Errorf("analytics.CommitsByContributor: %w", err)
	}
	return out, nil
}

// Contributors returns the leaderboard for the window.
func (s *Service) Contributors(ctx context.Context, orgID string, f Filter) ([]store.Contributor, error) {
	sf := f.toStoreFilter()
	var out []store.Contributor
	if err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		out, err = sf.Contributors(ctx, tx, orgID)
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

// ── Pull-request analytics (DB-backed) ────────────────────────────────────────

// PRResult is the pull-request analytics payload: state totals, derived
// merge-rate (%), lead-time percentiles (hours), average changed files, and the
// per-day opened/merged throughput series.
type PRResult struct {
	Total           int                  `json:"total"`
	Merged          int                  `json:"merged"`
	Open            int                  `json:"open"`
	Closed          int                  `json:"closed"`
	MergeRate       float64              `json:"mergeRate"` // merged/total, 0..1
	LeadTimeP50Hrs  float64              `json:"leadTimeP50Hours"`
	LeadTimeP90Hrs  float64              `json:"leadTimeP90Hours"`
	AvgChangedFiles float64              `json:"avgChangedFiles"`
	Throughput      []store.PRThroughput `json:"throughput"`
}

// PullRequests returns the pull-request analytics for the org over the window.
func (s *Service) PullRequests(ctx context.Context, orgID string, f Filter) (*PRResult, error) {
	sf := f.toStoreFilter()
	var (
		sum store.PRStats
		tp  []store.PRThroughput
	)
	if err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		if sum, err = sf.PRStats(ctx, tx); err != nil {
			return err
		}
		tp, err = sf.PRThroughput(ctx, tx)
		return err
	}); err != nil {
		return nil, fmt.Errorf("analytics.PullRequests: %w", err)
	}
	if tp == nil {
		tp = []store.PRThroughput{}
	}
	return &PRResult{
		Total:           sum.Total,
		Merged:          sum.Merged,
		Open:            sum.Open,
		Closed:          sum.Closed,
		MergeRate:       Rate(sum.Merged, sum.Total),
		LeadTimeP50Hrs:  round1(Percentile(sum.LeadTimeHours, 50)),
		LeadTimeP90Hrs:  round1(Percentile(sum.LeadTimeHours, 90)),
		AvgChangedFiles: round1(safeDiv(float64(sum.SumChangedFiles), float64(sum.Total))),
		Throughput:      tp,
	}, nil
}

// ── Issue-flow analytics (DB-backed) ──────────────────────────────────────────

// IssueFlowResult is the issue-flow payload: current state breakdown, the
// opened/closed-over-time series, and the per-project open/done split.
type IssueFlowResult struct {
	Open       int                      `json:"open"`
	InProgress int                      `json:"inProgress"`
	Done       int                      `json:"done"`
	Closed     int                      `json:"closed"`
	Opened     []store.DayCount         `json:"opened"`
	ClosedSrs  []store.DayCount         `json:"closedSeries"`
	ByProject  []store.IssueProjectStat `json:"byProject"`
}

// IssueFlow returns the issue-flow analytics for the org over the window.
func (s *Service) IssueFlow(ctx context.Context, orgID string, f Filter) (*IssueFlowResult, error) {
	sf := f.toStoreFilter()
	var (
		counts store.IssueFlowCounts
		opened []store.DayCount
		closed []store.DayCount
		byProj []store.IssueProjectStat
	)
	if err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		if counts, err = sf.IssueFlowCounts(ctx, tx); err != nil {
			return err
		}
		if opened, err = sf.IssuesOpenedOverTime(ctx, tx); err != nil {
			return err
		}
		if closed, err = sf.IssuesClosedOverTime(ctx, tx); err != nil {
			return err
		}
		byProj, err = sf.IssuesByProject(ctx, tx)
		return err
	}); err != nil {
		return nil, fmt.Errorf("analytics.IssueFlow: %w", err)
	}
	if opened == nil {
		opened = []store.DayCount{}
	}
	if closed == nil {
		closed = []store.DayCount{}
	}
	if byProj == nil {
		byProj = []store.IssueProjectStat{}
	}
	return &IssueFlowResult{
		Open:       counts.Open,
		InProgress: counts.InProgress,
		Done:       counts.Done,
		Closed:     counts.Closed,
		Opened:     opened,
		ClosedSrs:  closed,
		ByProject:  byProj,
	}, nil
}

// ── Agent-share analytics (DB-backed) ─────────────────────────────────────────

// AgentShareResult is the agent-vs-human payload: raw counts, derived agentPct
// (%) and the per-day split.
type AgentShareResult struct {
	AgentCommits int              `json:"agentCommits"`
	HumanCommits int              `json:"humanCommits"`
	AgentPct     float64          `json:"agentPct"`
	OverTime     []store.AgentDay `json:"overTime"`
}

// AgentShare returns the agent-vs-human commit analytics for the org over the window.
func (s *Service) AgentShare(ctx context.Context, orgID string, f Filter) (*AgentShareResult, error) {
	sf := f.toStoreFilter()
	var (
		share store.AgentShare
		ot    []store.AgentDay
	)
	if err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		if share, err = sf.AgentShare(ctx, tx); err != nil {
			return err
		}
		ot, err = sf.AgentShareOverTime(ctx, tx)
		return err
	}); err != nil {
		return nil, fmt.Errorf("analytics.AgentShare: %w", err)
	}
	if ot == nil {
		ot = []store.AgentDay{}
	}
	return &AgentShareResult{
		AgentCommits: share.AgentCommits,
		HumanCommits: share.HumanCommits,
		AgentPct:     Pct(share.AgentCommits, share.AgentCommits+share.HumanCommits),
		OverTime:     ot,
	}, nil
}

// ── Per-project analytics (DB-backed) ─────────────────────────────────────────

// Projects returns the per-project table for the org over the window.
func (s *Service) Projects(ctx context.Context, orgID string, f Filter) ([]store.ProjectStat, error) {
	sf := f.toStoreFilter()
	var out []store.ProjectStat
	if err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		out, err = sf.ProjectStats(ctx, tx)
		return err
	}); err != nil {
		return nil, fmt.Errorf("analytics.Projects: %w", err)
	}
	return out, nil
}
