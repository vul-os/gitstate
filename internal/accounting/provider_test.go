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
	"time"

	"github.com/exo/gitstate/internal/config"
)

// ── AuthCodeURL shaping ─────────────────────────────────────────────────────────

func TestXeroAuthCodeURL(t *testing.T) {
	p := NewXero(config.OAuthCreds{ClientID: "cid", ClientSecret: "sec"}, "https://app.example.com")
	raw := p.AuthCodeURL("state123")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if got, want := u.Scheme+"://"+u.Host+u.Path, xeroAuthorizeURL; got != want {
		t.Errorf("authorize endpoint = %s, want %s", got, want)
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
	if q.Get("scope") != xeroScopes {
		t.Errorf("scope = %q", q.Get("scope"))
	}
	if q.Get("redirect_uri") != "https://app.example.com/api/accounting/xero/callback" {
		t.Errorf("redirect_uri = %q", q.Get("redirect_uri"))
	}
}

func TestQuickBooksAuthCodeURL(t *testing.T) {
	p := NewQuickBooks(config.OAuthCreds{ClientID: "cid", ClientSecret: "sec"}, "https://app.example.com")
	u, err := url.Parse(p.AuthCodeURL("st"))
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if u.Scheme+"://"+u.Host+u.Path != qbAuthorizeURL {
		t.Errorf("authorize endpoint = %s", u.String())
	}
	if u.Query().Get("scope") != qbScopes {
		t.Errorf("scope = %q", u.Query().Get("scope"))
	}
}

// ── Exchange + CreateInvoice request shaping (Xero) ──────────────────────────────

func TestXeroExchangeAndCreateInvoice(t *testing.T) {
	var (
		gotTokenAuth   string
		gotGrant       string
		gotTenantHdr   string
		gotInvoiceBody xeroInvoiceRequest
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			gotTokenAuth = r.Header.Get("Authorization")
			_ = r.ParseForm()
			gotGrant = r.Form.Get("grant_type")
			writeJSON(w, map[string]any{
				"access_token": "xero-access", "refresh_token": "xero-refresh", "expires_in": 1800,
			})
		case r.URL.Path == "/connections":
			if r.Header.Get("Authorization") != "Bearer xero-access" {
				t.Errorf("connections auth = %q", r.Header.Get("Authorization"))
			}
			writeJSON(w, []xeroConnection{{TenantID: "tenant-1", TenantName: "Acme Books", TenantType: "ORGANISATION"}})
		case r.URL.Path == "/invoices":
			gotTenantHdr = r.Header.Get("Xero-tenant-id")
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotInvoiceBody)
			writeJSON(w, xeroInvoiceResponse{Invoices: []struct {
				InvoiceID     string `json:"InvoiceID"`
				InvoiceNumber string `json:"InvoiceNumber"`
			}{{InvoiceID: "INV-EXT-1", InvoiceNumber: "INV-2026-001"}}})
		default:
			http.Error(w, "unexpected "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := &xeroProvider{
		clientID: "cid", clientSecret: "sec", redirectURL: "https://app/cb",
		tokenURL: srv.URL + "/token", connectionsURL: srv.URL + "/connections", invoicesURL: srv.URL + "/invoices",
	}

	ctx := context.Background()
	tok, orgID, name, err := p.Exchange(ctx, "the-code", nil)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if !strings.HasPrefix(gotTokenAuth, "Basic ") {
		t.Errorf("token auth not Basic: %q", gotTokenAuth)
	}
	if gotGrant != "authorization_code" {
		t.Errorf("grant_type = %q", gotGrant)
	}
	if tok.AccessToken != "xero-access" || tok.RefreshToken != "xero-refresh" {
		t.Errorf("tokens = %+v", tok)
	}
	if tok.Expiry.IsZero() || !tok.Valid() {
		t.Errorf("expiry not set / invalid: %v", tok.Expiry)
	}
	if orgID != "tenant-1" || name != "Acme Books" {
		t.Errorf("tenant = %q / %q", orgID, name)
	}

	extID, extURL, err := p.CreateInvoice(ctx, tok, "tenant-1", Invoice{
		Number: "INV-2026-001", ContactName: "Widget Co", Currency: "USD",
		Lines: []InvoiceLine{{Description: "repo — 3 merged PRs", Quantity: 1, Amount: 1500, UnitAmount: 1500}},
	})
	if err != nil {
		t.Fatalf("create invoice: %v", err)
	}
	if gotTenantHdr != "tenant-1" {
		t.Errorf("Xero-tenant-id = %q", gotTenantHdr)
	}
	if len(gotInvoiceBody.Invoices) != 1 {
		t.Fatalf("invoice body = %+v", gotInvoiceBody)
	}
	iv := gotInvoiceBody.Invoices[0]
	if iv.Type != "ACCREC" || iv.Contact.Name != "Widget Co" || iv.CurrencyCode != "USD" {
		t.Errorf("invoice payload = %+v", iv)
	}
	if len(iv.LineItems) != 1 || iv.LineItems[0].UnitAmount != 1500 {
		t.Errorf("line items = %+v", iv.LineItems)
	}
	if extID != "INV-EXT-1" {
		t.Errorf("externalID = %q", extID)
	}
	if !strings.HasSuffix(extURL, "INV-EXT-1") {
		t.Errorf("externalURL = %q", extURL)
	}
}

// ── Token refresh (QuickBooks) ───────────────────────────────────────────────────

func TestQuickBooksRefresh(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" {
			t.Errorf("grant_type = %q", r.Form.Get("grant_type"))
		}
		if r.Form.Get("refresh_token") != "old-refresh" {
			t.Errorf("refresh_token = %q", r.Form.Get("refresh_token"))
		}
		writeJSON(w, map[string]any{"access_token": "new-access", "expires_in": 3600})
	}))
	defer srv.Close()

	p := &quickbooksProvider{clientID: "c", clientSecret: "s", tokenURL: srv.URL}
	tok, err := p.Refresh(context.Background(), "old-refresh")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if tok.AccessToken != "new-access" {
		t.Errorf("access = %q", tok.AccessToken)
	}
	// QuickBooks omitted a new refresh token → carry the old one forward.
	if tok.RefreshToken != "old-refresh" {
		t.Errorf("refresh not carried forward: %q", tok.RefreshToken)
	}
}

