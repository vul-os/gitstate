// Package store — tokens.go
// Org-scoped queries for the api_tokens table (Wave 2 of the AI/agent flywheel).
//
// API tokens authenticate a machine/agent against ONE org with least-privilege
// scopes, separate from human JWT sessions. The raw token ("gsk_<random>") is
// shown ONCE at creation; only its sha256 hex hash is stored. Pre-auth resolution
// (no org context yet) goes through the SECURITY DEFINER api_token_by_hash
// function, mirroring the refresh-token / webhook-secret / invoice-token pattern.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// tokenPrefix is the human-recognizable prefix on every raw API token.
const tokenPrefix = "gsk_"

// base62 alphabet for the random body of a token. Avoids URL-unsafe chars so the
// token can be passed as a bare Bearer credential or CLI arg without escaping.
const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// APIToken mirrors a row from api_tokens MINUS the secret material. The raw token
// and token_hash are NEVER carried on this struct (the raw is returned once, out
// of band, from CreateAPIToken).
type APIToken struct {
	ID         string
	OrgID      string
	UserID     string // creator (attribution); may be empty if creator was removed
	Name       string
	Prefix     string // display prefix, e.g. "gsk_AbC1…"
	Scopes     []string
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
}

// TokenPrincipal is the resolved identity of an API token, returned by TokenByHash
// (via the SECURITY DEFINER api_token_by_hash function). Only live tokens resolve.
type TokenPrincipal struct {
	OrgID   string
	UserID  string
	TokenID string
	Scopes  []string
}

// HashAPIToken returns the sha256 hex digest of a raw token string. This is the
// only representation of the token that is ever persisted or logged.
func HashAPIToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// generateRawToken returns a fresh "gsk_<base62>" token. The body has ~190 bits
// of entropy (32 base62 chars).
func generateRawToken() (string, error) {
	const bodyLen = 32
	var sb strings.Builder
	sb.WriteString(tokenPrefix)
	max := big.NewInt(int64(len(base62)))
	for i := 0; i < bodyLen; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("store: generate api token: %w", err)
		}
		sb.WriteByte(base62[n.Int64()])
	}
	return sb.String(), nil
}

// displayPrefix returns the first chars of a raw token for non-secret display,
// e.g. "gsk_AbC1" (prefix + first 4 body chars).
func displayPrefix(raw string) string {
	if len(raw) <= len(tokenPrefix)+4 {
		return raw
	}
	return raw[:len(tokenPrefix)+4]
}

// CreateAPIToken generates a new raw token, stores ONLY its sha256 hash + display
// prefix, and returns the raw token ONCE alongside the persisted row. The caller
// must surface the raw value to the user immediately; it cannot be recovered.
//
// Runs inside an org-scoped tx (tx from db.WithOrg) so RLS sets org_id correctly.
// expiresAt is nil for a non-expiring token.
func CreateAPIToken(ctx context.Context, tx pgx.Tx, orgID, userID, name string, scopes []string, expiresAt *time.Time) (raw string, tok *APIToken, err error) {
	raw, err = generateRawToken()
	if err != nil {
		return "", nil, err
	}
	hash := HashAPIToken(raw)
	prefix := displayPrefix(raw)

	if scopes == nil {
		scopes = []string{}
	}

	var creator *string
	if userID != "" {
		creator = &userID
	}

	const q = `
		INSERT INTO api_tokens (org_id, user_id, name, token_hash, prefix, scopes, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, org_id, COALESCE(user_id::text,''), name, prefix, scopes,
		          last_used_at, expires_at, revoked_at, created_at`

	row := tx.QueryRow(ctx, q, orgID, creator, name, hash, prefix, scopes, expiresAt)
	tok, err = scanAPIToken(row)
	if err != nil {
		return "", nil, fmt.Errorf("store: create api token: %w", err)
	}
	return raw, tok, nil
}

