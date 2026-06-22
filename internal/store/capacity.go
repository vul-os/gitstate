// Package store — capacity CRUD.
// All functions run inside a db.WithOrg transaction (RLS boundary set).
package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// ── Leave entries ─────────────────────────────────────────────────────────

// LeaveEntry mirrors a row from leave_entries.
type LeaveEntry struct {
	ID          string
	OrgID       string
	UserID      string
	Kind        string    // pto | sick | holiday (legacy classifier; kept for back-compat)
	LeaveTypeID string    // configurable leave type (may be empty for legacy rows)
	StartDate   time.Time // date stored as timestamptz midnight UTC
	EndDate     time.Time
	HalfDay     bool   // a single half-day off
	Portion     string // full | am | pm
	Status      string // pending | approved | rejected
	Note        string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// leaveEntryCols is the canonical column projection for a LeaveEntry, matching
// the scan order in scanLeaveEntry.
const leaveEntryCols = `id, org_id, user_id, kind, COALESCE(leave_type_id::text,''),
	start_date, end_date, half_day, portion, status, COALESCE(note,''), created_at, updated_at`

func scanLeaveEntry(row pgx.Row) (*LeaveEntry, error) {
	var e LeaveEntry
	err := row.Scan(
		&e.ID, &e.OrgID, &e.UserID, &e.Kind, &e.LeaveTypeID,
		&e.StartDate, &e.EndDate, &e.HalfDay, &e.Portion,
		&e.Status, &e.Note, &e.CreatedAt, &e.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// ListLeaveEntries returns all leave entries for the org, optionally filtered
// to a single user when userID is non-empty.
func ListLeaveEntries(ctx context.Context, tx pgx.Tx, orgID, userID string) ([]*LeaveEntry, error) {
	query := `SELECT ` + leaveEntryCols + ` FROM leave_entries WHERE org_id = $1`
	args := []any{orgID}
	if userID != "" {
		query += ` AND user_id = $2`
		args = append(args, userID)
	}
	query += ` ORDER BY start_date DESC`

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*LeaveEntry
	for rows.Next() {
		e, err := scanLeaveEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CreateLeaveEntry inserts a new leave entry. leaveTypeID may be empty (legacy
// rows keyed only by kind); portion defaults to "full". When halfDay is set the
// entry represents a single half-day off.
func CreateLeaveEntry(ctx context.Context, tx pgx.Tx, orgID, userID, kind, leaveTypeID, note string, start, end time.Time, halfDay bool, portion string) (*LeaveEntry, error) {
	if portion == "" {
		portion = "full"
	}
	return scanLeaveEntry(tx.QueryRow(ctx, `
		INSERT INTO leave_entries
		    (org_id, user_id, kind, leave_type_id, start_date, end_date, half_day, portion, note)
		VALUES ($1, $2, $3, NULLIF($4,'')::uuid, $5, $6, $7, $8, NULLIF($9,''))
		RETURNING `+leaveEntryCols,
		orgID, userID, kind, leaveTypeID, start, end, halfDay, portion, note))
}

// ApproveLeaveEntry sets the status of a leave entry (approved | rejected).
func ApproveLeaveEntry(ctx context.Context, tx pgx.Tx, orgID, id, status string) (*LeaveEntry, error) {
	e, err := scanLeaveEntry(tx.QueryRow(ctx, `
		UPDATE leave_entries
		SET status = $1, updated_at = now()
		WHERE id = $2 AND org_id = $3
		RETURNING `+leaveEntryCols,
		status, id, orgID))
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	return e, err
}

// ApprovedLeaveInPeriod returns approved leave entries for a user that overlap
// the half-open period [from, to).
func ApprovedLeaveInPeriod(ctx context.Context, tx pgx.Tx, orgID, userID string, from, to time.Time) ([]*LeaveEntry, error) {
	rows, err := tx.Query(ctx, `SELECT `+leaveEntryCols+`
		FROM leave_entries
		WHERE org_id = $1
		  AND user_id = $2
		  AND status = 'approved'
		  AND start_date < $4
		  AND end_date   >= $3
		ORDER BY start_date`,
		orgID, userID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*LeaveEntry
	for rows.Next() {
		e, err := scanLeaveEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ── Availability ──────────────────────────────────────────────────────────

// Availability mirrors a row from the availability table.
type Availability struct {
	ID            string
	OrgID         string
	UserID        string
	WeeklyHours   float64
	WorkingDays   []int32 // ISO weekday numbers: 1=Mon…7=Sun
	EffectiveFrom time.Time
	CreatedAt     time.Time
}

// GetAvailability returns the most-recent availability row effective on or before
// asOf for a given member. Returns ErrNotFound if no row exists.
func GetAvailability(ctx context.Context, tx pgx.Tx, orgID, userID string, asOf time.Time) (*Availability, error) {
	var a Availability
	err := tx.QueryRow(ctx, `
		SELECT id, org_id, user_id, weekly_hours, working_days, effective_from, created_at
		FROM availability
		WHERE org_id = $1 AND user_id = $2 AND effective_from <= $3
		ORDER BY effective_from DESC
		LIMIT 1`,
		orgID, userID, asOf).
		Scan(&a.ID, &a.OrgID, &a.UserID, &a.WeeklyHours, &a.WorkingDays, &a.EffectiveFrom, &a.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	return &a, err
}

// ListAvailability returns all availability rows for a user, newest first.
func ListAvailability(ctx context.Context, tx pgx.Tx, orgID, userID string) ([]*Availability, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, org_id, user_id, weekly_hours, working_days, effective_from, created_at
		FROM availability
		WHERE org_id = $1 AND user_id = $2
		ORDER BY effective_from DESC`,
		orgID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Availability
	for rows.Next() {
		var a Availability
		if err := rows.Scan(&a.ID, &a.OrgID, &a.UserID, &a.WeeklyHours, &a.WorkingDays, &a.EffectiveFrom, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

// UpsertAvailability records a point-in-time availability snapshot for a member.
// Each (org, user, effective_from) is a single snapshot: re-PUTing on the same
// effective date overwrites that day's row in place rather than appending a
// duplicate. (The availability table has no unique constraint on
// (org_id, user_id, effective_from) — only a non-unique index — so a plain
// ON CONFLICT clause has no arbiter and would never fire, silently stacking
// duplicate same-date rows that GetAvailability then resolves nondeterministically
// via ORDER BY effective_from DESC LIMIT 1. We therefore upsert explicitly:
// UPDATE the existing same-date row if present, else INSERT.)
func UpsertAvailability(ctx context.Context, tx pgx.Tx, orgID, userID string, weeklyHours float64, workingDays []int32, effectiveFrom time.Time) (*Availability, error) {
	var a Availability
	// effective_from is a DATE column; normalize to midnight so the equality
	// match below is stable regardless of any time component on the input.
	day := effectiveFrom.UTC().Truncate(24 * time.Hour)
	err := tx.QueryRow(ctx, `
		UPDATE availability
		SET weekly_hours = $3, working_days = $4
		WHERE org_id = $1 AND user_id = $2 AND effective_from = $5
		RETURNING id, org_id, user_id, weekly_hours, working_days, effective_from, created_at`,
		orgID, userID, weeklyHours, workingDays, day).
		Scan(&a.ID, &a.OrgID, &a.UserID, &a.WeeklyHours, &a.WorkingDays, &a.EffectiveFrom, &a.CreatedAt)
	if err == nil {
		return &a, nil
	}
	if err != pgx.ErrNoRows {
		return nil, err
	}
	// No row for this effective date yet — insert a fresh snapshot.
	err = tx.QueryRow(ctx, `
		INSERT INTO availability (org_id, user_id, weekly_hours, working_days, effective_from)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, org_id, user_id, weekly_hours, working_days, effective_from, created_at`,
		orgID, userID, weeklyHours, workingDays, day).
		Scan(&a.ID, &a.OrgID, &a.UserID, &a.WeeklyHours, &a.WorkingDays, &a.EffectiveFrom, &a.CreatedAt)
	return &a, err
}

// ── Time entries ──────────────────────────────────────────────────────────

// TimeEntry mirrors a row from time_entries.
type TimeEntry struct {
	ID         string
	OrgID      string
	UserID     string
	IssueID    string // may be empty
	Source     string // git | manual
	Minutes    int
	OccurredOn time.Time
	Note       string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ListTimeEntries returns time entries for the org, optionally scoped to a user.
func ListTimeEntries(ctx context.Context, tx pgx.Tx, orgID, userID string) ([]*TimeEntry, error) {
	query := `
		SELECT id, org_id, user_id, COALESCE(issue_id::text,''), source, minutes,
		       occurred_on, COALESCE(note,''), created_at, updated_at
		FROM time_entries
		WHERE org_id = $1`
	args := []any{orgID}
	if userID != "" {
		query += ` AND user_id = $2`
		args = append(args, userID)
	}
	query += ` ORDER BY occurred_on DESC`

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*TimeEntry
	for rows.Next() {
		var e TimeEntry
		if err := rows.Scan(
			&e.ID, &e.OrgID, &e.UserID, &e.IssueID, &e.Source,
			&e.Minutes, &e.OccurredOn, &e.Note, &e.CreatedAt, &e.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// CreateTimeEntry inserts a manual (or git-derived) time entry.
func CreateTimeEntry(ctx context.Context, tx pgx.Tx, orgID, userID, issueID, source, note string, minutes int, occurredOn time.Time) (*TimeEntry, error) {
	var e TimeEntry
	var issueIDArg any
	if issueID != "" {
		issueIDArg = issueID
	}
	err := tx.QueryRow(ctx, `
		INSERT INTO time_entries (org_id, user_id, issue_id, source, minutes, occurred_on, note)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7,''))
		RETURNING id, org_id, user_id, COALESCE(issue_id::text,''), source, minutes,
		          occurred_on, COALESCE(note,''), created_at, updated_at`,
		orgID, userID, issueIDArg, source, minutes, occurredOn, note).
		Scan(
			&e.ID, &e.OrgID, &e.UserID, &e.IssueID, &e.Source,
			&e.Minutes, &e.OccurredOn, &e.Note, &e.CreatedAt, &e.UpdatedAt,
		)
	return &e, err
}

// SumTimeMinutesInPeriod returns the total minutes logged by a user in [from, to).
func SumTimeMinutesInPeriod(ctx context.Context, tx pgx.Tx, orgID, userID string, from, to time.Time) (int, error) {
	var total int
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(minutes), 0)
		FROM time_entries
		WHERE org_id = $1
		  AND user_id = $2
		  AND occurred_on >= $3
		  AND occurred_on <  $4`,
		orgID, userID, from, to).Scan(&total)
	return total, err
}
