package api

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/invoicedelivery"
	"github.com/exo/gitstate/internal/invoicepdf"
	"github.com/exo/gitstate/internal/middleware"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// RegisterInvoiceRoutes wires the authenticated client-invoicing API onto mux.
// Called by the orchestrator from router.go — this file does NOT edit router.go.
//
// All routes require RequireAuth + OrgScope and run reads/writes inside
// db.WithOrg so RLS enforces the org boundary.
//
// Clients:
//
//	GET   /api/clients            list
//	POST  /api/clients            create
//	PATCH /api/clients/{id}       update
//
// Invoices:
//
//	GET    /api/invoices              list headers
//	GET    /api/invoices/{id}         header + line items
//	POST   /api/invoices             create a draft from explicit lines (git + manual)
//	POST   /api/invoices/generate    GENERATE a draft from merged-PR git effort (per-repo)
//	POST   /api/invoices/from-git    RICHER git draft: per-area lines, hours or $/point
//	PATCH  /api/invoices/{id}        update status/notes (status→sent sets a share token)
//	DELETE /api/invoices/{id}        delete
//
// The public, unauthenticated share route is registered separately by
// RegisterPublicInvoiceRoute.
func RegisterInvoiceRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	h := &invoiceHandlers{db: database, cfg: cfg}

	requireAuth := middleware.RequireAuth(cfg.Auth.JWTSigningKey)
	orgScope := middleware.OrgScope(database.Pool())
	auth := func(handler http.HandlerFunc) http.Handler {
		return requireAuth(orgScope(handler))
	}

	mux.Handle("GET /api/clients", auth(h.listClients))
	mux.Handle("POST /api/clients", auth(h.createClient))
	mux.Handle("PATCH /api/clients/{id}", auth(h.updateClient))

	mux.Handle("GET /api/invoices", auth(h.listInvoices))
	mux.Handle("POST /api/invoices/generate", auth(h.generate))
	mux.Handle("POST /api/invoices/from-git", auth(h.fromGit))
	mux.Handle("POST /api/invoices", auth(h.createInvoice))
	mux.Handle("GET /api/invoices/{id}/pdf", auth(h.invoicePDF))
	mux.Handle("GET /api/invoices/{id}", auth(h.getInvoice))
	mux.Handle("PATCH /api/invoices/{id}", auth(h.patchInvoice))
	mux.Handle("DELETE /api/invoices/{id}", auth(h.deleteInvoice))
}

type invoiceHandlers struct {
	db  *db.DB
	cfg *config.Config
}

// requireManager enforces owner/admin for mutating invoice/client routes. It
// returns false (and writes 401/403) when the caller is unauthenticated or lacks
// the role; callers must return immediately on false.
func (h *invoiceHandlers) requireManager(w http.ResponseWriter, r *http.Request, orgID string) bool {
	user := middleware.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return false
	}
	role, err := store.GetMemberRole(r.Context(), h.db.Pool(), orgID, user.ID)
	if err != nil || !canManageMembers(role) {
		writeError(w, http.StatusForbidden, "only owners and admins can manage invoices")
		return false
	}
	return true
}

// ── Clients ─────────────────────────────────────────────────────────────────────

func (h *invoiceHandlers) listClients(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "org context required")
		return
	}
	out := []*store.Client{}
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		cs, e := store.ListClients(r.Context(), tx, orgID)
		if cs != nil {
			out = cs
		}
		return e
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not list clients")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type clientRequest struct {
	Name         string `json:"name"`
	ContactEmail string `json:"contactEmail"`
	RateCents    *int   `json:"rateCents"`
	Notes        string `json:"notes"`
}

