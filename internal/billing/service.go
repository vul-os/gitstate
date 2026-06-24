// Package billing provides the core billing service for gitstate.
// It implements plan management, subscription lifecycle, usage metering,
// and invoice generation backed by git evidence (decisions P4/P6/A7/A8).
//
// Design rules:
//   - USD is the billing anchor; ZAR conversion happens at charge time via exchange.Service.
//   - Only org_members with role = 'owner' | 'admin' | 'member' count as builders.
//     role = 'stakeholder' is always free (decisions P6).
//   - Invoice lines backed by git evidence carry the evidence jsonb.
//     Work git can't see (meetings, research) appears as is_estimated=true lines
//     with a "confirmation_required" flag in evidence so humans can review (decisions P4).
//   - max_conns on a plan is a CEILING (not a reservation) — Service enforces it
//     by refusing connections above the ceiling, not by pre-allocating.
package billing

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/store"
)

// builderRoles is the set of org_member roles that count toward builder seat billing.
// stakeholder is explicitly excluded (decisions P6).
var builderRoles = map[string]bool{
	"owner":  true,
	"admin":  true,
	"member": true,
}

// Service is the core billing service. It coordinates plan lookups,
// subscription writes, usage recording, and invoice generation.
// It does NOT perform actual Paystack charging — that lives in ee/billing.
type Service struct {
	db  *db.DB
	cfg *config.Config
}

// New creates a new billing Service.
func New(database *db.DB, cfg *config.Config) *Service {
	return &Service{db: database, cfg: cfg}
}

// ── Plan helpers ──────────────────────────────────────────────────────────────

// ListPlans returns all available plans from the database.
func (s *Service) ListPlans(ctx context.Context) ([]store.Plan, error) {
	plans, err := store.ListPlans(ctx, s.db.Pool())
	if err != nil {
		return nil, fmt.Errorf("billing: list plans: %w", err)
	}
	return plans, nil
}

// GetPlan fetches a single plan by key, or store.ErrNotFound.
func (s *Service) GetPlan(ctx context.Context, key string) (*store.Plan, error) {
	plans, err := store.ListPlans(ctx, s.db.Pool())
	if err != nil {
		return nil, fmt.Errorf("billing: get plan: %w", err)
	}
	for _, p := range plans {
		if p.Key == key {
			pp := p
			return &pp, nil
		}
	}
	return nil, store.ErrNotFound
}

// PlanCeiling returns the max_conns ceiling for the org's current plan.
// Returns 0 if the org has no subscription (defaults to free plan ceiling).
// max_conns is a CEILING not a reservation (decisions A8 note / plan table comment).
func (s *Service) PlanCeiling(ctx context.Context, orgID string) (int, error) {
	sub, err := store.GetSubscription(ctx, s.db.Pool(), orgID)
	if err != nil && err != store.ErrNotFound {
		return 0, fmt.Errorf("billing: plan ceiling: %w", err)
	}

	planKey := "free"
	if sub != nil {
		planKey = sub.PlanKey
	}

	plan, err := s.GetPlan(ctx, planKey)
	if err != nil {
		return 0, fmt.Errorf("billing: plan ceiling for %q: %w", planKey, err)
	}
	return plan.MaxConns, nil
}

// WithinCeiling returns true when connCount <= the plan's max_conns ceiling.
// Always returns true for the "ent" plan (no ceiling: max_conns = 0 sentinel).
func (s *Service) WithinCeiling(ctx context.Context, orgID string, connCount int) (bool, error) {
	ceiling, err := s.PlanCeiling(ctx, orgID)
	if err != nil {
		return false, err
	}
	if ceiling == 0 {
		// Enterprise / unlimited
		return true, nil
	}
	return connCount <= ceiling, nil
}

// ── Subscription ──────────────────────────────────────────────────────────────

// GetSubscription fetches the current subscription for an org.
func (s *Service) GetSubscription(ctx context.Context, orgID string) (*store.Subscription, error) {
	sub, err := store.GetSubscription(ctx, s.db.Pool(), orgID)
	if err != nil {
		return nil, fmt.Errorf("billing: get subscription: %w", err)
	}
	return sub, nil
}

// ── Usage ─────────────────────────────────────────────────────────────────────

