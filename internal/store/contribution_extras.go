// Package store — contribution_extras.go
// Org-scoped reads/writes for the three CONTRIBUTION extensions added by
// migration 20260619_014:
//
//   - contribution_snapshots — a cached composite (+ the 6 dimension scores) per
//     member per period, so "contribution over time" (trends) renders without
//     recomputing every window on every page load. Idempotent upsert.
//   - equity_allocations    — the advisory equity ledger: a contribution-weighted
//     suggested_pct (model) alongside an admin-entered actual_pct (the real grant).
//   - kudos                 — peer recognition (SPACE "satisfaction"; a partial
//     answer to reviewer collusion — a human signal that doesn't feed the score).
//
// Every function MUST run inside db.WithOrg(ctx, orgID, …) so the org_isolation
// RLS policy is active.
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ── Snapshots (trends) ──────────────────────────────────────────────────────

// ContribSnapshot is one cached composite + dimension scores for a member in a
// period. Dimensions maps the canonical dimension keys → 0–100 scores.
type ContribSnapshot struct {
	UserID      string             `json:"userId"`
	Name        string             `json:"name"`
	Email       string             `json:"email"`
	PeriodStart time.Time          `json:"periodStart"`
	PeriodEnd   time.Time          `json:"periodEnd"`
	Composite   float64            `json:"composite"`
	Dimensions  map[string]float64 `json:"dimensions"`
}

// UpsertContributionSnapshot writes (idempotently) one member's composite +
// dimension scores for a period. Re-running a snapshot job overwrites the row
// rather than duplicating it (UNIQUE on org_id,user_id,period_start,period_end).
// Must run inside db.WithOrg.
func UpsertContributionSnapshot(ctx context.Context, tx pgx.Tx, orgID, userID string, start, end time.Time, composite float64, dims map[string]float64) error {
	dimJSON, err := json.Marshal(dims)
	if err != nil {
		return fmt.Errorf("store: marshal snapshot dimensions: %w", err)
	}
	const q = `
		INSERT INTO contribution_snapshots (org_id, user_id, period_start, period_end, composite, dimensions)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)
		ON CONFLICT (org_id, user_id, period_start, period_end) DO UPDATE SET
			composite  = EXCLUDED.composite,
			dimensions = EXCLUDED.dimensions`
	if _, err := tx.Exec(ctx, q, orgID, userID, start, end, composite, string(dimJSON)); err != nil {
		return fmt.Errorf("store: upsert contribution snapshot: %w", err)
	}
	return nil
}

