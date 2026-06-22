package accounting

import (
	"context"
	"fmt"
	"net/url"

	"github.com/exo/gitstate/internal/config"
)

// QuickBooks (Intuit) API endpoints (isolated as consts so they are swappable).
const (
	qbAuthorizeURL = "https://appcenter.intuit.com/connect/oauth2"
	qbTokenURL     = "https://oauth.platform.intuit.com/oauth2/v1/tokens/bearer"
	// qbAPIBase is the production API base; {realmId} is filled per request.
	qbAPIBase = "https://quickbooks.api.intuit.com/v3/company/"
	// qbInvoiceWebBase is the customer-facing deep-link base for a created invoice.
	qbInvoiceWebBase = "https://app.qbo.intuit.com/app/invoice?txnId="

	qbScopes = "com.intuit.quickbooks.accounting"
)

// quickbooksProvider implements Provider against the QuickBooks Online API.
type quickbooksProvider struct {
	clientID     string
	clientSecret string
	redirectURL  string
	// endpoints are overridable in tests; default to the consts above.
	authorizeURL, tokenURL, apiBase string
}

// NewQuickBooks builds a QuickBooks provider from the configured credentials.
func NewQuickBooks(creds config.OAuthCreds, publicURL string) Provider {
	return &quickbooksProvider{
		clientID:     creds.ClientID,
		clientSecret: creds.ClientSecret,
		redirectURL:  callbackURL(publicURL, "quickbooks"),
		authorizeURL: qbAuthorizeURL,
		tokenURL:     qbTokenURL,
		apiBase:      qbAPIBase,
	}
}

func (p *quickbooksProvider) Name() string   { return "quickbooks" }
func (p *quickbooksProvider) Scopes() string { return qbScopes }

func (p *quickbooksProvider) AuthCodeURL(state string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", p.clientID)
	q.Set("redirect_uri", p.redirectURL)
	q.Set("scope", qbScopes)
	q.Set("state", state)
	return p.authorizeURL + "?" + q.Encode()
}

func (p *quickbooksProvider) Exchange(ctx context.Context, code string, callbackQuery map[string]string) (Tokens, string, string, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", p.redirectURL)

	tr, err := postForm(ctx, p.tokenURL, basicAuthHeader(p.clientID, p.clientSecret), form)
	if err != nil {
		return Tokens{}, "", "", fmt.Errorf("quickbooks: exchange: %w", err)
	}
	// QuickBooks passes the connected company id (realmId) on the callback query,
	// not in the token response.
	realmID := callbackQuery["realmId"]
	if realmID == "" {
		return Tokens{}, "", "", fmt.Errorf("quickbooks: callback missing realmId")
	}
	tok := tr.tokens()
	name := p.companyName(ctx, tok.AccessToken, realmID) // best-effort
	return tok, realmID, name, nil
}

func (p *quickbooksProvider) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)

	tr, err := postForm(ctx, p.tokenURL, basicAuthHeader(p.clientID, p.clientSecret), form)
	if err != nil {
		return Tokens{}, fmt.Errorf("quickbooks: refresh: %w", err)
	}
	tok := tr.tokens()
	if tok.RefreshToken == "" {
		tok.RefreshToken = refreshToken
	}
	return tok, nil
}

// companyName fetches the CompanyInfo display name (best-effort; "" on failure so
// a name-fetch hiccup never discards an otherwise-usable connection).
func (p *quickbooksProvider) companyName(ctx context.Context, accessToken, realmID string) string {
	endpoint := p.apiBase + url.PathEscape(realmID) + "/companyinfo/" + url.PathEscape(realmID) + "?minorversion=65"
	var out struct {
		CompanyInfo struct {
			CompanyName string `json:"CompanyName"`
		} `json:"CompanyInfo"`
	}
	if err := doJSON(ctx, "GET", endpoint, accessToken, nil, &out, nil); err != nil {
		return ""
	}
	return out.CompanyInfo.CompanyName
}

// ── Invoice creation ──────────────────────────────────────────────────────────

// qbInvoiceRequest is the POST /invoice payload.
type qbInvoiceRequest struct {
	CustomerRef qbRef       `json:"CustomerRef"`
	Line        []qbLine    `json:"Line"`
	DocNumber   string      `json:"DocNumber,omitempty"`
	CurrencyRef *qbValueRef `json:"CurrencyRef,omitempty"`
	PrivateNote string      `json:"PrivateNote,omitempty"`
}

type qbRef struct {
	Value string `json:"value"`
	Name  string `json:"name,omitempty"`
}

type qbValueRef struct {
	Value string `json:"value"`
}

type qbLine struct {
	Amount              float64                `json:"Amount"`
	DetailType          string                 `json:"DetailType"` // SalesItemLineDetail
	Description         string                 `json:"Description,omitempty"`
	SalesItemLineDetail *qbSalesItemLineDetail `json:"SalesItemLineDetail,omitempty"`
}

type qbSalesItemLineDetail struct {
	Qty       float64 `json:"Qty,omitempty"`
	UnitPrice float64 `json:"UnitPrice,omitempty"`
}

// qbInvoiceResponse is the relevant slice of the POST /invoice response.
type qbInvoiceResponse struct {
	Invoice struct {
		ID        string `json:"Id"`
		DocNumber string `json:"DocNumber"`
	} `json:"Invoice"`
}

func (p *quickbooksProvider) CreateInvoice(ctx context.Context, tok Tokens, externalOrgID string, inv Invoice) (string, string, error) {
	if externalOrgID == "" {
		return "", "", fmt.Errorf("quickbooks: missing realmId")
	}
	payload := qbInvoiceRequest{
		// QuickBooks requires a CustomerRef.value (a Customer id). We pass the
		// client name; if the customer must pre-exist, the API surfaces a clear
		// error that the handler returns to the user. (A name-only ref is accepted
		// by QuickBooks when SparseUpdate/auto-create is enabled on the company.)
		CustomerRef: qbRef{Value: "", Name: inv.ContactName},
		Line:        qbLines(inv.Lines),
		DocNumber:   truncateDocNumber(inv.Number),
		PrivateNote: inv.Reference,
	}
	if inv.Currency != "" {
		payload.CurrencyRef = &qbValueRef{Value: inv.Currency}
	}

	endpoint := p.apiBase + url.PathEscape(externalOrgID) + "/invoice?minorversion=65"
	var out qbInvoiceResponse
	if err := doJSON(ctx, "POST", endpoint, tok.AccessToken, payload, &out, nil); err != nil {
		return "", "", fmt.Errorf("quickbooks: create invoice: %w", err)
	}
	if out.Invoice.ID == "" {
		return "", "", fmt.Errorf("quickbooks: create invoice: empty response")
	}
	return out.Invoice.ID, qbInvoiceWebBase + out.Invoice.ID, nil
}

func qbLines(lines []InvoiceLine) []qbLine {
	out := make([]qbLine, 0, len(lines))
	for _, l := range lines {
		qty := l.Quantity
		if qty == 0 {
			qty = 1
		}
		unit := l.UnitAmount
		if unit == 0 && qty != 0 {
			unit = l.Amount / qty
		}
		out = append(out, qbLine{
			Amount:      l.Amount,
			DetailType:  "SalesItemLineDetail",
			Description: l.Description,
			SalesItemLineDetail: &qbSalesItemLineDetail{
				Qty:       qty,
				UnitPrice: unit,
			},
		})
	}
	return out
}

// truncateDocNumber keeps DocNumber within QuickBooks' 21-char limit.
func truncateDocNumber(s string) string {
	if len(s) > 21 {
		return s[:21]
	}
	return s
}
