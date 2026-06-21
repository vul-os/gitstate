package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/exo/gitstate/internal/auth"
	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/store"
)

// tokenPrincipalKey is the context key for an authenticated API-token principal.
// When present, the request was authenticated by a "gsk_" API token rather than a
// human JWT session. It carries the token's scopes for RequireScope enforcement.
type tokenPrincipalKey struct{}

// TokenPrincipal is the identity attached to a request authenticated by an API
// token. The matching org + user are ALSO attached via the standard orgScopeKey /
// authClaimsKey so OrgFromContext / UserFromContext resolve uniformly for both
// JWT humans and machine tokens.
type TokenPrincipal struct {
	TokenID string
	OrgID   string
	UserID  string
	Scopes  []string
}

// TokenFromContext returns the API-token principal stored by RequireToken, or nil
// when the request was authenticated by a human JWT (or not at all).
func TokenFromContext(ctx context.Context) *TokenPrincipal {
	v, _ := ctx.Value(tokenPrincipalKey{}).(*TokenPrincipal)
	return v
}

// HasScope reports whether a token principal carries the given scope. A human JWT
// principal (token == nil) implicitly has every read scope; see RequireScope.
func (p *TokenPrincipal) HasScope(scope string) bool {
	if p == nil {
		return false
	}
	for _, s := range p.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// RequireToken authenticates a request from an "Authorization: Bearer gsk_..."
// API token. On success it resolves the token (sha256 → api_token_by_hash) and
// attaches:
//   - the standard org (orgScopeKey) so OrgFromContext works,
//   - a synthetic AuthUser (authClaimsKey) so UserFromContext works,
//   - the full TokenPrincipal (tokenPrincipalKey) carrying scopes.
//
// It updates last_used_at asynchronously (non-blocking). Returns 401 on any miss.
func RequireToken(database *db.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, ok := authenticateToken(r, database)
			if !ok {
				writeAuthError(w, "invalid or expired API token", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAuthOrToken accepts EITHER a human JWT (X-Org-ID header for org scope) or
// a machine API token. It tries the JWT path first; if there is no valid JWT it
// falls back to API-token auth. Exactly one of the two must succeed, else 401.
//
// JWT path: verifies the Bearer JWT, then (like OrgScope) reads X-Org-ID and
// verifies membership, attaching org + user to the context. Token path: as
// RequireToken (org comes from the token itself; X-Org-ID is ignored).
func RequireAuthOrToken(cfg *config.Config, database *db.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			bearer, _ := strings.CutPrefix(header, "Bearer ")
			bearer = strings.TrimSpace(bearer)

			// API tokens are unambiguous by their "gsk_" prefix — route them straight
			// to token auth. Everything else is treated as a candidate JWT.
			if strings.HasPrefix(bearer, "gsk_") {
				ctx, ok := authenticateToken(r, database)
				if !ok {
					writeAuthError(w, "invalid or expired API token", http.StatusUnauthorized)
					return
				}
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// JWT path.
			if bearer != "" {
				if claims, err := auth.ParseAccessToken(cfg.Auth.JWTSigningKey, bearer); err == nil {
					ctx, status, msg := authenticateJWTOrg(r, database, claims)
					if status != 0 {
						writeAuthError(w, msg, status)
						return
					}
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}

			writeAuthError(w, "missing or invalid credentials", http.StatusUnauthorized)
		})
	}
}

// RequireScope guards a handler so that a TOKEN principal must carry the named
// scope. Human JWT principals (no token in context) implicitly hold every read
// scope and are always admitted. Must run AFTER RequireToken / RequireAuthOrToken.
func RequireScope(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := TokenFromContext(r.Context())
			if p == nil {
				// Authenticated as a human; implicit read access.
				next.ServeHTTP(w, r)
				return
			}
			if !p.HasScope(scope) {
				writeAuthError(w, "token is missing required scope: "+scope, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// authenticateToken resolves a "gsk_" Bearer token and returns an enriched context
// on success. The bool is false on any failure (missing header, wrong prefix,
// unknown/expired/revoked token).
func authenticateToken(r *http.Request, database *db.DB) (context.Context, bool) {
	header := r.Header.Get("Authorization")
	raw, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		return r.Context(), false
	}
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "gsk_") {
		return r.Context(), false
	}

	hash := store.HashAPIToken(raw)
	princ, err := store.TokenByHash(r.Context(), database.Pool(), hash)
	if err != nil {
		return r.Context(), false
	}

	tp := &TokenPrincipal{
		TokenID: princ.TokenID,
		OrgID:   princ.OrgID,
		UserID:  princ.UserID,
		Scopes:  princ.Scopes,
	}

	// Update last_used_at without blocking the request. Use a detached context so
	// it survives the handler returning.
	go func(orgID, tokenID string) {
		if err := store.TouchTokenUsed(context.Background(), database.Pool(), orgID, tokenID); err != nil {
			slog.Debug("api token touch failed", "token_id", tokenID, "error", err)
		}
	}(princ.OrgID, princ.TokenID)

	ctx := r.Context()
	ctx = context.WithValue(ctx, tokenPrincipalKey{}, tp)
	ctx = context.WithValue(ctx, orgScopeKey{}, princ.OrgID)
	ctx = context.WithValue(ctx, authClaimsKey{}, &AuthUser{ID: princ.UserID})
	return ctx, true
}

// authenticateJWTOrg mirrors RequireAuth+OrgScope for the JWT branch of
// RequireAuthOrToken: it attaches the user, reads X-Org-ID, verifies membership,
// and attaches the org. On success it returns (ctx, 0, ""); on failure it returns
// the HTTP status + message to emit.
func authenticateJWTOrg(r *http.Request, database *db.DB, claims *auth.Claims) (context.Context, int, string) {
	user := &AuthUser{ID: claims.UserID(), Email: claims.Email, Name: claims.Name}

	orgID := r.Header.Get("X-Org-ID")
	if orgID == "" {
		return r.Context(), http.StatusBadRequest, "X-Org-ID header is required"
	}
	if _, err := store.GetMemberRole(r.Context(), database.Pool(), orgID, user.ID); err != nil {
		if err == store.ErrNotFound {
			return r.Context(), http.StatusForbidden, "not a member of this organization"
		}
		return r.Context(), http.StatusInternalServerError, "internal error"
	}

	ctx := r.Context()
	ctx = context.WithValue(ctx, authClaimsKey{}, user)
	ctx = context.WithValue(ctx, orgScopeKey{}, orgID)
	return ctx, 0, ""
}
