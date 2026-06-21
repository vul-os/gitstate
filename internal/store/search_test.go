// Package store — search_test.go
// DB-backed tests for Search (Wave 4 full-text + fuzzy search):
//   - FTS finds an issue/PR/commit by a content word;
//   - results are ranked (the issue whose title+body is densest for the term
//     outranks a weaker match);
//   - the fuzzy fallback fires for a typo ("athentication" → "authentication")
//     and reports fuzzy=true;
//   - RLS keeps another org's rows out of the results.
//
// Skips when DATABASE_URL is unset. Runs under the gitstate_app FORCE-RLS role.
package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestSearchFTSAndFuzzy(t *testing.T) {
	database := tokensTestDB(t)
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ns := time.Now().UnixNano()

	var orgID, otherOrgID, repoID string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("srch-%d", ns), "Search Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("srch-other-%d", ns), "Other Org").Scan(&otherOrgID); err != nil {
		t.Fatalf("create other org: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM organizations WHERE id IN ($1,$2)`, orgID, otherOrgID)
	})

	var strongIssueID, weakIssueID string
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO repos (org_id, platform, external_id, full_name) VALUES ($1,'github',$2,'acme/svc') RETURNING id`,
			orgID, fmt.Sprintf("srch-repo-%d", ns)).Scan(&repoID); err != nil {
			return err
		}
		// Strong match: "authentication" appears in both title and body.
		if err := tx.QueryRow(ctx,
			`INSERT INTO issues (org_id, repo_id, source, number, title, body, state, labels)
			 VALUES ($1,$2,'native',101,$3,$4,'open',$5) RETURNING id`,
			orgID, repoID, "Fix authentication redirect loop",
			"The authentication flow breaks the login authentication redirect.",
			[]string{"bug"}).Scan(&strongIssueID); err != nil {
			return err
		}
		// Weak match: "authentication" appears once, in the body only.
		if err := tx.QueryRow(ctx,
			`INSERT INTO issues (org_id, repo_id, source, number, title, body, state, labels)
			 VALUES ($1,$2,'native',102,$3,$4,'open',$5) RETURNING id`,
			orgID, repoID, "Update README",
			"Mention authentication setup somewhere.",
			[]string{"docs"}).Scan(&weakIssueID); err != nil {
			return err
		}
		// A PR matching on title.
		if _, err := tx.Exec(ctx,
			`INSERT INTO pull_requests (org_id, repo_id, platform, external_id, number, title, state, created_at)
			 VALUES ($1,$2,'github',$3,7,'Refactor authentication middleware','open',now())`,
			orgID, repoID, fmt.Sprintf("srch-pr-%d", ns)); err != nil {
			return err
		}
		// A commit matching on message.
		if _, err := tx.Exec(ctx,
			`INSERT INTO commits (org_id, repo_id, sha, author_login, message, committed_at)
			 VALUES ($1,$2,$3,'alice',E'fix: harden authentication checks\n\ndetails',now())`,
			orgID, repoID, fmt.Sprintf("authsha%020d", ns%100000)); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seed org: %v", err)
	}

	// Seed a matching issue in ANOTHER org — must never appear in orgID's results.
	if err := database.WithOrg(ctx, otherOrgID, func(tx pgx.Tx) error {
		var otherRepo string
		if err := tx.QueryRow(ctx,
			`INSERT INTO repos (org_id, platform, external_id, full_name) VALUES ($1,'github',$2,'other/svc') RETURNING id`,
			otherOrgID, fmt.Sprintf("srch-other-repo-%d", ns)).Scan(&otherRepo); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO issues (org_id, repo_id, source, number, title, body, state)
			 VALUES ($1,$2,'native',999,'Other org authentication bug','secret authentication body','open')`,
			otherOrgID, otherRepo)
		return err
	}); err != nil {
		t.Fatalf("seed other org: %v", err)
	}

	// ── 1. FTS finds all three entity types by a content word, and ranks. ──
	var results []SearchResult
	var fuzzy bool
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var e error
		results, fuzzy, e = Search(ctx, tx, orgID, "authentication", nil, 20)
		return e
	}); err != nil {
		t.Fatalf("Search FTS: %v", err)
	}
	if fuzzy {
		t.Fatalf("expected exact FTS (fuzzy=false), got fuzzy=true")
	}

	gotTypes := map[string]bool{}
	for _, r := range results {
		gotTypes[r.Type] = true
	}
	for _, want := range []string{SearchTypeIssue, SearchTypePR, SearchTypeCommit} {
		if !gotTypes[want] {
			t.Fatalf("FTS missing type %q in results: %+v", want, results)
		}
	}

	// Ranking: among issues, the strong (title+body) match must outrank the weak one.
	strongRank, weakRank := -1, -1
	for i, r := range results {
		if r.ID == strongIssueID {
			strongRank = i
		}
		if r.ID == weakIssueID {
			weakRank = i
		}
	}
	if strongRank == -1 {
		t.Fatalf("strong issue not found in results: %+v", results)
	}
	if weakRank != -1 && strongRank > weakRank {
		t.Fatalf("strong issue (idx %d) should outrank weak issue (idx %d)", strongRank, weakRank)
	}
	// Snippet should highlight the matched term.
	for _, r := range results {
		if r.ID == strongIssueID && r.Snippet == "" {
			t.Fatalf("expected a non-empty snippet for the strong issue")
		}
	}

	// ── 2. RLS: the other org's matching issue must NOT appear. ──
	for _, r := range results {
		if r.Number == 999 {
			t.Fatalf("RLS leak: other-org issue surfaced in results: %+v", r)
		}
	}

	// ── 3. Type filter narrows the result set. ──
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var e error
		results, fuzzy, e = Search(ctx, tx, orgID, "authentication", []string{"prs"}, 20)
		return e
	}); err != nil {
		t.Fatalf("Search type-filter: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected PR results for type=prs")
	}
	for _, r := range results {
		if r.Type != SearchTypePR {
			t.Fatalf("type filter leaked non-PR result: %+v", r)
		}
	}

	// ── 4. Fuzzy fallback: a typo finds the issue and reports fuzzy=true. ──
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var e error
		results, fuzzy, e = Search(ctx, tx, orgID, "athentication redirct", []string{"issues"}, 20)
		return e
	}); err != nil {
		t.Fatalf("Search fuzzy: %v", err)
	}
	if !fuzzy {
		t.Fatalf("expected fuzzy=true for a typo query, got fuzzy=false with results=%+v", results)
	}
	found := false
	for _, r := range results {
		if r.ID == strongIssueID {
			found = true
		}
	}
	if !found {
		t.Fatalf("fuzzy fallback should find the authentication issue via typo, got %+v", results)
	}
}
