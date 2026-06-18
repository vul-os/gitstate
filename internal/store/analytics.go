// Package store — analytics.go
// Org-scoped read-only aggregates over the commits table (with joins to repos /
// projects) that power the git-analytics dashboard: totals & averages, the
// GitHub-style contribution heatmap, commits-over-time, the contributor
// leaderboard, the per-repo table, and the per-day drill-down.
//
// Every function runs inside a db.WithOrg transaction so the org_isolation RLS
// policy enforces the tenancy boundary (decisions A2/S1). The org_id is never
// interpolated into SQL — RLS handles isolation — and all user-supplied filters
// are passed as bind parameters ($N), so the queries are injection-safe.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// AnalyticsFilter narrows every analytics query to a date window and optional
// repo / author. A zero From or To means "unbounded on that side"; the analytics
// Service supplies sane defaults (last 9 months) before calling into the store.
//
//   - From / To filter on commits.committed_at (inclusive lower, exclusive upper).
//   - RepoID filters on commits.repo_id (a UUID string) when non-empty.
//   - Author matches EITHER author_login OR author_email (case-insensitive),
//     so a caller can pass a login or an email transparently.
type AnalyticsFilter struct {
	From   time.Time
	To     time.Time
	RepoID string
	Author string
}

// whereClause builds the parameterised WHERE fragment shared by every analytics
// query. It always begins with the org-scoped table alias `c` for commits.
// startIdx is the next free positional-parameter index (1-based). It returns the
// SQL fragment (without a leading WHERE/AND keyword — callers prepend), the
// ordered args, and the next free index.
func (f AnalyticsFilter) whereClause(startIdx int) (string, []any, int) {
	var (
		clause string
		args   []any
		idx    = startIdx
	)
	if !f.From.IsZero() {
		clause += fmt.Sprintf(" AND c.committed_at >= $%d", idx)
		args = append(args, f.From)
		idx++
	}
	if !f.To.IsZero() {
		clause += fmt.Sprintf(" AND c.committed_at < $%d", idx)
		args = append(args, f.To)
		idx++
	}
	if f.RepoID != "" {
		clause += fmt.Sprintf(" AND c.repo_id = $%d", idx)
		args = append(args, f.RepoID)
		idx++
	}
	if f.Author != "" {
		// Match login or email, case-insensitive. author_email is citext so the
		// equality is already case-insensitive there; lower() handles author_login.
		clause += fmt.Sprintf(" AND (lower(COALESCE(c.author_login,'')) = lower($%d) OR c.author_email = $%d)", idx, idx)
		args = append(args, f.Author)
		idx++
	}
	return clause, args, idx
}

// ── Summary ─────────────────────────────────────────────────────────────────

// AnalyticsSummary holds the headline totals and averages for the dashboard.
// The averages are computed in Go (analytics.Derive*) from the raw totals so
// they stay unit-testable without a database; the store returns only raw counts.
type AnalyticsSummary struct {
	TotalCommits int   `json:"totalCommits"`
	Repos        int   `json:"repos"`
	Contributors int   `json:"contributors"`
	ActiveDays   int   `json:"activeDays"`
	Additions    int64 `json:"additions"`
	Deletions    int64 `json:"deletions"`
	NetLines     int64 `json:"netLines"`
}

// Summary returns the raw totals over the filtered commit set. The derived
// averages (commits/active-day, commits/contributor, lines/commit) are computed
// by the analytics package from these fields.
func (f AnalyticsFilter) Summary(ctx context.Context, tx pgx.Tx) (AnalyticsSummary, error) {
	where, args, _ := f.whereClause(1)
	q := `
		SELECT
			COUNT(*)                                              AS total_commits,
			COUNT(DISTINCT c.repo_id)                             AS repos,
			COUNT(DISTINCT COALESCE(NULLIF(lower(c.author_email::text),''),
			                        NULLIF(lower(c.author_login),''))) AS contributors,
			COUNT(DISTINCT c.committed_at::date)                  AS active_days,
			COALESCE(SUM(c.additions), 0)                         AS additions,
			COALESCE(SUM(c.deletions), 0)                         AS deletions
		FROM commits c
		WHERE true` + where

	var s AnalyticsSummary
	err := tx.QueryRow(ctx, q, args...).Scan(
		&s.TotalCommits, &s.Repos, &s.Contributors, &s.ActiveDays,
		&s.Additions, &s.Deletions,
	)
	if err != nil {
		return AnalyticsSummary{}, fmt.Errorf("store: analytics summary: %w", err)
	}
	s.NetLines = s.Additions - s.Deletions
	return s, nil
}

// ── Heatmap ─────────────────────────────────────────────────────────────────

// DayCount is one calendar day and its commit count. Used for both the heatmap
// and the day-bucketed commits-over-time series.
type DayCount struct {
	Date  time.Time `json:"date"`
	Count int       `json:"count"`
}

