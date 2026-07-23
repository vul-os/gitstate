package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/githubapp"
	"github.com/exo/gitstate/internal/jobs"
	"github.com/exo/gitstate/internal/metrics"
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
//
// pkgJobQueue is the process-wide durable job queue, set by main.go via
// SetJobQueue BEFORE RegisterSyncRoutes runs (NewRouter's signature can't change,
// so the queue is injected out-of-band). When nil (e.g. dev-without-queue, tests
// that don't wire it) the sync handlers fall back to the legacy in-process
// goroutines so behavior degrades gracefully rather than dropping the work.
var pkgJobQueue *jobs.Queue

// SetJobQueue injects the durable job queue used by the sync/import handlers to
// enqueue sync_repo / deep_analyze jobs instead of spawning detached goroutines.
// Call this from main.go after creating + starting the queue and BEFORE serving
// (it is read when each sync route handler runs). Passing nil restores the legacy
// in-process behavior.
func SetJobQueue(q *jobs.Queue) { pkgJobQueue = q }

func RegisterSyncRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	h := &syncHandlers{db: database, cfg: cfg, queue: pkgJobQueue}

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
	mux.Handle("DELETE /api/repos/{id}", auth(http.HandlerFunc(h.deleteRepo)))
	mux.Handle("POST /api/repos/sync-all", auth(http.HandlerFunc(h.syncAll)))
	mux.Handle("POST /api/repos/import", auth(http.HandlerFunc(h.importRepos)))

	mux.Handle("GET /api/issues", readIssues(http.HandlerFunc(h.listIssues)))
	mux.Handle("POST /api/issues", auth(http.HandlerFunc(h.createIssue)))
	mux.Handle("PATCH /api/issues/{id}", writeIssues(http.HandlerFunc(h.patchIssue)))
}

type syncHandlers struct {
	db    *db.DB
	cfg   *config.Config
	queue *jobs.Queue // durable job queue; nil → legacy in-process goroutine fallback
}

// enqueueRepoSync enqueues a durable sync_repo job (which itself enqueues a
// deep_analyze on completion), coalescing duplicates via the repo dedupe key.
// Returns false when no queue is wired so callers can fall back to the legacy
// in-process path.
func (h *syncHandlers) enqueueRepoSync(ctx context.Context, orgID, repoID string) bool {
	if h.queue == nil {
		return false
	}
	if err := h.queue.Enqueue(ctx, orgID, JobSyncRepo, repoJobPayload{RepoID: repoID}, jobs.EnqueueOpts{
		DedupeKey: SyncJobDedupeKey(repoID),
	}); err != nil {
		slog.Error("enqueue sync_repo job", "org_id", orgID, "repo_id", repoID, "err", err)
		return false
	}
	return true
}

// ── Response types ────────────────────────────────────────────────────────────

