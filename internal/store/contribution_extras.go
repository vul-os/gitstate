// Package store — contribution_extras.go
// Org-scoped reads/writes for the three CONTRIBUTION extensions added by
// migration 20260619_014:
//
//   - contribution_snapshots — a cached composite (+ the 6 dimension scores) per
//     member per period, so "contribution over time" (trends) renders without
//     recomputing every window on every page load. Idempotent upsert.
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
	ContributorID string             `json:"contributorId"`
	UserID        string             `json:"userId"`
	Name          string             `json:"name"`
	Email         string             `json:"email"`
	PeriodStart   time.Time          `json:"periodStart"`
	PeriodEnd     time.Time          `json:"periodEnd"`
	Composite     float64            `json:"composite"`
	Dimensions    map[string]float64 `json:"dimensions"`
}

// UpsertContributionSnapshot writes (idempotently) one member's composite +
// dimension scores for a period, keyed by a linked user. Thin wrapper over
// UpsertPersonSnapshot with no contributor id (back-compat for the user-keyed
// callers — e.g. the demo seed). Must run inside db.WithOrg.
func UpsertContributionSnapshot(ctx context.Context, tx pgx.Tx, orgID, userID string, start, end time.Time, composite float64, dims map[string]float64) error {
	return UpsertPersonSnapshot(ctx, tx, orgID, "", userID, start, end, composite, dims)
}

// UpsertPersonSnapshot writes (idempotently) one PERSON's composite + dimension
// scores for a period, keyed by the canonical contributor (the person) and/or a
// linked user. At least one of contributorID/userID must be non-empty; "" is
// stored as NULL. Re-running overwrites the row rather than duplicating it (UNIQUE
// on org_id, COALESCE(contributor_id,user_id), period_start, period_end — see
// migration 20260624_001). Must run inside db.WithOrg.
func UpsertPersonSnapshot(ctx context.Context, tx pgx.Tx, orgID, contributorID, userID string, start, end time.Time, composite float64, dims map[string]float64) error {
	dimJSON, err := json.Marshal(dims)
	if err != nil {
		return fmt.Errorf("store: marshal snapshot dimensions: %w", err)
	}
	const q = `
		INSERT INTO contribution_snapshots (org_id, contributor_id, user_id, period_start, period_end, composite, dimensions)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)
		ON CONFLICT (org_id, COALESCE(contributor_id, user_id), period_start, period_end) DO UPDATE SET
			composite      = EXCLUDED.composite,
			dimensions     = EXCLUDED.dimensions,
			contributor_id = EXCLUDED.contributor_id,
			user_id        = EXCLUDED.user_id`
	if _, err := tx.Exec(ctx, q, orgID, nullString(contributorID), nullString(userID), start, end, composite, string(dimJSON)); err != nil {
		return fmt.Errorf("store: upsert contribution snapshot: %w", err)
	}
	return nil
}

// ListContributionSnapshots returns every snapshot at or after `since`, joined to
// the member's name/email, oldest period first (so a trend line reads left→right).
// Must run inside db.WithOrg.
func ListContributionSnapshots(ctx context.Context, tx pgx.Tx, orgID string, since time.Time) ([]ContribSnapshot, error) {
	const q = `
		SELECT COALESCE(s.contributor_id::text,''), COALESCE(s.user_id::text,''),
		       COALESCE(c.display_name, u.name, ''),
		       COALESCE(c.primary_email, u.email::text, ''),
		       s.period_start, s.period_end, s.composite::float8, s.dimensions
		FROM contribution_snapshots s
		LEFT JOIN users u ON u.id = s.user_id
		LEFT JOIN contributors c ON c.id = s.contributor_id
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
		if err := rows.Scan(&s.ContributorID, &s.UserID, &s.Name, &s.Email, &s.PeriodStart, &s.PeriodEnd, &s.Composite, &raw); err != nil {
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
