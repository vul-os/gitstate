package accounting

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/exo/gitstate/internal/config"
)

// ── AuthCodeURL shaping ─────────────────────────────────────────────────────────

func TestSageAuthCodeURL(t *testing.T) {
	p := NewSage(config.OAuthCreds{ClientID: "cid", ClientSecret: "sec"}, "https://app.example.com")
	u, err := url.Parse(p.AuthCodeURL("state123"))
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != sageAuthorizeURL {
		t.Errorf("authorize endpoint = %s, want %s", got, sageAuthorizeURL)
	}
	q := u.Query()
	if q.Get("client_id") != "cid" {
		t.Errorf("client_id = %q", q.Get("client_id"))
	}
	if q.Get("state") != "state123" {
		t.Errorf("state = %q", q.Get("state"))
	}
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q", q.Get("response_type"))
	}
	if q.Get("scope") != sageScopes {
		t.Errorf("scope = %q", q.Get("scope"))
	}
	if q.Get("redirect_uri") != "https://app.example.com/api/accounting/sage/callback" {
		t.Errorf("redirect_uri = %q", q.Get("redirect_uri"))
	}
}

func TestZohoBooksAuthCodeURL(t *testing.T) {
	p := NewZohoBooks(config.OAuthCreds{ClientID: "cid", ClientSecret: "sec"}, "https://app.example.com")
	u, err := url.Parse(p.AuthCodeURL("st"))
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != zohoAuthorizeURL {
		t.Errorf("authorize endpoint = %s, want %s", got, zohoAuthorizeURL)
	}
	q := u.Query()
	if q.Get("scope") != zohoScopes {
		t.Errorf("scope = %q", q.Get("scope"))
	}
	if q.Get("state") != "st" {
		t.Errorf("state = %q", q.Get("state"))
	}
	// Zoho needs offline access to return a refresh token.
	if q.Get("access_type") != "offline" {
		t.Errorf("access_type = %q", q.Get("access_type"))
	}
	if q.Get("redirect_uri") != "https://app.example.com/api/accounting/zoho_books/callback" {
		t.Errorf("redirect_uri = %q", q.Get("redirect_uri"))
	}
}

func TestFreshBooksAuthCodeURL(t *testing.T) {
	p := NewFreshBooks(config.OAuthCreds{ClientID: "cid", ClientSecret: "sec"}, "https://app.example.com")
	u, err := url.Parse(p.AuthCodeURL("xyz"))
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != freshbooksAuthorizeURL {
		t.Errorf("authorize endpoint = %s, want %s", got, freshbooksAuthorizeURL)
	}
	q := u.Query()
	if q.Get("scope") != freshbooksScopes {
		t.Errorf("scope = %q", q.Get("scope"))
	}
	if q.Get("state") != "xyz" {
		t.Errorf("state = %q", q.Get("state"))
	}
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q", q.Get("response_type"))
	}
	if q.Get("redirect_uri") != "https://app.example.com/api/accounting/freshbooks/callback" {
		t.Errorf("redirect_uri = %q", q.Get("redirect_uri"))
	}
}

// ── Sage Exchange + CreateInvoice request shaping ───────────────────────────────

func TestSageExchangeAndCreateInvoice(t *testing.T) {
	var (
		gotBizHdr      string
		gotInvoiceBody sageInvoiceRequest
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Basic ") {
				t.Errorf("token auth not Basic: %q", r.Header.Get("Authorization"))
			}
			writeJSON(w, map[string]any{"access_token": "sage-access", "refresh_token": "sage-refresh", "expires_in": 1800})
		case r.URL.Path == "/businesses":
			if r.Header.Get("Authorization") != "Bearer sage-access" {
				t.Errorf("businesses auth = %q", r.Header.Get("Authorization"))
			}
			writeJSON(w, map[string]any{"$items": []map[string]any{{"id": "biz-1", "displayed_as": "Acme Ltd"}}})
		case r.URL.Path == "/sales_invoices":
			gotBizHdr = r.Header.Get("X-Business")
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotInvoiceBody)
			writeJSON(w, map[string]any{"id": "SI-1", "displayed_as": "SI-1"})
		default:
			http.Error(w, "unexpected "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := &sageProvider{clientID: "c", clientSecret: "s", redirectURL: "https://app/cb",
		tokenURL: srv.URL + "/token", apiBase: srv.URL}

	ctx := context.Background()
	tok, orgID, name, err := p.Exchange(ctx, "code", nil)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if orgID != "biz-1" || name != "Acme Ltd" {
		t.Errorf("business = %q / %q", orgID, name)
	}
	if tok.AccessToken != "sage-access" || !tok.Valid() {
		t.Errorf("tokens = %+v", tok)
	}

	extID, extURL, err := p.CreateInvoice(ctx, tok, "biz-1", Invoice{
		ContactName: "Widget Co", Currency: "GBP", Reference: "gs-1",
		Lines: []InvoiceLine{{Description: "repo work", Quantity: 1, Amount: 1500, UnitAmount: 1500}},
	})
	if err != nil {
		t.Fatalf("create invoice: %v", err)
	}
	if gotBizHdr != "biz-1" {
		t.Errorf("X-Business = %q", gotBizHdr)
	}
	if gotInvoiceBody.SalesInvoice.ContactName != "Widget Co" || len(gotInvoiceBody.SalesInvoice.InvoiceLines) != 1 {
		t.Errorf("invoice payload = %+v", gotInvoiceBody.SalesInvoice)
	}
	if gotInvoiceBody.SalesInvoice.InvoiceLines[0].UnitPrice != 1500 {
		t.Errorf("line unit price = %v", gotInvoiceBody.SalesInvoice.InvoiceLines[0].UnitPrice)
	}
	if extID != "SI-1" || !strings.HasSuffix(extURL, "SI-1") {
		t.Errorf("ext = %q / %q", extID, extURL)
	}
}

