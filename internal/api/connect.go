package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/exo/gitstate/internal/auth"
	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/crypto"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/middleware"
	oauthpkg "github.com/exo/gitstate/internal/oauth"
	"github.com/exo/gitstate/internal/store"
	gitSync "github.com/exo/gitstate/internal/sync"
	"github.com/jackc/pgx/v5"
)

// RegisterConnectRoutes wires the GitHub/GitLab OAuth-app connection endpoints.
// A user authorizes once; the org's access token is stored AES-256-GCM encrypted
// in platform_connections, and sync uses it without re-supplying a PAT.
//
// Routes (all require RequireAuth + OrgScope except the provider callback, which
// recovers the org from a signed-ish state cookie):
//
//	GET    /api/connect/{platform}/start     → 302 to provider authorize (404 if not configured)
//	GET    /api/connect/{platform}/callback  → exchange code, encrypt+store token, redirect to /repos
//	GET    /api/connect/status               → [{platform, connected, login}] for the org
//	GET    /api/connect/{platform}/repos     → repos available to the stored token (for import picking)
//	DELETE /api/connect/{platform}           → disconnect
//
// Called by the orchestrator from router.go — this package does NOT edit router.go.
func RegisterConnectRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	providers := oauthpkg.LoadConnections(cfg, cfg.App.PublicURL)
	h := &connectHandlers{db: database, cfg: cfg, providers: providers}

	requireAuth := middleware.RequireAuth(cfg.Auth.JWTSigningKey)
	orgScope := middleware.OrgScope(database.Pool())
	auth := func(handler http.Handler) http.Handler {
		return requireAuth(orgScope(handler))
	}

	// status/repos/disconnect are called via authenticated fetch (Bearer + X-Org-ID).
	mux.Handle("GET /api/connect/status", auth(http.HandlerFunc(h.status)))
	mux.Handle("GET /api/connect/{platform}/repos", auth(http.HandlerFunc(h.listRepos)))
	mux.Handle("DELETE /api/connect/{platform}", auth(http.HandlerFunc(h.disconnect)))

	// start is a top-level browser navigation (so the provider redirect lands on
	// a real page), which can't send the Bearer/X-Org-ID headers the middleware
	// needs. It self-authenticates from ?token= and ?org= query params instead.
	mux.HandleFunc("GET /api/connect/{platform}/start", h.start)

	// callback is a top-level provider redirect (no Authorization header) — the
	// org/user are recovered from the state cookie set at /start.
	mux.HandleFunc("GET /api/connect/{platform}/callback", h.callback)
}

type connectHandlers struct {
	db        *db.DB
	cfg       *config.Config
	providers oauthpkg.ConnProviders
}

// connectStateCookie carries the CSRF state plus the org/user that initiated the
// flow across the provider redirect (the callback has no auth context).
const connectStateCookie = "gs_connect_state"

// connectState is the JSON value stored (base64) in the state cookie.
type connectState struct {
	State    string `json:"s"`
	OrgID    string `json:"o"`
	UserID   string `json:"u"`
	Platform string `json:"p"`
}

// ── GET /api/connect/{platform}/start ──────────────────────────────────────────

func (h *connectHandlers) start(w http.ResponseWriter, r *http.Request) {
	platform := strings.ToLower(r.PathValue("platform"))
	p, ok := h.providers[platform]
	if !ok {
		writeError(w, http.StatusNotFound, "platform not configured for OAuth")
		return
	}

	// Self-authenticate: a top-level browser navigation cannot send the Bearer
	// or X-Org-ID headers, so the access token + active org arrive as query
	// params. Verify the JWT and require a non-empty org.
	tokenStr := r.URL.Query().Get("token")
	orgID := r.URL.Query().Get("org")
	if tokenStr == "" {
		if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			tokenStr = bearer
		}
	}
	claims, err := auth.ParseAccessToken(h.cfg.Auth.JWTSigningKey, tokenStr)
	if err != nil || orgID == "" {
		writeError(w, http.StatusUnauthorized, "missing or invalid auth")
		return
	}
	userID := claims.UserID()

	stateVal, err := oauthpkg.GenerateState()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generate state")
		return
	}

	cs := connectState{State: stateVal, OrgID: orgID, UserID: userID, Platform: platform}
	raw, _ := json.Marshal(cs)
	cookieVal := base64.RawURLEncoding.EncodeToString(raw)

	http.SetCookie(w, &http.Cookie{
		Name:     connectStateCookie,
		Value:    cookieVal,
		Path:     "/",
		MaxAge:   600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.URL.Scheme == "https",
	})

	http.Redirect(w, r, p.AuthCodeURL(stateVal), http.StatusFound)
}

