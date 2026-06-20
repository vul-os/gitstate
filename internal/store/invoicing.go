// Package store — invoicing.go
// Org-scoped queries for client invoicing DERIVED FROM GIT effort.
// Tables: clients, client_invoices, client_invoice_lines (migration 011).
// Every read/write runs inside db.WithOrg (pgx.Tx) so RLS enforces the org
// boundary. The public share path reads via a one-off WithOrg using the
// token row's org_id (see ClientInvoiceOrgByToken).
//
// Types/funcs are Client*-prefixed to avoid colliding with the unrelated
// per-seat billing store (billing.go also has Invoice/InvoiceLine).
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// pgUniqueViolation is the SQLSTATE for a unique_violation.
const pgUniqueViolation = "23505"

// IsUniqueViolation reports whether err is a Postgres unique_violation (23505).
// Used by callers that auto-number rows under a UNIQUE constraint to detect a
// lost race and retry with a freshly-computed number.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}

// ── Types ──────────────────────────────────────────────────────────────────────

// Client is a billable client of the org.
type Client struct {
	ID           string    `json:"id"`
	OrgID        string    `json:"orgId"`
	Name         string    `json:"name"`
	ContactEmail string    `json:"contactEmail"`
	RateCents    int       `json:"rateCents"`
	Notes        string    `json:"notes"`
	CreatedAt    time.Time `json:"createdAt"`
}

// ClientInvoice mirrors a client_invoices row (header only; lines fetched separately).
type ClientInvoice struct {
	ID            string     `json:"id"`
	OrgID         string     `json:"orgId"`
	ClientID      *string    `json:"clientId"`
	ProjectID     *string    `json:"projectId"`
	Number        string     `json:"number"`
	Status        string     `json:"status"`
	PeriodStart   time.Time  `json:"periodStart"`
	PeriodEnd     time.Time  `json:"periodEnd"`
	Currency      string     `json:"currency"`
	SubtotalCents int        `json:"subtotalCents"`
	TotalCents    int        `json:"totalCents"`
	ShareToken    *string    `json:"shareToken,omitempty"`
	Notes         string     `json:"notes"`
	IssuedAt      *time.Time `json:"issuedAt"`
	CreatedAt     time.Time  `json:"createdAt"`

	// Joined display fields (best-effort; empty when not joined).
	ClientName  string `json:"clientName,omitempty"`
	ProjectName string `json:"projectName,omitempty"`
}

// ClientInvoiceLine mirrors a client_invoice_lines row. Evidence is the raw git
// proof ([{prTitle, repo, mergedAt, sha}]).
type ClientInvoiceLine struct {
	ID            string         `json:"id"`
	InvoiceID     string         `json:"invoiceId"`
	Description   string         `json:"description"`
	EffortPoints  float64        `json:"effortPoints"`
	Quantity      float64        `json:"quantity"`
	UnitRateCents int            `json:"unitRateCents"`
	AmountCents   int            `json:"amountCents"`
	Evidence      []EvidenceItem `json:"evidence"`
	Sort          int            `json:"sort"`
}

// EvidenceItem is one piece of git proof backing a line item.
type EvidenceItem struct {
	PRTitle  string `json:"prTitle"`
	Repo     string `json:"repo"`
	MergedAt string `json:"mergedAt"`
	SHA      string `json:"sha"`
}

// ── Clients CRUD ────────────────────────────────────────────────────────────────

