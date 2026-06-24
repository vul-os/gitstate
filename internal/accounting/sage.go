package accounting

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/exo/gitstate/internal/config"
)

// Sage Business Cloud Accounting API endpoints (isolated as consts so they are
// swappable / mockable).
const (
	sageAuthorizeURL = "https://www.sageone.com/oauth2/auth/central"
	sageTokenURL     = "https://oauth.accounting.sage.com/token"
	// sageAPIBase is the production REST base; resources are appended.
	sageAPIBase = "https://api.accounting.sage.com/v3.1"
	// sageInvoiceWebBase is the customer-facing deep-link base for a created
	// sales invoice (the API does not return a UI URL).
	sageInvoiceWebBase = "https://accounts.sageone.com/sales/sales_invoices/"

	sageScopes = "full_access"
)

// sageProvider implements Provider against the Sage Business Cloud Accounting API.
type sageProvider struct {
	clientID     string
	clientSecret string
	redirectURL  string
	// endpoints are overridable in tests; default to the consts above.
	authorizeURL, tokenURL, apiBase string
}

// NewSage builds a Sage provider from the configured OAuth credentials.
func NewSage(creds config.OAuthCreds, publicURL string) Provider {
	return &sageProvider{
		clientID:     creds.ClientID,
		clientSecret: creds.ClientSecret,
		redirectURL:  callbackURL(publicURL, "sage"),
		authorizeURL: sageAuthorizeURL,
		tokenURL:     sageTokenURL,
		apiBase:      sageAPIBase,
	}
}

func (p *sageProvider) Name() string   { return "sage" }
func (p *sageProvider) Scopes() string { return sageScopes }

func (p *sageProvider) AuthCodeURL(state string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", p.clientID)
	q.Set("redirect_uri", p.redirectURL)
	q.Set("scope", sageScopes)
	q.Set("state", state)
	// Sage's central auth selects the country/region; default to the gb broker.
	q.Set("country", "gb")
	return p.authorizeURL + "?" + q.Encode()
}

func (p *sageProvider) Exchange(ctx context.Context, code string, _ map[string]string) (Tokens, string, string, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", p.redirectURL)
	// Sage's token endpoint accepts the client credentials in the body as well as
	// Basic auth; Basic auth keeps the secret out of any logged form body.
	tr, err := postForm(ctx, p.tokenURL, basicAuthHeader(p.clientID, p.clientSecret), form)
	if err != nil {
		return Tokens{}, "", "", fmt.Errorf("sage: exchange: %w", err)
	}
	tok := tr.tokens()

	bizID, bizName, err := p.fetchBusiness(ctx, tok.AccessToken)
	if err != nil {
		return Tokens{}, "", "", fmt.Errorf("sage: resolve business: %w", err)
	}
	return tok, bizID, bizName, nil
}

func (p *sageProvider) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)

	tr, err := postForm(ctx, p.tokenURL, basicAuthHeader(p.clientID, p.clientSecret), form)
	if err != nil {
		return Tokens{}, fmt.Errorf("sage: refresh: %w", err)
	}
	tok := tr.tokens()
	// Sage rotates refresh tokens; carry the old one forward if a new one is absent.
	if tok.RefreshToken == "" {
		tok.RefreshToken = refreshToken
	}
	return tok, nil
}

// sageBusinessList is the relevant slice of GET /businesses (the businesses the
// token can act on; the id is used as the X-Business context on later calls).
type sageBusinessList struct {
	Items []struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayed_as"`
		Name        string `json:"name"`
	} `json:"$items"`
}

func (p *sageProvider) fetchBusiness(ctx context.Context, accessToken string) (id, name string, err error) {
	var out sageBusinessList
	if err := doJSON(ctx, "GET", p.apiBase+"/businesses", accessToken, nil, &out, nil); err != nil {
		return "", "", err
	}
	if len(out.Items) == 0 {
		return "", "", fmt.Errorf("sage: no business connected to this token")
	}
	b := out.Items[0]
	name = b.DisplayName
	if name == "" {
		name = b.Name
	}
	return b.ID, name, nil
}

// ── Invoice creation ──────────────────────────────────────────────────────────

// sageInvoiceRequest is the POST /sales_invoices payload (nested under
// "sales_invoice", which is Sage's convention).
type sageInvoiceRequest struct {
	SalesInvoice sageInvoicePayload `json:"sales_invoice"`
}

type sageInvoicePayload struct {
	ContactName  string            `json:"contact_name"`
	Date         string            `json:"date"`
	Reference    string            `json:"reference,omitempty"`
	CurrencyID   string            `json:"currency_id,omitempty"`
	InvoiceLines []sageInvoiceLine `json:"invoice_lines"`
}

type sageInvoiceLine struct {
	Description string  `json:"description"`
	Quantity    float64 `json:"quantity"`
	UnitPrice   float64 `json:"unit_price"`
}

// sageInvoiceResponse is the relevant slice of the POST /sales_invoices response.
type sageInvoiceResponse struct {
	ID          string `json:"id"`
	DisplayedAs string `json:"displayed_as"`
}

func (p *sageProvider) CreateInvoice(ctx context.Context, tok Tokens, externalOrgID string, inv Invoice) (string, string, error) {
	if externalOrgID == "" {
		return "", "", fmt.Errorf("sage: missing business id")
	}
	payload := sageInvoiceRequest{SalesInvoice: sageInvoicePayload{
		ContactName:  inv.ContactName,
		Date:         time.Now().UTC().Format("2006-01-02"),
		Reference:    inv.Reference,
		CurrencyID:   inv.Currency,
		InvoiceLines: sageLines(inv.Lines),
	}}

	// Sage scopes a request to a business via the X-Business header.
	headers := map[string]string{"X-Business": externalOrgID}
	var out sageInvoiceResponse
	if err := doJSON(ctx, "POST", p.apiBase+"/sales_invoices", tok.AccessToken, payload, &out, headers); err != nil {
		return "", "", fmt.Errorf("sage: create invoice: %w", err)
	}
	if out.ID == "" {
		return "", "", fmt.Errorf("sage: create invoice: empty response")
	}
	return out.ID, sageInvoiceWebBase + out.ID, nil
}

func sageLines(lines []InvoiceLine) []sageInvoiceLine {
	out := make([]sageInvoiceLine, 0, len(lines))
	for _, l := range lines {
		qty := l.Quantity
		if qty == 0 {
			qty = 1
		}
		unit := l.UnitAmount
		if unit == 0 && qty != 0 {
			unit = l.Amount / qty
		}
		out = append(out, sageInvoiceLine{
			Description: l.Description,
			Quantity:    qty,
			UnitPrice:   unit,
		})
	}
	return out
}