// ListContributionSnapshots returns every snapshot at or after `since`, joined to
// the member's name/email, oldest period first (so a trend line reads left→right).
// Must run inside db.WithOrg.
func ListContributionSnapshots(ctx context.Context, tx pgx.Tx, orgID string, since time.Time) ([]ContribSnapshot, error) {
	const q = `
		SELECT s.user_id::text, COALESCE(u.name,''), COALESCE(u.email::text,''),
		       s.period_start, s.period_end, s.composite::float8, s.dimensions
		FROM contribution_snapshots s
		LEFT JOIN users u ON u.id = s.user_id
		WHERE s.org_id = $1 AND s.period_start >= ($2)::date
		ORDER BY s.period_start ASC, s.composite DESC`
	rows, err := tx.Query(ctx, q, orgID, since)
	if err != nil {
		return nil, fmt.Errorf("store: list contribution snapshots: %w", err)
	}
	defer rows.Close()

	var out []ContribSnapshot
	for rows.Next() {
		var s ContribSnapshot
		var raw []byte
		if err := rows.Scan(&s.UserID, &s.Name, &s.Email, &s.PeriodStart, &s.PeriodEnd, &s.Composite, &raw); err != nil {
			return nil, fmt.Errorf("store: scan contribution snapshot: %w", err)
		}
		s.Dimensions = map[string]float64{}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &s.Dimensions)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ── Equity ledger ───────────────────────────────────────────────────────────

// EquityAllocation is one member's row in the advisory equity ledger: the
// contribution-weighted suggested share (model) and the admin-entered actual
// grant (which is what really happened — the model only informs it).
type EquityAllocation struct {
	UserID       string    `json:"userId"`
	Name         string    `json:"name"`
	Email        string    `json:"email"`
	PeriodStart  time.Time `json:"periodStart"`
	PeriodEnd    time.Time `json:"periodEnd"`
	SuggestedPct float64   `json:"suggestedPct"`
	ActualPct    *float64  `json:"actualPct"` // null until an admin records a real grant
	PoolLabel    string    `json:"poolLabel"`
	Note         string    `json:"note"`
}

// ListEquityAllocations returns the stored ledger rows for a period (the
// admin-entered actual_pct / pool_label / note), keyed by user. The advisory
// suggested_pct is recomputed live from contribution by the service layer, so
// callers treat the stored suggested_pct as a fallback only.
// Must run inside db.WithOrg.
func ListEquityAllocations(ctx context.Context, tx pgx.Tx, orgID string, start, end time.Time) (map[string]EquityAllocation, error) {
	const q = `
		SELECT e.user_id::text, COALESCE(u.name,''), COALESCE(u.email::text,''),
		       e.period_start, e.period_end, e.suggested_pct::float8, e.actual_pct,
		       e.pool_label, COALESCE(e.note,'')
		FROM equity_allocations e
		LEFT JOIN users u ON u.id = e.user_id
		WHERE e.org_id = $1 AND e.period_start = ($2)::date AND e.period_end = ($3)::date`
	rows, err := tx.Query(ctx, q, orgID, start, end)
	if err != nil {
		return nil, fmt.Errorf("store: list equity allocations: %w", err)
	}
	defer rows.Close()

	out := map[string]EquityAllocation{}
	for rows.Next() {
		var a EquityAllocation
		var actual *float64
		if err := rows.Scan(&a.UserID, &a.Name, &a.Email, &a.PeriodStart, &a.PeriodEnd,
			&a.SuggestedPct, &actual, &a.PoolLabel, &a.Note); err != nil {
			return nil, fmt.Errorf("store: scan equity allocation: %w", err)
		}
		a.ActualPct = actual
		out[a.UserID] = a
	}
	return out, rows.Err()
}

// UpsertEquityAllocation writes the model's suggested_pct AND any admin-entered
// actual_pct / pool_label / note for one member in a period (idempotent on
// org_id,user_id,period_start,period_end). suggestedPct is always refreshed from
// the live model; actual_pct/pool_label/note are preserved/overwritten as given.
// Must run inside db.WithOrg.
func UpsertEquityAllocation(ctx context.Context, tx pgx.Tx, a EquityAllocation, orgID string) error {
	const q = `
		INSERT INTO equity_allocations
		    (org_id, user_id, period_start, period_end, suggested_pct, actual_pct, pool_label, note)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (org_id, user_id, period_start, period_end) DO UPDATE SET
			suggested_pct = EXCLUDED.suggested_pct,
			actual_pct    = EXCLUDED.actual_pct,
			pool_label    = EXCLUDED.pool_label,
			note          = EXCLUDED.note`
	label := a.PoolLabel
	if label == "" {
		label = "Contribution pool"
	}
	if _, err := tx.Exec(ctx, q, orgID, a.UserID, a.PeriodStart, a.PeriodEnd,
		a.SuggestedPct, a.ActualPct, label, a.Note); err != nil {
		return fmt.Errorf("store: upsert equity allocation: %w", err)
	}
	return nil
}

// ── Kudos ───────────────────────────────────────────────────────────────────

// Kudo is one peer-recognition message (giver → recipient), optionally tied to a
// contribution dimension.
type Kudo struct {
	ID        string    `json:"id"`
	FromUser  string    `json:"fromUser"`
	FromName  string    `json:"fromName"`
	ToUser    string    `json:"toUser"`
	ToName    string    `json:"toName"`
	Dimension string    `json:"dimension,omitempty"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"createdAt"`
}

// InsertKudo records one kudos. The caller enforces from != to. Must run inside
// db.WithOrg (org_id pinned by RLS).
func InsertKudo(ctx context.Context, tx pgx.Tx, orgID, fromUser, toUser, dimension, message string) (Kudo, error) {
	var dim *string
	if dimension != "" {
		dim = &dimension
	}
	const q = `
		INSERT INTO kudos (org_id, from_user, to_user, dimension, message)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id::text, created_at`
	var k Kudo
	if err := tx.QueryRow(ctx, q, orgID, fromUser, toUser, dim, message).Scan(&k.ID, &k.CreatedAt); err != nil {
		return Kudo{}, fmt.Errorf("store: insert kudo: %w", err)
	}
	k.FromUser, k.ToUser, k.Dimension, k.Message = fromUser, toUser, dimension, message
	return k, nil
}

// ListKudos returns recognition messages for the org, newest first. When toUser
// is non-empty it filters to that recipient. Must run inside db.WithOrg.
func ListKudos(ctx context.Context, tx pgx.Tx, orgID, toUser string, limit int) ([]Kudo, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	q := `
		SELECT k.id::text, k.from_user::text, COALESCE(fu.name, fu.email::text, ''),
		       k.to_user::text, COALESCE(tu.name, tu.email::text, ''),
		       COALESCE(k.dimension,''), k.message, k.created_at
		FROM kudos k
		LEFT JOIN users fu ON fu.id = k.from_user
		LEFT JOIN users tu ON tu.id = k.to_user
		WHERE k.org_id = $1`
	args := []any{orgID}
	if toUser != "" {
		q += ` AND k.to_user = $2`
		args = append(args, toUser)
	}
	q += fmt.Sprintf(` ORDER BY k.created_at DESC LIMIT %d`, limit)

	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list kudos: %w", err)
	}
	defer rows.Close()

	var out []Kudo
	for rows.Next() {
		var k Kudo
		if err := rows.Scan(&k.ID, &k.FromUser, &k.FromName, &k.ToUser, &k.ToName,
			&k.Dimension, &k.Message, &k.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scan kudo: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// KudosCounts returns kudos-received counts per recipient user_id across the org
// (a human signal the Contribution roster can surface beside each member).
// Must run inside db.WithOrg.
func KudosCounts(ctx context.Context, tx pgx.Tx, orgID string) (map[string]int, error) {
	const q = `
		SELECT to_user::text, COUNT(*)::int
		FROM kudos
		WHERE org_id = $1
		GROUP BY to_user`
	rows, err := tx.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("store: kudos counts: %w", err)
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var userID string
		var n int
		if err := rows.Scan(&userID, &n); err != nil {
			return nil, fmt.Errorf("store: scan kudos count: %w", err)
		}
		out[userID] = n
	}
	return out, rows.Err()
}
