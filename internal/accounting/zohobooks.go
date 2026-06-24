package accounting

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/exo/gitstate/internal/config"
)

// Zoho Books API endpoints (isolated as consts so they are swappable / mockable).
//
// TODO(region): Zoho is data-center scoped — a token minted on accounts.zoho.eu
// must call www.zohoapis.eu (likewise .in / .com.au / .jp). We default to the
// .com (US) data center; multi-DC support would read the `location`/`accounts-server`
// param Zoho returns on the callback and pick the matching API host.
const (
	zohoAuthorizeURL = "https://accounts.zoho.com/oauth/v2/auth"
	zohoTokenURL     = "https://accounts.zoho.com/oauth/v2/token"
	// zohoAPIBase is the production Books REST base (US data center).
	zohoAPIBase = "https://www.zohoapis.com/books/v3"
	// zohoInvoiceWebBase is the customer-facing deep-link base for a created invoice.
	zohoInvoiceWebBase = "https://books.zoho.com/app#/invoices/"

	// ZohoBooks.fullaccess.all covers organizations, contacts, and invoices.
	zohoScopes = "ZohoBooks.fullaccess.all"
)

// zohobooksProvider implements Provider against the Zoho Books API.
type zohobooksProvider struct {
	clientID     string
	clientSecret string
	redirectURL  string
	// endpoints are overridable in tests; default to the consts above.
	authorizeURL, tokenURL, apiBase string
}

// NewZohoBooks builds a Zoho Books provider from the configured OAuth credentials.
func NewZohoBooks(creds config.OAuthCreds, publicURL string) Provider {
	return &zohobooksProvider{
		clientID:     creds.ClientID,
		clientSecret: creds.ClientSecret,
		redirectURL:  callbackURL(publicURL, "zoho_books"),
		authorizeURL: zohoAuthorizeURL,
		tokenURL:     zohoTokenURL,
		apiBase:      zohoAPIBase,
	}
}

func (p *zohobooksProvider) Name() string   { return "zoho_books" }
func (p *zohobooksProvider) Scopes() string { return zohoScopes }

func (p *zohobooksProvider) AuthCodeURL(state string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", p.clientID)
	q.Set("redirect_uri", p.redirectURL)
	q.Set("scope", zohoScopes)
	q.Set("state", state)
	// Zoho only returns a refresh token when access_type=offline + prompt=consent.
	q.Set("access_type", "offline")
	q.Set("prompt", "consent")
	return p.authorizeURL + "?" + q.Encode()
}

func (p *zohobooksProvider) Exchange(ctx context.Context, code string, _ map[string]string) (Tokens, string, string, error) {
	// Zoho's token endpoint takes the client credentials as form params (no Basic
	// auth header) alongside the code.
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", p.redirectURL)
	form.Set("client_id", p.clientID)
	form.Set("client_secret", p.clientSecret)

	tr, err := postForm(ctx, p.tokenURL, "", form)
	if err != nil {
		return Tokens{}, "", "", fmt.Errorf("zoho_books: exchange: %w", err)
	}
	tok := tr.tokens()

	orgID, orgName, err := p.fetchOrganization(ctx, tok.AccessToken)
	if err != nil {
		return Tokens{}, "", "", fmt.Errorf("zoho_books: resolve organization: %w", err)
	}
	return tok, orgID, orgName, nil
}

func (p *zohobooksProvider) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", p.clientID)
	form.Set("client_secret", p.clientSecret)

	tr, err := postForm(ctx, p.tokenURL, "", form)
	if err != nil {
		return Tokens{}, fmt.Errorf("zoho_books: refresh: %w", err)
	}
	tok := tr.tokens()
	// Zoho does not return a new refresh token on refresh; carry the old one forward.
	if tok.RefreshToken == "" {
		tok.RefreshToken = refreshToken
	}
	return tok, nil
}

// Zoho's bearer scheme is "Zoho-oauthtoken <token>", not "Bearer <token>", and
// the shared doJSON helper always sends "Bearer". So Zoho API calls go through a
// dedicated path that sets the Authorization header explicitly.
//
// zohoOrgList is the relevant slice of GET /organizations.
type zohoOrgList struct {
	Organizations []struct {
		OrganizationID string `json:"organization_id"`
		Name           string `json:"name"`
	} `json:"organizations"`
}

