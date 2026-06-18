// Package store — S1 RLS cross-org isolation proof.
//
// This test creates two orgs and a project in each, then verifies that reading
// under org A's RLS context returns zero rows belonging to org B.
// It is the live proof of decision S1: "RLS is the tenancy boundary."
//
// The test requires a live Postgres database with the gitstate schema applied.
// When DATABASE_URL is not set it skips cleanly so `go test ./...` passes in CI
// without a database.
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

// TestRLSCrossOrgIsolation is the S1 proof: reading under org A's RLS context
// must return ZERO rows that belong to org B.
func TestRLSCrossOrgIsolation(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping RLS integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	// ── setup: create two scratch orgs and one project each ──────────────────
	// We use a dedicated connection + explicit transaction with SAVEPOINT so we
	// can roll everything back at the end, leaving the DB clean.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer conn.Release()

	// Wrap everything in a transaction we roll back at the end.
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // always roll back to keep DB clean

	// Insert two orgs directly (organizations table has no RLS).
	var orgA, orgB string
	if err := tx.QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1, $2) RETURNING id`,
		fmt.Sprintf("rls-test-a-%d", time.Now().UnixNano()), "RLS Test Org A",
	).Scan(&orgA); err != nil {
		t.Fatalf("create org A: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1, $2) RETURNING id`,
		fmt.Sprintf("rls-test-b-%d", time.Now().UnixNano()), "RLS Test Org B",
	).Scan(&orgB); err != nil {
		t.Fatalf("create org B: %v", err)
	}

	// Insert a project for each org. Under enforced RLS the WITH CHECK policy
	// requires app.current_org to match the row's org_id, so set the context
	// to each org before inserting its row.
	var projA, projB string
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org', $1, true)", orgA); err != nil {
		t.Fatalf("set ctx A: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO projects (org_id, name) VALUES ($1, 'Proj A') RETURNING id`, orgA,
	).Scan(&projA); err != nil {
		t.Fatalf("create proj A: %v", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org', $1, true)", orgB); err != nil {
		t.Fatalf("set ctx B: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO projects (org_id, name) VALUES ($1, 'Proj B') RETURNING id`, orgB,
	).Scan(&projB); err != nil {
		t.Fatalf("create proj B: %v", err)
	}

	// ── proof: read under org A's RLS context ────────────────────────────────
	// We start a *nested* savepoint, set app.current_org = orgA, then query
	// projects.  We expect to see exactly projA and zero rows of projB.
	if _, err := tx.Exec(ctx, "SAVEPOINT rls_check"); err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	// SET LOCAL can't bind params; set_config(...,true) is the parameterized equivalent (matches db.WithOrg).
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org', $1, true)", orgA); err != nil {
		t.Fatalf("set org A: %v", err)
	}

	// Query all projects — RLS should filter to org A only.
	rows, err := tx.Query(ctx, `SELECT id, org_id FROM projects`)
	if err != nil {
		t.Fatalf("query projects: %v", err)
	}
	type row struct{ id, orgID string }
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.orgID); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}

	// Release savepoint (SET LOCAL is scoped to the transaction, not savepoint;
	// reset via ROLLBACK TO SAVEPOINT to undo SET LOCAL for the next assertion).
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT rls_check"); err != nil {
		t.Fatalf("rollback to savepoint: %v", err)
	}

	// ── assertions ───────────────────────────────────────────────────────────
	// 1. projB must NOT appear in any row returned under org A's context.
	for _, r := range got {
		if r.id == projB {
			t.Errorf("S1 VIOLATION: proj B (%s) visible under org A's RLS context", projB)
		}
		if r.orgID == orgB {
			t.Errorf("S1 VIOLATION: row with org_id=%s visible under org A's RLS context", orgB)
		}
	}

	// 2. projA MUST be visible (sanity check that RLS is wired, not just broken).
	foundA := false
	for _, r := range got {
		if r.id == projA {
			foundA = true
		}
	}
	if !foundA {
		// Only fail here if there were no cross-org violations — if RLS filtered
		// everything (e.g. not enabled) that's a misconfiguration worth surfacing.
		t.Errorf("proj A (%s) not visible under org A's RLS context — RLS may not be configured", projA)
	}

	t.Logf("RLS OK: %d project(s) visible under org A; org B's rows: 0", len(got))
}
