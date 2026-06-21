// Package middleware — apitoken_test.go
// DB-backed test that RequireToken / RequireScope admit a live, in-scope API token
// and reject a missing, malformed, revoked, or out-of-scope one. Drives the real
// store + SECURITY DEFINER resolution path. Skips when DATABASE_URL is unset.
package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

func mwTestDB(t *testing.T) *db.DB {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping apitoken middleware test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	database, err := db.New(ctx, &config.Config{Database: config.DatabaseConfig{URL: dbURL}})
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	return database
}

func TestRequireTokenAndScope(t *testing.T) {
	database := mwTestDB(t)
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ns := time.Now().UnixNano()
	var orgID, userID string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("mw-%d", ns), "MW Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO users (email, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("mw-%d@example.test", ns), "MW User").Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, orgID)
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM users WHERE id=$1`, userID)
	})

	var raw string
	var tok *store.APIToken
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var e error
		raw, tok, e = store.CreateAPIToken(ctx, tx, orgID, userID, "mw-agent", []string{"read:context"}, nil)
		return e
	}); err != nil {
		t.Fatalf("create token: %v", err)
	}

	// A handler that asserts org/user/scope resolution flowed through context.
	guarded := RequireToken(database)(RequireScope("read:context")(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if OrgFromContext(r.Context()) != orgID {
				t.Errorf("org not resolved: got %q", OrgFromContext(r.Context()))
			}
			if u := UserFromContext(r.Context()); u == nil || u.ID != userID {
				t.Errorf("user not resolved: %+v", u)
			}
			if p := TokenFromContext(r.Context()); p == nil || p.TokenID != tok.ID {
				t.Errorf("token principal missing: %+v", p)
			}
			w.WriteHeader(http.StatusOK)
		})))

	do := func(auth string) int {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		guarded.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := do("Bearer " + raw); code != http.StatusOK {
		t.Fatalf("valid token: want 200, got %d", code)
	}
	if code := do(""); code != http.StatusUnauthorized {
		t.Fatalf("missing header: want 401, got %d", code)
	}
	if code := do("Bearer gsk_totally_bogus"); code != http.StatusUnauthorized {
		t.Fatalf("bad token: want 401, got %d", code)
	}

	// Out-of-scope: token has read:context, demand write:issues → 403.
	scopeGuard := RequireToken(database)(RequireScope("write:issues")(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	rec := httptest.NewRecorder()
	scopeGuard.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("out-of-scope: want 403, got %d", rec.Code)
	}

	// Revoke → 401.
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		return store.RevokeAPIToken(ctx, tx, orgID, tok.ID)
	}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if code := do("Bearer " + raw); code != http.StatusUnauthorized {
		t.Fatalf("revoked token: want 401, got %d", code)
	}
}