// CurrentUsage returns a realtime rollup of usage events for the current
// billing period (last 30 days as a fallback when no subscription period exists).
func (s *Service) CurrentUsage(ctx context.Context, orgID string) ([]store.UsageRollup, error) {
	// Determine the period: from the subscription's current_period start or 30 days ago.
	now := time.Now().UTC()
	from := now.AddDate(0, 0, -30)

	sub, err := store.GetSubscription(ctx, s.db.Pool(), orgID)
	if err != nil && err != store.ErrNotFound {
		return nil, fmt.Errorf("billing: current usage: get subscription: %w", err)
	}
	if sub != nil && sub.CurrentPeriodEnd != nil {
		// Approximate period start as ~30 days before period end.
		approxStart := sub.CurrentPeriodEnd.AddDate(0, -1, 0)
		if approxStart.Before(now) {
			from = approxStart
		}
	}

	var rollups []store.UsageRollup
	if err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		rollups, err = store.SumUsage(ctx, tx, orgID, from, now)
		return err
	}); err != nil {
		return nil, fmt.Errorf("billing: current usage: %w", err)
	}
	return rollups, nil
}

// CurrentUsageByModel returns the per-model managed-LLM usage breakdown for the
// current billing period (same window as CurrentUsage). Each entry is a
// (model, kind) rollup with summed tokens and provider cost.
func (s *Service) CurrentUsageByModel(ctx context.Context, orgID string) ([]store.ModelUsageRollup, error) {
	now := time.Now().UTC()
	from := now.AddDate(0, 0, -30)

	sub, err := store.GetSubscription(ctx, s.db.Pool(), orgID)
	if err != nil && err != store.ErrNotFound {
		return nil, fmt.Errorf("billing: usage by model: get subscription: %w", err)
	}
	if sub != nil && sub.CurrentPeriodEnd != nil {
		approxStart := sub.CurrentPeriodEnd.AddDate(0, -1, 0)
		if approxStart.Before(now) {
			from = approxStart
		}
	}

	var rollups []store.ModelUsageRollup
	if err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		rollups, err = store.UsageByModel(ctx, tx, orgID, from, now)
		return err
	}); err != nil {
		return nil, fmt.Errorf("billing: usage by model: %w", err)
	}
	return rollups, nil
}

// ── Wallet (prepaid balance) ───────────────────────────────────────────────────

// WalletBalance returns the org's current prepaid wallet balance in cents.
func (s *Service) WalletBalance(ctx context.Context, orgID string) (int64, error) {
	var bal int64
	if err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		bal, err = store.WalletBalance(ctx, tx, orgID)
		return err
	}); err != nil {
		return 0, fmt.Errorf("billing: wallet balance: %w", err)
	}
	return bal, nil
}

// WalletTransactions returns the org's wallet ledger, newest first (capped).
func (s *Service) WalletTransactions(ctx context.Context, orgID string, limit int) ([]store.WalletTxn, error) {
	var txns []store.WalletTxn
	if err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		txns, err = store.ListWalletTransactions(ctx, tx, orgID, limit)
		return err
	}); err != nil {
		return nil, fmt.Errorf("billing: wallet transactions: %w", err)
	}
	return txns, nil
}

// CreditWalletTopup idempotently credits the wallet for a Paystack top-up keyed on
// the Paystack reference. A replayed webhook with the same ref is a no-op (returns
// the current balance + credited=false). cents must be > 0.
func (s *Service) CreditWalletTopup(ctx context.Context, orgID string, cents int64, ref, desc string) (balance int64, credited bool, err error) {
	if cents <= 0 {
		return 0, false, fmt.Errorf("billing: wallet topup: cents must be positive, got %d", cents)
	}
	werr := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		if ref != "" {
			exists, e := store.WalletTopupExists(ctx, tx, orgID, ref)
			if e != nil {
				return e
			}
			if exists {
				// Already credited this reference — no-op (idempotent replay).
				bal, e := store.WalletBalance(ctx, tx, orgID)
				if e != nil {
					return e
				}
				balance = bal
				credited = false
				return nil
			}
		}
		bal, e := store.WalletCredit(ctx, tx, orgID, cents, "topup", desc, ref)
		if e != nil {
			return e
		}
		balance = bal
		credited = true
		return nil
	})
	if werr != nil {
		return 0, false, fmt.Errorf("billing: wallet topup: %w", werr)
	}
	return balance, credited, nil
}