// ── GET /api/connect/{platform}/callback ───────────────────────────────────────

func (h *connectHandlers) callback(w http.ResponseWriter, r *http.Request) {
	platform := strings.ToLower(r.PathValue("platform"))
	p, ok := h.providers[platform]
	if !ok {
		writeError(w, http.StatusNotFound, "platform not configured for OAuth")
		return
	}

	// Recover + verify CSRF state and the initiating org/user from the cookie.
	cookie, err := r.Cookie(connectStateCookie)
	if err != nil || cookie.Value == "" {
		writeError(w, http.StatusBadRequest, "missing state cookie")
		return
	}
	rawState, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid state cookie")
		return
	}
	var cs connectState
	if err := json.Unmarshal(rawState, &cs); err != nil {
		writeError(w, http.StatusBadRequest, "invalid state cookie")
		return
	}
	// Clear the state cookie.
	http.SetCookie(w, &http.Cookie{
		Name: connectStateCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})

	if r.URL.Query().Get("state") != cs.State || cs.Platform != platform || cs.OrgID == "" {
		writeError(w, http.StatusBadRequest, "state mismatch")
		return
	}

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		h.redirectRepos(w, r, platform, "error="+errParam)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, "missing code")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	token, acct, err := p.Exchange(ctx, code)
	if err != nil {
		slog.Error("connect: exchange", "platform", platform, "err", err)
		h.redirectRepos(w, r, platform, "error=exchange_failed")
		return
	}

	// Encrypt the access (and refresh) token before persisting (S3).
	key, err := crypto.KeyFromEnv()
	if err != nil {
		slog.Error("connect: encryption key", "err", err)
		h.redirectRepos(w, r, platform, "error=server_misconfigured")
		return
	}
	encToken, err := crypto.Encrypt([]byte(token.AccessToken), key)
	if err != nil {
		slog.Error("connect: encrypt token", "err", err)
		h.redirectRepos(w, r, platform, "error=encrypt_failed")
		return
	}
	var encRefresh []byte
	if token.RefreshToken != "" {
		if encRefresh, err = crypto.Encrypt([]byte(token.RefreshToken), key); err != nil {
			slog.Error("connect: encrypt refresh", "err", err)
		}
	}

	in := store.UpsertConnectionInput{
		OrgID:            cs.OrgID,
		Platform:         platform,
		ConnectedBy:      cs.UserID,
		ExternalLogin:    acct.Login,
		TokenEncrypted:   encToken,
		RefreshEncrypted: encRefresh,
		Scopes:           p.Scopes(),
	}
	if !token.Expiry.IsZero() {
		exp := token.Expiry.UTC()
		in.ExpiresAt = &exp
	}

	if err := h.db.WithOrg(r.Context(), cs.OrgID, func(tx pgx.Tx) error {
		_, e := store.UpsertConnection(r.Context(), tx, in)
		return e
	}); err != nil {
		slog.Error("connect: store connection", "platform", platform, "err", err)
		h.redirectRepos(w, r, platform, "error=store_failed")
		return
	}

	h.redirectRepos(w, r, platform, "connected="+platform)
}

func (h *connectHandlers) redirectRepos(w http.ResponseWriter, r *http.Request, platform, query string) {
	url := fmt.Sprintf("%s/repos?%s", h.cfg.App.PublicURL, query)
	http.Redirect(w, r, url, http.StatusFound)
}

// ── GET /api/connect/status ────────────────────────────────────────────────────

