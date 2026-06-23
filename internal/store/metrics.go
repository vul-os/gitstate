// Package store — metrics.go
// Org-scoped queries for cycle_times and involvement tables (decisions P2/P3).
// All writes use db.WithOrg (pgx.Tx) so RLS enforces the org boundary.
// Reads that span multiple rows use the pool directly; callers are responsible
// for ensuring the RLS context is set (OrgScope middleware or WithOrg).
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ── CycleTime ────────────────────────────────────────────────────────────────

// CycleTime mirrors a row from the cycle_times table (DORA-style, decisions P3).
// lead_time_secs = first_commit_at → merged_at for the PR.
// review_secs    = open → merged (currently stored as created_at → merged_at).
type CycleTime struct {
	ID           string
	OrgID        string
	PRID         *string // nullable — may be computed in bulk without a PR ref
	LeadTimeSecs *int64
	ReviewSecs   *int64
	ComputedAt   time.Time

	// PR context, joined for the reporting layer (the chart plots lead time
	// chronologically by MergedAt, not by ComputedAt). Empty/zero when the row
	// has no pr_id.
	MergedAt time.Time
	Title    string
	Repo     string
}

// UpsertCycleTime replaces the cycle_time measurement for the given PR.
//
// cycle_times has no unique constraint on pr_id (only the id PK), so a plain
// INSERT … ON CONFLICT DO NOTHING never matches an existing measurement — it
// would append a fresh duplicate row on every recompute, tripling/quadrupling
// the sample and skewing p50/p90/avg. To keep recompute idempotent we delete
// any prior measurement for this pr_id (within the caller's org-scoped tx, so
// RLS bounds the delete to this org) and then insert the latest.
//
// Rows with a nil pr_id (rare bulk measurements without a PR ref) are inserted
// unconditionally — there is no key to dedup them on.
//
// Callers: pass a tx from db.WithOrg so RLS is active.
func UpsertCycleTime(ctx context.Context, tx pgx.Tx, ct CycleTime) error {
	if ct.PRID != nil {
		const del = `DELETE FROM cycle_times WHERE org_id = $1 AND pr_id = $2`
		if _, err := tx.Exec(ctx, del, ct.OrgID, *ct.PRID); err != nil {
			return fmt.Errorf("store.metrics: clear prior cycle_time: %w", err)
		}
	}

	const q = `
		INSERT INTO cycle_times (org_id, pr_id, lead_time_secs, review_secs, computed_at)
		VALUES ($1, $2, $3, $4, now())`
	_, err := tx.Exec(ctx, q,
		ct.OrgID,
		ct.PRID,
		ct.LeadTimeSecs,
		ct.ReviewSecs,
	)
	if err != nil {
		return fmt.Errorf("store.metrics: upsert cycle_time: %w", err)
	}
	return nil
}

// DeleteCycleTimeForPR removes any cycle_time measurement for a PR. Used to
// self-heal rows that should no longer exist (e.g. a PR re-classified as
// bot-authored and excluded from human cycle-time stats). Org-scoped via the
// caller's WithOrg tx and the explicit org_id predicate.
func DeleteCycleTimeForPR(ctx context.Context, tx pgx.Tx, orgID, prID string) error {
	if _, err := tx.Exec(ctx,
		`DELETE FROM cycle_times WHERE org_id = $1 AND pr_id = $2`, orgID, prID); err != nil {
		return fmt.Errorf("store.metrics: delete cycle_time for pr: %w", err)
	}
	return nil
}

// CycleTimeFilter narrows ListCycleTimes queries.
type CycleTimeFilter struct {
	RepoID string    // optional — filter to a specific repo's PRs
	From   time.Time // optional — computed_at >= From
	To     time.Time // optional — computed_at <= To

	// AuthorIdentities, when non-empty, restricts to PRs whose author_login is ANY
	// of the given lowercased identities — the full set of a grouped contributor's
	// git identities. PRs carry only a login, so email identities simply never
	// match (harmless). Populated by the service layer when an author filter is a
	// `contributor:<uuid>` token expanded via ContributorIdentityValues.
	AuthorIdentities []string
}

