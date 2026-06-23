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
	"sort"
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

// CommitsOverTime returns commit counts bucketed by day, week, or month over the
// range, ordered ascending. bucket must be "day", "week", or "month"; any other
// value defaults to "day". The bucket date is the start of the period
// (date_trunc). Month bucketing keeps very wide ("All time") ranges renderable
// without thousands of daily points.
func (f AnalyticsFilter) CommitsOverTime(ctx context.Context, tx pgx.Tx, bucket string) ([]DayCount, error) {
	trunc := "day"
	switch bucket {
	case "week":
		trunc = "week"
	case "month":
		trunc = "month"
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

// ── Commits-by-contributor (per-contributor over-time series) ──────────────────

// ContributorSeries is one contributor's commit-count timeline over the
// bucketed range. Points carries one entry per bucket in the window (0-filled so
// every series shares the same x-axis), ordered ascending by bucket start.
type ContributorSeries struct {
	Login   string     `json:"login"`
	Name    string     `json:"name"`
	Email   string     `json:"email"`
	IsAgent bool       `json:"isAgent"`
	Points  []DayCount `json:"points"`
}

// CommitsByContributor returns a per-contributor commit-count timeline for the
// top-N contributors (by total commits in the window), bucketed the same way as
// CommitsOverTime (day/week/month). Identities are merged by email (falling back
// to login) exactly like the leaderboard, and agent/bot commits are included to
// match how the leaderboard ranks.
//
// Every returned series is 0-filled across the full set of buckets that appear in
// the window (the union of buckets with any commit), so the lines share a common
// x-axis and align point-for-point. Series are ordered by total commits
// descending (ties broken by login) — index 0 is the top contributor.
//
// When includeOther is true an extra "Everyone else" series (the aggregate of all
// non-top-N contributors) is appended after the top-N, also 0-filled; it is
// omitted entirely when there are no other contributors.
//
// bucket must be "day", "week", or "month"; any other value defaults to "day".
func (f AnalyticsFilter) CommitsByContributor(ctx context.Context, tx pgx.Tx, orgID, bucket string, topN int, includeOther bool) ([]ContributorSeries, error) {
	if topN <= 0 {
		topN = 5
	}
	// Resolver for canonicalizing raw idents → contributors (empty pre-detection).
	resolver, err := IdentityToContributor(ctx, tx, orgID)
	if err != nil {
		return nil, err
	}
	var excluded, bots map[string]bool
	if len(resolver) > 0 {
		excluded, bots, err = ExcludedContributors(ctx, tx, orgID)
		if err != nil {
			return nil, err
		}
	}
	trunc := "day"
	switch bucket {
	case "week":
		trunc = "week"
	case "month":
		trunc = "month"
	}
	where, args, _ := f.whereClause(1)

	// Per identity per bucket commit counts for ALL identities (no SQL LIMIT): we
	// collapse identities onto their canonical contributor in Go FIRST, THEN rank
	// the top-N, so a person's many emails/logins count as one before ranking. We
	// surface the lowercased email + login per identity so the resolver can match
	// by either.
	q := fmt.Sprintf(`
		WITH scoped AS (
			SELECT
				COALESCE(NULLIF(lower(c.author_email::text),''),
				         NULLIF(lower(c.author_login),'')) AS identity,
				lower(COALESCE(c.author_email::text,'')) AS lemail,
				lower(COALESCE(c.author_login,''))       AS llogin,
				c.author_login,
				c.author_email,
				c.is_agent,
				c.committed_at,
				date_trunc('%s', c.committed_at)::date AS bucket
			FROM commits c
			WHERE true%s
		),
		latest AS (
			SELECT DISTINCT ON (identity)
				identity,
				COALESCE(author_login,'')        AS login,
				COALESCE(author_email::text,'')  AS email,
				is_agent
			FROM scoped
			WHERE identity IS NOT NULL
			ORDER BY identity, committed_at DESC
		),
		per_bucket AS (
			SELECT identity, MAX(lemail) AS lemail, MAX(llogin) AS llogin, bucket, COUNT(*) AS n
			FROM scoped
			WHERE identity IS NOT NULL
			GROUP BY identity, bucket
		)
		SELECT
			pb.identity, l.login, l.email, pb.lemail, pb.llogin, l.is_agent, pb.bucket, pb.n
		FROM per_bucket pb
		JOIN latest l USING (identity)
		ORDER BY pb.identity ASC, pb.bucket ASC`, trunc, where)

	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: analytics commits-by-contributor: %w", err)
	}
	defer rows.Close()

	type acc struct {
		series ContributorSeries
		total  int
		counts map[time.Time]int
	}
	// Accumulate per CANONICAL key: contributor_id when resolvable, else the raw
	// identity string. Excluded/bot contributors are dropped on sight.
	byKey := make(map[string]*acc)
	order := make([]string, 0)
	bucketSet := make(map[time.Time]struct{})

	resolve := func(lemail, llogin string) string {
		if lemail != "" {
			if cid, ok := resolver[lemail]; ok {
				return cid
			}
		}
		if llogin != "" {
			if cid, ok := resolver[llogin]; ok {
				return cid
			}
		}
		return ""
	}

	for rows.Next() {
		var (
			identity, login, email, lemail, llogin string
			isAgent                                bool
			bkt                                    time.Time
			n                                      int
		)
		if err := rows.Scan(&identity, &login, &email, &lemail, &llogin, &isAgent, &bkt, &n); err != nil {
			return nil, fmt.Errorf("store: scan commits-by-contributor row: %w", err)
		}
		cid := resolve(lemail, llogin)
		if cid != "" && (excluded[cid] || bots[cid]) {
			continue
		}
		key := cid
		if key == "" {
			key = identity
		}
		a := byKey[key]
		if a == nil {
			name := login
			if cid != "" {
				if d, ok := contribDisplayName(ctx, tx, orgID, cid); ok && d != "" {
					name = d
				}
			}
			a = &acc{
				series: ContributorSeries{Login: login, Email: email, Name: name, IsAgent: isAgent},
				counts: map[time.Time]int{},
			}
			byKey[key] = a
			order = append(order, key)
		}
		a.counts[bkt] += n
		a.total += n
		bucketSet[bkt] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: analytics commits-by-contributor rows: %w", err)
	}

	// Rank the canonical accumulators by total commits and keep the top-N; the
	// rest optionally collapse into "Everyone else".
	type ranked struct {
		key string
		a   *acc
	}
	all := make([]ranked, 0, len(order))
	for _, k := range order {
		all = append(all, ranked{k, byKey[k]})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].a.total != all[j].a.total {
			return all[i].a.total > all[j].a.total
		}
		return all[i].key < all[j].key
	})

	var top []ranked
	var rest []ranked
	if len(all) > topN {
		top = all[:topN]
		rest = all[topN:]
	} else {
		top = all
	}

	var other *acc
	if includeOther && len(rest) > 0 {
		o := &acc{
			series: ContributorSeries{Login: "Everyone else", Name: "Everyone else"},
			counts: map[time.Time]int{},
		}
		for _, r := range rest {
			for b, c := range r.a.counts {
				o.counts[b] += c
				o.total += c
			}
		}
		if o.total > 0 {
			other = o
		}
	}

	// Sorted, deduplicated bucket axis shared by every series.
	buckets := make([]time.Time, 0, len(bucketSet))
	for b := range bucketSet {
		buckets = append(buckets, b)
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].Before(buckets[j]) })

	fill := func(a *acc) ContributorSeries {
		s := a.series
		s.Points = make([]DayCount, len(buckets))
		for i, b := range buckets {
			s.Points[i] = DayCount{Date: b, Count: a.counts[b]}
		}
		return s
	}

	out := make([]ContributorSeries, 0, len(top)+1)
	for _, r := range top {
		out = append(out, fill(r.a))
	}
	if other != nil {
		out = append(out, fill(other))
	}
	return out, nil
}