// ── 401 handling ─────────────────────────────────────────────────────────────────

func TestCreateInvoice401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	p := &xeroProvider{clientID: "c", clientSecret: "s", invoicesURL: srv.URL}
	_, _, err := p.CreateInvoice(context.Background(), Tokens{AccessToken: "expired"}, "tenant", Invoice{
		Lines: []InvoiceLine{{Description: "x", Amount: 1}},
	})
	if err == nil || !errorsIs(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestExchange401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	p := &quickbooksProvider{clientID: "c", clientSecret: "s", tokenURL: srv.URL}
	_, _, _, err := p.Exchange(context.Background(), "code", map[string]string{"realmId": "r1"})
	if err == nil || !errorsIs(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestQuickBooksExchangeMissingRealm(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"access_token": "a", "expires_in": 3600})
	}))
	defer srv.Close()
	p := &quickbooksProvider{clientID: "c", clientSecret: "s", tokenURL: srv.URL, apiBase: srv.URL + "/"}
	_, _, _, err := p.Exchange(context.Background(), "code", map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "realmId") {
		t.Fatalf("want missing realmId error, got %v", err)
	}
}

// ── Load gating ──────────────────────────────────────────────────────────────────

func TestLoadGating(t *testing.T) {
	cfg := &config.Config{}
	cfg.Accounting.Xero = config.OAuthCreds{ClientID: "x", ClientSecret: "y", Enabled: true}
	cfg.Accounting.QuickBooks = config.OAuthCreds{} // not enabled
	got := Load(cfg, "https://app")
	if _, ok := got["xero"]; !ok {
		t.Errorf("xero should be loaded")
	}
	if _, ok := got["quickbooks"]; ok {
		t.Errorf("quickbooks should NOT be loaded")
	}
}

func TestTokensValid(t *testing.T) {
	if (Tokens{}).Valid() {
		t.Error("empty tokens valid")
	}
	if (Tokens{AccessToken: "a", Expiry: time.Now().Add(-time.Minute)}).Valid() {
		t.Error("expired tokens valid")
	}
	if !(Tokens{AccessToken: "a"}).Valid() {
		t.Error("no-expiry tokens should be valid")
	}
	if !(Tokens{AccessToken: "a", Expiry: time.Now().Add(time.Hour)}).Valid() {
		t.Error("future-expiry tokens should be valid")
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// errorsIs avoids importing errors twice in test edits; thin wrapper.
func errorsIs(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		type wrapper interface{ Unwrap() error }
		w, ok := err.(wrapper)
		if !ok {
			return false
		}
		err = w.Unwrap()
	}
	return false
}