func (h *invoiceHandlers) createClient(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "org context required")
		return
	}
	if !h.requireManager(w, r, orgID) {
		return
	}
	var req clientRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	rate := 15000
	if req.RateCents != nil && *req.RateCents >= 0 {
		rate = *req.RateCents
	}
	var out *store.Client
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		c, e := store.CreateClient(r.Context(), tx, orgID, req.Name, strings.TrimSpace(req.ContactEmail), rate, req.Notes)
		out = c
		return e
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not create client: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *invoiceHandlers) updateClient(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "org context required")
		return
	}
	if !h.requireManager(w, r, orgID) {
		return
	}
	id := r.PathValue("id")
	var req clientRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	patch := store.ClientPatch{}
	if n := strings.TrimSpace(req.Name); n != "" {
		patch.Name = &n
	}
	if req.ContactEmail != "" {
		e := strings.TrimSpace(req.ContactEmail)
		patch.ContactEmail = &e
	}
	if req.RateCents != nil {
		patch.RateCents = req.RateCents
	}
	if req.Notes != "" {
		patch.Notes = &req.Notes
	}
	var out *store.Client
	err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		c, e := store.UpdateClient(r.Context(), tx, orgID, id, patch)
		out = c
		return e
	})
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "client not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not update client")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ── Invoices ────────────────────────────────────────────────────────────────────

func (h *invoiceHandlers) listInvoices(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "org context required")
		return
	}
	out := []*store.ClientInvoice{}
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		is, e := store.ListClientInvoices(r.Context(), tx, orgID)
		if is != nil {
			out = is
		}
		return e
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not list invoices")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// invoiceDetail is the header + its line items.
type invoiceDetail struct {
	*store.ClientInvoice
	Lines []*store.ClientInvoiceLine `json:"lines"`
}

func (h *invoiceHandlers) getInvoice(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "org context required")
		return
	}
	id := r.PathValue("id")
	var detail invoiceDetail
	detail.Lines = []*store.ClientInvoiceLine{}
	err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		inv, e := store.GetClientInvoice(r.Context(), tx, orgID, id)
		if e != nil {
			return e
		}
		detail.ClientInvoice = inv
		lines, e := store.GetClientInvoiceLines(r.Context(), tx, orgID, id)
		if lines != nil {
			detail.Lines = lines
		}
		return e
	})
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "invoice not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load invoice")
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// invoicePDF streams a branded PDF for a finalized billing invoice (the per-seat
// invoices table: USD billed / ZAR charged / locked FX rate). Any org member may
// download it; the invoice is read under RLS so only the caller's org is visible.
//
//	GET /api/invoices/{id}/pdf  → application/pdf (Content-Disposition: attachment)
func (h *invoiceHandlers) invoicePDF(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "org context required")
		return
	}
	id := r.PathValue("id")

	var (
		inv   *store.Invoice
		lines []store.InvoiceLine
		org   *store.Org
	)
	err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		i, ls, e := store.GetInvoice(r.Context(), tx, orgID, id)
		if e != nil {
			return e
		}
		inv, lines = i, ls
		o, e := store.GetOrg(r.Context(), tx, orgID)
		if e != nil {
			return e
		}
		org = o
		return nil
	})
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "invoice not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load invoice")
		return
	}

	appBillingURL := ""
	if h.cfg != nil {
		if base := strings.TrimRight(h.cfg.App.PublicURL, "/"); base != "" {
			appBillingURL = base + "/billing"
		}
	}
	data := invoicedelivery.BuildInvoiceData(inv, lines, org, appBillingURL)
	pdfBytes, err := invoicepdf.Render(data)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not render invoice pdf")
		return
	}

	filename := "gitstate-invoice-" + invoiceFilenameSlug(data.Number) + ".pdf"
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(pdfBytes)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pdfBytes)
}

// invoiceFilenameSlug keeps only filename-safe characters from an invoice number.
func invoiceFilenameSlug(s string) string {
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, s)
	if out == "" {
		return "invoice"
	}
	return out
}

// createInvoiceRequest is the body for POST /api/invoices (manual draft). Lines
// may be git-derived (source "git", carry evidence) or manual (source "manual",
// free-form). discountCents/taxCents/taxRate adjust the header total.
type createInvoiceRequest struct {
	ClientID      string             `json:"clientId"`
	ProjectID     string             `json:"projectId"`
	From          string             `json:"from"`
	To            string             `json:"to"`
	Currency      string             `json:"currency"`
	Notes         string             `json:"notes"`
	DiscountCents int                `json:"discountCents"`
	TaxCents      int                `json:"taxCents"`
	TaxRate       float64            `json:"taxRate"`
	Lines         []invoiceLineInput `json:"lines"`
}

type invoiceLineInput struct {
	Source        string               `json:"source"` // "git" | "manual" (default "git")
	Description   string               `json:"description"`
	EffortPoints  float64              `json:"effortPoints"`
	Quantity      float64              `json:"quantity"`
	UnitRateCents int                  `json:"unitRateCents"`
	AmountCents   int                  `json:"amountCents"`
	Evidence      []store.EvidenceItem `json:"evidence"`
}