func (p *zohobooksProvider) fetchOrganization(ctx context.Context, accessToken string) (id, name string, err error) {
	var out zohoOrgList
	if err := zohoGetJSON(ctx, p.apiBase+"/organizations", accessToken, &out); err != nil {
		return "", "", err
	}
	if len(out.Organizations) == 0 {
		return "", "", fmt.Errorf("zoho_books: no organization connected to this token")
	}
	o := out.Organizations[0]
	return o.OrganizationID, o.Name, nil
}

// ── Invoice creation ──────────────────────────────────────────────────────────

// zohoInvoiceRequest is the POST /invoices payload. Zoho auto-creates the
// customer when customer_name is supplied without a customer_id.
type zohoInvoiceRequest struct {
	CustomerName string         `json:"customer_name"`
	ReferenceNum string         `json:"reference_number,omitempty"`
	LineItems    []zohoLineItem `json:"line_items"`
}

type zohoLineItem struct {
	Name     string  `json:"name"`
	Quantity float64 `json:"quantity"`
	Rate     float64 `json:"rate"`
}

// zohoInvoiceResponse is the relevant slice of the POST /invoices response.
type zohoInvoiceResponse struct {
	Invoice struct {
		InvoiceID     string `json:"invoice_id"`
		InvoiceNumber string `json:"invoice_number"`
	} `json:"invoice"`
}

func (p *zohobooksProvider) CreateInvoice(ctx context.Context, tok Tokens, externalOrgID string, inv Invoice) (string, string, error) {
	if externalOrgID == "" {
		return "", "", fmt.Errorf("zoho_books: missing organization id")
	}
	payload := zohoInvoiceRequest{
		CustomerName: inv.ContactName,
		ReferenceNum: inv.Reference,
		LineItems:    zohoLines(inv.Lines),
	}

	endpoint := p.apiBase + "/invoices?organization_id=" + url.QueryEscape(externalOrgID)
	var out zohoInvoiceResponse
	if err := zohoPostJSON(ctx, endpoint, tok.AccessToken, payload, &out); err != nil {
		return "", "", fmt.Errorf("zoho_books: create invoice: %w", err)
	}
	if out.Invoice.InvoiceID == "" {
		return "", "", fmt.Errorf("zoho_books: create invoice: empty response")
	}
	return out.Invoice.InvoiceID, zohoInvoiceWebBase + out.Invoice.InvoiceID, nil
}

func zohoLines(lines []InvoiceLine) []zohoLineItem {
	out := make([]zohoLineItem, 0, len(lines))
	for _, l := range lines {
		qty := l.Quantity
		if qty == 0 {
			qty = 1
		}
		rate := l.UnitAmount
		if rate == 0 && qty != 0 {
			rate = l.Amount / qty
		}
		out = append(out, zohoLineItem{
			Name:     l.Description,
			Quantity: qty,
			Rate:     rate,
		})
	}
	return out
}

// ── Zoho auth-scheme HTTP helpers ──────────────────────────────────────────────
//
// Zoho's bearer scheme is "Zoho-oauthtoken <token>" (not "Bearer <token>"), so
// these mirror the shared doJSON helper but with Zoho's Authorization header. A
// 401 maps to ErrUnauthorized so callers can refresh + retry.

func zohoGetJSON(ctx context.Context, endpoint, accessToken string, out any) error {
	return zohoDoJSON(ctx, http.MethodGet, endpoint, accessToken, nil, out)
}

func zohoPostJSON(ctx context.Context, endpoint, accessToken string, reqBody, out any) error {
	return zohoDoJSON(ctx, http.MethodPost, endpoint, accessToken, reqBody, out)
}

func zohoDoJSON(ctx context.Context, method, endpoint, accessToken string, reqBody, out any) error {
	var bodyReader io.Reader
	if reqBody != nil {
		raw, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("accounting: marshal request: %w", err)
		}
		bodyReader = strings.NewReader(string(raw))
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, bodyReader)
	if err != nil {
		return fmt.Errorf("accounting: build api request: %w", err)
	}
	req.Header.Set("Authorization", "Zoho-oauthtoken "+accessToken)
	req.Header.Set("Accept", "application/json")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("accounting: api request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("accounting: api status %d: %s", resp.StatusCode, snippet(respBody))
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("accounting: decode api response: %w", err)
		}
	}
	return nil
}
