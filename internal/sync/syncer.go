package sync

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/embed"
	"github.com/exo/gitstate/internal/git"
	"github.com/exo/gitstate/internal/gitanalysis"
	"github.com/exo/gitstate/internal/metrics"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// issueRefRe matches issue references in PR titles/bodies:
//   - #123
//   - Closes #123 / Fixes #123 / Resolves #123 (GitHub closing keywords)
//
// Capture group 1 is the issue number string.
var issueRefRe = regexp.MustCompile(`(?i)(?:closes?|fixes?|resolves?)?\s*#(\d+)`)

// analyzeBlame clones the repo once (blobless) and does TWO things in that single
// clone: (1) ingests EVERY commit on ALL branches into the commits table — the
// PRIMARY commit source, zero API calls — and (2) runs the deep git analysis
// (commit_files / blame-survival / SZZ / test-coupling) that needs real git
// objects. The clone is deliberately minimal:
//
//   - --filter=blob:none → a BLOBLESS partial clone: it fetches commits + trees
//     for the full history but pulls file blobs lazily, on demand, only when blame
//     actually touches a file. That is far less data than a full working-tree clone.
//   - --no-single-branch → fetch ALL branch refs (so the commit walk sees every
//     branch, fixing the "default-branch only" gap the commits API had). Blobs are
//     still lazy, so the extra refs cost almost nothing.
//   - --no-tags → no tag refs.
//   - NO --depth: blame-survival needs the FULL history, so the graph stays intact.
//
// It returns true when commits were successfully walked+upserted from the clone
// (so the caller can skip the commits-API fallback entirely → a normal sync makes
// ZERO commit-API calls). The clone lands in a temp dir and is deleted on return —
// the repo is NEVER cached or persisted. Best-effort: a clone or blame failure logs
// and returns false, so it never fails the overall sync.
func analyzeBlame(ctx context.Context, database *db.DB, orgID string, repo store.Repo, token string, log *slog.Logger) (commitsIngested bool) {
	tmp, err := os.MkdirTemp("", "gitstate-sync-*")
	if err != nil {
		log.Error("sync: temp dir", "err", err)
		return false
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	cloneCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(cloneCtx, "git", "clone",
		"--filter=blob:none", "--no-tags", "--no-single-branch",
		injectCloneToken(repo.CloneURL, token), tmp)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		log.Error("sync: clone repo (blobless)", "err", err, "stderr", strings.TrimSpace(stderr.String()))
		return false
	}

	// (1) Ingest commits from the clone (ALL branches, zero API calls). INCREMENTAL:
	// since = repo.LastSyncedAt, so a re-sync only walks commits added since the last
	// run (a zero LastSyncedAt walks full history). UpsertCommit is idempotent on
	// (org_id, repo_id, sha). This is the PRIMARY commit path; the API path is only a
	// fallback when the repo has no clone URL or the clone fails (handled by caller).
	commitsIngested = ingestCommitsFromClone(ctx, database, orgID, repo, tmp, log)

	// (2) Deep analysis → commit_files / blame-survival / SZZ (Contribution dashboards).
	// AnalyzeRepo runs `git log` + `git blame`; the blobless clone fetches the blobs
	// blame touches on demand, so this works without a full checkout.
	if res, err := gitanalysis.AnalyzeRepo(ctx, tmp); err != nil {
		log.Error("sync: analyze git history", "err", err)
	} else if err := store.StoreResult(ctx, database, orgID, repo.ID, res); err != nil {
		log.Error("sync: store git analysis", "err", err)
	}
	return commitsIngested
}

