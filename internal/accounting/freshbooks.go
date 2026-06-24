package accounting

import (
	"context"
	"fmt"
	"net/url"

	"github.com/exo/gitstate/internal/config"
)

// FreshBooks API endpoints (isolated as consts so they are swappable / mockable).
const (
	freshbooksAuthorizeURL = "https://auth.freshbooks.com/oauth/authorize"
	freshbooksTokenURL     = "https://api.freshbooks.com/auth/oauth/token"
	// freshbooksAPIBase is the production API base; resources are appended.
	freshbooksAPIBase = "https://api.freshbooks.com"
	// freshbooksInvoiceWebBase is the customer-facing deep-link base for an invoice.
	freshbooksInvoiceWebBase = "https://my.freshbooks.com/#/invoice/"

	// FreshBooks issues a single broad scope for the accounting API.
	freshbooksScopes = "user:invoices:read user:invoices:write user:clients:read user:clients:write"
)

// freshbooksProvider implements Provider against the FreshBooks accounting API.
type freshbooksProvider struct {
	clientID     string
	clientSecret string
	redirectURL  string
	// endpoints are overridable in tests; default to the consts above.
	authorizeURL, tokenURL, apiBase string
}

// NewFreshBooks builds a FreshBooks provider from the configured credentials.
func NewFreshBooks(creds config.OAuthCreds, publicURL string) Provider {
	return &freshbooksProvider{
		clientID:     creds.ClientID,
		clientSecret: creds.ClientSecret,
		redirectURL:  callbackURL(publicURL, "freshbooks"),
		authorizeURL: freshbooksAuthorizeURL,
		tokenURL:     freshbooksTokenURL,
		apiBase:      freshbooksAPIBase,
	}
}

func (p *freshbooksProvider) Name() string   { return "freshbooks" }
func (p *freshbooksProvider) Scopes() string { return freshbooksScopes }

func (p *freshbooksProvider) AuthCodeURL(state string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", p.clientID)
	q.Set("redirect_uri", p.redirectURL)
	q.Set("scope", freshbooksScopes)
	q.Set("state", state)
	return p.authorizeURL + "?" + q.Encode()
}

func (p *freshbooksProvider) Exchange(ctx context.Context, code string, _ map[string]string) (Tokens, string, string, error) {
	// FreshBooks' token endpoint takes the client credentials in the body (no
	// Basic auth header).
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", p.redirectURL)
	form.Set("client_id", p.clientID)
	form.Set("client_secret", p.clientSecret)

	tr, err := postForm(ctx, p.tokenURL, "", form)
	if err != nil {
		return Tokens{}, "", "", fmt.Errorf("freshbooks: exchange: %w", err)
	}
	tok := tr.tokens()

	acctID, acctName, err := p.fetchAccount(ctx, tok.AccessToken)
	if err != nil {
		return Tokens{}, "", "", fmt.Errorf("freshbooks: resolve account: %w", err)
	}
	return tok, acctID, acctName, nil
}

func (p *freshbooksProvider) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", p.clientID)
	form.Set("client_secret", p.clientSecret)
	form.Set("redirect_uri", p.redirectURL)

	tr, err := postForm(ctx, p.tokenURL, "", form)
	if err != nil {
		return Tokens{}, fmt.Errorf("freshbooks: refresh: %w", err)
	}
	tok := tr.tokens()
	// FreshBooks rotates refresh tokens; carry the old one forward if absent.
	if tok.RefreshToken == "" {
		tok.RefreshToken = refreshToken
	}
	return tok, nil
}

// freshbooksMeResponse is the relevant slice of GET /auth/api/v1/users/me. A
// FreshBooks login owns one or more business memberships; the account_id of the
// first business is used as the externalOrgID for later accounting calls.
type freshbooksMeResponse struct {
	Response struct {
		BusinessMemberships []struct {
			Business struct {
				AccountID string `json:"account_id"`
				Name      string `json:"name"`
			} `json:"business"`
		} `json:"business_memberships"`
	} `json:"response"`
}

