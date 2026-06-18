package sync

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// issueRefRe matches issue references in PR titles/bodies:
//   - #123
//   - Closes #123 / Fixes #123 / Resolves #123 (GitHub closing keywords)
//
// Capture group 1 is the issue number string.
var issueRefRe = regexp.MustCompile(`(?i)(?:closes?|fixes?|resolves?)?\s*#(\d+)`)

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
func SyncRepo(ctx context.Context, database *db.DB, provider Provider, orgID string, repo store.Repo) error {
	log := slog.With(
		"org_id", orgID,
		"repo_id", repo.ID,
		"platform", repo.Platform,
		"full_name", repo.FullName,
	)
	log.Info("sync: starting repo sync")

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
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

	// ── 2. Fetch remote PRs ───────────────────────────────────────────────────
	remotePRs, err := provider.ListPullRequests(ctx, repo.FullName)
	if err != nil {
		return fmt.Errorf("sync: list prs: %w", err)
	}
	log.Info("sync: fetched remote prs", "count", len(remotePRs))

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

	// ── 4. Update last_synced_at on the repo ──────────────────────────────────
	if err := store.UpdateRepoSyncedAt(ctx, database.Pool(), orgID, repo.ID); err != nil {
		log.Error("sync: update last_synced_at", "err", err)
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
