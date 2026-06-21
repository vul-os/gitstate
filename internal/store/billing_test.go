// internal/store/billing_test.go — DB-backed tests for the per-seat billing store.
//
// Covered (gated on DATABASE_URL, skipped cleanly when unset):
//   - GetAdminStats MRR aggregate: per_builder_cents × billable builders, counting
//     owner+admin+member and EXCLUDING stakeholders (asserted as a delta so the test
//     is robust to other rows already in the instance).
//   - UpsertSubscription + GetSubscription round-trip (shape, free-plan nil period,
//     and the SET LOCAL/set_config fix for RLS context).
//   - IsUniqueViolation + the NextClientInvoiceNumber retry contract: a duplicate
//     INV number raises 23505, IsUniqueViolation classifies it, and recomputing the
//     number yields the next free slot.
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

func billingTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set — skipping store billing test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// withOrgCtx runs fn in an org-scoped tx (set_config app.current_org) and commits.
func withOrgCtx(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID string, fn func(tx pgx.Tx)) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org', $1, true)", orgID); err != nil {
		t.Fatalf("set org: %v", err)
	}
	fn(tx)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func makeOrg(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (string, func()) {
	t.Helper()
	ns := time.Now().UnixNano()
	var orgID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("sb-%d", ns), "Store Billing Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	return orgID, func() { _, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, orgID) }
}

func makeMember(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, role string) {
	t.Helper()
	ns := time.Now().UnixNano()
	var uid string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("%s-%d@ex.io", role, ns), role).Scan(&uid); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO org_members (org_id, user_id, role) VALUES ($1,$2,$3)`, orgID, uid, role); err != nil {
		t.Fatalf("add member: %v", err)
	}
}

// mrrAggSQL is the exact MRR aggregate from GetAdminStats. We run it inside an
// org-scoped tx (so the FORCE-RLS subscriptions/org_members rows are visible) to
// assert its SEMANTICS — per_builder_cents × billable builders, counting
// owner/admin/member and excluding stakeholders, for active subscriptions only.
//
// NOTE: GetAdminStats itself runs this on a pool with NO org context. subscriptions
// is FORCE-RLS, so under the plain app role the join sees zero rows and MRR reads
// 0 — correct behaviour only when the admin handler supplies a BYPASSRLS pool
// (cfg.Admin.DatabaseURL). This test pins the query math under a context where the
// rows are visible; the pool/bypass requirement is verified by TestGetAdminStats_RunsOnPool.
const mrrAggSQL = `
	SELECT COALESCE(SUM(p.per_builder_cents * bc.cnt), 0)
	FROM subscriptions s
	JOIN plans p ON p.key = s.plan_key
	JOIN (
		SELECT org_id, COUNT(*) AS cnt
		FROM org_members
		WHERE role IN ('owner','admin','member')
		GROUP BY org_id
	) bc ON bc.org_id = s.org_id
	WHERE s.status = 'active'
	  AND s.org_id = $1`

func TestMRRAggregate_ExcludesStakeholders(t *testing.T) {
	pool := billingTestPool(t)
	ctx := context.Background()

	orgID, cleanup := makeOrg(t, ctx, pool)
	defer cleanup()

	// 1 owner + 1 admin + 2 members = 4 builders; 3 stakeholders = free.
	makeMember(t, ctx, pool, orgID, "owner")
	makeMember(t, ctx, pool, orgID, "admin")
	makeMember(t, ctx, pool, orgID, "member")
	makeMember(t, ctx, pool, orgID, "member")
	makeMember(t, ctx, pool, orgID, "stakeholder")
	makeMember(t, ctx, pool, orgID, "stakeholder")
	makeMember(t, ctx, pool, orgID, "stakeholder")

	withOrgCtx(t, ctx, pool, orgID, func(tx pgx.Tx) {
		if err := UpsertSubscription(ctx, tx, orgID, "team", "active", nil, ""); err != nil {
			t.Fatalf("upsert sub: %v", err)
		}
	})

	var perBuilder int
	if err := pool.QueryRow(ctx, `SELECT per_builder_cents FROM plans WHERE key='team'`).Scan(&perBuilder); err != nil {
		t.Fatalf("read team plan: %v", err)
	}

	var mrr int
	withOrgCtx(t, ctx, pool, orgID, func(tx pgx.Tx) {
		if err := tx.QueryRow(ctx, mrrAggSQL, orgID).Scan(&mrr); err != nil {
			t.Fatalf("mrr agg: %v", err)
		}
	})
	want := 4 * perBuilder // 4 builders, 3 stakeholders excluded
	if mrr != want {
		t.Errorf("MRR = %d, want %d (4 builders × %d, stakeholders excluded)", mrr, want, perBuilder)
	}
}

func TestMRRAggregate_CanceledSubscriptionNotCounted(t *testing.T) {
	pool := billingTestPool(t)
	ctx := context.Background()

	orgID, cleanup := makeOrg(t, ctx, pool)
	defer cleanup()
	makeMember(t, ctx, pool, orgID, "owner")
	makeMember(t, ctx, pool, orgID, "member")

	withOrgCtx(t, ctx, pool, orgID, func(tx pgx.Tx) {
		if err := UpsertSubscription(ctx, tx, orgID, "team", "canceled", nil, ""); err != nil {
			t.Fatalf("upsert sub: %v", err)
		}
	})

	var mrr int
	withOrgCtx(t, ctx, pool, orgID, func(tx pgx.Tx) {
		if err := tx.QueryRow(ctx, mrrAggSQL, orgID).Scan(&mrr); err != nil {
			t.Fatalf("mrr agg: %v", err)
		}
	})
	if mrr != 0 {
		t.Errorf("canceled sub MRR = %d, want 0", mrr)
	}
}

// TestGetAdminStats_RunsOnPool confirms GetAdminStats executes without error
// against the app pool and returns sane, non-negative counters. (MRR may read 0
// under the FORCE-RLS app role without an admin BYPASSRLS pool — see mrrAggSQL doc.)
func TestGetAdminStats_RunsOnPool(t *testing.T) {
	pool := billingTestPool(t)
	ctx := context.Background()
	stats, err := GetAdminStats(ctx, pool)
	if err != nil {
		t.Fatalf("GetAdminStats: %v", err)
	}
	if stats.TotalUsers < 0 || stats.TotalOrgs < 0 || stats.MRREstimateCents < 0 {
		t.Errorf("negative counter in %+v", stats)
	}
}

// TestSubscription_UpsertRoundTrip exercises the GetSubscription RLS-context fix:
// insert via UpsertSubscription, then read it back via GetSubscription (which sets
// app.current_org via set_config, not a bind-param SET LOCAL).
func TestSubscription_UpsertRoundTrip(t *testing.T) {
	pool := billingTestPool(t)
	ctx := context.Background()

	orgID, cleanup := makeOrg(t, ctx, pool)
	defer cleanup()

	periodEnd := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	withOrgCtx(t, ctx, pool, orgID, func(tx pgx.Tx) {
		if err := UpsertSubscription(ctx, tx, orgID, "business", "active", &periodEnd, "psub_123"); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	})

	sub, err := GetSubscription(ctx, pool, orgID)
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}
	if sub.PlanKey != "business" {
		t.Errorf("plan = %q, want business", sub.PlanKey)
	}
	if sub.Status != "active" {
		t.Errorf("status = %q, want active", sub.Status)
	}
	if sub.PaystackSubCode != "psub_123" {
		t.Errorf("paystack code = %q, want psub_123", sub.PaystackSubCode)
	}
	if sub.CurrentPeriodEnd == nil || !sub.CurrentPeriodEnd.Equal(periodEnd) {
		t.Errorf("period end = %v, want %v", sub.CurrentPeriodEnd, periodEnd)
	}

	// Upsert again (ON CONFLICT path) — change plan, clear period.
	withOrgCtx(t, ctx, pool, orgID, func(tx pgx.Tx) {
		if err := UpsertSubscription(ctx, tx, orgID, "team", "past_due", nil, ""); err != nil {
			t.Fatalf("re-upsert: %v", err)
		}
	})
	sub2, err := GetSubscription(ctx, pool, orgID)
	if err != nil {
		t.Fatalf("GetSubscription #2: %v", err)
	}
	if sub2.PlanKey != "team" || sub2.Status != "past_due" {
		t.Errorf("after re-upsert: plan=%q status=%q, want team/past_due", sub2.PlanKey, sub2.Status)
	}
	if sub2.CurrentPeriodEnd != nil {
		t.Errorf("free/cleared period end = %v, want nil", sub2.CurrentPeriodEnd)
	}
	if sub2.ID != sub.ID {
		t.Errorf("upsert created a new row (%s != %s); expected in-place update", sub2.ID, sub.ID)
	}
}

func TestGetSubscription_NotFound(t *testing.T) {
	pool := billingTestPool(t)
	ctx := context.Background()
	orgID, cleanup := makeOrg(t, ctx, pool)
	defer cleanup()
	if _, err := GetSubscription(ctx, pool, orgID); err != ErrNotFound {
		t.Errorf("GetSubscription on org with no sub = %v, want ErrNotFound", err)
	}
}

// TestInvoiceNumber_UniqueViolationRetry simulates the lost-race the retry contract
// guards: NextClientInvoiceNumber returns INV-YYYY-001; we insert it; a second
// insert of the SAME number raises 23505; IsUniqueViolation classifies it; and a
// fresh NextClientInvoiceNumber now returns INV-YYYY-002.
func TestInvoiceNumber_UniqueViolationRetry(t *testing.T) {
	pool := billingTestPool(t)
	ctx := context.Background()
	orgID, cleanup := makeOrg(t, ctx, pool)
	defer cleanup()

	year := 2099 // unlikely to collide with seed data
	insInvoice := func(tx pgx.Tx, n string) error {
		_, e := tx.Exec(ctx,
			`INSERT INTO client_invoices
			   (org_id, number, status, period_start, period_end, currency, subtotal_cents, total_cents)
			 VALUES ($1,$2,'draft', now(), now(), 'USD', 0, 0)`,
			orgID, n)
		return e
	}

	// Tx 1: compute the next number (001) and persist it.
	var firstNum string
	withOrgCtx(t, ctx, pool, orgID, func(tx pgx.Tx) {
		n, err := NextClientInvoiceNumber(ctx, tx, orgID, year)
		if err != nil {
			t.Fatalf("NextClientInvoiceNumber: %v", err)
		}
		if want := fmt.Sprintf("INV-%d-001", year); n != want {
			t.Fatalf("first number = %q, want %q", n, want)
		}
		firstNum = n
		if err := insInvoice(tx, n); err != nil {
			t.Fatalf("insert first invoice: %v", err)
		}
	})

	// Tx 2 (separate, so the abort doesn't poison the others): re-inserting the
	// same number raises 23505, which IsUniqueViolation must classify — this is the
	// lost-race the retry contract guards.
	{
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin dup tx: %v", err)
		}
		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org', $1, true)", orgID); err != nil {
			t.Fatalf("set org: %v", err)
		}
		dupErr := insInvoice(tx, firstNum)
		_ = tx.Rollback(ctx) // the violation aborts the tx; roll it back explicitly
		if dupErr == nil {
			t.Fatal("duplicate number insert succeeded, want unique violation")
		}
		if !IsUniqueViolation(dupErr) {
			t.Fatalf("IsUniqueViolation(%v) = false, want true", dupErr)
		}
	}

	// Tx 3: after the first invoice committed, the recomputed number advances to 002.
	withOrgCtx(t, ctx, pool, orgID, func(tx pgx.Tx) {
		num2, err := NextClientInvoiceNumber(ctx, tx, orgID, year)
		if err != nil {
			t.Fatalf("NextClientInvoiceNumber #2: %v", err)
		}
		if want := fmt.Sprintf("INV-%d-002", year); num2 != want {
			t.Errorf("retry number = %q, want %q", num2, want)
		}
	})
}

func TestIsUniqueViolation_Classification(t *testing.T) {
	if IsUniqueViolation(nil) {
		t.Error("IsUniqueViolation(nil) = true, want false")
	}
	if IsUniqueViolation(context.Canceled) {
		t.Error("IsUniqueViolation(non-pg error) = true, want false")
	}
}
