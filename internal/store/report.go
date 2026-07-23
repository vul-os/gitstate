// Package store — report.go
// Read-only helpers that power the reporting dashboard and burndown endpoints.
// All functions run inside db.WithOrg (org-scoped tx) so RLS enforces the
// org boundary (decisions A2/S1). No writes happen here.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ── Dashboard rollup ──────────────────────────────────────────────────────────

// IssueStateCounts holds how many issues are in each canonical state for an org.
type IssueStateCounts struct {
	Open       int `json:"open"`
	InProgress int `json:"inProgress"`
	Done       int `json:"done"`
	Closed     int `json:"closed"`
}

// IssueStateRollup returns a per-state count of issues for the org.
// Must run inside a db.WithOrg transaction so RLS is active.
func IssueStateRollup(ctx context.Context, tx pgx.Tx, orgID string) (IssueStateCounts, error) {
	const q = `
		SELECT
			COUNT(*) FILTER (WHERE COALESCE(derived_state, state) = 'open')       AS open,
			COUNT(*) FILTER (WHERE COALESCE(derived_state, state) = 'in_progress') AS in_progress,
			COUNT(*) FILTER (WHERE COALESCE(derived_state, state) = 'done')        AS done,
			COUNT(*) FILTER (WHERE COALESCE(derived_state, state) = 'closed')      AS closed
		FROM issues
		WHERE org_id = $1`

	var c IssueStateCounts
	err := tx.QueryRow(ctx, q, orgID).Scan(&c.Open, &c.InProgress, &c.Done, &c.Closed)
	if err != nil {
		return IssueStateCounts{}, fmt.Errorf("store: issue state rollup: %w", err)
	}
	return c, nil
}

// ThroughputPoint is the number of issues closed/done in a calendar week.
type ThroughputPoint struct {
	WeekStart time.Time `json:"weekStart"`
	Count     int       `json:"count"`
}

// WeeklyThroughput returns the count of issues moved to done/closed per week for
// the last n weeks. Must run inside a db.WithOrg transaction.
func WeeklyThroughput(ctx context.Context, tx pgx.Tx, orgID string, weeks int) ([]ThroughputPoint, error) {
	if weeks <= 0 {
		weeks = 12
	}
	const q = `
		SELECT
			date_trunc('week', updated_at)::date AS week_start,
			COUNT(*)
		FROM issues
		WHERE org_id = $1
		  AND COALESCE(derived_state, state) IN ('done', 'closed')
		  AND updated_at >= now() - make_interval(weeks => $2)
		GROUP BY 1
		ORDER BY 1`

	rows, err := tx.Query(ctx, q, orgID, weeks)
	if err != nil {
		return nil, fmt.Errorf("store: weekly throughput: %w", err)
	}
	defer rows.Close()

	var out []ThroughputPoint
	for rows.Next() {
		var p ThroughputPoint
		if err := rows.Scan(&p.WeekStart, &p.Count); err != nil {
			return nil, fmt.Errorf("store: scan throughput point: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: weekly throughput rows: %w", err)
	}
	return out, nil
}

// RecentActivityItem is a lightweight summary of recent PR/issue/commit activity
// used both for the dashboard feed and as input to llm.SynthesizeStatus.
type RecentActivityItem struct {
	Kind      string // "pr" | "issue" | "commit"
	Title     string
	Author    string
	State     string
	UpdatedAt time.Time `json:"updatedAt"`
}

// RecentActivity returns the last limit activity items (PRs, issues, commits)
// for the org, ordered newest-first. Must run inside a db.WithOrg transaction.
func RecentActivity(ctx context.Context, tx pgx.Tx, orgID string, limit int) ([]RecentActivityItem, error) {
	if limit <= 0 {
		limit = 20
	}
	// Union across PRs, issues, and commits. All three tables carry org_id and
	// are protected by the org_isolation RLS policy.
	const q = `
		(
			SELECT 'pr'::text AS kind,
			       COALESCE(title, '') AS title,
			       COALESCE(author_login, '') AS author,
			       COALESCE(state, '') AS state,
			       created_at AS updated_at
			FROM pull_requests
			WHERE org_id = $1
		)
		UNION ALL
		(
			SELECT 'issue' AS kind,
			       title,
			       '' AS author,
			       COALESCE(derived_state, state) AS state,
			       updated_at
			FROM issues
			WHERE org_id = $1
		)
		UNION ALL
		(
			SELECT 'commit' AS kind,
			       COALESCE(message, '') AS title,
			       COALESCE(author_login, '') AS author,
			       '' AS state,
			       COALESCE(committed_at, now()) AS updated_at
			FROM commits
			WHERE org_id = $1
		)
		ORDER BY updated_at DESC
		LIMIT $2`

	rows, err := tx.Query(ctx, q, orgID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: recent activity: %w", err)
	}
	defer rows.Close()

	var out []RecentActivityItem
	for rows.Next() {
		var a RecentActivityItem
		if err := rows.Scan(&a.Kind, &a.Title, &a.Author, &a.State, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("store: scan activity item: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: recent activity rows: %w", err)
	}
	return out, nil
}

// ── Burndown series ───────────────────────────────────────────────────────────

// BurndownPoint is the count of open issues at a given date.
type BurndownPoint struct {
	Date  time.Time `json:"date"`
	Open  int       `json:"open"`
	Total int       `json:"total"`
}

// BurndownSeries returns a daily burndown series for a project over the last 30
// days. If projectID is empty it covers the whole org. Must run inside a
// db.WithOrg transaction.
//
// The "open" count is the number of issues that were NOT done/closed as of that
// date, approximated by using updated_at. A proper point-in-time snapshot would
// require a history table; this is the best-effort version from the live schema.
func BurndownSeries(ctx context.Context, tx pgx.Tx, orgID, projectID string, days int) ([]BurndownPoint, error) {
	if days <= 0 {
		days = 30
	}

	// Build day series. For each day in the window we count issues created on or
	// before that day (total) and issues not yet done/closed by that day (open),
	// using updated_at as a proxy for state-change time.
	baseQ := `
		WITH days AS (
			SELECT generate_series(
				(now() - make_interval(days => $2))::date,
				now()::date,
				'1 day'::interval
			)::date AS day
		),
		issues_base AS (
			SELECT
				created_at::date   AS created_day,
				updated_at::date   AS updated_day,
				COALESCE(derived_state, state) AS effective_state
			FROM issues
			WHERE org_id = $1`

	args := []any{orgID, days}

	if projectID != "" {
		args = append(args, projectID)
		baseQ += fmt.Sprintf(`
			  AND project_id = $%d`, len(args))
	}

	baseQ += `
		)
		SELECT
			d.day,
			COUNT(i.created_day) FILTER (WHERE i.created_day <= d.day)          AS total,
			COUNT(i.created_day) FILTER (
				WHERE i.created_day <= d.day
				  AND NOT (i.effective_state IN ('done','closed') AND i.updated_day <= d.day)
			) AS open
		FROM days d
		LEFT JOIN issues_base i ON true
		GROUP BY d.day
		ORDER BY d.day`

	rows, err := tx.Query(ctx, baseQ, args...)
	if err != nil {
		return nil, fmt.Errorf("store: burndown series: %w", err)
	}
	defer rows.Close()

	var out []BurndownPoint
	for rows.Next() {
		var p BurndownPoint
		if err := rows.Scan(&p.Date, &p.Total, &p.Open); err != nil {
			return nil, fmt.Errorf("store: scan burndown point: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: burndown series rows: %w", err)
	}
	return out, nil
}
