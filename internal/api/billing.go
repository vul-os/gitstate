// Package api — billing.go
// REST handlers for billing: plans, subscriptions, usage, and invoices.
//
// Decision compliance:
//   - A7: Routes are open-core; the handler file lives in internal/ not ee/.
//     Actual charging (Paystack) lives in ee/billing.
//   - A8: Bill USD / charge ZAR; invoice responses expose both currencies + rate.
//   - P4: Invoice line evidence is surfaced verbatim; is_estimated=true lines carry
//     a "confirmation_required" flag so the frontend can flag gaps to the user.
//   - P6: Plans endpoint exposes builder-seat model; stakeholder concept shown in description.
//
// All routes are gated by cfg.Billing.Enabled; returns 404 when disabled.
package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/exo/gitstate/internal/billing"
	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/middleware"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// RegisterBillingRoutes wires the /api/billing/* endpoints onto mux.
// All routes are behind RequireAuth + OrgScope and runtime-gated by cfg.Billing.Enabled.
// Called by the orchestrator from router.go — this package does NOT edit router.go.
func RegisterBillingRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	svc := billing.New(database, cfg)
	h := &billingHandlers{db: database, cfg: cfg, svc: svc}

	requireAuth := middleware.RequireAuth(cfg.Auth.JWTSigningKey)
	orgScope := middleware.OrgScope(database.Pool())
	auth := func(handler http.Handler) http.Handler {
		return requireAuth(orgScope(handler))
	}

	mux.Handle("GET /api/billing/plans", auth(http.HandlerFunc(h.listPlans)))
	mux.Handle("GET /api/billing/subscription", auth(http.HandlerFunc(h.getSubscription)))
	mux.Handle("GET /api/billing/usage", auth(http.HandlerFunc(h.getUsage)))
	mux.Handle("GET /api/billing/usage/by-model", auth(http.HandlerFunc(h.getUsageByModel)))
	mux.Handle("GET /api/billing/invoices", auth(http.HandlerFunc(h.listInvoices)))
	mux.Handle("GET /api/billing/invoices/{id}", auth(http.HandlerFunc(h.getInvoice)))
	mux.Handle("GET /api/billing/wallet", auth(http.HandlerFunc(h.getWallet)))
}

type billingHandlers struct {
	db  *db.DB
	cfg *config.Config
	svc *billing.Service
}

// billingEnabled returns false and writes 404 when billing is disabled.
func (h *billingHandlers) billingEnabled(w http.ResponseWriter) bool {
	if !h.cfg.Billing.Enabled {
		writeError(w, http.StatusNotFound, "billing is not enabled")
		return false
	}
	return true
}

// ── Response types ────────────────────────────────────────────────────────────

type planResponse struct {
	Key              string         `json:"key"`
	Name             string         `json:"name"`
	USDCents         int            `json:"usdCents"`
	PerBuilderCents  int            `json:"perBuilderCents"`  // monthly price per billable builder
	IncludedLLMCents int            `json:"includedLLMCents"` // included managed-LLM allowance per builder/mo
	OverageMarkup    float64        `json:"overageMarkup"`    // markup on managed-LLM beyond allowance (1.05 = +5%)
	Builders         int            `json:"builders"`
	MaxConns         int            `json:"maxConns"`
	Features         map[string]any `json:"features"`
}

type subscriptionResponse struct {
	ID               string  `json:"id"`
	PlanKey          string  `json:"planKey"`
	Status           string  `json:"status"`
	CurrentPeriodEnd *string `json:"currentPeriodEnd"`
	PaystackSubCode  string  `json:"paystackSubCode,omitempty"`
}

type usageRollupResponse struct {
	Kind         string  `json:"kind"`
	TotalQty     float64 `json:"totalQty"`
	TotalCostUSD float64 `json:"totalCostUSD"`
}

type modelUsageResponse struct {
	Model        string  `json:"model"`
	Kind         string  `json:"kind"`
	TotalQty     float64 `json:"totalQty"`     // tokens (see TODO in store.UsageByModel)
	TotalCostUSD float64 `json:"totalCostUSD"` // provider cost in USD
}