func (h *invoiceHandlers) createInvoice(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "org context required")
		return
	}
	if !h.requireManager(w, r, orgID) {
		return
	}
	var req createInvoiceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	from, to, ok := parseInvoicePeriod(req.From, req.To)
	if !ok {
		writeError(w, http.StatusBadRequest, "from/to must be valid dates with from <= to")
		return
	}

	in := store.CreateClientInvoiceInput{
		ClientID:      nullableUUID(req.ClientID),
		ProjectID:     nullableUUID(req.ProjectID),
		PeriodStart:   from,
		PeriodEnd:     to,
		Currency:      req.Currency,
		Notes:         req.Notes,
		DiscountCents: req.DiscountCents,
		TaxCents:      req.TaxCents,
		TaxRate:       req.TaxRate,
		Lines:         toStoreLines(req.Lines),
	}

	var out *store.ClientInvoice
	// Auto-numbering races on UNIQUE(org_id, number) under read-committed: two
	// concurrent creates can pick the same MAX+1 and one loses the insert. Retry
	// the whole tx (fresh number each attempt) on a unique-violation.
	err := withInvoiceNumberRetry(func() error {
		return h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
			num, e := store.NextClientInvoiceNumber(r.Context(), tx, orgID, from.Year())
			if e != nil {
				return e
			}
			in.Number = num
			inv, e := store.CreateClientInvoice(r.Context(), tx, orgID, in)
			out = inv
			return e
		})
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create invoice: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// withInvoiceNumberRetry runs fn, retrying a few times when it fails with a
// unique_violation (a lost invoice-number race). A non-unique error, or success,
// returns immediately.
func withInvoiceNumberRetry(fn func() error) error {
	const maxAttempts = 5
	var err error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		err = fn()
		if err == nil || !store.IsUniqueViolation(err) {
			return err
		}
	}
	return err
}

// ── Generate from git ───────────────────────────────────────────────────────────

// generateRequest is the body for POST /api/invoices/generate.
type generateRequest struct {
	ClientID  string `json:"clientId"`
	ProjectID string `json:"projectId"`
	From      string `json:"from"`
	To        string `json:"to"`
	RateCents *int   `json:"rateCents"`
	// Preview=true returns the would-be lines without persisting anything.
	Preview bool `json:"preview"`
}

func (h *invoiceHandlers) generate(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "org context required")
		return
	}
	if !h.requireManager(w, r, orgID) {
		return
	}
	var req generateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	from, to, ok := parseInvoicePeriod(req.From, req.To)
	if !ok {
		writeError(w, http.StatusBadRequest, "from/to must be valid dates with from <= to")
		return
	}

	clientID := nullableUUID(req.ClientID)
	projectID := nullableUUID(req.ProjectID)

	var (
		lines   []store.ClientInvoiceLine
		rate    int
		preview *store.ClientInvoice
	)

	// Retry the tx on a lost invoice-number race (UNIQUE(org_id, number)); the
	// preview path never inserts, so it returns on the first attempt.
	err := withInvoiceNumberRetry(func() error {
		return h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
			// Resolve the billing rate: explicit > client default > schema default.
			rate = 15000
			if clientID != nil {
				cs, e := store.ListClients(r.Context(), tx, orgID)
				if e != nil {
					return e
				}
				for _, c := range cs {
					if c.ID == *clientID {
						rate = c.RateCents
						break
					}
				}
			}
			if req.RateCents != nil && *req.RateCents > 0 {
				rate = *req.RateCents
			}

			eff, e := store.MergedPREffort(r.Context(), tx, orgID, store.MergedEffortInput{
				ProjectID: derefStr(projectID),
				From:      from,
				To:        to,
			})
			if e != nil {
				return e
			}
			lines = buildLines(eff, rate)

			if req.Preview {
				// Build an unsaved preview header (subtotal/total computed here).
				subtotal := 0
				for _, l := range lines {
					subtotal += l.AmountCents
				}
				preview = &store.ClientInvoice{
					ClientID:      clientID,
					ProjectID:     projectID,
					Number:        "(draft)",
					Status:        "draft",
					PeriodStart:   from,
					PeriodEnd:     to,
					Currency:      "USD",
					SubtotalCents: subtotal,
					TotalCents:    subtotal,
				}
				return nil
			}

			num, e := store.NextClientInvoiceNumber(r.Context(), tx, orgID, from.Year())
			if e != nil {
				return e
			}
			inv, e := store.CreateClientInvoice(r.Context(), tx, orgID, store.CreateClientInvoiceInput{
				ClientID:    clientID,
				ProjectID:   projectID,
				Number:      num,
				PeriodStart: from,
				PeriodEnd:   to,
				Currency:    "USD",
				Lines:       lines,
			})
			preview = inv
			return e
		})
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not generate invoice: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, invoiceDetail{
		ClientInvoice: preview,
		Lines:         linesWithoutID(lines),
	})
}

