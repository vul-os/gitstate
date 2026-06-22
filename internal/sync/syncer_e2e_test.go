// Package sync — end-to-end SyncRepo test with a fake Provider. Exercises the
// full post-sync pipeline against a real database: issue/PR upsert (the
// transaction-local set_config RLS path), derived-state auto-progress from a
// merged PR's issue reference, last_synced_at update, the post-sync metrics
// recompute, and the embedding pass. The point is to prove the steps after the
// platform pull actually RUN and land their side effects (the class of bug
// where a sync "succeeds" but writes nothing).
package sync

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// fakeProvider returns canned remote data so SyncRepo runs without network.
type fakeProvider struct {
	issues []RemoteIssue
	prs    []RemotePR
}

func (f *fakeProvider) Platform() string { return "github" }
func (f *fakeProvider) ListRepos(context.Context) ([]RemoteRepo, error) {
	return nil, nil
}
func (f *fakeProvider) ListIssues(context.Context, string) ([]RemoteIssue, error) {
	return f.issues, nil
}
func (f *fakeProvider) ListPullRequests(context.Context, string) ([]RemotePR, error) {
	return f.prs, nil
}
func (f *fakeProvider) UpdateIssueState(context.Context, string, int, string) error {
	return nil
}

func TestSyncRepoEndToEnd(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping SyncRepo e2e test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	database, err := db.New(ctx, &config.Config{Database: config.DatabaseConfig{URL: dbURL}})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	ns := time.Now().UnixNano()
	var orgID string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("sync-e2e-%d", ns), "Sync E2E").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, orgID)
	})

	// Seed a repo to sync into.
	var repoID, fullName string
	fullName = "acme/e2e"
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`INSERT INTO repos (org_id, platform, external_id, full_name, default_branch)
			 VALUES ($1,'github',$2,$3,'main') RETURNING id`,
			orgID, fmt.Sprintf("e2e-repo-%d", ns), fullName).Scan(&repoID)
	}); err != nil {
		t.Fatalf("seed repo: %v", err)
	}

	merged := time.Now().Add(-24 * time.Hour)
	prov := &fakeProvider{
		issues: []RemoteIssue{
			{ExternalID: fmt.Sprintf("e2e-iss-%d", ns), Number: 4242, Title: "Fix the widget", Body: "broken", State: "open", Labels: []string{"bug"}},
		},
		prs: []RemotePR{
			{ExternalID: fmt.Sprintf("e2e-pr-%d", ns), Number: 9001, Title: "Fix the widget (closes #4242)", Body: "closes #4242", State: "merged", AuthorLogin: "dev", Additions: 10, Deletions: 2, ChangedFiles: 3, MergedAt: &merged, CreatedAt: merged.Add(-2 * time.Hour)},
		},
	}

	var repo *store.Repo
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		r, e := store.GetRepo(ctx, tx, orgID, repoID)
		repo = r
		return e
	}); err != nil {
		t.Fatalf("load repo: %v", err)
	}

	if err := SyncRepo(ctx, database, prov, orgID, *repo); err != nil {
		t.Fatalf("SyncRepo returned error: %v", err)
	}

	// Verify side effects landed, all reads inside the org's RLS context.
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		// 1. Issue upserted (set_config RLS path worked).
		var issState, derived string
		if err := tx.QueryRow(ctx,
			`SELECT state, COALESCE(derived_state,'') FROM issues WHERE org_id=$1 AND number=4242`, orgID).
			Scan(&issState, &derived); err != nil {
			return fmt.Errorf("issue not upserted: %w", err)
		}
		// 2. Derived-state auto-progress: merged PR referencing #4242 → "done".
		if derived != "done" {
			t.Errorf("derived_state = %q, want done (merged PR closes #4242)", derived)
		}

		// 3. PR upserted.
		var prCount int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM pull_requests WHERE org_id=$1 AND number=9001`, orgID).Scan(&prCount); err != nil {
			return err
		}
		if prCount != 1 {
			t.Errorf("PR rows = %d, want 1", prCount)
		}

		// 4. last_synced_at set on the repo.
		var synced *time.Time
		if err := tx.QueryRow(ctx,
			`SELECT last_synced_at FROM repos WHERE id=$1`, repoID).Scan(&synced); err != nil {
			return err
		}
		if synced == nil {
			t.Error("last_synced_at not set — step 4 (post-pull) did not run")
		}

		// 5. Embedding pass ran: the freshly upserted issue got a vector on
		//    issues.embedding. (Proves step 6 executed — embeddings are the
		//    flywheel's semantic index.)
		var embCount int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM issues WHERE org_id=$1 AND number=4242 AND embedding IS NOT NULL`, orgID).
			Scan(&embCount); err != nil {
			return err
		}
		if embCount == 0 {
			t.Error("no embedding for the synced issue — post-sync embed step did not run")
		}
		return nil
	}); err != nil {
		t.Fatalf("side-effect verification: %v", err)
	}
}
