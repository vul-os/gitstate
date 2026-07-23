// Package admin provides the super-admin middleware and helpers used by the
// admin console pages.
//
// RequireSuperAdmin gates any route to users whose is_super_admin flag is set
// in the users table.  Every cross-org access additionally writes an audit_log
// row via store.WriteAudit (decisions S2).
package admin

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/middleware"
	"github.com/exo/gitstate/internal/store"
)

// RequireSuperAdmin returns a middleware that:
//  1. Confirms the authenticated user exists (RequireAuth must have run first).
//  2. Checks the user's email against cfg.Admin.SuperAdminEmails (comma-separated)
//     OR checks the is_super_admin flag in the users table.
//  3. Returns 403 if neither condition is satisfied.
//
// The returned middleware wraps any http.Handler and is the mandatory gate for
// all super-admin routes (decisions S2 — super-admin is audited, never ambient).
func RequireSuperAdmin(cfg *config.Config, database *db.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := middleware.UserFromContext(r.Context())
			if user == nil {
				writeSuperAdminError(w, "authentication required", http.StatusUnauthorized)
				return
			}

			// Fast path: email is in the super-admin allow-list from config.
			if isEmailAllowed(user.Email, cfg.Admin.SuperAdminEmails) {
				next.ServeHTTP(w, r)
				return
			}

			// DB path: check is_super_admin flag in the users table.
			if database != nil {
				u, err := store.GetUserByID(r.Context(), database.Pool(), user.ID)
				if err == nil && u.IsSuperAdmin {
					next.ServeHTTP(w, r)
					return
				}
			}

			writeSuperAdminError(w, "super-admin access required", http.StatusForbidden)
		})
	}
}

// isEmailAllowed returns true when email appears in the comma-separated list.
func isEmailAllowed(email, list string) bool {
	if list == "" || email == "" {
		return false
	}
	for _, e := range strings.Split(list, ",") {
		if strings.EqualFold(strings.TrimSpace(e), strings.TrimSpace(email)) {
			return true
		}
	}
	return false
}

func writeSuperAdminError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
