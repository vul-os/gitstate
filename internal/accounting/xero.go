package accounting

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/exo/gitstate/internal/config"
)

// Xero API endpoints (isolated as consts so they are swappable / mockable).
const (
	xeroAuthorizeURL   = "https://login.xero.com/identity/connect/authorize"
	xeroTokenURL       = "https://identity.xero.com/connect/token"
	xeroConnectionsURL = "https://api.xero.com/connections"
	xeroInvoicesURL    = "https://api.xero.com/api.xro/2.0/Invoices"
	// xeroInvoiceWebBase is the customer-facing deep-link base for a created
	// invoice (the API does not return a UI URL).
	xeroInvoiceWebBase = "https://go.xero.com/app/invoices/edit/"

	xeroScopes = "openid accounting.transactions accounting.contacts offline_access"
)

// xeroProvider implements Provider against the Xero accounting API.
type xeroProvider struct {
	clientID     string
	clientSecret string
	redirectURL  string
	// endpoints are overridable in tests; default to the consts above.
	authorizeURL, tokenURL, connectionsURL, invoicesURL string
}

// NewXero builds a Xero provider from the configured OAuth credentials.
func NewXero(creds config.OAuthCreds, publicURL string) Provider {
	return &xeroProvider{
		clientID:       creds.ClientID,
		clientSecret:   creds.ClientSecret,
		redirectURL:    callbackURL(publicURL, "xero"),
		authorizeURL:   xeroAuthorizeURL,
		tokenURL:       xeroTokenURL,
		connectionsURL: xeroConnectionsURL,
		invoicesURL:    xeroInvoicesURL,
	}
}

func (p *xeroProvider) Name() string   { return "xero" }
func (p *xeroProvider) Scopes() string { return xeroScopes }

func (p *xeroProvider) AuthCodeURL(state string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", p.clientID)
	q.Set("redirect_uri", p.redirectURL)
	q.Set("scope", xeroScopes)
	q.Set("state", state)
	return p.authorizeURL + "?" + q.Encode()
}

func (p *xeroProvider) Exchange(ctx context.Context, code string, _ map[string]string) (Tokens, string, string, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", p.redirectURL)

	tr, err := postForm(ctx, p.tokenURL, basicAuthHeader(p.clientID, p.clientSecret), form)
	if err != nil {
		return Tokens{}, "", "", fmt.Errorf("xero: exchange: %w", err)
	}
	tok := tr.tokens()

	tenantID, tenantName, err := p.fetchTenant(ctx, tok.AccessToken)
	if err != nil {
		return Tokens{}, "", "", fmt.Errorf("xero: resolve tenant: %w", err)
	}
	return tok, tenantID, tenantName, nil
}

func (p *xeroProvider) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)

	tr, err := postForm(ctx, p.tokenURL, basicAuthHeader(p.clientID, p.clientSecret), form)
	if err != nil {
		return Tokens{}, fmt.Errorf("xero: refresh: %w", err)
	}
	tok := tr.tokens()
	// Xero rotates refresh tokens; carry the old one forward if a new one is absent.
	if tok.RefreshToken == "" {
		tok.RefreshToken = refreshToken
	}
	return tok, nil
}

// xeroConnection is one row of GET /connections (a tenant the token can act on).
type xeroConnection struct {
	TenantID   string `json:"tenantId"`
	TenantName string `json:"tenantName"`
	TenantType string `json:"tenantType"`
}

func (p *xeroProvider) fetchTenant(ctx context.Context, accessToken string) (id, name string, err error) {
	var conns []xeroConnection
	if err := doJSON(ctx, "GET", p.connectionsURL, accessToken, nil, &conns, nil); err != nil {
		return "", "", err
	}
	for _, c := range conns {
		if c.TenantType == "" || c.TenantType == "ORGANISATION" {
			return c.TenantID, c.TenantName, nil
		}
	}
	if len(conns) > 0 {
		return conns[0].TenantID, conns[0].TenantName, nil
	}
	return "", "", fmt.Errorf("xero: no organisation connected to this token")
}

// ── Invoice creation ──────────────────────────────────────────────────────────

// xeroInvoiceRequest is the POST /Invoices payload (a batch of one).
type xeroInvoiceRequest struct {
	Invoices []xeroInvoicePayload `json:"Invoices"`
}

type xeroInvoicePayload struct {
	Type            string         `json:"Type"` // ACCREC = accounts receivable (a sales invoice)
	Contact         xeroContact    `json:"Contact"`
	Date            string         `json:"Date,omitempty"`
	LineAmountTypes string         `json:"LineAmountTypes"` // Exclusive | Inclusive | NoTax
	Reference       string         `json:"Reference,omitempty"`
	CurrencyCode    string         `json:"CurrencyCode,omitempty"`
	Status          string         `json:"Status,omitempty"` // DRAFT
	LineItems       []xeroLineItem `json:"LineItems"`
}

type xeroContact struct {
	Name string `json:"Name"`
}

type xeroLineItem struct {
	Description string  `json:"Description"`
	Quantity    float64 `json:"Quantity"`
	UnitAmount  float64 `json:"UnitAmount"`
}

// xeroInvoiceResponse is the relevant slice of the POST /Invoices response.
type xeroInvoiceResponse struct {
	Invoices []struct {
		InvoiceID     string `json:"InvoiceID"`
		InvoiceNumber string `json:"InvoiceNumber"`
	} `json:"Invoices"`
}

func (p *xeroProvider) CreateInvoice(ctx context.Context, tok Tokens, externalOrgID string, inv Invoice) (string, string, error) {
	if externalOrgID == "" {
		return "", "", fmt.Errorf("xero: missing tenant id")
	}
	payload := xeroInvoiceRequest{Invoices: []xeroInvoicePayload{{
		Type:            "ACCREC",
		Contact:         xeroContact{Name: inv.ContactName},
		Date:            time.Now().UTC().Format("2006-01-02"),
		LineAmountTypes: "Exclusive",
		Reference:       inv.Reference,
		CurrencyCode:    inv.Currency,
		Status:          "DRAFT",
		LineItems:       xeroLines(inv.Lines),
	}}}

	headers := map[string]string{"Xero-tenant-id": externalOrgID}
	var out xeroInvoiceResponse
	if err := doJSON(ctx, "POST", p.invoicesURL, tok.AccessToken, payload, &out, headers); err != nil {
		return "", "", fmt.Errorf("xero: create invoice: %w", err)
	}
	if len(out.Invoices) == 0 || out.Invoices[0].InvoiceID == "" {
		return "", "", fmt.Errorf("xero: create invoice: empty response")
	}
	id := out.Invoices[0].InvoiceID
	return id, xeroInvoiceWebBase + id, nil
}

func xeroLines(lines []InvoiceLine) []xeroLineItem {
	out := make([]xeroLineItem, 0, len(lines))
	for _, l := range lines {
		qty := l.Quantity
		if qty == 0 {
			qty = 1
		}
		unit := l.UnitAmount
		if unit == 0 && qty != 0 {
			unit = l.Amount / qty
		}
		out = append(out, xeroLineItem{
			Description: l.Description,
			Quantity:    qty,
			UnitAmount:  unit,
		})
	}
	return out
}
