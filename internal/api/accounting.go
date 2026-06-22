// Package api — Xero / QuickBooks accounting connection + invoice-push routes.
//
// An org owner/admin authorizes once; the connection's access + refresh tokens
// are stored AES-256-GCM encrypted in accounting_connections (one per provider
// per org). From there a gitstate ClientInvoice — manual or generated-from-git —
// can be pushed to the external system, recording the external id + deep link on
// the invoice (client_invoices.external_*).
//
// Config-gated + graceful: providers without OAuth credentials are reported as
// "not configured" and their start/callback/push routes return 4xx. The OAuth
// flow copies calendar.go's CSRF state-cookie pattern (proxy-aware Secure).
package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/exo/gitstate/internal/accounting"
	"github.com/exo/gitstate/internal/auth"
	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/crypto"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/middleware"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// RegisterAccountingRoutes wires the accounting connection + invoice-push
// endpoints behind RequireAuth + OrgScope (except the provider callback, which
// recovers the org/user from the state cookie). Called by the orchestrator from
// router.go — this package does NOT edit router.go.
//
//	GET    /api/accounting/status               → [{provider, configured, connected, externalName}]
//	GET    /api/accounting/{provider}/start     → 302 to provider authorize (404 if not configured)
//	GET    /api/accounting/{provider}/callback  → exchange, encrypt+store, redirect to /invoices
//	DELETE /api/accounting/{provider}           → disconnect (manager-gated)
//	POST   /api/invoices/{id}/push              → push invoice to provider (manager-gated)
func RegisterAccountingRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	providers := accounting.Load(cfg, cfg.App.PublicURL)
	h := &accountingHandlers{db: database, cfg: cfg, providers: providers}

	requireAuth := middleware.RequireAuth(cfg.Auth.JWTSigningKey)
	orgScope := middleware.OrgScope(database.Pool())
	authed := func(handler http.Handler) http.Handler {
		return requireAuth(orgScope(handler))
	}

	mux.Handle("GET /api/accounting/status", authed(http.HandlerFunc(h.status)))
	mux.Handle("DELETE /api/accounting/{provider}", authed(http.HandlerFunc(h.disconnect)))
	mux.Handle("POST /api/invoices/{id}/push", authed(http.HandlerFunc(h.push)))

	// start is a top-level browser navigation (provider redirect must land on a
	// real page), so it self-authenticates from ?token= and ?org= query params.
	mux.HandleFunc("GET /api/accounting/{provider}/start", h.start)
	// callback is a top-level provider redirect (no Authorization header) — the
	// org/user are recovered from the state cookie set at /start.
	mux.HandleFunc("GET /api/accounting/{provider}/callback", h.callback)
}

type accountingHandlers struct {
	db        *db.DB
	cfg       *config.Config
	providers map[string]accounting.Provider
}

// accountingStateCookie carries CSRF state + the org/user that initiated the flow
// across the provider redirect (the callback has no auth context).
const accountingStateCookie = "gs_accounting_state"

type accountingState struct {
	State    string `json:"s"`
	OrgID    string `json:"o"`
	UserID   string `json:"u"`
	Provider string `json:"p"`
}

// requireManager enforces owner/admin for mutating accounting routes.
func (h *accountingHandlers) requireManager(w http.ResponseWriter, r *http.Request, orgID string) bool {
	user := middleware.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return false
	}
	role, err := store.GetMemberRole(r.Context(), h.db.Pool(), orgID, user.ID)
	if err != nil || !canManageMembers(role) {
		writeError(w, http.StatusForbidden, "only owners and admins can manage accounting")
		return false
	}
	return true
}

// ── GET /api/accounting/status ─────────────────────────────────────────────────

type accountingStatus struct {
	Provider     string `json:"provider"`
	Configured   bool   `json:"configured"`
	Connected    bool   `json:"connected"`
	ExternalName string `json:"externalName,omitempty"`
}

