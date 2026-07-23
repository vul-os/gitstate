// Package api — profile_test.go
// Covers the self-profile endpoints (GET/PATCH /api/profile) that let a user set a
// real contact email when an OAuth login (e.g. GitHub with a hidden email) only
// yielded a `@users.noreply.*` placeholder. DB-backed parts skip without DATABASE_URL.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/exo/gitstate/internal/auth"
	"github.com/exo/gitstate/internal/config"
)

func TestProfile_HelperDetectsPlaceholderAndEmail(t *testing.T) {
	placeholders := []string{
		"octocat@users.noreply.github.com",
		"123-name@users.noreply.gitlab.com",
		"X@USERS.NOREPLY.GITHUB.COM",
	}
	for _, e := range placeholders {
		if !emailIsPlaceholder(e) {
			t.Errorf("emailIsPlaceholder(%q) = false, want true", e)
		}
	}
	real := []string{"jane@acme.com", "dev@example.test", "a.b+c@sub.domain.io"}
	for _, e := range real {
		if emailIsPlaceholder(e) {
			t.Errorf("emailIsPlaceholder(%q) = true, want false", e)
		}
	}

	good := []string{"a@b.co", "jane.doe@acme.com"}
	for _, e := range good {
		if !looksLikeEmail(e) {
			t.Errorf("looksLikeEmail(%q) = false", e)
		}
	}
	bad := []string{"", "no-at-sign", "@nodomain", "a@b", "a b@c.com", "trailing@"}
	for _, e := range bad {
		if looksLikeEmail(e) {
			t.Errorf("looksLikeEmail(%q) = true", e)
		}
	}
}

func TestProfile_GetUpdateAndConflicts(t *testing.T) {
	database := apiTestDB(t)
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const key = "test-signing-key-for-profile"
	cfg := &config.Config{}
	cfg.Auth.JWTSigningKey = key

	ns := time.Now().UnixNano()
	var orgID string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("profile-%d", ns), "Profile Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	defer func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, orgID)
	}()

	u1ID, u1Tok := seedMember(t, ctx, database, key, orgID, "owner")
	u2ID, _ := seedMember(t, ctx, database, key, orgID, "member")
	var u2Email string
	if err := database.Pool().QueryRow(ctx, `SELECT email FROM users WHERE id=$1`, u2ID).Scan(&u2Email); err != nil {
		t.Fatalf("read u2 email: %v", err)
	}
	// Clean up the directly-created users (org cascade only covers memberships).
	defer func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM users WHERE id IN ($1,$2)`, u1ID, u2ID)
	}()

	mux := http.NewServeMux()
	RegisterProfileRoutes(mux, database, cfg)
	do := func(method, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/profile", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+u1Tok)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	decode := func(rec *httptest.ResponseRecorder) profileResponse {
		var p profileResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
		}
		return p
	}

	// GET → 200, seeded email is a real one (not placeholder).
	if rec := do(http.MethodGet, ""); rec.Code != http.StatusOK {
		t.Fatalf("GET profile = %d, body=%s", rec.Code, rec.Body.String())
	} else if p := decode(rec); p.EmailIsPlaceholder {
		t.Errorf("seeded email flagged placeholder: %q", p.Email)
	}

	// PATCH name + email → 200, persisted.
	newEmail := fmt.Sprintf("real-%d@example.com", ns)
	rec := do(http.MethodPatch, fmt.Sprintf(`{"name":"Renamed","email":%q}`, newEmail))
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH = %d, body=%s", rec.Code, rec.Body.String())
	}
	if p := decode(rec); p.Name != "Renamed" || !strings.EqualFold(p.Email, newEmail) {
		t.Errorf("after PATCH: name=%q email=%q", p.Name, p.Email)
	}

	// PATCH to a noreply placeholder → 400 (refuse fake contact emails).
	if rec := do(http.MethodPatch, `{"email":"who@users.noreply.github.com"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("PATCH placeholder email = %d, want 400", rec.Code)
	}

	// PATCH to another user's email → 409.
	if rec := do(http.MethodPatch, fmt.Sprintf(`{"email":%q}`, u2Email)); rec.Code != http.StatusConflict {
		t.Errorf("PATCH taken email = %d, want 409 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestProfile_GetFlagsNoreplyPlaceholder(t *testing.T) {
	database := apiTestDB(t)
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const key = "test-signing-key-for-profile-ph"
	cfg := &config.Config{}
	cfg.Auth.JWTSigningKey = key

	ns := time.Now().UnixNano()
	email := fmt.Sprintf("ghuser%d@users.noreply.github.com", ns)
	var uid string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO users (email, name) VALUES ($1,'GH User') RETURNING id`, email).Scan(&uid); err != nil {
		t.Fatalf("create user: %v", err)
	}
	defer func() { _, _ = database.Pool().Exec(context.Background(), `DELETE FROM users WHERE id=$1`, uid) }()

	tok, err := auth.IssueAccessToken(key, uid, email, "GH User", time.Hour)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	mux := http.NewServeMux()
	RegisterProfileRoutes(mux, database, cfg)
	req := httptest.NewRequest(http.MethodGet, "/api/profile", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d", rec.Code)
	}
	var p profileResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !p.EmailIsPlaceholder {
		t.Errorf("noreply email not flagged as placeholder: %q", p.Email)
	}
}
