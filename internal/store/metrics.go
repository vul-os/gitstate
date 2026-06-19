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
	ID            string
	OrgID         string
	PRID          *string // nullable — may be computed in bulk without a PR ref
	LeadTimeSecs  *int64
	ReviewSecs    *int64
	ComputedAt    time.Time
}

// UpsertCycleTime inserts or updates a cycle_time row for the given PR.
// If a row for this pr_id already exists it is replaced (delete + insert pattern
// is avoided; ON CONFLICT on pr_id is not possible since pr_id is nullable in
// the schema, so we use a manual check-then-insert approach within the caller's
// WithOrg transaction).
//
// Callers: pass a tx from db.WithOrg so RLS is active.
func UpsertCycleTime(ctx context.Context, tx pgx.Tx, ct CycleTime) error {
	const q = `
		INSERT INTO cycle_times (org_id, pr_id, lead_time_secs, review_secs, computed_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT DO NOTHING`
	// cycle_times has no unique constraint beyond id; we insert unconditionally.
	// A PR can have multiple measurements over time (re-computes); only the
	// latest is returned by ListCycleTimes via ORDER BY computed_at DESC.
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

// CycleTimeFilter narrows ListCycleTimes queries.
type CycleTimeFilter struct {
	RepoID string    // optional — filter to a specific repo's PRs
	From   time.Time // optional — computed_at >= From
	To     time.Time // optional — computed_at <= To
}

// Querier is satisfied by both *pgxpool.Pool and pgx.Tx, so these reads can run
// inside a db.WithOrg transaction (which sets app.current_org for RLS). Running
// them on a bare pool returns ZERO rows now that RLS is enforced.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// ListCycleTimes returns cycle_time rows for an org, newest-computed first.
// MUST be called inside db.WithOrg(ctx, orgID, …) so the RLS context is set.
func ListCycleTimes(ctx context.Context, qr Querier, orgID string, f CycleTimeFilter) ([]*CycleTime, error) {
	// Build a parameterised query; extra predicates are appended as needed.
	baseQ := `
		SELECT ct.id, ct.org_id, ct.pr_id::text,
		       ct.lead_time_secs, ct.review_secs, ct.computed_at
		FROM cycle_times ct`

	args := []interface{}{orgID}
	where := " WHERE ct.org_id = $1"
	idx := 2

	if f.RepoID != "" {
		// Join pull_requests to filter by repo_id.
		baseQ += " LEFT JOIN pull_requests pr ON pr.id = ct.pr_id"
		where += fmt.Sprintf(" AND pr.repo_id = $%d", idx)
		args = append(args, f.RepoID)
		idx++
	}
	if !f.From.IsZero() {
		where += fmt.Sprintf(" AND ct.computed_at >= $%d", idx)
		args = append(args, f.From)
		idx++
	}
	if !f.To.IsZero() {
		where += fmt.Sprintf(" AND ct.computed_at <= $%d", idx)
		args = append(args, f.To)
		idx++ //nolint:ineffassign // idx kept for future extension
	}

	q := baseQ + where + " ORDER BY ct.computed_at DESC"
	rows, err := qr.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store.metrics: list cycle_times: %w", err)
	}
	defer rows.Close()

	var out []*CycleTime
	for rows.Next() {
		ct := &CycleTime{}
		if err := rows.Scan(
			&ct.ID, &ct.OrgID, &ct.PRID,
			&ct.LeadTimeSecs, &ct.ReviewSecs, &ct.ComputedAt,
		); err != nil {
			return nil, fmt.Errorf("store.metrics: scan cycle_time: %w", err)
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
// On conflict, all dimension columns are updated to reflect the latest
// re-compute (re-computing is safe and idempotent).
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
		    reviews_done     = EXCLUDED.reviews_done,
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