// ── From-git builder (richer) ────────────────────────────────────────────────────

// fromGitRequest is the body for POST /api/invoices/from-git — a richer git-derived
// draft builder. It takes a client/period + optional project scope and a billing
// rate expressed either as $/effort-point (rateCents, the default) OR as an
// effort→hours conversion (hoursPerPoint) priced at hourlyRateCents. Each generated
// line is git-derived (source "git") and carries its Evidence (PR titles + repo +
// merged_at + sha). discountCents/taxCents/taxRate carry through to the header.
type fromGitRequest struct {
	ClientID  string `json:"clientId"`
	ProjectID string `json:"projectId"`
	From      string `json:"from"`
	To        string `json:"to"`

	// Pricing. If HoursPerPoint > 0, lines bill HoursPerPoint×points hours at
	// HourlyRateCents; quantity is the hours. Otherwise lines bill points × RateCents
	// directly (quantity = points). RateCents falls back to the client/default rate.
	RateCents       *int    `json:"rateCents"`
	HoursPerPoint   float64 `json:"hoursPerPoint"`
	HourlyRateCents int     `json:"hourlyRateCents"`
	// RateBasis ("effort" | "hours") + ProjectIDs are the frontend's contract: when
	// basis=="hours" we bill points as hours (1 point ≈ 1 hour) at the entered rate.
	RateBasis  string   `json:"rateBasis"`
	ProjectIDs []string `json:"projectIds"`

	DiscountCents int     `json:"discountCents"`
	TaxCents      int     `json:"taxCents"`
	TaxRate       float64 `json:"taxRate"`
	Notes         string  `json:"notes"`

	// Preview=true returns the would-be lines + header without persisting.
	Preview bool `json:"preview"`
}

