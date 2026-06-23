// Package store — metrics_test.go
// DB-backed tests for the cycle_times and involvement read/write paths that the
// metric-correctness fixes depend on:
//
//   - UpsertCycleTime must be IDEMPOTENT: re-running ComputeCycleTimes (which
//     calls UpsertCycleTime per PR) must NOT accumulate duplicate rows that
//     would triple-count a PR in the p50/p90/avg stats.
//   - ListCycleTimes must return ONE measurement per PR (the latest) in
//     chronological merge order, carrying the PR title/repo for the chart.
//   - ListInvolvementMembers must aggregate the monthly involvement rows into a
//     single card per user, joined to the users table, dropping orphan rows that
//     carry no user_id.
//
// One transaction, always rolled back. RLS enforced under the app role.
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

func metricsTestTx(t *testing.T) (context.Context, pgx.Tx, string) {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping metrics store integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	t.Cleanup(conn.Release)

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	ns := time.Now().UnixNano()
	var orgID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("met-%d", ns), "Metrics Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org', $1, true)", orgID); err != nil {
		t.Fatalf("set org: %v", err)
	}
	return ctx, tx, orgID
}

// TestUpsertCycleTimeIdempotent proves the duplicate-row bug is fixed: calling
// UpsertCycleTime repeatedly for the same PR leaves exactly one row (the latest),
// so re-computes don't inflate the sample size.
func TestUpsertCycleTimeIdempotent(t *testing.T) {
	ctx, tx, orgID := metricsTestTx(t)
	ns := time.Now().UnixNano()

	var repoID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO repos (org_id, platform, external_id, full_name) VALUES ($1,'github',$2,'acme/svc') RETURNING id`,
		orgID, fmt.Sprintf("met-repo-%d", ns)).Scan(&repoID); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	var prID string
	merged := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	first := merged.Add(-48 * time.Hour)
	if err := tx.QueryRow(ctx,
		`INSERT INTO pull_requests (org_id, repo_id, platform, external_id, number, title, state, first_commit_at, merged_at, created_at)
		 VALUES ($1,$2,'github',$3,1,'add feature','merged',$4,$5,$4) RETURNING id`,
		orgID, repoID, fmt.Sprintf("met-pr-%d", ns), first, merged).Scan(&prID); err != nil {
		t.Fatalf("insert pr: %v", err)
	}

	lead := int64(172800) // 48h
	review := int64(3600)
	ct := CycleTime{OrgID: orgID, PRID: &prID, LeadTimeSecs: &lead, ReviewSecs: &review}

	// Three recomputes — historically this produced 3 rows.
	for i := 0; i < 3; i++ {
		if err := UpsertCycleTime(ctx, tx, ct); err != nil {
			t.Fatalf("upsert #%d: %v", i, err)
		}
	}

	var n int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM cycle_times WHERE org_id=$1 AND pr_id=$2`, orgID, prID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("cycle_times rows after 3 recomputes = %d, want 1 (idempotent)", n)
	}

	// ListCycleTimes returns the single measurement with PR context.
	rows, err := ListCycleTimes(ctx, tx, orgID, CycleTimeFilter{RepoID: repoID})
	if err != nil {
		t.Fatalf("ListCycleTimes: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListCycleTimes len = %d, want 1", len(rows))
	}
	got := rows[0]
	if got.LeadTimeSecs == nil || *got.LeadTimeSecs != lead {
		t.Errorf("lead = %v, want %d", got.LeadTimeSecs, lead)
	}
	if got.Title != "add feature" {
		t.Errorf("title = %q, want %q", got.Title, "add feature")
	}
	if got.Repo != "acme/svc" {
		t.Errorf("repo = %q, want %q", got.Repo, "acme/svc")
	}
	if !got.MergedAt.Equal(merged) {
		t.Errorf("mergedAt = %v, want %v", got.MergedAt, merged)
	}
}