// Heatmap returns commits-per-calendar-day over the filtered range, ordered by
// date ascending. Only days with at least one commit appear (the frontend fills
// the gaps for the GitHub-style calendar grid).
func (f AnalyticsFilter) Heatmap(ctx context.Context, tx pgx.Tx) ([]DayCount, error) {
	where, args, _ := f.whereClause(1)
	q := `
		SELECT c.committed_at::date AS day, COUNT(*) AS n
		FROM commits c
		WHERE true` + where + `
		GROUP BY 1
		ORDER BY 1`
	return scanDayCounts(ctx, tx, "analytics heatmap", q, args)
}

// ── CommitsOverTime ─────────────────────────────────────────────────────────

// CommitsOverTime returns commit counts bucketed by day or week over the range,
// ordered ascending. bucket must be "day" or "week"; any other value defaults to
// "day". The bucket date is the start of the period (date_trunc).
func (f AnalyticsFilter) CommitsOverTime(ctx context.Context, tx pgx.Tx, bucket string) ([]DayCount, error) {
	trunc := "day"
	if bucket == "week" {
		trunc = "week"
	}
	where, args, _ := f.whereClause(1)
	q := fmt.Sprintf(`
		SELECT date_trunc('%s', c.committed_at)::date AS bucket, COUNT(*) AS n
		FROM commits c
		WHERE true%s
		GROUP BY 1
		ORDER BY 1`, trunc, where)
	return scanDayCounts(ctx, tx, "analytics commits-over-time", q, args)
}

func scanDayCounts(ctx context.Context, tx pgx.Tx, label, q string, args []any) ([]DayCount, error) {
	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: %s: %w", label, err)
	}
	defer rows.Close()

	var out []DayCount
	for rows.Next() {
		var d DayCount
		if err := rows.Scan(&d.Date, &d.Count); err != nil {
			return nil, fmt.Errorf("store: scan %s row: %w", label, err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %s rows: %w", label, err)
	}
	return out, nil
}

// ── Contributors leaderboard ──────────────────────────────────────────────────

// Contributor is one row of the leaderboard, ranked by commit count. Authors are
// merged by email (falling back to login when email is absent), mirroring
// gitrack's identity-merge behaviour.
type Contributor struct {
	Login      string    `json:"login"`
	Name       string    `json:"name"`
	Email      string    `json:"email"`
	Commits    int       `json:"commits"`
	Additions  int64     `json:"additions"`
	Deletions  int64     `json:"deletions"`
	ActiveDays int       `json:"activeDays"`
	Projects   int       `json:"projects"`
	FirstAt    time.Time `json:"firstAt"`
	LastAt     time.Time `json:"lastAt"`
	IsAgent    bool      `json:"isAgent"`
}

// Contributors returns the leaderboard for the filtered commit set, ranked by
// commit count descending (ties broken by additions). Identities are merged by
// email; the login/name shown is that of the most recent commit for the identity.
// Projects counts distinct projects touched (via repo→project linkage through
// commits.repo_id; with the current schema repos are the project proxy, so this
// is the distinct repo count attributed to the contributor).
func (f AnalyticsFilter) Contributors(ctx context.Context, tx pgx.Tx) ([]Contributor, error) {
	where, args, _ := f.whereClause(1)
	// identity = lowercased email when present, else lowercased login. We pick
	// a representative login/name/is_agent via the latest commit per identity.
	q := `
		WITH scoped AS (
			SELECT
				COALESCE(NULLIF(lower(c.author_email::text),''),
				         NULLIF(lower(c.author_login),'')) AS identity,
				c.author_login,
				c.author_email,
				c.is_agent,
				c.additions,
				c.deletions,
				c.committed_at,
				c.repo_id
			FROM commits c
			WHERE true` + where + `
		),
		agg AS (
			SELECT
				identity,
				COUNT(*)                              AS commits,
				COALESCE(SUM(additions),0)            AS additions,
				COALESCE(SUM(deletions),0)            AS deletions,
				COUNT(DISTINCT committed_at::date)    AS active_days,
				COUNT(DISTINCT repo_id)               AS projects,
				MIN(committed_at)                     AS first_at,
				MAX(committed_at)                     AS last_at
			FROM scoped
			WHERE identity IS NOT NULL
			GROUP BY identity
		),
		latest AS (
			SELECT DISTINCT ON (identity)
				identity,
				COALESCE(author_login,'') AS login,
				COALESCE(author_email::text,'') AS email,
				is_agent
			FROM scoped
			WHERE identity IS NOT NULL
			ORDER BY identity, committed_at DESC
		)
		SELECT
			l.login, l.email, a.commits, a.additions, a.deletions,
			a.active_days, a.projects, a.first_at, a.last_at, l.is_agent
		FROM agg a
		JOIN latest l USING (identity)
		ORDER BY a.commits DESC, a.additions DESC, l.login ASC`

	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: analytics contributors: %w", err)
	}
	defer rows.Close()

	var out []Contributor
	for rows.Next() {
		var c Contributor
		if err := rows.Scan(
			&c.Login, &c.Email, &c.Commits, &c.Additions, &c.Deletions,
			&c.ActiveDays, &c.Projects, &c.FirstAt, &c.LastAt, &c.IsAgent,
		); err != nil {
			return nil, fmt.Errorf("store: scan contributor: %w", err)
		}
		// Name falls back to login when no display name is recorded.
		c.Name = c.Login
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: analytics contributors rows: %w", err)
	}
	return out, nil
}