func (h *accountingHandlers) status(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())

	byProvider := map[string]*accountingStatus{}
	order := []string{"xero", "quickbooks"}
	for _, prov := range order {
		_, configured := h.providers[prov]
		byProvider[prov] = &accountingStatus{Provider: prov, Configured: configured}
	}

	var conns []*store.AccountingConnection
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		c, e := store.ListAccountingConnections(r.Context(), tx, orgID)
		conns = c
		return e
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "list accounting connections")
		return
	}
	for _, c := range conns {
		if s, ok := byProvider[c.Provider]; ok {
			s.Connected = true
			s.ExternalName = c.ExternalName
		}
	}

	out := make([]accountingStatus, 0, len(order))
	for _, prov := range order {
		out = append(out, *byProvider[prov])
	}
	writeJSON(w, http.StatusOK, out)
}

// ── GET /api/accounting/{provider}/start ───────────────────────────────────────

func (h *accountingHandlers) start(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(r.PathValue("provider"))
	p, ok := h.providers[provider]
	if !ok {
		writeError(w, http.StatusNotFound, "accounting provider not configured")
		return
	}

	tokenStr := r.URL.Query().Get("token")
	orgID := r.URL.Query().Get("org")
	if tokenStr == "" {
		if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			tokenStr = bearer
		}
	}
	claims, err := auth.ParseAccessToken(h.cfg.Auth.JWTSigningKey, tokenStr)
	if err != nil || orgID == "" {
		writeError(w, http.StatusUnauthorized, "missing or invalid auth")
		return
	}

	stateVal, err := generateShareToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generate state")
		return
	}

	cs := accountingState{State: stateVal, OrgID: orgID, UserID: claims.UserID(), Provider: provider}
	raw, _ := json.Marshal(cs)
	http.SetCookie(w, &http.Cookie{
		Name:     accountingStateCookie,
		Value:    base64.RawURLEncoding.EncodeToString(raw),
		Path:     "/",
		MaxAge:   600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(r),
	})

	http.Redirect(w, r, p.AuthCodeURL(stateVal), http.StatusFound)
}

// ── GET /api/accounting/{provider}/callback ────────────────────────────────────

func (h *accountingHandlers) callback(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(r.PathValue("provider"))
	p, ok := h.providers[provider]
	if !ok {
		writeError(w, http.StatusNotFound, "accounting provider not configured")
		return
	}

	cookie, err := r.Cookie(accountingStateCookie)
	if err != nil || cookie.Value == "" {
		writeError(w, http.StatusBadRequest, "missing state cookie")
		return
	}
	rawState, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid state cookie")
		return
	}
	var cs accountingState
	if err := json.Unmarshal(rawState, &cs); err != nil {
		writeError(w, http.StatusBadRequest, "invalid state cookie")
		return
	}
	// Clear the state cookie.
	http.SetCookie(w, &http.Cookie{
		Name: accountingStateCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})

	if r.URL.Query().Get("state") != cs.State || cs.Provider != provider || cs.OrgID == "" || cs.UserID == "" {
		writeError(w, http.StatusBadRequest, "state mismatch")
		return
	}

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		h.redirectInvoices(w, r, "error="+errParam)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, "missing code")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	// QuickBooks passes realmId on the callback query; collect the whole query.
	cbq := map[string]string{}
	for k := range r.URL.Query() {
		cbq[k] = r.URL.Query().Get(k)
	}

	tok, externalOrgID, externalName, err := p.Exchange(ctx, code, cbq)
	if err != nil {
		slog.Error("accounting: exchange", "provider", provider, "err", err)
		h.redirectInvoices(w, r, "error=exchange_failed")
		return
	}

	key, err := crypto.KeyFromEnv()
	if err != nil {
		slog.Error("accounting: encryption key", "err", err)
		h.redirectInvoices(w, r, "error=server_misconfigured")
		return
	}
	encToken, err := crypto.Encrypt([]byte(tok.AccessToken), key)
	if err != nil {
		slog.Error("accounting: encrypt token", "err", err)
		h.redirectInvoices(w, r, "error=encrypt_failed")
		return
	}
	var encRefresh []byte
	if tok.RefreshToken != "" {
		if encRefresh, err = crypto.Encrypt([]byte(tok.RefreshToken), key); err != nil {
			slog.Error("accounting: encrypt refresh", "err", err)
		}
	}

	in := store.UpsertAccountingConnectionInput{
		OrgID:            cs.OrgID,
		UserID:           cs.UserID,
		Provider:         provider,
		ExternalOrgID:    externalOrgID,
		ExternalName:     externalName,
		TokenEncrypted:   encToken,
		RefreshEncrypted: encRefresh,
		Scopes:           p.Scopes(),
	}
	if !tok.Expiry.IsZero() {
		exp := tok.Expiry.UTC()
		in.ExpiresAt = &exp
	}

	if err := h.db.WithOrg(r.Context(), cs.OrgID, func(tx pgx.Tx) error {
		_, e := store.UpsertAccountingConnection(r.Context(), tx, in)
		return e
	}); err != nil {
		slog.Error("accounting: store connection", "provider", provider, "err", err)
		h.redirectInvoices(w, r, "error=store_failed")
		return
	}

	h.redirectInvoices(w, r, "accounting="+provider)
}

