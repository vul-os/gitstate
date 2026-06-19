// Package notifications builds evidence-based status digests from the gitstate
// data already captured by the tracker, and delivers them to where people work:
// Slack, a generic webhook, or email.
//
// The package is split into three concerns:
//
//   - digest.go    — the digest data model + builders. Builders are pure-ish:
//     they read org-scoped data (inside db.WithOrg, so RLS enforces tenancy) and
//     assemble a structured Digest. They never deliver anything.
//   - render.go    — renders a Digest to both a Slack Blocks payload (also valid
//     for a generic webhook) and a plain-text body.
//   - deliver.go   — delivery: HTTP POST for slack/webhook, SMTP for email (only
//     when configured), preview (render without sending), and test sends.
//
// Digest kinds:
//
//	weeklyStatus — shipped/merged this week, throughput, top movement.
//	stalePRs     — open PRs older than N days.
//	ooo          — approved leave overlapping the upcoming week.
package notifications

import (
	"context"
	"fmt"
	"time"

	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// Kind identifies a digest type. These string values are the public API
// (query param ?kind=, channel.digests keys, log rows).
const (
	KindWeeklyStatus = "weeklyStatus"
	KindStalePRs     = "stalePRs"
	KindOOO          = "ooo"
)

// ValidKind reports whether k is a recognised digest kind.
func ValidKind(k string) bool {
	switch k {
	case KindWeeklyStatus, KindStalePRs, KindOOO:
		return true
	}
	return false
}

// staleThresholdDays is how old (in days) an open PR must be to count as stale.
const staleThresholdDays = 7

// Digest is a fully-built, structured status report ready to render. It is the
// single shape every builder produces and every renderer consumes.
type Digest struct {
	Kind        string    `json:"kind"`
	Title       string    `json:"title"`
	Subtitle    string    `json:"subtitle"`
	GeneratedAt time.Time `json:"generatedAt"`
	PeriodFrom  time.Time `json:"periodFrom,omitempty"`
	PeriodTo    time.Time `json:"periodTo,omitempty"`
	Sections    []Section `json:"sections"`
	Empty       bool      `json:"empty"`
	EmptyReason string    `json:"emptyReason,omitempty"`
	Metrics     []Metric  `json:"metrics,omitempty"`
}

// Metric is a single headline number (shipped, merged, …) shown as a stat.
type Metric struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Section is a titled list of lines, each line being one piece of evidence.
type Section struct {
	Heading string `json:"heading"`
	Lines   []Line `json:"lines"`
}

// Line is one evidence row: a primary text plus optional meta (author, age, …).
type Line struct {
	Text string `json:"text"`
	Meta string `json:"meta,omitempty"`
}

// Builder assembles digests for an org from stored data. Reads always run inside
// db.WithOrg so RLS enforces the org boundary.
type Builder struct {
	db *db.DB
}

// NewBuilder constructs a Builder backed by the given database.
func NewBuilder(database *db.DB) *Builder {
	return &Builder{db: database}
}

// Build dispatches to the correct builder for kind. Returns an error for an
// unknown kind.
func (b *Builder) Build(ctx context.Context, orgID, kind string) (*Digest, error) {
	switch kind {
	case KindWeeklyStatus:
		return b.WeeklyStatus(ctx, orgID)
	case KindStalePRs:
		return b.StalePRs(ctx, orgID)
	case KindOOO:
		return b.OOO(ctx, orgID)
	default:
		return nil, fmt.Errorf("notifications: unknown digest kind %q", kind)
	}
}

// startOfWeek returns the Monday 00:00 UTC of t's ISO week.
func startOfWeek(t time.Time) time.Time {
	t = t.UTC()
	// Go's Weekday: Sunday=0 … Saturday=6. Shift so Monday=0.
	offset := (int(t.Weekday()) + 6) % 7
	d := t.AddDate(0, 0, -offset)
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
}

// WeeklyStatus builds the weekly status digest: what shipped/merged this week,
// throughput, and the most recent movement. Evidence comes from the same stores
// that power the dashboard rollup.
func (b *Builder) WeeklyStatus(ctx context.Context, orgID string) (*Digest, error) {
	now := time.Now().UTC()
	from := startOfWeek(now)
	to := from.AddDate(0, 0, 7)

	d := &Digest{
		Kind:        KindWeeklyStatus,
		Title:       "Weekly status",
		Subtitle:    fmt.Sprintf("Week of %s", from.Format("Jan 2, 2006")),
		GeneratedAt: now,
		PeriodFrom:  from,
		PeriodTo:    to,
	}

	err := b.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		shipped, err := store.WeeklyShippedCounts(ctx, tx, orgID, from, to)
		if err != nil {
			return err
		}
		d.Metrics = []Metric{
			{Label: "Issues shipped", Value: fmt.Sprintf("%d", shipped.IssuesDone)},
			{Label: "PRs merged", Value: fmt.Sprintf("%d", shipped.PRsMerged)},
			{Label: "Commits", Value: fmt.Sprintf("%d", shipped.CommitsTotal)},
		}

		// State rollup for an "open work" snapshot.
		counts, err := store.IssueStateRollup(ctx, tx, orgID)
		if err != nil {
			return err
		}
		statusSection := Section{Heading: "Open work"}
		statusSection.Lines = append(statusSection.Lines,
			Line{Text: fmt.Sprintf("%d open · %d in progress · %d done", counts.Open, counts.InProgress, counts.Done)},
		)
		d.Sections = append(d.Sections, statusSection)

		// Throughput over the last 6 weeks for trend context.
		tp, err := store.WeeklyThroughput(ctx, tx, orgID, 6)
		if err != nil {
			return err
		}
		if len(tp) > 0 {
			trend := Section{Heading: "Throughput (issues done / week)"}
			for _, p := range tp {
				trend.Lines = append(trend.Lines, Line{
					Text: p.WeekStart.Format("Jan 2"),
					Meta: fmt.Sprintf("%d", p.Count),
				})
			}
			d.Sections = append(d.Sections, trend)
		}

		// Top recent movement (latest activity) as concrete evidence.
		acts, err := store.RecentActivity(ctx, tx, orgID, 8)
		if err != nil {
			return err
		}
		if len(acts) > 0 {
			mv := Section{Heading: "Recent movement"}
			for _, a := range acts {
				meta := a.Kind
				if a.Author != "" {
					meta = a.Kind + " · " + a.Author
				}
				if a.State != "" {
					meta += " · " + a.State
				}
				mv.Lines = append(mv.Lines, Line{Text: truncate(a.Title, 90), Meta: meta})
			}
			d.Sections = append(d.Sections, mv)
		}

		// "Empty" if literally nothing happened and there is no open work either.
		if shipped.IssuesDone == 0 && shipped.PRsMerged == 0 && shipped.CommitsTotal == 0 &&
			counts.Open == 0 && counts.InProgress == 0 && len(acts) == 0 {
			d.Empty = true
			d.EmptyReason = "No tracked activity yet. Connect a repository and sync to populate the weekly status."
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("notifications.WeeklyStatus: %w", err)
	}
	return d, nil
}

// StalePRs builds the stale/blocked-PR digest: open PRs older than the
// threshold, oldest first.
func (b *Builder) StalePRs(ctx context.Context, orgID string) (*Digest, error) {
	now := time.Now().UTC()
	d := &Digest{
		Kind:        KindStalePRs,
		Title:       "Stale & blocked PRs",
		Subtitle:    fmt.Sprintf("Open PRs older than %d days", staleThresholdDays),
		GeneratedAt: now,
	}

	err := b.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		prs, err := store.ListStalePRs(ctx, tx, orgID, staleThresholdDays)
		if err != nil {
			return err
		}
		if len(prs) == 0 {
			d.Empty = true
			d.EmptyReason = fmt.Sprintf("No open PRs older than %d days. Nothing is blocked.", staleThresholdDays)
			return nil
		}
		d.Metrics = []Metric{{Label: "Stale PRs", Value: fmt.Sprintf("%d", len(prs))}}
		sec := Section{Heading: "Needs attention"}
		for _, p := range prs {
			title := p.Title
			if title == "" {
				title = fmt.Sprintf("PR #%d", p.Number)
			}
			loc := p.RepoName
			if p.Number > 0 {
				loc = fmt.Sprintf("%s #%d", p.RepoName, p.Number)
			}
			meta := fmt.Sprintf("%d days old", p.AgeDays)
			if p.AuthorLogin != "" {
				meta += " · " + p.AuthorLogin
			}
			if loc != "" && loc != " " {
				meta += " · " + loc
			}
			sec.Lines = append(sec.Lines, Line{Text: truncate(title, 90), Meta: meta})
		}
		d.Sections = append(d.Sections, sec)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("notifications.StalePRs: %w", err)
	}
	return d, nil
}