// ListClients returns all clients for an org, sorted by name.
func ListClients(ctx context.Context, tx pgx.Tx, orgID string) ([]*Client, error) {
	const q = `
		SELECT id, org_id, name, COALESCE(contact_email,''), rate_cents, COALESCE(notes,''), created_at
		FROM clients
		WHERE org_id = $1
		ORDER BY name`
	rows, err := tx.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("store.invoicing: list clients: %w", err)
	}
	defer rows.Close()
	var out []*Client
	for rows.Next() {
		c := &Client{}
		if err := rows.Scan(&c.ID, &c.OrgID, &c.Name, &c.ContactEmail, &c.RateCents, &c.Notes, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("store.invoicing: scan client: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CreateClient inserts a new client.
func CreateClient(ctx context.Context, tx pgx.Tx, orgID, name, email string, rateCents int, notes string) (*Client, error) {
	const q = `
		INSERT INTO clients (org_id, name, contact_email, rate_cents, notes)
		VALUES ($1, $2, NULLIF($3,''), $4, NULLIF($5,''))
		RETURNING id, org_id, name, COALESCE(contact_email,''), rate_cents, COALESCE(notes,''), created_at`
	c := &Client{}
	err := tx.QueryRow(ctx, q, orgID, name, email, rateCents, notes).Scan(
		&c.ID, &c.OrgID, &c.Name, &c.ContactEmail, &c.RateCents, &c.Notes, &c.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("store.invoicing: create client: %w", err)
	}
	return c, nil
}

// ClientPatch carries optional client field updates (nil = unchanged).
type ClientPatch struct {
	Name         *string
	ContactEmail *string
	RateCents    *int
	Notes        *string
}

// UpdateClient applies a partial update to a client.
func UpdateClient(ctx context.Context, tx pgx.Tx, orgID, id string, p ClientPatch) (*Client, error) {
	const q = `
		UPDATE clients SET
			name          = COALESCE($3, name),
			contact_email = COALESCE($4, contact_email),
			rate_cents    = COALESCE($5, rate_cents),
			notes         = COALESCE($6, notes)
		WHERE org_id = $1 AND id = $2
		RETURNING id, org_id, name, COALESCE(contact_email,''), rate_cents, COALESCE(notes,''), created_at`
	c := &Client{}
	err := tx.QueryRow(ctx, q, orgID, id, p.Name, p.ContactEmail, p.RateCents, p.Notes).Scan(
		&c.ID, &c.OrgID, &c.Name, &c.ContactEmail, &c.RateCents, &c.Notes, &c.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store.invoicing: update client: %w", err)
	}
	return c, nil
}

// ── Invoices ────────────────────────────────────────────────────────────────────

// ListClientInvoices returns invoice headers for an org (no lines), newest
// first, with joined client/project names for display.
func ListClientInvoices(ctx context.Context, tx pgx.Tx, orgID string) ([]*ClientInvoice, error) {
	const q = invoiceSelect + ` WHERE i.org_id = $1 ORDER BY i.created_at DESC`
	rows, err := tx.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("store.invoicing: list invoices: %w", err)
	}
	defer rows.Close()
	var out []*ClientInvoice
	for rows.Next() {
		inv := &ClientInvoice{}
		if err := scanClientInvoice(rows, inv); err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// GetClientInvoice returns a single invoice header (with joined names) by id.
func GetClientInvoice(ctx context.Context, tx pgx.Tx, orgID, id string) (*ClientInvoice, error) {
	const q = invoiceSelect + ` WHERE i.org_id = $1 AND i.id = $2`
	inv := &ClientInvoice{}
	if err := scanClientInvoice(tx.QueryRow(ctx, q, orgID, id), inv); err != nil {
		return nil, err
	}
	return inv, nil
}

const invoiceSelect = `
	SELECT i.id, i.org_id, i.client_id::text, i.project_id::text, i.number, i.status,
	       i.period_start, i.period_end, i.currency, i.subtotal_cents, i.total_cents,
	       i.share_token, COALESCE(i.notes,''), i.issued_at, i.created_at,
	       COALESCE(c.name,''), COALESCE(p.name,'')
	FROM client_invoices i
	LEFT JOIN clients  c ON c.id = i.client_id
	LEFT JOIN projects p ON p.id = i.project_id`

// GetClientInvoiceLines returns the line items for an invoice, in sort order.
func GetClientInvoiceLines(ctx context.Context, tx pgx.Tx, orgID, invoiceID string) ([]*ClientInvoiceLine, error) {
	const q = `
		SELECT id, invoice_id::text, description, effort_points, quantity,
		       unit_rate_cents, amount_cents, evidence, sort
		FROM client_invoice_lines
		WHERE org_id = $1 AND invoice_id = $2
		ORDER BY sort, id`
	rows, err := tx.Query(ctx, q, orgID, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("store.invoicing: list lines: %w", err)
	}
	defer rows.Close()
	var out []*ClientInvoiceLine
	for rows.Next() {
		l := &ClientInvoiceLine{}
		var ev []byte
		if err := rows.Scan(&l.ID, &l.InvoiceID, &l.Description, &l.EffortPoints,
			&l.Quantity, &l.UnitRateCents, &l.AmountCents, &ev, &l.Sort); err != nil {
			return nil, fmt.Errorf("store.invoicing: scan line: %w", err)
		}
		l.Evidence = decodeEvidence(ev)
		out = append(out, l)
	}
	return out, rows.Err()
}

// CreateClientInvoiceInput holds the data needed to persist a draft invoice + lines.
type CreateClientInvoiceInput struct {
	ClientID    *string
	ProjectID   *string
	Number      string
	PeriodStart time.Time
	PeriodEnd   time.Time
	Currency    string
	Notes       string
	Lines       []ClientInvoiceLine
}

// CreateClientInvoice inserts an invoice header and its lines in one tx,
// computing subtotal/total from the lines. Status is always 'draft' on creation.
func CreateClientInvoice(ctx context.Context, tx pgx.Tx, orgID string, in CreateClientInvoiceInput) (*ClientInvoice, error) {
	subtotal := 0
	for _, l := range in.Lines {
		subtotal += l.AmountCents
	}
	currency := in.Currency
	if currency == "" {
		currency = "USD"
	}

	const insHdr = `
		INSERT INTO client_invoices
			(org_id, client_id, project_id, number, status, period_start, period_end,
			 currency, subtotal_cents, total_cents, notes)
		VALUES ($1, $2, $3, $4, 'draft', $5, $6, $7, $8, $8, NULLIF($9,''))
		RETURNING id`
	var invoiceID string
	if err := tx.QueryRow(ctx, insHdr, orgID, in.ClientID, in.ProjectID, in.Number,
		in.PeriodStart, in.PeriodEnd, currency, subtotal, in.Notes).Scan(&invoiceID); err != nil {
		return nil, fmt.Errorf("store.invoicing: insert invoice: %w", err)
	}

	if err := insertClientInvoiceLines(ctx, tx, orgID, invoiceID, in.Lines); err != nil {
		return nil, err
	}

	return GetClientInvoice(ctx, tx, orgID, invoiceID)
}

func insertClientInvoiceLines(ctx context.Context, tx pgx.Tx, orgID, invoiceID string, lines []ClientInvoiceLine) error {
	if len(lines) == 0 {
		return nil
	}
	const insLine = `
		INSERT INTO client_invoice_lines
			(org_id, invoice_id, description, effort_points, quantity,
			 unit_rate_cents, amount_cents, evidence, sort)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	batch := &pgx.Batch{}
	for i, l := range lines {
		ev, _ := json.Marshal(l.Evidence)
		if len(ev) == 0 {
			ev = []byte("[]")
		}
		qty := l.Quantity
		if qty == 0 {
			qty = 1
		}
		batch.Queue(insLine, orgID, invoiceID, l.Description, l.EffortPoints, qty,
			l.UnitRateCents, l.AmountCents, ev, i)
	}
	br := tx.SendBatch(ctx, batch)
	defer br.Close()
	for range lines {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("store.invoicing: insert line: %w", err)
		}
	}
	return nil
}

// ClientInvoicePatch carries optional invoice updates (nil = unchanged).
type ClientInvoicePatch struct {
	Status *string
	Notes  *string
}

// UpdateClientInvoice applies a partial update to an invoice header. issued_at
// is stamped the first time the invoice is marked 'sent'. Returns the refreshed
// header.
func UpdateClientInvoice(ctx context.Context, tx pgx.Tx, orgID, id string, p ClientInvoicePatch) (*ClientInvoice, error) {
	const q = `
		UPDATE client_invoices SET
			status    = COALESCE($3, status),
			notes     = COALESCE($4, notes),
			issued_at = CASE
			    WHEN $3 = 'sent' AND issued_at IS NULL THEN now()
			    ELSE issued_at
			END
		WHERE org_id = $1 AND id = $2
		RETURNING id`
	var got string
	if err := tx.QueryRow(ctx, q, orgID, id, p.Status, p.Notes).Scan(&got); err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store.invoicing: update invoice: %w", err)
	}
	return GetClientInvoice(ctx, tx, orgID, id)
}

// SetClientInvoiceShareToken sets (or clears) the public share token on an invoice.
func SetClientInvoiceShareToken(ctx context.Context, tx pgx.Tx, orgID, id, token string) error {
	const q = `UPDATE client_invoices SET share_token = NULLIF($3,'') WHERE org_id = $1 AND id = $2`
	ct, err := tx.Exec(ctx, q, orgID, id, token)
	if err != nil {
		return fmt.Errorf("store.invoicing: set share token: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteClientInvoice removes an invoice (lines cascade via FK).
func DeleteClientInvoice(ctx context.Context, tx pgx.Tx, orgID, id string) error {
	const q = `DELETE FROM client_invoices WHERE org_id = $1 AND id = $2`
	ct, err := tx.Exec(ctx, q, orgID, id)
	if err != nil {
		return fmt.Errorf("store.invoicing: delete invoice: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ── Auto-numbering ──────────────────────────────────────────────────────────────

// NextClientInvoiceNumber returns the next INV-YYYY-NNN number for the org/year,
// based on the highest existing sequence for that year. Must run in WithOrg.
func NextClientInvoiceNumber(ctx context.Context, tx pgx.Tx, orgID string, year int) (string, error) {
	prefix := fmt.Sprintf("INV-%d-", year)
	const q = `
		SELECT COALESCE(MAX( (regexp_replace(number, '^.*-', ''))::int ), 0)
		FROM client_invoices
		WHERE org_id = $1 AND number LIKE $2`
	var maxN int
	if err := tx.QueryRow(ctx, q, orgID, prefix+"%").Scan(&maxN); err != nil {
		return "", fmt.Errorf("store.invoicing: next number: %w", err)
	}
	return fmt.Sprintf("%s%03d", prefix, maxN+1), nil
}

// ── Generate-from-git source data ───────────────────────────────────────────────

// EffortLine is one merged PR joined with its LLM effort estimate + repo, the
// raw material for a generated invoice line.
type EffortLine struct {
	Repo       string
	PRTitle    string
	SHA        string
	MergedAt   time.Time
	Difficulty float64
}

// MergedEffortInput narrows the merged-PR effort query.
type MergedEffortInput struct {
	ProjectID string    // optional — restrict to repos that this project's issues touch
	From      time.Time // window start (merged_at >= From)
	To        time.Time // window end   (merged_at <= To)
}

// MergedPREffort returns one row per merged PR in the window, joined to its repo
// and (if present) its latest LLM effort estimate. When ProjectID is set,
// results are restricted to repos that the project's issues reference (the only
// project→repo linkage in the schema). Must run inside db.WithOrg.
//
// The difficulty (1–10) IS the effort signal. PRs merged in-window with no
// estimate come back with difficulty 0 so unestimated delivered work is still
// visible — the API decides how to price them.
func MergedPREffort(ctx context.Context, tx pgx.Tx, orgID string, in MergedEffortInput) ([]EffortLine, error) {
	args := []any{orgID, in.From, in.To}
	q := `
		SELECT r.full_name,
		       COALESCE(pr.title, '') AS title,
		       COALESCE(
		         (SELECT c.sha FROM commits c
		            WHERE c.org_id = pr.org_id AND c.repo_id = pr.repo_id
		            ORDER BY c.committed_at DESC LIMIT 1), '') AS sha,
		       pr.merged_at,
		       COALESCE(ee.difficulty, 0) AS difficulty
		FROM pull_requests pr
		JOIN repos r ON r.id = pr.repo_id
		LEFT JOIN LATERAL (
		    SELECT difficulty FROM effort_estimates e
		    WHERE e.org_id = pr.org_id AND e.pr_id = pr.id
		    ORDER BY e.created_at DESC LIMIT 1
		) ee ON true
		WHERE pr.org_id = $1
		  AND pr.state = 'merged'
		  AND pr.merged_at IS NOT NULL
		  AND pr.merged_at >= $2
		  AND pr.merged_at <= $3`

	if in.ProjectID != "" {
		args = append(args, in.ProjectID)
		q += fmt.Sprintf(`
		  AND pr.repo_id IN (
		      SELECT DISTINCT i.repo_id FROM issues i
		      WHERE i.org_id = pr.org_id AND i.project_id = $%d AND i.repo_id IS NOT NULL
		  )`, len(args))
	}
	q += `
		ORDER BY r.full_name, pr.merged_at`

	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store.invoicing: merged pr effort: %w", err)
	}
	defer rows.Close()
	var out []EffortLine
	for rows.Next() {
		var e EffortLine
		if err := rows.Scan(&e.Repo, &e.PRTitle, &e.SHA, &e.MergedAt, &e.Difficulty); err != nil {
			return nil, fmt.Errorf("store.invoicing: scan effort line: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ── Public share read ───────────────────────────────────────────────────────────

// ClientInvoiceOrgByToken resolves the org_id + invoice_id that owns a share
// token, bypassing RLS via the pool (the public endpoint has no org context).
// Returns ErrNotFound if the token is unknown. The caller then opens
// db.WithOrg(orgID, …) to read the invoice under RLS, so only that one org's
// row is ever visible.
func ClientInvoiceOrgByToken(ctx context.Context, q Querier, token string) (orgID, invoiceID string, err error) {
	// SECURITY DEFINER function bypasses RLS for this one pre-auth token lookup
	// (the app role can't read client_invoices without an org context set).
	const sql = `SELECT org_id::text, invoice_id::text FROM client_invoice_org_by_token($1)`
	rows, e := q.Query(ctx, sql, token)
	if e != nil {
		return "", "", fmt.Errorf("store.invoicing: resolve token: %w", e)
	}
	defer rows.Close()
	if !rows.Next() {
		return "", "", ErrNotFound
	}
	if e := rows.Scan(&orgID, &invoiceID); e != nil {
		return "", "", fmt.Errorf("store.invoicing: scan token: %w", e)
	}
	return orgID, invoiceID, nil
}

// ── scan helpers ────────────────────────────────────────────────────────────────

// invoiceScanner is satisfied by both pgx.Row and pgx.Rows.
type invoiceScanner interface {
	Scan(dest ...any) error
}

func scanClientInvoice(row invoiceScanner, inv *ClientInvoice) error {
	if err := row.Scan(
		&inv.ID, &inv.OrgID, &inv.ClientID, &inv.ProjectID, &inv.Number, &inv.Status,
		&inv.PeriodStart, &inv.PeriodEnd, &inv.Currency, &inv.SubtotalCents, &inv.TotalCents,
		&inv.ShareToken, &inv.Notes, &inv.IssuedAt, &inv.CreatedAt,
		&inv.ClientName, &inv.ProjectName,
	); err != nil {
		if err == pgx.ErrNoRows {
			return ErrNotFound
		}
		return fmt.Errorf("store.invoicing: scan invoice: %w", err)
	}
	return nil
}

func decodeEvidence(raw []byte) []EvidenceItem {
	if len(raw) == 0 {
		return []EvidenceItem{}
	}
	var out []EvidenceItem
	if err := json.Unmarshal(raw, &out); err != nil {
		return []EvidenceItem{}
	}
	if out == nil {
		return []EvidenceItem{}
	}
	return out
}