// contribDisplayName returns the contributor's display name for the given id
// (best-effort; ("",false) when unknown). Cheap single-row lookup used to label
// collapsed contributor series.
func contribDisplayName(ctx context.Context, tx pgx.Tx, orgID, id string) (string, bool) {
	var name string
	err := tx.QueryRow(ctx, `SELECT display_name FROM contributors WHERE org_id=$1 AND id=$2`, orgID, id).Scan(&name)
	if err != nil {
		return "", false
	}
	return name, true
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
func (f AnalyticsFilter) Contributors(ctx context.Context, tx pgx.Tx, orgID string) ([]Contributor, error) {
	where, args, _ := f.whereClause(1)
	// identity = lowercased email when present, else lowercased login. We pick
	// a representative login/name/is_agent via the latest commit per identity.
	// We also surface the email AND login separately so the contributor resolver
	// can canonicalize by either.
	q := `
		WITH scoped AS (
			SELECT
				COALESCE(NULLIF(lower(c.author_email::text),''),
				         NULLIF(lower(c.author_login),'')) AS identity,
				lower(COALESCE(c.author_email::text,'')) AS lemail,
				lower(COALESCE(c.author_login,''))       AS llogin,
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
				MAX(lemail)                           AS lemail,
				MAX(llogin)                           AS llogin,
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
			l.login, l.email, a.lemail, a.llogin, a.commits, a.additions, a.deletions,
			a.active_days, a.projects, a.first_at, a.last_at, l.is_agent
		FROM agg a
		JOIN latest l USING (identity)
		ORDER BY a.commits DESC, a.additions DESC, l.login ASC`

	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: analytics contributors: %w", err)
	}
	defer rows.Close()

	type rawContrib struct {
		Contributor
		lemail string
		llogin string
	}
	var raw []rawContrib
	for rows.Next() {
		var c Contributor
		var lemail, llogin string
		if err := rows.Scan(
			&c.Login, &c.Email, &lemail, &llogin, &c.Commits, &c.Additions, &c.Deletions,
			&c.ActiveDays, &c.Projects, &c.FirstAt, &c.LastAt, &c.IsAgent,
		); err != nil {
			return nil, fmt.Errorf("store: scan contributor: %w", err)
		}
		// Name falls back to login when no display name is recorded.
		c.Name = c.Login
		raw = append(raw, rawContrib{Contributor: c, lemail: lemail, llogin: llogin})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: analytics contributors rows: %w", err)
	}

	// Canonicalize to contributors: collapse identities that resolve to the same
	// contributor and drop excluded/bot ones. When detection hasn't run the
	// resolver is empty and every row passes through unchanged.
	resolver, err := IdentityToContributor(ctx, tx, orgID)
	if err != nil {
		return nil, err
	}
	var excluded, bots map[string]bool
	if len(resolver) > 0 {
		excluded, bots, err = ExcludedContributors(ctx, tx, orgID)
		if err != nil {
			return nil, err
		}
	}
	resolve := func(r rawContrib) string {
		if r.lemail != "" {
			if cid, ok := resolver[r.lemail]; ok {
				return cid
			}
		}
		if r.llogin != "" {
			if cid, ok := resolver[r.llogin]; ok {
				return cid
			}
		}
		return ""
	}

	out := make([]Contributor, 0, len(raw))
	byContrib := map[string]int{} // contributor_id -> index in out
	contribDisplay, _ := contributorDisplay(ctx, tx, orgID) // id -> (name,email)
	for _, r := range raw {
		cid := resolve(r)
		if cid != "" && (excluded[cid] || bots[cid]) {
			continue
		}
		if cid == "" {
			out = append(out, r.Contributor)
			continue
		}
		if idx, ok := byContrib[cid]; ok {
			m := &out[idx]
			m.Commits += r.Commits
			m.Additions += r.Additions
			m.Deletions += r.Deletions
			m.ActiveDays += r.ActiveDays
			m.Projects += r.Projects
			if r.FirstAt.Before(m.FirstAt) {
				m.FirstAt = r.FirstAt
			}
			if r.LastAt.After(m.LastAt) {
				m.LastAt = r.LastAt
			}
			if r.IsAgent {
				m.IsAgent = true
			}
			continue
		}
		c := r.Contributor
		if d, ok := contribDisplay[cid]; ok {
			if d[0] != "" {
				c.Name = d[0]
			}
			if d[1] != "" {
				c.Email = d[1]
			}
		}
		byContrib[cid] = len(out)
		out = append(out, c)
	}

	// Re-sort by commits desc (the collapse can change ranking).
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Commits != out[j].Commits {
			return out[i].Commits > out[j].Commits
		}
		if out[i].Additions != out[j].Additions {
			return out[i].Additions > out[j].Additions
		}
		return out[i].Login < out[j].Login
	})
	return out, nil
}

