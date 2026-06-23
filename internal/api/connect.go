package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	gogithub "github.com/google/go-github/v66/github"
	"github.com/jackc/pgx/v5"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
	"golang.org/x/oauth2"

	"github.com/exo/gitstate/internal/auth"
	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/crypto"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/middleware"
	oauthpkg "github.com/exo/gitstate/internal/oauth"
	"github.com/exo/gitstate/internal/store"
	gitSync "github.com/exo/gitstate/internal/sync"
	"github.com/exo/gitstate/internal/webhooks"
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

// ── Webhook auto-registration on connect ───────────────────────────────────────
//
// When a repo is connected we register a platform webhook pointing at our public
// receiver so the platform PUSHES changes (push/PR/issue/review/deployment) — the
// ongoing real-time sync layer — instead of us polling. It is:
//
//   - GATED on a publicly-reachable PublicURL. GitHub/GitLab cannot deliver to
//     localhost/127.0.0.1/private hosts, so when PublicURL is empty or local we
//     SKIP (log INFO) and do NOT error. Webhooks are a deploy-time feature; the
//     initial backfill is what runs locally.
//   - IDEMPOTENT: existing hooks are listed and one already pointing at our URL is
//     left in place (its event set / secret are refreshed if drifted), never
//     duplicated.
//   - SECURED with the org's webhook secret (reused, or generated once via the
//     existing webhooks.GenerateSecret + store.UpsertWebhookSecret mechanism).
//
// It builds a DIRECT platform client from the stored token (no dependency on
// internal/sync's Provider). Best-effort: every failure logs and returns nil so a
// webhook hiccup never blocks the connect/import.

// ghWebhookEvents are the GitHub events we subscribe the auto-registered hook to.
var ghWebhookEvents = []string{
	"push", "pull_request", "pull_request_review", "issues",
	"deployment", "deployment_status",
}

// publiclyReachable reports whether rawURL is an https(/http) URL on a host the
// platforms can actually deliver to (not localhost / loopback / private / .local).
func publiclyReachable(rawURL string) bool {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	hl := strings.ToLower(host)
	if hl == "localhost" || strings.HasSuffix(hl, ".localhost") || strings.HasSuffix(hl, ".local") || hl == "host.docker.internal" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
			return false
		}
	}
	return true
}

// receiverURL builds the public payload URL the platform delivers to (carrying
// the org hint the receiver uses to find the secret pre-auth).
func receiverURL(publicURL, platform, orgID string) string {
	return strings.TrimRight(publicURL, "/") + "/api/webhooks/" + platform + "?org=" + orgID
}

// ensureWebhookSecret returns the org's stored webhook secret for the provider,
// generating + persisting one (the existing reveal-once mechanism) if absent.
func ensureWebhookSecret(ctx context.Context, database *db.DB, orgID, provider string) (string, error) {
	var secret string
	err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		c, e := store.GetWebhookConfig(ctx, tx, orgID, provider)
		if e == nil && c.Secret != "" {
			secret = c.Secret
			return nil
		}
		if e != nil && !errors.Is(e, store.ErrNotFound) {
			return e
		}
		gen, e := webhooks.GenerateSecret()
		if e != nil {
			return e
		}
		saved, e := store.UpsertWebhookSecret(ctx, tx, orgID, provider, gen)
		if e != nil {
			return e
		}
		secret = saved.Secret
		return nil
	})
	return secret, err
}

// autoRegisterRepoWebhook idempotently registers (or updates) the platform repo
// webhook pointing at our receiver. Gated on a publicly-reachable PublicURL.
// Best-effort: it never returns an error that should block the connect — callers
// may ignore the returned error (logged here too).
func autoRegisterRepoWebhook(ctx context.Context, database *db.DB, cfg *config.Config, orgID, platform, fullName, externalID, token, baseURL string) error {
	publicURL := ""
	if cfg != nil {
		publicURL = cfg.App.PublicURL
	}
	if !publiclyReachable(publicURL) {
		slog.Info("webhook auto-register skipped: PublicURL is not publicly reachable (set PUBLIC_URL to a public https URL / tunnel)",
			"platform", platform, "full_name", fullName)
		return nil
	}

	secret, err := ensureWebhookSecret(ctx, database, orgID, platform)
	if err != nil {
		slog.Error("webhook auto-register: ensure secret", "platform", platform, "full_name", fullName, "err", err)
		return nil
	}

	target := receiverURL(publicURL, platform, orgID)
	switch platform {
	case "github":
		if err := registerGitHubWebhook(ctx, token, fullName, target, secret); err != nil {
			slog.Error("webhook auto-register: github", "full_name", fullName, "err", err)
		} else {
			slog.Info("webhook auto-registered", "platform", "github", "full_name", fullName)
		}
	case "gitlab":
		if err := registerGitLabWebhook(ctx, token, baseURL, externalID, fullName, target, secret); err != nil {
			slog.Error("webhook auto-register: gitlab", "full_name", fullName, "err", err)
		} else {
			slog.Info("webhook auto-registered", "platform", "gitlab", "full_name", fullName)
		}
	}
	return nil
}

