// Package metrics computes derived metrics from synced git/PR data and persists
// them to the metrics tables (cycle_times, involvement, effort_estimates).
//
// Decisions enforced here:
//   - P2: Involvement is TEXTURE across multiple dimensions — features shipped,
//     reviews done (the invisible senior work), areas owned, active flag, plus
//     extensible dimensions jsonb. NO composite score is ever computed or returned.
//   - P3: Effort estimates are backed by the git diff (LLM reads the actual diff).
//   - A2/S1: All writes run inside db.WithOrg so RLS enforces the org boundary.
package metrics

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/exo/gitstate/internal/calibration"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/llm"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// Service holds dependencies for metric computation.
type Service struct {
	db  *db.DB
	llm *llm.Service
}

// New constructs a metrics Service. llmSvc may be nil-provider (LLM not configured);
// ComputeCycleTimes and ComputeInvolvement work without it; EstimateForPR requires it.
func New(database *db.DB, llmSvc *llm.Service) *Service {
	return &Service{db: database, llm: llmSvc}
}

// ── ComputeCycleTimes ────────────────────────────────────────────────────────

// ComputeCycleTimes reads all merged pull_requests for the given repo within the
// org and writes a cycle_time row for each PR that has both first_commit_at and
// merged_at set. Safe to call repeatedly (each call appends a new measurement;
// ListCycleTimes returns the latest by computed_at).
//
// lead_time_secs = first_commit_at → merged_at (the full DORA lead time).
// review_secs    = pr.created_at   → merged_at (time in review / open state).
func (s *Service) ComputeCycleTimes(ctx context.Context, orgID, repoID string) error {
	return s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		prs, err := store.ListPRsTx(ctx, tx, repoID)
		if err != nil {
			return fmt.Errorf("metrics.ComputeCycleTimes: list PRs: %w", err)
		}

		// Bot-authored PRs (dependabot, agents) inflate "throughput" without
		// representing human cycle time — exclude them by author_login. An
		// identity is a bot when every one of its commits is agent-authored.
		botLogins, err := agentLogins(ctx, tx, orgID)
		if err != nil {
			return fmt.Errorf("metrics.ComputeCycleTimes: agent logins: %w", err)
		}

		computed := 0
		for _, pr := range prs {
			// Only PRs that are actually merged carry meaningful cycle-time data.
			if pr.State != "merged" {
				continue
			}
			if pr.MergedAt.IsZero() {
				continue
			}
			if _, isBot := botLogins[strings.ToLower(pr.AuthorLogin)]; isBot {
				// Self-heal: drop any stale cycle_time row a bot PR may already have
				// (e.g. seeded before bot exclusion existed) so stats stay human-only.
				if err := store.DeleteCycleTimeForPR(ctx, tx, orgID, pr.ID); err != nil {
					slog.Warn("metrics: delete bot cycle_time failed",
						"org_id", orgID, "pr_id", pr.ID, "err", err)
				}
				continue
			}

			ct := store.CycleTime{
				OrgID: orgID,
			}
			prID := pr.ID
			ct.PRID = &prID

			if !pr.FirstCommitAt.IsZero() {
				secs := int64(pr.MergedAt.Sub(pr.FirstCommitAt).Seconds())
				if secs < 0 {
					secs = 0
				}
				ct.LeadTimeSecs = &secs
			}

			if !pr.CreatedAt.IsZero() {
				secs := int64(pr.MergedAt.Sub(pr.CreatedAt).Seconds())
				if secs < 0 {
					secs = 0
				}
				ct.ReviewSecs = &secs
			}

			if err := store.UpsertCycleTime(ctx, tx, ct); err != nil {
				slog.Warn("metrics: upsert cycle_time failed",
					"org_id", orgID, "pr_id", pr.ID, "err", err)
				continue
			}
			computed++
		}

		slog.Info("metrics.ComputeCycleTimes: done",
			"org_id", orgID, "repo_id", repoID, "cycle_times_written", computed)
		return nil
	})
}