func (h *invoiceHandlers) fromGit(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "org context required")
		return
	}
	if !h.requireManager(w, r, orgID) {
		return
	}
	var req fromGitRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Frontend contract reconciliation: accept projectIds[] (use the first) and
	// rateBasis=="hours" (bill points as hours at the entered rate, 1 point ≈ 1h).
	if req.ProjectID == "" && len(req.ProjectIDs) > 0 {
		req.ProjectID = req.ProjectIDs[0]
	}
	if strings.EqualFold(req.RateBasis, "hours") && req.HoursPerPoint == 0 {
		req.HoursPerPoint = 1
		if req.HourlyRateCents == 0 && req.RateCents != nil {
			req.HourlyRateCents = *req.RateCents
		}
	}
	from, to, ok := parseInvoicePeriod(req.From, req.To)
	if !ok {
		writeError(w, http.StatusBadRequest, "from/to must be valid dates with from <= to")
		return
	}

	clientID := nullableUUID(req.ClientID)
	projectID := nullableUUID(req.ProjectID)

	var (
		lines  []store.ClientInvoiceLine
		header *store.ClientInvoice
	)

	err := withInvoiceNumberRetry(func() error {
		return h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
			// Resolve the per-point rate: explicit > client default > schema default.
			rate := 15000
			if clientID != nil {
				cs, e := store.ListClients(r.Context(), tx, orgID)
				if e != nil {
					return e
				}
				for _, c := range cs {
					if c.ID == *clientID {
						rate = c.RateCents
						break
					}
				}
			}
			if req.RateCents != nil && *req.RateCents > 0 {
				rate = *req.RateCents
			}

			eff, e := store.MergedPREffort(r.Context(), tx, orgID, store.MergedEffortInput{
				ProjectID: derefStr(projectID),
				From:      from,
				To:        to,
			})
			if e != nil {
				return e
			}
			lines = buildLinesFromGit(eff, rate, req.HoursPerPoint, req.HourlyRateCents)

			if req.Preview {
				subtotal := 0
				for _, l := range lines {
					subtotal += l.AmountCents
				}
				base := subtotal - req.DiscountCents
				if base < 0 {
					base = 0
				}
				tax := req.TaxCents
				if tax == 0 && req.TaxRate > 0 {
					tax = int(math.Round(float64(base) * req.TaxRate / 100))
				}
				header = &store.ClientInvoice{
					ClientID:      clientID,
					ProjectID:     projectID,
					Number:        "(draft)",
					Status:        "draft",
					PeriodStart:   from,
					PeriodEnd:     to,
					Currency:      "USD",
					SubtotalCents: subtotal,
					DiscountCents: req.DiscountCents,
					TaxCents:      tax,
					TaxRate:       req.TaxRate,
					TotalCents:    base + tax,
					Notes:         req.Notes,
				}
				return nil
			}

			num, e := store.NextClientInvoiceNumber(r.Context(), tx, orgID, from.Year())
			if e != nil {
				return e
			}
			inv, e := store.CreateClientInvoice(r.Context(), tx, orgID, store.CreateClientInvoiceInput{
				ClientID:      clientID,
				ProjectID:     projectID,
				Number:        num,
				PeriodStart:   from,
				PeriodEnd:     to,
				Currency:      "USD",
				Notes:         req.Notes,
				DiscountCents: req.DiscountCents,
				TaxCents:      req.TaxCents,
				TaxRate:       req.TaxRate,
				Lines:         lines,
			})
			header = inv
			return e
		})
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not build invoice from git: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, invoiceDetail{
		ClientInvoice: header,
		Lines:         linesWithoutID(lines),
	})
}

// buildLinesFromGit groups merged-PR effort into per-repo (per-area) billable
// lines. When hoursPerPoint>0, each line bills points×hoursPerPoint hours at
// hourlyRateCents (quantity = hours, unit = hourly rate); otherwise it bills
// points directly at perPointRateCents (quantity = points, unit = per-point rate).
// Every line is source "git" and carries its PRs as Evidence.
func buildLinesFromGit(eff []store.EffortLine, perPointRateCents int, hoursPerPoint float64, hourlyRateCents int) []store.ClientInvoiceLine {
	type group struct {
		repo     string
		points   float64
		count    int
		evidence []store.EvidenceItem
	}
	byRepo := map[string]*group{}
	order := []string{}
	for _, e := range eff {
		g := byRepo[e.Repo]
		if g == nil {
			g = &group{repo: e.Repo}
			byRepo[e.Repo] = g
			order = append(order, e.Repo)
		}
		pts := e.Difficulty
		if pts <= 0 {
			pts = 1 // baseline for unestimated delivered work
		}
		g.points += pts
		g.count++
		g.evidence = append(g.evidence, store.EvidenceItem{
			PRTitle:  e.PRTitle,
			Repo:     e.Repo,
			MergedAt: e.MergedAt.Format(time.RFC3339),
			SHA:      e.SHA,
		})
	}
	sort.Strings(order)

	hourly := hoursPerPoint > 0 && hourlyRateCents > 0
	lines := make([]store.ClientInvoiceLine, 0, len(order))
	for _, repo := range order {
		g := byRepo[repo]
		pts := math.Round(g.points*10) / 10
		noun := "merged PR"
		if g.count != 1 {
			noun = "merged PRs"
		}

		var qty float64
		var unit, amount int
		var desc string
		if hourly {
			hours := math.Round(pts*hoursPerPoint*10) / 10
			qty = hours
			unit = hourlyRateCents
			amount = int(math.Round(hours * float64(hourlyRateCents)))
			desc = fmt.Sprintf("%s — %d %s delivered (%.1f effort pts → %.1f hrs)", repo, g.count, noun, pts, hours)
		} else {
			qty = pts
			unit = perPointRateCents
			amount = int(math.Round(pts * float64(perPointRateCents)))
			desc = fmt.Sprintf("%s — %d %s delivered (%.1f effort pts)", repo, g.count, noun, pts)
		}

		lines = append(lines, store.ClientInvoiceLine{
			Source:        "git",
			Description:   desc,
			EffortPoints:  pts,
			Quantity:      qty,
			UnitRateCents: unit,
			AmountCents:   amount,
			Evidence:      g.evidence,
		})
	}
	return lines
}

