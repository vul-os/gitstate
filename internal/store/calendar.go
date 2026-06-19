// Package store — per-user Google/Microsoft calendar connections.
//
// GetCalendarConnection / ListCalendarConnections / UpsertCalendarConnection /
// DeleteCalendarConnection persist a member's calendar OAuth connection in the
// calendar_connections table (migration 007). Access + refresh tokens are stored
// AES-256-GCM encrypted; the store layer deals only in the raw encrypted bytes
// (callers encrypt via internal/crypto), so key management stays out of the store.
//
// All functions run inside an org-scoped transaction (pgx.Tx) — callers MUST use
// db.WithOrg so the RLS policy (org_id = current_org()) is enforced, preventing
// cross-org access to calendar tokens (decisions S1/S3).
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// CalendarConnection mirrors the calendar_connections table. TokenEncrypted and
// RefreshEncrypted hold AES-256-GCM ciphertext (never plaintext, never logged).
type CalendarConnection struct {
	ID               string
	OrgID            string
	UserID           string
	Provider         string // google | microsoft
	ExternalEmail    string
	CalendarID       string // target calendar; "" → provider default ("primary")
	TokenEncrypted   []byte
	RefreshEncrypted []byte
	Scopes           string
	ExpiresAt        *time.Time
	PushLeave        bool
	PullBusy         bool
	LastSyncedAt     *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// UpsertCalendarConnectionInput is the payload for UpsertCalendarConnection.
type UpsertCalendarConnectionInput struct {
	OrgID            string
	UserID           string
	Provider         string
	ExternalEmail    string
	CalendarID       string
	TokenEncrypted   []byte // required, AES-GCM ciphertext
	RefreshEncrypted []byte // optional
	Scopes           string
	ExpiresAt        *time.Time
}

const calendarConnectionCols = `
	id, org_id, user_id, provider, COALESCE(external_email,''), COALESCE(calendar_id,''),
	token_encrypted, refresh_encrypted, COALESCE(scopes,''), expires_at,
	push_leave, pull_busy, last_synced_at, created_at, updated_at`

func scanCalendarConnection(row pgx.Row) (*CalendarConnection, error) {
	var c CalendarConnection
	err := row.Scan(
		&c.ID, &c.OrgID, &c.UserID, &c.Provider, &c.ExternalEmail, &c.CalendarID,
		&c.TokenEncrypted, &c.RefreshEncrypted, &c.Scopes, &c.ExpiresAt,
		&c.PushLeave, &c.PullBusy, &c.LastSyncedAt, &c.CreatedAt, &c.UpdatedAt,
	)
	return &c, err
}

// GetCalendarConnection returns a member's connection for a provider, or
// ErrNotFound.
func GetCalendarConnection(ctx context.Context, tx pgx.Tx, orgID, userID, provider string) (*CalendarConnection, error) {
	q := `SELECT ` + calendarConnectionCols + `
		FROM calendar_connections
		WHERE org_id = $1 AND user_id = $2 AND provider = $3`

	c, err := scanCalendarConnection(tx.QueryRow(ctx, q, orgID, userID, provider))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get calendar connection: %w", err)
	}
	return c, nil
}

