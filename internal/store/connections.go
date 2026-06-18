// Package store — per-org platform OAuth-app connections.
//
// GetConnection / UpsertConnection / DeleteConnection persist the org's
// GitHub/GitLab OAuth connection in the platform_connections table (migration
// 005). The access token is stored AES-256-GCM encrypted; the store layer deals
// only in the raw encrypted bytes (callers encrypt via internal/crypto), so key
// management stays out of the store.
//
// All three run inside an org-scoped transaction (pgx.Tx) — callers MUST use
// db.WithOrg so the RLS policy (org_id = current_org()) is enforced, preventing
// cross-org access to connection tokens (decisions S1/S3).
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PlatformConnection mirrors the platform_connections table. TokenEncrypted and
// RefreshEncrypted hold AES-256-GCM ciphertext (never plaintext, never logged).
type PlatformConnection struct {
	ID               string
	OrgID            string
	Platform         string // github | gitlab
	ConnectedBy      string
	ExternalLogin    string
	TokenEncrypted   []byte
	RefreshEncrypted []byte
	Scopes           string
	ExpiresAt        *time.Time
	BaseURL          string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// UpsertConnectionInput is the payload for UpsertConnection.
type UpsertConnectionInput struct {
	OrgID            string
	Platform         string
	ConnectedBy      string // user id; may be "" → stored NULL
	ExternalLogin    string
	TokenEncrypted   []byte // required, AES-GCM ciphertext
	RefreshEncrypted []byte // optional
	Scopes           string
	ExpiresAt        *time.Time
	BaseURL          string
}

// GetConnection returns the org's connection for a platform, or ErrNotFound.
func GetConnection(ctx context.Context, tx pgx.Tx, orgID, platform string) (*PlatformConnection, error) {
	const q = `
		SELECT id, org_id, platform, COALESCE(connected_by::text,''), COALESCE(external_login,''),
		       token_encrypted, refresh_encrypted, COALESCE(scopes,''), expires_at,
		       COALESCE(base_url,''), created_at, updated_at
		FROM platform_connections
		WHERE org_id = $1 AND platform = $2`

	var c PlatformConnection
	err := tx.QueryRow(ctx, q, orgID, platform).Scan(
		&c.ID, &c.OrgID, &c.Platform, &c.ConnectedBy, &c.ExternalLogin,
		&c.TokenEncrypted, &c.RefreshEncrypted, &c.Scopes, &c.ExpiresAt,
		&c.BaseURL, &c.CreatedAt, &c.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get connection: %w", err)
	}
	return &c, nil
}

// ListConnections returns all platform connections for the org (no token bytes
// in the typical caller's response — callers must not leak TokenEncrypted).
func ListConnections(ctx context.Context, tx pgx.Tx, orgID string) ([]PlatformConnection, error) {
	const q = `
		SELECT id, org_id, platform, COALESCE(connected_by::text,''), COALESCE(external_login,''),
		       token_encrypted, refresh_encrypted, COALESCE(scopes,''), expires_at,
		       COALESCE(base_url,''), created_at, updated_at
		FROM platform_connections
		WHERE org_id = $1
		ORDER BY platform`

	rows, err := tx.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("store: list connections: %w", err)
	}
	defer rows.Close()

	var out []PlatformConnection
	for rows.Next() {
		var c PlatformConnection
		if err := rows.Scan(
			&c.ID, &c.OrgID, &c.Platform, &c.ConnectedBy, &c.ExternalLogin,
			&c.TokenEncrypted, &c.RefreshEncrypted, &c.Scopes, &c.ExpiresAt,
			&c.BaseURL, &c.CreatedAt, &c.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan connection: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpsertConnection inserts or updates the org's connection for a platform.
// On conflict (org_id, platform) it refreshes the token + account metadata.
func UpsertConnection(ctx context.Context, tx pgx.Tx, in UpsertConnectionInput) (*PlatformConnection, error) {
	if len(in.TokenEncrypted) == 0 {
		return nil, fmt.Errorf("store: upsert connection: token required")
	}

	var connectedBy any
	if in.ConnectedBy != "" {
		connectedBy = in.ConnectedBy
	}

	const q = `
		INSERT INTO platform_connections
			(org_id, platform, connected_by, external_login, token_encrypted,
			 refresh_encrypted, scopes, expires_at, base_url, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
		ON CONFLICT (org_id, platform) DO UPDATE SET
			connected_by      = EXCLUDED.connected_by,
			external_login    = EXCLUDED.external_login,
			token_encrypted   = EXCLUDED.token_encrypted,
			refresh_encrypted = EXCLUDED.refresh_encrypted,
			scopes            = EXCLUDED.scopes,
			expires_at        = EXCLUDED.expires_at,
			base_url          = EXCLUDED.base_url,
			updated_at        = now()
		RETURNING id, org_id, platform, COALESCE(connected_by::text,''), COALESCE(external_login,''),
		          token_encrypted, refresh_encrypted, COALESCE(scopes,''), expires_at,
		          COALESCE(base_url,''), created_at, updated_at`

	var c PlatformConnection
	err := tx.QueryRow(ctx, q,
		in.OrgID, in.Platform, connectedBy, in.ExternalLogin, in.TokenEncrypted,
		in.RefreshEncrypted, in.Scopes, in.ExpiresAt, in.BaseURL,
	).Scan(
		&c.ID, &c.OrgID, &c.Platform, &c.ConnectedBy, &c.ExternalLogin,
		&c.TokenEncrypted, &c.RefreshEncrypted, &c.Scopes, &c.ExpiresAt,
		&c.BaseURL, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("store: upsert connection: %w", err)
	}
	return &c, nil
}

// DeleteConnection removes the org's connection for a platform. No error if it
// does not exist (idempotent disconnect).
func DeleteConnection(ctx context.Context, tx pgx.Tx, orgID, platform string) error {
	const q = `DELETE FROM platform_connections WHERE org_id = $1 AND platform = $2`
	if _, err := tx.Exec(ctx, q, orgID, platform); err != nil {
		return fmt.Errorf("store: delete connection: %w", err)
	}
	return nil
}
