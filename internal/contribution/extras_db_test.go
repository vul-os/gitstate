// Package contribution — extras_db_test.go
// DB-backed tests proving the contribution engine + trends are keyed by the
// canonical CONTRIBUTOR (the person), not by userId — so a GROUPED person with no
// linked user still gets a real aggregate (canonical name + contributorId + summed
// facts) and a real trend series. Skips without DATABASE_URL (gitstate_test).
package contribution

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

func contribTestDB(t *testing.T) *db.DB {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping contribution DB test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	database, err := db.New(ctx, &config.Config{Database: config.DatabaseConfig{URL: dbURL}})
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	return database
}

// TestContribByContributor_CanonicalNameAndTrends seeds one PERSON who commits
// under TWO emails (and NO linked user) plus a SECOND person, runs the real engine
// + trends, and asserts:
//   - the grouped person's aggregate carries the CANONICAL display_name, the
//     contributorId, and SUMMED commits across both identities;
//   - ComputeTrends returns a per-CONTRIBUTOR series (keyed by contributorId), and
//     the unlinked grouped person STILL gets a trend with points;
//   - an excluded contributor is absent from both.
func TestContribByContributor_CanonicalNameAndTrends(t *testing.T) {
	database := contribTestDB(t)
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	ns := time.Now().UnixNano()
	var orgID string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("contrib-byc-%d", ns), "Contrib ByContributor Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	defer func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, orgID)
	}()

	// Anchor the commits inside the most recent ~6 month windows so the trend has
	// points. Spread Cameron's two emails across two recent months.
	now := time.Now().UTC()
	m0 := time.Date(now.Year(), now.Month(), 10, 9, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	m1 := m0.AddDate(0, -1, 0)

	var camID, danID, botID string
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var repoID string
		if err := tx.QueryRow(ctx,
			`INSERT INTO repos (org_id, platform, external_id, full_name, default_branch)
			 VALUES ($1,'github',$2,$3,'main') RETURNING id`,
			orgID, fmt.Sprintf("ext-%d", ns), "acme/x").Scan(&repoID); err != nil {
			return err
		}
		commit := func(sha, login, email string, agent bool, at time.Time) error {
			_, err := tx.Exec(ctx,
				`INSERT INTO commits (org_id, repo_id, sha, author_login, author_email, is_agent, committed_at)
				 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
				orgID, repoID, sha, login, email, agent, at)
			return err
		}
		// Cameron commits under TWO emails (same person) in two different months.
		if err := commit(fmt.Sprintf("c1-%d", ns), "cameroncognizance", "cam@a.com", false, m1); err != nil {
			return err
		}
		if err := commit(fmt.Sprintf("c2-%d", ns), "cameroncognizance", "cam@b.com", false, m0); err != nil {
			return err
		}
		if err := commit(fmt.Sprintf("c3-%d", ns), "cameroncognizance", "cam@a.com", false, m0.Add(time.Hour)); err != nil {
			return err
		}
		// Dan, a second person, one commit.
		if err := commit(fmt.Sprintf("d1-%d", ns), "dan", "dan@x.com", false, m0.Add(2*time.Hour)); err != nil {
			return err
		}
		// A bot (excluded by is_bot).
		if err := commit(fmt.Sprintf("b1-%d", ns), "robot", "bot@x.com", true, m0.Add(3*time.Hour)); err != nil {
			return err
		}

		// Canonical contributors — Cameron has CANONICAL name "Cameron Dawson" and
		// BOTH emails + login; NO user link.
		var e error
		if camID, e = store.CreateContributor(ctx, tx, orgID, "Cameron Dawson", "cam@a.com", false); e != nil {
			return e
		}
		for _, v := range []struct{ kind, val string }{
			{"login", "cameroncognizance"}, {"email", "cam@a.com"}, {"email", "cam@b.com"},
		} {
			if e = store.UpsertIdentity(ctx, tx, orgID, camID, v.kind, v.val, "Cameron Dawson"); e != nil {
				return e
			}
		}
		if danID, e = store.CreateContributor(ctx, tx, orgID, "Dan Excluded", "dan@x.com", false); e != nil {
			return e
		}
		_ = store.UpsertIdentity(ctx, tx, orgID, danID, "login", "dan", "Dan")
		_ = store.UpsertIdentity(ctx, tx, orgID, danID, "email", "dan@x.com", "Dan")
		if botID, e = store.CreateContributor(ctx, tx, orgID, "Robot", "bot@x.com", true); e != nil {
			return e
		}
		_ = store.UpsertIdentity(ctx, tx, orgID, botID, "login", "robot", "Robot")
		_ = store.UpsertIdentity(ctx, tx, orgID, botID, "email", "bot@x.com", "Robot")
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	svc := New(database)
	p := Period{From: m1.AddDate(0, -1, 0), To: now}

	rep, err := svc.Compute(ctx, orgID, p)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	// Find Cameron by contributorId — NOT by userId (he has none).
	var cam *Member
	for i := range rep.Members {
		if rep.Members[i].ContributorID == camID {
			cam = &rep.Members[i]
		}
		if rep.Members[i].ContributorID == botID {
			t.Errorf("bot contributor present in members: %+v", rep.Members[i])
		}
	}
	if cam == nil {
		t.Fatalf("Cameron (contributorId=%s) not found in members %+v", camID, rep.Members)
	}
	if cam.Name != "Cameron Dawson" {
		t.Errorf("Cameron name = %q, want canonical %q (not a raw git identity)", cam.Name, "Cameron Dawson")
	}
	if cam.UserID != "" {
		t.Errorf("Cameron has UserID %q, want empty (unlinked person)", cam.UserID)
	}
	// Summed across both emails + 3 commits.
	if got := cam.Raw.HumanCommits; got != 3 {
		t.Errorf("Cameron human commits = %d, want 3 (summed across both emails)", got)
	}

	// Trends: per-contributor series; the UNLINKED grouped person STILL gets one.
	series, err := svc.ComputeTrends(ctx, orgID, 6, IntervalMonth, now)
	if err != nil {
		t.Fatalf("ComputeTrends: %v", err)
	}
	var camSeries *TrendSeries
	for i := range series {
		if series[i].ContributorID == camID {
			camSeries = &series[i]
		}
		if series[i].ContributorID == botID {
			t.Errorf("bot contributor present in trend series")
		}
	}
	if camSeries == nil {
		t.Fatalf("no trend series for unlinked grouped person (contributorId=%s); series=%+v", camID, series)
	}
	if camSeries.Name != "Cameron Dawson" {
		t.Errorf("trend series name = %q, want canonical %q", camSeries.Name, "Cameron Dawson")
	}
	if camSeries.UserID != "" {
		t.Errorf("trend series UserID = %q, want empty (unlinked)", camSeries.UserID)
	}
	// Must have at least one non-zero composite point (the real trend, not empty/fake).
	nonZero := false
	for _, pt := range camSeries.Points {
		if pt.Composite > 0 {
			nonZero = true
		}
	}
	if !nonZero {
		t.Errorf("trend series for grouped person has no non-zero point — trend is empty/fake: %+v", camSeries.Points)
	}

	// The snapshot was persisted per-contributor (contributor_id set, user_id NULL).
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var n int
		if e := tx.QueryRow(ctx,
			`SELECT count(*) FROM contribution_snapshots WHERE org_id=$1 AND contributor_id=$2 AND user_id IS NULL`,
			orgID, camID).Scan(&n); e != nil {
			return e
		}
		if n == 0 {
			t.Errorf("no per-contributor snapshot rows persisted for unlinked person")
		}
		return nil
	}); err != nil {
		t.Fatalf("verify snapshots: %v", err)
	}
}