// Querier is satisfied by both *pgxpool.Pool and pgx.Tx, so these reads can run
// inside a db.WithOrg transaction (which sets app.current_org for RLS). Running
// them on a bare pool returns ZERO rows now that RLS is enforced.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// ListCycleTimes returns one cycle_time measurement per merged PR for an org,
// ordered chronologically by the PR's merged_at (oldest → newest) so the chart
// reads as a real lead-time-over-time series.
//
// Because cycle_times has no unique constraint on pr_id, historical recomputes
// may have left several rows per PR (see UpsertCycleTime). DISTINCT ON (pr_id)
// with the inner ORDER BY collapses those to the latest measurement, so stats
// computed downstream never double-count a PR.
//
// The join carries the PR's merged_at / title and the repo full_name so the
// reporting layer can plot and label without a second round-trip. The `From`/
// `To` filter is applied to the PR's merged_at (the meaningful event time), not
// to computed_at.
//
// MUST be called inside db.WithOrg(ctx, orgID, …) so the RLS context is set.
func ListCycleTimes(ctx context.Context, qr Querier, orgID string, f CycleTimeFilter) ([]*CycleTime, error) {
	args := []interface{}{orgID}
	where := " WHERE ct.org_id = $1 AND ct.pr_id IS NOT NULL"
	idx := 2

	if f.RepoID != "" {
		where += fmt.Sprintf(" AND pr.repo_id = $%d", idx)
		args = append(args, f.RepoID)
		idx++
	}
	if !f.From.IsZero() {
		where += fmt.Sprintf(" AND pr.merged_at >= $%d", idx)
		args = append(args, f.From)
		idx++
	}
	if !f.To.IsZero() {
		where += fmt.Sprintf(" AND pr.merged_at <= $%d", idx)
		args = append(args, f.To)
		idx++
	}
	if len(f.AuthorIdentities) > 0 {
		where += fmt.Sprintf(" AND lower(COALESCE(pr.author_login,'')) = ANY($%d)", idx)
		args = append(args, f.AuthorIdentities)
		idx++ //nolint:ineffassign // idx kept for future extension
	}

	// Inner: latest measurement per PR. Outer: chronological by merge time.
	q := `
		SELECT id, org_id, pr_id, lead_time_secs, review_secs, computed_at,
		       merged_at, title, repo
		FROM (
			SELECT DISTINCT ON (ct.pr_id)
			       ct.id, ct.org_id, ct.pr_id::text AS pr_id,
			       ct.lead_time_secs, ct.review_secs, ct.computed_at,
			       pr.merged_at, pr.title, COALESCE(r.full_name, '') AS repo
			FROM cycle_times ct
			JOIN pull_requests pr ON pr.id = ct.pr_id
			LEFT JOIN repos r ON r.id = pr.repo_id` + where + `
			ORDER BY ct.pr_id, ct.computed_at DESC
		) latest
		ORDER BY merged_at ASC, computed_at ASC`

	rows, err := qr.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store.metrics: list cycle_times: %w", err)
	}
	defer rows.Close()

	var out []*CycleTime
	for rows.Next() {
		ct := &CycleTime{}
		var mergedAt *time.Time
		if err := rows.Scan(
			&ct.ID, &ct.OrgID, &ct.PRID,
			&ct.LeadTimeSecs, &ct.ReviewSecs, &ct.ComputedAt,
			&mergedAt, &ct.Title, &ct.Repo,
		); err != nil {
			return nil, fmt.Errorf("store.metrics: scan cycle_time: %w", err)
		}
		if mergedAt != nil {
			ct.MergedAt = *mergedAt
		}
		out = append(out, ct)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store.metrics: list cycle_times rows: %w", err)
	}
	return out, nil
}

