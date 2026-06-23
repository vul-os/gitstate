package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
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
	mux.Handle("POST /api/repos/sync-all", auth(http.HandlerFunc(h.syncAll)))
	mux.Handle("POST /api/repos/import", auth(http.HandlerFunc(h.importRepos)))

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
		stored, storedBase, err := resolveStoredToken(r.Context(), h.db, h.cfg, orgID, req.Platform)
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

	// Auto-register the platform webhook so changes are PUSHED to us (ongoing
	// real-time sync) after this backfill. Gated on a publicly-reachable
	// PublicURL — skipped (logged, no error) on localhost. Best-effort.
	_ = autoRegisterRepoWebhook(r.Context(), h.db, h.cfg, orgID, req.Platform, repo.FullName, externalID, token, baseURL)

	// Auto-sync the freshly imported repo so its issues/PRs/commits pull right away
	// instead of waiting for a manual /sync. Best-effort, in the background.
	h.startBackgroundSync(orgID, *repo, token, baseURL)

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
		stored, storedBase, err := resolveStoredToken(r.Context(), h.db, h.cfg, orgID, repo.Platform)
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

	h.startBackgroundSync(orgID, *repo, token, baseURL)
}

// ── POST /api/repos/sync-all ──────────────────────────────────────────────────

// syncAll re-syncs every repo in the org, SEQUENTIALLY in one background goroutine
// (used after a bulk import). Sequential on purpose: firing one goroutine per repo
// would hammer the platform API and exhaust the DB pool. Responds 202 with the
// count queued; the per-repo sync is idempotent so re-syncing synced repos is safe.
func (h *syncHandlers) syncAll(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())

	var repos []store.Repo
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		rs, e := store.ListRepos(r.Context(), tx, orgID)
		repos = rs
		return e
	}); err != nil {
		writeSyncError(w, "list repos", err)
		return
	}
	if len(repos) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"status": "no repos to sync", "count": 0})
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"status": "sync queued", "count": len(repos)})

	bgCtx := context.Background()
	go func() {
		tokenCache := map[string][2]string{} // platform → {token, baseURL}, resolved once
		ok, failed := 0, 0
		for _, repo := range repos {
			tb, cached := tokenCache[repo.Platform]
			if !cached {
				tok, base, err := resolveStoredToken(bgCtx, h.db, h.cfg, orgID, repo.Platform)
				if err != nil {
					slog.Warn("sync-all: no stored token", "platform", repo.Platform, "err", err)
					tokenCache[repo.Platform] = [2]string{} // cache the miss so we don't retry per repo
					continue
				}
				tb = [2]string{tok, base}
				tokenCache[repo.Platform] = tb
			}
			if tb[0] == "" {
				continue
			}
			provider, err := gitSync.NewProvider(bgCtx, repo.Platform, tb[0], tb[1])
			if err != nil {
				slog.Error("sync-all: build provider", "repo", repo.FullName, "err", err)
				failed++
				continue
			}
			if err := gitSync.SyncRepo(bgCtx, h.db, provider, orgID, repo, tb[0]); err != nil {
				slog.Error("sync-all: sync repo", "repo", repo.FullName, "err", err)
				failed++
				continue
			}
			ok++
		}
		slog.Info("sync-all: complete", "org", orgID, "synced", ok, "failed", failed, "total", len(repos))
	}()
}

// ── POST /api/repos/import ────────────────────────────────────────────────────

// importReposRequest is the body for the bulk background import (e.g. "Import all"
// for a whole org). fullNames are "owner/repo" strings from the connect picker.
type importReposRequest struct {
	Platform  string   `json:"platform"`
	FullNames []string `json:"fullNames"`
}

