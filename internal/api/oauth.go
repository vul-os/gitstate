package api

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	oauthpkg "github.com/exo/gitstate/internal/oauth"
	"github.com/exo/gitstate/internal/auth"
	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/store"
)

// RegisterOAuthRoutes wires the /auth/oauth/* endpoints onto mux.
// Called by the orchestrator from router.go — this package does NOT edit router.go.
func RegisterOAuthRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	providers := oauthpkg.Load(cfg, cfg.App.PublicURL)
	h := &oauthHandlers{db: database, cfg: cfg, providers: providers}

	mux.HandleFunc("GET /auth/oauth/{provider}/start", h.start)
	mux.HandleFunc("GET /auth/oauth/{provider}/callback", h.callback)
}

// oauthHandlers holds dependencies for the OAuth HTTP handlers.
type oauthHandlers struct {
	db        *db.DB
	cfg       *config.Config
	providers oauthpkg.Providers
}

// stateCookieName is the httpOnly cookie used to persist the CSRF state value
// across the provider redirect and the callback.
const stateCookieName = "gs_oauth_state"

// GET /auth/oauth/{provider}/start
// Returns 404 when the provider is not enabled; otherwise redirects to the
// provider consent page and sets a short-lived CSRF-state cookie.
func (h *oauthHandlers) start(w http.ResponseWriter, r *http.Request) {
	providerName := r.PathValue("provider")
	p, ok := h.providers[strings.ToLower(providerName)]
	if !ok {
		http.Error(w, "oauth provider not enabled", http.StatusNotFound)
		return
	}

	state, err := oauthpkg.GenerateState()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Store state in a short-lived, httpOnly, SameSite=Lax cookie so the
	// callback handler can verify it without a server-side session store.
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    state,
		Path:     "/",
		MaxAge:   600, // 10 min — plenty for the OAuth round-trip
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.URL.Scheme == "https",
	})

	http.Redirect(w, r, p.AuthCodeURL(state), http.StatusFound)
}

// GET /auth/oauth/{provider}/callback
// Verifies CSRF state, exchanges the code, finds-or-creates the user, issues
// gitstate access + refresh tokens, and redirects to the frontend login fragment.
// Redirect: ${cfg.App.PublicURL}/login#access=<tok>&refresh=<tok>
// On signup-via-oauth, a personal org is created matching the password-signup path.
func (h *oauthHandlers) callback(w http.ResponseWriter, r *http.Request) {
	providerName := r.PathValue("provider")
	p, ok := h.providers[strings.ToLower(providerName)]
	if !ok {
		http.Error(w, "oauth provider not enabled", http.StatusNotFound)
		return
	}

	// ── CSRF check ──────────────────────────────────────────────────────────
	cookie, err := r.Cookie(stateCookieName)
	if err != nil || cookie.Value == "" {
		http.Error(w, "missing state cookie", http.StatusBadRequest)
		return
	}
	if r.URL.Query().Get("state") != cookie.Value {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	// Clear the state cookie.
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	// Provider may send an error param (e.g. user denied access).
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		h.redirectError(w, r, errParam)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	// ── Token exchange + userinfo ────────────────────────────────────────────
	_, info, err := p.Exchange(r.Context(), code)
	if err != nil {
		log.Printf("oauth %s: exchange error: %v", providerName, err)
		h.redirectError(w, r, "exchange_failed")
		return
	}

	// ── Find or create user ──────────────────────────────────────────────────
	pool := h.db.Pool()
	u, isNew, err := store.FindOrCreateOAuthUser(
		r.Context(), pool,
		p.Name, info.Sub, info.Email, info.Name, info.AvatarURL,
	)
	if err != nil {
		log.Printf("oauth %s: find-or-create user: %v", providerName, err)
		h.redirectError(w, r, "user_error")
		return
	}

	// ── Personal org for brand-new users (mirrors password signup) ───────────
	if isNew {
		if orgErr := h.createPersonalOrgOAuth(r, u); orgErr != nil {
			// Non-fatal: user row exists; log but don't fail the login.
			log.Printf("oauth %s: create personal org: %v", providerName, orgErr)
		}
	}

	// ── Issue gitstate tokens ────────────────────────────────────────────────
	accessToken, err := auth.IssueAccessToken(
		h.cfg.Auth.JWTSigningKey, u.ID, u.Email, u.Name,
		h.cfg.Auth.AccessTokenTTL,
	)
	if err != nil {
		log.Printf("oauth %s: issue access token: %v", providerName, err)
		h.redirectError(w, r, "token_error")
		return
	}

	rawRefresh, hashRefresh, err := auth.GenerateRefreshToken()
	if err != nil {
		log.Printf("oauth %s: generate refresh token: %v", providerName, err)
		h.redirectError(w, r, "token_error")
		return
	}

	familyID, err := newUUID(r.Context(), pool)
	if err != nil {
		log.Printf("oauth %s: generate family id: %v", providerName, err)
		h.redirectError(w, r, "token_error")
		return
	}

	expiresAt := time.Now().UTC().Add(h.cfg.Auth.RefreshTokenTTL)
	if _, err = store.InsertRefresh(r.Context(), pool, u.ID, familyID, hashRefresh, expiresAt); err != nil {
		log.Printf("oauth %s: store refresh token: %v", providerName, err)
		h.redirectError(w, r, "token_error")
		return
	}

	// ── Redirect to frontend with tokens in the URL fragment ─────────────────
	// Fragment (#) is never sent to the server and not logged by proxies.
	redirectURL := fmt.Sprintf("%s/login#access=%s&refresh=%s",
		h.cfg.App.PublicURL, accessToken, rawRefresh)
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// redirectError redirects the browser to the login page with an error fragment.
func (h *oauthHandlers) redirectError(w http.ResponseWriter, r *http.Request, reason string) {
	redirectURL := fmt.Sprintf("%s/login#error=%s", h.cfg.App.PublicURL, reason)
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// createPersonalOrgOAuth creates a personal org for a brand-new OAuth user.
// Mirrors authHandlers.createPersonalOrg in auth.go.
func (h *oauthHandlers) createPersonalOrgOAuth(r *http.Request, u *store.User) error {
	ctx := r.Context()
	pool := h.db.Pool()

	baseSlug := slugify(strings.SplitN(u.Email, "@", 2)[0])
	orgName := u.Name + "'s workspace"
	if u.Name == "" {
		orgName = strings.SplitN(u.Email, "@", 2)[0] + "'s workspace"
	}

	const insertOrg = `
		INSERT INTO organizations (slug, name)
		VALUES ($1, $2)
		ON CONFLICT (slug) DO UPDATE SET slug = EXCLUDED.slug || '-' || LEFT($3::text, 8)
		RETURNING id`

	var orgID string
	if err := pool.QueryRow(ctx, insertOrg, baseSlug, orgName, u.ID).Scan(&orgID); err != nil {
		return fmt.Errorf("create personal org: %w", err)
	}

	const insertMember = `
		INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'owner')
		ON CONFLICT DO NOTHING`
	if _, err := pool.Exec(ctx, insertMember, orgID, u.ID); err != nil {
		return fmt.Errorf("add org owner: %w", err)
	}

	return nil
}
