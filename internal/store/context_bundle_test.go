// Package store — context_bundle_test.go
// DB-backed test that BuildIssueContext returns a curated, capped bundle for a
// seeded issue: the issue itself (trimmed), related PRs in its repo with lead
// time, recent commits as short-sha + subject, code areas, and a similar past
// issue (shared label) with its resolving merged PR.
package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestBuildIssueContextShape(t *testing.T) {
	database := tokensTestDB(t)
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ns := time.Now().UnixNano()
	var orgID, repoID string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("ctx-%d", ns), "Ctx Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, orgID)
	})

	var issueID, similarID string
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO repos (org_id, platform, external_id, full_name) VALUES ($1,'github',$2,'acme/svc') RETURNING id`,
			orgID, fmt.Sprintf("ctx-repo-%d", ns)).Scan(&repoID); err != nil {
			return err
		}
		// Target issue.
		if err := tx.QueryRow(ctx,
			`INSERT INTO issues (org_id, repo_id, source, title, body, state, labels)
			 VALUES ($1,$2,'native',$3,$4,'open',$5) RETURNING id`,
			orgID, repoID, "Fix login redirect loop", "Long body...", []string{"bug", "auth"}).Scan(&issueID); err != nil {
			return err
		}
		// Similar past issue: shares the "auth" label, lives in same repo.
		if err := tx.QueryRow(ctx,
			`INSERT INTO issues (org_id, repo_id, source, title, state, labels)
			 VALUES ($1,$2,'native',$3,'done',$4) RETURNING id`,
			orgID, repoID, "Auth token refresh bug", []string{"auth"}).Scan(&similarID); err != nil {
			return err
		}
		// A merged PR in the repo (becomes related + resolving PR) + its cycle time.
		var prID string
		mergedAt := time.Now().UTC()
		if err := tx.QueryRow(ctx,
			`INSERT INTO pull_requests (org_id, repo_id, platform, external_id, number, title, state, merged_at, created_at)
			 VALUES ($1,$2,'github',$3,7,'Refactor auth middleware','merged',$4,$4) RETURNING id`,
			orgID, repoID, fmt.Sprintf("ctx-pr-%d", ns), mergedAt).Scan(&prID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO cycle_times (org_id, pr_id, lead_time_secs) VALUES ($1,$2,$3)`,
			orgID, prID, int64(7200)); err != nil {
			return err
		}
		// A commit in the repo.
		if _, err := tx.Exec(ctx,
			`INSERT INTO commits (org_id, repo_id, sha, author_login, message, committed_at)
			 VALUES ($1,$2,$3,'alice',E'fix: guard nil session\n\ndetails',now())`,
			orgID, repoID, fmt.Sprintf("abcdef1234567890%d", ns%1000)); err != nil {
			return err
		}
		// A task_file path (code area).
		if _, err := tx.Exec(ctx,
			`INSERT INTO task_files (org_id, repo_id, path) VALUES ($1,$2,'internal/auth/login.go')`,
			orgID, repoID); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var bundle *IssueContextBundle
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var e error
		bundle, e = BuildIssueContext(ctx, tx, orgID, issueID)
		return e
	}); err != nil {
		t.Fatalf("BuildIssueContext: %v", err)
	}

	if bundle.Issue.Title != "Fix login redirect loop" {
		t.Fatalf("issue title wrong: %q", bundle.Issue.Title)
	}
	if len(bundle.Issue.Labels) != 2 {
		t.Fatalf("want 2 labels, got %v", bundle.Issue.Labels)
	}
	if len(bundle.RelatedPRs) == 0 {
		t.Fatalf("expected at least one related PR")
	}
	if bundle.RelatedPRs[0].LeadTimeSecs == nil || *bundle.RelatedPRs[0].LeadTimeSecs != 7200 {
		t.Fatalf("expected lead time 7200, got %+v", bundle.RelatedPRs[0])
	}
	if len(bundle.RecentCommits) == 0 || len(bundle.RecentCommits[0].SHA) > 10 {
		t.Fatalf("expected short-sha commit, got %+v", bundle.RecentCommits)
	}
	if bundle.RecentCommits[0].Subject != "fix: guard nil session" {
		t.Fatalf("commit subject should be first line only, got %q", bundle.RecentCommits[0].Subject)
	}
	if len(bundle.CodeAreas) == 0 || bundle.CodeAreas[0] != "internal/auth/login.go" {
		t.Fatalf("expected code area, got %v", bundle.CodeAreas)
	}
	if len(bundle.SimilarIssues) == 0 {
		t.Fatalf("expected a similar issue sharing the auth label")
	}
	si := bundle.SimilarIssues[0]
	if si.ID != similarID {
		t.Fatalf("similar issue id mismatch: got %s want %s", si.ID, similarID)
	}
	gotShared := false
	for _, l := range si.SharedLabels {
		if l == "auth" {
			gotShared = true
		}
	}
	if !gotShared {
		t.Fatalf("similar issue should share the auth label, got %v", si.SharedLabels)
	}
	if si.ResolvedByPR == nil || !si.ResolvedByPR.Merged {
		t.Fatalf("similar issue should carry a resolving merged PR, got %+v", si.ResolvedByPR)
	}
}
