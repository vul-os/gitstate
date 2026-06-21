// Package store — contribution_extras_test.go
// DB-backed tests for the three contribution extensions:
//   - snapshots:  UpsertContributionSnapshot is idempotent (re-upsert overwrites,
//     never duplicates) and dimensions JSON round-trips.
//   - kudos:      InsertKudo + ListKudos + KudosCounts aggregate per recipient.
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

func TestContributionExtras(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping contribution-extras integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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

	ns := time.Now().UnixNano()
	var orgID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("contrib-%d", ns), "Contrib Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org', $1, true)", orgID); err != nil {
		t.Fatalf("set org: %v", err)
	}

	mkUser := func(name string) string {
		var id string
		if err := tx.QueryRow(ctx,
			`INSERT INTO users (email, name) VALUES ($1,$2) RETURNING id`,
			fmt.Sprintf("%s-%d@x.io", name, ns), name).Scan(&id); err != nil {
			t.Fatalf("create user %s: %v", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO org_members (org_id, user_id, role) VALUES ($1,$2,'member')`, orgID, id); err != nil {
			t.Fatalf("member %s: %v", name, err)
		}
		return id
	}
	alice := mkUser("alice")
	bob := mkUser("bob")

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)

	// ── Snapshots: upsert idempotency. ──
	dims := map[string]float64{"shipped": 80, "review": 60, "effort": 70}
	if err := UpsertContributionSnapshot(ctx, tx, orgID, alice, start, end, 75, dims); err != nil {
		t.Fatalf("UpsertContributionSnapshot #1: %v", err)
	}
	// Re-upsert the SAME key with new values → overwrite, not duplicate.
	dims2 := map[string]float64{"shipped": 90, "review": 65, "effort": 72}
	if err := UpsertContributionSnapshot(ctx, tx, orgID, alice, start, end, 82, dims2); err != nil {
		t.Fatalf("UpsertContributionSnapshot #2: %v", err)
	}
	snaps, err := ListContributionSnapshots(ctx, tx, orgID, start)
	if err != nil {
		t.Fatalf("ListContributionSnapshots: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("snapshots = %d, want 1 (idempotent upsert)", len(snaps))
	}
	if snaps[0].Composite != 82 {
		t.Errorf("snapshot composite = %v, want 82 (overwritten)", snaps[0].Composite)
	}
	if snaps[0].Dimensions["shipped"] != 90 {
		t.Errorf("snapshot dim shipped = %v, want 90", snaps[0].Dimensions["shipped"])
	}
	if snaps[0].Name != "alice" {
		t.Errorf("snapshot joined name = %q, want alice", snaps[0].Name)
	}

	// ── Kudos: insert + list + per-recipient counts. ──
	if _, err := InsertKudo(ctx, tx, orgID, alice, bob, "review", "great review"); err != nil {
		t.Fatalf("InsertKudo a→b: %v", err)
	}
	if _, err := InsertKudo(ctx, tx, orgID, alice, bob, "", "and again"); err != nil {
		t.Fatalf("InsertKudo a→b 2: %v", err)
	}
	if _, err := InsertKudo(ctx, tx, orgID, bob, alice, "", "thanks back"); err != nil {
		t.Fatalf("InsertKudo b→a: %v", err)
	}
	all, err := ListKudos(ctx, tx, orgID, "", 50)
	if err != nil {
		t.Fatalf("ListKudos: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("total kudos = %d, want 3", len(all))
	}
	toBob, err := ListKudos(ctx, tx, orgID, bob, 50)
	if err != nil {
		t.Fatalf("ListKudos(bob): %v", err)
	}
	if len(toBob) != 2 {
		t.Errorf("kudos to bob = %d, want 2", len(toBob))
	}
	// Newest first, and joined names populated.
	if toBob[0].FromName != "alice" || toBob[0].ToName != "bob" {
		t.Errorf("kudos joined names = from %q to %q, want alice→bob", toBob[0].FromName, toBob[0].ToName)
	}
	counts, err := KudosCounts(ctx, tx, orgID)
	if err != nil {
		t.Fatalf("KudosCounts: %v", err)
	}
	if counts[bob] != 2 {
		t.Errorf("bob kudos count = %d, want 2", counts[bob])
	}
	if counts[alice] != 1 {
		t.Errorf("alice kudos count = %d, want 1", counts[alice])
	}

	t.Logf("contribution extras OK: 1 snapshot (idempotent), kudos counts bob=%d alice=%d",
		counts[bob], counts[alice])
}