// agentLogins returns the set of lower-cased author_login values that are bots
// — identities whose every commit is agent-authored (is_agent). Used to exclude
// bot PRs from human cycle-time stats. Runs inside the caller's org-scoped tx.
func agentLogins(ctx context.Context, tx pgx.Tx, orgID string) (map[string]struct{}, error) {
	const q = `
		SELECT lower(author_login)
		FROM commits
		WHERE org_id = $1 AND author_login IS NOT NULL AND author_login <> ''
		GROUP BY lower(author_login)
		HAVING bool_and(is_agent)`
	rows, err := tx.Query(ctx, q, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var login string
		if err := rows.Scan(&login); err != nil {
			return nil, err
		}
		out[login] = struct{}{}
	}
	return out, rows.Err()
}

// ── ComputeInvolvement ───────────────────────────────────────────────────────
//
// Involvement is stored as TEXTURE — independent observable dimensions (P2).
// No composite score, no ranking formula. Each dimension is a fact derived from
// git/PR history; the extensible `dimensions` jsonb can be enriched over time.
//
// Dimensions computed:
//   - features_shipped: count of merged PRs where author_login = this user in period
//   - reviews_done:     count of PRs NOT authored by this user that were merged during
//                       the period (approximation: any merged PR by others; a richer
//                       signal requires review-event data from the sync layer which
//                       stores reviewer logins in the future dimensions column)
//   - areas_owned:      count of distinct repos that had commits by this author in period
//   - active:           true if features_shipped + areas_owned > 0
//   - dimensions:       extensible jsonb with additions/deletions for extra texture

// periodEnd returns the last instant of the calendar month starting at periodStart.
func periodEnd(periodStart time.Time) time.Time {
	// Add one month, then subtract one nanosecond.
	return periodStart.AddDate(0, 1, 0).Add(-time.Nanosecond)
}