func (h *accountingHandlers) redirectInvoices(w http.ResponseWriter, r *http.Request, query string) {
	url := h.cfg.App.PublicURL + "/invoices?" + query
	http.Redirect(w, r, url, http.StatusFound)
}

// ── DELETE /api/accounting/{provider} ──────────────────────────────────────────

func (h *accountingHandlers) disconnect(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(r.PathValue("provider"))
	orgID := middleware.OrgFromContext(r.Context())
	if !h.requireManager(w, r, orgID) {
		return
	}
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		return store.DeleteAccountingConnection(r.Context(), tx, orgID, provider)
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "disconnect accounting")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── POST /api/invoices/{id}/push ───────────────────────────────────────────────

type pushRequest struct {
	Provider string `json:"provider"`
}

type pushResponse struct {
	Provider    string `json:"provider"`
	ExternalID  string `json:"externalId"`
	ExternalURL string `json:"externalUrl"`
}

func (h *accountingHandlers) push(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "org context required")
		return
	}
	if !h.requireManager(w, r, orgID) {
		return
	}
	invoiceID := r.PathValue("id")

	var req pushRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	p, ok := h.providers[provider]
	if !ok {
		writeError(w, http.StatusBadRequest, "accounting provider not configured")
		return
	}

	key, err := crypto.KeyFromEnv()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server misconfigured")
		return
	}

	// Load the invoice + lines + the org's connection for the provider.
	var (
		inv   *store.ClientInvoice
		lines []*store.ClientInvoiceLine
		conn  *store.AccountingConnection
	)
	err = h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		i, e := store.GetClientInvoice(r.Context(), tx, orgID, invoiceID)
		if e != nil {
			return e
		}
		inv = i
		ls, e := store.GetClientInvoiceLines(r.Context(), tx, orgID, invoiceID)
		if e != nil {
			return e
		}
		lines = ls
		c, e := store.GetAccountingConnection(r.Context(), tx, orgID, provider)
		if e != nil {
			return e
		}
		conn = c
		return nil
	})
	if errors.Is(err, store.ErrNotFound) {
		// Either the invoice or the connection is missing — disambiguate.
		if inv == nil {
			writeError(w, http.StatusNotFound, "invoice not found")
			return
		}
		writeError(w, http.StatusBadRequest, "not connected to "+provider+"; connect it in Settings first")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load invoice")
		return
	}

	tok, err := h.tokensForConn(r.Context(), key, orgID, p, conn)
	if err != nil {
		if errors.Is(err, accounting.ErrUnauthorized) {
			writeError(w, http.StatusBadRequest, "the "+provider+" connection expired; reconnect it in Settings")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not prepare accounting credentials")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	extID, extURL, err := p.CreateInvoice(ctx, tok, conn.ExternalOrgID, mapInvoice(inv, lines))
	if err != nil {
		if errors.Is(err, accounting.ErrUnauthorized) {
			writeError(w, http.StatusBadRequest, "the "+provider+" connection expired; reconnect it in Settings")
			return
		}
		slog.Error("accounting: create invoice", "provider", provider, "invoice", invoiceID, "err", err)
		writeError(w, http.StatusBadGateway, "the accounting provider rejected the invoice: "+err.Error())
		return
	}

	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		return store.SetInvoiceExternalRef(r.Context(), tx, orgID, invoiceID, provider, extID, extURL)
	}); err != nil {
		slog.Error("accounting: store external ref", "provider", provider, "invoice", invoiceID, "err", err)
		// The invoice WAS created externally; surface the link even if we couldn't
		// persist the ref.
	}

	writeJSON(w, http.StatusOK, pushResponse{Provider: provider, ExternalID: extID, ExternalURL: extURL})
}

