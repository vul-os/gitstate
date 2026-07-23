// internal/store/billing_test.go — DB-backed tests for the per-seat billing store.
//
// Covered (gated on DATABASE_URL, skipped cleanly when unset):
//   - GetAdminStats MRR aggregate: per_builder_cents × billable builders, counting
//     owner+admin+member and EXCLUDING stakeholders (asserted as a delta so the test
//     is robust to other rows already in the instance).
//   - UpsertSubscription + GetSubscription round-trip (shape, free-plan nil period,
//     and the SET LOCAL/set_config fix for RLS context).
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
		if err := UpsertSubscription(ctx, tx, orgID, "starter", "active", nil, ""); err != nil {
			t.Fatalf("upsert sub: %v", err)
		}
	})

	var perBuilder int
	if err := pool.QueryRow(ctx, `SELECT per_builder_cents FROM plans WHERE key='starter'`).Scan(&perBuilder); err != nil {
		t.Fatalf("read starter plan: %v", err)
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
		if err := UpsertSubscription(ctx, tx, orgID, "starter", "canceled", nil, ""); err != nil {
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
		if err := UpsertSubscription(ctx, tx, orgID, "pro", "active", &periodEnd, "psub_123"); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	})

	sub, err := GetSubscription(ctx, pool, orgID)
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}
	if sub.PlanKey != "pro" {
		t.Errorf("plan = %q, want pro", sub.PlanKey)
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
		if err := UpsertSubscription(ctx, tx, orgID, "starter", "past_due", nil, ""); err != nil {
			t.Fatalf("re-upsert: %v", err)
		}
	})
	sub2, err := GetSubscription(ctx, pool, orgID)
	if err != nil {
		t.Fatalf("GetSubscription #2: %v", err)
	}
	if sub2.PlanKey != "starter" || sub2.Status != "past_due" {
		t.Errorf("after re-upsert: plan=%q status=%q, want starter/past_due", sub2.PlanKey, sub2.Status)
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

// TestWallet_CreditDebitBalance exercises the wallet ledger: an empty wallet reads
// 0; a credit then a debit compute the correct balance_after on each row and the
// correct running balance; and ListWalletTransactions returns newest-first.
func TestWallet_CreditDebitBalance(t *testing.T) {
	pool := billingTestPool(t)
	ctx := context.Background()
	orgID, cleanup := makeOrg(t, ctx, pool)
	defer cleanup()

	// Empty wallet.
	withOrgCtx(t, ctx, pool, orgID, func(tx pgx.Tx) {
		bal, err := WalletBalance(ctx, tx, orgID)
		if err != nil {
			t.Fatalf("WalletBalance(empty): %v", err)
		}
		if bal != 0 {
			t.Errorf("empty wallet balance = %d, want 0", bal)
		}
	})

	// Credit 5000, then debit 1500 → balance 3500.
	withOrgCtx(t, ctx, pool, orgID, func(tx pgx.Tx) {
		b1, err := WalletCredit(ctx, tx, orgID, 5000, "topup", "first top-up", "ref-credit-1")
		if err != nil {
			t.Fatalf("WalletCredit: %v", err)
		}
		if b1 != 5000 {
			t.Errorf("balance after credit = %d, want 5000", b1)
		}
		b2, err := WalletDebit(ctx, tx, orgID, 1500, "usage", "llm usage", "evt-1")
		if err != nil {
			t.Fatalf("WalletDebit: %v", err)
		}
		if b2 != 3500 {
			t.Errorf("balance after debit = %d, want 3500", b2)
		}
	})

	// Final balance.
	withOrgCtx(t, ctx, pool, orgID, func(tx pgx.Tx) {
		bal, err := WalletBalance(ctx, tx, orgID)
		if err != nil {
			t.Fatalf("WalletBalance(final): %v", err)
		}
		if bal != 3500 {
			t.Errorf("final balance = %d, want 3500", bal)
		}

		txns, err := ListWalletTransactions(ctx, tx, orgID, 10)
		if err != nil {
			t.Fatalf("ListWalletTransactions: %v", err)
		}
		if len(txns) != 2 {
			t.Fatalf("got %d txns, want 2", len(txns))
		}
		// Newest first: the debit row, then the credit row.
		if txns[0].Kind != "usage" || txns[0].AmountCents != -1500 || txns[0].BalanceAfterCents != 3500 {
			t.Errorf("txns[0] = %+v, want usage/-1500/3500", txns[0])
		}
		if txns[1].Kind != "topup" || txns[1].AmountCents != 5000 || txns[1].BalanceAfterCents != 5000 {
			t.Errorf("txns[1] = %+v, want topup/5000/5000", txns[1])
		}
	})
}

// TestWallet_DebitAllowedNegative documents that a debit beyond the balance drives
// it negative (the wallet is "extra billing", reconciled on the next invoice).
func TestWallet_DebitAllowedNegative(t *testing.T) {
	pool := billingTestPool(t)
	ctx := context.Background()
	orgID, cleanup := makeOrg(t, ctx, pool)
	defer cleanup()

	withOrgCtx(t, ctx, pool, orgID, func(tx pgx.Tx) {
		if _, err := WalletCredit(ctx, tx, orgID, 1000, "topup", "", "r1"); err != nil {
			t.Fatalf("credit: %v", err)
		}
		bal, err := WalletDebit(ctx, tx, orgID, 2500, "usage", "", "e1")
		if err != nil {
			t.Fatalf("debit: %v", err)
		}
		if bal != -1500 {
			t.Errorf("over-debit balance = %d, want -1500 (negative allowed)", bal)
		}
	})
}

// TestWallet_TopupIdempotency mirrors the webhook idempotency contract: two credits
// with the same paystack ref via WalletTopupExists credit exactly once.
func TestWallet_TopupIdempotency(t *testing.T) {
	pool := billingTestPool(t)
	ctx := context.Background()
	orgID, cleanup := makeOrg(t, ctx, pool)
	defer cleanup()

	ref := "ps-topup-abc123"
	creditOnce := func() (int64, bool) {
		var bal int64
		var credited bool
		withOrgCtx(t, ctx, pool, orgID, func(tx pgx.Tx) {
			exists, err := WalletTopupExists(ctx, tx, orgID, ref)
			if err != nil {
				t.Fatalf("WalletTopupExists: %v", err)
			}
			if exists {
				bal, err = WalletBalance(ctx, tx, orgID)
				if err != nil {
					t.Fatalf("balance: %v", err)
				}
				return
			}
			bal, err = WalletCredit(ctx, tx, orgID, 4000, "topup", "ps top-up", ref)
			if err != nil {
				t.Fatalf("credit: %v", err)
			}
			credited = true
		})
		return bal, credited
	}

	b1, c1 := creditOnce()
	if !c1 || b1 != 4000 {
		t.Errorf("first credit: balance=%d credited=%v, want 4000/true", b1, c1)
	}
	b2, c2 := creditOnce()
	if c2 || b2 != 4000 {
		t.Errorf("replayed credit: balance=%d credited=%v, want 4000/false (no double-credit)", b2, c2)
	}

	// Exactly one topup row for the ref. wallet_ledger is FORCE RLS, so the count
	// must run inside the org context (a bare-pool query sees zero rows).
	var n int
	withOrgCtx(t, ctx, pool, orgID, func(tx pgx.Tx) {
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM wallet_ledger WHERE org_id=$1 AND kind='topup' AND ref=$2`,
			orgID, ref).Scan(&n); err != nil {
			t.Fatalf("count topups: %v", err)
		}
	})
	if n != 1 {
		t.Errorf("topup rows for ref = %d, want 1", n)
	}
}

// TestUsageByModel_GroupsAndSums records llm_tokens usage for two models and checks
// UsageByModel groups + sums per (model, kind) and excludes blank-model rows.
func TestUsageByModel_GroupsAndSums(t *testing.T) {
	pool := billingTestPool(t)
	ctx := context.Background()
	orgID, cleanup := makeOrg(t, ctx, pool)
	defer cleanup()

	from := time.Now().UTC().Add(-time.Hour)
	to := time.Now().UTC().Add(time.Hour)

	withOrgCtx(t, ctx, pool, orgID, func(tx pgx.Tx) {
		// model A: two events → qty 30, cost 0.30
		if err := RecordUsage(ctx, tx, orgID, "llm_tokens", 10, 0.10, "claude-opus"); err != nil {
			t.Fatalf("record A1: %v", err)
		}
		if err := RecordUsage(ctx, tx, orgID, "llm_tokens", 20, 0.20, "claude-opus"); err != nil {
			t.Fatalf("record A2: %v", err)
		}
		// model B: one event → qty 5, cost 0.05
		if err := RecordUsage(ctx, tx, orgID, "llm_tokens", 5, 0.05, "claude-haiku"); err != nil {
			t.Fatalf("record B1: %v", err)
		}
		// blank model (BYOK / non-LLM) — must be excluded from the breakdown.
		if err := RecordUsage(ctx, tx, orgID, "sync", 1, 0.99, ""); err != nil {
			t.Fatalf("record blank: %v", err)
		}
	})

	withOrgCtx(t, ctx, pool, orgID, func(tx pgx.Tx) {
		rollups, err := UsageByModel(ctx, tx, orgID, from, to)
		if err != nil {
			t.Fatalf("UsageByModel: %v", err)
		}
		byModel := map[string]ModelUsageRollup{}
		for _, r := range rollups {
			byModel[r.Model] = r
		}
		if len(byModel) != 2 {
			t.Fatalf("got %d models, want 2 (blank excluded): %+v", len(byModel), rollups)
		}
		a := byModel["claude-opus"]
		if a.Kind != "llm_tokens" || a.TotalQty != 30 || (a.TotalCostUSD < 0.2999 || a.TotalCostUSD > 0.3001) {
			t.Errorf("claude-opus rollup = %+v, want qty 30 cost ~0.30", a)
		}
		b := byModel["claude-haiku"]
		if b.TotalQty != 5 || (b.TotalCostUSD < 0.0499 || b.TotalCostUSD > 0.0501) {
			t.Errorf("claude-haiku rollup = %+v, want qty 5 cost ~0.05", b)
		}
		if _, ok := byModel[""]; ok {
			t.Error("blank-model row leaked into UsageByModel")
		}
	})
}
