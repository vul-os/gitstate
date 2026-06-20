// Package admin — auth.go
// Cookie-aware authentication for the browser-reachable super-admin console.
//
// The HTML admin pages are navigated to directly in a browser, which cannot
// send an `Authorization: Bearer` header. RequireAdminAuth therefore reads the
// access token from the `gs_admin` httpOnly cookie (falling back to a Bearer
// header for API/automation callers), verifies it, confirms super-admin via the
// shared RequireSuperAdmin gate, and redirects unauthenticated browsers to the
// branded /admin/login form instead of emitting a bare 401/403.
package admin

import (
	"net/http"
	"strings"
	"time"

	"github.com/exo/gitstate/internal/auth"
	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/middleware"
	"github.com/exo/gitstate/internal/store"
)

// adminCookieName is the httpOnly cookie that carries the super-admin access token.
const adminCookieName = "gs_admin"

// loginPath is where unauthenticated browsers are redirected.
const loginPath = "/admin/login"

// RequireAdminAuth returns a middleware that authenticates the super-admin
// console from a cookie (preferred) or a Bearer header (fallback).
//
// Flow:
//  1. Read the token from the gs_admin cookie, else from Authorization: Bearer.
//  2. Verify it with auth.ParseAccessToken using cfg.Auth.JWTSigningKey.
//  3. Inject the verified AuthUser into the request context (by delegating to
//     middleware.RequireAuth via a synthesised Bearer header so the context key
//     stays single-sourced) and confirm super-admin via RequireSuperAdmin.
//  4. On any failure, redirect the browser to /admin/login (303).
//
// Tokens are never logged.
func RequireAdminAuth(cfg *config.Config, database *db.DB) func(http.Handler) http.Handler {
	requireAuth := middleware.RequireAuth(cfg.Auth.JWTSigningKey)
	requireSuper := RequireSuperAdmin(cfg, database)
	guarded := func(next http.Handler) http.Handler { return requireAuth(requireSuper(next)) }

	return func(next http.Handler) http.Handler {
		chain := guarded(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := adminTokenFromRequest(r)
			if token == "" {
				redirectToLogin(w, r)
				return
			}

			// Verify up-front so we can redirect (not 401) on a bad/expired token.
			if _, err := auth.ParseAccessToken(cfg.Auth.JWTSigningKey, token); err != nil {
				clearAdminCookie(w, httpsDeployment(cfg))
				redirectToLogin(w, r)
				return
			}

			// Hand the verified token to the existing RequireAuth→RequireSuperAdmin
			// chain by presenting it as a Bearer header. This keeps the context key
			// and the super-admin decision single-sourced. A non-admin valid user
			// will get a 403 from RequireSuperAdmin, which we surface as a redirect.
			r.Header.Set("Authorization", "Bearer "+token)
			sw := &statusCapturingWriter{ResponseWriter: w}
			chain.ServeHTTP(sw, r)
			if (sw.status == http.StatusUnauthorized || sw.status == http.StatusForbidden) && !sw.wroteBody {
				redirectToLogin(w, r)
			}
		})
	}
}

// adminTokenFromRequest extracts the access token from the gs_admin cookie,
// falling back to an Authorization: Bearer header.
func adminTokenFromRequest(r *http.Request) string {
	if c, err := r.Cookie(adminCookieName); err == nil && c.Value != "" {
		return c.Value
	}
	if h := r.Header.Get("Authorization"); h != "" {
		if tok, ok := strings.CutPrefix(h, "Bearer "); ok {
			return tok
		}
	}
	return ""
}

// redirectToLogin sends the browser to the login page (303 See Other so that a
// failed POST is retried as a GET).
func redirectToLogin(w http.ResponseWriter, r *http.Request) {
	// Don't redirect SSE/htmx polling endpoints into an HTML page loop.
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", loginPath)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, loginPath, http.StatusSeeOther)
}

// setAdminCookie stores the access token as an httpOnly, SameSite=Lax cookie.
// secure should be true on https deployments (derived from cfg.App.PublicURL) so
// the super-admin session token never rides a plaintext request.
func setAdminCookie(w http.ResponseWriter, token string, ttl time.Duration, secure bool) {
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookieName,
		Value:    token,
		Path:     "/admin",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(ttl),
		MaxAge:   int(ttl.Seconds()),
	})
}

// clearAdminCookie expires the admin session cookie.
func clearAdminCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookieName,
		Value:    "",
		Path:     "/admin",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

// httpsDeployment reports whether the configured public URL is https (so cookies
// should be Secure). Empty/localhost dev → false.
func httpsDeployment(cfg *config.Config) bool {
	return strings.HasPrefix(strings.ToLower(cfg.App.PublicURL), "https://")
}

// statusCapturingWriter records the status code and whether a body was written
// so RequireAdminAuth can convert a downstream 401/403 into a login redirect
// without double-writing a response.
type statusCapturingWriter struct {
	http.ResponseWriter
	status    int
	wroteBody bool
}

func (w *statusCapturingWriter) WriteHeader(code int) {
	w.status = code
	if code == http.StatusUnauthorized || code == http.StatusForbidden {
		// Swallow the header write; RequireAdminAuth will issue a redirect.
		return
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusCapturingWriter) Write(b []byte) (int, error) {
	if w.status == http.StatusUnauthorized || w.status == http.StatusForbidden {
		// Swallow the JSON error body from the auth chain; we redirect instead.
		return len(b), nil
	}
	w.wroteBody = true
	return w.ResponseWriter.Write(b)
}

// Flush forwards to the underlying writer when it supports flushing (SSE).
func (w *statusCapturingWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// authenticateAdmin validates an email+password against the users table and
// confirms the account is a super-admin. On success it returns the user.
// All failures collapse to ErrAdminLogin so the form can't be used to probe
// which emails exist.
func authenticateAdmin(r *http.Request, cfg *config.Config, database *db.DB, email, password string) (*store.User, error) {
	if database == nil {
		return nil, errAdminLogin
	}
	u, err := store.GetUserByEmail(r.Context(), database.Pool(), email)
	if err != nil {
		return nil, errAdminLogin
	}
	if u.PasswordHash == "" {
		return nil, errAdminLogin
	}
	if err := auth.VerifyPassword(password, u.PasswordHash); err != nil {
		return nil, errAdminLogin
	}
	if !isAdminUser(cfg, u) {
		return nil, errAdminLogin
	}
	return u, nil
}

// isAdminUser confirms super-admin status via the config allow-list or the
// is_super_admin column (mirrors RequireSuperAdmin's decision).
func isAdminUser(cfg *config.Config, u *store.User) bool {
	if isEmailAllowed(u.Email, cfg.Admin.SuperAdminEmails) {
		return true
	}
	return u.IsSuperAdmin
}
