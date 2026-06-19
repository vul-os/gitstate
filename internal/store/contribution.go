// Package store — contribution.go
// Org-scoped reads for the "dev contribution to outcomes" engine, plus the
// configurable per-dimension WEIGHTS (contribution_weights table, migration
// 20260619_009).
//
// Design principles (decisions P2/P3/P5, and the gaming-resistance brief):
//   - GATES IN SQL. "shipped" and "effort" only count ACCEPTED work: merged PRs
//     (state='merged' OR merged_at present) and done/closed issues. Unmerged work
//     contributes nothing — so opening throwaway PRs can't farm a score.
//   - NEVER a raw commit/LOC count drives a score. Commits are only used for the
//     authorship transparency split (human vs agent) and the revert/hotfix signal.
//   - IDENTITY MAP. Members are git identities (email, falling back to login),
//     joined to users by email when a matching user row exists. Agent identities
//     are flagged (isAgentBot) so they never silently inflate a human.
//
// Every function MUST run inside db.WithOrg(ctx, orgID, …) so the org_isolation
// RLS policy is active. The org_id is also passed as a bind param for the
// non-RLS-protected JOIN predicates (users has no org scope).
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ── Weights ─────────────────────────────────────────────────────────────────

// ContributionWeights mirrors a contribution_weights row (relative, non-negative).
type ContributionWeights struct {
	OrgID     string
	Shipped   float64
	Review    float64
	Effort    float64
	Quality   float64
	Ownership float64
	UpdatedAt time.Time
}

// defaultContributionWeights matches the column defaults in the migration.
func defaultContributionWeights(orgID string) ContributionWeights {
	return ContributionWeights{OrgID: orgID, Shipped: 30, Review: 20, Effort: 20, Quality: 15, Ownership: 15}
}

// GetContributionWeights returns the org's weights, or the migration defaults
// when no row exists yet. Must run inside db.WithOrg.
func GetContributionWeights(ctx context.Context, tx pgx.Tx, orgID string) (ContributionWeights, error) {
	const q = `
		SELECT org_id::text, shipped::float8, review::float8, effort::float8,
		       quality::float8, ownership::float8, updated_at
		FROM contribution_weights
		WHERE org_id = $1`
	var w ContributionWeights
	err := tx.QueryRow(ctx, q, orgID).Scan(
		&w.OrgID, &w.Shipped, &w.Review, &w.Effort, &w.Quality, &w.Ownership, &w.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return defaultContributionWeights(orgID), nil
	}
	if err != nil {
		return ContributionWeights{}, fmt.Errorf("store: get contribution weights: %w", err)
	}
	return w, nil
}

// UpsertContributionWeights writes the org's weights and returns the stored row.
// Must run inside db.WithOrg.
func UpsertContributionWeights(ctx context.Context, tx pgx.Tx, w ContributionWeights) (ContributionWeights, error) {
	const q = `
		INSERT INTO contribution_weights (org_id, shipped, review, effort, quality, ownership, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (org_id) DO UPDATE SET
			shipped    = EXCLUDED.shipped,
			review     = EXCLUDED.review,
			effort     = EXCLUDED.effort,
			quality    = EXCLUDED.quality,
			ownership  = EXCLUDED.ownership,
			updated_at = now()
		RETURNING org_id::text, shipped::float8, review::float8, effort::float8,
		          quality::float8, ownership::float8, updated_at`
	var out ContributionWeights
	err := tx.QueryRow(ctx, q, w.OrgID, w.Shipped, w.Review, w.Effort, w.Quality, w.Ownership).Scan(
		&out.OrgID, &out.Shipped, &out.Review, &out.Effort, &out.Quality, &out.Ownership, &out.UpdatedAt,
	)
	if err != nil {
		return ContributionWeights{}, fmt.Errorf("store: upsert contribution weights: %w", err)
	}
	return out, nil
}

// ── Aggregates ──────────────────────────────────────────────────────────────

// ContribAggregate is the merge-gated, period-scoped facts for one member,
// already mapped to a user identity. All "shipped"/"effort" counts are gated to
// accepted work in SQL.
type ContribAggregate struct {
	UserID          string
	Name            string
	Email           string
	Login           string
	IsAgentBot      bool
	MergedPRs       int
	IssuesClosed    int
	FeaturesShipped int
	ReviewsDone     int
	EffortPoints    float64
	Reverts         int
	AvgCycleHours   float64
	AreasOwned      int
	HumanCommits    int
	AgentCommits    int
}