// importRepos imports + syncs a batch of repos in ONE detached background goroutine,
// so a bulk "Import all" survives the browser closing or navigating away — the
// import no longer runs client-side. Responds 202 immediately; the frontend polls
// GET /api/repos to watch rows appear and sync. Sequential (one repo at a time) so
// it never hammers the platform API or exhausts the DB pool. Idempotent: re-importing
// an existing repo just re-syncs it.
func (h *syncHandlers) importRepos(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())

	var req importReposRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeSyncError(w, "invalid request body", err)
		return
	}
	req.Platform = strings.ToLower(strings.TrimSpace(req.Platform))
	if req.Platform != "github" && req.Platform != "gitlab" {
		http.Error(w, `{"error":"platform must be github or gitlab"}`, http.StatusBadRequest)
		return
	}
	if len(req.FullNames) == 0 {
		http.Error(w, `{"error":"fullNames is required"}`, http.StatusBadRequest)
		return
	}

	// Need a stored connection token (the picker only works once connected).
	token, baseURL, err := resolveStoredToken(r.Context(), h.db, h.cfg, orgID, req.Platform)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, `{"error":"connect this platform first"}`, http.StatusBadRequest)
			return
		}
		writeSyncError(w, "resolve stored token", err)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"status": "import queued",
		"count":  len(req.FullNames),
	})

	// One detached goroutine does the whole batch — survives the request/browser.
	platform, fullNames := req.Platform, req.FullNames
	bgCtx := context.Background()
	go func() {
		provider, err := gitSync.NewProvider(bgCtx, platform, token, baseURL)
		if err != nil {
			slog.Error("import: build provider", "platform", platform, "err", err)
			return
		}
		// Resolve repo metadata (externalId/cloneURL/defaultBranch) once for the batch.
		remote, err := provider.ListRepos(bgCtx)
		if err != nil {
			slog.Error("import: list repos", "platform", platform, "err", err)
			return
		}
		byName := make(map[string]gitSync.RemoteRepo, len(remote))
		for _, rr := range remote {
			byName[strings.ToLower(rr.FullName)] = rr
		}

		// PHASE 1 — connect ALL repos first (fast: just DB rows, no sync). This makes
		// every repo appear in the UI immediately instead of trickling in one-per-sync.
		var repos []store.Repo
		for _, fn := range fullNames {
			rr, ok := byName[strings.ToLower(strings.TrimSpace(fn))]
			if !ok {
				slog.Warn("import: repo not accessible to token", "full_name", fn)
				continue
			}
			var repo *store.Repo
			if e := h.db.WithOrg(bgCtx, orgID, func(tx pgx.Tx) error {
				rp, ce := store.ConnectRepo(bgCtx, tx, orgID, platform, rr.ExternalID, rr.FullName, rr.DefaultBranch, rr.CloneURL)
				repo = rp
				return ce
			}); e != nil {
				slog.Error("import: connect repo", "full_name", fn, "err", e)
				continue
			}
			repos = append(repos, *repo)
			// Real-time webhook (best-effort; skipped on localhost).
			_ = autoRegisterRepoWebhook(bgCtx, h.db, h.cfg, orgID, platform, rr.FullName, rr.ExternalID, token, baseURL)
		}
		slog.Info("import: connected, starting parallel sync", "org", orgID, "platform", platform,
			"connected", len(repos), "requested", len(fullNames))

		// PHASE 2 — sync them with a bounded worker pool (parallel, not one-at-a-time).
		// The App installation token has its own rate budget and GraphQL batches PRs,
		// so several repos at once is safe and far faster than sequential.
		syncReposConcurrently(bgCtx, h.db, provider, orgID, token, repos, importSyncWorkers)
		slog.Info("import: batch complete", "org", orgID, "platform", platform, "synced", len(repos))
	}()
}

// importSyncWorkers bounds how many repos sync concurrently during a bulk import.
const importSyncWorkers = 5

// syncReposConcurrently runs SyncRepo over repos with a fixed worker pool, so a
// large import finishes in roughly total/workers time instead of the sum of every
// repo's sync. Each SyncRepo is independent (its own temp clone), and the provider
// (go-github / gitlab client) is safe for concurrent use.
func syncReposConcurrently(ctx context.Context, database *db.DB, provider gitSync.Provider, orgID, token string, repos []store.Repo, workers int) {
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan store.Repo)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for repo := range jobs {
				if e := gitSync.SyncRepo(ctx, database, provider, orgID, repo, token); e != nil {
					slog.Error("import: sync repo", "full_name", repo.FullName, "err", e)
				}
			}
		}()
	}
	for _, repo := range repos {
		jobs <- repo
	}
	close(jobs)
	wg.Wait()
}

// startBackgroundSync runs a repo sync in a detached goroutine. Shared by the
// import path (so a freshly connected repo pulls issues/PRs/commits immediately)
// and the manual /sync trigger. Best-effort: it logs failures and never blocks
// the HTTP response.
func (h *syncHandlers) startBackgroundSync(orgID string, repo store.Repo, token, baseURL string) {
	bgCtx := context.Background()
	go func() {
		provider, err := gitSync.NewProvider(bgCtx, repo.Platform, token, baseURL)
		if err != nil {
			slog.Error("sync: build provider", "repo_id", repo.ID, "err", err)
			return
		}
		if err := gitSync.SyncRepo(bgCtx, h.db, provider, orgID, repo, token); err != nil {
			slog.Error("sync: sync repo", "repo_id", repo.ID, "err", err)
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
			if stored, storedBase, terr := resolveStoredToken(r.Context(), h.db, h.cfg, orgID, repo.Platform); terr == nil {
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
