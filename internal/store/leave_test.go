// Package store — leave_test.go
// DB-backed test for leave types, balances, and the used-days recompute:
// remaining = entitled + carried − used, with half-day entries counting 0.5 and
// full multi-day entries counting the inclusive day span. Only APPROVED entries
// for the matching type/year contribute.
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

func TestLeaveBalanceRecompute(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping leave integration test")
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
		fmt.Sprintf("leave-%d", ns), "Leave Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org', $1, true)", orgID); err != nil {
		t.Fatalf("set org: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO users (email, name) VALUES ($1,'Member') RETURNING id`,
		fmt.Sprintf("leave-u-%d@x.io", ns)).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Configurable leave type + a balance: entitled 20, carried 5.
	lt, err := CreateLeaveType(ctx, tx, &LeaveType{OrgID: orgID, Name: "Annual", DefaultDays: 20})
	if err != nil {
		t.Fatalf("CreateLeaveType: %v", err)
	}
	bal, err := UpsertLeaveBalance(ctx, tx, orgID, userID, lt.ID, 2026, 20, 5)
	if err != nil {
		t.Fatalf("UpsertLeaveBalance: %v", err)
	}
	if bal.EntitledDays != 20 || bal.CarriedDays != 5 {
		t.Fatalf("balance entitled/carried = %v/%v, want 20/5", bal.EntitledDays, bal.CarriedDays)
	}
	if bal.Remaining() != 25 {
		t.Fatalf("initial remaining = %v, want 25 (no leave taken yet)", bal.Remaining())
	}

	// Approved leave: a 3-day block (Mar 2–4 inclusive = 3 days) + a half-day.
	mkLeave := func(start, end time.Time, half bool, status string) {
		portion := "full"
		if half {
			portion = "am"
		}
		e, err := CreateLeaveEntry(ctx, tx, orgID, userID, "pto", lt.ID, "", start, end, half, portion)
		if err != nil {
			t.Fatalf("CreateLeaveEntry: %v", err)
		}
		if status != "pending" {
			if _, err := ApproveLeaveEntry(ctx, tx, orgID, e.ID, status); err != nil {
				t.Fatalf("ApproveLeaveEntry: %v", err)
			}
		}
	}
	mar2 := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	mar4 := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	mar10 := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	mar20 := time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC)

	mkLeave(mar2, mar4, false, "approved")  // 3 days
	mkLeave(mar10, mar10, true, "approved") // 0.5 day
	mkLeave(mar20, mar20, false, "pending") // pending → must NOT count

	got, err := RecomputeUsedDays(ctx, tx, orgID, userID, lt.ID, 2026)
	if err != nil {
		t.Fatalf("RecomputeUsedDays: %v", err)
	}
	// used = 3 (block) + 0.5 (half) = 3.5; pending excluded.
	if got.UsedDays != 3.5 {
		t.Errorf("used_days = %v, want 3.5 (3-day block + half-day, pending excluded)", got.UsedDays)
	}
	// remaining = entitled(20) + carried(5) − used(3.5) = 21.5.
	if got.Remaining() != 21.5 {
		t.Errorf("remaining = %v, want 21.5", got.Remaining())
	}

	// Approving the pending entry (a single full day) bumps used to 4.5.
	// Re-list and approve the still-pending entry.
	entries, err := ListLeaveEntries(ctx, tx, orgID, userID)
	if err != nil {
		t.Fatalf("ListLeaveEntries: %v", err)
	}
	for _, e := range entries {
		if e.Status == "pending" {
			if _, err := ApproveLeaveEntry(ctx, tx, orgID, e.ID, "approved"); err != nil {
				t.Fatalf("approve pending: %v", err)
			}
		}
	}
	got2, err := RecomputeUsedDays(ctx, tx, orgID, userID, lt.ID, 2026)
	if err != nil {
		t.Fatalf("RecomputeUsedDays #2: %v", err)
	}
	if got2.UsedDays != 4.5 {
		t.Errorf("used_days after approving pending = %v, want 4.5", got2.UsedDays)
	}
	if got2.Remaining() != 20.5 {
		t.Errorf("remaining after approve = %v, want 20.5", got2.Remaining())
	}

	t.Logf("leave OK: used=%v remaining=%v", got2.UsedDays, got2.Remaining())
}

// TestLeaveYearCrossing verifies a Dec→Jan entry is split across the two years:
// each year counts only the days that fall within its own bounds.
func TestLeaveYearCrossing(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping leave integration test")
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
		fmt.Sprintf("leave-yc-%d", ns), "Leave YC Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org', $1, true)", orgID); err != nil {
		t.Fatalf("set org: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO users (email, name) VALUES ($1,'Member') RETURNING id`,
		fmt.Sprintf("leave-yc-u-%d@x.io", ns)).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	lt, err := CreateLeaveType(ctx, tx, &LeaveType{OrgID: orgID, Name: "Annual", DefaultDays: 20})
	if err != nil {
		t.Fatalf("CreateLeaveType: %v", err)
	}

	// Dec 30 2026 → Jan 2 2027 inclusive: 4 calendar days spanning the boundary.
	dec30 := time.Date(2026, 12, 30, 0, 0, 0, 0, time.UTC)
	jan2 := time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC)
	e, err := CreateLeaveEntry(ctx, tx, orgID, userID, "pto", lt.ID, "", dec30, jan2, false, "full")
	if err != nil {
		t.Fatalf("CreateLeaveEntry: %v", err)
	}
	if _, err := ApproveLeaveEntry(ctx, tx, orgID, e.ID, "approved"); err != nil {
		t.Fatalf("ApproveLeaveEntry: %v", err)
	}

	// 2026 should count only Dec 30 + Dec 31 = 2 days.
	got26, err := countApprovedLeaveDays(ctx, tx, orgID, userID, lt.ID, 2026)
	if err != nil {
		t.Fatalf("countApprovedLeaveDays(2026): %v", err)
	}
	if got26 != 2 {
		t.Errorf("2026 days = %v, want 2 (Dec 30–31 only)", got26)
	}
	// 2027 should count only Jan 1 + Jan 2 = 2 days.
	got27, err := countApprovedLeaveDays(ctx, tx, orgID, userID, lt.ID, 2027)
	if err != nil {
		t.Fatalf("countApprovedLeaveDays(2027): %v", err)
	}
	if got27 != 2 {
		t.Errorf("2027 days = %v, want 2 (Jan 1–2 only)", got27)
	}
	// A year the entry doesn't touch counts 0.
	got25, err := countApprovedLeaveDays(ctx, tx, orgID, userID, lt.ID, 2025)
	if err != nil {
		t.Fatalf("countApprovedLeaveDays(2025): %v", err)
	}
	if got25 != 0 {
		t.Errorf("2025 days = %v, want 0", got25)
	}

	t.Logf("leave year-crossing OK: 2026=%v 2027=%v 2025=%v", got26, got27, got25)
}
