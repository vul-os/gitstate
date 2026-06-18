//go:build ee

// Package eebilling implements the Paystack charging layer for gitstate Enterprise Edition.
// Prices are defined in USD; charges are issued in ZAR using the capture-time exchange rate
// (decisions A7/A8, security S4).
package eebilling

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/exchange"
	"github.com/exo/gitstate/internal/middleware"
	"github.com/exo/gitstate/internal/store"
)

const paystackBase = "https://api.paystack.co"

// RegisterPaystackRoutes registers the EE billing routes onto mux.
// This is the real implementation compiled only under the `ee` build tag.
//
//	POST /api/billing/checkout        — initialize a ZAR Paystack transaction for a plan
//	POST /api/billing/webhook         — receive and process Paystack webhook events
//	GET  /api/billing/verify/{ref}    — verify a Paystack transaction by reference
func RegisterPaystackRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	svc := newPaystackService(database, cfg)

	auth := middleware.RequireAuth(cfg.Auth.JWTSigningKey)
	orgScope := middleware.OrgScope(database.Pool())

	// POST /api/billing/checkout — requires auth + org scope
	checkoutHandler := auth(orgScope(http.HandlerFunc(svc.handleCheckout)))
	mux.Handle("POST /api/billing/checkout", checkoutHandler)

	// POST /api/billing/webhook — public endpoint, verified via HMAC-SHA512 signature (S4)
	mux.HandleFunc("POST /api/billing/webhook", svc.handleWebhook)

	// GET /api/billing/verify/{ref} — requires auth
	verifyHandler := auth(http.HandlerFunc(svc.handleVerify))
	mux.Handle("GET /api/billing/verify/{ref}", verifyHandler)
}

// paystackService holds dependencies for Paystack billing handlers.
type paystackService struct {
	db       *db.DB
	cfg      *config.Config
	exchange *exchange.Service
	client   *http.Client
}