type repoResponse struct {
	ID            string     `json:"id"`
	Platform      string     `json:"platform"`
	ExternalID    string     `json:"externalId"`
	FullName      string     `json:"fullName"`
	DefaultBranch string     `json:"defaultBranch"`
	LastSyncedAt  *time.Time `json:"lastSyncedAt"`
	ProjectID     *string    `json:"projectId"` // user-created project (null = unassigned)
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
		ProjectID:     r.ProjectID,
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

// ── DELETE /api/repos/{id} ──────────────────────────────────────────────────────
// Disconnect a repo and delete ALL its synced data (commits, PRs, reviews, issues,
// cycle times, deployments, …). Irreversible — the frontend confirms first.
func (h *syncHandlers) deleteRepo(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	repoID := r.PathValue("id")
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		return store.DeleteRepo(r.Context(), tx, orgID, repoID)
	}); err != nil {
		writeSyncError(w, "delete repo", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
		owner, _, _ := splitOwnerName(req.FullName)
		stored, storedBase, err := resolveStoredTokenForOwner(r.Context(), h.db, h.cfg, orgID, req.Platform, owner)
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
	// instead of waiting for a manual /sync. Prefer the DURABLE queue (survives a
	// restart); fall back to a detached goroutine only when no queue is wired.
	if !h.enqueueRepoSync(r.Context(), orgID, repo.ID) {
		h.startBackgroundSync(orgID, *repo, token, baseURL)
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
		owner, _, _ := splitOwnerName(repo.FullName)
		stored, storedBase, err := resolveStoredTokenForOwner(r.Context(), h.db, h.cfg, orgID, repo.Platform, owner)
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

	// Prefer the DURABLE queue (a restart no longer strands the sync). Fall back to
	// a detached goroutine only when no queue is wired (dev-without-queue / tests).
	if !h.enqueueRepoSync(r.Context(), orgID, repoID) {
		h.startBackgroundSync(orgID, *repo, token, baseURL)
	}
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

	// Prefer the DURABLE queue: enqueue one sync_repo job per repo (each enqueues a
	// deep_analyze on completion). Coalesced by dedupe key, so re-running sync-all
	// while jobs are still live is a no-op. Survives a restart.
	if h.queue != nil {
		queued := 0
		for _, repo := range repos {
			if h.enqueueRepoSync(r.Context(), orgID, repo.ID) {
				queued++
			}
		}
		slog.Info("sync-all: enqueued durable jobs", "org", orgID, "queued", queued, "total", len(repos))
		return
	}

	bgCtx := context.Background()
	go func() {
		// One owner-token cache per platform: for a github_app connection each repo is
		// fetched/cloned with the token of the installation that OWNS it (the App spans
		// many orgs); OAuth uses its single token. Tokens are minted at most once per
		// (platform, owner) across the whole pass.
		caches := map[string]*ownerTokenCache{}
		cacheFor := func(platform string) *ownerTokenCache {
			c, ok := caches[platform]
			if !ok {
				c = newOwnerTokenCache(h.db, h.cfg, orgID, platform)
				caches[platform] = c
			}
			return c
		}

		ok, failed := 0, 0
		var toAnalyze []store.Repo
		for _, repo := range repos {
			tok, base, terr := cacheFor(repo.Platform).tokenFor(bgCtx, repo.FullName)
			if terr != nil {
				slog.Warn("sync-all: no token for repo owner", "repo", repo.FullName, "err", terr)
				continue
			}
			provider, err := gitSync.NewProvider(bgCtx, repo.Platform, tok, base)
			if err != nil {
				slog.Error("sync-all: build provider", "repo", repo.FullName, "err", err)
				failed++
				continue
			}
			if err := gitSync.SyncRepo(bgCtx, h.db, provider, orgID, repo, tok); err != nil {
				slog.Error("sync-all: sync repo", "repo", repo.FullName, "err", err)
				failed++
				continue
			}
			ok++
			toAnalyze = append(toAnalyze, repo)
		}
		slog.Info("sync-all: fast sync complete", "org", orgID, "synced", ok, "failed", failed, "total", len(repos))

		// DEEP analysis pass over the fast-synced repos, low concurrency (split from the
		// fast sync; skips repos whose HEAD is unchanged). Group by platform; each repo
		// resolves its OWNER's token from the cache so it clones with the right credential.
		byPlatform := map[string][]store.Repo{}
		for _, repo := range toAnalyze {
			byPlatform[repo.Platform] = append(byPlatform[repo.Platform], repo)
		}
		analyzed := 0
		for platform, rs := range byPlatform {
			analyzeReposConcurrently(bgCtx, h.db, orgID, platform, cacheFor(platform), rs, deepAnalysisWorkers)
			analyzed += len(rs)
		}
		if analyzed > 0 {
			slog.Info("sync-all: deep analysis complete", "org", orgID, "analyzed", analyzed)
		}
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
		// Per-owner token cache: for a github_app connection each repo is fetched/cloned
		// with the token of the installation that OWNS it (the App spans many orgs), so
		// a cognizance repo uses cognizance's token and a nu-bi repo uses nu-bi's. For
		// OAuth the single user token serves every owner. Tokens are minted at most once
		// per owner across the whole batch.
		tokens := newOwnerTokenCache(h.db, h.cfg, orgID, platform)

		// Resolve repo metadata (externalId/cloneURL/defaultBranch) for the batch. For a
		// github_app connection ListRepos must span ALL installations (the requested repos
		// may live in different orgs); the OAuth path lists with its single token.
		byName, err := h.discoverImportRepos(bgCtx, orgID, platform, token, baseURL)
		if err != nil {
			slog.Error("import: list repos", "platform", platform, "err", err)
			return
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
			// RESUMABLE: only (re)sync repos that aren't already fully synced. A repo
			// keeps last_synced_at NULL until a COMPLETE sync, so re-clicking "Import all"
			// picks up exactly the not-yet-imported + previously-failed/incomplete repos
			// and skips the ones already done — no wasted re-syncing.
			if repo.LastSyncedAt == nil {
				repos = append(repos, *repo)
			}
			// Real-time webhook (best-effort; skipped on localhost). Use this repo
			// owner's token so the hook is created against the right installation.
			hookTok, hookBase := token, baseURL
			if t, b, e := tokens.tokenFor(bgCtx, rr.FullName); e == nil {
				hookTok, hookBase = t, b
			}
			_ = autoRegisterRepoWebhook(bgCtx, h.db, h.cfg, orgID, platform, rr.FullName, rr.ExternalID, hookTok, hookBase)
		}
		slog.Info("import: connected, enqueuing durable syncs", "org", orgID, "platform", platform,
			"to_sync", len(repos), "requested", len(fullNames))

		// PHASE 2 — enqueue one DURABLE sync_repo job per repo. Each sync_repo handler
		// enqueues a deep_analyze follow-up on completion, so the whole pipeline (fast
		// sync → deep analysis) is now restart-proof: a server restart mid-import
		// resumes the remaining jobs instead of stranding the import at e.g. 6/105.
		// Jobs coalesce on the repo dedupe key, so a re-click of "Import all" is safe.
		// Legacy in-process pools (syncReposConcurrently/analyzeReposConcurrently) are
		// the fallback used only when no queue is wired.
		if h.queue != nil {
			queued := 0
			for _, repo := range repos {
				if h.enqueueRepoSync(bgCtx, orgID, repo.ID) {
					queued++
				}
			}
			slog.Info("import: enqueued durable jobs", "org", orgID, "platform", platform, "queued", queued, "total", len(repos))
			return
		}

		// Fallback (no queue): the legacy bounded in-process pools.
		syncReposConcurrently(bgCtx, h.db, orgID, platform, baseURL, tokens, repos, importSyncWorkers)
		slog.Info("import: fast sync complete", "org", orgID, "platform", platform, "synced", len(repos))
		analyzeReposConcurrently(bgCtx, h.db, orgID, platform, tokens, repos, deepAnalysisWorkers)
		slog.Info("import: deep analysis complete", "org", orgID, "platform", platform, "analyzed", len(repos))
	}()
}

// discoverImportRepos resolves repo metadata (externalId/cloneURL/defaultBranch) for
// the import batch, keyed by lower-cased full name. For a github_app connection it
// spans EVERY installation of the App (the requested repos may live in different orgs),
// minting each installation's token and aggregating; otherwise it lists with the single
// stored token. Repos are deduped by external id.
func (h *syncHandlers) discoverImportRepos(ctx context.Context, orgID, platform, token, baseURL string) (map[string]gitSync.RemoteRepo, error) {
	byName := map[string]gitSync.RemoteRepo{}

	// github_app: aggregate repos across all installations.
	if platform == "github" && h.cfg != nil && h.cfg.Git.GitHub.AppEnabled && h.isGitHubAppConn(ctx, orgID) {
		insts, err := githubapp.ListInstallations(ctx, h.cfg.Git.GitHub.AppID, h.cfg.Git.GitHub.AppPrivateKey)
		if err != nil {
			return nil, err
		}
		for _, inst := range insts {
			tok, _, terr := githubapp.InstallationToken(ctx, h.cfg.Git.GitHub.AppID, h.cfg.Git.GitHub.AppPrivateKey, fmt.Sprint(inst.ID))
			if terr != nil {
				slog.Warn("import discover: mint installation token", "login", inst.Login, "err", terr)
				continue
			}
			provider, perr := gitSync.NewProvider(ctx, "github", tok, "")
			if perr != nil {
				slog.Warn("import discover: build provider", "login", inst.Login, "err", perr)
				continue
			}
			repos, lerr := provider.ListRepos(ctx)
			if lerr != nil {
				slog.Warn("import discover: list repos", "login", inst.Login, "err", lerr)
				continue
			}
			for _, rr := range repos {
				byName[strings.ToLower(rr.FullName)] = rr
			}
		}
		return byName, nil
	}

	provider, err := gitSync.NewProvider(ctx, platform, token, baseURL)
	if err != nil {
		return nil, err
	}
	remote, err := provider.ListRepos(ctx)
	if err != nil {
		return nil, err
	}
	for _, rr := range remote {
		byName[strings.ToLower(rr.FullName)] = rr
	}
	return byName, nil
}

// isGitHubAppConn reports whether the org's github connection is a github_app
// connection. Best-effort: a lookup failure returns false (→ OAuth path).
func (h *syncHandlers) isGitHubAppConn(ctx context.Context, orgID string) bool {
	var conn *store.PlatformConnection
	if e := h.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		c, ge := store.GetConnection(ctx, tx, orgID, "github")
		conn = c
		return ge
	}); e != nil || conn == nil {
		return false
	}
	return conn.ConnectionType == "github_app"
}