// registerGitHubWebhook creates (or updates if drifted) the repo hook pointing at
// target. Idempotent: an existing hook with the same config.url is reused.
func registerGitHubWebhook(ctx context.Context, token, fullName, target, secret string) error {
	owner, name, ok := splitOwnerName(fullName)
	if !ok {
		return fmt.Errorf("github: bad full name %q", fullName)
	}
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	client := gogithub.NewClient(oauth2.NewClient(ctx, ts))

	desired := &gogithub.Hook{
		Active: gogithub.Bool(true),
		Events: ghWebhookEvents,
		Config: &gogithub.HookConfig{
			URL:         gogithub.String(target),
			ContentType: gogithub.String("json"),
			Secret:      gogithub.String(secret),
		},
	}

	opts := &gogithub.ListOptions{PerPage: 100}
	for {
		hooks, resp, err := client.Repositories.ListHooks(ctx, owner, name, opts)
		if err != nil {
			return fmt.Errorf("github: list hooks: %w", err)
		}
		for _, hk := range hooks {
			if hk.Config == nil || hk.Config.URL == nil {
				continue
			}
			if sameWebhookURL(*hk.Config.URL, target) {
				// Already registered — refresh events/secret in case they drifted.
				if _, _, err := client.Repositories.EditHook(ctx, owner, name, hk.GetID(), desired); err != nil {
					return fmt.Errorf("github: edit hook: %w", err)
				}
				return nil
			}
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	if _, _, err := client.Repositories.CreateHook(ctx, owner, name, desired); err != nil {
		return fmt.Errorf("github: create hook: %w", err)
	}
	return nil
}

// registerGitLabWebhook creates (or updates if drifted) the project hook pointing
// at target. Idempotent on the hook URL. externalID is the numeric project id.
func registerGitLabWebhook(ctx context.Context, token, baseURL, externalID, fullName, target, secret string) error {
	pid := projectPID(externalID, fullName)

	var opts []gogitlab.ClientOptionFunc
	if baseURL != "" {
		opts = append(opts, gogitlab.WithBaseURL(baseURL))
	}
	client, err := gogitlab.NewOAuthClient(token, opts...)
	if err != nil {
		return fmt.Errorf("gitlab: create client: %w", err)
	}

	t := true
	addOpts := &gogitlab.AddProjectHookOptions{
		URL:                 gogitlab.Ptr(target),
		Token:               gogitlab.Ptr(secret),
		PushEvents:          &t,
		MergeRequestsEvents: &t,
		IssuesEvents:        &t,
		NoteEvents:          &t,
		DeploymentEvents:    &t,
		TagPushEvents:       &t,
		PipelineEvents:      &t,
	}

	listOpts := &gogitlab.ListProjectHooksOptions{ListOptions: gogitlab.ListOptions{PerPage: 100}}
	for {
		hooks, resp, err := client.Projects.ListProjectHooks(pid, listOpts, gogitlab.WithContext(ctx))
		if err != nil {
			return fmt.Errorf("gitlab: list hooks: %w", err)
		}
		for _, hk := range hooks {
			if sameWebhookURL(hk.URL, target) {
				editOpts := &gogitlab.EditProjectHookOptions{
					URL:                 gogitlab.Ptr(target),
					Token:               gogitlab.Ptr(secret),
					PushEvents:          &t,
					MergeRequestsEvents: &t,
					IssuesEvents:        &t,
					NoteEvents:          &t,
					DeploymentEvents:    &t,
					TagPushEvents:       &t,
					PipelineEvents:      &t,
				}
				if _, _, err := client.Projects.EditProjectHook(pid, hk.ID, editOpts, gogitlab.WithContext(ctx)); err != nil {
					return fmt.Errorf("gitlab: edit hook: %w", err)
				}
				return nil
			}
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		listOpts.Page = resp.NextPage
	}

	if _, _, err := client.Projects.AddProjectHook(pid, addOpts, gogitlab.WithContext(ctx)); err != nil {
		return fmt.Errorf("gitlab: add hook: %w", err)
	}
	return nil
}

// projectPID prefers the numeric GitLab project id; falls back to the
// owner/name path (which the GitLab client URL-encodes).
func projectPID(externalID, fullName string) any {
	if externalID != "" {
		if n, err := strconv.Atoi(externalID); err == nil {
			return n
		}
		return externalID
	}
	return fullName
}

// sameWebhookURL compares two payload URLs ignoring trailing-slash / case in the
// scheme+host so a drift-free re-register is detected as a duplicate. The path +
// org query must match.
func sameWebhookURL(a, b string) bool {
	na, errA := normalizeHookURL(a)
	nb, errB := normalizeHookURL(b)
	if errA != nil || errB != nil {
		return strings.EqualFold(strings.TrimRight(a, "/"), strings.TrimRight(b, "/"))
	}
	return na == nb
}

func normalizeHookURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Host)
	path := strings.TrimRight(u.Path, "/")
	q := u.Query()
	org := q.Get("org")
	return fmt.Sprintf("%s://%s%s?org=%s", scheme, host, path, org), nil
}

// splitOwnerName splits "owner/name" → (owner, name). Returns ok=false if the
// shape is wrong.
func splitOwnerName(fullName string) (string, string, bool) {
	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