func newPaystackService(database *db.DB, cfg *config.Config) *paystackService {
	return &paystackService{
		db:       database,
		cfg:      cfg,
		exchange: exchange.New(database, cfg),
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

// ── Paystack API helpers ──────────────────────────────────────────────────────

// paystackDo sends an authenticated request to the Paystack API and returns the raw response.
func (s *paystackService) paystackDo(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("paystack: marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, paystackBase+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("paystack: new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.Billing.Paystack.SecretKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("paystack: do request %s %s: %w", method, path, err)
	}
	return resp, nil
}

// decodePaystack reads and decodes a Paystack API response into out, returning an error
// for non-2xx status codes.
func decodePaystack(resp *http.Response, out any) error {
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("paystack: unexpected status %d: %s", resp.StatusCode, string(raw))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("paystack: decode response: %w", err)
	}
	return nil
}

// ── Checkout — POST /api/billing/checkout ────────────────────────────────────

type checkoutRequest struct {
	Plan string `json:"plan"` // plan key, e.g. "hobby", "pro"
}

type checkoutResponse struct {
	AuthorizationURL string `json:"authorization_url"`
	Reference        string `json:"reference"`
	ZARCents         int    `json:"zar_cents"`
	USDCents         int    `json:"usd_cents"`
	FXRate           string `json:"fx_rate"`
}

// handleCheckout initialises a Paystack transaction in ZAR for the requested plan.
// It converts the plan's USD price to ZAR at the current exchange rate, stores the
// fx rate on the invoice, and returns the Paystack authorization_url.
func (s *paystackService) handleCheckout(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromContext(r.Context())
	if user == nil {
		writeError(w, "authentication required", http.StatusUnauthorized)
		return
	}
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, "org context required", http.StatusBadRequest)
		return
	}

	var req checkoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Plan == "" {
		writeError(w, "plan is required", http.StatusBadRequest)
		return
	}

	// Per-builder pricing: amount = per-builder price × billable builders (stakeholders free).
	perBuilderCents, err := s.planPerBuilderCents(req.Plan)
	if err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}
	usdCents := perBuilderCents * s.orgBuilderCount(r.Context(), orgID)

	// Convert USD → ZAR at the capture-time rate (decisions A8).
	zarCents, rateID, err := s.exchange.Convert(r.Context(), usdCents)
	if err != nil {
		var stale *exchange.ErrStaleRate
		if !errors.As(err, &stale) {
			slog.ErrorContext(r.Context(), "ee/billing: exchange rate unavailable", "error", err)
			writeError(w, "exchange rate unavailable", http.StatusServiceUnavailable)
			return
		}
		slog.WarnContext(r.Context(), "ee/billing: using stale exchange rate", "error", err)
	}

	var fxRate float64
	if usdCents > 0 {
		fxRate = float64(zarCents) / float64(usdCents)
	}

	// Create a draft invoice, then stamp the ZAR charge and fx rate onto it.
	// Also ensure a subscription row exists for the org.
	var (
		invoiceID string
		paystackRef string // filled in after Paystack init
	)

	now := time.Now().UTC()
	periodEnd := now.AddDate(0, 1, 0) // 1 month period

	if err := s.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		// Ensure subscription row exists (upsert to pending if absent).
		if upsertErr := store.UpsertSubscription(
			r.Context(), tx, orgID, req.Plan, "pending", &periodEnd, "",
		); upsertErr != nil {
			return fmt.Errorf("upsert subscription: %w", upsertErr)
		}

		// Create a draft invoice for this charge period.
		inv, invErr := store.CreateInvoice(r.Context(), tx, orgID, usdCents, now, periodEnd)
		if invErr != nil {
			return fmt.Errorf("create invoice: %w", invErr)
		}
		invoiceID = inv.ID

		// Stamp ZAR charge + fx rate on the invoice (status becomes "open").
		// paystackRef is empty at this point; updated after Paystack responds.
		return store.SetInvoiceCharge(r.Context(), tx, invoiceID, zarCents, fxRate, rateID, "")
	}); err != nil {
		slog.ErrorContext(r.Context(), "ee/billing: checkout db error", "error", err)
		writeError(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Initialise transaction with Paystack (amount in kobo = ZAR cents).
	initPayload := map[string]any{
		"email":    user.Email,
		"amount":   zarCents,
		"currency": "ZAR",
		"metadata": map[string]any{
			"org_id":     orgID,
			"plan":       req.Plan,
			"invoice_id": invoiceID,
			"usd_cents":  usdCents,
			"rate_id":    rateID,
		},
	}

	resp, err := s.paystackDo(r.Context(), http.MethodPost, "/transaction/initialize", initPayload)
	if err != nil {
		slog.ErrorContext(r.Context(), "ee/billing: paystack init failed", "error", err)
		writeError(w, "payment provider unavailable", http.StatusBadGateway)
		return
	}
	var initResp struct {
		Status bool   `json:"status"`
		Data   struct {
			AuthorizationURL string `json:"authorization_url"`
			Reference        string `json:"reference"`
		} `json:"data"`
	}
	if err := decodePaystack(resp, &initResp); err != nil {
		slog.ErrorContext(r.Context(), "ee/billing: paystack init decode failed", "error", err)
		writeError(w, "payment provider error", http.StatusBadGateway)
		return
	}
	paystackRef = initResp.Data.Reference

	// Update invoice with the Paystack reference now that we have it.
	_ = s.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		return store.SetInvoiceCharge(r.Context(), tx, invoiceID, zarCents, fxRate, rateID, paystackRef)
	})

	writeJSON(w, http.StatusOK, checkoutResponse{
		AuthorizationURL: initResp.Data.AuthorizationURL,
		Reference:        paystackRef,
		ZARCents:         zarCents,
		USDCents:         usdCents,
		FXRate:           fmt.Sprintf("%.6f", fxRate),
	})
}

// ── Webhook — POST /api/billing/webhook ──────────────────────────────────────

