// Package store — availability_test.go
// DB-backed regression test for UpsertAvailability idempotency.
//
// Bug class (same family as the SET app.current_org / ON CONFLICT-with-no-arbiter
// failures): the availability table has only a NON-UNIQUE index on
// (org_id, user_id, effective_from), so an `ON CONFLICT DO NOTHING` clause has no
// arbiter and never fires — re-PUTing availability on the same effective date
// silently stacked duplicate rows, which GetAvailability then resolved
// nondeterministically via `ORDER BY effective_from DESC LIMIT 1`.
//
// This test re-saves availability for the SAME effective date with a changed
// value and asserts there is still exactly ONE row, holding the latest value.
// It fails on the old code (two rows; latest value not guaranteed).
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

func TestUpsertAvailabilityIdempotentSameDate(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping availability integration test")
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
	var orgID, userID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("avail-%d", ns), "Avail Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org', $1, true)", orgID); err != nil {
		t.Fatalf("set org: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO users (email, name) VALUES ($1,'Member') RETURNING id`,
		fmt.Sprintf("avail-u-%d@x.io", ns)).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}

	day := time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC)

	// First save: 40h, Mon–Fri.
	a1, err := UpsertAvailability(ctx, tx, orgID, userID, 40, []int32{1, 2, 3, 4, 5}, day)
	if err != nil {
		t.Fatalf("UpsertAvailability #1: %v", err)
	}
	if a1.WeeklyHours != 40 {
		t.Fatalf("first save weeklyHours = %v, want 40", a1.WeeklyHours)
	}

	// Second save on the SAME effective date with a different value (user edited
	// their availability again the same day). This must OVERWRITE, not append.
	a2, err := UpsertAvailability(ctx, tx, orgID, userID, 32, []int32{1, 2, 3, 4}, day)
	if err != nil {
		t.Fatalf("UpsertAvailability #2 (same date): %v", err)
	}

	// Exactly one row must exist for this (org, user, effective_from).
	var n int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM availability WHERE org_id=$1 AND user_id=$2 AND effective_from=$3`,
		orgID, userID, day).Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if n != 1 {
		t.Fatalf("availability rows for same effective date = %d, want 1 (re-PUT must upsert, not append duplicates)", n)
	}

	// The surviving row must hold the LATEST value.
	if a2.WeeklyHours != 32 {
		t.Errorf("returned weeklyHours = %v, want 32 (latest)", a2.WeeklyHours)
	}
	if len(a2.WorkingDays) != 4 {
		t.Errorf("returned workingDays len = %d, want 4 (latest)", len(a2.WorkingDays))
	}

	// And GetAvailability (the read path used by capacity) must resolve to the
	// latest value deterministically.
	got, err := GetAvailability(ctx, tx, orgID, userID, day)
	if err != nil {
		t.Fatalf("GetAvailability: %v", err)
	}
	if got.WeeklyHours != 32 {
		t.Errorf("GetAvailability weeklyHours = %v, want 32 (latest)", got.WeeklyHours)
	}

	t.Logf("availability upsert OK: 1 row, weeklyHours=%v", got.WeeklyHours)
}
