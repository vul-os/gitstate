package api

import (
	"encoding/json"
	"net/http"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/middleware"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// RegisterProjectRoutes wires /api/projects (list + create). Org-scoped via X-Org-ID.
// Minimal CRUD added at integration so the board/projects UI has a backend; richer
// project features land with metrics (Wave 4).
func RegisterProjectRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	h := &projectHandlers{db: database, cfg: cfg}
	requireAuth := middleware.RequireAuth(cfg.Auth.JWTSigningKey)
	orgScope := middleware.OrgScope(database.Pool())
	mux.Handle("GET /api/projects", requireAuth(orgScope(http.HandlerFunc(h.list))))
	mux.Handle("POST /api/projects", requireAuth(orgScope(http.HandlerFunc(h.create))))
}

type projectHandlers struct {
	db  *db.DB
	cfg *config.Config
}

type projectResponse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Key      string `json:"key"`
	Archived bool   `json:"archived"`
}

func (h *projectHandlers) list(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "X-Org-ID header required")
		return
	}
	out := []projectResponse{}
	err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		ps, err := store.ListProjects(r.Context(), tx, orgID)
		if err != nil {
			return err
		}
		for _, p := range ps {
			out = append(out, projectResponse{ID: p.ID, Name: p.Name, Key: p.Key, Archived: p.Archived})
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list projects")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *projectHandlers) create(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "X-Org-ID header required")
		return
	}
	var body struct {
		Name string `json:"name"`
		Key  string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	var resp projectResponse
	err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		p, err := store.CreateProject(r.Context(), tx, orgID, body.Name, body.Key)
		if err != nil {
			return err
		}
		resp = projectResponse{ID: p.ID, Name: p.Name, Key: p.Key, Archived: p.Archived}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create project")
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}
