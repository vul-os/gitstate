package middleware

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// orgScopeKey is the context key for the active org ID attached by OrgScope.
type orgScopeKey struct{}

// OrgFromContext returns the org ID stored in ctx by OrgScope, or empty string.
func OrgFromContext(ctx context.Context) string {
	v, _ := ctx.Value(orgScopeKey{}).(string)
	return v
}

// OrgScope is a middleware that:
//  1. Reads the active org from the X-Org-ID request header.
//  2. Verifies the authenticated user (from UserFromContext) is a member of that org.
//  3. Attaches the org ID to the request context so downstream handlers can call
//     OrgFromContext(ctx) and pass it to db.WithOrg for RLS-scoped queries.
//
// Requires RequireAuth to have run first (it depends on UserFromContext).
// Returns 400 if X-Org-ID is missing, 403 if the user is not a member.
func OrgScope(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			orgID := r.Header.Get("X-Org-ID")
			if orgID == "" {
				writeOrgError(w, "X-Org-ID header is required", http.StatusBadRequest)
				return
			}

			user := UserFromContext(r.Context())
			if user == nil {
				// RequireAuth should have run first; guard anyway.
				writeOrgError(w, "authentication required", http.StatusUnauthorized)
				return
			}

			_, err := store.GetMemberRole(r.Context(), pool, orgID, user.ID)
			if err != nil {
				// ErrNotFound means not a member; any other error is a server fault.
				if err == store.ErrNotFound {
					writeOrgError(w, "not a member of this organization", http.StatusForbidden)
					return
				}
				writeOrgError(w, "internal error", http.StatusInternalServerError)
				return
			}

			ctx := context.WithValue(r.Context(), orgScopeKey{}, orgID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeOrgError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