// ── Involvement ───────────────────────────────────────────────────────────────
//
// Involvement is stored as TEXTURE across multiple dimensions (decisions P2).
// There is NO composite score; each dimension is an independent fact derived
// from observable git / PR activity. The extensible `dimensions` jsonb column
// accepts additional metrics without schema changes.

// Involvement mirrors a row from the involvement table.
type Involvement struct {
	ID              string
	OrgID           string
	ProjectID       *string
	UserID          *string
	PeriodStart     time.Time
	FeaturesShipped int               // merged PRs authored by this user this period
	ReviewsDone     int               // PRs reviewed (the invisible senior work — decisions P2)
	AreasOwned      int               // distinct file areas touched / owned via git history
	Active          bool              // true if any activity in this period
	Dimensions      map[string]interface{} // extensible texture (no scoring, no composite)
}

// InvolvementUpsertInput carries all fields needed to upsert an involvement row.
// It deliberately omits any "score" or "rank" field to enforce decisions P2.
type InvolvementUpsertInput struct {
	OrgID           string
	ProjectID       *string
	UserID          *string
	PeriodStart     time.Time
	FeaturesShipped int
	ReviewsDone     int
	AreasOwned      int
	Active          bool
	Dimensions      map[string]interface{}
}

// UpsertInvolvement inserts or updates an involvement row.
// The unique constraint is (org_id, project_id, user_id, period_start).
//
// On conflict the git-derived dimensions (features_shipped, areas_owned, active,
// commit-volume dimensions) are refreshed, but reviews_done is PRESERVED:
// gitstate's sync layer has no review-event signal, so the metrics recompute
// passes ReviewsDone=0 and must not clobber a richer value already in the row
// (e.g. seeded review texture, or a future review-event importer). Use GREATEST
// so a genuinely larger incoming value still wins, but a 0 never erases data.
//
// Callers: pass a tx from db.WithOrg so RLS is active.
func UpsertInvolvement(ctx context.Context, tx pgx.Tx, in InvolvementUpsertInput) error {
	dimJSON, err := json.Marshal(in.Dimensions)
	if err != nil {
		return fmt.Errorf("store.metrics: marshal dimensions: %w", err)
	}
	if in.Dimensions == nil {
		dimJSON = []byte("{}")
	}

	const q = `
		INSERT INTO involvement
		    (org_id, project_id, user_id, period_start,
		     features_shipped, reviews_done, areas_owned, active, dimensions)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (org_id, project_id, user_id, period_start) DO UPDATE SET
		    features_shipped = EXCLUDED.features_shipped,
		    reviews_done     = GREATEST(involvement.reviews_done, EXCLUDED.reviews_done),
		    areas_owned      = EXCLUDED.areas_owned,
		    active           = EXCLUDED.active,
		    dimensions       = EXCLUDED.dimensions`

	_, err = tx.Exec(ctx, q,
		in.OrgID,
		in.ProjectID,
		in.UserID,
		in.PeriodStart,
		in.FeaturesShipped,
		in.ReviewsDone,
		in.AreasOwned,
		in.Active,
		dimJSON,
	)
	if err != nil {
		return fmt.Errorf("store.metrics: upsert involvement: %w", err)
	}
	return nil
}