// ListAPITokens returns every token for the org (live + revoked + expired) MINUS
// any secret material. Ordered newest-first. Runs inside an org-scoped tx.
func ListAPITokens(ctx context.Context, tx pgx.Tx, orgID string) ([]*APIToken, error) {
	const q = `
		SELECT id, org_id, COALESCE(user_id::text,''), name, prefix, scopes,
		       last_used_at, expires_at, revoked_at, created_at
		FROM api_tokens
		WHERE org_id = $1
		ORDER BY created_at DESC`

	rows, err := tx.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("store: list api tokens: %w", err)
	}
	defer rows.Close()

	var out []*APIToken
	for rows.Next() {
		tok, err := scanAPIToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, tok)
	}
	return out, rows.Err()
}

// RevokeAPIToken sets revoked_at = now() on a token. Idempotent: a second revoke
// is a no-op. Returns ErrNotFound if the id does not belong to the org (RLS) or
// does not exist. Runs inside an org-scoped tx.
func RevokeAPIToken(ctx context.Context, tx pgx.Tx, orgID, tokenID string) error {
	const q = `
		UPDATE api_tokens SET revoked_at = now()
		WHERE id = $1 AND org_id = $2 AND revoked_at IS NULL`
	tag, err := tx.Exec(ctx, q, tokenID, orgID)
	if err != nil {
		return fmt.Errorf("store: revoke api token %s: %w", tokenID, err)
	}
	if tag.RowsAffected() == 0 {
		// Either it doesn't exist / wrong org, or it was already revoked. Probe to
		// distinguish a genuine not-found (so the API can 404) from an idempotent
		// re-revoke (which should succeed).
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM api_tokens WHERE id = $1 AND org_id = $2)`,
			tokenID, orgID).Scan(&exists); err != nil {
			return fmt.Errorf("store: revoke api token probe %s: %w", tokenID, err)
		}
		if !exists {
			return ErrNotFound
		}
	}
	return nil
}

// TokenByHash resolves a token hash to its org/user/scopes via the SECURITY
// DEFINER api_token_by_hash function. It runs on the BARE pool, pre-auth (no org
// context yet) — the function bypasses RLS and only returns a row for a LIVE
// (non-revoked, non-expired) token. Returns ErrNotFound on miss.
func TokenByHash(ctx context.Context, pool *pgxpool.Pool, hash string) (*TokenPrincipal, error) {
	const q = `SELECT org_id::text, COALESCE(user_id::text,''), token_id::text, scopes
	           FROM api_token_by_hash($1)`
	var p TokenPrincipal
	var scopes []string
	err := pool.QueryRow(ctx, q, hash).Scan(&p.OrgID, &p.UserID, &p.TokenID, &scopes)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: token by hash: %w", err)
	}
	if scopes == nil {
		scopes = []string{}
	}
	p.Scopes = scopes
	return &p, nil
}

// TouchTokenUsed stamps last_used_at = now() on a token. Best-effort: called
// async on the hot auth path. The orgID comes from TokenByHash (which resolved it
// via the SECURITY DEFINER function), so we can set the RLS parameter and update
// under FORCE-RLS on the bare pool without any request org context.
func TouchTokenUsed(ctx context.Context, pool *pgxpool.Pool, orgID, tokenID string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: touch token: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Transaction-local RLS parameter: auto-cleared on commit/rollback, so it can
	// never leak the org scope onto the next request reusing this pooled conn.
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org', $1, true)", orgID); err != nil {
		return fmt.Errorf("store: touch token: set org: %w", err)
	}
	const q = `UPDATE api_tokens SET last_used_at = now() WHERE id = $1 AND org_id = $2`
	if _, err := tx.Exec(ctx, q, tokenID, orgID); err != nil {
		return fmt.Errorf("store: touch token %s: %w", tokenID, err)
	}
	return tx.Commit(ctx)
}

// scanAPIToken reads one api_tokens row (without secret material) from any source.
func scanAPIToken(row pgx.Row) (*APIToken, error) {
	var t APIToken
	var scopes []string
	if err := row.Scan(
		&t.ID, &t.OrgID, &t.UserID, &t.Name, &t.Prefix, &scopes,
		&t.LastUsedAt, &t.ExpiresAt, &t.RevokedAt, &t.CreatedAt,
	); err != nil {
		return nil, fmt.Errorf("store: scan api token: %w", err)
	}
	if scopes == nil {
		scopes = []string{}
	}
	t.Scopes = scopes
	return &t, nil
}