// tokensForConn decrypts a connection's tokens and refreshes them if the access
// token is (about to be) expired, persisting any refreshed token. Returns the
// usable token set.
func (h *accountingHandlers) tokensForConn(ctx context.Context, key [32]byte, orgID string, p accounting.Provider, conn *store.AccountingConnection) (accounting.Tokens, error) {
	if len(conn.TokenEncrypted) == 0 {
		return accounting.Tokens{}, accounting.ErrUnauthorized
	}
	accessBytes, err := crypto.Decrypt(conn.TokenEncrypted, key)
	if err != nil {
		return accounting.Tokens{}, err
	}
	tok := accounting.Tokens{AccessToken: string(accessBytes)}
	if conn.ExpiresAt != nil {
		tok.Expiry = *conn.ExpiresAt
	}
	if len(conn.RefreshEncrypted) > 0 {
		if rb, derr := crypto.Decrypt(conn.RefreshEncrypted, key); derr == nil {
			tok.RefreshToken = string(rb)
		}
	}

	if tok.Valid() {
		return tok, nil
	}
	if tok.RefreshToken == "" {
		return accounting.Tokens{}, accounting.ErrUnauthorized
	}

	refreshCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	fresh, err := p.Refresh(refreshCtx, tok.RefreshToken)
	if err != nil {
		return accounting.Tokens{}, err
	}
	h.persistTokens(ctx, key, orgID, p.Name(), fresh)
	return fresh, nil
}

// persistTokens re-encrypts and stores a refreshed token set (best-effort).
func (h *accountingHandlers) persistTokens(ctx context.Context, key [32]byte, orgID, provider string, tok accounting.Tokens) {
	if tok.AccessToken == "" {
		return
	}
	encToken, err := crypto.Encrypt([]byte(tok.AccessToken), key)
	if err != nil {
		slog.Warn("accounting: re-encrypt refreshed token", "err", err)
		return
	}
	var encRefresh []byte
	if tok.RefreshToken != "" {
		if e, eerr := crypto.Encrypt([]byte(tok.RefreshToken), key); eerr == nil {
			encRefresh = e
		}
	}
	var exp *time.Time
	if !tok.Expiry.IsZero() {
		e := tok.Expiry.UTC()
		exp = &e
	}
	_ = h.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		return store.UpdateAccountingTokens(ctx, tx, orgID, provider, encToken, encRefresh, exp)
	})
}

// mapInvoice maps a gitstate ClientInvoice (+ lines) to the provider-agnostic
// accounting.Invoice. Amounts are converted from integer cents to major units.
func mapInvoice(inv *store.ClientInvoice, lines []*store.ClientInvoiceLine) accounting.Invoice {
	out := accounting.Invoice{
		Number:      inv.Number,
		ContactName: inv.ClientName,
		Currency:    inv.Currency,
		Reference:   inv.Number,
		Lines:       make([]accounting.InvoiceLine, 0, len(lines)),
	}
	if out.ContactName == "" {
		out.ContactName = "Client"
	}
	for _, l := range lines {
		qty := l.Quantity
		if qty == 0 {
			qty = 1
		}
		amount := float64(l.AmountCents) / 100
		unit := float64(l.UnitRateCents) / 100
		if unit == 0 && qty != 0 {
			unit = amount / qty
		}
		out.Lines = append(out.Lines, accounting.InvoiceLine{
			Description: l.Description,
			Quantity:    qty,
			UnitAmount:  unit,
			Amount:      amount,
		})
	}
	return out
}
