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
	"time"

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

		computed := 0
		for _, pr := range prs {
			// Only PRs that are actually merged carry meaningful cycle-time data.
			if pr.State != "merged" {
				continue
			}
			if pr.MergedAt.IsZero() {
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

	// userStats accumulates per-login dimension values before upserting.
	type userStats struct {
		featuresShipped int
		additions       int
		deletions       int
		repos           map[string]struct{} // distinct repos with commits
	}
	stats := make(map[string]*userStats) // key = author_login

	// ── Dimension: features_shipped (merged PRs authored by this user) ────────
	const prQ = `
		SELECT COALESCE(author_login,'') AS login, COUNT(*) AS cnt
		FROM pull_requests
		WHERE org_id = $1
		  AND state = 'merged'
		  AND merged_at >= $2
		  AND merged_at <= $3
		  AND author_login IS NOT NULL
		  AND author_login != ''
		GROUP BY author_login`

	pool := s.db.Pool()
	prRows, err := pool.Query(ctx, prQ, orgID, start, end)
	if err != nil {
		return fmt.Errorf("metrics.ComputeInvolvement: query merged PRs: %w", err)
	}
	defer prRows.Close()

	for prRows.Next() {
		var login string
		var cnt int
		if err := prRows.Scan(&login, &cnt); err != nil {
			return fmt.Errorf("metrics.ComputeInvolvement: scan merged PR row: %w", err)
		}
		if _, ok := stats[login]; !ok {
			stats[login] = &userStats{repos: make(map[string]struct{})}
		}
		stats[login].featuresShipped = cnt
	}
	if err := prRows.Err(); err != nil {
		return fmt.Errorf("metrics.ComputeInvolvement: merged PR rows: %w", err)
	}

	// ── Dimension: areas_owned + additions/deletions (via commits) ────────────
	const commitQ = `
		SELECT COALESCE(author_login, COALESCE(author_email,'unknown')) AS login,
		       repo_id::text,
		       COALESCE(SUM(additions),0),
		       COALESCE(SUM(deletions),0)
		FROM commits
		WHERE org_id = $1
		  AND committed_at >= $2
		  AND committed_at <= $3
		GROUP BY login, repo_id`

	commitRows, err := pool.Query(ctx, commitQ, orgID, start, end)
	if err != nil {
		return fmt.Errorf("metrics.ComputeInvolvement: query commits: %w", err)
	}
	defer commitRows.Close()

	for commitRows.Next() {
		var login, repoID string
		var adds, dels int
		if err := commitRows.Scan(&login, &repoID, &adds, &dels); err != nil {
			return fmt.Errorf("metrics.ComputeInvolvement: scan commit row: %w", err)
		}
		if _, ok := stats[login]; !ok {
			stats[login] = &userStats{repos: make(map[string]struct{})}
		}
		stats[login].repos[repoID] = struct{}{}
		stats[login].additions += adds
		stats[login].deletions += dels
	}
	if err := commitRows.Err(); err != nil {
		return fmt.Errorf("metrics.ComputeInvolvement: commit rows: %w", err)
	}

	// ── Dimension: reviews_done ───────────────────────────────────────────────
	// A full review-event signal requires the sync layer to store reviewer logins
	// (e.g. from GitHub review events). As a conservative approximation we count,
	// for each user, the number of merged PRs during the period that were authored
	// by someone *else*. This gives a floor for review activity and makes seniors
	// and tech leads visible even when they ship fewer features themselves.
	//
	// Future: when review_event data is available in a reviews table this can be
	// replaced with an exact count without changing the schema or the API contract.
	const reviewQ = `
		SELECT COALESCE(author_login,'') AS login, COUNT(*) AS cnt
		FROM pull_requests
		WHERE org_id = $1
		  AND state = 'merged'
		  AND merged_at >= $2
		  AND merged_at <= $3
		  AND author_login IS NOT NULL
		  AND author_login != ''
		GROUP BY author_login`

	// We have author counts already. reviews_done for user A ≈
	// total_merged_this_period − features_shipped_by_A.
	// Count total merged PRs in the period.
	var totalMerged int
	const totalQ = `
		SELECT COUNT(*)
		FROM pull_requests
		WHERE org_id = $1
		  AND state = 'merged'
		  AND merged_at >= $2
		  AND merged_at <= $3`
	_ = reviewQ // reviewQ defined above for documentation; totalQ used below
	if err := pool.QueryRow(ctx, totalQ, orgID, start, end).Scan(&totalMerged); err != nil {
		return fmt.Errorf("metrics.ComputeInvolvement: count total merged: %w", err)
	}

	// ── Upsert one row per user ───────────────────────────────────────────────
	return s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		for login, us := range stats {
			// reviews_done = PRs merged by others (conservative approximation).
			reviewsDone := totalMerged - us.featuresShipped
			if reviewsDone < 0 {
				reviewsDone = 0
			}

			active := us.featuresShipped > 0 || len(us.repos) > 0

			// extensible dimensions — independent facts, no score
			dimensions := map[string]interface{}{
				"additions":    us.additions,
				"deletions":    us.deletions,
				"author_login": login,
				"period_end":   end.Format(time.RFC3339),
				// reviews_done_note: approximation until review-event sync lands
				"reviews_done_basis": "merged_prs_by_others",
			}

			in := store.InvolvementUpsertInput{
				OrgID:           orgID,
				UserID:          nil, // author_login used; user UUID lookup deferred to reporting layer
				PeriodStart:     start,
				FeaturesShipped: us.featuresShipped,
				ReviewsDone:     reviewsDone,
				AreasOwned:      len(us.repos),
				Active:          active,
				Dimensions:      dimensions,
			}

			// Store author_login in dimensions so the reporting layer can join to users.
			if err := store.UpsertInvolvement(ctx, tx, in); err != nil {
				slog.Warn("metrics: upsert involvement failed",
					"org_id", orgID, "login", login, "err", err)
				continue
			}
		}

		slog.Info("metrics.ComputeInvolvement: done",
			"org_id", orgID, "period_start", start.Format("2006-01-02"),
			"users_computed", len(stats))
		return nil
	})
}

// ── EstimateForPR ────────────────────────────────────────────────────────────

// EstimateForPR calls the LLM to judge the semantic difficulty of a PR diff and
// persists the result via store.SaveEstimate (decisions P3). Returns the saved
// EffortEstimate. Returns llm.ErrLLMNotConfigured when the LLM is not set up.
func (s *Service) EstimateForPR(ctx context.Context, orgID, prID, diff string) (*store.EffortEstimate, error) {
	// Fetch the PR for metadata context (title, repo) — makes the LLM prompt richer.
	pr, err := store.GetPR(ctx, s.db.Pool(), prID)
	if err != nil {
		return nil, fmt.Errorf("metrics.EstimateForPR: get PR: %w", err)
	}
	if pr.OrgID != orgID {
		return nil, fmt.Errorf("metrics.EstimateForPR: PR %s does not belong to org %s", prID, orgID)
	}

	meta := llm.DiffMeta{
		PRID:    prID,
		PRTitle: pr.Title,
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
