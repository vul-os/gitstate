package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/middleware"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// RegisterSearchRoutes wires the /api/search endpoint onto mux.
// Called by the orchestrator from router.go — this package does NOT edit router.go.
//
// Wave 4 of the AI/agent flywheel: full-text + fuzzy search over issues, PRs and
// commits so an agent (human JWT OR machine API token) can find work by meaning.
// A JWT human implicitly holds read scopes; a token principal must carry
// "read:issues". The org is resolved uniformly from the context and the search
// runs via db.WithOrg so RLS scopes every hit to the org.
func RegisterSearchRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	h := &searchHandlers{db: database, cfg: cfg}
	authOrToken := middleware.RequireAuthOrToken(cfg, database)
	readIssues := middleware.RequireScope("read:issues")

	mux.Handle("GET /api/search",
		authOrToken(readIssues(http.HandlerFunc(h.search))))
}

type searchHandlers struct {
	db  *db.DB
	cfg *config.Config
}

// searchResponse is the compact, LLM-friendly payload.
type searchResponse struct {
	Query   string               `json:"query"`
	Fuzzy   bool                 `json:"fuzzy"`
	Results []store.SearchResult `json:"results"`
}

// GET /api/search?q=&type=issues,prs,commits&limit=
//
// 400 on an empty q. `type` is a comma-separated subset of issues|prs|commits
// (default all). `limit` defaults to 20, capped at 100. `fuzzy` is true when the
// results came from the typo-tolerant fallback rather than exact FTS.
func (h *searchHandlers) search(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusUnauthorized, "org context required")
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, "q is required")
		return
	}

	var types []string
	if raw := r.URL.Query().Get("type"); raw != "" {
		types = strings.Split(raw, ",")
	}

	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}

	var (
		results []store.SearchResult
		fuzzy   bool
	)
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		var e error
		results, fuzzy, e = store.Search(r.Context(), tx, orgID, query, types, limit)
		return e
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "search failed")
		return
	}

	if results == nil {
		results = []store.SearchResult{}
	}
	writeJSON(w, http.StatusOK, searchResponse{
		Query:   query,
		Fuzzy:   fuzzy,
		Results: results,
	})
}
