package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/middleware"
	"github.com/exo/gitstate/internal/store"
	gitSync "github.com/exo/gitstate/internal/sync"
	"github.com/jackc/pgx/v5"
)

// RegisterSyncRoutes wires the sync, repo, and issue endpoints onto mux.
// All routes require a valid JWT (RequireAuth) and an active org (OrgScope).
//
// Routes:
//
//	GET  /api/repos                — list connected repos for the active org
//	POST /api/repos                — connect a new repo (see connectRepoRequest)
//	POST /api/repos/{id}/sync      — trigger background sync → 202
//	GET  /api/issues               — list issues ?source=&state=&project=
//	POST /api/issues               — create a native (non-git) issue
//	PATCH /api/issues/{id}         — update state; writes back to platform when source='git'
func RegisterSyncRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	h := &syncHandlers{db: database, cfg: cfg}

	requireAuth := middleware.RequireAuth(cfg.Auth.JWTSigningKey)
	orgScope := middleware.OrgScope(database.Pool())
	auth := func(handler http.Handler) http.Handler {
		return requireAuth(orgScope(handler))
	}
	// Read access that also accepts a scoped API token (gsk_…) so agents/the
	// gittrack CLI can list issues; human JWT sessions still work unchanged.
	tokenOrAuth := middleware.RequireAuthOrToken(cfg, database)
	readIssues := func(handler http.Handler) http.Handler {
		return tokenOrAuth(middleware.RequireScope("read:issues")(handler))
	}
	// Write access for a scoped API token (gsk_…) so an agent (MCP update_issue_state)
	// can move an issue's state; human JWT sessions still work unchanged.
	writeIssues := func(handler http.Handler) http.Handler {
		return tokenOrAuth(middleware.RequireScope("write:issues")(handler))
	}

	mux.Handle("GET /api/repos", auth(http.HandlerFunc(h.listRepos)))
	mux.Handle("POST /api/repos", auth(http.HandlerFunc(h.connectRepo)))
	mux.Handle("POST /api/repos/{id}/sync", auth(http.HandlerFunc(h.triggerSync)))

	mux.Handle("GET /api/issues", readIssues(http.HandlerFunc(h.listIssues)))
	mux.Handle("POST /api/issues", auth(http.HandlerFunc(h.createIssue)))
	mux.Handle("PATCH /api/issues/{id}", writeIssues(http.HandlerFunc(h.patchIssue)))
}

type syncHandlers struct {
	db  *db.DB
	cfg *config.Config
}

// ── Response types ────────────────────────────────────────────────────────────

type repoResponse struct {
	ID            string     `json:"id"`
	Platform      string     `json:"platform"`
	ExternalID    string     `json:"externalId"`
	FullName      string     `json:"fullName"`
	DefaultBranch string     `json:"defaultBranch"`
	LastSyncedAt  *time.Time `json:"lastSyncedAt"`
	CreatedAt     time.Time  `json:"createdAt"`
}

func repoToResponse(r store.Repo) repoResponse {
	return repoResponse{
		ID:            r.ID,
		Platform:      r.Platform,
		ExternalID:    r.ExternalID,
		FullName:      r.FullName,
		DefaultBranch: r.DefaultBranch,
		LastSyncedAt:  r.LastSyncedAt,
		CreatedAt:     r.CreatedAt,
	}
}