// ── Zoho Exchange + CreateInvoice request shaping ───────────────────────────────

func TestZohoBooksExchangeAndCreateInvoice(t *testing.T) {
	var (
		gotOrgParam    string
		gotInvoiceBody zohoInvoiceRequest
		gotAuthScheme  string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			_ = r.ParseForm()
			if r.Form.Get("client_id") != "c" || r.Form.Get("client_secret") != "s" {
				t.Errorf("zoho token creds not in body: %v", r.Form)
			}
			writeJSON(w, map[string]any{"access_token": "zoho-access", "refresh_token": "zoho-refresh", "expires_in": 3600})
		case r.URL.Path == "/organizations":
			gotAuthScheme = r.Header.Get("Authorization")
			writeJSON(w, map[string]any{"organizations": []map[string]any{{"organization_id": "org-9", "name": "Books Co"}}})
		case r.URL.Path == "/invoices":
			gotOrgParam = r.URL.Query().Get("organization_id")
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotInvoiceBody)
			writeJSON(w, map[string]any{"invoice": map[string]any{"invoice_id": "ZINV-1", "invoice_number": "INV-001"}})
		default:
			http.Error(w, "unexpected "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := &zohobooksProvider{clientID: "c", clientSecret: "s", redirectURL: "https://app/cb",
		tokenURL: srv.URL + "/token", apiBase: srv.URL}

	ctx := context.Background()
	tok, orgID, name, err := p.Exchange(ctx, "code", nil)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if !strings.HasPrefix(gotAuthScheme, "Zoho-oauthtoken ") {
		t.Errorf("zoho auth scheme = %q", gotAuthScheme)
	}
	if orgID != "org-9" || name != "Books Co" {
		t.Errorf("org = %q / %q", orgID, name)
	}

	extID, extURL, err := p.CreateInvoice(ctx, tok, "org-9", Invoice{
		ContactName: "Widget Co", Reference: "gs-1",
		Lines: []InvoiceLine{{Description: "repo work", Quantity: 2, Amount: 1000, UnitAmount: 500}},
	})
	if err != nil {
		t.Fatalf("create invoice: %v", err)
	}
	if gotOrgParam != "org-9" {
		t.Errorf("organization_id param = %q", gotOrgParam)
	}
	if gotInvoiceBody.CustomerName != "Widget Co" || len(gotInvoiceBody.LineItems) != 1 || gotInvoiceBody.LineItems[0].Rate != 500 {
		t.Errorf("invoice payload = %+v", gotInvoiceBody)
	}
	if extID != "ZINV-1" || !strings.HasSuffix(extURL, "ZINV-1") {
		t.Errorf("ext = %q / %q", extID, extURL)
	}
}

// ── FreshBooks Exchange + CreateInvoice request shaping ──────────────────────────

func TestFreshBooksExchangeAndCreateInvoice(t *testing.T) {
	var (
		gotPath        string
		gotInvoiceBody freshbooksInvoiceRequest
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/auth/oauth/token":
			_ = r.ParseForm()
			if r.Form.Get("client_id") != "c" {
				t.Errorf("freshbooks token creds not in body: %v", r.Form)
			}
			writeJSON(w, map[string]any{"access_token": "fb-access", "refresh_token": "fb-refresh", "expires_in": 3600})
		case r.URL.Path == "/auth/api/v1/users/me":
			writeJSON(w, map[string]any{"response": map[string]any{
				"business_memberships": []map[string]any{
					{"business": map[string]any{"account_id": "acct-7", "name": "My Biz"}},
				}}})
		case strings.HasSuffix(r.URL.Path, "/invoices/invoices"):
			gotPath = r.URL.Path
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotInvoiceBody)
			writeJSON(w, map[string]any{"response": map[string]any{"result": map[string]any{
				"invoice": map[string]any{"id": 4242, "invoice_number": "0000042"}}}})
		default:
			http.Error(w, "unexpected "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := &freshbooksProvider{clientID: "c", clientSecret: "s", redirectURL: "https://app/cb",
		tokenURL: srv.URL + "/auth/oauth/token", apiBase: srv.URL}

	ctx := context.Background()
	tok, orgID, name, err := p.Exchange(ctx, "code", nil)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if orgID != "acct-7" || name != "My Biz" {
		t.Errorf("account = %q / %q", orgID, name)
	}

	extID, extURL, err := p.CreateInvoice(ctx, tok, "acct-7", Invoice{
		ContactName: "Widget Co", Currency: "USD", Reference: "gs-1",
		Lines: []InvoiceLine{{Description: "repo work", Quantity: 1, Amount: 1500, UnitAmount: 1500}},
	})
	if err != nil {
		t.Fatalf("create invoice: %v", err)
	}
	if gotPath != "/accounting/account/acct-7/invoices/invoices" {
		t.Errorf("invoice path = %q", gotPath)
	}
	if gotInvoiceBody.Invoice.Organization != "Widget Co" || len(gotInvoiceBody.Invoice.Lines) != 1 {
		t.Errorf("invoice payload = %+v", gotInvoiceBody.Invoice)
	}
	if gotInvoiceBody.Invoice.Lines[0].UnitCost.Amount != "1500.00" {
		t.Errorf("unit cost = %q", gotInvoiceBody.Invoice.Lines[0].UnitCost.Amount)
	}
	if extID != "4242" || !strings.HasSuffix(extURL, "4242") {
		t.Errorf("ext = %q / %q", extID, extURL)
	}
}

// ── error / 401 paths ────────────────────────────────────────────────────────────

func TestSageExchange401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	p := &sageProvider{clientID: "c", clientSecret: "s", tokenURL: srv.URL, apiBase: srv.URL}
	_, _, _, err := p.Exchange(context.Background(), "code", nil)
	if err == nil || !errorsIs(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestZohoBooksCreateInvoice401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	p := &zohobooksProvider{clientID: "c", clientSecret: "s", apiBase: srv.URL}
	_, _, err := p.CreateInvoice(context.Background(), Tokens{AccessToken: "expired"}, "org-1", Invoice{
		Lines: []InvoiceLine{{Description: "x", Amount: 1}},
	})
	if err == nil || !errorsIs(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestFreshBooksCreateInvoice401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	p := &freshbooksProvider{clientID: "c", clientSecret: "s", apiBase: srv.URL}
	_, _, err := p.CreateInvoice(context.Background(), Tokens{AccessToken: "expired"}, "acct-1", Invoice{
		Lines: []InvoiceLine{{Description: "x", Amount: 1}},
	})
	if err == nil || !errorsIs(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestNewProvidersMissingOrgID(t *testing.T) {
	ctx := context.Background()
	if _, _, err := (&sageProvider{}).CreateInvoice(ctx, Tokens{}, "", Invoice{}); err == nil {
		t.Error("sage: want error on missing business id")
	}
	if _, _, err := (&zohobooksProvider{}).CreateInvoice(ctx, Tokens{}, "", Invoice{}); err == nil {
		t.Error("zoho: want error on missing organization id")
	}
	if _, _, err := (&freshbooksProvider{}).CreateInvoice(ctx, Tokens{}, "", Invoice{}); err == nil {
		t.Error("freshbooks: want error on missing account id")
	}
}

// ── Refresh token carry-forward ──────────────────────────────────────────────────

func TestSageRefreshCarriesToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" {
			t.Errorf("grant_type = %q", r.Form.Get("grant_type"))
		}
		writeJSON(w, map[string]any{"access_token": "new", "expires_in": 1800})
	}))
	defer srv.Close()
	p := &sageProvider{clientID: "c", clientSecret: "s", tokenURL: srv.URL}
	tok, err := p.Refresh(context.Background(), "old-refresh")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if tok.AccessToken != "new" || tok.RefreshToken != "old-refresh" {
		t.Errorf("tokens = %+v", tok)
	}
}

// ── Load gating for the new providers ────────────────────────────────────────────

func TestLoadGatingNewProviders(t *testing.T) {
	cfg := &config.Config{}
	cfg.Accounting.Sage = config.OAuthCreds{ClientID: "a", ClientSecret: "b", Enabled: true}
	cfg.Accounting.ZohoBooks = config.OAuthCreds{ClientID: "a", ClientSecret: "b", Enabled: true}
	cfg.Accounting.FreshBooks = config.OAuthCreds{} // not enabled
	got := Load(cfg, "https://app")
	if _, ok := got["sage"]; !ok {
		t.Error("sage should be loaded")
	}
	if _, ok := got["zoho_books"]; !ok {
		t.Error("zoho_books should be loaded")
	}
	if _, ok := got["freshbooks"]; ok {
		t.Error("freshbooks should NOT be loaded")
	}
}