type walletTxnResponse struct {
	ID                string `json:"id"`
	Kind              string `json:"kind"` // topup | usage | adjustment | refund
	AmountCents       int64  `json:"amountCents"`
	Currency          string `json:"currency"`
	BalanceAfterCents int64  `json:"balanceAfterCents"`
	Description       string `json:"description"`
	Ref               string `json:"ref,omitempty"`
	CreatedAt         string `json:"createdAt"`
}

type walletResponse struct {
	BalanceCents int64               `json:"balanceCents"`
	Currency     string              `json:"currency"`
	Transactions []walletTxnResponse `json:"transactions"`
}

type invoiceResponse struct {
	ID          string   `json:"id"`
	Status      string   `json:"status"`
	USDCents    int      `json:"usdCents"`
	ZARCents    *int     `json:"zarCents"`
	FXRate      *float64 `json:"fxRate"`
	PeriodStart *string  `json:"periodStart"`
	PeriodEnd   *string  `json:"periodEnd"`
	PaystackRef string   `json:"paystackRef,omitempty"`
	IssuedAt    *string  `json:"issuedAt"`
	PaidAt      *string  `json:"paidAt"`
	CreatedAt   string   `json:"createdAt"`
}

type invoiceLineResponse struct {
	ID          string         `json:"id"`
	Description string         `json:"description"`
	USDCents    int            `json:"usdCents"`
	Evidence    map[string]any `json:"evidence"`
	IsEstimated bool           `json:"isEstimated"`
	// confirmation_required surfaces gaps per decisions P4 — visible in evidence map
}

