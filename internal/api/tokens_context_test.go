// Package api — tokens_context_test.go
// End-to-end HTTP tests over the REAL middleware chain for:
//   - token management (POST/GET/DELETE /api/tokens, owner-gated, raw shown once),
//   - the agent context endpoint (GET /api/context/issue/{id}) authenticated by
//     the freshly-minted API token + read:context scope.
//
// Skips when DATABASE_URL is unset.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/exo/gitstate/internal/auth"
	"github.com/exo/gitstate/internal/config"
	"github.com/jackc/pgx/v5"
)

func TestTokenManagementAndContextE2E(t *testing.T) {
	database := apiTestDB(t)
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const signingKey = "test-signing-key-for-tokens"
	cfg := &config.Config{}
	cfg.Auth.JWTSigningKey = signingKey

	ns := time.Now().UnixNano()
	var orgID, ownerID, memberID, issueID string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("tokapi-%d", ns), "TokAPI Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, orgID)
	})

	ownerEmail := fmt.Sprintf("owner-%d@example.test", ns)
	if err := database.Pool().QueryRow(ctx, `INSERT INTO users (email,name) VALUES ($1,'Owner') RETURNING id`, ownerEmail).Scan(&ownerID); err != nil {
		t.Fatalf("owner: %v", err)
	}
	memberEmail := fmt.Sprintf("member-%d@example.test", ns)
	if err := database.Pool().QueryRow(ctx, `INSERT INTO users (email,name) VALUES ($1,'Member') RETURNING id`, memberEmail).Scan(&memberID); err != nil {
		t.Fatalf("member: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM users WHERE id IN ($1,$2)`, ownerID, memberID)
	})
	if _, err := database.Pool().Exec(ctx, `INSERT INTO org_members (org_id,user_id,role) VALUES ($1,$2,'owner'),($1,$3,'member')`, orgID, ownerID, memberID); err != nil {
		t.Fatalf("members: %v", err)
	}

	// Seed an issue to fetch context for.
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`INSERT INTO issues (org_id, source, title, body, state, labels) VALUES ($1,'native','Seeded issue','body','open',$2) RETURNING id`,
			orgID, []string{"bug"}).Scan(&issueID)
	}); err != nil {
		t.Fatalf("seed issue: %v", err)
	}

	ownerTok, err := auth.IssueAccessToken(signingKey, ownerID, ownerEmail, "Owner", time.Hour)
	if err != nil {
		t.Fatalf("owner jwt: %v", err)
	}
	memberTok, err := auth.IssueAccessToken(signingKey, memberID, memberEmail, "Member", time.Hour)
	if err != nil {
		t.Fatalf("member jwt: %v", err)
	}

	tokenMux := http.NewServeMux()
	RegisterTokenRoutes(tokenMux, database, cfg)
	ctxMux := http.NewServeMux()
	RegisterContextRoutes(ctxMux, database, cfg)

	// Member cannot create a token (owner/admin gate).
	{
		body, _ := json.Marshal(map[string]any{"name": "x", "scopes": []string{"read:context"}})
		req := httptest.NewRequest(http.MethodPost, "/api/tokens", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+memberTok)
		req.Header.Set("X-Org-ID", orgID)
		rec := httptest.NewRecorder()
		tokenMux.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("member create token: want 403, got %d (%s)", rec.Code, rec.Body.String())
		}
	}

	// Owner creates a read:context token → raw returned once.
	var rawToken string
	{
		body, _ := json.Marshal(map[string]any{"name": "agent", "scopes": []string{"read:context", "read:issues"}})
		req := httptest.NewRequest(http.MethodPost, "/api/tokens", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+ownerTok)
		req.Header.Set("X-Org-ID", orgID)
		rec := httptest.NewRecorder()
		tokenMux.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("owner create token: want 201, got %d (%s)", rec.Code, rec.Body.String())
		}
		var resp createTokenResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode create resp: %v", err)
		}
		if resp.Token == "" || resp.Token[:4] != "gsk_" {
			t.Fatalf("raw token not returned: %q", resp.Token)
		}
		rawToken = resp.Token
	}

	// List shows the token (no raw/hash field present).
	{
		req := httptest.NewRequest(http.MethodGet, "/api/tokens", nil)
		req.Header.Set("Authorization", "Bearer "+ownerTok)
		req.Header.Set("X-Org-ID", orgID)
		rec := httptest.NewRecorder()
		tokenMux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("list tokens: want 200, got %d", rec.Code)
		}
		var rows []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("want 1 token, got %d", len(rows))
		}
		if _, leaked := rows[0]["token"]; leaked {
			t.Fatalf("list leaked raw token")
		}
		if _, leaked := rows[0]["tokenHash"]; leaked {
			t.Fatalf("list leaked token hash")
		}
	}

	// The API token authenticates the context endpoint (no X-Org-ID needed).
	{
		req := httptest.NewRequest(http.MethodGet, "/api/context/issue/"+issueID, nil)
		req.Header.Set("Authorization", "Bearer "+rawToken)
		rec := httptest.NewRecorder()
		ctxMux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("context via token: want 200, got %d (%s)", rec.Code, rec.Body.String())
		}
		var bundle map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &bundle); err != nil {
			t.Fatalf("decode bundle: %v", err)
		}
		iss, _ := bundle["issue"].(map[string]any)
		if iss == nil || iss["title"] != "Seeded issue" {
			t.Fatalf("bundle issue wrong: %v", bundle["issue"])
		}
	}

	// A human JWT (owner) can also call context (implicit read scope) with X-Org-ID.
	{
		req := httptest.NewRequest(http.MethodGet, "/api/context/issue/"+issueID, nil)
		req.Header.Set("Authorization", "Bearer "+ownerTok)
		req.Header.Set("X-Org-ID", orgID)
		rec := httptest.NewRecorder()
		ctxMux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("context via JWT: want 200, got %d (%s)", rec.Code, rec.Body.String())
		}
	}
}
