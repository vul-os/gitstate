// Package oauth — Google/Microsoft *calendar* connection providers (distinct
// from the Google/Microsoft *login* providers in providers.go).
//
// A calendar provider authorizes the gitstate OAuth app against a user's Google
// Calendar or Microsoft 365 calendar with read/write event scopes, so gitstate
// can push approved leave as OOO events and pull busy blocks back into
// availability. It reuses the SAME OAuth client id/secret as the login providers
// (cfg.Auth.Providers.{Google,Microsoft}) — only the requested scopes differ
// (decisions A6 config-gating, S3 secrets).
//
// Like the GitHub/GitLab connection providers, calendar providers do NOT touch
// the users table — they yield a token + the connected calendar email, which the
// API layer encrypts (internal/crypto) into calendar_connections.
package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/endpoints"
	"golang.org/x/oauth2/microsoft"

	"github.com/exo/gitstate/internal/config"
)

// CalAccount is the connected calendar-account profile returned after a calendar
// token exchange (the calendar email only — no gitstate identity).
type CalAccount struct {
	Email string
}

// CalProvider authorizes the gitstate OAuth app against Google Calendar or
// Microsoft Graph and exchanges the code for a token + connected calendar email.
type CalProvider struct {
	Name      string // "google" | "microsoft"
	config    *oauth2.Config
	scopes    []string
	fetchUser func(ctx context.Context, token *oauth2.Token) (*CalAccount, error)
}

// AuthCodeURL returns the provider consent URL. state is a CSRF value the caller
// must store (cookie) and verify on callback. AccessTypeOffline + consent prompt
// are requested so the provider returns a refresh token (needed for unattended
// push/pull after the access token expires).
func (p *CalProvider) AuthCodeURL(state string) string {
	return p.config.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"),
	)
}

// Scopes returns the requested OAuth scopes (stored on the connection record).
func (p *CalProvider) Scopes() []string { return p.scopes }

// Config exposes the underlying oauth2.Config so the calendar client can refresh
// expired tokens with the stored refresh token (TokenSource).
func (p *CalProvider) Config() *oauth2.Config { return p.config }

// Exchange swaps the authorization code for a token and fetches the connected
// calendar email.
func (p *CalProvider) Exchange(ctx context.Context, code string) (*oauth2.Token, *CalAccount, error) {
	token, err := p.config.Exchange(ctx, code)
	if err != nil {
		return nil, nil, fmt.Errorf("oauth %s calendar: exchange code: %w", p.Name, err)
	}
	acct, err := p.fetchUser(ctx, token)
	if err != nil {
		// A failed email fetch is non-fatal: we still have a usable token. Return
		// an empty email rather than discarding the connection.
		acct = &CalAccount{}
	}
	return token, acct, nil
}

// CalProviders holds the configured calendar providers keyed by lowercase name.
type CalProviders map[string]*CalProvider

// LoadCalendars initialises the Google/Microsoft calendar providers that have
// OAuth credentials configured (decisions A6). It reuses the login OAuth client
// id/secret; providers without an id + secret are omitted, so the API returns
// 404 for them.
func LoadCalendars(cfg *config.Config, publicURL string) CalProviders {
	providers := make(CalProviders)

	if cfg.Auth.Providers.Google.Enabled {
		providers["google"] = newGoogleCalendar(cfg.Auth.Providers.Google, publicURL)
	}
	if cfg.Auth.Providers.Microsoft.Enabled {
		providers["microsoft"] = newMicrosoftCalendar(cfg.Auth.Providers.Microsoft, publicURL)
	}

	return providers
}

// calendarRedirectURL builds the OAuth-app callback URL for a calendar provider.
func calendarRedirectURL(publicURL, provider string) string {
	return publicURL + "/api/calendar/" + provider + "/callback"
}

// ── Google Calendar ─────────────────────────────────────────────────────────

func newGoogleCalendar(cfg config.GoogleConfig, publicURL string) *CalProvider {
	// calendar.events: read/write events (push OOO).
	// calendar.freebusy: query busy windows (pull availability).
	// openid+email: discover the connected calendar email.
	scopes := []string{
		"openid", "email",
		"https://www.googleapis.com/auth/calendar.events",
		"https://www.googleapis.com/auth/calendar.freebusy",
	}
	oc := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  calendarRedirectURL(publicURL, "google"),
		Scopes:       scopes,
		Endpoint:     endpoints.Google,
	}
	return &CalProvider{
		Name:      "google",
		config:    oc,
		scopes:    scopes,
		fetchUser: fetchGoogleCalEmail,
	}
}

func fetchGoogleCalEmail(ctx context.Context, token *oauth2.Token) (*CalAccount, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://openidconnect.googleapis.com/v1/userinfo", nil)
	if err != nil {
		return nil, fmt.Errorf("oauth google calendar: build userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth google calendar: userinfo request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("oauth google calendar: read userinfo body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth google calendar: userinfo status %d", resp.StatusCode)
	}

	var u struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("oauth google calendar: decode userinfo: %w", err)
	}
	return &CalAccount{Email: u.Email}, nil
}

// ── Microsoft Calendar (Graph) ──────────────────────────────────────────────

func newMicrosoftCalendar(cfg config.MicrosoftConfig, publicURL string) *CalProvider {
	tenant := cfg.Tenant
	if tenant == "" {
		tenant = "common"
	}
	// Calendars.ReadWrite: create OOO events + read events.
	// offline_access: receive a refresh token.
	// openid+email: discover the connected mailbox.
	scopes := []string{
		"openid", "email", "offline_access",
		"https://graph.microsoft.com/Calendars.ReadWrite",
	}
	oc := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  calendarRedirectURL(publicURL, "microsoft"),
		Scopes:       scopes,
		Endpoint:     microsoft.AzureADEndpoint(tenant),
	}
	return &CalProvider{
		Name:      "microsoft",
		config:    oc,
		scopes:    scopes,
		fetchUser: fetchMicrosoftCalEmail,
	}
}

func fetchMicrosoftCalEmail(ctx context.Context, token *oauth2.Token) (*CalAccount, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://graph.microsoft.com/v1.0/me", nil)
	if err != nil {
		return nil, fmt.Errorf("oauth microsoft calendar: build me request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth microsoft calendar: me request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("oauth microsoft calendar: read me body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth microsoft calendar: me status %d", resp.StatusCode)
	}

	var u struct {
		Mail              string `json:"mail"`
		UserPrincipalName string `json:"userPrincipalName"`
	}
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("oauth microsoft calendar: decode me: %w", err)
	}
	email := u.Mail
	if email == "" {
		email = u.UserPrincipalName
	}
	return &CalAccount{Email: email}, nil
}
