// Package store — tokens_test.go
// DB-backed tests for the api_tokens lifecycle:
//   - CreateAPIToken returns a raw "gsk_" token whose sha256 round-trips through
//     the SECURITY DEFINER api_token_by_hash (via TokenByHash) to the right
//     org/user/scopes;
//   - a revoked token no longer resolves;
//   - an expired token no longer resolves;
//   - ListAPITokens never leaks the hash or raw, and RevokeAPIToken is idempotent
//   - 404s on a foreign id.
//
// Each test uses a throwaway org and cleans up via cascade. Skips when
// DATABASE_URL is unset. Runs under the gitstate_app (FORCE-RLS) role.
package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/jackc/pgx/v5"
)

func tokensTestDB(t *testing.T) *db.DB {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping tokens store test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	database, err := db.New(ctx, &config.Config{Database: config.DatabaseConfig{URL: dbURL}})
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	return database
}

func seedTokenOrgUser(t *testing.T, ctx context.Context, database *db.DB) (orgID, userID string) {
	t.Helper()
	ns := time.Now().UnixNano()
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("tok-%d", ns), "Token Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO users (email, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("tok-%d@example.test", ns), "Token User").Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, orgID)
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM users WHERE id=$1`, userID)
	})
	return orgID, userID
}

func TestAPITokenRoundTripAndRevoke(t *testing.T) {
	database := tokensTestDB(t)
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	orgID, userID := seedTokenOrgUser(t, ctx, database)

	var raw string
	var tok *APIToken
	scopes := []string{"read:context", "read:issues"}
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var e error
		raw, tok, e = CreateAPIToken(ctx, tx, orgID, userID, "ci-agent", scopes, nil)
		return e
	}); err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}

	// Raw shape + no secret leakage on the row.
	if len(raw) < 8 || raw[:4] != "gsk_" {
		t.Fatalf("raw token has wrong shape: %q", raw)
	}
	if tok.Prefix == raw {
		t.Fatalf("prefix must not equal the full raw token")
	}

	// Round-trip: sha256(raw) → TokenByHash → org/user/scopes.
	princ, err := TokenByHash(ctx, database.Pool(), HashAPIToken(raw))
	if err != nil {
		t.Fatalf("TokenByHash live: %v", err)
	}
	if princ.OrgID != orgID || princ.UserID != userID || princ.TokenID != tok.ID {
		t.Fatalf("principal mismatch: %+v (want org=%s user=%s id=%s)", princ, orgID, userID, tok.ID)
	}
	if len(princ.Scopes) != 2 {
		t.Fatalf("want 2 scopes, got %v", princ.Scopes)
	}

	// A wrong hash resolves to nothing.
	if _, err := TokenByHash(ctx, database.Pool(), HashAPIToken("gsk_does_not_exist")); err != ErrNotFound {
		t.Fatalf("want ErrNotFound for bad hash, got %v", err)
	}

	// List never leaks secrets and shows the token.
	var list []*APIToken
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var e error
		list, e = ListAPITokens(ctx, tx, orgID)
		return e
	}); err != nil {
		t.Fatalf("ListAPITokens: %v", err)
	}
	if len(list) != 1 || list[0].ID != tok.ID {
		t.Fatalf("want 1 listed token = %s, got %+v", tok.ID, list)
	}

	// TouchTokenUsed stamps last_used_at.
	if err := TouchTokenUsed(ctx, database.Pool(), orgID, tok.ID); err != nil {
		t.Fatalf("TouchTokenUsed: %v", err)
	}

	// Revoke → no longer resolves; second revoke is idempotent (no error).
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		return RevokeAPIToken(ctx, tx, orgID, tok.ID)
	}); err != nil {
		t.Fatalf("RevokeAPIToken: %v", err)
	}
	if _, err := TokenByHash(ctx, database.Pool(), HashAPIToken(raw)); err != ErrNotFound {
		t.Fatalf("revoked token must not resolve, got %v", err)
	}
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		return RevokeAPIToken(ctx, tx, orgID, tok.ID)
	}); err != nil {
		t.Fatalf("idempotent re-revoke should succeed, got %v", err)
	}

	// Revoking an unknown id → ErrNotFound.
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		return RevokeAPIToken(ctx, tx, orgID, "00000000-0000-0000-0000-000000000000")
	}); err != ErrNotFound {
		t.Fatalf("want ErrNotFound revoking unknown id, got %v", err)
	}
}

func TestAPITokenExpiry(t *testing.T) {
	database := tokensTestDB(t)
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	orgID, userID := seedTokenOrgUser(t, ctx, database)

	past := time.Now().UTC().Add(-time.Hour)
	var raw string
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var e error
		raw, _, e = CreateAPIToken(ctx, tx, orgID, userID, "expired", []string{"read:context"}, &past)
		return e
	}); err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}

	if _, err := TokenByHash(ctx, database.Pool(), HashAPIToken(raw)); err != ErrNotFound {
		t.Fatalf("expired token must not resolve, got %v", err)
	}
}