// ingestCommitsFromClone walks ALL branches of the local clone and upserts each
// commit into the commits table — the PRIMARY commit source (zero API calls). The
// walk carries per-commit churn (additions/deletions) and the is_agent flag, which
// the commits API path cannot supply. Returns true on a successful walk+store so
// the caller knows the API fallback is unnecessary; a walk failure returns false.
func ingestCommitsFromClone(ctx context.Context, database *db.DB, orgID string, repo store.Repo, dir string, log *slog.Logger) bool {
	var since time.Time
	if repo.LastSyncedAt != nil {
		since = *repo.LastSyncedAt
	}
	records, err := git.WalkAllCommits(ctx, dir, since)
	if err != nil {
		log.Error("sync: walk commits from clone", "err", err)
		return false
	}
	if len(records) == 0 {
		return true // nothing new since last sync — the clone IS the source of truth
	}
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		for _, c := range records {
			if c.SHA == "" {
				continue
			}
			if err := store.UpsertCommit(ctx, tx, &store.Commit{
				OrgID:       orgID,
				RepoID:      repo.ID,
				SHA:         c.SHA,
				AuthorLogin: c.AuthorName,
				AuthorEmail: c.AuthorEmail,
				IsAgent:     c.IsAgent,
				Message:     c.Message,
				Additions:   c.Additions,
				Deletions:   c.Deletions,
				CommittedAt: c.CommittedAt,
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		log.Error("sync: store commits from clone tx", "err", err)
		return false
	}
	log.Info("sync: commits stored (clone, all branches)", "count", len(records), "incremental", !since.IsZero())
	return true
}

// injectCloneToken adds x-access-token auth to an https clone URL so private repos
// can be cloned with the org's stored token.
func injectCloneToken(url, token string) string {
	if token == "" || !strings.HasPrefix(url, "https://") {
		return url
	}
	rest := url[len("https://"):]
	if i := strings.IndexByte(rest, '/'); i >= 0 && strings.Contains(rest[:i], "@") {
		return url // already has userinfo
	}
	return "https://x-access-token:" + token + "@" + rest
}

// SyncRepo pulls all issues and pull requests from the remote platform into the
// gitstate database for the given repo, then computes derived_state from linked
// git activity (the wedge: auto-progress, decisions P1).
//
// The caller is responsible for providing the correct Provider for the repo's
// platform. SyncRepo is designed to be run in a goroutine — it is context-aware
// and logs structured errors rather than returning them from this function.
//
// Auto-progress rule (decisions P1 — derived-not-entered):
//   - issue referenced by an open PR  → derived_state = "in_progress"
//   - issue referenced by a merged PR → derived_state = "done"
//   - merged PR wins over open PR if both reference the same issue.
//
// Issue references are parsed from PR title + body using:
//   - bare "#<N>" references
//   - GitHub/GitLab closing keywords: "Closes #N", "Fixes #N", "Resolves #N"
func SyncRepo(ctx context.Context, database *db.DB, provider Provider, orgID string, repo store.Repo, cloneToken string) error {
	log := slog.With(
		"org_id", orgID,
		"repo_id", repo.ID,
		"platform", repo.Platform,
		"full_name", repo.FullName,
	)
	log.Info("sync: starting repo sync")

	// Cloning a full repo + analyzing its history can take a while on large repos,
	// so this sync gets a longer budget than the API-only steps would need.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	// ── 1. Fetch remote issues ────────────────────────────────────────────────
	remoteIssues, err := provider.ListIssues(ctx, repo.FullName)
	if err != nil {
		return fmt.Errorf("sync: list issues: %w", err)
	}
	log.Info("sync: fetched remote issues", "count", len(remoteIssues))

	// Upsert all remote issues (source='git') using pool-based upsert.
	// RLS is satisfied by setting app.current_org at session level in UpsertIssue.
	for _, ri := range remoteIssues {
		issue := store.IssueUpsert{
			OrgID:      orgID,
			RepoID:     repo.ID,
			Source:     "git",
			Platform:   repo.Platform,
			ExternalID: ri.ExternalID,
			Number:     ri.Number,
			Title:      ri.Title,
			Body:       ri.Body,
			State:      ri.State,
			Labels:     ri.Labels,
		}
		if err := store.UpsertIssue(ctx, database.Pool(), orgID, issue); err != nil {
			log.Error("sync: upsert issue", "external_id", ri.ExternalID, "err", err)
		}
	}

	// ── 2. Fetch remote PRs (+ reviews + first-commit in ONE batched GraphQL pass) ─
	// If the provider implements the optional prReviewLister capability (GitHub /
	// GitLab via GraphQL), one query per 50 PRs returns the PRs, each PR's reviews,
	// AND each PR's first-commit date — replacing the REST fan-out (1 list call + a
	// per-merged-PR first-commit call + a per-merged-PR reviews call). The provider
	// itself falls back to REST on any GraphQL error, signalling that with
	// graphQLReviews==nil so the syncer reverts to the per-PR REST review path.
	var (
		remotePRs      []RemotePR
		graphQLReviews map[int][]RemoteReview
	)
	if lister, ok := provider.(prReviewLister); ok {
		prs, reviews, usedGraphQL, lerr := lister.ListPullRequestsWithReviews(ctx, repo.FullName)
		if lerr != nil {
			return fmt.Errorf("sync: list prs (graphql): %w", lerr)
		}
		remotePRs = prs
		if usedGraphQL {
			// reviews may legitimately be empty (no PR had a review); a non-nil map
			// signals "GraphQL supplied reviews → skip the per-PR REST review calls".
			if reviews == nil {
				reviews = map[int][]RemoteReview{}
			}
			graphQLReviews = reviews
		}
	} else {
		prs, err := provider.ListPullRequests(ctx, repo.FullName)
		if err != nil {
			return fmt.Errorf("sync: list prs: %w", err)
		}
		remotePRs = prs
	}
	log.Info("sync: fetched remote prs", "count", len(remotePRs), "via_graphql", graphQLReviews != nil)

	// issueProgress maps issue number → derived state.
	// "done" takes precedence over "in_progress" (merged PR beats open PR).
	issueProgress := map[int]string{}

	// Upsert PRs inside a single db.WithOrg transaction for RLS correctness.
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		for _, rpr := range remotePRs {
			pr := remotePRtoPullRequest(orgID, repo.ID, repo.Platform, rpr)
			if err := store.UpsertPR(ctx, tx, pr); err != nil {
				// Log but don't abort the whole sync on a single PR failure.
				log.Error("sync: upsert pr", "external_id", rpr.ExternalID, "err", err)
			}

			// Parse issue references from title + body for auto-progress.
			refs := parseIssueRefs(rpr.Title + "\n" + rpr.Body)
			for _, num := range refs {
				switch rpr.State {
				case "merged":
					issueProgress[num] = "done" // merged always wins
				case "open":
					if issueProgress[num] != "done" {
						issueProgress[num] = "in_progress"
					}
				}
			}
		}
		return nil
	}); err != nil {
		log.Error("sync: upsert prs tx", "err", err)
	}

	// fetchComplete tracks whether EVERY remote FETCH (issues/PRs above, plus
	// reviews/deployments/commits below) succeeded after retries. last_synced_at
	// is advanced ONLY when this stays true — otherwise the next sync re-pulls
	// from the last good point (commits `since` stays put) so a rate-limit-
	// truncated run can never leave a permanent gap. Issues+PRs already returned
	// early on error above, so reaching here they succeeded.
	fetchComplete := true

	// ── 2.5. Store PR reviews (Involvement: reviews_done) ─────────────────────
	// When the GraphQL pass supplied reviews (graphQLReviews != nil) they are stored
	// WITHOUT any further API calls — the reviews already came WITH the PRs. Only when
	// GraphQL was unavailable/failed does syncPRReviews fall back to the REST per-PR
	// review path (and there it queries only MERGED PRs to cut the multiplier).
	// Self-reviews (reviewer == PR author) are skipped. A REST reviews FETCH error
	// after retries marks the sync incomplete (so last_synced_at is not advanced);
	// store errors stay best-effort.
	if !syncPRReviews(ctx, database, provider, orgID, repo, remotePRs, graphQLReviews, log) {
		fetchComplete = false
	}

	// ── 2.6. Fetch + store deployments (DORA: deploy frequency / CFR) ─────────
	// Idempotent on (org_id, source, external_id) via store.InsertDeployment's
	// ON CONFLICT, so re-syncs do not double-count. A deployments FETCH error
	// after retries marks the sync incomplete.
	if !syncDeployments(ctx, database, provider, orgID, repo, log) {
		fetchComplete = false
	}

	// ── 2.7. Derive incidents from synced issues (DORA: MTTR) ─────────────────
	// GitHub/GitLab have no native incidents — derive them HONESTLY from issues
	// whose labels mark them as an incident/outage/sevN. Best-effort.
	syncIncidentsFromIssues(ctx, database, orgID, repo, remoteIssues, log)

	// ── 3. Apply derived_state from linked PRs (auto-progress) ───────────────
	if len(issueProgress) > 0 {
		issues, err := store.ListIssuesByRepo(ctx, database.Pool(), orgID, repo.ID)
		if err != nil {
			log.Error("sync: list issues by repo for derived state", "err", err)
		} else {
			for _, iss := range issues {
				derived, linked := issueProgress[iss.Number]
				if !linked {
					continue
				}
				if err := store.SetDerivedState(ctx, database.Pool(), orgID, iss.ID, derived); err != nil {
					log.Error("sync: set derived state",
						"issue_id", iss.ID, "state", derived, "err", err)
				}
			}
		}
	}

	// ── 4a. Commits from the blame clone (PRIMARY, zero API calls, ALL branches) ─
	// The single blobless clone we already do for blame now ALSO ingests every
	// commit on every branch (git WalkAllCommits → UpsertCommit) — zero commit-API
	// calls and no "default-branch only" gap. analyzeBlame also runs the deep blame /
	// SZZ / coupling analysis in the same clone. Best-effort: a clone failure (no
	// token, network) must not fail the sync. Runs BEFORE ComputeCycleTimes so the
	// commits feed is current.
	commitsFromClone := false
	if repo.CloneURL == "" {
		log.Warn("sync: no clone URL — skipping blame/contribution analysis; commits fall back to API")
	} else {
		commitsFromClone = analyzeBlame(ctx, database, orgID, repo, cloneToken, log)
	}

	// ── 4b. Commit-API FALLBACK — only when the clone did not supply commits ──────
	// A normal sync makes ZERO commit-API calls (the clone is the source). This path
	// runs ONLY when there is no clone URL or the clone/walk failed, so commit data is
	// never lost. INCREMENTAL: since = repo.LastSyncedAt. UpsertCommit is idempotent.
	if !commitsFromClone {
		if !syncCommitsFromAPI(ctx, database, provider, orgID, repo, log) {
			fetchComplete = false
		}
	}

	// ── 4. Update last_synced_at on the repo — ONLY on a COMPLETE sync ─────────
	// If any remote fetch above failed after retries (e.g. a rate-limit wait was
	// cut short by the ctx budget), advancing last_synced_at would make the next
	// incremental run skip the never-fetched window → a permanent gap. So skip the
	// update and let the next sync re-pull from the last good point.
	if fetchComplete {
		if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
			return store.UpdateRepoSyncedAt(ctx, tx, orgID, repo.ID)
		}); err != nil {
			log.Error("sync: update last_synced_at", "err", err)
		}
	} else {
		log.Warn("sync: incomplete — not advancing last_synced_at; will re-fetch next run")
	}

	// ── 5. Post-sync metrics: cycle times + self-calibrating effort curves ─────
	// Fresh merged PRs change the cycle-time series and the difficulty→time
	// calibration. ComputeCycleTimes produces the lead times that
	// RecomputeCalibration then backfills into effort_estimates.actual_secs and
	// folds into the per-cohort curves. Non-fatal: a metrics failure must not fail
	// the sync. The LLM is not needed here (nil-provider service is fine).
	metricsSvc := metrics.New(database, nil)
	if err := metricsSvc.ComputeCycleTimes(ctx, orgID, repo.ID); err != nil {
		log.Error("sync: compute cycle times", "err", err)
	}
	if err := metricsSvc.RecomputeCalibration(ctx, orgID); err != nil {
		log.Error("sync: recompute calibration", "err", err)
	}

	// ── 6. Post-sync embeddings: keep the semantic (pgvector) index current ────
	// Freshly upserted/edited issues need a (re)computed embedding so semantic
	// search can find them by meaning. The local embedder is deterministic and
	// network-free; the pass is idempotent (only NULL/stale rows). Non-fatal: a
	// failure here must never fail the sync.
	if n, err := embed.EmbedPendingIssues(ctx, database, orgID); err != nil {
		log.Error("sync: embed pending issues", "err", err)
	} else if n > 0 {
		log.Info("sync: embedded pending issues", "count", n)
	}

	log.Info("sync: repo sync complete",
		"issues", len(remoteIssues),
		"prs", len(remotePRs),
		"derived_states", len(issueProgress),
	)
	return nil
}