type invoiceDetailResponse struct {
	invoiceResponse
	Lines []invoiceLineResponse `json:"lines"`
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func fmtTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

func toInvoiceResponse(inv store.Invoice) invoiceResponse {
	return invoiceResponse{
		ID:          inv.ID,
		Status:      inv.Status,
		USDCents:    inv.USDCents,
		ZARCents:    inv.ZARCents,
		FXRate:      inv.FXRate,
		PeriodStart: fmtTimePtr(inv.PeriodStart),
		PeriodEnd:   fmtTimePtr(inv.PeriodEnd),
		PaystackRef: inv.PaystackRef,
		IssuedAt:    fmtTimePtr(inv.IssuedAt),
		PaidAt:      fmtTimePtr(inv.PaidAt),
		CreatedAt:   inv.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// GET /api/billing/plans
// Returns the full plan ladder.
func (h *billingHandlers) listPlans(w http.ResponseWriter, r *http.Request) {
	if !h.billingEnabled(w) {
		return
	}

	plans, err := h.svc.ListPlans(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list plans")
		return
	}

	resp := make([]planResponse, 0, len(plans))
	for _, p := range plans {
		resp = append(resp, planResponse{
			Key:              p.Key,
			Name:             p.Name,
			USDCents:         p.USDCents,
			PerBuilderCents:  p.PerBuilderCents,
			IncludedLLMCents: p.IncludedLLMCents,
			OverageMarkup:    p.OverageMarkup,
			Builders:         p.Builders,
			MaxConns:         p.MaxConns,
			Features:         p.Features,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// GET /api/billing/subscription
// Returns the org's current subscription. 404 if none exists (on free plan).
func (h *billingHandlers) getSubscription(w http.ResponseWriter, r *http.Request) {
	if !h.billingEnabled(w) {
		return
	}
	orgID := middleware.OrgFromContext(r.Context())

	sub, err := h.svc.GetSubscription(r.Context(), orgID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no subscription (free plan)")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not fetch subscription")
		return
	}

	resp := subscriptionResponse{
		ID:               sub.ID,
		PlanKey:          sub.PlanKey,
		Status:           sub.Status,
		CurrentPeriodEnd: fmtTimePtr(sub.CurrentPeriodEnd),
		PaystackSubCode:  sub.PaystackSubCode,
	}
	writeJSON(w, http.StatusOK, resp)
}

// GET /api/billing/usage
// Returns realtime usage rollups for the current billing period.
func (h *billingHandlers) getUsage(w http.ResponseWriter, r *http.Request) {
	if !h.billingEnabled(w) {
		return
	}
	orgID := middleware.OrgFromContext(r.Context())

	rollups, err := h.svc.CurrentUsage(r.Context(), orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not fetch usage")
		return
	}

	resp := make([]usageRollupResponse, 0, len(rollups))
	for _, u := range rollups {
		resp = append(resp, usageRollupResponse{
			Kind:         u.Kind,
			TotalQty:     u.TotalQty,
			TotalCostUSD: u.TotalCostUSD,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// GET /api/billing/usage/by-model
// Returns the per-model managed-LLM usage breakdown for the current period.
func (h *billingHandlers) getUsageByModel(w http.ResponseWriter, r *http.Request) {
	if !h.billingEnabled(w) {
		return
	}
	orgID := middleware.OrgFromContext(r.Context())

	rollups, err := h.svc.CurrentUsageByModel(r.Context(), orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not fetch per-model usage")
		return
	}

	resp := make([]modelUsageResponse, 0, len(rollups))
	for _, u := range rollups {
		resp = append(resp, modelUsageResponse{
			Model:        u.Model,
			Kind:         u.Kind,
			TotalQty:     u.TotalQty,
			TotalCostUSD: u.TotalCostUSD,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// GET /api/billing/wallet
// Returns the org's prepaid wallet balance + recent ledger transactions.
func (h *billingHandlers) getWallet(w http.ResponseWriter, r *http.Request) {
	if !h.billingEnabled(w) {
		return
	}
	orgID := middleware.OrgFromContext(r.Context())

	bal, err := h.svc.WalletBalance(r.Context(), orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not fetch wallet balance")
		return
	}
	txns, err := h.svc.WalletTransactions(r.Context(), orgID, 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not fetch wallet transactions")
		return
	}

	txnResp := make([]walletTxnResponse, 0, len(txns))
	for _, t := range txns {
		txnResp = append(txnResp, walletTxnResponse{
			ID:                t.ID,
			Kind:              t.Kind,
			AmountCents:       t.AmountCents,
			Currency:          t.Currency,
			BalanceAfterCents: t.BalanceAfterCents,
			Description:       t.Description,
			Ref:               t.Ref,
			CreatedAt:         t.CreatedAt.UTC().Format(time.RFC3339),
		})
	}

	writeJSON(w, http.StatusOK, walletResponse{
		BalanceCents: bal,
		Currency:     "USD",
		Transactions: txnResp,
	})
}

// GET /api/billing/invoices
// Returns all invoices for the org, newest first.
func (h *billingHandlers) listInvoices(w http.ResponseWriter, r *http.Request) {
	if !h.billingEnabled(w) {
		return
	}
	orgID := middleware.OrgFromContext(r.Context())

	var invoices []store.Invoice
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		var err error
		invoices, err = store.ListInvoices(r.Context(), tx, orgID)
		return err
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not list invoices")
		return
	}

	resp := make([]invoiceResponse, 0, len(invoices))
	for _, inv := range invoices {
		resp = append(resp, toInvoiceResponse(inv))
	}
	writeJSON(w, http.StatusOK, resp)
}

// GET /api/billing/invoices/{id}
// Returns a single invoice with all its line items (evidence surfaced verbatim — decisions P4).
func (h *billingHandlers) getInvoice(w http.ResponseWriter, r *http.Request) {
	if !h.billingEnabled(w) {
		return
	}
	orgID := middleware.OrgFromContext(r.Context())
	invoiceID := r.PathValue("id")
	if invoiceID == "" {
		writeError(w, http.StatusBadRequest, "invoice id is required")
		return
	}

	var inv *store.Invoice
	var lines []store.InvoiceLine
	var getErr error

	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		inv, lines, getErr = store.GetInvoice(r.Context(), tx, orgID, invoiceID)
		return getErr
	}); err != nil {
		if errors.Is(getErr, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "invoice not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not fetch invoice")
		return
	}

	lineResp := make([]invoiceLineResponse, 0, len(lines))
	for _, l := range lines {
		evidence := l.Evidence
		if evidence == nil {
			evidence = map[string]any{}
		}
		lineResp = append(lineResp, invoiceLineResponse{
			ID:          l.ID,
			Description: l.Description,
			USDCents:    l.USDCents,
			Evidence:    evidence,
			IsEstimated: l.IsEstimated,
		})
	}

	writeJSON(w, http.StatusOK, invoiceDetailResponse{
		invoiceResponse: toInvoiceResponse(*inv),
		Lines:           lineResp,
	})
}