// ComputeInvolvement derives per-user involvement texture for the calendar month
// that begins on periodStart (truncated to date). It queries commits and
// pull_requests for the org across all repos and upserts one involvement row
// per active user.
//
// This function intentionally avoids any "score" or aggregate rank — it only
// persists independent observable facts (decisions P2).
func (s *Service) ComputeInvolvement(ctx context.Context, orgID string, periodStart time.Time) error {
	// Normalise to midnight UTC on the given day.
	start := time.Date(periodStart.Year(), periodStart.Month(), periodStart.Day(), 0, 0, 0, 0, time.UTC)
	end := periodEnd(start)

	// stats is keyed by the RESOLVED user_id so each row maps to a real users row
	// (the upsert's natural key includes user_id). A contributor with commits/PRs
	// but no users row is skipped from the persisted texture — there is nothing to
	// attribute the row to, and writing a null-user_id row produces an orphan the
	// reporting layer (which joins involvement→users) can never surface. This is
	// the bug that previously left recompute writing rows nobody could read.
	stats := make(map[string]*userStats)
	getStat := func(userID string) *userStats {
		us, ok := stats[userID]
		if !ok {
			us = &userStats{}
			stats[userID] = us
		}
		return us
	}

	// All reads + writes run inside ONE db.WithOrg tx so RLS (FORCE RLS on the
	// non-superuser app role) sees app.current_org — on the bare pool every query
	// below would return ZERO rows and the recompute would silently write nothing.
	if err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		// ── Dimension: features_shipped (merged PRs authored by this user) ────────
		// Resolve the PR author_login → users via the matching commit author_email
		// (PRs carry no email; commits carry both). GATE: merged only, humans and
		// agents alike (agents are flagged via is_agent, never silently dropped, so
		// the texture stays honest — the UI separates them).
		//
		// Contributor grouping: when a login maps to a contributor that is linked to
		// a user (contributors.user_id), ALL of that contributor's identities collapse
		// onto the same user — so a grouped person's involvement is unified even when
		// an identity's email differs from the user's email. Identities belonging to
		// an excluded/bot contributor are dropped (consistent with the leaderboard).
		// The direct email→users bridge is preserved as a fallback for identities not
		// yet mapped to any contributor, so existing linkage never breaks.
		const prQ = `
			WITH ident AS (
				SELECT DISTINCT lower(author_login) AS login,
				       lower(author_email::text)    AS email
				FROM commits
				WHERE org_id = $1 AND author_login IS NOT NULL AND author_login <> ''
			),
			cmap AS (
				SELECT lower(ci.value) AS value, c.user_id::text AS user_id,
				       c.excluded OR c.is_bot AS drop
				FROM contributor_identities ci
				JOIN contributors c ON c.id = ci.contributor_id
				WHERE ci.org_id = $1
			)
			SELECT COALESCE(cl.user_id, u.id::text) AS user_id, COUNT(*) AS cnt
			FROM pull_requests p
			JOIN ident i ON i.login = lower(p.author_login)
			JOIN users  u ON lower(u.email::text) = i.email
			LEFT JOIN cmap cl ON cl.value = lower(p.author_login)
			WHERE p.org_id = $1
			  AND p.state = 'merged'
			  AND p.merged_at >= $2 AND p.merged_at <= $3
			  AND p.author_login IS NOT NULL AND p.author_login <> ''
			  AND NOT COALESCE(cl.drop, false)
			  AND COALESCE(cl.user_id, u.id::text) IS NOT NULL
			GROUP BY 1`

		prRows, err := tx.Query(ctx, prQ, orgID, start, end)
		if err != nil {
			return fmt.Errorf("metrics.ComputeInvolvement: query merged PRs: %w", err)
		}
		for prRows.Next() {
			var userID string
			var cnt int
			if err := prRows.Scan(&userID, &cnt); err != nil {
				prRows.Close()
				return fmt.Errorf("metrics.ComputeInvolvement: scan merged PR row: %w", err)
			}
			getStat(userID).featuresShipped = cnt
		}
		if err := prRows.Err(); err != nil {
			prRows.Close()
			return fmt.Errorf("metrics.ComputeInvolvement: merged PR rows: %w", err)
		}
		prRows.Close()

		// ── Dimension: reviews_done (DISTINCT PRs reviewed, from pr_reviews) ──────
		// The invisible senior work (decisions P2). Count the DISTINCT PRs each
		// reviewer touched within the window, resolving reviewer_login → users via
		// the same commit-identity bridge used for PR authorship. Self-reviews
		// (reviewer == PR author) are excluded so a contributor cannot inflate this
		// by "reviewing" their own PR.
		const reviewQ = `
			WITH ident AS (
				SELECT DISTINCT lower(author_login) AS login,
				       lower(author_email::text)    AS email
				FROM commits
				WHERE org_id = $1 AND author_login IS NOT NULL AND author_login <> ''
			),
			cmap AS (
				SELECT lower(ci.value) AS value, c.user_id::text AS user_id,
				       c.excluded OR c.is_bot AS drop
				FROM contributor_identities ci
				JOIN contributors c ON c.id = ci.contributor_id
				WHERE ci.org_id = $1
			)
			SELECT COALESCE(cl.user_id, u.id::text) AS user_id, COUNT(DISTINCT r.pr_id) AS cnt
			FROM pr_reviews r
			JOIN pull_requests p ON p.id = r.pr_id
			JOIN ident i ON i.login = lower(r.reviewer_login)
			JOIN users u ON lower(u.email::text) = i.email
			LEFT JOIN cmap cl ON cl.value = lower(r.reviewer_login)
			WHERE r.org_id = $1
			  AND r.submitted_at >= $2 AND r.submitted_at <= $3
			  AND lower(r.reviewer_login) <> lower(COALESCE(p.author_login,''))
			  AND NOT COALESCE(cl.drop, false)
			  AND COALESCE(cl.user_id, u.id::text) IS NOT NULL
			GROUP BY 1`

		reviewRows, err := tx.Query(ctx, reviewQ, orgID, start, end)
		if err != nil {
			return fmt.Errorf("metrics.ComputeInvolvement: query reviews: %w", err)
		}
		for reviewRows.Next() {
			var userID string
			var cnt int
			if err := reviewRows.Scan(&userID, &cnt); err != nil {
				reviewRows.Close()
				return fmt.Errorf("metrics.ComputeInvolvement: scan review row: %w", err)
			}
			getStat(userID).reviewsDone = cnt
		}
		if err := reviewRows.Err(); err != nil {
			reviewRows.Close()
			return fmt.Errorf("metrics.ComputeInvolvement: review rows: %w", err)
		}
		reviewRows.Close()

		// ── Dimensions: areas_owned + commit volume (via commits → users) ─────────
		// areas_owned = distinct repos a user committed to in the period.
		// is_agent    = the identity's commits are entirely agent-authored.
		const commitQ = `
			WITH cmap AS (
				SELECT lower(ci.value) AS value, c.user_id::text AS user_id,
				       c.excluded OR c.is_bot AS drop
				FROM contributor_identities ci
				JOIN contributors c ON c.id = ci.contributor_id
				WHERE ci.org_id = $1
			)
			SELECT COALESCE(ce.user_id, cl.user_id, u.id::text) AS user_id,
			       COUNT(DISTINCT c.repo_id)              AS areas,
			       COUNT(*)                               AS commits,
			       COALESCE(SUM(c.additions),0)           AS adds,
			       COALESCE(SUM(c.deletions),0)           AS dels,
			       bool_and(c.is_agent)                   AS all_agent
			FROM commits c
			JOIN users u ON lower(u.email::text) = lower(c.author_email::text)
			LEFT JOIN cmap ce ON ce.value = lower(c.author_email::text)
			LEFT JOIN cmap cl ON cl.value = lower(c.author_login)
			WHERE c.org_id = $1
			  AND c.committed_at >= $2 AND c.committed_at <= $3
			  AND NOT COALESCE(ce.drop, cl.drop, false)
			  AND COALESCE(ce.user_id, cl.user_id, u.id::text) IS NOT NULL
			GROUP BY 1`

		commitRows, err := tx.Query(ctx, commitQ, orgID, start, end)
		if err != nil {
			return fmt.Errorf("metrics.ComputeInvolvement: query commits: %w", err)
		}
		for commitRows.Next() {
			var userID string
			var areas, commits, adds, dels int
			var allAgent *bool
			if err := commitRows.Scan(&userID, &areas, &commits, &adds, &dels, &allAgent); err != nil {
				commitRows.Close()
				return fmt.Errorf("metrics.ComputeInvolvement: scan commit row: %w", err)
			}
			us := getStat(userID)
			us.areas = areas
			us.commits = commits
			us.additions = adds
			us.deletions = dels
			us.isAgent = allAgent != nil && *allAgent
		}
		if err := commitRows.Err(); err != nil {
			commitRows.Close()
			return fmt.Errorf("metrics.ComputeInvolvement: commit rows: %w", err)
		}
		commitRows.Close()

		return s.upsertInvolvement(ctx, tx, orgID, start, end, stats)
	}); err != nil {
		return err
	}
	return nil
}