// remotePRtoPullRequest converts a RemotePR to the store.PullRequest type.
func remotePRtoPullRequest(orgID, repoID, platform string, rpr RemotePR) *store.PullRequest {
	pr := &store.PullRequest{
		OrgID:        orgID,
		RepoID:       repoID,
		Platform:     platform,
		ExternalID:   rpr.ExternalID,
		Number:       rpr.Number,
		Title:        rpr.Title,
		AuthorLogin:  rpr.AuthorLogin,
		State:        rpr.State,
		Additions:    rpr.Additions,
		Deletions:    rpr.Deletions,
		ChangedFiles: rpr.ChangedFiles,
		CreatedAt:    rpr.CreatedAt,
	}
	if rpr.MergedAt != nil {
		pr.MergedAt = *rpr.MergedAt
	}
	if !rpr.FirstCommitAt.IsZero() {
		pr.FirstCommitAt = rpr.FirstCommitAt
	}
	return pr
}

// parseIssueRefs returns all unique issue numbers referenced in text.
// Handles both bare "#123" and closing-keyword forms like "Closes #123".
func parseIssueRefs(text string) []int {
	matches := issueRefRe.FindAllStringSubmatch(text, -1)
	seen := map[int]bool{}
	var out []int
	for _, m := range matches {
		n, err := strconv.Atoi(strings.TrimSpace(m[1]))
		if err != nil || n <= 0 {
			continue
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// syncPRReviews stores PR reviews mapped to each PR's internal id. When
// graphQLReviews is non-nil the reviews already came WITH the PRs (one batched
// GraphQL pass) and NO per-PR API call is made — it stores straight from the map
// (for every PR that has reviews, regardless of state). When graphQLReviews is nil
// (GraphQL unavailable/failed) it falls back to the REST per-PR path, querying only
// MERGED PRs ("reviews done" is the completed-work signal, and gating on merged
// cuts the request multiplier). Reviews authored by the PR author (self-reviews)
// are skipped. Returns false only if a REST review FETCH failed after retries (so
// the caller can hold last_synced_at); store failures stay best-effort.
func syncPRReviews(ctx context.Context, database *db.DB, provider Provider, orgID string, repo store.Repo, remotePRs []RemotePR, graphQLReviews map[int][]RemoteReview, log *slog.Logger) bool {
	stored := 0
	complete := true
	for _, rpr := range remotePRs {
		var reviews []RemoteReview
		if graphQLReviews != nil {
			// Reviews supplied by the GraphQL pass — no API call.
			reviews = graphQLReviews[rpr.Number]
		} else {
			// REST fallback: only merged PRs carry the completed-work review signal.
			if rpr.State != "merged" {
				continue
			}
			revs, err := provider.ListReviews(ctx, repo.FullName, rpr.Number)
			if err != nil {
				log.Error("sync: list reviews", "pr_number", rpr.Number, "err", err)
				complete = false
				continue
			}
			reviews = revs
		}
		if len(reviews) == 0 {
			continue
		}
		if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
			// Resolve this PR's internal UUID (UpsertPR keys on external_id). Read
			// inside the org-scoped tx so FORCE-RLS permits it (a bare-pool lookup
			// returns no rows here).
			var prID string
			if err := tx.QueryRow(ctx,
				`SELECT id FROM pull_requests WHERE org_id=$1 AND repo_id=$2 AND external_id=$3`,
				orgID, repo.ID, rpr.ExternalID).Scan(&prID); err != nil {
				return fmt.Errorf("resolve pr %s: %w", rpr.ExternalID, err)
			}
			for _, rv := range reviews {
				// Skip self-reviews: a reviewer who is the PR author is not doing the
				// invisible review work Involvement credits.
				if strings.EqualFold(rv.ReviewerLogin, rpr.AuthorLogin) {
					continue
				}
				if rv.ReviewerLogin == "" {
					continue
				}
				if err := store.UpsertPRReview(ctx, tx, store.PRReviewInput{
					OrgID:         orgID,
					RepoID:        repo.ID,
					PRID:          prID,
					ReviewerLogin: rv.ReviewerLogin,
					State:         rv.State,
					ExternalID:    rv.ExternalID,
					SubmittedAt:   rv.SubmittedAt,
				}); err != nil {
					log.Error("sync: upsert review", "pr_id", prID, "reviewer", rv.ReviewerLogin, "err", err)
					continue
				}
				stored++
			}
			return nil
		}); err != nil {
			log.Error("sync: store reviews tx", "pr_number", rpr.Number, "err", err)
		}
	}
	if stored > 0 {
		log.Info("sync: pr reviews stored", "count", stored)
	}
	return complete
}

// syncDeployments fetches CI/CD deployments for the repo and stores them
// idempotently (ON CONFLICT on (org_id, source, external_id)). Returns false if
// the deployments FETCH failed after retries; store failures stay best-effort.
func syncDeployments(ctx context.Context, database *db.DB, provider Provider, orgID string, repo store.Repo, log *slog.Logger) bool {
	deps, err := provider.ListDeployments(ctx, repo.FullName)
	if err != nil {
		log.Error("sync: list deployments", "err", err)
		return false
	}
	if len(deps) == 0 {
		return true
	}
	source := "manual"
	switch provider.Platform() {
	case "github":
		source = "github_actions"
	case "gitlab":
		source = "gitlab_ci"
	}
	stored := 0
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		for _, d := range deps {
			if _, err := store.InsertDeployment(ctx, tx, store.DeploymentInput{
				OrgID:       orgID,
				RepoID:      repo.ID,
				Environment: d.Environment,
				Status:      d.Status,
				SHA:         d.SHA,
				Source:      source,
				ExternalID:  d.ExternalID,
				DeployedAt:  d.DeployedAt,
			}); err != nil {
				log.Error("sync: insert deployment", "external_id", d.ExternalID, "err", err)
				continue
			}
			stored++
		}
		return nil
	}); err != nil {
		log.Error("sync: store deployments tx", "err", err)
	}
	if stored > 0 {
		log.Info("sync: deployments stored", "count", stored)
	}
	return true
}

