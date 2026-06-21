package api

import (
	"errors"
	"net/http"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/middleware"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// RegisterContextRoutes wires the /api/context/* endpoints onto mux.
// Called by the orchestrator from router.go — this package does NOT edit router.go.
//
// These are the LLM/agent context endpoints: a JWT human OR an API token may call
// them (RequireAuthOrToken). Token principals must additionally carry the
// "read:context" scope (RequireScope); human JWT sessions implicitly do. The org
// is resolved uniformly from the context (X-Org-ID for humans, the token's own org
// for machines) and every read runs via db.WithOrg so RLS scopes the bundle.
func RegisterContextRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	h := &contextHandlers{db: database, cfg: cfg}
	authOrToken := middleware.RequireAuthOrToken(cfg, database)
	readContext := middleware.RequireScope("read:context")

	mux.Handle("GET /api/context/issue/{id}",
		authOrToken(readContext(http.HandlerFunc(h.issueContext))))
	mux.Handle("GET /api/context/pr/{id}",
		authOrToken(readContext(http.HandlerFunc(h.prContext))))
}

type contextHandlers struct {
	db  *db.DB
	cfg *config.Config
}

// GET /api/context/issue/{id}
// Returns a token-efficient bundle an agent can start work from.
func (h *contextHandlers) issueContext(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	issueID := r.PathValue("id")
	if orgID == "" {
		writeError(w, http.StatusUnauthorized, "org context required")
		return
	}

	var bundle *store.IssueContextBundle
	var buildErr error
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		bundle, buildErr = store.BuildIssueContext(r.Context(), tx, orgID, issueID)
		return buildErr
	}); err != nil {
		if errors.Is(buildErr, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "issue not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not build issue context")
		return
	}

	writeJSON(w, http.StatusOK, bundle)
}

// GET /api/context/pr/{id}
// Returns a PR bundle: header + diff summary + cycle time + calibrated estimate.
func (h *contextHandlers) prContext(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	prID := r.PathValue("id")
	if orgID == "" {
		writeError(w, http.StatusUnauthorized, "org context required")
		return
	}

	var bundle *store.PRContextBundle
	var buildErr error
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		bundle, buildErr = store.BuildPRContext(r.Context(), tx, orgID, prID)
		return buildErr
	}); err != nil {
		if errors.Is(buildErr, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "pull request not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not build pr context")
		return
	}

	writeJSON(w, http.StatusOK, bundle)
}