// OOO builds the who's-out digest: approved leave overlapping the upcoming week.
func (b *Builder) OOO(ctx context.Context, orgID string) (*Digest, error) {
	now := time.Now().UTC()
	// Upcoming week = next Monday through the following Sunday. If today is mid-
	// week, "upcoming" still starts at the next Monday so the digest looks ahead.
	from := startOfWeek(now).AddDate(0, 0, 7)
	to := from.AddDate(0, 0, 7)

	d := &Digest{
		Kind:        KindOOO,
		Title:       "Who's out next week",
		Subtitle:    fmt.Sprintf("%s – %s", from.Format("Jan 2"), to.AddDate(0, 0, -1).Format("Jan 2")),
		GeneratedAt: now,
		PeriodFrom:  from,
		PeriodTo:    to,
	}

	err := b.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		entries, err := store.ListOOOInPeriod(ctx, tx, orgID, from, to)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			d.Empty = true
			d.EmptyReason = "No approved leave overlaps next week. Full team availability."
			return nil
		}
		d.Metrics = []Metric{{Label: "People out", Value: fmt.Sprintf("%d", countDistinctPeople(entries))}}
		sec := Section{Heading: "Approved leave"}
		for _, e := range entries {
			name := e.Name
			if name == "" {
				name = e.Email
			}
			span := formatLeaveSpan(e)
			meta := e.Kind
			if e.HalfDay {
				meta += " · half day"
			}
			sec.Lines = append(sec.Lines, Line{Text: fmt.Sprintf("%s — %s", name, span), Meta: meta})
		}
		d.Sections = append(d.Sections, sec)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("notifications.OOO: %w", err)
	}
	return d, nil
}

func countDistinctPeople(entries []*store.OOOEntry) int {
	seen := map[string]struct{}{}
	for _, e := range entries {
		seen[e.UserID] = struct{}{}
	}
	return len(seen)
}

// formatLeaveSpan renders a leave entry's date span compactly.
func formatLeaveSpan(e *store.OOOEntry) string {
	start := e.StartDate.UTC()
	end := e.EndDate.UTC()
	if start.Year() == end.Year() && start.YearDay() == end.YearDay() {
		return start.Format("Mon Jan 2")
	}
	return fmt.Sprintf("%s – %s", start.Format("Mon Jan 2"), end.Format("Mon Jan 2"))
}

// truncate shortens s to at most n runes, appending an ellipsis when cut.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