// ── Invoice generation ────────────────────────────────────────────────────────

// GenerateInvoice builds a USD invoice for the org covering [periodStart, periodEnd].
//
// It produces these categories of line items:
//
//  1. Per-builder seat lines — one line per builder (role = owner/admin/member) at
//     the plan's per_builder_cents price; stakeholders are never counted (decisions P6).
//     Each line carries git evidence (commit count, PR count for the period).
//
//  2. A managed-LLM line — provider cost beyond the org's included allowance
//     (builders × included_llm_cents) billed at × overage_markup. BYOK records $0
//     provider cost and so never overflows; the free tier has no allowance/managed LLM.
//
//  3. Other metered usage lines — one line per remaining usage kind (sync, etc.).
//
// Any work that git cannot prove (zero git activity for a builder) is recorded as an
// is_estimated=true line with evidence["confirmation_required"]=true so a human can
// review before the invoice is finalised (decisions P4).
//
// The invoice is created in "draft" status. The EE Paystack agent calls
// SetInvoiceCharge + MarkInvoicePaid to complete the cycle.
func (s *Service) GenerateInvoice(ctx context.Context, orgID string, periodStart, periodEnd time.Time) (*store.Invoice, error) {
	// 1. Determine the plan and per-seat USD price.
	sub, err := store.GetSubscription(ctx, s.db.Pool(), orgID)
	if err != nil && err != store.ErrNotFound {
		return nil, fmt.Errorf("billing: generate invoice: get subscription: %w", err)
	}

	planKey := "free"
	if sub != nil {
		planKey = sub.PlanKey
	}

	plan, err := s.GetPlan(ctx, planKey)
	if err != nil {
		return nil, fmt.Errorf("billing: generate invoice: get plan %q: %w", planKey, err)
	}

	// 2. Collect builder members (non-stakeholders) and usage within the period.
	var (
		members []store.OrgMember
		usage   []store.UsageRollup
		commits []store.CommitSummary
		prs     []store.PRSummary
	)

	if err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var err error

		members, err = store.ListMembers(ctx, tx, orgID)
		if err != nil {
			return fmt.Errorf("list members: %w", err)
		}

		usage, err = store.SumUsage(ctx, tx, orgID, periodStart, periodEnd)
		if err != nil {
			return fmt.Errorf("sum usage: %w", err)
		}

		commits, err = store.ListCommitsInPeriod(ctx, tx, orgID, periodStart, periodEnd)
		if err != nil {
			return fmt.Errorf("list commits: %w", err)
		}

		prs, err = store.ListPRsInPeriod(ctx, tx, orgID, periodStart, periodEnd)
		if err != nil {
			return fmt.Errorf("list prs: %w", err)
		}

		return nil
	}); err != nil {
		return nil, fmt.Errorf("billing: generate invoice: collect data: %w", err)
	}

	// 3. Index git activity per author (login or email) for evidence.
	commitsByAuthor := make(map[string]int)
	for _, c := range commits {
		key := c.AuthorLogin
		if key == "" {
			key = c.AuthorEmail
		}
		commitsByAuthor[key]++
	}

	prsByAuthor := make(map[string]int)
	for _, pr := range prs {
		prsByAuthor[pr.AuthorLogin]++
	}

	// 4. Build invoice lines for builders only.
	var (
		totalUSDCents int
		seatLines     []invoiceLine
		numBuilders   int
	)

	for _, m := range members {
		if !builderRoles[m.Role] {
			// stakeholder — free, skip (decisions P6)
			continue
		}
		numBuilders++

		seatCostUSD := plan.PerBuilderCents // per-builder monthly price in USD cents

		// Gather git evidence for this builder.
		numCommits := commitsByAuthor[m.Name]
		if numCommits == 0 {
			// Try email as fallback key (name may differ from git author).
			numCommits = commitsByAuthor[m.Email]
		}
		numPRs := prsByAuthor[m.Name]

		evidence := map[string]any{
			"user_id":      m.UserID,
			"role":         m.Role,
			"commits":      numCommits,
			"prs_opened":   numPRs,
			"period_start": periodStart.Format(time.DateOnly),
			"period_end":   periodEnd.Format(time.DateOnly),
		}

		isEstimated := false
		if numCommits == 0 && numPRs == 0 {
			// Git can't prove this builder worked — flag for human review (decisions P4).
			isEstimated = true
			evidence["confirmation_required"] = true
			evidence["note"] = "no git activity found for this period; human confirmation required before billing"
		}

		seatLines = append(seatLines, invoiceLine{
			desc:        fmt.Sprintf("Builder seat: %s (%s)", m.Email, m.Role),
			usdCents:    seatCostUSD,
			evidence:    evidence,
			isEstimated: isEstimated,
		})
		totalUSDCents += seatCostUSD
	}

	// 5. Build the managed-LLM line + any other metered usage lines.
	//
	// Managed-LLM model: usage beyond the org's included allowance
	// (builders × included_llm_cents) is billed at provider-cost × overage_markup.
	// BYOK usage records $0 provider cost, so it never produces overage. The free
	// tier carries no allowance and no managed LLM (BYOK-only) → no overage either.
	var (
		usageLines   []invoiceLine
		llmCostCents int  // total managed-LLM provider cost for the period (cents)
		sawLLM       bool // did we observe any llm_tokens usage?
		llmTotalQty  float64
	)
	for _, u := range usage {
		// cost_usd is stored as a float; convert to cents. Round (not truncate) so
		// sub-cent costs aren't systematically under-billed.
		costCents := int(math.Round(u.TotalCostUSD * 100))

		if u.Kind == "llm_tokens" {
			// Managed-LLM provider cost — aggregated into the allowance/overage line below.
			llmCostCents += costCents
			llmTotalQty += u.TotalQty
			sawLLM = true
			continue
		}

		if costCents == 0 {
			continue // zero-cost usage events don't appear on the invoice
		}
		usageLines = append(usageLines, invoiceLine{
			desc:     fmt.Sprintf("Usage: %s (qty %.2f)", u.Kind, u.TotalQty),
			usdCents: costCents,
			evidence: map[string]any{
				"kind":         u.Kind,
				"total_qty":    u.TotalQty,
				"period_start": periodStart.Format(time.DateOnly),
				"period_end":   periodEnd.Format(time.DateOnly),
			},
			isEstimated: false,
		})
		totalUSDCents += costCents
	}

	// Managed-LLM allowance/overage line.
	allowanceCents := numBuilders * plan.IncludedLLMCents
	overageCostCents := llmCostCents - allowanceCents
	if overageCostCents < 0 {
		overageCostCents = 0
	}
	billedLLMCents := int(float64(overageCostCents) * plan.OverageMarkup)
	if sawLLM && billedLLMCents > 0 {
		usageLines = append(usageLines, invoiceLine{
			desc:     fmt.Sprintf("Managed LLM overage (%d builders × $%.2f allowance, ×%.2f markup)", numBuilders, float64(plan.IncludedLLMCents)/100, plan.OverageMarkup),
			usdCents: billedLLMCents,
			evidence: map[string]any{
				"kind":              "llm_tokens",
				"total_qty":         llmTotalQty,
				"provider_cost_usd": float64(llmCostCents) / 100,
				"allowance_usd":     float64(allowanceCents) / 100,
				"overage_cost_usd":  float64(overageCostCents) / 100,
				"overage_markup":    plan.OverageMarkup,
				"builders":          numBuilders,
				"period_start":      periodStart.Format(time.DateOnly),
				"period_end":        periodEnd.Format(time.DateOnly),
			},
			isEstimated: false,
		})
		totalUSDCents += billedLLMCents
	}

	// 6. Persist: create invoice + lines in a single org-scoped transaction.
	var inv *store.Invoice
	if err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		inv, err = store.CreateInvoice(ctx, tx, orgID, totalUSDCents, periodStart, periodEnd)
		if err != nil {
			return fmt.Errorf("create invoice: %w", err)
		}

		for _, l := range append(seatLines, usageLines...) {
			if err := store.AddInvoiceLine(ctx, tx, inv.ID, l.desc, l.usdCents, l.evidence, l.isEstimated); err != nil {
				return fmt.Errorf("add invoice line: %w", err)
			}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("billing: generate invoice: persist: %w", err)
	}

	return inv, nil
}

// invoiceLine is a local scratch type for building lines before persisting.
type invoiceLine struct {
	desc        string
	usdCents    int
	evidence    map[string]any
	isEstimated bool
}
