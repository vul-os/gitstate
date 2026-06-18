// Package store — analytics_test.go
// DB-backed tests for the git-analytics aggregates. They insert a small fixture
// of commits inside a transaction that is ALWAYS rolled back (leaving the DB
// clean) and assert Summary / Heatmap / Contributors / RepoStats / CommitsOnDay
// compute correctly under the org's RLS context.
//
// Mirrors rls_test.go: when DATABASE_URL is unset the test skips cleanly so
// `go test ./...` passes in CI without a database.
package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAnalyticsAggregates(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping analytics integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer conn.Release()

	// Everything happens in one tx we roll back at the end → DB stays clean.
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// ── fixture: one org, two repos, a handful of commits ────────────────────
	var orgID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1, $2) RETURNING id`,
		fmt.Sprintf("analytics-test-%d", time.Now().UnixNano()), "Analytics Test Org",
	).Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}

	var repoA, repoB string
	if err := tx.QueryRow(ctx,
		`INSERT INTO repos (org_id, platform, external_id, full_name)
		 VALUES ($1, 'github', $2, 'acme/alpha') RETURNING id`,
		orgID, fmt.Sprintf("ext-a-%d", time.Now().UnixNano()),
	).Scan(&repoA); err != nil {
		t.Fatalf("create repo A: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO repos (org_id, platform, external_id, full_name)
		 VALUES ($1, 'github', $2, 'acme/beta') RETURNING id`,
		orgID, fmt.Sprintf("ext-b-%d", time.Now().UnixNano()),
	).Scan(&repoB); err != nil {
		t.Fatalf("create repo B: %v", err)
	}

	// Activate the org RLS context for the commit inserts + reads.
	// SET LOCAL does not accept bind params; set_config(...,true) is the
	// parameterized, transaction-local equivalent (matches db.WithOrg).
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org', $1, true)", orgID); err != nil {
		t.Fatalf("set org: %v", err)
	}

	// Fixed reference day so date assertions are deterministic.
	day1 := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 3, 11, 14, 0, 0, 0, time.UTC)

	type fix struct {
		repo               string
		sha, login, email  string
		isAgent            bool
		adds, dels         int
		at                 time.Time
	}
	fixtures := []fix{
		// jane: 3 commits across 2 days, repo A — top contributor
		{repoA, "sha1", "jane", "jane@work", false, 100, 10, day1},
		{repoA, "sha2", "jane", "jane@work", false, 50, 5, day1.Add(time.Hour)},
		{repoA, "sha3", "jane", "jane@work", false, 20, 0, day2},
		// bob: 2 commits, one in each repo, 1 day
		{repoA, "sha4", "bob", "bob@work", false, 10, 2, day2},
		{repoB, "sha5", "bob", "bob@work", false, 5, 1, day2},
		// agent: 1 commit repo B
		{repoB, "sha6", "agent-x", "agent@bots", true, 200, 100, day1},
	}
	for _, f := range fixtures {
		if _, err := tx.Exec(ctx,
			`INSERT INTO commits (org_id, repo_id, sha, author_login, author_email,
			 is_agent, message, additions, deletions, committed_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			orgID, f.repo, f.sha, f.login, f.email, f.isAgent,
			"msg "+f.sha, f.adds, f.dels, f.at,
		); err != nil {
			t.Fatalf("insert commit %s: %v", f.sha, err)
		}
	}

	// Window covering both days.
	filter := AnalyticsFilter{
		From: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
	}

	// ── Summary ──────────────────────────────────────────────────────────────
	sum, err := filter.Summary(ctx, tx)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if sum.TotalCommits != 6 {
		t.Errorf("TotalCommits = %d, want 6", sum.TotalCommits)
	}
	if sum.Repos != 2 {
		t.Errorf("Repos = %d, want 2", sum.Repos)
	}
	if sum.Contributors != 3 {
		t.Errorf("Contributors = %d, want 3", sum.Contributors)
	}
	if sum.ActiveDays != 2 {
		t.Errorf("ActiveDays = %d, want 2", sum.ActiveDays)
	}
	wantAdds := int64(100 + 50 + 20 + 10 + 5 + 200)
	wantDels := int64(10 + 5 + 0 + 2 + 1 + 100)
	if sum.Additions != wantAdds {
		t.Errorf("Additions = %d, want %d", sum.Additions, wantAdds)
	}
	if sum.Deletions != wantDels {
		t.Errorf("Deletions = %d, want %d", sum.Deletions, wantDels)
	}
	if sum.NetLines != wantAdds-wantDels {
		t.Errorf("NetLines = %d, want %d", sum.NetLines, wantAdds-wantDels)
	}

	// ── Heatmap ──────────────────────────────────────────────────────────────
	hm, err := filter.Heatmap(ctx, tx)
	if err != nil {
		t.Fatalf("Heatmap: %v", err)
	}
	if len(hm) != 2 {
		t.Fatalf("Heatmap days = %d, want 2", len(hm))
	}
	// day1 has 3 commits (sha1, sha2, sha6); day2 has 3 (sha3, sha4, sha5).
	byDay := map[string]int{}
	for _, d := range hm {
		byDay[d.Date.Format("2006-01-02")] = d.Count
	}
	if byDay["2026-03-10"] != 3 {
		t.Errorf("heatmap 2026-03-10 = %d, want 3", byDay["2026-03-10"])
	}
	if byDay["2026-03-11"] != 3 {
		t.Errorf("heatmap 2026-03-11 = %d, want 3", byDay["2026-03-11"])
	}

	// ── CommitsOverTime (week) ───────────────────────────────────────────────
	wk, err := filter.CommitsOverTime(ctx, tx, "week")
	if err != nil {
		t.Fatalf("CommitsOverTime: %v", err)
	}
	total := 0
	for _, d := range wk {
		total += d.Count
	}
	if total != 6 {
		t.Errorf("commits-over-time total = %d, want 6", total)
	}

	// ── Contributors ─────────────────────────────────────────────────────────
	contribs, err := filter.Contributors(ctx, tx)
	if err != nil {
		t.Fatalf("Contributors: %v", err)
	}
	if len(contribs) != 3 {
		t.Fatalf("Contributors = %d, want 3", len(contribs))
	}
	// Ranked by commits desc → jane first with 3.
	if contribs[0].Email != "jane@work" {
		t.Errorf("top contributor email = %q, want jane@work", contribs[0].Email)
	}
	if contribs[0].Commits != 3 {
		t.Errorf("jane commits = %d, want 3", contribs[0].Commits)
	}
	if contribs[0].ActiveDays != 2 {
		t.Errorf("jane active days = %d, want 2", contribs[0].ActiveDays)
	}
	if contribs[0].Projects != 1 {
		t.Errorf("jane projects(repos) = %d, want 1", contribs[0].Projects)
	}
	if contribs[0].Additions != 170 {
		t.Errorf("jane additions = %d, want 170", contribs[0].Additions)
	}
	if !contribs[0].FirstAt.Equal(day1) {
		t.Errorf("jane firstAt = %v, want %v", contribs[0].FirstAt, day1)
	}
	// Find the agent and verify is_agent + 2 distinct repos for bob.
	var sawAgent bool
	for _, c := range contribs {
		if c.Email == "agent@bots" {
			sawAgent = true
			if !c.IsAgent {
				t.Errorf("agent contributor IsAgent = false, want true")
			}
		}
		if c.Email == "bob@work" && c.Projects != 2 {
			t.Errorf("bob projects(repos) = %d, want 2", c.Projects)
		}
	}
	if !sawAgent {
		t.Errorf("agent contributor missing from leaderboard")
	}

	// ── RepoStats ────────────────────────────────────────────────────────────
	repos, err := filter.RepoStats(ctx, tx)
	if err != nil {
		t.Fatalf("RepoStats: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("RepoStats = %d, want 2", len(repos))
	}
	// repoA has 4 commits (jane x3 + bob x1) → ranked first.
	if repos[0].FullName != "acme/alpha" {
		t.Errorf("top repo = %q, want acme/alpha", repos[0].FullName)
	}
	if repos[0].Commits != 4 {
		t.Errorf("acme/alpha commits = %d, want 4", repos[0].Commits)
	}
	if repos[0].Contributors != 2 {
		t.Errorf("acme/alpha contributors = %d, want 2", repos[0].Contributors)
	}

	// ── CommitsOnDay drill-down ──────────────────────────────────────────────
	dayCommits, err := filter.CommitsOnDay(ctx, tx, day1)
	if err != nil {
		t.Fatalf("CommitsOnDay: %v", err)
	}
	if len(dayCommits) != 3 {
		t.Errorf("CommitsOnDay(day1) = %d, want 3", len(dayCommits))
	}
	for _, dc := range dayCommits {
		if dc.RepoFullName == "" {
			t.Errorf("day commit %s missing repo full name", dc.SHA)
		}
	}

	// ── Filter: author narrows everything ────────────────────────────────────
	janeOnly := filter
	janeOnly.Author = "jane@work"
	js, err := janeOnly.Summary(ctx, tx)
	if err != nil {
		t.Fatalf("Summary(author=jane): %v", err)
	}
	if js.TotalCommits != 3 {
		t.Errorf("author-filtered commits = %d, want 3", js.TotalCommits)
	}
	if js.Contributors != 1 {
		t.Errorf("author-filtered contributors = %d, want 1", js.Contributors)
	}

	// ── Filter: author by login also works ───────────────────────────────────
	bobByLogin := filter
	bobByLogin.Author = "bob"
	bs, err := bobByLogin.Summary(ctx, tx)
	if err != nil {
		t.Fatalf("Summary(author=bob login): %v", err)
	}
	if bs.TotalCommits != 2 {
		t.Errorf("login-filtered commits = %d, want 2", bs.TotalCommits)
	}

	// ── Filter: repo narrows to repoB ────────────────────────────────────────
	repoBOnly := filter
	repoBOnly.RepoID = repoB
	rs, err := repoBOnly.Summary(ctx, tx)
	if err != nil {
		t.Fatalf("Summary(repo=B): %v", err)
	}
	if rs.TotalCommits != 2 {
		t.Errorf("repo-filtered commits = %d, want 2", rs.TotalCommits)
	}

	t.Logf("analytics aggregates OK: %d commits, %d contributors, %d repos",
		sum.TotalCommits, sum.Contributors, sum.Repos)
}