// contributorDisplay returns contributor_id -> [displayName, primaryEmail] so the
// analytics leaderboard can show the canonical person's name/email after a
// collapse. Best-effort: returns an empty map (no error surfaced to the caller)
// when the table is empty.
func contributorDisplay(ctx context.Context, tx pgx.Tx, orgID string) (map[string][2]string, error) {
	const q = `SELECT id::text, display_name, COALESCE(primary_email,'') FROM contributors WHERE org_id = $1`
	rows, err := tx.Query(ctx, q, orgID)
	if err != nil {
		return map[string][2]string{}, fmt.Errorf("store: contributor display: %w", err)
	}
	defer rows.Close()
	m := map[string][2]string{}
	for rows.Next() {
		var id, name, email string
		if err := rows.Scan(&id, &name, &email); err != nil {
			return m, fmt.Errorf("store: scan contributor display: %w", err)
		}
		m[id] = [2]string{name, email}
	}
	return m, rows.Err()
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

// ── Pull-request analytics ────────────────────────────────────────────────────

// prWhere builds the parameterised WHERE fragment for pull_requests, aliased
// `p`. The date window filters on p.created_at (inclusive lower, exclusive
// upper); RepoID filters p.repo_id; Author matches p.author_login
// case-insensitively (PRs carry no email). Returns the fragment (no leading
// keyword), the ordered args, and the next free positional index.
func (f AnalyticsFilter) prWhere(startIdx int) (string, []any, int) {
	var (
		clause string
		args   []any
		idx    = startIdx
	)
	if !f.From.IsZero() {
		clause += fmt.Sprintf(" AND p.created_at >= $%d", idx)
		args = append(args, f.From)
		idx++
	}
	if !f.To.IsZero() {
		clause += fmt.Sprintf(" AND p.created_at < $%d", idx)
		args = append(args, f.To)
		idx++
	}
	if f.RepoID != "" {
		clause += fmt.Sprintf(" AND p.repo_id = $%d", idx)
		args = append(args, f.RepoID)
		idx++
	}
	if f.Author != "" {
		clause += fmt.Sprintf(" AND lower(COALESCE(p.author_login,'')) = lower($%d)", idx)
		args = append(args, f.Author)
		idx++
	}
	return clause, args, idx
}

// PRStats holds the raw pull-request totals plus the lead-time samples (hours
// from first_commit_at — falling back to created_at — to merged_at, for merged
// PRs only). Percentiles and the merge rate are derived in the analytics package
// from these fields so they stay unit-testable without a database.
type PRStats struct {
	Total           int       `json:"total"`
	Merged          int       `json:"merged"`
	Open            int       `json:"open"`
	Closed          int       `json:"closed"`
	SumChangedFiles int64     `json:"-"`
	LeadTimeHours   []float64 `json:"-"`
}

// PRStats returns pull-request totals and per-merged-PR lead-time samples over
// the filtered window. State buckets: merged (state='merged' OR merged_at set),
// open (state='open'), closed (state='closed' and not merged). Lead time is
// computed in SQL as EPOCH hours but returned as raw samples so the percentiles
// are derived (and tested) in Go.
func (f AnalyticsFilter) PRStats(ctx context.Context, tx pgx.Tx) (PRStats, error) {
	where, args, _ := f.prWhere(1)
	q := `
		SELECT
			COUNT(*)                                                       AS total,
			COUNT(*) FILTER (WHERE p.state = 'merged' OR p.merged_at IS NOT NULL) AS merged,
			COUNT(*) FILTER (WHERE p.state = 'open'   AND p.merged_at IS NULL)    AS open,
			COUNT(*) FILTER (WHERE p.state = 'closed' AND p.merged_at IS NULL)    AS closed,
			COALESCE(SUM(p.changed_files), 0)                              AS sum_changed_files
		FROM pull_requests p
		WHERE true` + where

	var s PRStats
	if err := tx.QueryRow(ctx, q, args...).Scan(
		&s.Total, &s.Merged, &s.Open, &s.Closed, &s.SumChangedFiles,
	); err != nil {
		return PRStats{}, fmt.Errorf("store: analytics pr summary: %w", err)
	}

	// Lead-time samples (hours) for merged PRs with a usable start timestamp.
	lq := `
		SELECT EXTRACT(EPOCH FROM (p.merged_at - COALESCE(p.first_commit_at, p.created_at))) / 3600.0
		FROM pull_requests p
		WHERE p.merged_at IS NOT NULL
		  AND p.merged_at >= COALESCE(p.first_commit_at, p.created_at)` + where
	rows, err := tx.Query(ctx, lq, args...)
	if err != nil {
		return PRStats{}, fmt.Errorf("store: analytics pr lead-time: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var h float64
		if err := rows.Scan(&h); err != nil {
			return PRStats{}, fmt.Errorf("store: scan pr lead-time: %w", err)
		}
		s.LeadTimeHours = append(s.LeadTimeHours, h)
	}
	if err := rows.Err(); err != nil {
		return PRStats{}, fmt.Errorf("store: analytics pr lead-time rows: %w", err)
	}
	return s, nil
}

// PRThroughput is one day with the count of PRs opened (by created_at) and
// merged (by merged_at) on that day.
type PRThroughput struct {
	Date   time.Time `json:"date"`
	Opened int       `json:"opened"`
	Merged int       `json:"merged"`
}

// PRThroughput returns per-day opened/merged PR counts over the window, ordered
// ascending. A PR contributes to the "opened" series on its created_at date and
// to the "merged" series on its merged_at date; both are bounded by the window.
// The two series are unioned so a day appears if it had either event.
func (f AnalyticsFilter) PRThroughput(ctx context.Context, tx pgx.Tx) ([]PRThroughput, error) {
	where, args, _ := f.prWhere(1)
	// "opened" rows use the standard window (created_at). For "merged" we reuse
	// the same repo/author filters but bound by merged_at against the window.
	mwhere := where
	q := `
		WITH opened AS (
			SELECT p.created_at::date AS day, COUNT(*) AS n
			FROM pull_requests p
			WHERE true` + where + `
			GROUP BY 1
		),
		merged AS (
			SELECT p.merged_at::date AS day, COUNT(*) AS n
			FROM pull_requests p
			WHERE p.merged_at IS NOT NULL` + mwhere + `
			GROUP BY 1
		)
		SELECT
			COALESCE(o.day, m.day)  AS day,
			COALESCE(o.n, 0)        AS opened,
			COALESCE(m.n, 0)        AS merged
		FROM opened o
		FULL OUTER JOIN merged m ON o.day = m.day
		ORDER BY day`

	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: analytics pr throughput: %w", err)
	}
	defer rows.Close()

	var out []PRThroughput
	for rows.Next() {
		var t PRThroughput
		if err := rows.Scan(&t.Date, &t.Opened, &t.Merged); err != nil {
			return nil, fmt.Errorf("store: scan pr throughput: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: analytics pr throughput rows: %w", err)
	}
	return out, nil
}

// ── Issue-flow analytics ──────────────────────────────────────────────────────

// issueWhere builds the parameterised WHERE fragment for issues, aliased `i`.
// dateCol selects which timestamp column the window filters on ("created_at" or
// "updated_at"). RepoID filters i.repo_id; the Author filter is ignored (issues
// carry no author identity). Returns the fragment, args, next index.
func (f AnalyticsFilter) issueWhere(startIdx int, dateCol string) (string, []any, int) {
	var (
		clause string
		args   []any
		idx    = startIdx
	)
	if !f.From.IsZero() {
		clause += fmt.Sprintf(" AND i.%s >= $%d", dateCol, idx)
		args = append(args, f.From)
		idx++
	}
	if !f.To.IsZero() {
		clause += fmt.Sprintf(" AND i.%s < $%d", dateCol, idx)
		args = append(args, f.To)
		idx++
	}
	if f.RepoID != "" {
		clause += fmt.Sprintf(" AND i.repo_id = $%d", idx)
		args = append(args, f.RepoID)
		idx++
	}
	return clause, args, idx
}

// IssueState is the effective state of an issue: COALESCE(derived_state, state).
// Buckets: open | in_progress | done | closed (anything else → open).

// IssueFlowCounts holds the current state breakdown over the filtered set. The
// window filters on created_at (i.e. issues created in the window).
type IssueFlowCounts struct {
	Open       int `json:"open"`
	InProgress int `json:"inProgress"`
	Done       int `json:"done"`
	Closed     int `json:"closed"`
}

// IssueFlowCounts returns the effective-state breakdown for issues created in
// the window. State is COALESCE(derived_state, state) so git-derived states win.
func (f AnalyticsFilter) IssueFlowCounts(ctx context.Context, tx pgx.Tx) (IssueFlowCounts, error) {
	where, args, _ := f.issueWhere(1, "created_at")
	q := `
		SELECT
			COUNT(*) FILTER (WHERE COALESCE(i.derived_state, i.state) = 'open')        AS open,
			COUNT(*) FILTER (WHERE COALESCE(i.derived_state, i.state) = 'in_progress') AS in_progress,
			COUNT(*) FILTER (WHERE COALESCE(i.derived_state, i.state) = 'done')        AS done,
			COUNT(*) FILTER (WHERE COALESCE(i.derived_state, i.state) = 'closed')      AS closed
		FROM issues i
		WHERE true` + where

	var c IssueFlowCounts
	if err := tx.QueryRow(ctx, q, args...).Scan(
		&c.Open, &c.InProgress, &c.Done, &c.Closed,
	); err != nil {
		return IssueFlowCounts{}, fmt.Errorf("store: analytics issue state counts: %w", err)
	}
	return c, nil
}

// IssuesOpenedOverTime returns per-day counts of issues created in the window,
// ordered ascending (only days with ≥1 created issue).
func (f AnalyticsFilter) IssuesOpenedOverTime(ctx context.Context, tx pgx.Tx) ([]DayCount, error) {
	where, args, _ := f.issueWhere(1, "created_at")
	q := `
		SELECT i.created_at::date AS day, COUNT(*) AS n
		FROM issues i
		WHERE true` + where + `
		GROUP BY 1
		ORDER BY 1`
	return scanDayCounts(ctx, tx, "analytics issues-opened", q, args)
}

// IssuesClosedOverTime returns per-day counts of issues that reached a terminal
// state (done|closed) within the window, bucketed by updated_at (the best
// available "resolved at" proxy in the schema), ordered ascending.
func (f AnalyticsFilter) IssuesClosedOverTime(ctx context.Context, tx pgx.Tx) ([]DayCount, error) {
	where, args, _ := f.issueWhere(1, "updated_at")
	q := `
		SELECT i.updated_at::date AS day, COUNT(*) AS n
		FROM issues i
		WHERE COALESCE(i.derived_state, i.state) IN ('done','closed')` + where + `
		GROUP BY 1
		ORDER BY 1`
	return scanDayCounts(ctx, tx, "analytics issues-closed", q, args)
}

// IssueProjectStat is one project's open/done issue counts.
type IssueProjectStat struct {
	Project string `json:"project"`
	Open    int    `json:"open"`
	Done    int    `json:"done"`
}

// IssuesByProject returns per-project open vs done (terminal) issue counts for
// issues created in the window, ordered by total descending. Issues with no
// project are grouped under "(no project)".
func (f AnalyticsFilter) IssuesByProject(ctx context.Context, tx pgx.Tx) ([]IssueProjectStat, error) {
	where, args, _ := f.issueWhere(1, "created_at")
	q := `
		SELECT
			COALESCE(pr.name, '(no project)') AS project,
			COUNT(*) FILTER (WHERE COALESCE(i.derived_state, i.state) NOT IN ('done','closed')) AS open,
			COUNT(*) FILTER (WHERE COALESCE(i.derived_state, i.state) IN ('done','closed'))     AS done
		FROM issues i
		LEFT JOIN projects pr ON pr.id = i.project_id
		WHERE true` + where + `
		GROUP BY 1
		ORDER BY (COUNT(*)) DESC, project ASC`

	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: analytics issues-by-project: %w", err)
	}
	defer rows.Close()

	var out []IssueProjectStat
	for rows.Next() {
		var s IssueProjectStat
		if err := rows.Scan(&s.Project, &s.Open, &s.Done); err != nil {
			return nil, fmt.Errorf("store: scan issue project stat: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: analytics issues-by-project rows: %w", err)
	}
	return out, nil
}

// ── Agent-share analytics ─────────────────────────────────────────────────────

// AgentShare holds the agent-vs-human commit split over the filtered window.
// agentPct is derived in the analytics package from the two raw counts.
type AgentShare struct {
	AgentCommits int `json:"agentCommits"`
	HumanCommits int `json:"humanCommits"`
}

// AgentShare returns the agent vs human commit counts over the filtered commit
// set, using commits.is_agent (decision P5). Reuses the standard commit
// whereClause so the date/repo/author filters apply uniformly.
func (f AnalyticsFilter) AgentShare(ctx context.Context, tx pgx.Tx) (AgentShare, error) {
	where, args, _ := f.whereClause(1)
	q := `
		SELECT
			COUNT(*) FILTER (WHERE c.is_agent)       AS agent,
			COUNT(*) FILTER (WHERE NOT c.is_agent)   AS human
		FROM commits c
		WHERE true` + where

	var a AgentShare
	if err := tx.QueryRow(ctx, q, args...).Scan(&a.AgentCommits, &a.HumanCommits); err != nil {
		return AgentShare{}, fmt.Errorf("store: analytics agent share: %w", err)
	}
	return a, nil
}

// AgentDay is one day with its agent vs human commit counts.
type AgentDay struct {
	Date  time.Time `json:"date"`
	Agent int       `json:"agent"`
	Human int       `json:"human"`
}

// AgentShareOverTime returns per-day agent vs human commit counts over the
// window, ordered ascending (only days with ≥1 commit).
func (f AnalyticsFilter) AgentShareOverTime(ctx context.Context, tx pgx.Tx) ([]AgentDay, error) {
	where, args, _ := f.whereClause(1)
	q := `
		SELECT
			c.committed_at::date                   AS day,
			COUNT(*) FILTER (WHERE c.is_agent)     AS agent,
			COUNT(*) FILTER (WHERE NOT c.is_agent) AS human
		FROM commits c
		WHERE true` + where + `
		GROUP BY 1
		ORDER BY 1`

	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: analytics agent share over time: %w", err)
	}
	defer rows.Close()

	var out []AgentDay
	for rows.Next() {
		var d AgentDay
		if err := rows.Scan(&d.Date, &d.Agent, &d.Human); err != nil {
			return nil, fmt.Errorf("store: scan agent day: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: analytics agent share over time rows: %w", err)
	}
	return out, nil
}

// ── Per-project analytics ─────────────────────────────────────────────────────

// ProjectStat is one row of the per-project table. Commit/contributor/churn
// metrics are attributed to a project via the issues→repo→commits linkage:
// commits in any repo that the project's issues reference. openIssues/doneIssues
// come straight from the project's issues (effective state).
type ProjectStat struct {
	ProjectID    string `json:"projectId"`
	Name         string `json:"name"`
	Commits      int    `json:"commits"`
	Contributors int    `json:"contributors"`
	OpenIssues   int    `json:"openIssues"`
	DoneIssues   int    `json:"doneIssues"`
	Additions    int64  `json:"additions"`
	Deletions    int64  `json:"deletions"`
}

// ProjectStats returns the per-project table over the filtered window. Issue
// counts use issues created in the window (effective state). Commit/churn
// metrics are summed over commits (in the window) in the repos that the
// project's issues link to — the schema's only project→code bridge. Projects
// are ordered by commits then open issues, descending.
func (f AnalyticsFilter) ProjectStats(ctx context.Context, tx pgx.Tx) ([]ProjectStat, error) {
	// Issue side: window on issues.created_at, repo filter honoured.
	iWhere, iArgs, next := f.issueWhere(1, "created_at")
	// Commit side: window on commits.committed_at, repo/author filters honoured.
	cWhere, cArgs, _ := f.whereClause(next)
	args := append(append([]any{}, iArgs...), cArgs...)

	q := `
		WITH proj AS (
			SELECT id, name FROM projects
		),
		issue_agg AS (
			SELECT
				i.project_id,
				COUNT(*) FILTER (WHERE COALESCE(i.derived_state, i.state) NOT IN ('done','closed')) AS open_issues,
				COUNT(*) FILTER (WHERE COALESCE(i.derived_state, i.state) IN ('done','closed'))     AS done_issues,
				array_agg(DISTINCT i.repo_id) FILTER (WHERE i.repo_id IS NOT NULL)                  AS repo_ids
			FROM issues i
			WHERE true` + iWhere + `
			GROUP BY i.project_id
		),
		commit_agg AS (
			SELECT
				ia.project_id,
				COUNT(c.*)                          AS commits,
				COUNT(DISTINCT COALESCE(NULLIF(lower(c.author_email::text),''),
				                        NULLIF(lower(c.author_login),''))) AS contributors,
				COALESCE(SUM(c.additions), 0)       AS additions,
				COALESCE(SUM(c.deletions), 0)       AS deletions
			FROM issue_agg ia
			JOIN commits c ON c.repo_id = ANY(ia.repo_ids)
			WHERE true` + cWhere + `
			GROUP BY ia.project_id
		)
		SELECT
			p.id::text,
			p.name,
			COALESCE(ca.commits, 0)       AS commits,
			COALESCE(ca.contributors, 0)  AS contributors,
			COALESCE(ia.open_issues, 0)   AS open_issues,
			COALESCE(ia.done_issues, 0)   AS done_issues,
			COALESCE(ca.additions, 0)     AS additions,
			COALESCE(ca.deletions, 0)     AS deletions
		FROM proj p
		LEFT JOIN issue_agg ia  ON ia.project_id = p.id
		LEFT JOIN commit_agg ca ON ca.project_id = p.id
		ORDER BY commits DESC, open_issues DESC, p.name ASC`

	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: analytics project stats: %w", err)
	}
	defer rows.Close()

	var out []ProjectStat
	for rows.Next() {
		var s ProjectStat
		if err := rows.Scan(
			&s.ProjectID, &s.Name, &s.Commits, &s.Contributors,
			&s.OpenIssues, &s.DoneIssues, &s.Additions, &s.Deletions,
		); err != nil {
			return nil, fmt.Errorf("store: scan project stat: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: analytics project stats rows: %w", err)
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