type connectStatus struct {
	Platform   string `json:"platform"`
	Connected  bool   `json:"connected"`
	Login      string `json:"login,omitempty"`
	Configured bool   `json:"configured"`
}

func (h *connectHandlers) status(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())

	// Start from the platforms that have OAuth configured.
	byPlatform := map[string]*connectStatus{}
	for _, plat := range []string{"github", "gitlab"} {
		_, configured := h.providers[plat]
		byPlatform[plat] = &connectStatus{Platform: plat, Configured: configured}
	}

	var conns []store.PlatformConnection
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		c, e := store.ListConnections(r.Context(), tx, orgID)
		conns = c
		return e
	}); err != nil {
		writeSyncError(w, "list connections", err)
		return
	}
	for _, c := range conns {
		if s, ok := byPlatform[c.Platform]; ok {
			s.Connected = true
			s.Login = c.ExternalLogin
		}
	}

	out := []connectStatus{*byPlatform["github"], *byPlatform["gitlab"]}
	writeJSON(w, http.StatusOK, out)
}

// ── GET /api/connect/{platform}/repos ──────────────────────────────────────────

type connectRepoOption struct {
	ExternalID    string `json:"externalId"`
	FullName      string `json:"fullName"`
	DefaultBranch string `json:"defaultBranch"`
	CloneURL      string `json:"cloneURL"`
}

func (h *connectHandlers) listRepos(w http.ResponseWriter, r *http.Request) {
	platform := strings.ToLower(r.PathValue("platform"))
	orgID := middleware.OrgFromContext(r.Context())

	token, baseURL, err := h.resolveConnectionToken(r.Context(), orgID, platform)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no connection for platform")
			return
		}
		writeSyncError(w, "resolve connection token", err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	provider, err := gitSync.NewProvider(ctx, platform, token, baseURL)
	if err != nil {
		writeSyncError(w, "build provider", err)
		return
	}
	repos, err := provider.ListRepos(ctx)
	if err != nil {
		writeSyncError(w, "list repos from platform", err)
		return
	}

	out := make([]connectRepoOption, 0, len(repos))
	for _, rr := range repos {
		out = append(out, connectRepoOption{
			ExternalID:    rr.ExternalID,
			FullName:      rr.FullName,
			DefaultBranch: rr.DefaultBranch,
			CloneURL:      rr.CloneURL,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// ── DELETE /api/connect/{platform} ─────────────────────────────────────────────

func (h *connectHandlers) disconnect(w http.ResponseWriter, r *http.Request) {
	platform := strings.ToLower(r.PathValue("platform"))
	orgID := middleware.OrgFromContext(r.Context())

	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		return store.DeleteConnection(r.Context(), tx, orgID, platform)
	}); err != nil {
		writeSyncError(w, "disconnect", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Shared: resolve the org's stored connection token (decrypt) ────────────────

// resolveConnectionToken returns the decrypted access token + base URL for the
// org's stored connection on platform. Returns store.ErrNotFound when no
// connection exists. Tokens are never logged.
func (h *connectHandlers) resolveConnectionToken(ctx context.Context, orgID, platform string) (string, string, error) {
	return resolveStoredToken(ctx, h.db, orgID, platform)
}

// resolveStoredToken fetches + decrypts the org's stored connection token for a
// platform. Shared by connect.go and sync.go so syncing uses the stored token.
func resolveStoredToken(ctx context.Context, database *db.DB, orgID, platform string) (token string, baseURL string, err error) {
	key, keyErr := crypto.KeyFromEnv()
	if keyErr != nil {
		return "", "", keyErr
	}

	var conn *store.PlatformConnection
	if e := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		c, ge := store.GetConnection(ctx, tx, orgID, platform)
		conn = c
		return ge
	}); e != nil {
		return "", "", e
	}
	if len(conn.TokenEncrypted) == 0 {
		return "", "", store.ErrNotFound
	}

	pt, e := crypto.Decrypt(conn.TokenEncrypted, key)
	if e != nil {
		return "", "", fmt.Errorf("decrypt connection token: %w", e)
	}
	return string(pt), conn.BaseURL, nil
}
