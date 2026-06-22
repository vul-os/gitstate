// Package store — accounting_connections (Xero / QuickBooks) connection CRUD +
// invoice external-ref linkage (migration 024).
//
// Mirrors calendar.go: access + refresh tokens are stored AES-256-GCM encrypted
// (callers encrypt via internal/crypto; the store deals only in ciphertext bytes,
// never plaintext, never logged). Every function runs inside an org-scoped
// transaction (pgx.Tx) so RLS enforces the org boundary — callers MUST use
// db.WithOrg.
//
// Unlike calendar_connections (per user, per provider), accounting_connections is
// UNIQUE(org_id, provider): one accounting connection per provider per org (the
// org owner connects the company's books once for everyone).
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// AccountingConnection mirrors an accounting_connections row. TokenEncrypted and
// RefreshEncrypted hold AES-256-GCM ciphertext (never plaintext, never logged).
type AccountingConnection struct {
	ID               string
	OrgID            string
	UserID           string
	Provider         string // xero | quickbooks
	ExternalOrgID    string // Xero tenantId / QuickBooks realmId
	ExternalName     string // connected company/org name
	TokenEncrypted   []byte
	RefreshEncrypted []byte
	Scopes           string
	ExpiresAt        *time.Time
	LastSyncedAt     *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// UpsertAccountingConnectionInput is the payload for UpsertAccountingConnection.
type UpsertAccountingConnectionInput struct {
	OrgID            string
	UserID           string
	Provider         string
	ExternalOrgID    string
	ExternalName     string
	TokenEncrypted   []byte // required, AES-GCM ciphertext
	RefreshEncrypted []byte // optional
	Scopes           string
	ExpiresAt        *time.Time
}

const accountingConnectionCols = `
	id, org_id, user_id, provider, COALESCE(external_org_id,''), COALESCE(external_name,''),
	token_encrypted, refresh_encrypted, COALESCE(scopes,''), expires_at,
	last_synced_at, created_at, updated_at`

func scanAccountingConnection(row pgx.Row) (*AccountingConnection, error) {
	var c AccountingConnection
	err := row.Scan(
		&c.ID, &c.OrgID, &c.UserID, &c.Provider, &c.ExternalOrgID, &c.ExternalName,
		&c.TokenEncrypted, &c.RefreshEncrypted, &c.Scopes, &c.ExpiresAt,
		&c.LastSyncedAt, &c.CreatedAt, &c.UpdatedAt,
	)
	return &c, err
}

// GetAccountingConnection returns the org's connection for a provider, or
// ErrNotFound.
func GetAccountingConnection(ctx context.Context, tx pgx.Tx, orgID, provider string) (*AccountingConnection, error) {
	const q = `SELECT ` + accountingConnectionCols + `
		FROM accounting_connections
		WHERE org_id = $1 AND provider = $2`
	c, err := scanAccountingConnection(tx.QueryRow(ctx, q, orgID, provider))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get accounting connection: %w", err)
	}
	return c, nil
}