// handleWebhook processes Paystack webhook events.
// Signature verification: HMAC-SHA512 over raw body with webhook secret (decisions S4).
// Idempotency: events recorded in paystack_events; duplicates are silently skipped.
func (s *paystackService) handleWebhook(w http.ResponseWriter, r *http.Request) {
	// Read raw body — required for both HMAC verification and JSON parsing.
	rawBody, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MB limit
	if err != nil {
		writeError(w, "failed to read body", http.StatusBadRequest)
		return
	}

	// Verify signature (S4) — HMAC-SHA512 constant-time compare.
	sig := r.Header.Get("x-paystack-signature")
	if !verifyWebhookSignature(s.cfg.Billing.Paystack.WebhookSecret, rawBody, sig) {
		slog.WarnContext(r.Context(), "ee/billing: webhook signature verification failed",
			"remote_addr", r.RemoteAddr)
		// Return 200 to avoid leaking information about what failed.
		w.WriteHeader(http.StatusOK)
		return
	}

	// Parse event envelope.
	var event struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rawBody, &event); err != nil {
		// Malformed payload — still return 200 so Paystack doesn't retry.
		slog.WarnContext(r.Context(), "ee/billing: webhook parse error", "error", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Extract a stable event identifier for idempotency.
	// Paystack places a reference on the data object; use it as the key.
	var meta struct {
		ID        string `json:"id"`
		Reference string `json:"reference"`
	}
	_ = json.Unmarshal(event.Data, &meta)

	eventID := meta.ID
	if eventID == "" {
		eventID = meta.Reference
	}
	if eventID == "" {
		// Synthesise a key from event type + body hash to avoid re-processing.
		mac := hmac.New(sha512.New, []byte(s.cfg.Billing.Paystack.WebhookSecret))
		mac.Write(rawBody)
		eventID = event.Event + ":" + hex.EncodeToString(mac.Sum(nil))[:16]
	}

	// Idempotency check.
	processed, chkErr := store.IsPaystackEventProcessed(r.Context(), s.db.Pool(), eventID)
	if chkErr != nil {
		slog.ErrorContext(r.Context(), "ee/billing: idempotency check failed", "error", chkErr, "event_id", eventID)
		// Proceed anyway — risk of double-processing is lower than blocking Paystack retries.
	}
	if processed {
		slog.InfoContext(r.Context(), "ee/billing: duplicate event skipped",
			"event_id", eventID, "event_type", event.Event)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Record event before processing (at-least-once delivery; handlers are idempotent).
	if recErr := store.RecordPaystackEvent(r.Context(), s.db.Pool(), eventID, event.Event, rawBody); recErr != nil {
		slog.WarnContext(r.Context(), "ee/billing: failed to record event",
			"error", recErr, "event_id", eventID)
	}

	// Dispatch by event type.
	switch event.Event {
	case "charge.success":
		s.handleChargeSuccess(r.Context(), event.Data)
	default:
		slog.InfoContext(r.Context(), "ee/billing: unhandled webhook event", "event_type", event.Event)
	}

	// Always return 200 so Paystack does not retry.
	w.WriteHeader(http.StatusOK)
}

// verifyWebhookSignature performs HMAC-SHA512 over body with secret and compares
// the result against sig using constant-time comparison (decisions S4).
func verifyWebhookSignature(secret string, body []byte, sig string) bool {
	if secret == "" || sig == "" {
		return false
	}
	mac := hmac.New(sha512.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	// Constant-time compare (both lowercased for robustness).
	return hmac.Equal([]byte(strings.ToLower(expected)), []byte(strings.ToLower(sig)))
}

// handleChargeSuccess processes a charge.success event:
// marks the invoice paid, records the payment, and activates the subscription.
func (s *paystackService) handleChargeSuccess(ctx context.Context, data json.RawMessage) {
	var charge struct {
		Reference string `json:"reference"`
		Amount    int    `json:"amount"` // ZAR cents/kobo as charged by Paystack
		Metadata  struct {
			OrgID     string `json:"org_id"`
			Plan      string `json:"plan"`
			InvoiceID string `json:"invoice_id"`
			USDCents  int    `json:"usd_cents"`
			RateID    string `json:"rate_id"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(data, &charge); err != nil {
		slog.ErrorContext(ctx, "ee/billing: charge.success parse error", "error", err)
		return
	}

	orgID := charge.Metadata.OrgID
	invoiceID := charge.Metadata.InvoiceID
	plan := charge.Metadata.Plan

	if orgID == "" || invoiceID == "" {
		slog.ErrorContext(ctx, "ee/billing: charge.success missing metadata",
			"org_id", orgID, "invoice_id", invoiceID)
		return
	}

	paidAt := time.Now().UTC()
	periodEnd := paidAt.AddDate(0, 1, 0)

	err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		// Mark invoice paid.
		if err := store.MarkInvoicePaid(ctx, tx, invoiceID, paidAt); err != nil {
			return fmt.Errorf("mark invoice paid: %w", err)
		}

		// Record the payment row (status = "succeeded").
		if err := store.RecordPayment(ctx, tx, orgID, invoiceID, charge.Amount, "succeeded", charge.Reference); err != nil {
			return fmt.Errorf("record payment: %w", err)
		}

		// Activate subscription on the paid plan with a 1-month period.
		if err := store.UpsertSubscription(ctx, tx, orgID, plan, "active", &periodEnd, ""); err != nil {
			return fmt.Errorf("activate subscription: %w", err)
		}

		return nil
	})
	if err != nil {
		slog.ErrorContext(ctx, "ee/billing: charge.success processing failed",
			"org_id", orgID, "invoice_id", invoiceID,
			"reference", charge.Reference, "error", err)
		return
	}

	slog.InfoContext(ctx, "ee/billing: charge.success processed",
		"org_id", orgID, "plan", plan,
		"reference", charge.Reference, "zar_cents", charge.Amount)
}

// ── Verify — GET /api/billing/verify/{ref} ───────────────────────────────────

// handleVerify calls the Paystack verify endpoint for the given transaction reference
// and returns the raw Paystack response to the caller.
func (s *paystackService) handleVerify(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	if ref == "" {
		writeError(w, "transaction reference is required", http.StatusBadRequest)
		return
	}

	resp, err := s.paystackDo(r.Context(), http.MethodGet, "/transaction/verify/"+ref, nil)
	if err != nil {
		slog.ErrorContext(r.Context(), "ee/billing: paystack verify failed",
			"ref", ref, "error", err)
		writeError(w, "payment provider unavailable", http.StatusBadGateway)
		return
	}

	var result map[string]any
	if err := decodePaystack(resp, &result); err != nil {
		slog.ErrorContext(r.Context(), "ee/billing: paystack verify decode failed",
			"ref", ref, "error", err)
		writeError(w, "payment provider error", http.StatusBadGateway)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// ── Plan lookup ───────────────────────────────────────────────────────────────

// planPerBuilderCents returns the per-builder monthly price in USD cents for a plan.
func (s *paystackService) planPerBuilderCents(planKey string) (int, error) {
	for _, p := range s.cfg.Billing.Plans {
		if strings.EqualFold(p.Key, planKey) {
			return p.PerBuilderUSD * 100, nil // config stores whole USD dollars; convert to cents
		}
	}
	return 0, fmt.Errorf("unknown plan: %q", planKey)
}

// orgBuilderCount counts billable builders (owner/admin/member; stakeholders are
// free per decisions P6). org_members is not RLS-protected, so a pool query is fine.
func (s *paystackService) orgBuilderCount(ctx context.Context, orgID string) int {
	var n int
	if err := s.db.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM org_members WHERE org_id=$1 AND role IN ('owner','admin','member')`,
		orgID).Scan(&n); err != nil || n < 1 {
		return 1 // charge at least one builder
	}
	return n
}

// ── JSON helpers ──────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, msg string, status int) {
	writeJSON(w, status, map[string]string{"error": msg})
}