// revertPredicate matches revert / hotfix / rollback commit messages (the only
// quality signal available without blame/SZZ data — see contribution.SZZQuality).
const revertPredicate = `(lower(c.message) LIKE 'revert%' OR lower(c.message) LIKE '%hotfix%' OR lower(c.message) LIKE '%rollback%' OR lower(c.message) LIKE '%roll back%')`

// LoadContributionAggregates builds one ContribAggregate per git identity active
// in [from,to), with all the GATES applied in SQL. Identity = lower(author_email)
// when present, else lower(author_login); it is joined to users by email so a
// userId/name surface when known.
//
// Sources & gates:
//   - shipped.mergedPRs   : pull_requests where (state='merged' OR merged_at) in window, by author_login.
//   - shipped.issuesClosed: issues reaching done/closed in window (updated_at), by assignee → user → email/login.
//   - shipped.featuresShipped, review.reviewsDone, ownership.areasOwned: involvement rows in window, by user.
//   - effort.effortPoints : SUM(effort_estimates.difficulty) over the member's MERGED PRs only.
//   - quality.reverts     : revert/hotfix/rollback commits authored in window.
//   - quality.avgCycleHours: mean lead_time_secs of the member's merged PRs' cycle_times.
//   - authorship          : human vs agent commit counts (is_agent), transparency only.
//
// Must run inside db.WithOrg(ctx, orgID, …).
func LoadContributionAggregates(ctx context.Context, tx pgx.Tx, orgID string, from, to time.Time) ([]ContribAggregate, error) {
	// We assemble per-identity facts from several sources, then merge in Go,
	// because the natural join key differs per source (PRs/commits → login/email;
	// involvement/issues → user_id). Keeping each query simple keeps it auditable.

	byIdent := map[string]*contribAcc{}

	// get-or-create an accumulator keyed by a normalized identity string.
	get := func(ident, login, email string) *contribAcc {
		if ident == "" {
			ident = "(unknown)"
		}
		a := byIdent[ident]
		if a == nil {
			a = &contribAcc{}
			a.Login = login
			a.Email = email
			byIdent[ident] = a
		} else {
			if a.Login == "" {
				a.Login = login
			}
			if a.Email == "" {
				a.Email = email
			}
		}
		a.seen = true
		return a
	}

	// 1) Commit-derived: human/agent split, reverts, and agent-bot detection.
	//    Identity = lower(email) ?? lower(login).
	{
		const q = `
			SELECT
				COALESCE(NULLIF(lower(c.author_email::text),''), NULLIF(lower(c.author_login),'')) AS ident,
				COALESCE(max(c.author_login),'')          AS login,
				COALESCE(max(c.author_email::text),'')     AS email,
				COUNT(*) FILTER (WHERE NOT c.is_agent)     AS human_commits,
				COUNT(*) FILTER (WHERE c.is_agent)         AS agent_commits,
				COUNT(*) FILTER (WHERE ` + revertPredicate + `) AS reverts,
				bool_and(c.is_agent)                       AS all_agent
			FROM commits c
			WHERE c.org_id = $1 AND c.committed_at >= $2 AND c.committed_at < $3
			GROUP BY 1`
		rows, err := tx.Query(ctx, q, orgID, from, to)
		if err != nil {
			return nil, fmt.Errorf("store: contribution commit agg: %w", err)
		}
		for rows.Next() {
			var ident, login, email string
			var human, agent, reverts int
			var allAgent *bool
			if err := rows.Scan(&ident, &login, &email, &human, &agent, &reverts, &allAgent); err != nil {
				rows.Close()
				return nil, fmt.Errorf("store: scan contribution commit agg: %w", err)
			}
			if ident == "" {
				continue
			}
			a := get(ident, login, email)
			a.HumanCommits = human
			a.AgentCommits = agent
			a.Reverts = reverts
			// An identity whose every commit is agent-authored is an agent bot.
			if allAgent != nil && *allAgent {
				a.IsAgentBot = true
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: contribution commit agg rows: %w", err)
		}
		rows.Close()
	}

	// 2) Merged-PR shipped count + effort points + cycle hours, by author_login.
	//    GATE: merged only. Identity = lower(login) (PRs carry no email).
	{
		const q = `
			WITH merged AS (
				SELECT p.id, lower(p.author_login) AS ident, p.author_login
				FROM pull_requests p
				WHERE p.org_id = $1
				  AND (p.state = 'merged' OR p.merged_at IS NOT NULL)
				  AND p.merged_at >= $2 AND p.merged_at < $3
				  AND p.author_login IS NOT NULL AND p.author_login <> ''
			),
			eff AS (
				SELECT m.ident, COALESCE(SUM(e.difficulty),0)::float8 AS effort_points
				FROM merged m
				LEFT JOIN effort_estimates e ON e.pr_id = m.id AND e.org_id = $1
				GROUP BY m.ident
			),
			cyc AS (
				SELECT m.ident,
				       AVG(ct.lead_time_secs)::float8 / 3600.0 AS avg_cycle_hours
				FROM merged m
				JOIN cycle_times ct ON ct.pr_id = m.id AND ct.org_id = $1
				WHERE ct.lead_time_secs IS NOT NULL
				GROUP BY m.ident
			),
			cnt AS (
				SELECT ident, COUNT(*) AS merged_prs, max(author_login) AS login
				FROM merged GROUP BY ident
			)
			SELECT cnt.ident, cnt.login, cnt.merged_prs,
			       COALESCE(eff.effort_points,0), COALESCE(cyc.avg_cycle_hours,0)
			FROM cnt
			LEFT JOIN eff ON eff.ident = cnt.ident
			LEFT JOIN cyc ON cyc.ident = cnt.ident`
		rows, err := tx.Query(ctx, q, orgID, from, to)
		if err != nil {
			return nil, fmt.Errorf("store: contribution pr agg: %w", err)
		}
		for rows.Next() {
			var ident, login string
			var mergedPRs int
			var effort, cycle float64
			if err := rows.Scan(&ident, &login, &mergedPRs, &effort, &cycle); err != nil {
				rows.Close()
				return nil, fmt.Errorf("store: scan contribution pr agg: %w", err)
			}
			if ident == "" {
				continue
			}
			// Prefer matching to an existing commit-identity by login when emails
			// don't line up; otherwise key by this login identity.
			a := mergeByLogin(byIdent, ident, login)
			a.MergedPRs = mergedPRs
			a.EffortPoints = effort
			a.AvgCycleHours = cycle
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: contribution pr agg rows: %w", err)
		}
		rows.Close()
	}

	// 3) Issues closed in window, attributed to the assignee user's email/login.
	//    GATE: effective state in (done,closed) and updated within window.
	{
		const q = `
			SELECT COALESCE(lower(u.email::text),'') AS ident,
			       COALESCE(u.id::text,'') AS user_id,
			       COALESCE(u.name,'') AS name,
			       COALESCE(u.email::text,'') AS email,
			       COUNT(*) AS issues_closed
			FROM issues i
			JOIN users u ON u.id = i.assignee_id
			WHERE i.org_id = $1
			  AND COALESCE(i.derived_state, i.state) IN ('done','closed')
			  AND i.updated_at >= $2 AND i.updated_at < $3
			GROUP BY 1,2,3,4`
		rows, err := tx.Query(ctx, q, orgID, from, to)
		if err != nil {
			return nil, fmt.Errorf("store: contribution issue agg: %w", err)
		}
		for rows.Next() {
			var ident, userID, name, email string
			var closed int
			if err := rows.Scan(&ident, &userID, &name, &email, &closed); err != nil {
				rows.Close()
				return nil, fmt.Errorf("store: scan contribution issue agg: %w", err)
			}
			if ident == "" {
				continue
			}
			a := get(ident, "", email)
			a.IssuesClosed = closed
			if a.UserID == "" {
				a.UserID = userID
			}
			if a.Name == "" {
				a.Name = name
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: contribution issue agg rows: %w", err)
		}
		rows.Close()
	}

	// 4) Involvement texture (features_shipped, reviews_done, areas_owned),
	//    summed over periods overlapping the window, attributed to the user.
	{
		const q = `
			SELECT COALESCE(lower(u.email::text),'') AS ident,
			       COALESCE(u.id::text,'') AS user_id,
			       COALESCE(u.name,'') AS name,
			       COALESCE(u.email::text,'') AS email,
			       COALESCE(SUM(inv.features_shipped),0) AS features_shipped,
			       COALESCE(SUM(inv.reviews_done),0)     AS reviews_done,
			       COALESCE(MAX(inv.areas_owned),0)      AS areas_owned
			FROM involvement inv
			JOIN users u ON u.id = inv.user_id
			WHERE inv.org_id = $1
			  AND inv.period_start >= ($2)::date AND inv.period_start < ($3)::date
			GROUP BY 1,2,3,4`
		rows, err := tx.Query(ctx, q, orgID, from, to)
		if err != nil {
			return nil, fmt.Errorf("store: contribution involvement agg: %w", err)
		}
		for rows.Next() {
			var ident, userID, name, email string
			var feats, reviews, areas int
			if err := rows.Scan(&ident, &userID, &name, &email, &feats, &reviews, &areas); err != nil {
				rows.Close()
				return nil, fmt.Errorf("store: scan contribution involvement agg: %w", err)
			}
			if ident == "" {
				continue
			}
			a := get(ident, "", email)
			a.FeaturesShipped = feats
			a.ReviewsDone = reviews
			a.AreasOwned = areas
			if a.UserID == "" {
				a.UserID = userID
			}
			if a.Name == "" {
				a.Name = name
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: contribution involvement agg rows: %w", err)
		}
		rows.Close()
	}

	// 5) Backfill user_id/name for identities we matched only by login/email but
	//    that DO have a user row (so the UI gets a real userId).
	if err := backfillUsers(ctx, tx, byIdent); err != nil {
		return nil, err
	}

	out := make([]ContribAggregate, 0, len(byIdent))
	for _, a := range byIdent {
		if !a.seen {
			continue
		}
		// Name falls back to login then email for display.
		if a.Name == "" {
			if a.Login != "" {
				a.Name = a.Login
			} else {
				a.Name = a.Email
			}
		}
		out = append(out, a.ContribAggregate)
	}
	return out, nil
}

// contribAcc accumulates one identity's facts as the per-source queries merge in.
type contribAcc struct {
	ContribAggregate
	seen bool
}

// mergeByLogin returns the accumulator for a PR identity: it reuses an existing
// commit-identity whose login matches (case-insensitive) so a person's PRs and
// commits collapse into one member even when their git email differs from their
// platform login; otherwise it creates/returns an accumulator keyed by the PR
// identity. login is stored for evidence lookups.
func mergeByLogin(byIdent map[string]*contribAcc, ident, login string) *contribAcc {
	if login != "" {
		for _, a := range byIdent {
			if a.Login != "" && equalFoldASCII(a.Login, login) {
				a.seen = true
				return a
			}
		}
	}
	a := byIdent[ident]
	if a == nil {
		a = &contribAcc{}
		byIdent[ident] = a
	}
	if a.Login == "" {
		a.Login = login
	}
	a.seen = true
	return a
}

// equalFoldASCII is a small case-insensitive compare (logins are ASCII).
func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// backfillUsers resolves user_id/name for identities matched only by email/login
// to a real user row, so the UI can link to the user. Best-effort: identities
// without a matching user keep an empty UserID (still shown, with login as name).
func backfillUsers(ctx context.Context, tx pgx.Tx, byIdent map[string]*contribAcc) error {
	// Collect the emails we still need to resolve.
	emails := make([]string, 0, len(byIdent))
	for _, a := range byIdent {
		if a.seen && a.UserID == "" && a.Email != "" {
			emails = append(emails, a.Email)
		}
	}
	if len(emails) == 0 {
		return nil
	}
	const q = `SELECT lower(email::text), id::text, COALESCE(name,'') FROM users WHERE email = ANY($1)`
	rows, err := tx.Query(ctx, q, emails)
	if err != nil {
		return fmt.Errorf("store: contribution backfill users: %w", err)
	}
	defer rows.Close()
	resolved := map[string][2]string{}
	for rows.Next() {
		var lemail, id, name string
		if err := rows.Scan(&lemail, &id, &name); err != nil {
			return fmt.Errorf("store: scan backfill user: %w", err)
		}
		resolved[lemail] = [2]string{id, name}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store: contribution backfill users rows: %w", err)
	}
	for _, a := range byIdent {
		if a.UserID != "" || a.Email == "" {
			continue
		}
		if r, ok := resolved[lowerASCII(a.Email)]; ok {
			a.UserID = r[0]
			if a.Name == "" {
				a.Name = r[1]
			}
		}
	}
	return nil
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// ── Evidence (drill-down) ──────────────────────────────────────────────────────

// ContribEvidenceItem is one real row backing a dimension (the texture proof).
type ContribEvidenceItem struct {
	Title   string    `json:"title"`
	Repo    string    `json:"repo,omitempty"`
	Message string    `json:"message,omitempty"`
	At      time.Time `json:"at"`
}

// ContribEvidence bundles the per-dimension evidence rows for one member.
type ContribEvidence struct {
	Shipped []ContribEvidenceItem
	Review  []ContribEvidenceItem
	Quality []ContribEvidenceItem
	Effort  []ContribEvidenceItem
}

// LoadContributionEvidence returns the real rows that back each dimension for one
// member (matched by email and/or login), newest-first, capped per dimension.
// Must run inside db.WithOrg.
func LoadContributionEvidence(ctx context.Context, tx pgx.Tx, orgID, email, login string, from, to time.Time) (ContribEvidence, error) {
	var ev ContribEvidence
	const limit = 20

	// shipped + effort: the member's merged PRs in window (effort evidence is the
	// same PRs annotated with their LLM difficulty).
	{
		const q = `
			SELECT COALESCE(p.title,'(untitled PR)'), COALESCE(r.full_name,''), p.merged_at,
			       COALESCE((SELECT e.difficulty FROM effort_estimates e WHERE e.pr_id = p.id AND e.org_id = $1 ORDER BY e.created_at DESC LIMIT 1), 0)::float8
			FROM pull_requests p
			LEFT JOIN repos r ON r.id = p.repo_id
			WHERE p.org_id = $1
			  AND (p.state = 'merged' OR p.merged_at IS NOT NULL)
			  AND p.merged_at >= $2 AND p.merged_at < $3
			  AND lower(COALESCE(p.author_login,'')) = lower($4)
			ORDER BY p.merged_at DESC
			LIMIT $5`
		rows, err := tx.Query(ctx, q, orgID, from, to, login, limit)
		if err != nil {
			return ContribEvidence{}, fmt.Errorf("store: contribution evidence shipped: %w", err)
		}
		for rows.Next() {
			var it ContribEvidenceItem
			var at *time.Time
			var difficulty float64
			if err := rows.Scan(&it.Title, &it.Repo, &at, &difficulty); err != nil {
				rows.Close()
				return ContribEvidence{}, fmt.Errorf("store: scan evidence shipped: %w", err)
			}
			if at != nil {
				it.At = *at
			}
			ev.Shipped = append(ev.Shipped, it)
			if difficulty > 0 {
				eff := it
				eff.Title = fmt.Sprintf("%s (difficulty %.0f)", it.Title, difficulty)
				ev.Effort = append(ev.Effort, eff)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return ContribEvidence{}, fmt.Errorf("store: contribution evidence shipped rows: %w", err)
		}
		rows.Close()
	}

	// review: the member's involvement rows that recorded review work.
	{
		const q = `
			SELECT 'Reviewed ' || inv.reviews_done || ' PR(s)' AS title,
			       COALESCE(pr.name,'') AS repo,
			       inv.period_start
			FROM involvement inv
			JOIN users u ON u.id = inv.user_id
			LEFT JOIN projects pr ON pr.id = inv.project_id
			WHERE inv.org_id = $1 AND inv.reviews_done > 0
			  AND lower(u.email::text) = lower($2)
			  AND inv.period_start >= ($3)::date AND inv.period_start < ($4)::date
			ORDER BY inv.period_start DESC
			LIMIT $5`
		rows, err := tx.Query(ctx, q, orgID, email, from, to, limit)
		if err != nil {
			return ContribEvidence{}, fmt.Errorf("store: contribution evidence review: %w", err)
		}
		for rows.Next() {
			var it ContribEvidenceItem
			if err := rows.Scan(&it.Title, &it.Repo, &it.At); err != nil {
				rows.Close()
				return ContribEvidence{}, fmt.Errorf("store: scan evidence review: %w", err)
			}
			ev.Review = append(ev.Review, it)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return ContribEvidence{}, fmt.Errorf("store: contribution evidence review rows: %w", err)
		}
		rows.Close()
	}

	// quality: the revert/hotfix/rollback commits the member authored (the bad
	// signal the quality score inverts — shown so the score is never a hidden rank).
	{
		const q = `
			SELECT COALESCE(c.message,''), COALESCE(r.full_name,''), COALESCE(c.committed_at, now())
			FROM commits c
			LEFT JOIN repos r ON r.id = c.repo_id
			WHERE c.org_id = $1
			  AND c.committed_at >= $2 AND c.committed_at < $3
			  AND (lower(COALESCE(c.author_email::text,'')) = lower($4) OR lower(COALESCE(c.author_login,'')) = lower($5))
			  AND ` + revertPredicate + `
			ORDER BY c.committed_at DESC
			LIMIT $6`
		rows, err := tx.Query(ctx, q, orgID, from, to, email, login, limit)
		if err != nil {
			return ContribEvidence{}, fmt.Errorf("store: contribution evidence quality: %w", err)
		}
		for rows.Next() {
			var it ContribEvidenceItem
			if err := rows.Scan(&it.Message, &it.Repo, &it.At); err != nil {
				rows.Close()
				return ContribEvidence{}, fmt.Errorf("store: scan evidence quality: %w", err)
			}
			it.Title = it.Message
			ev.Quality = append(ev.Quality, it)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return ContribEvidence{}, fmt.Errorf("store: contribution evidence quality rows: %w", err)
		}
		rows.Close()
	}

	return ev, nil
}