// ListAccountingConnections returns all accounting connections for the org.
// Callers must not leak TokenEncrypted/RefreshEncrypted into responses.
func ListAccountingConnections(ctx context.Context, tx pgx.Tx, orgID string) ([]*AccountingConnection, error) {
	const q = `SELECT ` + accountingConnectionCols + `
		FROM accounting_connections
		WHERE org_id = $1
		ORDER BY provider`
	rows, err := tx.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("store: list accounting connections: %w", err)
	}
	defer rows.Close()

	var out []*AccountingConnection
	for rows.Next() {
		c, err := scanAccountingConnection(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan accounting connection: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpsertAccountingConnection inserts or updates the org's connection for a
// provider. On conflict (org_id, provider) it refreshes the tokens + metadata.
func UpsertAccountingConnection(ctx context.Context, tx pgx.Tx, in UpsertAccountingConnectionInput) (*AccountingConnection, error) {
	if len(in.TokenEncrypted) == 0 {
		return nil, fmt.Errorf("store: upsert accounting connection: token required")
	}
	const q = `
		INSERT INTO accounting_connections
			(org_id, user_id, provider, external_org_id, external_name, token_encrypted,
			 refresh_encrypted, scopes, expires_at, updated_at)
		VALUES ($1, $2, $3, NULLIF($4,''), NULLIF($5,''), $6, $7, $8, $9, now())
		ON CONFLICT (org_id, provider) DO UPDATE SET
			user_id           = EXCLUDED.user_id,
			external_org_id   = EXCLUDED.external_org_id,
			external_name     = EXCLUDED.external_name,
			token_encrypted   = EXCLUDED.token_encrypted,
			refresh_encrypted = COALESCE(EXCLUDED.refresh_encrypted, accounting_connections.refresh_encrypted),
			scopes            = EXCLUDED.scopes,
			expires_at        = EXCLUDED.expires_at,
			updated_at        = now()
		RETURNING ` + accountingConnectionCols

	c, err := scanAccountingConnection(tx.QueryRow(ctx, q,
		in.OrgID, in.UserID, in.Provider, in.ExternalOrgID, in.ExternalName,
		in.TokenEncrypted, in.RefreshEncrypted, in.Scopes, in.ExpiresAt,
	))
	if err != nil {
		return nil, fmt.Errorf("store: upsert accounting connection: %w", err)
	}
	return c, nil
}

// UpdateAccountingTokens persists a refreshed access token (+ optional refresh
// token + new expiry) for the org's connection. Used after a silent refresh.
func UpdateAccountingTokens(ctx context.Context, tx pgx.Tx, orgID, provider string, tokenEnc, refreshEnc []byte, expiresAt *time.Time) error {
	const q = `
		UPDATE accounting_connections
		SET token_encrypted   = $3,
		    refresh_encrypted = COALESCE($4, refresh_encrypted),
		    expires_at        = $5,
		    updated_at        = now()
		WHERE org_id = $1 AND provider = $2`
	if _, err := tx.Exec(ctx, q, orgID, provider, tokenEnc, refreshEnc, expiresAt); err != nil {
		return fmt.Errorf("store: update accounting tokens: %w", err)
	}
	return nil
}

// MarkAccountingSynced stamps last_synced_at = now() for the org's connection.
func MarkAccountingSynced(ctx context.Context, tx pgx.Tx, orgID, provider string) error {
	const q = `
		UPDATE accounting_connections SET last_synced_at = now(), updated_at = now()
		WHERE org_id = $1 AND provider = $2`
	if _, err := tx.Exec(ctx, q, orgID, provider); err != nil {
		return fmt.Errorf("store: mark accounting synced: %w", err)
	}
	return nil
}

// DeleteAccountingConnection removes the org's connection for a provider. No
// error if it does not exist (idempotent disconnect).
func DeleteAccountingConnection(ctx context.Context, tx pgx.Tx, orgID, provider string) error {
	const q = `DELETE FROM accounting_connections WHERE org_id = $1 AND provider = $2`
	if _, err := tx.Exec(ctx, q, orgID, provider); err != nil {
		return fmt.Errorf("store: delete accounting connection: %w", err)
	}
	return nil
}

// SetInvoiceExternalRef records the external accounting system reference on an
// invoice after a successful push (provider, external id, deep-link url).
func SetInvoiceExternalRef(ctx context.Context, tx pgx.Tx, orgID, invoiceID, provider, externalID, url string) error {
	const q = `
		UPDATE client_invoices
		SET external_provider = NULLIF($3,''),
		    external_id       = NULLIF($4,''),
		    external_url      = NULLIF($5,'')
		WHERE org_id = $1 AND id = $2`
	ct, err := tx.Exec(ctx, q, orgID, invoiceID, provider, externalID, url)
	if err != nil {
		return fmt.Errorf("store: set invoice external ref: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