// syncCommitsFromAPI pulls commits from the platform commits API (no clone) and
// upserts them into the commits table. The pull is INCREMENTAL: since =
// repo.LastSyncedAt, so a re-sync only fetches commits added since the last sync;
// a zero LastSyncedAt (first sync) pulls the full history. UpsertCommit is
// idempotent on (org_id, repo_id, sha). Returns false if the commits FETCH failed
// after retries (so last_synced_at is held and the same `since` window is re-
// pulled next run); store failures stay best-effort.
func syncCommitsFromAPI(ctx context.Context, database *db.DB, provider Provider, orgID string, repo store.Repo, log *slog.Logger) bool {
	var since time.Time
	if repo.LastSyncedAt != nil {
		since = *repo.LastSyncedAt
	}
	commits, err := provider.ListCommits(ctx, repo.FullName, since)
	if err != nil {
		log.Error("sync: list commits (api)", "err", err)
		return false
	}
	if len(commits) == 0 {
		return true
	}
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		for _, c := range commits {
			if err := store.UpsertCommit(ctx, tx, &store.Commit{
				OrgID:       orgID,
				RepoID:      repo.ID,
				SHA:         c.SHA,
				AuthorLogin: c.AuthorLogin,
				AuthorEmail: c.AuthorEmail,
				Message:     c.Message,
				CommittedAt: c.CommittedAt,
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		// Store failure is best-effort and does NOT hold last_synced_at — the FETCH
		// (the thing that can truncate under rate limits) succeeded.
		log.Error("sync: store commits (api) tx", "err", err)
		return true
	}
	log.Info("sync: commits stored (api)", "count", len(commits), "incremental", !since.IsZero())
	return true
}

// incidentLabelRe / incidentSeverity classify an issue's labels as an incident.
var (
	incidentLabelRe = regexp.MustCompile(`(?i)^(incident|outage|sev[-_ ]?[12]|severity[:\-_/].+)$`)
	severityLabelRe = regexp.MustCompile(`(?i)^(?:sev[-_ ]?([12])|severity[:\-_/](.+))$`)
)

// incidentFromLabels reports whether the labels mark an issue as an incident and,
// if so, the derived severity (e.g. "sev1", "sev2", or the severity:* value).
func incidentFromLabels(labels []string) (bool, string) {
	isIncident := false
	severity := ""
	for _, l := range labels {
		t := strings.TrimSpace(l)
		if !incidentLabelRe.MatchString(t) {
			continue
		}
		isIncident = true
		if m := severityLabelRe.FindStringSubmatch(t); m != nil {
			switch {
			case m[1] != "":
				severity = "sev" + m[1]
			case m[2] != "":
				severity = strings.ToLower(strings.TrimSpace(m[2]))
			}
		} else if severity == "" {
			// bare "incident"/"outage" with no severity → "major"
			severity = "major"
		}
	}
	return isIncident, severity
}

// syncIncidentsFromIssues derives incidents from synced issues whose labels mark
// them as an incident/outage/sevN. opened_at = issue created_at; resolved_at =
// the close time when the issue is closed. Best-effort and idempotent by title
// dedupe (one open incident per repo+title at a time via HasOpenIncidentForRepo
// is too coarse, so we dedupe on existing rows with the same title+opened_at).
func syncIncidentsFromIssues(ctx context.Context, database *db.DB, orgID string, repo store.Repo, issues []RemoteIssue, log *slog.Logger) {
	created := 0
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		for _, iss := range issues {
			ok, sev := incidentFromLabels(iss.Labels)
			if !ok {
				continue
			}
			opened := iss.CreatedAt
			if opened.IsZero() {
				opened = time.Now().UTC()
			}
			// Idempotency: skip if an incident with the same repo+title+opened_at
			// already exists (re-sync of the same issue must not duplicate).
			var exists bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM incidents
					WHERE org_id = $1 AND repo_id = $2 AND title = $3 AND opened_at = $4
				)`, orgID, repo.ID, iss.Title, opened.UTC()).Scan(&exists); err != nil {
				log.Error("sync: incident exists check", "issue_number", iss.Number, "err", err)
				continue
			}
			if exists {
				// Already recorded; if the issue has since closed, stamp resolved_at.
				if iss.State == "closed" && !iss.UpdatedAt.IsZero() {
					if _, err := tx.Exec(ctx, `
						UPDATE incidents SET resolved_at = $5
						WHERE org_id = $1 AND repo_id = $2 AND title = $3 AND opened_at = $4
						  AND resolved_at IS NULL`,
						orgID, repo.ID, iss.Title, opened.UTC(), iss.UpdatedAt.UTC()); err != nil {
						log.Error("sync: incident resolve", "issue_number", iss.Number, "err", err)
					}
				}
				continue
			}
			inc, err := store.InsertIncident(ctx, tx, store.IncidentInput{
				OrgID:    orgID,
				RepoID:   repo.ID,
				Title:    iss.Title,
				Severity: sev,
				OpenedAt: opened,
			})
			if err != nil {
				log.Error("sync: insert incident", "issue_number", iss.Number, "err", err)
				continue
			}
			created++
			// A closed incident-issue is a resolved incident → resolved_at = close time.
			if iss.State == "closed" && !iss.UpdatedAt.IsZero() {
				if _, err := store.ResolveIncident(ctx, tx, orgID, inc.ID, iss.UpdatedAt); err != nil {
					log.Error("sync: resolve incident", "issue_number", iss.Number, "err", err)
				}
			}
		}
		return nil
	}); err != nil {
		log.Error("sync: store incidents tx", "err", err)
	}
	if created > 0 {
		log.Info("sync: incidents derived from issues", "count", created)
	}
}

// NewProvider constructs the correct Provider for the given platform.
// baseURL is used only for GitLab self-hosted instances; pass "" for gitlab.com.
// ctx is only used for GitHub (oauth2 transport setup).
func NewProvider(ctx context.Context, platform, token, baseURL string) (Provider, error) {
	switch platform {
	case "github":
		return NewGitHubProvider(ctx, token), nil
	case "gitlab":
		return NewGitLabProvider(token, baseURL)
	default:
		return nil, fmt.Errorf("sync: unknown platform %q (supported: github, gitlab)", platform)
	}
}