// importSyncWorkers bounds how many repos run the FAST sync concurrently during a
// bulk import. The App installation token has its own rate budget and GraphQL
// batches issues/PRs, so 8 concurrent fast syncs are safe and far faster.
const importSyncWorkers = 8

// deepAnalysisWorkers bounds how many repos run the SLOW deep analysis (clone +
// blame/SZZ) concurrently. Kept low: each is CPU+memory heavy and running many at
// once gets git OOM-killed ("signal: killed").
const deepAnalysisWorkers = 2

// syncReposConcurrently runs SyncRepo over repos with a fixed worker pool, so a
// large import finishes in roughly total/workers time instead of the sum of every
// repo's sync. Each SyncRepo is independent (its own temp clone). The token+provider
// are resolved PER REPO from the owner token cache, so a github_app import spanning
// several orgs fetches each repo with its owning installation's credential. The cache
// is concurrency-safe (mutex-guarded), minting at most one token per owner.
func syncReposConcurrently(ctx context.Context, database *db.DB, orgID, platform, baseURL string, tokens *ownerTokenCache, repos []store.Repo, workers int) {
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
				tok, base, terr := tokens.tokenFor(ctx, repo.FullName)
				if terr != nil {
					slog.Error("import: resolve owner token", "full_name", repo.FullName, "err", terr)
					continue
				}
				if base == "" {
					base = baseURL
				}
				provider, perr := gitSync.NewProvider(ctx, platform, tok, base)
				if perr != nil {
					slog.Error("import: build provider", "full_name", repo.FullName, "err", perr)
					continue
				}
				if e := gitSync.SyncRepo(ctx, database, provider, orgID, repo, tok); e != nil {
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

// analyzeReposConcurrently runs the SLOW deep analysis (gitSync.AnalyzeRepoDeep) over
// repos with a fixed, low-concurrency worker pool. It is a SEPARATE pass from the
// fast sync so contribution depth (blame-survival / SZZ) backfills behind the already-
// populated dashboards. Each AnalyzeRepoDeep is best-effort (it logs, never returns a
// fatal error) and skips repos whose HEAD is unchanged since the last deep run, so a
// re-import that touched nothing is near-instant.
func analyzeReposConcurrently(ctx context.Context, database *db.DB, orgID, platform string, tokens *ownerTokenCache, repos []store.Repo, workers int) {
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
				// Clone with the token of the installation that OWNS this repo (a
				// github_app spans many orgs); OAuth returns its single token.
				tok, _, terr := tokens.tokenFor(ctx, repo.FullName)
				if terr != nil {
					slog.Error("import: resolve owner token for analyze", "full_name", repo.FullName, "err", terr)
					continue
				}
				if e := gitSync.AnalyzeRepoDeep(ctx, database, orgID, repo, tok, slog.Default()); e != nil {
					slog.Error("import: deep analyze repo", "full_name", repo.FullName, "err", e)
				}
			}
		}()
	}
	for _, repo := range repos {
		jobs <- repo
	}
	close(jobs)
	wg.Wait()

	// Make the CURRENT month's contribution texture live right after a sync, so a
	// freshly-synced org doesn't show empty ownership/review/shipped until someone
	// opens the Contribution page (the handler still backfills the full window on
	// demand; this just keeps "now" fresh). Idempotent + best-effort; llm is nil
	// because ComputeInvolvement never uses it.
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if err := metrics.New(database, nil).ComputeInvolvement(ctx, orgID, monthStart); err != nil {
		slog.Warn("sync: compute current-month involvement failed", "org_id", orgID, "err", err)
	}
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
		// DEEP analysis runs AFTER the fast sync so the dashboards populate first;
		// Contribution depth (blame-survival / SZZ) backfills behind it. It skips when
		// HEAD is unchanged since the last deep run (near-instant re-sync). Best-effort.
		if err := gitSync.AnalyzeRepoDeep(bgCtx, h.db, orgID, repo, token, slog.Default()); err != nil {
			slog.Error("sync: deep analyze repo", "repo_id", repo.ID, "err", err)
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
			owner, _, _ := splitOwnerName(repo.FullName)
			if stored, storedBase, terr := resolveStoredTokenForOwner(r.Context(), h.db, h.cfg, orgID, repo.Platform, owner); terr == nil {
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
