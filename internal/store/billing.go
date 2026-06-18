// Package store — billing.go
// Data-access queries for billing tables: plans, subscriptions, usage_events,
// invoices, invoice_lines, payments, and paystack_events (decisions A7/A8/P4/P6).
//
// Design rules applied here:
//   - All org-scoped writes receive a pgx.Tx (from db.WithOrg) so RLS fires.
//   - Reads that need no org scope (plans, paystack_events) use *pgxpool.Pool directly.
//   - evidence jsonb on invoice_lines is map[string]any; marshalled to/from JSON here.
//   - is_estimated = true lines carry an explicit human-confirmation note (decisions P4).
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ── Domain types ─────────────────────────────────────────────────────────────

// Plan mirrors a row from the plans table.
type Plan struct {
	Key              string
	Name             string
	USDCents         int     // legacy flat price (0 for per-builder tiers; kept for back-compat)
	PerBuilderCents  int     // monthly price per billable builder
	IncludedLLMCents int     // included managed-LLM allowance per builder/mo (our provider cost)
	OverageMarkup    float64 // markup on managed-LLM usage beyond the allowance (e.g. 1.30)
	Builders         int     // cap: 0 = unlimited
	MaxConns         int
	Features         map[string]any
}

// Subscription mirrors a row from the subscriptions table.
type Subscription struct {
	ID              string
	OrgID           string
	PlanKey         string
	Status          string     // active | past_due | canceled
	CurrentPeriodEnd *time.Time
	PaystackSubCode  string
	CreatedAt       time.Time
}

// UsageRollup is a single kind-aggregated result from SumUsage.
type UsageRollup struct {
	Kind        string
	TotalQty    float64
	TotalCostUSD float64
}

// Invoice mirrors a row from the invoices table.
type Invoice struct {
	ID          string
	OrgID       string
	Status      string // draft | open | paid | void
	USDCents    int
	ZARCents    *int
	FXRate      *float64
	FXRateID    *string
	PeriodStart *time.Time
	PeriodEnd   *time.Time
	PaystackRef string
	IssuedAt    *time.Time
	PaidAt      *time.Time
	CreatedAt   time.Time
}

// InvoiceLine mirrors a row from the invoice_lines table.
type InvoiceLine struct {
	ID          string
	InvoiceID   string
	Description string
	USDCents    int
	Evidence    map[string]any // jsonb; git-backed or gap-flagged (decisions P4)
	IsEstimated bool
}

// ── Plans ────────────────────────────────────────────────────────────────────

