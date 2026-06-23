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

	// Activate the org RLS context BEFORE inserting org-scoped rows — under enforced
	// RLS the WITH CHECK policy requires app.current_org to match. set_config(...,true)
	// is the parameterized, transaction-local equivalent of SET LOCAL (matches db.WithOrg).
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org', $1, true)", orgID); err != nil {
		t.Fatalf("set org: %v", err)
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
		repo              string
		sha, login, email string
		isAgent           bool
		adds, dels        int
		at                time.Time
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
	contribs, err := filter.Contributors(ctx, tx, orgID)
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

	// ── Agent share ──────────────────────────────────────────────────────────
	// Of the 6 commits exactly one (sha6) is agent-authored.
	share, err := filter.AgentShare(ctx, tx)
	if err != nil {
		t.Fatalf("AgentShare: %v", err)
	}
	if share.AgentCommits != 1 {
		t.Errorf("AgentCommits = %d, want 1", share.AgentCommits)
	}
	if share.HumanCommits != 5 {
		t.Errorf("HumanCommits = %d, want 5", share.HumanCommits)
	}
	ats, err := filter.AgentShareOverTime(ctx, tx)
	if err != nil {
		t.Fatalf("AgentShareOverTime: %v", err)
	}
	var agentTotal, humanTotal int
	for _, d := range ats {
		agentTotal += d.Agent
		humanTotal += d.Human
	}
	if agentTotal != 1 || humanTotal != 5 {
		t.Errorf("agent-share over time totals = (%d,%d), want (1,5)", agentTotal, humanTotal)
	}

	// ── Pull requests ────────────────────────────────────────────────────────
	// 4 PRs: 3 merged (lead times 2h, 4h, 24h), 1 open. merge rate = 3/4.
	prDay := time.Date(2026, 3, 5, 8, 0, 0, 0, time.UTC)
	type prFix struct {
		ext          string
		state        string
		login        string
		adds, dels   int
		changedFiles int
		firstCommit  time.Time
		merged       *time.Time
	}
	mAt := func(h int) *time.Time { v := prDay.Add(time.Duration(h) * time.Hour); return &v }
	prFixtures := []prFix{
		{"pr1", "merged", "jane", 100, 10, 5, prDay, mAt(2)}, // 2h lead
		{"pr2", "merged", "bob", 50, 5, 3, prDay, mAt(4)},    // 4h lead
		{"pr3", "merged", "jane", 20, 2, 8, prDay, mAt(24)},  // 24h lead
		{"pr4", "open", "bob", 10, 0, 1, prDay, nil},         // still open
	}
	for _, p := range prFixtures {
		if _, err := tx.Exec(ctx,
			`INSERT INTO pull_requests (org_id, repo_id, platform, external_id, number,
			 title, author_login, state, additions, deletions, changed_files,
			 first_commit_at, merged_at, created_at)
			 VALUES ($1,$2,'github',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
			orgID, repoA, p.ext, 1, "title "+p.ext, p.login, p.state,
			p.adds, p.dels, p.changedFiles, p.firstCommit, p.merged, prDay,
		); err != nil {
			t.Fatalf("insert pr %s: %v", p.ext, err)
		}
	}
	prs, err := filter.PRStats(ctx, tx)
	if err != nil {
		t.Fatalf("PRStats: %v", err)
	}
	if prs.Total != 4 {
		t.Errorf("PR total = %d, want 4", prs.Total)
	}
	if prs.Merged != 3 {
		t.Errorf("PR merged = %d, want 3", prs.Merged)
	}
	if prs.Open != 1 {
		t.Errorf("PR open = %d, want 1", prs.Open)
	}
	if len(prs.LeadTimeHours) != 3 {
		t.Fatalf("lead-time samples = %d, want 3", len(prs.LeadTimeHours))
	}
	// Verify the lead-time samples are the expected hours (order not guaranteed).
	wantLead := map[int]bool{2: false, 4: false, 24: false}
	for _, h := range prs.LeadTimeHours {
		wantLead[int(h+0.5)] = true
	}
	for h, seen := range wantLead {
		if !seen {
			t.Errorf("missing lead-time sample ~%dh (got %v)", h, prs.LeadTimeHours)
		}
	}
	if prs.SumChangedFiles != int64(5+3+8+1) {
		t.Errorf("sum changed files = %d, want 17", prs.SumChangedFiles)
	}
	tp, err := filter.PRThroughput(ctx, tx)
	if err != nil {
		t.Fatalf("PRThroughput: %v", err)
	}
	var opened, merged int
	for _, d := range tp {
		opened += d.Opened
		merged += d.Merged
	}
	if opened != 4 {
		t.Errorf("throughput opened total = %d, want 4", opened)
	}
	if merged != 3 {
		t.Errorf("throughput merged total = %d, want 3", merged)
	}

	// ── Issues ───────────────────────────────────────────────────────────────
	// Create a project + 4 issues: 1 open, 1 in_progress, 2 done.
	var projID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO projects (org_id, name) VALUES ($1, 'Apollo') RETURNING id`,
		orgID,
	).Scan(&projID); err != nil {
		t.Fatalf("create project: %v", err)
	}
	issueDay := time.Date(2026, 3, 6, 9, 0, 0, 0, time.UTC)
	type issueFix struct {
		title, state string
		withProject  bool
	}
	issueFixtures := []issueFix{
		{"i-open", "open", true},
		{"i-prog", "in_progress", true},
		{"i-done1", "done", true},
		{"i-done2", "closed", false}, // no project
	}
	for _, is := range issueFixtures {
		var pid any
		if is.withProject {
			pid = projID
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO issues (org_id, project_id, repo_id, source, title, state, created_at, updated_at)
			 VALUES ($1,$2,$3,'native',$4,$5,$6,$6)`,
			orgID, pid, repoA, is.title, is.state, issueDay,
		); err != nil {
			t.Fatalf("insert issue %s: %v", is.title, err)
		}
	}
	counts, err := filter.IssueFlowCounts(ctx, tx)
	if err != nil {
		t.Fatalf("IssueFlowCounts: %v", err)
	}
	if counts.Open != 1 || counts.InProgress != 1 || counts.Done != 1 || counts.Closed != 1 {
		t.Errorf("issue counts = %+v, want open=1 inProgress=1 done=1 closed=1", counts)
	}
	opIss, err := filter.IssuesOpenedOverTime(ctx, tx)
	if err != nil {
		t.Fatalf("IssuesOpenedOverTime: %v", err)
	}
	var openedIss int
	for _, d := range opIss {
		openedIss += d.Count
	}
	if openedIss != 4 {
		t.Errorf("issues opened total = %d, want 4", openedIss)
	}
	byProj, err := filter.IssuesByProject(ctx, tx)
	if err != nil {
		t.Fatalf("IssuesByProject: %v", err)
	}
	var apollo *IssueProjectStat
	for i := range byProj {
		if byProj[i].Project == "Apollo" {
			apollo = &byProj[i]
		}
	}
	if apollo == nil {
		t.Fatalf("Apollo project missing from issues-by-project")
	}
	// Apollo has open(open) + in_progress(open) + done(done) = 2 open, 1 done.
	if apollo.Open != 2 || apollo.Done != 1 {
		t.Errorf("Apollo by-project = (open=%d done=%d), want (2,1)", apollo.Open, apollo.Done)
	}

	// ── Per-project stats ────────────────────────────────────────────────────
	projStats, err := filter.ProjectStats(ctx, tx)
	if err != nil {
		t.Fatalf("ProjectStats: %v", err)
	}
	var apolloStat *ProjectStat
	for i := range projStats {
		if projStats[i].Name == "Apollo" {
			apolloStat = &projStats[i]
		}
	}
	if apolloStat == nil {
		t.Fatalf("Apollo missing from project stats")
	}
	if apolloStat.OpenIssues != 2 || apolloStat.DoneIssues != 1 {
		t.Errorf("Apollo issues = (open=%d done=%d), want (2,1)", apolloStat.OpenIssues, apolloStat.DoneIssues)
	}
	// Apollo's issues link to repoA, which has 4 commits in the window.
	if apolloStat.Commits != 4 {
		t.Errorf("Apollo commits (via repoA linkage) = %d, want 4", apolloStat.Commits)
	}

	t.Logf("analytics aggregates OK: %d commits, %d contributors, %d repos; "+
		"PRs total=%d merged=%d; agent=%d human=%d",
		sum.TotalCommits, sum.Contributors, sum.Repos,
		prs.Total, prs.Merged, share.AgentCommits, share.HumanCommits)
}

// TestCommitsByContributor verifies the per-contributor over-time series:
// top-N ranking by total commits, identity-merge by email, 0-filled shared
// bucket axis, and the optional "Everyone else" aggregate line.
func TestCommitsByContributor(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping commits-by-contributor integration test")
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
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orgID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1, $2) RETURNING id`,
		fmt.Sprintf("cbc-test-%d", time.Now().UnixNano()), "CBC Test Org",
	).Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org', $1, true)", orgID); err != nil {
		t.Fatalf("set org: %v", err)
	}
	var repoID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO repos (org_id, platform, external_id, full_name)
		 VALUES ($1, 'github', $2, 'acme/series') RETURNING id`,
		orgID, fmt.Sprintf("cbc-repo-%d", time.Now().UnixNano()),
	).Scan(&repoID); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	// Three authors across three distinct weeks so weekly bucketing yields a
	// 3-bucket shared x-axis and each author has a "hole" to be 0-filled.
	//   wk0 (Mar  2): jane x3, bob x1
	//   wk1 (Mar  9): jane x2, carol x1
	//   wk2 (Mar 16): bob  x2
	// totals: jane=5, bob=3, carol=1.
	wk0 := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC) // a Monday
	wk1 := wk0.AddDate(0, 0, 7)
	wk2 := wk0.AddDate(0, 0, 14)
	type fix struct {
		sha, login, email string
		at                time.Time
	}
	fixtures := []fix{
		{"c1", "jane", "jane@work", wk0}, {"c2", "jane", "jane@work", wk0.Add(time.Hour)}, {"c3", "jane", "jane@work", wk0.Add(2 * time.Hour)},
		{"c4", "bob", "bob@work", wk0},
		{"c5", "jane", "jane@work", wk1}, {"c6", "jane", "jane@work", wk1.Add(time.Hour)},
		{"c7", "carol", "carol@work", wk1},
		{"c8", "bob", "bob@work", wk2}, {"c9", "bob", "bob@work", wk2.Add(time.Hour)},
	}
	for _, f := range fixtures {
		if _, err := tx.Exec(ctx,
			`INSERT INTO commits (org_id, repo_id, sha, author_login, author_email,
			 is_agent, message, additions, deletions, committed_at)
			 VALUES ($1,$2,$3,$4,$5,false,$6,1,0,$7)`,
			orgID, repoID, f.sha, f.login, f.email, "msg "+f.sha, f.at,
		); err != nil {
			t.Fatalf("insert commit %s: %v", f.sha, err)
		}
	}

	filter := AnalyticsFilter{
		From: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
	}

	// top-2, no "other": jane then bob, each 0-filled to the 3-week axis.
	series, err := filter.CommitsByContributor(ctx, tx, orgID, "week", 2, false)
	if err != nil {
		t.Fatalf("CommitsByContributor: %v", err)
	}
	if len(series) != 2 {
		t.Fatalf("series = %d, want 2 (top-2)", len(series))
	}
	if series[0].Email != "jane@work" {
		t.Errorf("series[0].Email = %q, want jane@work (top contributor)", series[0].Email)
	}
	if series[1].Email != "bob@work" {
		t.Errorf("series[1].Email = %q, want bob@work (2nd)", series[1].Email)
	}
	// Shared, aligned 3-bucket axis for every series.
	if len(series[0].Points) != 3 || len(series[1].Points) != 3 {
		t.Fatalf("points = (%d,%d), want both 3 (shared axis)", len(series[0].Points), len(series[1].Points))
	}
	for i := range series[0].Points {
		if !series[0].Points[i].Date.Equal(series[1].Points[i].Date) {
			t.Errorf("axis mismatch at %d: %v vs %v", i, series[0].Points[i].Date, series[1].Points[i].Date)
		}
	}
	// jane: [3,2,0]; bob: [1,0,2] (the 0s are the 0-fill).
	wantJane := []int{3, 2, 0}
	wantBob := []int{1, 0, 2}
	for i, w := range wantJane {
		if series[0].Points[i].Count != w {
			t.Errorf("jane point[%d] = %d, want %d", i, series[0].Points[i].Count, w)
		}
	}
	for i, w := range wantBob {
		if series[1].Points[i].Count != w {
			t.Errorf("bob point[%d] = %d, want %d", i, series[1].Points[i].Count, w)
		}
	}
	janeTotal := 0
	for _, p := range series[0].Points {
		janeTotal += p.Count
	}
	if janeTotal != 5 {
		t.Errorf("jane total = %d, want 5", janeTotal)
	}

	// top-2 WITH "other": carol (the only non-top-2 contributor) collapses into
	// a single "Everyone else" line with total 1, also 0-filled to 3 buckets.
	withOther, err := filter.CommitsByContributor(ctx, tx, orgID, "week", 2, true)
	if err != nil {
		t.Fatalf("CommitsByContributor(other): %v", err)
	}
	if len(withOther) != 3 {
		t.Fatalf("series with other = %d, want 3 (top-2 + everyone else)", len(withOther))
	}
	other := withOther[2]
	if other.Name != "Everyone else" {
		t.Errorf("other.Name = %q, want \"Everyone else\"", other.Name)
	}
	if len(other.Points) != 3 {
		t.Fatalf("other points = %d, want 3", len(other.Points))
	}
	otherTotal := 0
	for _, p := range other.Points {
		otherTotal += p.Count
	}
	if otherTotal != 1 {
		t.Errorf("everyone-else total = %d, want 1 (carol)", otherTotal)
	}

	// top-5 (more than the 3 distinct authors) → exactly 3 series, no "other".
	all, err := filter.CommitsByContributor(ctx, tx, orgID, "week", 5, true)
	if err != nil {
		t.Fatalf("CommitsByContributor(top5): %v", err)
	}
	if len(all) != 3 {
		t.Errorf("top-5 over 3 authors = %d series, want 3 (no everyone-else)", len(all))
	}

	t.Logf("commits-by-contributor OK: top series jane=%d bob=%d, %d buckets",
		janeTotal, func() int {
			n := 0
			for _, p := range series[1].Points {
				n += p.Count
			}
			return n
		}(), len(series[0].Points))
}

// TestUpsertPlatformDates verifies that UpsertPR and UpsertIssue persist the REAL
// platform created_at / updated_at — NOT the sync time (DB default now()). This is
// the regression for "pull requests date dont seem to be date of pull request":
// the upserts previously omitted created_at, so every PR/issue was stamped at sync.
func TestUpsertPlatformDates(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping platform-dates integration test")
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

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orgID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1, $2) RETURNING id`,
		fmt.Sprintf("pdates-test-%d", time.Now().UnixNano()), "Platform Dates Test",
	).Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org', $1, true)", orgID); err != nil {
		t.Fatalf("set org: %v", err)
	}
	var repoID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO repos (org_id, platform, external_id, full_name)
		 VALUES ($1, 'github', $2, 'acme/dates') RETURNING id`,
		orgID, fmt.Sprintf("pdates-repo-%d", time.Now().UnixNano()),
	).Scan(&repoID); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	// A platform created_at well in the PAST (distinct from now()), so a regression
	// (created_at = now()) is unambiguously caught.
	platformCreated := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	platformMerged := time.Date(2024, 1, 16, 12, 0, 0, 0, time.UTC)

	// ── PR: created_at must be the platform date, not the sync time ──────────────
	if err := UpsertPR(ctx, tx, &PullRequest{
		OrgID: orgID, RepoID: repoID, Platform: "github", ExternalID: "pr-dates-1",
		Number: 7, Title: "old PR", AuthorLogin: "jane", State: "merged",
		MergedAt: platformMerged, CreatedAt: platformCreated,
	}); err != nil {
		t.Fatalf("UpsertPR: %v", err)
	}
	var gotPRCreated time.Time
	if err := tx.QueryRow(ctx,
		`SELECT created_at FROM pull_requests WHERE org_id=$1 AND repo_id=$2 AND external_id=$3`,
		orgID, repoID, "pr-dates-1").Scan(&gotPRCreated); err != nil {
		t.Fatalf("read PR created_at: %v", err)
	}
	if !gotPRCreated.UTC().Equal(platformCreated) {
		t.Errorf("PR created_at = %s, want platform date %s (regression: was sync time)",
			gotPRCreated.UTC(), platformCreated)
	}

	// Re-upsert with the SAME created_at must keep it stable (idempotent).
	if err := UpsertPR(ctx, tx, &PullRequest{
		OrgID: orgID, RepoID: repoID, Platform: "github", ExternalID: "pr-dates-1",
		Number: 7, Title: "old PR v2", AuthorLogin: "jane", State: "merged",
		MergedAt: platformMerged, CreatedAt: platformCreated,
	}); err != nil {
		t.Fatalf("UpsertPR update: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`SELECT created_at FROM pull_requests WHERE org_id=$1 AND repo_id=$2 AND external_id=$3`,
		orgID, repoID, "pr-dates-1").Scan(&gotPRCreated); err != nil {
		t.Fatalf("re-read PR created_at: %v", err)
	}
	if !gotPRCreated.UTC().Equal(platformCreated) {
		t.Errorf("PR created_at after re-upsert = %s, want %s", gotPRCreated.UTC(), platformCreated)
	}

	// ── Issue: created_at + updated_at must be the platform dates ────────────────
	issueCreated := time.Date(2024, 2, 1, 8, 0, 0, 0, time.UTC)
	issueUpdated := time.Date(2024, 2, 3, 9, 30, 0, 0, time.UTC)
	const iq = `
		INSERT INTO issues
			(org_id, repo_id, source, platform, external_id, number, title, body, state, labels,
			 created_at, updated_at)
		VALUES ($1,$2,'git','github',$3,$4,$5,'',$6,'{}', COALESCE($7,now()), COALESCE($8,now()))`
	if _, err := tx.Exec(ctx, iq,
		orgID, repoID, "iss-dates-1", 3, "old issue", "open", issueCreated, issueUpdated,
	); err != nil {
		t.Fatalf("insert issue with platform dates: %v", err)
	}
	var gotIssCreated, gotIssUpdated time.Time
	if err := tx.QueryRow(ctx,
		`SELECT created_at, updated_at FROM issues WHERE org_id=$1 AND platform='github' AND external_id=$2`,
		orgID, "iss-dates-1").Scan(&gotIssCreated, &gotIssUpdated); err != nil {
		t.Fatalf("read issue dates: %v", err)
	}
	if !gotIssCreated.UTC().Equal(issueCreated) {
		t.Errorf("issue created_at = %s, want platform date %s", gotIssCreated.UTC(), issueCreated)
	}
	if !gotIssUpdated.UTC().Equal(issueUpdated) {
		t.Errorf("issue updated_at = %s, want platform date %s", gotIssUpdated.UTC(), issueUpdated)
	}
}