// ListCalendarConnections returns all calendar connections for the org (across
// members). Callers must not leak TokenEncrypted/RefreshEncrypted into responses.
func ListCalendarConnections(ctx context.Context, tx pgx.Tx, orgID string) ([]*CalendarConnection, error) {
	q := `SELECT ` + calendarConnectionCols + `
		FROM calendar_connections
		WHERE org_id = $1
		ORDER BY user_id, provider`

	rows, err := tx.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("store: list calendar connections: %w", err)
	}
	defer rows.Close()

	var out []*CalendarConnection
	for rows.Next() {
		c, err := scanCalendarConnection(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan calendar connection: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListCalendarConnectionsForUser returns a single member's calendar connections.
func ListCalendarConnectionsForUser(ctx context.Context, tx pgx.Tx, orgID, userID string) ([]*CalendarConnection, error) {
	q := `SELECT ` + calendarConnectionCols + `
		FROM calendar_connections
		WHERE org_id = $1 AND user_id = $2
		ORDER BY provider`

	rows, err := tx.Query(ctx, q, orgID, userID)
	if err != nil {
		return nil, fmt.Errorf("store: list calendar connections for user: %w", err)
	}
	defer rows.Close()

	var out []*CalendarConnection
	for rows.Next() {
		c, err := scanCalendarConnection(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan calendar connection: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpsertCalendarConnection inserts or updates a member's connection for a
// provider. On conflict (org_id, user_id, provider) it refreshes the tokens +
// account metadata, preserving the existing push/pull toggles.
func UpsertCalendarConnection(ctx context.Context, tx pgx.Tx, in UpsertCalendarConnectionInput) (*CalendarConnection, error) {
	if len(in.TokenEncrypted) == 0 {
		return nil, fmt.Errorf("store: upsert calendar connection: token required")
	}

	q := `
		INSERT INTO calendar_connections
			(org_id, user_id, provider, external_email, calendar_id, token_encrypted,
			 refresh_encrypted, scopes, expires_at, updated_at)
		VALUES ($1, $2, $3, $4, NULLIF($5,''), $6, $7, $8, $9, now())
		ON CONFLICT (org_id, user_id, provider) DO UPDATE SET
			external_email    = EXCLUDED.external_email,
			calendar_id       = COALESCE(EXCLUDED.calendar_id, calendar_connections.calendar_id),
			token_encrypted   = EXCLUDED.token_encrypted,
			refresh_encrypted = COALESCE(EXCLUDED.refresh_encrypted, calendar_connections.refresh_encrypted),
			scopes            = EXCLUDED.scopes,
			expires_at        = EXCLUDED.expires_at,
			updated_at        = now()
		RETURNING ` + calendarConnectionCols

	c, err := scanCalendarConnection(tx.QueryRow(ctx, q,
		in.OrgID, in.UserID, in.Provider, in.ExternalEmail, in.CalendarID,
		in.TokenEncrypted, in.RefreshEncrypted, in.Scopes, in.ExpiresAt,
	))
	if err != nil {
		return nil, fmt.Errorf("store: upsert calendar connection: %w", err)
	}
	return c, nil
}

// UpdateCalendarToggles sets the push_leave/pull_busy flags for a member's
// connection. Returns ErrNotFound if no connection exists.
func UpdateCalendarToggles(ctx context.Context, tx pgx.Tx, orgID, userID, provider string, pushLeave, pullBusy bool) (*CalendarConnection, error) {
	q := `
		UPDATE calendar_connections
		SET push_leave = $4, pull_busy = $5, updated_at = now()
		WHERE org_id = $1 AND user_id = $2 AND provider = $3
		RETURNING ` + calendarConnectionCols

	c, err := scanCalendarConnection(tx.QueryRow(ctx, q, orgID, userID, provider, pushLeave, pullBusy))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: update calendar toggles: %w", err)
	}
	return c, nil
}

// MarkCalendarSynced stamps last_synced_at = now() for a member's connection.
func MarkCalendarSynced(ctx context.Context, tx pgx.Tx, orgID, userID, provider string) error {
	const q = `
		UPDATE calendar_connections SET last_synced_at = now(), updated_at = now()
		WHERE org_id = $1 AND user_id = $2 AND provider = $3`
	if _, err := tx.Exec(ctx, q, orgID, userID, provider); err != nil {
		return fmt.Errorf("store: mark calendar synced: %w", err)
	}
	return nil
}

// UpdateCalendarTokens persists a refreshed access token (+ optional refresh
// token + new expiry) for a member's connection. Used after a silent token
// refresh so the new token is reused next time.
func UpdateCalendarTokens(ctx context.Context, tx pgx.Tx, orgID, userID, provider string, tokenEnc, refreshEnc []byte, expiresAt *time.Time) error {
	const q = `
		UPDATE calendar_connections
		SET token_encrypted   = $4,
		    refresh_encrypted = COALESCE($5, refresh_encrypted),
		    expires_at        = $6,
		    updated_at        = now()
		WHERE org_id = $1 AND user_id = $2 AND provider = $3`
	if _, err := tx.Exec(ctx, q, orgID, userID, provider, tokenEnc, refreshEnc, expiresAt); err != nil {
		return fmt.Errorf("store: update calendar tokens: %w", err)
	}
	return nil
}

// DeleteCalendarConnection removes a member's connection for a provider. No error
// if it does not exist (idempotent disconnect).
func DeleteCalendarConnection(ctx context.Context, tx pgx.Tx, orgID, userID, provider string) error {
	const q = `DELETE FROM calendar_connections WHERE org_id = $1 AND user_id = $2 AND provider = $3`
	if _, err := tx.Exec(ctx, q, orgID, userID, provider); err != nil {
		return fmt.Errorf("store: delete calendar connection: %w", err)
	}
	return nil
}

// ── leave_entries calendar-event linkage (migration 007 columns) ────────────

// GetLeaveEntry returns a single leave entry by id within the org, including its
// calendar linkage. Returns ErrNotFound if absent.
func GetLeaveEntry(ctx context.Context, tx pgx.Tx, orgID, id string) (*LeaveEntry, error) {
	var e LeaveEntry
	err := tx.QueryRow(ctx, `
		SELECT id, org_id, user_id, kind, start_date, end_date, status, COALESCE(note,''), created_at, updated_at
		FROM leave_entries
		WHERE id = $1 AND org_id = $2`,
		id, orgID).
		Scan(&e.ID, &e.OrgID, &e.UserID, &e.Kind, &e.StartDate, &e.EndDate, &e.Status, &e.Note, &e.CreatedAt, &e.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get leave entry: %w", err)
	}
	return &e, nil
}

// GetLeaveCalendarLink returns the (eventID, provider) previously stored on a
// leave entry, both possibly empty when nothing has been pushed yet.
func GetLeaveCalendarLink(ctx context.Context, tx pgx.Tx, orgID, leaveID string) (eventID, provider string, err error) {
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(calendar_event_id,''), COALESCE(calendar_provider,'')
		FROM leave_entries WHERE id = $1 AND org_id = $2`,
		leaveID, orgID).Scan(&eventID, &provider)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("store: get leave calendar link: %w", err)
	}
	return eventID, provider, nil
}

// SetLeaveCalendarEvent records the calendar event id + provider created for a
// leave entry (or clears them when eventID == ""), so updates/cancellations sync.
func SetLeaveCalendarEvent(ctx context.Context, tx pgx.Tx, orgID, leaveID, eventID, provider string) error {
	const q = `
		UPDATE leave_entries
		SET calendar_event_id = NULLIF($3,''), calendar_provider = NULLIF($4,''), updated_at = now()
		WHERE id = $1 AND org_id = $2`
	if _, err := tx.Exec(ctx, q, leaveID, orgID, eventID, provider); err != nil {
		return fmt.Errorf("store: set leave calendar event: %w", err)
	}
	return nil
}