// ReplaceUserInvolvement makes the metrics recompute the single authoritative
// source for one (user, period): it deletes EVERY existing involvement row for
// that user+period (across all project partitions, including any legacy
// per-project rows) and writes exactly one org-level row (project_id = NULL).
//
// Why delete-then-insert instead of UpsertInvolvement's ON CONFLICT: the unique
// constraint is (org_id, project_id, user_id, period_start), and Postgres treats
// NULL project_id as DISTINCT — so an INSERT … ON CONFLICT with a NULL project
// NEVER matches and would append an unbounded duplicate on every recompute.
// Collapsing to one row also prevents the reporting aggregation from double-
// counting a user who otherwise had both a recompute row and seeded per-project
// rows for the same month.
//
// reviews_done is carried forward as the MAX over the deleted rows so a richer
// signal (seeded reviewer texture, or a future review-event importer) survives a
// recompute that itself has no review data (in.ReviewsDone is typically 0).
//
// Callers: pass a tx from db.WithOrg so RLS bounds the delete + insert to the org.
func ReplaceUserInvolvement(ctx context.Context, tx pgx.Tx, in InvolvementUpsertInput) error {
	if in.UserID == nil || *in.UserID == "" {
		return fmt.Errorf("store.metrics: ReplaceUserInvolvement requires a user_id")
	}
	dimJSON, err := json.Marshal(in.Dimensions)
	if err != nil {
		return fmt.Errorf("store.metrics: marshal dimensions: %w", err)
	}
	if in.Dimensions == nil {
		dimJSON = []byte("{}")
	}

	// Preserve the strongest prior reviews_done across the rows we are about to
	// replace (covers both the org-level row and any per-project rows).
	var priorReviews int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(reviews_done), 0)
		FROM involvement
		WHERE org_id = $1 AND user_id = $2 AND period_start = $3`,
		in.OrgID, *in.UserID, in.PeriodStart).Scan(&priorReviews); err != nil {
		return fmt.Errorf("store.metrics: read prior reviews_done: %w", err)
	}
	reviews := in.ReviewsDone
	if priorReviews > reviews {
		reviews = priorReviews
	}

	if _, err := tx.Exec(ctx, `
		DELETE FROM involvement
		WHERE org_id = $1 AND user_id = $2 AND period_start = $3`,
		in.OrgID, *in.UserID, in.PeriodStart); err != nil {
		return fmt.Errorf("store.metrics: clear prior involvement: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO involvement
		    (org_id, project_id, user_id, period_start,
		     features_shipped, reviews_done, areas_owned, active, dimensions)
		VALUES ($1, NULL, $2, $3, $4, $5, $6, $7, $8)`,
		in.OrgID, *in.UserID, in.PeriodStart,
		in.FeaturesShipped, reviews, in.AreasOwned, in.Active, dimJSON,
	); err != nil {
		return fmt.Errorf("store.metrics: insert involvement: %w", err)
	}
	return nil
}

// InvolvementFilter narrows ListInvolvement queries.
type InvolvementFilter struct {
	ProjectID   string    // optional
	PeriodStart time.Time // optional — exact period start date match
}

// ListInvolvement returns involvement rows for an org, sorted by user_id then
// period_start descending. MUST be called inside db.WithOrg so RLS context is set.
func ListInvolvement(ctx context.Context, qr Querier, orgID string, f InvolvementFilter) ([]*Involvement, error) {
	q := `
		SELECT id, org_id, project_id::text, user_id::text,
		       period_start, features_shipped, reviews_done, areas_owned,
		       active, dimensions
		FROM involvement
		WHERE org_id = $1`

	args := []interface{}{orgID}
	idx := 2

	if f.ProjectID != "" {
		q += fmt.Sprintf(" AND project_id = $%d", idx)
		args = append(args, f.ProjectID)
		idx++
	}
	if !f.PeriodStart.IsZero() {
		q += fmt.Sprintf(" AND period_start = $%d", idx)
		args = append(args, f.PeriodStart)
		idx++ //nolint:ineffassign
	}

	q += " ORDER BY user_id, period_start DESC"

	rows, err := qr.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store.metrics: list involvement: %w", err)
	}
	defer rows.Close()

	var out []*Involvement
	for rows.Next() {
		inv := &Involvement{}
		var dimRaw []byte
		if err := rows.Scan(
			&inv.ID, &inv.OrgID, &inv.ProjectID, &inv.UserID,
			&inv.PeriodStart, &inv.FeaturesShipped, &inv.ReviewsDone,
			&inv.AreasOwned, &inv.Active, &dimRaw,
		); err != nil {
			return nil, fmt.Errorf("store.metrics: scan involvement: %w", err)
		}
		if len(dimRaw) > 0 {
			if err := json.Unmarshal(dimRaw, &inv.Dimensions); err != nil {
				return nil, fmt.Errorf("store.metrics: unmarshal dimensions: %w", err)
			}
		}
		if inv.Dimensions == nil {
			inv.Dimensions = make(map[string]interface{})
		}
		out = append(out, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store.metrics: list involvement rows: %w", err)
	}
	return out, nil
}