// buildLines groups merged-PR effort into per-repo invoice line items. Each
// line's effort_points = summed difficulty, amount = round(effort × rate), and
// evidence = the actual PRs ([{prTitle, repo, mergedAt, sha}]). PRs with no LLM
// estimate contribute a baseline of 1 effort point so delivered-but-unestimated
// work still bills (and is auditable via its evidence).
func buildLines(eff []store.EffortLine, rateCents int) []store.ClientInvoiceLine {
	type group struct {
		repo     string
		points   float64
		count    int
		evidence []store.EvidenceItem
	}
	byRepo := map[string]*group{}
	order := []string{}
	for _, e := range eff {
		g := byRepo[e.Repo]
		if g == nil {
			g = &group{repo: e.Repo}
			byRepo[e.Repo] = g
			order = append(order, e.Repo)
		}
		pts := e.Difficulty
		if pts <= 0 {
			pts = 1 // baseline for unestimated delivered work
		}
		g.points += pts
		g.count++
		g.evidence = append(g.evidence, store.EvidenceItem{
			PRTitle:  e.PRTitle,
			Repo:     e.Repo,
			MergedAt: e.MergedAt.Format(time.RFC3339),
			SHA:      e.SHA,
		})
	}
	sort.Strings(order)

	lines := make([]store.ClientInvoiceLine, 0, len(order))
	for _, repo := range order {
		g := byRepo[repo]
		// round effort to one decimal for a tidy invoice; price off the rounded value.
		pts := math.Round(g.points*10) / 10
		amount := int(math.Round(pts * float64(rateCents)))
		noun := "merged PR"
		if g.count != 1 {
			noun = "merged PRs"
		}
		lines = append(lines, store.ClientInvoiceLine{
			Source:        "git",
			Description:   fmt.Sprintf("%s — %d %s delivered", repo, g.count, noun),
			EffortPoints:  pts,
			Quantity:      1,
			UnitRateCents: rateCents,
			AmountCents:   amount,
			Evidence:      g.evidence,
		})
	}
	return lines
}

// linesWithoutID converts store lines into a slice with stable shape for the
// preview response (no DB ids yet).
func linesWithoutID(lines []store.ClientInvoiceLine) []*store.ClientInvoiceLine {
	out := make([]*store.ClientInvoiceLine, 0, len(lines))
	for i := range lines {
		l := lines[i]
		if l.Evidence == nil {
			l.Evidence = []store.EvidenceItem{}
		}
		out = append(out, &l)
	}
	return out
}

// ── Patch / status / share ──────────────────────────────────────────────────────

type patchInvoiceRequest struct {
	Status        *string  `json:"status"`
	Notes         *string  `json:"notes"`
	DiscountCents *int     `json:"discountCents"`
	TaxCents      *int     `json:"taxCents"`
	TaxRate       *float64 `json:"taxRate"`
}

var validStatuses = map[string]bool{"draft": true, "sent": true, "paid": true, "void": true}

func (h *invoiceHandlers) patchInvoice(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "org context required")
		return
	}
	if !h.requireManager(w, r, orgID) {
		return
	}
	id := r.PathValue("id")
	var req patchInvoiceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Status != nil && !validStatuses[*req.Status] {
		writeError(w, http.StatusBadRequest, "status must be one of draft|sent|paid|void")
		return
	}

	var out *store.ClientInvoice
	err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		// When moving to 'sent', mint a share token if one doesn't exist yet.
		if req.Status != nil && *req.Status == "sent" {
			cur, e := store.GetClientInvoice(r.Context(), tx, orgID, id)
			if e != nil {
				return e
			}
			if cur.ShareToken == nil || *cur.ShareToken == "" {
				tok, e := generateShareToken()
				if e != nil {
					return e
				}
				if e := store.SetClientInvoiceShareToken(r.Context(), tx, orgID, id, tok); e != nil {
					return e
				}
			}
		}
		inv, e := store.UpdateClientInvoice(r.Context(), tx, orgID, id, store.ClientInvoicePatch{
			Status:        req.Status,
			Notes:         req.Notes,
			DiscountCents: req.DiscountCents,
			TaxCents:      req.TaxCents,
			TaxRate:       req.TaxRate,
		})
		out = inv
		return e
	})
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "invoice not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not update invoice")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *invoiceHandlers) deleteInvoice(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "org context required")
		return
	}
	if !h.requireManager(w, r, orgID) {
		return
	}
	id := r.PathValue("id")
	err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		return store.DeleteClientInvoice(r.Context(), tx, orgID, id)
	})
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "invoice not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete invoice")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Public share route ──────────────────────────────────────────────────────────