// ── Per-repo table ────────────────────────────────────────────────────────────

// RepoStat is one row of the per-repo table.
type RepoStat struct {
	RepoID       string    `json:"repoId"`
	FullName     string    `json:"fullName"`
	Commits      int       `json:"commits"`
	Contributors int       `json:"contributors"`
	Additions    int64     `json:"additions"`
	Deletions    int64     `json:"deletions"`
	LastActivity time.Time `json:"lastActivity"`
}

// RepoStats returns per-repo aggregates over the filtered commit set, ordered by
// commit count descending. Joined to repos for the human-readable full_name.
func (f AnalyticsFilter) RepoStats(ctx context.Context, tx pgx.Tx) ([]RepoStat, error) {
	where, args, _ := f.whereClause(1)
	q := `
		SELECT
			c.repo_id,
			COALESCE(r.full_name, '')                          AS full_name,
			COUNT(*)                                           AS commits,
			COUNT(DISTINCT COALESCE(NULLIF(lower(c.author_email::text),''),
			                        NULLIF(lower(c.author_login),''))) AS contributors,
			COALESCE(SUM(c.additions),0)                       AS additions,
			COALESCE(SUM(c.deletions),0)                       AS deletions,
			MAX(c.committed_at)                                AS last_activity
		FROM commits c
		LEFT JOIN repos r ON r.id = c.repo_id
		WHERE true` + where + `
		GROUP BY c.repo_id, r.full_name
		ORDER BY commits DESC, full_name ASC`

	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: analytics repo stats: %w", err)
	}
	defer rows.Close()

	var out []RepoStat
	for rows.Next() {
		var s RepoStat
		if err := rows.Scan(
			&s.RepoID, &s.FullName, &s.Commits, &s.Contributors,
			&s.Additions, &s.Deletions, &s.LastActivity,
		); err != nil {
			return nil, fmt.Errorf("store: scan repo stat: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: analytics repo stats rows: %w", err)
	}
	return out, nil
}

// ── Day drill-down ────────────────────────────────────────────────────────────

// DayCommit is one commit in the per-day drill-down list.
type DayCommit struct {
	SHA          string    `json:"sha"`
	AuthorLogin  string    `json:"authorLogin"`
	RepoFullName string    `json:"repoFullName"`
	Message      string    `json:"message"`
	Additions    int       `json:"additions"`
	Deletions    int       `json:"deletions"`
	CommittedAt  time.Time `json:"committedAt"`
}

// CommitsOnDay returns every commit made on the given calendar day (in UTC),
// honouring the repo/author filters, ordered newest-first. day is truncated to
// its date; the half-open window [day, day+1) is matched.
func (f AnalyticsFilter) CommitsOnDay(ctx context.Context, tx pgx.Tx, day time.Time) ([]DayCommit, error) {
	d := day.UTC().Truncate(24 * time.Hour)
	next := d.Add(24 * time.Hour)

	// Build filter args but ignore the From/To window — the day bounds replace it.
	dayFilter := AnalyticsFilter{RepoID: f.RepoID, Author: f.Author}
	where, args, _ := dayFilter.whereClause(3) // $1, $2 reserved for the day bounds
	q := `
		SELECT
			c.sha,
			COALESCE(c.author_login,'')   AS author_login,
			COALESCE(r.full_name,'')      AS repo_full_name,
			COALESCE(c.message,'')        AS message,
			COALESCE(c.additions,0)       AS additions,
			COALESCE(c.deletions,0)       AS deletions,
			COALESCE(c.committed_at, now()) AS committed_at
		FROM commits c
		LEFT JOIN repos r ON r.id = c.repo_id
		WHERE c.committed_at >= $1 AND c.committed_at < $2` + where + `
		ORDER BY c.committed_at DESC`

	allArgs := append([]any{d, next}, args...)
	rows, err := tx.Query(ctx, q, allArgs...)
	if err != nil {
		return nil, fmt.Errorf("store: analytics commits on day: %w", err)
	}
	defer rows.Close()

	var out []DayCommit
	for rows.Next() {
		var dc DayCommit
		if err := rows.Scan(
			&dc.SHA, &dc.AuthorLogin, &dc.RepoFullName, &dc.Message,
			&dc.Additions, &dc.Deletions, &dc.CommittedAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan day commit: %w", err)
		}
		out = append(out, dc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: analytics commits on day rows: %w", err)
	}
	return out, nil
}
