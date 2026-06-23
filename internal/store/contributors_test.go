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

// setupContribOrg opens a rolled-back tx with a fresh org + RLS context.
func setupContribOrg(t *testing.T) (context.Context, pgx.Tx, string, func()) {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping contributors DB test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		cancel()
		t.Fatalf("pool: %v", err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		pool.Close()
		cancel()
		t.Fatalf("acquire: %v", err)
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		conn.Release()
		pool.Close()
		cancel()
		t.Fatalf("begin: %v", err)
	}
	var orgID string
	if err := tx.QueryRow(ctx, `INSERT INTO organizations (slug,name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("contrib-test-%d", time.Now().UnixNano()), "Contrib Test").Scan(&orgID); err != nil {
		t.Fatalf("org: %v", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org',$1,true)", orgID); err != nil {
		t.Fatalf("set org: %v", err)
	}
	cleanup := func() {
		_ = tx.Rollback(ctx)
		conn.Release()
		pool.Close()
		cancel()
	}
	return ctx, tx, orgID, cleanup
}

func TestContributors_MergeSplitLink(t *testing.T) {
	ctx, tx, orgID, done := setupContribOrg(t)
	defer done()

	// Two contributors, each with an identity.
	aID, err := CreateContributor(ctx, tx, orgID, "Alice", "alice@x.com", false)
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	bID, err := CreateContributor(ctx, tx, orgID, "Alice Alt", "alice2@y.com", false)
	if err != nil {
		t.Fatalf("create B: %v", err)
	}
	if err := UpsertIdentity(ctx, tx, orgID, aID, "login", "alice", "Alice"); err != nil {
		t.Fatalf("ident A: %v", err)
	}
	if err := UpsertIdentity(ctx, tx, orgID, bID, "email", "alice2@y.com", "Alice"); err != nil {
		t.Fatalf("ident B: %v", err)
	}

	// Merge B into A: B's identity moves to A, B is deleted, A keeps its name.
	if err := MergeContributors(ctx, tx, orgID, bID, aID); err != nil {
		t.Fatalf("merge: %v", err)
	}
	got, err := GetContributor(ctx, tx, orgID, aID)
	if err != nil {
		t.Fatalf("get A: %v", err)
	}
	if len(got.Identities) != 2 {
		t.Errorf("A has %d identities, want 2 after merge", len(got.Identities))
	}
	if got.DisplayName != "Alice" {
		t.Errorf("survivor name = %q, want 'Alice' (manual field kept)", got.DisplayName)
	}
	if _, err := GetContributor(ctx, tx, orgID, bID); err != ErrNotFound {
		t.Errorf("B should be deleted, got %v", err)
	}

	// Split alice2@y.com back into its own contributor.
	newID, err := SplitIdentity(ctx, tx, orgID, "alice2@y.com")
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if newID == aID {
		t.Fatalf("split should create a NEW contributor")
	}
	a2, err := GetContributor(ctx, tx, orgID, aID)
	if err != nil {
		t.Fatalf("get A post-split: %v", err)
	}
	if len(a2.Identities) != 1 {
		t.Errorf("A has %d identities post-split, want 1", len(a2.Identities))
	}
	split, err := GetContributor(ctx, tx, orgID, newID)
	if err != nil {
		t.Fatalf("get split: %v", err)
	}
	if len(split.Identities) != 1 || split.Identities[0].Value != "alice2@y.com" {
		t.Errorf("split contributor identities = %+v", split.Identities)
	}

	// Link A to a user.
	var userID string
	if err := tx.QueryRow(ctx, `INSERT INTO users (email,name) VALUES ($1,'Alice U') RETURNING id`,
		fmt.Sprintf("alice-%d@u.com", time.Now().UnixNano())).Scan(&userID); err != nil {
		t.Fatalf("user: %v", err)
	}
	if err := LinkContributorToUser(ctx, tx, orgID, aID, userID); err != nil {
		t.Fatalf("link: %v", err)
	}
	linked, err := GetContributor(ctx, tx, orgID, aID)
	if err != nil {
		t.Fatalf("get linked: %v", err)
	}
	if linked.UserID != userID {
		t.Errorf("A.UserID = %q, want %q", linked.UserID, userID)
	}
}

func TestContributors_Resolver(t *testing.T) {
	ctx, tx, orgID, done := setupContribOrg(t)
	defer done()

	cID, _ := CreateContributor(ctx, tx, orgID, "Jane", "jane@x.com", false)
	_ = UpsertIdentity(ctx, tx, orgID, cID, "login", "jane", "Jane")
	_ = UpsertIdentity(ctx, tx, orgID, cID, "email", "jane@x.com", "Jane")

	m, err := IdentityToContributor(ctx, tx, orgID)
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	if m["jane"] != cID || m["jane@x.com"] != cID {
		t.Errorf("resolver did not map both identities to %s: %+v", cID, m)
	}
}

// TestContribAgg_CollapsesAndExcludes verifies LoadContributionAggregates groups
// a person's many identities into one (via the contributor resolver) and drops an
// excluded contributor.
func TestContribAgg_CollapsesAndExcludes(t *testing.T) {
	ctx, tx, orgID, done := setupContribOrg(t)
	defer done()

	var repo string
	if err := tx.QueryRow(ctx,
		`INSERT INTO repos (org_id,platform,external_id,full_name) VALUES ($1,'github',$2,'acme/x') RETURNING id`,
		orgID, fmt.Sprintf("ext-%d", time.Now().UnixNano())).Scan(&repo); err != nil {
		t.Fatalf("repo: %v", err)
	}
	from := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	day := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)

	// jane commits under TWO emails (same person); carol once; bot once.
	rows := []struct{ sha, login, email string }{
		{"j1", "jane", "jane@a.com"},
		{"j2", "jane", "jane@b.com"},
		{"c1", "carol", "carol@x.com"},
	}
	for i, c := range rows {
		if _, err := tx.Exec(ctx,
			`INSERT INTO commits (org_id,repo_id,sha,author_login,author_email,is_agent,committed_at)
			 VALUES ($1,$2,$3,$4,$5,false,$6)`,
			orgID, repo, c.sha, c.login, c.email, day.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("commit %s: %v", c.sha, err)
		}
	}

	// Before detection: jane's two emails are two raw idents.
	pre, err := LoadContributionAggregates(ctx, tx, orgID, from, to)
	if err != nil {
		t.Fatalf("pre agg: %v", err)
	}
	preCount := countWithCommits(pre)
	if preCount < 3 {
		t.Fatalf("pre-detection identities = %d, want >=3 (jane x2 + carol)", preCount)
	}

	// Create canonical contributors: jane (2 emails + login), carol.
	janeID, _ := CreateContributor(ctx, tx, orgID, "Jane", "jane@a.com", false)
	_ = UpsertIdentity(ctx, tx, orgID, janeID, "login", "jane", "Jane")
	_ = UpsertIdentity(ctx, tx, orgID, janeID, "email", "jane@a.com", "Jane")
	_ = UpsertIdentity(ctx, tx, orgID, janeID, "email", "jane@b.com", "Jane")
	carolID, _ := CreateContributor(ctx, tx, orgID, "Carol", "carol@x.com", false)
	_ = UpsertIdentity(ctx, tx, orgID, carolID, "login", "carol", "Carol")
	_ = UpsertIdentity(ctx, tx, orgID, carolID, "email", "carol@x.com", "Carol")

	post, err := LoadContributionAggregates(ctx, tx, orgID, from, to)
	if err != nil {
		t.Fatalf("post agg: %v", err)
	}
	// jane's two emails now COUNT AS ONE → 2 contributors with commits (jane, carol).
	if c := countWithCommits(post); c != 2 {
		t.Errorf("post-detection contributors with commits = %d, want 2 (jane merged, carol)", c)
	}
	// jane's merged human commits = 2.
	var janeHuman int
	for _, a := range post {
		if a.HumanCommits == 2 {
			janeHuman = a.HumanCommits
		}
	}
	if janeHuman != 2 {
		t.Errorf("jane merged human commits = %d, want 2", janeHuman)
	}

	// Exclude carol → she drops out.
	excl := true
	if err := UpdateContributor(ctx, tx, orgID, carolID, ContributorUpdate{Excluded: &excl}); err != nil {
		t.Fatalf("exclude: %v", err)
	}
	excluded, err := LoadContributionAggregates(ctx, tx, orgID, from, to)
	if err != nil {
		t.Fatalf("excluded agg: %v", err)
	}
	if c := countWithCommits(excluded); c != 1 {
		t.Errorf("after exclude, contributors with commits = %d, want 1 (only jane)", c)
	}
	for _, a := range excluded {
		if a.Login == "carol" || a.Email == "carol@x.com" {
			t.Errorf("excluded carol still present: %+v", a)
		}
	}
}

func countWithCommits(aggs []ContribAggregate) int {
	n := 0
	for _, a := range aggs {
		if a.HumanCommits+a.AgentCommits > 0 {
			n++
		}
	}
	return n
}
