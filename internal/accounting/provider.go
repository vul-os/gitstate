// Package accounting implements the Xero and QuickBooks OAuth + invoice-push
// integrations. An org owner connects their accounting system once (OAuth, with
// access + refresh tokens stored AES-256-GCM encrypted in accounting_connections),
// then a gitstate ClientInvoice — manual or generated-from-git — can be pushed to
// that system, recording the external id + deep link back on the invoice.
//
// Each accounting system is a Provider. The two implementations (xero.go,
// quickbooks.go) speak the documented REST APIs directly with the stdlib http
// client; every endpoint URL is isolated as a const so it is swappable, every
// call is timeout-bounded by the caller's context, and tokens are NEVER logged.
package accounting

import (
	"context"
	"time"

	"github.com/exo/gitstate/internal/config"
)

// Tokens is a provider-agnostic OAuth token set (the subset gitstate persists).
// AccessToken/RefreshToken are plaintext here; the store layer encrypts them.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	Expiry       time.Time // zero when the provider did not return an expiry
}

// Valid reports whether the access token is present and not (about to be) expired.
// A 60s skew keeps a token that is about to lapse from being used mid-request.
func (t Tokens) Valid() bool {
	if t.AccessToken == "" {
		return false
	}
	if t.Expiry.IsZero() {
		return true
	}
	return time.Now().Add(60 * time.Second).Before(t.Expiry)
}

// InvoiceLine is one billable line pushed to the accounting system. Amount is in
// the invoice currency's major unit (e.g. dollars), already computed by gitstate.
type InvoiceLine struct {
	Description string
	Quantity    float64
	UnitAmount  float64 // unit price in major currency units
	Amount      float64 // line total in major currency units
}

// Invoice is the provider-agnostic payload mapped from a gitstate ClientInvoice.
type Invoice struct {
	Number      string
	ContactName string // client name → Xero Contact / QuickBooks Customer
	Currency    string
	Reference   string // gitstate invoice number / note shown on the external invoice
	Lines       []InvoiceLine
}

// Provider authorizes against an accounting system and pushes invoices to it.
// Implementations are stateless and safe for concurrent use.
type Provider interface {
	// Name is the lowercase provider key ("xero" | "quickbooks").
	Name() string

	// Scopes returns the OAuth scopes requested (stored on the connection).
	Scopes() string

	// AuthCodeURL returns the consent URL. state is a CSRF value the caller stores
	// (cookie) and verifies on callback.
	AuthCodeURL(state string) string

	// Exchange swaps an authorization code for tokens and resolves the connected
	// external org (Xero tenantId / QuickBooks realmId) + its display name.
	// callbackQuery carries the raw callback query params (QuickBooks passes the
	// realmId there; Xero does not).
	Exchange(ctx context.Context, code string, callbackQuery map[string]string) (Tokens, string, string, error)

	// Refresh exchanges a refresh token for a fresh token set.
	Refresh(ctx context.Context, refreshToken string) (Tokens, error)

	// CreateInvoice creates inv in the external org, returning the external invoice
	// id and a deep link to view it.
	CreateInvoice(ctx context.Context, tok Tokens, externalOrgID string, inv Invoice) (externalID, url string, err error)
}

// Load returns the configured providers keyed by lowercase name. Providers
// without client credentials are omitted (so the API returns 404/"not configured"
// for them). publicURL is the app's public base URL used to build callback URLs.
func Load(cfg *config.Config, publicURL string) map[string]Provider {
	providers := map[string]Provider{}
	if cfg.Accounting.Xero.Enabled {
		providers["xero"] = NewXero(cfg.Accounting.Xero, publicURL)
	}
	if cfg.Accounting.QuickBooks.Enabled {
		providers["quickbooks"] = NewQuickBooks(cfg.Accounting.QuickBooks, publicURL)
	}
	if cfg.Accounting.Sage.Enabled {
		providers["sage"] = NewSage(cfg.Accounting.Sage, publicURL)
	}
	if cfg.Accounting.ZohoBooks.Enabled {
		providers["zoho_books"] = NewZohoBooks(cfg.Accounting.ZohoBooks, publicURL)
	}
	if cfg.Accounting.FreshBooks.Enabled {
		providers["freshbooks"] = NewFreshBooks(cfg.Accounting.FreshBooks, publicURL)
	}
	return providers
}

// callbackURL builds the OAuth-app redirect URL for a provider.
func callbackURL(publicURL, provider string) string {
	return publicURL + "/api/accounting/" + provider + "/callback"
}