type issueResponse struct {
	ID           string    `json:"id"`
	Source       string    `json:"source"`
	Platform     string    `json:"platform,omitempty"`
	ExternalID   string    `json:"externalId,omitempty"`
	Number       int       `json:"number,omitempty"`
	Title        string    `json:"title"`
	Body         string    `json:"body"`
	State        string    `json:"state"`
	DerivedState string    `json:"derivedState,omitempty"`
	ProjectID    string    `json:"projectId,omitempty"`
	RepoID       string    `json:"repoId,omitempty"`
	Labels       []string  `json:"labels"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

func issueToResponse(i store.Issue) issueResponse {
	labels := i.Labels
	if labels == nil {
		labels = []string{}
	}
	return issueResponse{
		ID:           i.ID,
		Source:       i.Source,
		Platform:     i.Platform,
		ExternalID:   i.ExternalID,
		Number:       i.Number,
		Title:        i.Title,
		Body:         i.Body,
		State:        i.State,
		DerivedState: i.DerivedState,
		ProjectID:    i.ProjectID,
		RepoID:       i.RepoID,
		Labels:       labels,
		CreatedAt:    i.CreatedAt,
		UpdatedAt:    i.UpdatedAt,
	}
}

// ── GET /api/repos ────────────────────────────────────────────────────────────

func (h *syncHandlers) listRepos(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())

	var result []store.Repo
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		repos, err := store.ListRepos(r.Context(), tx, orgID)
		if err != nil {
			return err
		}
		result = repos
		return nil
	}); err != nil {
		writeSyncError(w, "list repos", err)
		return
	}

	out := make([]repoResponse, len(result))
	for i, rp := range result {
		out[i] = repoToResponse(rp)
	}
	writeJSON(w, http.StatusOK, out)
}

// ── POST /api/repos ───────────────────────────────────────────────────────────

// connectRepoRequest is the body for POST /api/repos.
//
// Web contract (JSON):
//
//	{
//	  "platform":   "github" | "gitlab",      // required
//	  "fullName":   "owner/repo",             // required
//	  "token":      "<pat or oauth token>",   // required
//	  "baseURL":    "https://gitlab.example.com", // optional, gitlab self-hosted only
//	  "externalId": "12345"                   // optional; looked up via API if omitted
//	}
type connectRepoRequest struct {
	Platform   string `json:"platform"`
	FullName   string `json:"fullName"`
	Token      string `json:"token"`
	BaseURL    string `json:"baseURL"`
	ExternalID string `json:"externalId"`
}

func (h *syncHandlers) connectRepo(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())

	var req connectRepoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeSyncError(w, "invalid request body", err)
		return
	}
	if req.Platform == "" || req.FullName == "" {
		http.Error(w, `{"error":"platform and fullName are required"}`, http.StatusBadRequest)
		return
	}
	if req.Platform != "github" && req.Platform != "gitlab" {
		http.Error(w, `{"error":"platform must be github or gitlab"}`, http.StatusBadRequest)
		return
	}

	// Resolve the token: explicit PAT in the body wins; otherwise fall back to
	// the org's stored OAuth-app connection token (decrypt).
	token := req.Token
	baseURL := req.BaseURL
	if token == "" {
		stored, storedBase, err := resolveStoredToken(r.Context(), h.db, orgID, req.Platform)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, `{"error":"no token: connect this platform first or supply a token"}`, http.StatusBadRequest)
				return
			}
			writeSyncError(w, "resolve stored token", err)
			return
		}
		token = stored
		if baseURL == "" {
			baseURL = storedBase
		}
	}

	// Resolve externalId, defaultBranch, cloneURL from the platform if not supplied.
	externalID := req.ExternalID
	defaultBranch := ""
	cloneURL := ""

	if externalID == "" {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		provider, err := gitSync.NewProvider(ctx, req.Platform, token, baseURL)
		if err != nil {
			writeSyncError(w, "build provider", err)
			return
		}
		repos, err := provider.ListRepos(ctx)
		if err != nil {
			writeSyncError(w, "list repos from platform", err)
			return
		}
		for _, rr := range repos {
			if strings.EqualFold(rr.FullName, req.FullName) {
				externalID = rr.ExternalID
				defaultBranch = rr.DefaultBranch
				cloneURL = rr.CloneURL
				break
			}
		}
		if externalID == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "repo not found on platform (check token scope and fullName)",
			})
			return
		}
	}

	var repo *store.Repo
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		rp, e := store.ConnectRepo(
			r.Context(), tx,
			orgID, req.Platform, externalID, req.FullName, defaultBranch, cloneURL,
		)
		repo = rp
		return e
	}); err != nil {
		writeSyncError(w, "connect repo", err)
		return
	}

	writeJSON(w, http.StatusCreated, repoToResponse(*repo))
}

// ── POST /api/repos/{id}/sync ─────────────────────────────────────────────────

func (h *syncHandlers) triggerSync(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	repoID := r.PathValue("id")

	// Optional body: token and baseURL for the sync call.
	type syncBody struct {
		Token   string `json:"token"`
		BaseURL string `json:"baseURL"`
	}
	var body syncBody
	_ = json.NewDecoder(r.Body).Decode(&body) // body is optional

	// Fetch the repo record to verify ownership (org-scoped tx so RLS applies).
	var repo *store.Repo
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		rp, e := store.GetRepo(r.Context(), tx, orgID, repoID)
		repo = rp
		return e
	}); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, `{"error":"repo not found"}`, http.StatusNotFound)
			return
		}
		writeSyncError(w, "get repo", err)
		return
	}

	// Resolve the sync token: explicit body token wins; otherwise fall back to
	// the org's stored OAuth-app connection token (decrypt).
	token := body.Token
	baseURL := body.BaseURL
	if token == "" {
		stored, storedBase, err := resolveStoredToken(r.Context(), h.db, orgID, repo.Platform)
		if err != nil {
			slog.Warn("sync trigger: no token in body and no stored connection; sync may fail",
				"org_id", orgID, "repo_id", repoID, "platform", repo.Platform)
		} else {
			token = stored
			if baseURL == "" {
				baseURL = storedBase
			}
		}
	}

	// Respond 202 immediately.
	writeJSON(w, http.StatusAccepted, map[string]string{
		"status": "sync queued",
		"repoId": repoID,
	})

	// Run sync in background with a detached context.
	bgCtx := context.Background()
	go func() {
		provider, err := gitSync.NewProvider(bgCtx, repo.Platform, token, baseURL)
		if err != nil {
			slog.Error("sync: build provider", "repo_id", repoID, "err", err)
			return
		}
		if err := gitSync.SyncRepo(bgCtx, h.db, provider, orgID, *repo); err != nil {
			slog.Error("sync: sync repo", "repo_id", repoID, "err", err)
		}
	}()
}

// ── GET /api/issues ───────────────────────────────────────────────────────────

func (h *syncHandlers) listIssues(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())

	f := store.IssueFilter{
		Source:    r.URL.Query().Get("source"),
		State:     r.URL.Query().Get("state"),
		ProjectID: r.URL.Query().Get("project"),
	}

	var result []store.Issue
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		issues, err := store.ListIssues(r.Context(), tx, orgID, f)
		if err != nil {
			return err
		}
		result = issues
		return nil
	}); err != nil {
		writeSyncError(w, "list issues", err)
		return
	}

	out := make([]issueResponse, len(result))
	for i, iss := range result {
		out[i] = issueToResponse(iss)
	}
	writeJSON(w, http.StatusOK, out)
}

// ── POST /api/issues ──────────────────────────────────────────────────────────

type createIssueRequest struct {
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	ProjectID string   `json:"projectId"`
	Labels    []string `json:"labels"`
}

func (h *syncHandlers) createIssue(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())

	var req createIssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeSyncError(w, "invalid request body", err)
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		http.Error(w, `{"error":"title is required"}`, http.StatusBadRequest)
		return
	}

	input := store.NativeIssueInput{
		OrgID:     orgID,
		ProjectID: req.ProjectID,
		Title:     req.Title,
		Body:      req.Body,
		Labels:    req.Labels,
	}

	var created *store.Issue
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		iss, err := store.CreateNativeIssue(r.Context(), tx, input)
		if err != nil {
			return err
		}
		created = iss
		return nil
	}); err != nil {
		writeSyncError(w, "create issue", err)
		return
	}

	writeJSON(w, http.StatusCreated, issueToResponse(*created))
}

// ── PATCH /api/issues/{id} ────────────────────────────────────────────────────

type patchIssueRequest struct {
	State   string `json:"state"`
	Token   string `json:"token"`   // required when source='git' for write-back
	BaseURL string `json:"baseURL"` // gitlab self-hosted
}

func (h *syncHandlers) patchIssue(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	issueID := r.PathValue("id")

	var req patchIssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeSyncError(w, "invalid request body", err)
		return
	}
	if req.State == "" {
		http.Error(w, `{"error":"state is required"}`, http.StatusBadRequest)
		return
	}

	var updated *store.Issue
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		iss, err := store.GetIssue(r.Context(), tx, orgID, issueID)
		if err != nil {
			return err
		}
		if err := store.UpdateIssueState(r.Context(), tx, orgID, issueID, req.State); err != nil {
			return err
		}
		iss.State = req.State
		updated = iss
		return nil
	}); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, `{"error":"issue not found"}`, http.StatusNotFound)
			return
		}
		writeSyncError(w, "patch issue", err)
		return
	}

	// Write-back to platform for git-sourced issues. Use an explicit token if
	// supplied, else fall back to the org's stored OAuth-app connection token.
	if updated.Source == "git" && updated.Number > 0 && updated.RepoID != "" {
		var repo *store.Repo
		err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
			rp, e := store.GetRepo(r.Context(), tx, orgID, updated.RepoID)
			repo = rp
			return e
		})
		writeBackToken := req.Token
		writeBackBase := req.BaseURL
		if err == nil && writeBackToken == "" {
			if stored, storedBase, terr := resolveStoredToken(r.Context(), h.db, orgID, repo.Platform); terr == nil {
				writeBackToken = stored
				if writeBackBase == "" {
					writeBackBase = storedBase
				}
			}
		}
		if err == nil && writeBackToken != "" {
			go func() {
				bgCtx := context.Background()
				provider, err := gitSync.NewProvider(bgCtx, repo.Platform, writeBackToken, writeBackBase)
				if err != nil {
					slog.Error("sync: write-back provider", "issue_id", issueID, "err", err)
					return
				}
				// Map internal states to platform-compatible ones.
				platformState := req.State
				switch req.State {
				case "done", "in_progress":
					// Platforms only support "open"/"closed"; done → closed.
					if req.State == "done" {
						platformState = "closed"
					} else {
						platformState = "open"
					}
				}
				if err := provider.UpdateIssueState(bgCtx, repo.FullName, updated.Number, platformState); err != nil {
					slog.Error("sync: write-back issue state", "issue_id", issueID, "err", err)
				}
			}()
		}
	}

	writeJSON(w, http.StatusOK, issueToResponse(*updated))
}

// ── Sync-specific error helper ────────────────────────────────────────────────

func writeSyncError(w http.ResponseWriter, msg string, err error) {
	slog.Error("sync api error", "msg", msg, "err", err)
	writeError(w, http.StatusInternalServerError, msg)
}
