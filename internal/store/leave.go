// Package store — leave-management CRUD (richer leave model).
//
// Adds configurable leave TYPES and per-user BALANCES (entitled / carried /
// used / remaining) on top of the existing leave_entries table. Inspired by
// dedicated leave tools: a balance is keyed by (user, type, year), used_days is
// derived from APPROVED leave_entries that carry that type, and "remaining" is
// entitled + carried − used.
//
// Every function runs inside a db.WithOrg transaction so RLS applies on
// leave_types / leave_balances (org_isolation policies, migration 008).
package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// ── Leave types ─────────────────────────────────────────────────────────────

// LeaveType mirrors a row from leave_types.
type LeaveType struct {
	ID               string
	OrgID            string
	Name             string
	Color            string
	DefaultDays      float64
	RequiresApproval bool
	Accrues          bool
	CarryoverMax     float64
	Paid             bool
	Archived         bool
	CreatedAt        time.Time
}

const leaveTypeCols = `id, org_id, name, color, default_days, requires_approval, accrues, carryover_max, paid, archived, created_at`

func scanLeaveType(row pgx.Row) (*LeaveType, error) {
	var t LeaveType
	err := row.Scan(
		&t.ID, &t.OrgID, &t.Name, &t.Color, &t.DefaultDays,
		&t.RequiresApproval, &t.Accrues, &t.CarryoverMax, &t.Paid,
		&t.Archived, &t.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ListLeaveTypes returns leave types for the org. When includeArchived is false
// archived types are filtered out.
func ListLeaveTypes(ctx context.Context, tx pgx.Tx, orgID string, includeArchived bool) ([]*LeaveType, error) {
	query := `SELECT ` + leaveTypeCols + ` FROM leave_types WHERE org_id = $1`
	if !includeArchived {
		query += ` AND archived = false`
	}
	query += ` ORDER BY archived, name`

	rows, err := tx.Query(ctx, query, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*LeaveType
	for rows.Next() {
		t, err := scanLeaveType(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetLeaveType returns a single leave type by id (org-scoped), or ErrNotFound.
func GetLeaveType(ctx context.Context, tx pgx.Tx, orgID, id string) (*LeaveType, error) {
	t, err := scanLeaveType(tx.QueryRow(ctx,
		`SELECT `+leaveTypeCols+` FROM leave_types WHERE org_id = $1 AND id = $2`, orgID, id))
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	return t, err
}

// CreateLeaveType inserts a new leave type. On a name conflict (per org) it
// returns the existing row updated with the supplied attributes (un-archiving it
// if needed) so the operation is idempotent for seeding and re-creation.
func CreateLeaveType(ctx context.Context, tx pgx.Tx, t *LeaveType) (*LeaveType, error) {
	if t.Color == "" {
		t.Color = "#2DD4BF"
	}
	return scanLeaveType(tx.QueryRow(ctx, `
		INSERT INTO leave_types
		    (org_id, name, color, default_days, requires_approval, accrues, carryover_max, paid)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (org_id, name) DO UPDATE SET
		    color             = EXCLUDED.color,
		    default_days      = EXCLUDED.default_days,
		    requires_approval = EXCLUDED.requires_approval,
		    accrues           = EXCLUDED.accrues,
		    carryover_max     = EXCLUDED.carryover_max,
		    paid              = EXCLUDED.paid,
		    archived          = false
		RETURNING `+leaveTypeCols,
		t.OrgID, t.Name, t.Color, t.DefaultDays, t.RequiresApproval,
		t.Accrues, t.CarryoverMax, t.Paid))
}

// LeaveTypePatch carries the optional fields of an update. Nil pointers are left
// untouched (partial PATCH semantics).
type LeaveTypePatch struct {
	Name             *string
	Color            *string
	DefaultDays      *float64
	RequiresApproval *bool
	Accrues          *bool
	CarryoverMax     *float64
	Paid             *bool
	Archived         *bool
}

// UpdateLeaveType applies a partial patch to a leave type, returning the updated
// row or ErrNotFound. COALESCE keeps existing values for nil patch fields.
func UpdateLeaveType(ctx context.Context, tx pgx.Tx, orgID, id string, p LeaveTypePatch) (*LeaveType, error) {
	t, err := scanLeaveType(tx.QueryRow(ctx, `
		UPDATE leave_types SET
		    name              = COALESCE($3, name),
		    color             = COALESCE($4, color),
		    default_days      = COALESCE($5, default_days),
		    requires_approval = COALESCE($6, requires_approval),
		    accrues           = COALESCE($7, accrues),
		    carryover_max     = COALESCE($8, carryover_max),
		    paid              = COALESCE($9, paid),
		    archived          = COALESCE($10, archived)
		WHERE org_id = $1 AND id = $2
		RETURNING `+leaveTypeCols,
		orgID, id, p.Name, p.Color, p.DefaultDays, p.RequiresApproval,
		p.Accrues, p.CarryoverMax, p.Paid, p.Archived))
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	return t, err
}

// ── Leave balances ──────────────────────────────────────────────────────────

// LeaveBalance mirrors a row from leave_balances plus the derived Remaining.
type LeaveBalance struct {
	ID           string
	OrgID        string
	UserID       string
	LeaveTypeID  string
	Year         int
	EntitledDays float64
	CarriedDays  float64
	UsedDays     float64
	UpdatedAt    time.Time
}

// Remaining is entitled + carried − used, never below zero.
func (b *LeaveBalance) Remaining() float64 {
	r := b.EntitledDays + b.CarriedDays - b.UsedDays
	if r < 0 {
		return 0
	}
	return r
}

const leaveBalanceCols = `id, org_id, user_id, leave_type_id, year, entitled_days, carried_days, used_days, updated_at`

func scanLeaveBalance(row pgx.Row) (*LeaveBalance, error) {
	var b LeaveBalance
	err := row.Scan(
		&b.ID, &b.OrgID, &b.UserID, &b.LeaveTypeID, &b.Year,
		&b.EntitledDays, &b.CarriedDays, &b.UsedDays, &b.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ListLeaveBalances returns balances for a user in a given year. When userID is
// empty it returns balances for the whole org (year-scoped).
func ListLeaveBalances(ctx context.Context, tx pgx.Tx, orgID, userID string, year int) ([]*LeaveBalance, error) {
	query := `SELECT ` + leaveBalanceCols + ` FROM leave_balances WHERE org_id = $1 AND year = $2`
	args := []any{orgID, year}
	if userID != "" {
		query += ` AND user_id = $3`
		args = append(args, userID)
	}
	query += ` ORDER BY user_id, leave_type_id`

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*LeaveBalance
	for rows.Next() {
		b, err := scanLeaveBalance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// UpsertLeaveBalance inserts or updates a balance row (keyed on
// org/user/type/year). used_days is left to RecomputeUsedDays; this sets the
// entitlement / carryover.
func UpsertLeaveBalance(ctx context.Context, tx pgx.Tx, orgID, userID, leaveTypeID string, year int, entitled, carried float64) (*LeaveBalance, error) {
	return scanLeaveBalance(tx.QueryRow(ctx, `
		INSERT INTO leave_balances
		    (org_id, user_id, leave_type_id, year, entitled_days, carried_days)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (org_id, user_id, leave_type_id, year) DO UPDATE SET
		    entitled_days = EXCLUDED.entitled_days,
		    carried_days  = EXCLUDED.carried_days,
		    updated_at    = now()
		RETURNING `+leaveBalanceCols,
		orgID, userID, leaveTypeID, year, entitled, carried))
}

// countApprovedLeaveDays returns the number of leave-days a user has consumed in
// a year for a given leave type, summing across approved leave_entries. Each
// entry contributes (inclusive day-span) days, or 0.5 when half_day is set.
//
// This mirrors the capacity math at a coarser (whole-day) grain: it is the
// authoritative "used_days" figure for a balance.
func countApprovedLeaveDays(ctx context.Context, tx pgx.Tx, orgID, userID, leaveTypeID string, year int) (float64, error) {
	var total float64
	// Count only the days that fall WITHIN the target year: clamp each entry's
	// [start_date, end_date] span to [Jan 1, Dec 31] of the year before measuring.
	// A Dec→Jan entry therefore contributes its December days to this year and its
	// January days to the next. (end - start) is integer days; +1 = inclusive.
	// A half_day entry is a single day → 0.5, counted in the year it starts.
	err := tx.QueryRow(ctx, `
		WITH bounds AS (
		    SELECT make_date($4, 1, 1) AS y_start,
		           make_date($4, 12, 31) AS y_end
		)
		SELECT COALESCE(SUM(
		    CASE WHEN half_day THEN 0.5
		         ELSE (LEAST(end_date, y_end) - GREATEST(start_date, y_start)) + 1 END
		), 0)::numeric
		FROM leave_entries, bounds
		WHERE org_id = $1
		  AND user_id = $2
		  AND leave_type_id = $3
		  AND status = 'approved'
		  AND start_date <= y_end
		  AND end_date   >= y_start`,
		orgID, userID, leaveTypeID, year).Scan(&total)
	return total, err
}

// RecomputeUsedDays recalculates used_days for a single (user, type, year)
// balance from approved leave_entries and persists it. If no balance row exists
// yet it is created with zero entitlement so the figure is still recorded.
// Returns the refreshed balance.
func RecomputeUsedDays(ctx context.Context, tx pgx.Tx, orgID, userID, leaveTypeID string, year int) (*LeaveBalance, error) {
	used, err := countApprovedLeaveDays(ctx, tx, orgID, userID, leaveTypeID, year)
	if err != nil {
		return nil, err
	}
	return scanLeaveBalance(tx.QueryRow(ctx, `
		INSERT INTO leave_balances
		    (org_id, user_id, leave_type_id, year, used_days)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (org_id, user_id, leave_type_id, year) DO UPDATE SET
		    used_days  = EXCLUDED.used_days,
		    updated_at = now()
		RETURNING `+leaveBalanceCols,
		orgID, userID, leaveTypeID, year, used))
}

// RecomputeUserBalances refreshes used_days for every leave type the user has
// for the given year (driven by the org's leave types). Useful after a bulk
// status change. Best-effort over each type.
func RecomputeUserBalances(ctx context.Context, tx pgx.Tx, orgID, userID string, year int) error {
	types, err := ListLeaveTypes(ctx, tx, orgID, true)
	if err != nil {
		return err
	}
	for _, t := range types {
		if _, err := RecomputeUsedDays(ctx, tx, orgID, userID, t.ID, year); err != nil {
			return err
		}
	}
	return nil
}