// InvolvementMember is one person's involvement TEXTURE, aggregated across every
// involvement period that falls inside the requested window and joined to the
// user for display. This is the shape the reporting layer renders: one card per
// person, never per (person × month × project) — which is what the raw rows are.
//
// There is intentionally NO composite score (decisions P2): each field is an
// independent observable fact.
type InvolvementMember struct {
	UserID          string
	Name            string
	Email           string
	AvatarURL       string
	FeaturesShipped int
	ReviewsDone     int
	AreasOwned      int   // max distinct areas owned across the window's periods
	Active          bool  // active in any period within the window
	LastActive      time.Time
	IsAgent         bool
	CommitCount     int
	LinesAdded      int
	LinesDeleted    int
}

// ListInvolvementMembers returns aggregated per-user involvement for the org over
// involvement periods with period_start >= windowStart (windowStart zero = all
// history). Rows are grouped by user so each person appears exactly once, sorted
// by features_shipped + reviews_done descending (display order only — NOT a rank
// or score persisted anywhere). projectID optionally narrows to one project.
//
// MUST be called inside db.WithOrg so the RLS org context is set; orphan rows
// with a NULL user_id are excluded (they can't be attributed to a person).
func ListInvolvementMembers(ctx context.Context, qr Querier, orgID string, windowStart time.Time, projectID string) ([]*InvolvementMember, error) {
	q := `
		SELECT u.id::text,
		       COALESCE(u.name,''), COALESCE(u.email::text,''), COALESCE(u.avatar_url,''),
		       COALESCE(SUM(i.features_shipped),0),
		       COALESCE(SUM(i.reviews_done),0),
		       COALESCE(MAX(i.areas_owned),0),
		       bool_or(i.active),
		       MAX(i.period_start),
		       COALESCE(bool_and((i.dimensions->>'is_agent')::boolean), false),
		       COALESCE(SUM((i.dimensions->>'commit_count')::int),0),
		       COALESCE(SUM((i.dimensions->>'lines_added')::int),0),
		       COALESCE(SUM((i.dimensions->>'lines_deleted')::int),0)
		FROM involvement i
		JOIN users u ON u.id = i.user_id
		WHERE i.org_id = $1`

	args := []interface{}{orgID}
	idx := 2
	if !windowStart.IsZero() {
		q += fmt.Sprintf(" AND i.period_start >= $%d", idx)
		args = append(args, windowStart)
		idx++
	}
	if projectID != "" {
		q += fmt.Sprintf(" AND i.project_id = $%d", idx)
		args = append(args, projectID)
		idx++ //nolint:ineffassign
	}
	q += `
		GROUP BY u.id, u.name, u.email, u.avatar_url
		ORDER BY (COALESCE(SUM(i.features_shipped),0) + COALESCE(SUM(i.reviews_done),0)) DESC,
		         lower(COALESCE(u.name, u.email::text)) ASC`

	rows, err := qr.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store.metrics: list involvement members: %w", err)
	}
	defer rows.Close()

	var out []*InvolvementMember
	for rows.Next() {
		m := &InvolvementMember{}
		var last *time.Time
		if err := rows.Scan(
			&m.UserID, &m.Name, &m.Email, &m.AvatarURL,
			&m.FeaturesShipped, &m.ReviewsDone, &m.AreasOwned,
			&m.Active, &last, &m.IsAgent,
			&m.CommitCount, &m.LinesAdded, &m.LinesDeleted,
		); err != nil {
			return nil, fmt.Errorf("store.metrics: scan involvement member: %w", err)
		}
		if last != nil {
			m.LastActive = *last
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store.metrics: list involvement members rows: %w", err)
	}
	return out, nil
}
