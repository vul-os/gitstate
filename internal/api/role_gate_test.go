// Package api — role_gate_test.go
// DB-backed HTTP tests asserting the owner/admin role gates added to the
// mutating webhook-secret route. A plain member must be 403'd on every
// mutating endpoint; an owner is admitted.
//
// The tests drive the REAL middleware chain (RequireAuth + OrgScope) by minting a
// valid access token and setting X-Org-ID, then seeding throwaway orgs/users and
// cleaning them up (cascade) at the end. They skip cleanly when DATABASE_URL is
// unset.
package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/exo/gitstate/internal/auth"
	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
)

// seedMember inserts a user and an org_members row with the given role, returning
// the new user's id and a signed access token for it.
func seedMember(t *testing.T, ctx context.Context, database *db.DB, signingKey, orgID, role string) (userID, token string) {
	t.Helper()
	ns := time.Now().UnixNano()
	email := fmt.Sprintf("role-%s-%d@example.test", role, ns)
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO users (email, name) VALUES ($1, $2) RETURNING id`,
		email, role).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := database.Pool().Exec(ctx,
		`INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, $3)`,
		orgID, userID, role); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	tok, err := auth.IssueAccessToken(signingKey, userID, email, role, time.Hour)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return userID, tok
}

func TestRoleGates_MutatingRoutesRequireManager(t *testing.T) {
	database := apiTestDB(t)
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const signingKey = "test-signing-key-for-role-gate"
	cfg := &config.Config{}
	cfg.Auth.JWTSigningKey = signingKey

	ns := time.Now().UnixNano()
	var orgID string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("role-gate-%d", ns), "Role Gate Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	defer func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM organizations WHERE id = $1`, orgID)
	}()

	_, memberTok := seedMember(t, ctx, database, signingKey, orgID, "member")
	_, ownerTok := seedMember(t, ctx, database, signingKey, orgID, "owner")

	// Build the real authed mux for webhooks.
	mux := http.NewServeMux()
	RegisterWebhookRoutes(mux, database, cfg)

	do := func(method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Org-ID", orgID)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	// Every mutating route must 403 a plain member.
	memberCases := []struct {
		name, method, path, body string
	}{
		{"rotateWebhookSecret", http.MethodPost, "/api/webhooks/config", `{"provider":"github"}`},
	}
	for _, c := range memberCases {
		rec := do(c.method, c.path, memberTok, c.body)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s as member: status = %d, want 403 (body=%s)", c.name, rec.Code, rec.Body.String())
		}
	}

	// An owner is admitted past the gate. rotateWebhookSecret should succeed
	// (200). This proves the gate lets managers through (not merely that
	// everyone is blocked).
	if rec := do(http.MethodPost, "/api/webhooks/config", ownerTok, `{"provider":"github"}`); rec.Code != http.StatusOK {
		t.Errorf("rotateWebhookSecret as owner: status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	t.Logf("role gates OK: member 403 on all mutating routes; owner admitted")
}