func (p *freshbooksProvider) fetchAccount(ctx context.Context, accessToken string) (id, name string, err error) {
	var out freshbooksMeResponse
	if err := doJSON(ctx, "GET", p.apiBase+"/auth/api/v1/users/me", accessToken, nil, &out, nil); err != nil {
		return "", "", err
	}
	for _, m := range out.Response.BusinessMemberships {
		if m.Business.AccountID != "" {
			return m.Business.AccountID, m.Business.Name, nil
		}
	}
	return "", "", fmt.Errorf("freshbooks: no business connected to this token")
}

// ── Invoice creation ──────────────────────────────────────────────────────────

// freshbooksInvoiceRequest is the POST .../invoices/invoices payload. FreshBooks
// nests the resource under an "invoice" key.
type freshbooksInvoiceRequest struct {
	Invoice freshbooksInvoicePayload `json:"invoice"`
}

type freshbooksInvoicePayload struct {
	// FreshBooks identifies the client by id; we pass the organization (fname)
	// fields so the invoice carries the client name. customerid is resolved by
	// FreshBooks when create_date + the client name match an existing client.
	Organization string           `json:"organization"`
	CurrencyCode string           `json:"currency_code,omitempty"`
	PONumber     string           `json:"po_number,omitempty"`
	Notes        string           `json:"notes,omitempty"`
	Lines        []freshbooksLine `json:"lines"`
}

type freshbooksLine struct {
	Name        string           `json:"name,omitempty"`
	Description string           `json:"description"`
	Quantity    float64          `json:"qty"`
	UnitCost    freshbooksAmount `json:"unit_cost"`
}

// freshbooksAmount is FreshBooks' money shape (amount as a string + currency).
type freshbooksAmount struct {
	Amount string `json:"amount"`
	Code   string `json:"code,omitempty"`
}

// freshbooksInvoiceResponse is the relevant slice of the create-invoice response.
type freshbooksInvoiceResponse struct {
	Response struct {
		Result struct {
			Invoice struct {
				ID            int64  `json:"id"`
				InvoiceNumber string `json:"invoice_number"`
			} `json:"invoice"`
		} `json:"result"`
	} `json:"response"`
}

func (p *freshbooksProvider) CreateInvoice(ctx context.Context, tok Tokens, externalOrgID string, inv Invoice) (string, string, error) {
	if externalOrgID == "" {
		return "", "", fmt.Errorf("freshbooks: missing account id")
	}
	payload := freshbooksInvoiceRequest{Invoice: freshbooksInvoicePayload{
		Organization: inv.ContactName,
		CurrencyCode: inv.Currency,
		Notes:        inv.Reference,
		Lines:        freshbooksLines(inv.Lines, inv.Currency),
	}}

	endpoint := p.apiBase + "/accounting/account/" + url.PathEscape(externalOrgID) + "/invoices/invoices"
	var out freshbooksInvoiceResponse
	if err := doJSON(ctx, "POST", endpoint, tok.AccessToken, payload, &out, nil); err != nil {
		return "", "", fmt.Errorf("freshbooks: create invoice: %w", err)
	}
	id := out.Response.Result.Invoice.ID
	if id == 0 {
		return "", "", fmt.Errorf("freshbooks: create invoice: empty response")
	}
	idStr := fmt.Sprintf("%d", id)
	return idStr, freshbooksInvoiceWebBase + idStr, nil
}

func freshbooksLines(lines []InvoiceLine, currency string) []freshbooksLine {
	out := make([]freshbooksLine, 0, len(lines))
	for _, l := range lines {
		qty := l.Quantity
		if qty == 0 {
			qty = 1
		}
		unit := l.UnitAmount
		if unit == 0 && qty != 0 {
			unit = l.Amount / qty
		}
		out = append(out, freshbooksLine{
			Description: l.Description,
			Quantity:    qty,
			UnitCost: freshbooksAmount{
				Amount: fmt.Sprintf("%.2f", unit),
				Code:   currency,
			},
		})
	}
	return out
}