// ListPlans returns all plans ordered by usd_cents ascending.
// Plans are global (no org scope) — uses pool directly.
func ListPlans(ctx context.Context, pool *pgxpool.Pool) ([]Plan, error) {
	const q = `
		SELECT key, name, usd_cents, per_builder_cents, included_llm_cents, overage_markup,
		       builders, max_conns, features
		FROM plans
		ORDER BY per_builder_cents ASC, usd_cents ASC`

	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store.billing: list plans: %w", err)
	}
	defer rows.Close()

	var out []Plan
	for rows.Next() {
		var p Plan
		var featuresJSON []byte
		if err := rows.Scan(
			&p.Key, &p.Name, &p.USDCents, &p.PerBuilderCents, &p.IncludedLLMCents,
			&p.OverageMarkup, &p.Builders, &p.MaxConns, &featuresJSON,
		); err != nil {
			return nil, fmt.Errorf("store.billing: scan plan: %w", err)
		}
		if len(featuresJSON) > 0 {
			if err := json.Unmarshal(featuresJSON, &p.Features); err != nil {
				return nil, fmt.Errorf("store.billing: unmarshal plan features: %w", err)
			}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ── Subscriptions ────────────────────────────────────────────────────────────

// GetSubscription returns the active subscription for an org, or ErrNotFound.
// subscriptions is org-scoped (RLS); the caller must run this inside db.WithOrg
// — but because we need to run this outside a tx for handler convenience we accept
// a *pgxpool.Pool and apply org scope manually via SET LOCAL in a short tx.
// The inner SET LOCAL is safe because we only read here.
func GetSubscription(ctx context.Context, pool *pgxpool.Pool, orgID string) (*Subscription, error) {
	const q = `
		SELECT id, org_id, plan_key, status,
		       current_period_end, COALESCE(paystack_sub_code,''), created_at
		FROM subscriptions
		WHERE org_id = $1`

	// Run in a short org-scoped tx so RLS fires.
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store.billing: get subscription: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SET LOCAL app.current_org = $1", orgID); err != nil {
		return nil, fmt.Errorf("store.billing: get subscription: set org: %w", err)
	}

	var s Subscription
	err = tx.QueryRow(ctx, q, orgID).Scan(
		&s.ID, &s.OrgID, &s.PlanKey, &s.Status,
		&s.CurrentPeriodEnd, &s.PaystackSubCode, &s.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store.billing: get subscription: %w", err)
	}
	_ = tx.Commit(ctx)
	return &s, nil
}

// UpsertSubscription inserts or updates a subscription for an org.
// Must be called inside db.WithOrg (tx already has the RLS context set).
// periodEnd may be nil for the free plan.
func UpsertSubscription(ctx context.Context, tx pgx.Tx, orgID, planKey, status string, periodEnd *time.Time, paystackSubCode string) error {
	const q = `
		INSERT INTO subscriptions (org_id, plan_key, status, current_period_end, paystack_sub_code)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (org_id) DO UPDATE SET
			plan_key             = EXCLUDED.plan_key,
			status               = EXCLUDED.status,
			current_period_end   = EXCLUDED.current_period_end,
			paystack_sub_code    = EXCLUDED.paystack_sub_code`

	if _, err := tx.Exec(ctx, q, orgID, planKey, status, periodEnd, paystackSubCode); err != nil {
		return fmt.Errorf("store.billing: upsert subscription: %w", err)
	}
	return nil
}

// ── Usage events ─────────────────────────────────────────────────────────────

// RecordUsage appends a usage_event row for the org.
// Must be called inside db.WithOrg (tx already has the RLS context set).
// kind examples: "builder_seat", "llm_tokens", "sync".
func RecordUsage(ctx context.Context, tx pgx.Tx, orgID, kind string, qty, costUSD float64) error {
	const q = `
		INSERT INTO usage_events (org_id, kind, quantity, cost_usd)
		VALUES ($1, $2, $3, $4)`

	if _, err := tx.Exec(ctx, q, orgID, kind, qty, costUSD); err != nil {
		return fmt.Errorf("store.billing: record usage: %w", err)
	}
	return nil
}

// SumUsage returns per-kind aggregated usage for an org within [from, to].
// Must be called inside db.WithOrg (tx already has the RLS context set).
func SumUsage(ctx context.Context, tx pgx.Tx, orgID string, from, to time.Time) ([]UsageRollup, error) {
	const q = `
		SELECT kind, SUM(quantity)::float8, SUM(cost_usd)::float8
		FROM usage_events
		WHERE org_id = $1
		  AND occurred_at >= $2
		  AND occurred_at <= $3
		GROUP BY kind
		ORDER BY kind`

	rows, err := tx.Query(ctx, q, orgID, from, to)
	if err != nil {
		return nil, fmt.Errorf("store.billing: sum usage: %w", err)
	}
	defer rows.Close()

	var out []UsageRollup
	for rows.Next() {
		var r UsageRollup
		if err := rows.Scan(&r.Kind, &r.TotalQty, &r.TotalCostUSD); err != nil {
			return nil, fmt.Errorf("store.billing: scan usage rollup: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── Invoices ─────────────────────────────────────────────────────────────────

// CreateInvoice inserts a draft invoice for an org.
// Must be called inside db.WithOrg (tx already has the RLS context set).
// Returns the new invoice with its generated ID.
func CreateInvoice(ctx context.Context, tx pgx.Tx, orgID string, usdCents int, periodStart, periodEnd time.Time) (*Invoice, error) {
	const q = `
		INSERT INTO invoices (org_id, status, usd_cents, period_start, period_end)
		VALUES ($1, 'draft', $2, $3, $4)
		RETURNING id, org_id, status, usd_cents, zar_cents, fx_rate, fx_rate_id::text,
		          period_start, period_end, COALESCE(paystack_ref,''), issued_at, paid_at, created_at`

	var inv Invoice
	err := tx.QueryRow(ctx, q, orgID, usdCents, periodStart.UTC(), periodEnd.UTC()).Scan(
		&inv.ID, &inv.OrgID, &inv.Status, &inv.USDCents,
		&inv.ZARCents, &inv.FXRate, &inv.FXRateID,
		&inv.PeriodStart, &inv.PeriodEnd,
		&inv.PaystackRef, &inv.IssuedAt, &inv.PaidAt, &inv.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("store.billing: create invoice: %w", err)
	}
	return &inv, nil
}

// AddInvoiceLine appends a line item to an invoice.
// Must be called inside db.WithOrg (tx already has the RLS context set).
// evidence is stored as jsonb; for is_estimated=true lines the caller should
// include a "confirmation_required" key in evidence to surface the gap (decisions P4).
func AddInvoiceLine(ctx context.Context, tx pgx.Tx, invoiceID, desc string, usdCents int, evidence map[string]any, isEstimated bool) error {
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		return fmt.Errorf("store.billing: marshal evidence: %w", err)
	}

	const q = `
		INSERT INTO invoice_lines (invoice_id, description, usd_cents, evidence, is_estimated)
		VALUES ($1, $2, $3, $4, $5)`

	if _, err := tx.Exec(ctx, q, invoiceID, desc, usdCents, evidenceJSON, isEstimated); err != nil {
		return fmt.Errorf("store.billing: add invoice line: %w", err)
	}
	return nil
}

// SetInvoiceCharge records the ZAR charge details (FX conversion) on an invoice
// and transitions its status from draft to open (ready for payment).
// Must be called inside db.WithOrg (tx already has the RLS context set).
func SetInvoiceCharge(ctx context.Context, tx pgx.Tx, invoiceID string, zarCents int, fxRate float64, fxRateID, paystackRef string) error {
	const q = `
		UPDATE invoices
		SET status       = 'open',
		    zar_cents    = $2,
		    fx_rate      = $3,
		    fx_rate_id   = $4::uuid,
		    paystack_ref = $5,
		    issued_at    = now()
		WHERE id = $1`

	tag, err := tx.Exec(ctx, q, invoiceID, zarCents, fxRate, fxRateID, paystackRef)
	if err != nil {
		return fmt.Errorf("store.billing: set invoice charge: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkInvoicePaid transitions an invoice to the "paid" state.
// Must be called inside db.WithOrg (tx already has the RLS context set).
func MarkInvoicePaid(ctx context.Context, tx pgx.Tx, invoiceID string, paidAt time.Time) error {
	const q = `
		UPDATE invoices
		SET status  = 'paid',
		    paid_at = $2
		WHERE id = $1`

	tag, err := tx.Exec(ctx, q, invoiceID, paidAt.UTC())
	if err != nil {
		return fmt.Errorf("store.billing: mark invoice paid: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListInvoices returns all invoices for an org, newest first.
// Must be called inside db.WithOrg (tx already has the RLS context set).
func ListInvoices(ctx context.Context, tx pgx.Tx, orgID string) ([]Invoice, error) {
	const q = `
		SELECT id, org_id, status, usd_cents, zar_cents, fx_rate, fx_rate_id::text,
		       period_start, period_end, COALESCE(paystack_ref,''), issued_at, paid_at, created_at
		FROM invoices
		WHERE org_id = $1
		ORDER BY created_at DESC`

	rows, err := tx.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("store.billing: list invoices: %w", err)
	}
	defer rows.Close()

	var out []Invoice
	for rows.Next() {
		var inv Invoice
		if err := rows.Scan(
			&inv.ID, &inv.OrgID, &inv.Status, &inv.USDCents,
			&inv.ZARCents, &inv.FXRate, &inv.FXRateID,
			&inv.PeriodStart, &inv.PeriodEnd,
			&inv.PaystackRef, &inv.IssuedAt, &inv.PaidAt, &inv.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("store.billing: scan invoice: %w", err)
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// GetInvoice fetches a single invoice and all its line items.
// Must be called inside db.WithOrg (tx already has the RLS context set).
// Returns ErrNotFound if the invoice doesn't exist (or doesn't belong to the org).
func GetInvoice(ctx context.Context, tx pgx.Tx, orgID, id string) (*Invoice, []InvoiceLine, error) {
	const invQ = `
		SELECT id, org_id, status, usd_cents, zar_cents, fx_rate, fx_rate_id::text,
		       period_start, period_end, COALESCE(paystack_ref,''), issued_at, paid_at, created_at
		FROM invoices
		WHERE id = $1 AND org_id = $2`

	var inv Invoice
	err := tx.QueryRow(ctx, invQ, id, orgID).Scan(
		&inv.ID, &inv.OrgID, &inv.Status, &inv.USDCents,
		&inv.ZARCents, &inv.FXRate, &inv.FXRateID,
		&inv.PeriodStart, &inv.PeriodEnd,
		&inv.PaystackRef, &inv.IssuedAt, &inv.PaidAt, &inv.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("store.billing: get invoice: %w", err)
	}

	const lineQ = `
		SELECT id, invoice_id, description, usd_cents, evidence, is_estimated
		FROM invoice_lines
		WHERE invoice_id = $1
		ORDER BY id`

	rows, err := tx.Query(ctx, lineQ, id)
	if err != nil {
		return nil, nil, fmt.Errorf("store.billing: get invoice lines: %w", err)
	}
	defer rows.Close()

	var lines []InvoiceLine
	for rows.Next() {
		var l InvoiceLine
		var evidenceJSON []byte
		if err := rows.Scan(&l.ID, &l.InvoiceID, &l.Description, &l.USDCents, &evidenceJSON, &l.IsEstimated); err != nil {
			return nil, nil, fmt.Errorf("store.billing: scan invoice line: %w", err)
		}
		if len(evidenceJSON) > 0 {
			if err := json.Unmarshal(evidenceJSON, &l.Evidence); err != nil {
				return nil, nil, fmt.Errorf("store.billing: unmarshal line evidence: %w", err)
			}
		}
		lines = append(lines, l)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("store.billing: iterate invoice lines: %w", err)
	}

	return &inv, lines, nil
}

// ── Payments ──────────────────────────────────────────────────────────────────

// RecordPayment inserts a payment record for an org.
// Must be called inside db.WithOrg (tx already has the RLS context set).
func RecordPayment(ctx context.Context, tx pgx.Tx, orgID, invoiceID string, zarCents int, status, paystackRef string) error {
	const q = `
		INSERT INTO payments (org_id, invoice_id, zar_cents, status, paystack_ref)
		VALUES ($1, $2, $3, $4, $5)`

	if _, err := tx.Exec(ctx, q, orgID, invoiceID, zarCents, status, paystackRef); err != nil {
		return fmt.Errorf("store.billing: record payment: %w", err)
	}
	return nil
}

// ── Git activity helpers for invoice generation ───────────────────────────────

// CommitSummary is a lightweight commit record used for billing evidence.
type CommitSummary struct {
	AuthorLogin string
	AuthorEmail string
}

// PRSummary is a lightweight PR record used for billing evidence.
type PRSummary struct {
	AuthorLogin string
}

// ListCommitsInPeriod returns lightweight commit rows for an org within [from, to].
// Must be called inside db.WithOrg (tx already has the RLS context set).
func ListCommitsInPeriod(ctx context.Context, tx pgx.Tx, orgID string, from, to time.Time) ([]CommitSummary, error) {
	const q = `
		SELECT COALESCE(author_login,''), COALESCE(author_email,'')
		FROM commits
		WHERE org_id = $1
		  AND committed_at >= $2
		  AND committed_at <= $3`

	rows, err := tx.Query(ctx, q, orgID, from, to)
	if err != nil {
		return nil, fmt.Errorf("store.billing: list commits in period: %w", err)
	}
	defer rows.Close()

	var out []CommitSummary
	for rows.Next() {
		var c CommitSummary
		if err := rows.Scan(&c.AuthorLogin, &c.AuthorEmail); err != nil {
			return nil, fmt.Errorf("store.billing: scan commit summary: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListPRsInPeriod returns lightweight PR rows for an org within [from, to].
// Must be called inside db.WithOrg (tx already has the RLS context set).
func ListPRsInPeriod(ctx context.Context, tx pgx.Tx, orgID string, from, to time.Time) ([]PRSummary, error) {
	const q = `
		SELECT COALESCE(author_login,'')
		FROM pull_requests
		WHERE org_id = $1
		  AND created_at >= $2
		  AND created_at <= $3`

	rows, err := tx.Query(ctx, q, orgID, from, to)
	if err != nil {
		return nil, fmt.Errorf("store.billing: list prs in period: %w", err)
	}
	defer rows.Close()

	var out []PRSummary
	for rows.Next() {
		var p PRSummary
		if err := rows.Scan(&p.AuthorLogin); err != nil {
			return nil, fmt.Errorf("store.billing: scan pr summary: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ── Paystack event idempotency ────────────────────────────────────────────────

// IsPaystackEventProcessed returns true when the Paystack event ID is already in
// the paystack_events table. paystack_events is NOT org-scoped (no RLS) — uses pool.
func IsPaystackEventProcessed(ctx context.Context, pool *pgxpool.Pool, eventID string) (bool, error) {
	const q = `SELECT EXISTS(SELECT 1 FROM paystack_events WHERE id = $1)`
	var exists bool
	if err := pool.QueryRow(ctx, q, eventID).Scan(&exists); err != nil {
		return false, fmt.Errorf("store.billing: check paystack event: %w", err)
	}
	return exists, nil
}

// RecordPaystackEvent stores a Paystack webhook event for idempotency (decisions S4).
// paystack_events is NOT org-scoped — uses pool directly.
func RecordPaystackEvent(ctx context.Context, pool *pgxpool.Pool, eventID, eventType string, payload []byte) error {
	const q = `
		INSERT INTO paystack_events (id, type, payload)
		VALUES ($1, $2, $3)
		ON CONFLICT (id) DO NOTHING`

	if _, err := pool.Exec(ctx, q, eventID, eventType, payload); err != nil {
		return fmt.Errorf("store.billing: record paystack event: %w", err)
	}
	return nil
}
