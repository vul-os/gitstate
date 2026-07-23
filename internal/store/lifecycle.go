// Package store — lifecycle.go
// Data-access for the billing LIFECYCLE: the dunning state machine, period
// windows, the auto-charge card flag, idempotency guards, refund reversal, and
// org data purge (decisions A7/A8, integrity closures #2/#3/#5/#7/#8/#9/#10).
//
// Design rules (consistent with billing.go):
//   - Every org-scoped write receives a pgx.Tx from db.WithOrg so RLS fires.
//   - The dunning columns live on the subscriptions table (migration 003).
//   - billing_status is the authoritative lifecycle state:
//     active | past_due | suspended | canceled
//     The legacy `status` column is kept in sync for back-compat (active|past_due|canceled;
//     suspended maps to past_due on the legacy column since it predates suspension).
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Billing lifecycle states (closure #7). billing_status authoritative.
const (
	BillingActive    = "active"
	BillingPastDue   = "past_due"
	BillingSuspended = "suspended"
	BillingCanceled  = "canceled"
)

// SubscriptionLifecycle mirrors the dunning/period columns added in migration 003,
// plus the identifying fields needed by the scheduler.
type SubscriptionLifecycle struct {
	OrgID               string
	PlanKey             string
	BillingStatus       string
	DunningAttempts     int
	NextRetryAt         *time.Time
	SuspendedAt         *time.Time
	CanceledAt          *time.Time
	PaymentMethodOnFile bool
	CurrentPeriodStart  *time.Time
	CurrentPeriodEnd    *time.Time
}

// ── Scheduler scans (cross-org; run on the admin/bypass pool) ─────────────────

// ListSubscriptionsDueForBilling returns active subscriptions whose current
// period has ended at or before `now` (closure: "for each org whose period ended").
// Cross-org scan → must run on a BYPASSRLS pool (the scheduler uses the admin pool).
func ListSubscriptionsDueForBilling(ctx context.Context, q pgxQuerier, now time.Time) ([]SubscriptionLifecycle, error) {
	const sql = `
		SELECT org_id, plan_key, billing_status, dunning_attempts, next_retry_at,
		       suspended_at, canceled_at, payment_method_on_file,
		       current_period_start, current_period_end
		FROM subscriptions
		WHERE billing_status = 'active'
		  AND current_period_end IS NOT NULL
		  AND current_period_end <= $1
		ORDER BY current_period_end`
	return scanLifecycles(ctx, q, sql, now)
}

// ListSubscriptionsDueForRetry returns past_due/suspended subscriptions whose
// next_retry_at is due at or before `now` (closure #7 dunning escalation).
// Cross-org scan → admin pool.
func ListSubscriptionsDueForRetry(ctx context.Context, q pgxQuerier, now time.Time) ([]SubscriptionLifecycle, error) {
	const sql = `
		SELECT org_id, plan_key, billing_status, dunning_attempts, next_retry_at,
		       suspended_at, canceled_at, payment_method_on_file,
		       current_period_start, current_period_end
		FROM subscriptions
		WHERE billing_status IN ('past_due','suspended')
		  AND next_retry_at IS NOT NULL
		  AND next_retry_at <= $1
		ORDER BY next_retry_at`
	return scanLifecycles(ctx, q, sql, now)
}

func scanLifecycles(ctx context.Context, q pgxQuerier, sql string, arg time.Time) ([]SubscriptionLifecycle, error) {
	rows, err := q.Query(ctx, sql, arg)
	if err != nil {
		return nil, fmt.Errorf("store.lifecycle: scan subscriptions: %w", err)
	}
	defer rows.Close()
	var out []SubscriptionLifecycle
	for rows.Next() {
		var s SubscriptionLifecycle
		if err := rows.Scan(
			&s.OrgID, &s.PlanKey, &s.BillingStatus, &s.DunningAttempts, &s.NextRetryAt,
			&s.SuspendedAt, &s.CanceledAt, &s.PaymentMethodOnFile,
			&s.CurrentPeriodStart, &s.CurrentPeriodEnd,
		); err != nil {
			return nil, fmt.Errorf("store.lifecycle: scan subscription row: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// pgxQuerier is the read surface shared by *pgxpool.Pool and pgx.Tx.
type pgxQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// ── Single-org lifecycle reads/writes (org-scoped; run inside db.WithOrg) ──────

// GetSubscriptionLifecycle returns the dunning/period state for an org, or ErrNotFound.
// Must run inside db.WithOrg (RLS).
func GetSubscriptionLifecycle(ctx context.Context, tx pgx.Tx, orgID string) (*SubscriptionLifecycle, error) {
	const q = `
		SELECT org_id, plan_key, billing_status, dunning_attempts, next_retry_at,
		       suspended_at, canceled_at, payment_method_on_file,
		       current_period_start, current_period_end
		FROM subscriptions
		WHERE org_id = $1`
	var s SubscriptionLifecycle
	err := tx.QueryRow(ctx, q, orgID).Scan(
		&s.OrgID, &s.PlanKey, &s.BillingStatus, &s.DunningAttempts, &s.NextRetryAt,
		&s.SuspendedAt, &s.CanceledAt, &s.PaymentMethodOnFile,
		&s.CurrentPeriodStart, &s.CurrentPeriodEnd,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store.lifecycle: get subscription lifecycle: %w", err)
	}
	return &s, nil
}

// SetPaymentMethodOnFile flags whether the org has a valid auto-charge card
// (closures #2/#3 gate managed-LLM allowance + overage on this). Org-scoped.
func SetPaymentMethodOnFile(ctx context.Context, tx pgx.Tx, orgID string, onFile bool) error {
	tag, err := tx.Exec(ctx,
		`UPDATE subscriptions SET payment_method_on_file = $2 WHERE org_id = $1`, orgID, onFile)
	if err != nil {
		return fmt.Errorf("store.lifecycle: set payment method: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkBillingSuccess records a successful charge: clears dunning, sets billing_status
// active, and advances the period window to [newPeriodStart, newPeriodEnd].
// Idempotent at the row level — safe to call on an already-active subscription.
// Org-scoped.
func MarkBillingSuccess(ctx context.Context, tx pgx.Tx, orgID string, newPeriodStart, newPeriodEnd time.Time) error {
	const q = `
		UPDATE subscriptions
		SET billing_status       = 'active',
		    status               = 'active',
		    dunning_attempts     = 0,
		    next_retry_at        = NULL,
		    suspended_at         = NULL,
		    canceled_at          = NULL,
		    current_period_start = $2,
		    current_period_end   = $3
		WHERE org_id = $1`
	tag, err := tx.Exec(ctx, q, orgID, newPeriodStart.UTC(), newPeriodEnd.UTC())
	if err != nil {
		return fmt.Errorf("store.lifecycle: mark billing success: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// EnterPastDue transitions a subscription to past_due and schedules the first
// retry (closure #7). attempts is the dunning attempt counter to store (caller
// computes it). Org-scoped.
func EnterPastDue(ctx context.Context, tx pgx.Tx, orgID string, attempts int, nextRetryAt time.Time) error {
	const q = `
		UPDATE subscriptions
		SET billing_status   = 'past_due',
		    status           = 'past_due',
		    dunning_attempts = $2,
		    next_retry_at    = $3
		WHERE org_id = $1`
	tag, err := tx.Exec(ctx, q, orgID, attempts, nextRetryAt.UTC())
	if err != nil {
		return fmt.Errorf("store.lifecycle: enter past_due: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ScheduleNextRetry bumps the dunning attempt counter and reschedules the next
// retry while staying in the current billing_status (closure #7). Org-scoped.
func ScheduleNextRetry(ctx context.Context, tx pgx.Tx, orgID string, attempts int, nextRetryAt time.Time) error {
	const q = `
		UPDATE subscriptions
		SET dunning_attempts = $2,
		    next_retry_at    = $3
		WHERE org_id = $1`
	tag, err := tx.Exec(ctx, q, orgID, attempts, nextRetryAt.UTC())
	if err != nil {
		return fmt.Errorf("store.lifecycle: schedule next retry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Suspend transitions a past_due subscription to suspended (closure #7 day-7).
// Writes/sync are blocked while suspended but data is kept. next_retry_at is kept
// so dunning continues toward cancellation. Org-scoped.
func Suspend(ctx context.Context, tx pgx.Tx, orgID string, suspendedAt time.Time, attempts int, nextRetryAt time.Time) error {
	const q = `
		UPDATE subscriptions
		SET billing_status   = 'suspended',
		    status           = 'past_due',
		    suspended_at     = $2,
		    dunning_attempts = $3,
		    next_retry_at    = $4
		WHERE org_id = $1`
	tag, err := tx.Exec(ctx, q, orgID, suspendedAt.UTC(), attempts, nextRetryAt.UTC())
	if err != nil {
		return fmt.Errorf("store.lifecycle: suspend: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Cancel transitions a subscription to canceled (closure #7 day-14). The PURGE of
// org data is a separate call (PurgeOrgData) the caller makes in the same tx.
// Org-scoped.
func Cancel(ctx context.Context, tx pgx.Tx, orgID string, canceledAt time.Time) error {
	const q = `
		UPDATE subscriptions
		SET billing_status = 'canceled',
		    status         = 'canceled',
		    canceled_at    = $2,
		    next_retry_at  = NULL
		WHERE org_id = $1`
	tag, err := tx.Exec(ctx, q, orgID, canceledAt.UTC())
	if err != nil {
		return fmt.Errorf("store.lifecycle: cancel: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ── Idempotency (closure #8) ──────────────────────────────────────────────────

// InvoiceExistsForPeriod returns true when a non-void invoice already exists for
// (org, periodStart, periodEnd). The billing cycle checks this BEFORE generating a
// new invoice so re-running the scheduler for the same period never double-bills.
// The migration's partial unique index is the backstop. Org-scoped.
func InvoiceExistsForPeriod(ctx context.Context, tx pgx.Tx, orgID string, periodStart, periodEnd time.Time) (bool, error) {
	const q = `
		SELECT EXISTS(
			SELECT 1 FROM invoices
			WHERE org_id = $1 AND period_start = $2 AND period_end = $3 AND status <> 'void'
		)`
	var exists bool
	if err := tx.QueryRow(ctx, q, orgID, periodStart.UTC(), periodEnd.UTC()).Scan(&exists); err != nil {
		return false, fmt.Errorf("store.lifecycle: invoice exists for period: %w", err)
	}
	return exists, nil
}

// GetOpenInvoiceForPeriod returns the most recent non-paid, non-void invoice for a
// period (used by dunning retries to charge the SAME invoice instead of minting a
// new one — closure #8). Returns ErrNotFound when none is outstanding. Org-scoped.
func GetOpenInvoiceForPeriod(ctx context.Context, tx pgx.Tx, orgID string, periodStart, periodEnd time.Time) (*Invoice, error) {
	const q = `
		SELECT id, org_id, status, usd_cents, zar_cents, fx_rate, fx_rate_id::text,
		       period_start, period_end, COALESCE(paystack_ref,''), issued_at, paid_at, created_at
		FROM invoices
		WHERE org_id = $1 AND period_start = $2 AND period_end = $3 AND status IN ('draft','open')
		ORDER BY created_at DESC
		LIMIT 1`
	var inv Invoice
	err := tx.QueryRow(ctx, q, orgID, periodStart.UTC(), periodEnd.UTC()).Scan(
		&inv.ID, &inv.OrgID, &inv.Status, &inv.USDCents,
		&inv.ZARCents, &inv.FXRate, &inv.FXRateID,
		&inv.PeriodStart, &inv.PeriodEnd,
		&inv.PaystackRef, &inv.IssuedAt, &inv.PaidAt, &inv.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store.lifecycle: get open invoice for period: %w", err)
	}
	return &inv, nil
}

// ── Refund / chargeback reversal (closure #9) ─────────────────────────────────

// ReverseInvoice voids a paid/open invoice (a refund or dispute), records a
// reversing payment row, and flags the org's subscription past_due so the next
// cycle re-evaluates. Org-scoped; call inside db.WithOrg.
func ReverseInvoice(ctx context.Context, tx pgx.Tx, orgID, invoiceID, paystackRef string, reason string, now time.Time) error {
	// Void the invoice.
	tag, err := tx.Exec(ctx,
		`UPDATE invoices SET status = 'void' WHERE id = $1 AND org_id = $2`, invoiceID, orgID)
	if err != nil {
		return fmt.Errorf("store.lifecycle: reverse invoice void: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	// Record a reversing (negative) payment for audit. zar_cents stored as the
	// reversed magnitude; status = "reversed" distinguishes it.
	var zar int
	_ = tx.QueryRow(ctx, `SELECT COALESCE(zar_cents,0) FROM invoices WHERE id = $1`, invoiceID).Scan(&zar)
	if _, err := tx.Exec(ctx,
		`INSERT INTO payments (org_id, invoice_id, zar_cents, status, paystack_ref)
		 VALUES ($1, $2, $3, 'reversed', $4)`,
		orgID, invoiceID, -zar, paystackRef); err != nil {
		return fmt.Errorf("store.lifecycle: reverse invoice payment: %w", err)
	}

	// Flag the org: drop the card-on-file gate and put the subscription past_due so
	// managed-LLM/overage are immediately capped (closures #2/#3) and the next cycle
	// re-charges. A first retry is scheduled for `now` so dunning picks it up.
	if _, err := tx.Exec(ctx,
		`UPDATE subscriptions
		    SET billing_status = 'past_due', status = 'past_due',
		        payment_method_on_file = false, next_retry_at = $2, dunning_attempts = 0
		  WHERE org_id = $1`, orgID, now.UTC()); err != nil {
		return fmt.Errorf("store.lifecycle: reverse invoice flag org: %w", err)
	}
	return nil
}

// ── Data purge (closure #7 day-14: cancel + PURGE) ────────────────────────────

// purgeTables is the set of org-scoped data tables emptied on cancellation.
// Order respects FKs (children before parents). The organization row itself and
// its members/subscription/invoices/audit trail are intentionally KEPT — purge
// deletes the *work product* (repos, commits, PRs, issues, contributors, metrics)
// not the billing/account record. repos has ON DELETE CASCADE children
// (commits, pull_requests, issues, task_files, commit_files, …) but we delete the
// org-scoped rows explicitly so the count is auditable and RLS-checked per table.
var purgeTables = []string{
	// derived / analysis first (reference repos/commits/prs)
	"bug_introductions", "author_survival", "commit_files",
	"effort_estimates", "cycle_times", "involvement", "agent_runs",
	"pr_reviews", "deployments", "incidents",
	"contribution_snapshots", "equity_allocations", "kudos",
	"task_files", "issues", "pull_requests", "commits",
	"contributors",
	// finally the repos themselves
	"repos",
}

// PurgeOrgData deletes the org's work product across purgeTables (closure #7).
// Returns the total rows deleted (for test assertions / audit). Org-scoped: must
// run inside db.WithOrg so every DELETE is RLS-checked. It does NOT delete the
// organization, members, subscription, invoices, payments, or audit_log — those
// are the retained account/billing record.
//
// Tables that don't exist in a given deployment are skipped silently (best-effort
// across schema variants); a table that exists but errors aborts the purge.
func PurgeOrgData(ctx context.Context, tx pgx.Tx, orgID string) (int64, error) {
	var total int64
	for _, table := range purgeTables {
		// Guard against schema drift: skip tables that don't exist OR lack an
		// org_id column (some derived tables are keyed only via FK cascade).
		var hasOrgID bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM information_schema.columns
			              WHERE table_schema='public' AND table_name=$1 AND column_name='org_id')`, table).Scan(&hasOrgID); err != nil {
			return total, fmt.Errorf("store.lifecycle: purge probe %s: %w", table, err)
		}
		if !hasOrgID {
			continue
		}
		tag, err := tx.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE org_id = $1`, table), orgID)
		if err != nil {
			return total, fmt.Errorf("store.lifecycle: purge %s: %w", table, err)
		}
		total += tag.RowsAffected()
	}
	return total, nil
}