// TestListCycleTimesChronologicalAndDeduped seeds two PRs, each with TWO
// cycle_times rows (simulating a pre-fix duplicate), and asserts the list
// returns the latest row per PR ordered by merge date ascending.
func TestListCycleTimesChronologicalAndDeduped(t *testing.T) {
	ctx, tx, orgID := metricsTestTx(t)
	ns := time.Now().UnixNano()

	var repoID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO repos (org_id, platform, external_id, full_name) VALUES ($1,'github',$2,'acme/web') RETURNING id`,
		orgID, fmt.Sprintf("met-repo2-%d", ns)).Scan(&repoID); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	type prSpec struct {
		merged time.Time
		latest int64 // the latest (correct) lead value
		stale  int64 // an older duplicate that must be ignored
	}
	// Insert OUT of merge order so we can prove ListCycleTimes sorts.
	specs := []prSpec{
		{merged: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC), latest: 200000, stale: 999999},
		{merged: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), latest: 100000, stale: 888888},
	}
	for i, s := range specs {
		var prID string
		if err := tx.QueryRow(ctx,
			`INSERT INTO pull_requests (org_id, repo_id, platform, external_id, number, title, state, first_commit_at, merged_at, created_at)
			 VALUES ($1,$2,'github',$3,$4,$5,'merged',$6,$7,$6) RETURNING id`,
			orgID, repoID, fmt.Sprintf("met-pr2-%d-%d", i, ns), i+1,
			fmt.Sprintf("pr-%d", i), s.merged.Add(-72*time.Hour), s.merged).Scan(&prID); err != nil {
			t.Fatalf("insert pr %d: %v", i, err)
		}
		// stale row first (earlier computed_at), then the latest.
		if _, err := tx.Exec(ctx,
			`INSERT INTO cycle_times (org_id, pr_id, lead_time_secs, computed_at) VALUES ($1,$2,$3, now() - interval '1 hour')`,
			orgID, prID, s.stale); err != nil {
			t.Fatalf("insert stale ct %d: %v", i, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO cycle_times (org_id, pr_id, lead_time_secs, computed_at) VALUES ($1,$2,$3, now())`,
			orgID, prID, s.latest); err != nil {
			t.Fatalf("insert latest ct %d: %v", i, err)
		}
	}

	rows, err := ListCycleTimes(ctx, tx, orgID, CycleTimeFilter{RepoID: repoID})
	if err != nil {
		t.Fatalf("ListCycleTimes: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len = %d, want 2 (one per PR, deduped)", len(rows))
	}
	// Chronological by merge date: May 1 first, then May 20.
	if rows[0].LeadTimeSecs == nil || *rows[0].LeadTimeSecs != 100000 {
		t.Errorf("row0 lead = %v, want 100000 (latest of earliest-merged PR)", rows[0].LeadTimeSecs)
	}
	if rows[1].LeadTimeSecs == nil || *rows[1].LeadTimeSecs != 200000 {
		t.Errorf("row1 lead = %v, want 200000", rows[1].LeadTimeSecs)
	}
	if !rows[0].MergedAt.Before(rows[1].MergedAt) {
		t.Errorf("rows not chronological: %v then %v", rows[0].MergedAt, rows[1].MergedAt)
	}
}

// TestListCycleTimesAuthorIdentities proves the cycle-time author filter matches
// PR author_login against the supplied identity set (a grouped contributor's full
// set), so filtering by a person returns ALL their PRs and none of another's.
func TestListCycleTimesAuthorIdentities(t *testing.T) {
	ctx, tx, orgID := metricsTestTx(t)
	ns := time.Now().UnixNano()

	var repoID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO repos (org_id, platform, external_id, full_name) VALUES ($1,'github',$2,'acme/ct') RETURNING id`,
		orgID, fmt.Sprintf("met-ctauth-%d", ns)).Scan(&repoID); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	// Two PRs by cameron under different logins, one by dana.
	specs := []struct {
		login  string
		merged time.Time
	}{
		{"cam", time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)},
		{"cameron", time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)},
		{"dana", time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)},
	}
	for i, s := range specs {
		var prID string
		if err := tx.QueryRow(ctx,
			`INSERT INTO pull_requests (org_id, repo_id, platform, external_id, number, title, state, author_login, first_commit_at, merged_at, created_at)
			 VALUES ($1,$2,'github',$3,$4,$5,'merged',$6,$7,$8,$7) RETURNING id`,
			orgID, repoID, fmt.Sprintf("met-ctauth-pr-%d-%d", i, ns), i+1,
			fmt.Sprintf("pr-%s", s.login), s.login, s.merged.Add(-24*time.Hour), s.merged).Scan(&prID); err != nil {
			t.Fatalf("insert pr %d: %v", i, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO cycle_times (org_id, pr_id, lead_time_secs, computed_at) VALUES ($1,$2,$3, now())`,
			orgID, prID, int64(86400)); err != nil {
			t.Fatalf("insert ct %d: %v", i, err)
		}
	}

	// Filter by cameron's identity set (both logins): 2 PRs, not dana's.
	rows, err := ListCycleTimes(ctx, tx, orgID, CycleTimeFilter{
		RepoID:           repoID,
		AuthorIdentities: []string{"cam", "cameron", "cam@a.com"},
	})
	if err != nil {
		t.Fatalf("ListCycleTimes: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("grouped author rows = %d, want 2 (cameron's two PRs)", len(rows))
	}

	// A single-identity set returns only that one PR.
	one, err := ListCycleTimes(ctx, tx, orgID, CycleTimeFilter{
		RepoID:           repoID,
		AuthorIdentities: []string{"cam"},
	})
	if err != nil {
		t.Fatalf("ListCycleTimes single: %v", err)
	}
	if len(one) != 1 {
		t.Errorf("single-identity rows = %d, want 1", len(one))
	}
}

// TestListInvolvementMembersAggregates seeds two monthly involvement rows for one
// user plus one orphan (null user_id) row, and asserts the aggregation collapses
// to a single member card, summed across periods, with the orphan excluded.
func TestListInvolvementMembersAggregates(t *testing.T) {
	ctx, tx, orgID := metricsTestTx(t)
	ns := time.Now().UnixNano()

	var userID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO users (email, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("dev-%d@acme.dev", ns), "Dev Person").Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}

	mk := func(periodStart time.Time, feats, reviews, areas, commits int) {
		t.Helper()
		dims := fmt.Sprintf(`{"commit_count":%d,"lines_added":10,"lines_deleted":2,"is_agent":false}`, commits)
		if _, err := tx.Exec(ctx,
			`INSERT INTO involvement (org_id, user_id, period_start, features_shipped, reviews_done, areas_owned, active, dimensions)
			 VALUES ($1,$2,$3,$4,$5,$6,true,$7::jsonb)`,
			orgID, userID, periodStart, feats, reviews, areas, dims); err != nil {
			t.Fatalf("insert involvement: %v", err)
		}
	}
	mk(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), 3, 2, 2, 20)
	mk(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), 4, 1, 3, 26)

	// Orphan row (null user_id) — must be excluded from the member list.
	if _, err := tx.Exec(ctx,
		`INSERT INTO involvement (org_id, user_id, period_start, features_shipped, reviews_done, areas_owned, active, dimensions)
		 VALUES ($1, NULL, $2, 99, 99, 9, true, '{"author_login":"ghost"}'::jsonb)`,
		orgID, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("insert orphan: %v", err)
	}

	members, err := ListInvolvementMembers(ctx, tx, orgID, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), "")
	if err != nil {
		t.Fatalf("ListInvolvementMembers: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("members = %d, want 1 (one card per user, orphan excluded)", len(members))
	}
	m := members[0]
	if m.UserID != userID {
		t.Errorf("userID = %q, want %q", m.UserID, userID)
	}
	if m.Name != "Dev Person" {
		t.Errorf("name = %q, want joined user name", m.Name)
	}
	if m.FeaturesShipped != 7 { // 3 + 4
		t.Errorf("featuresShipped = %d, want 7 (summed across periods)", m.FeaturesShipped)
	}
	if m.ReviewsDone != 3 { // 2 + 1
		t.Errorf("reviewsDone = %d, want 3", m.ReviewsDone)
	}
	if m.AreasOwned != 3 { // MAX(2,3)
		t.Errorf("areasOwned = %d, want 3 (max breadth)", m.AreasOwned)
	}
	if m.CommitCount != 46 { // 20 + 26
		t.Errorf("commitCount = %d, want 46", m.CommitCount)
	}
	if !m.LastActive.Equal(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("lastActive = %v, want 2026-06-01", m.LastActive)
	}
}