// userStats accumulates per-user dimension values before upserting.
type userStats struct {
	featuresShipped int
	reviewsDone     int // DISTINCT PRs reviewed in the period (from pr_reviews)
	areas           int // distinct repos committed to (areas_owned)
	commits         int // commit volume in the period (texture only)
	additions       int
	deletions       int
	isAgent         bool // identity is entirely agent-authored
}

// upsertInvolvement writes one involvement row per RESOLVED user inside the
// caller's org-scoped tx.
//
// reviews_done is now a REAL signal: the count of DISTINCT PRs each user
// reviewed in the period, sourced from the pr_reviews table the sync layer
// populates from GitHub PR reviews / GitLab approvals+notes. Self-reviews are
// excluded upstream in the query. ReplaceUserInvolvement still carries the MAX
// of (prior, incoming) forward, so a recompute can only ever raise this value,
// never erase a previously-recorded review count.
//
// ReplaceUserInvolvement deletes any prior rows for (user, period) — across every
// project partition — and writes one org-level row, so recompute is idempotent
// and the reporting layer never double-counts a user across lineages.
func (s *Service) upsertInvolvement(ctx context.Context, tx pgx.Tx, orgID string, start, end time.Time, stats map[string]*userStats) error {
	written := 0
	for userID, us := range stats {
		active := us.featuresShipped > 0 || us.areas > 0 || us.commits > 0 || us.reviewsDone > 0

		// extensible dimensions — independent facts, no score. Keys mirror the seed
		// shape so the texture reads consistently across seeded + recomputed rows.
		dimensions := map[string]interface{}{
			"commit_count":  us.commits,
			"lines_added":   us.additions,
			"lines_deleted": us.deletions,
			"is_agent":      us.isAgent,
			"period_end":    end.Format(time.RFC3339),
		}

		uid := userID
		in := store.InvolvementUpsertInput{
			OrgID:           orgID,
			UserID:          &uid,
			ProjectID:       nil, // org-level: commits/PRs carry no project attribution
			PeriodStart:     start,
			FeaturesShipped: us.featuresShipped,
			ReviewsDone:     us.reviewsDone, // DISTINCT PRs reviewed (from pr_reviews)
			AreasOwned:      us.areas,
			Active:          active,
			Dimensions:      dimensions,
		}

		if err := store.ReplaceUserInvolvement(ctx, tx, in); err != nil {
			slog.Warn("metrics: replace involvement failed",
				"org_id", orgID, "user_id", userID, "err", err)
			continue
		}
		written++
	}

	slog.Info("metrics.ComputeInvolvement: done",
		"org_id", orgID, "period_start", start.Format("2006-01-02"),
		"users_computed", written)
	return nil
}

