package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/exo/gitstate/internal/auth"
	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/crypto"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// appEnabledCfg returns a config with the GitHub App enabled (App ID + key + slug).
func appEnabledCfg(signingKey string) *config.Config {
	cfg := &config.Config{}
	cfg.Auth.JWTSigningKey = signingKey
	cfg.App.PublicURL = "https://gitstate.example"
	cfg.Git.GitHub.AppID = "123456"
	cfg.Git.GitHub.AppSlug = "my-gitstate-app"
	cfg.Git.GitHub.AppPrivateKey = "-----BEGIN RSA PRIVATE KEY-----\nfake\n-----END RSA PRIVATE KEY-----"
	cfg.Git.GitHub.AppEnabled = true
	return cfg
}

// TestGitHubAppInstall_SetsStateCookieAnd302 drives the install handler and asserts
// it self-authenticates from ?token=&org=, sets the CSRF state cookie, and 302s to
// the GitHub App install URL. No DB needed.
func TestGitHubAppInstall_SetsStateCookieAnd302(t *testing.T) {
	const signingKey = "test-signing-key-app-install"
	cfg := appEnabledCfg(signingKey)

	h := &connectHandlers{cfg: cfg}

	tok, err := auth.IssueAccessToken(signingKey, "user-1", "u@example.test", "User", time.Hour)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet,
		"/api/connect/github/app/install?token="+tok+"&org=org-123", nil)
	rec := httptest.NewRecorder()
	h.appInstall(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://github.com/apps/my-gitstate-app/installations/new?state=") {
		t.Fatalf("Location = %q, want App install URL", loc)
	}

	// State cookie must be set and carry a non-empty value.
	var stateCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == connectStateCookie {
			stateCookie = c
		}
	}
	if stateCookie == nil || stateCookie.Value == "" {
		t.Fatal("expected a non-empty state cookie")
	}
}

// TestGitHubAppInstall_Unauthorized: missing/invalid token → 401.
func TestGitHubAppInstall_Unauthorized(t *testing.T) {
	const signingKey = "test-signing-key-app-install-401"
	h := &connectHandlers{cfg: appEnabledCfg(signingKey)}

	req := httptest.NewRequest(http.MethodGet, "/api/connect/github/app/install?org=org-123", nil)
	rec := httptest.NewRecorder()
	h.appInstall(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// TestGitHubAppRoutesGatedOnAppEnabled: the install/callback routes are only wired
// when AppEnabled. With the App disabled, the install path is not registered.
func TestGitHubAppRoutesGatedOnAppEnabled(t *testing.T) {
	database := apiTestDB(t)
	defer database.Close()

	const signingKey = "test-signing-key-app-gate"
	cfg := &config.Config{}
	cfg.Auth.JWTSigningKey = signingKey
	// AppEnabled stays false.

	mux := http.NewServeMux()
	RegisterConnectRoutes(mux, database, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/connect/github/app/install", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("with App disabled, install route status = %d, want 404", rec.Code)
	}
}

// TestGitHubAppCallback_StoresConnection drives the public Setup-URL callback and
// asserts a github_app connection is persisted with the installation_id. The
// installation-login lookup hits GitHub with a fake key and fails best-effort, so
// the connection is still stored (login empty). DB-backed.
func TestGitHubAppCallback_StoresConnection(t *testing.T) {
	database := apiTestDB(t)
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const signingKey = "test-signing-key-app-callback"
	cfg := appEnabledCfg(signingKey)
	h := &connectHandlers{db: database, cfg: cfg}

	ns := time.Now().UnixNano()
	var orgID string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("app-cb-%d", ns), "App CB Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	defer func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, orgID)
	}()

	// Build the state cookie the install step would have set.
	state := "csrf-state-value"
	cs := connectState{State: state, OrgID: orgID, UserID: "", Platform: "github"}
	cookieVal := encodeConnectState(t, cs)

	const installationID = "987654"
	req := httptest.NewRequest(http.MethodGet,
		"/api/connect/github/app/callback?installation_id="+installationID+"&setup_action=install&state="+state, nil)
	req.AddCookie(&http.Cookie{Name: connectStateCookie, Value: cookieVal})
	rec := httptest.NewRecorder()

	h.appCallback(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want 302; body=%s", rec.Code, rec.Body.String())
	}

	// The connection must be stored as github_app with the installation_id.
	var conn *store.PlatformConnection
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		c, e := store.GetConnection(ctx, tx, orgID, "github")
		conn = c
		return e
	}); err != nil {
		t.Fatalf("get stored connection: %v", err)
	}
	if conn.ConnectionType != "github_app" {
		t.Fatalf("ConnectionType = %q, want github_app", conn.ConnectionType)
	}
	if conn.InstallationID != installationID {
		t.Fatalf("InstallationID = %q, want %s", conn.InstallationID, installationID)
	}
	if len(conn.TokenEncrypted) != 0 {
		t.Fatal("github_app connection should be stored with no token (minted on demand)")
	}
}

// TestResolveStoredToken_GitHubAppCachedToken seeds a github_app connection with a
// FRESH cached token (encrypted, expiry well in the future) and asserts
// resolveStoredToken returns the cached token WITHOUT minting (no network). This
// exercises the github_app branch + the >5min cache-reuse rule.
func TestResolveStoredToken_GitHubAppCachedToken(t *testing.T) {
	database := apiTestDB(t)
	defer database.Close()

	t.Setenv("TOKEN_ENC_KEY", "test-token-enc-key-for-app-cache")
	key, err := crypto.KeyFromEnv()
	if err != nil {
		t.Fatalf("key from env: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cfg := appEnabledCfg("unused-signing-key")

	ns := time.Now().UnixNano()
	var orgID string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("app-tok-%d", ns), "App Tok Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	defer func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, orgID)
	}()

	const cachedToken = "ghs_cached_installation_token"
	enc, err := crypto.Encrypt([]byte(cachedToken), key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	future := time.Now().Add(45 * time.Minute).UTC()
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		_, e := store.UpsertConnection(ctx, tx, store.UpsertConnectionInput{
			OrgID:          orgID,
			Platform:       "github",
			ExternalLogin:  "acme",
			TokenEncrypted: enc,
			ExpiresAt:      &future,
			ConnectionType: "github_app",
			InstallationID: "555",
		})
		return e
	}); err != nil {
		t.Fatalf("seed github_app connection: %v", err)
	}

	tok, baseURL, err := resolveStoredToken(ctx, database, cfg, orgID, "github")
	if err != nil {
		t.Fatalf("resolveStoredToken: %v", err)
	}
	if tok != cachedToken {
		t.Fatalf("token = %q, want the cached installation token", tok)
	}
	if baseURL != "" {
		t.Fatalf("baseURL = %q, want empty (github.com)", baseURL)
	}
}

// encodeConnectState mirrors the base64(JSON) encoding the install handler uses for
// the state cookie.
func encodeConnectState(t *testing.T, cs connectState) string {
	t.Helper()
	raw, err := json.Marshal(cs)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}