// RegisterPublicInvoiceRoute wires the UNAUTHENTICATED, token-scoped public
// invoice view. The orchestrator registers this on the public mux.
//
//	GET /api/public/invoices/{token}
//
// The org is resolved from the token row (bypassing RLS via the pool), then the
// invoice + lines are read in a one-off db.WithOrg for that org_id — so only the
// single invoice tied to the token is ever exposed. No other org data leaks.
func RegisterPublicInvoiceRoute(mux *http.ServeMux, database *db.DB) {
	mux.HandleFunc("GET /api/public/invoices/{token}", func(w http.ResponseWriter, r *http.Request) {
		token := r.PathValue("token")
		if token == "" {
			writeError(w, http.StatusNotFound, "not found")
			return
		}

		orgID, invoiceID, err := store.ClientInvoiceOrgByToken(r.Context(), database.Pool(), token)
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "invoice not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not load invoice")
			return
		}

		var detail invoiceDetail
		detail.Lines = []*store.ClientInvoiceLine{}
		err = database.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
			inv, e := store.GetClientInvoice(r.Context(), tx, orgID, invoiceID)
			if e != nil {
				return e
			}
			// Never expose the share token itself in the public payload.
			inv.ShareToken = nil
			detail.ClientInvoice = inv
			lines, e := store.GetClientInvoiceLines(r.Context(), tx, orgID, invoiceID)
			if lines != nil {
				detail.Lines = lines
			}
			return e
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not load invoice")
			return
		}
		writeJSON(w, http.StatusOK, detail)
	})
}

// ── helpers ─────────────────────────────────────────────────────────────────────

// generateShareToken returns a 32-byte URL-safe random token (unguessable).
func generateShareToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// parsePeriod parses YYYY-MM-DD (or RFC3339) from/to and validates ordering.
func parseInvoicePeriod(from, to string) (time.Time, time.Time, bool) {
	f, ok1 := parseDate(from)
	t, ok2 := parseDate(to)
	if !ok1 || !ok2 || t.Before(f) {
		return time.Time{}, time.Time{}, false
	}
	// Make the window inclusive of the whole end day.
	t = t.Add(24*time.Hour - time.Second)
	return f, t, true
}

func parseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339} {
		if d, err := time.Parse(layout, s); err == nil {
			return d.UTC(), true
		}
	}
	return time.Time{}, false
}

func nullableUUID(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// toStoreLines maps request lines to store lines. A line is "manual" (free-form,
// no evidence, amount = quantity × unitRate) or "git" (default; amount falls back
// to effortPoints × unitRate and evidence is preserved).
func toStoreLines(in []invoiceLineInput) []store.ClientInvoiceLine {
	out := make([]store.ClientInvoiceLine, 0, len(in))
	for _, l := range in {
		qty := l.Quantity
		if qty == 0 {
			qty = 1
		}
		source := l.Source
		if source != "manual" {
			source = "git"
		}
		amount := l.AmountCents
		if amount == 0 && l.UnitRateCents != 0 {
			if source == "manual" {
				amount = int(math.Round(qty * float64(l.UnitRateCents)))
			} else {
				amount = int(math.Round(l.EffortPoints * float64(l.UnitRateCents)))
			}
		}
		ev := l.Evidence
		if source == "manual" || ev == nil {
			ev = []store.EvidenceItem{}
		}
		out = append(out, store.ClientInvoiceLine{
			Source:        source,
			Description:   l.Description,
			EffortPoints:  l.EffortPoints,
			Quantity:      qty,
			UnitRateCents: l.UnitRateCents,
			AmountCents:   amount,
			Evidence:      ev,
		})
	}
	return out
}