// ── EstimateForPR ────────────────────────────────────────────────────────────

// EstimateForPR calls the LLM to judge the semantic difficulty of a PR diff and
// persists the result via store.SaveEstimate (decisions P3). Returns the saved
// EffortEstimate. Returns llm.ErrLLMNotConfigured when the LLM is not set up.
func (s *Service) EstimateForPR(ctx context.Context, orgID, prID, diff string) (*store.EffortEstimate, error) {
	// Cold-start guard: the service may have been built with a nil llm.Service
	// (e.g. metrics.New(db, nil) in the post-sync path). Without this, the call
	// to llmSvc.EstimateDifficulty below would dereference a nil *llm.Service and
	// panic. Return the documented sentinel so callers (API handler → 503) treat
	// it as "LLM not configured" rather than crashing.
	if s.llm == nil {
		return nil, llm.ErrLLMNotConfigured
	}
	// Fetch the PR for metadata context (title, repo) inside an org-scoped tx so
	// RLS (FORCE RLS on the non-superuser role) permits the read — a bare-pool
	// GetPR returns ErrNotFound here.
	var pr *store.PullRequest
	if err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		p, e := store.GetPR(ctx, tx, prID)
		pr = p
		return e
	}); err != nil {
		return nil, fmt.Errorf("metrics.EstimateForPR: get PR: %w", err)
	}
	if pr.OrgID != orgID {
		return nil, fmt.Errorf("metrics.EstimateForPR: PR %s does not belong to org %s", prID, orgID)
	}

	// ── Derive cohort / size / change_type from the PR + its diff ───────────────
	paths := diffChangedPaths(diff)
	topDirs := calibration.TopDirsFromPaths(paths)
	stats := calibration.DiffStats{
		Additions:    pr.Additions,
		Deletions:    pr.Deletions,
		ChangedFiles: pr.ChangedFiles,
		TopDirs:      topDirs,
	}
	prMeta := calibration.PRMeta{
		RepoID: pr.RepoID,
		Title:  pr.Title,
		Paths:  paths,
	}
	changeType := calibration.ChangeType(prMeta)
	sizeBucket := calibration.SizeBucket(stats)
	cohortCandidates := calibration.CohortCandidates(prMeta, stats, changeType)
	richestCohort := cohortCandidates[0]
	area := ""
	if len(topDirs) > 0 {
		area = topDirs[0]
	}

	meta := llm.DiffMeta{
		PRID:    prID,
		PRTitle: pr.Title,
		Area:    area,
	}

	// Fetch a few exemplars (similar past merged PRs in the richest cohort, with
	// predicted + actual) to anchor the difficulty prompt. Cold-start: no rows,
	// the estimate still proceeds with an empty anchor list.
	if exs, err := s.fetchExemplars(ctx, orgID, richestCohort); err != nil {
		slog.Warn("metrics: fetch exemplars failed (continuing without anchors)",
			"org_id", orgID, "cohort", richestCohort, "err", err)
	} else {
		meta.Exemplars = exs
	}

	// Resolve the org's LLM mode: BYOK (org's own key, $0 managed cost) vs managed
	// (platform key, metered + billed as overage). Falls back to the platform
	// Service when no BYOK key is configured.
	llmSvc, managed := s.llm, true
	if s.llm != nil {
		if resolved, m, rerr := s.llm.ForOrg(ctx, s.db, orgID); rerr == nil {
			llmSvc, managed = resolved, m
		} else {
			slog.Warn("metrics: llm ForOrg resolution failed; using platform service",
				"org_id", orgID, "err", rerr)
		}
	}

	summary, err := llmSvc.EstimateDifficulty(ctx, diff, meta)
	if err != nil {
		return nil, fmt.Errorf("metrics.EstimateForPR: llm estimate: %w", err)
	}

	// Convert the model difficulty into calibrated seconds using the org's own
	// learned curve (richest cohort first, empirical-Bayes shrinkage, cold-start
	// fixed prior). This reads the DB inside its own WithOrg — never errors on
	// "no data", only on real failures.
	calRes, err := calibration.CalibratedSecs(ctx, s.db, orgID, summary.Difficulty, cohortCandidates)
	if err != nil {
		return nil, fmt.Errorf("metrics.EstimateForPR: calibrate: %w", err)
	}

	var saved *store.EffortEstimate
	if err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		in := store.SaveEstimateInput{
			OrgID:      orgID,
			PRID:       &prID,
			Difficulty: summary.Difficulty,
			Rationale:  summary.Rationale,
			Evidence:   summary.Evidence,
			Model:      summary.Model,
		}
		est, err := store.SaveEstimate(ctx, tx, in)
		if err != nil {
			return err
		}
		saved = est

		// Persist the calibration fields on the freshly-inserted row.
		if err := store.UpdateEstimateCalibration(ctx, tx, orgID, est.ID,
			calRes.PredictedSecs, calRes.CohortKey, sizeBucket, changeType); err != nil {
			return err
		}
		saved.PredictedSecs = &calRes.PredictedSecs
		saved.CohortKey = calRes.CohortKey
		saved.SizeBucket = sizeBucket
		saved.ChangeType = changeType

		// Meter managed LLM usage so it flows into billing overage (decisions P4,
		// billing.GenerateInvoice aggregates kind="llm_tokens"). BYOK records $0 —
		// the org pays its provider directly, we incur no managed cost.
		qty, costUSD := llmEstimateUsage(diff)
		if !managed {
			costUSD = 0
		}
		if err := store.RecordUsage(ctx, tx, orgID, "llm_tokens", qty, costUSD); err != nil {
			// Non-fatal: the estimate is saved; a missed usage row only under-bills.
			slog.Warn("metrics: record llm usage failed",
				"org_id", orgID, "managed", managed, "err", err)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("metrics.EstimateForPR: persist estimate: %w", err)
	}

	return saved, nil
}