// TestReplaceUserInvolvementIdempotentAndPreservesReviews proves the recompute
// path is idempotent despite the NULL-project unique-constraint gap, and that it
// carries a richer reviews_done forward instead of zeroing it.
func TestReplaceUserInvolvementIdempotentAndPreservesReviews(t *testing.T) {
	ctx, tx, orgID := metricsTestTx(t)
	ns := time.Now().UnixNano()

	var userID, projID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO users (email, name) VALUES ($1,'Reviewer') RETURNING id`,
		fmt.Sprintf("rev-%d@acme.dev", ns)).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO projects (org_id, name, key) VALUES ($1,'Proj',$2) RETURNING id`,
		orgID, fmt.Sprintf("P%d", ns%100000)).Scan(&projID); err != nil {
		t.Fatalf("create project: %v", err)
	}

	period := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	// A seeded per-project row with rich reviewer texture (reviews_done = 42).
	if _, err := tx.Exec(ctx,
		`INSERT INTO involvement (org_id, project_id, user_id, period_start, features_shipped, reviews_done, areas_owned, active, dimensions)
		 VALUES ($1,$2,$3,$4,2,42,3,true,'{}'::jsonb)`,
		orgID, projID, userID, period); err != nil {
		t.Fatalf("seed involvement: %v", err)
	}

	uid := userID
	in := InvolvementUpsertInput{
		OrgID:           orgID,
		UserID:          &uid,
		PeriodStart:     period,
		FeaturesShipped: 5,
		ReviewsDone:     0, // recompute has no review signal
		AreasOwned:      4,
		Active:          true,
		Dimensions:      map[string]interface{}{"commit_count": 30},
	}

	// Run the replace twice — must remain a single row (NULL project can't ON CONFLICT).
	for i := 0; i < 2; i++ {
		if err := ReplaceUserInvolvement(ctx, tx, in); err != nil {
			t.Fatalf("ReplaceUserInvolvement #%d: %v", i, err)
		}
	}

	var rows, nullProj, feats, reviews int
	if err := tx.QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE project_id IS NULL),
		        COALESCE(MAX(features_shipped),0), COALESCE(MAX(reviews_done),0)
		 FROM involvement WHERE org_id=$1 AND user_id=$2 AND period_start=$3`,
		orgID, userID, period).Scan(&rows, &nullProj, &feats, &reviews); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rows != 1 {
		t.Fatalf("rows = %d, want 1 (idempotent, single lineage)", rows)
	}
	if nullProj != 1 {
		t.Errorf("null-project rows = %d, want 1 (collapsed to org-level)", nullProj)
	}
	if feats != 5 {
		t.Errorf("features_shipped = %d, want 5 (refreshed from git)", feats)
	}
	if reviews != 42 {
		t.Errorf("reviews_done = %d, want 42 (carried forward, not zeroed)", reviews)
	}
}