// fetchExemplars loads up to 3 recent merged PRs in the given cohort that have
// both a prediction and an actual, and converts them to LLM anchors (hours).
// Returns an empty slice (not an error) on cold start.
func (s *Service) fetchExemplars(ctx context.Context, orgID, cohortKey string) ([]llm.Exemplar, error) {
	var exs []store.Exemplar
	if err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var e error
		exs, e = store.ListExemplars(ctx, tx, orgID, cohortKey, 3)
		return e
	}); err != nil {
		return nil, err
	}
	out := make([]llm.Exemplar, 0, len(exs))
	for _, ex := range exs {
		anchor := llm.Exemplar{Title: ex.PRTitle, Difficulty: ex.Difficulty}
		if ex.PredictedSec != nil {
			anchor.PredictedHours = *ex.PredictedSec / 3600
		}
		if ex.ActualSecs != nil {
			anchor.ActualHours = float64(*ex.ActualSecs) / 3600
		}
		out = append(out, anchor)
	}
	return out, nil
}

// RecomputeCalibration re-derives the org's effort-calibration curves and
// accuracy summary from its merged history. It is the closed-loop counterpart to
// EstimateForPR and is invoked from the post-sync metrics path. Delegates to the
// calibration package (which owns the WithOrg tx + the math).
func (s *Service) RecomputeCalibration(ctx context.Context, orgID string) error {
	return calibration.RecomputeCalibration(ctx, s.db, orgID, time.Now())
}

// diffFilePathRe matches the b-side path of a unified-diff file header:
//
//	+++ b/internal/api/x.go
//	+++ internal/api/x.go   (no a/ b/ prefix)
//
// The "/dev/null" sentinel (deletions) is filtered out by the caller.
var diffFilePathRe = regexp.MustCompile(`(?m)^\+\+\+ (?:b/)?(.+)$`)

// diffChangedPaths extracts the changed file paths from a unified git diff so the
// cohort area (top-level dir) can be derived. Best-effort: returns nil when the
// diff is empty or has no recognisable headers (area cohort is then skipped).
func diffChangedPaths(diff string) []string {
	if diff == "" {
		return nil
	}
	matches := diffFilePathRe.FindAllStringSubmatch(diff, -1)
	seen := map[string]struct{}{}
	var out []string
	for _, m := range matches {
		p := m[1]
		// Strip a trailing tab + timestamp that some diff tools append.
		if i := indexByte(p, '\t'); i >= 0 {
			p = p[:i]
		}
		if p == "" || p == "/dev/null" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// llmEstimateUsage produces an approximate (token-quantity, managed-cost-USD)
// pair for one difficulty estimate, derived from the diff size. Quantity is an
// estimated input-token count (~4 chars/token plus the structured output budget);
// cost uses a blended Anthropic per-token rate so managed orgs accrue overage
// proportional to real work. The figure is intentionally conservative — billing
// under-counts rather than fabricates (decisions P4).
func llmEstimateUsage(diff string) (qty, costUSD float64) {
	inputTokens := float64(len(diff))/4 + 600 // + system prompt overhead
	const outputTokenBudget = 1024            // mirrors llm.maxOutputTokens response ceiling
	totalTokens := inputTokens + outputTokenBudget
	// Blended rate ≈ $4 / 1M tokens (Sonnet-class input+output average), in USD.
	const blendedUSDPerToken = 4.0 / 1_000_000
	return totalTokens, totalTokens * blendedUSDPerToken
}
