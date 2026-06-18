// Package oauth configures Google and Microsoft OAuth 2.0 providers for gitstate.
// Each provider is gated by cfg.Auth.Providers.{Google,Microsoft}.Enabled (decisions A6).
// Neither provider is registered when its client credentials are absent.
package oauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/endpoints"
	"golang.org/x/oauth2/microsoft"

	"github.com/exo/gitstate/internal/config"
)

// UserInfo holds the normalized profile data returned by an OAuth provider.
type UserInfo struct {
	Sub       string // provider-local user ID (the "sub" claim)
	Email     string
	Name      string
	AvatarURL string
}

// Provider wraps an oauth2.Config and knows how to fetch UserInfo after the
// token exchange.
type Provider struct {
	Name   string
	config *oauth2.Config
	// fetchUser is provider-specific userinfo fetch.
	fetchUser func(ctx context.Context, token *oauth2.Token) (*UserInfo, error)
}

// AuthCodeURL returns the OAuth consent-page URL to redirect the user to.
// state is a CSRF-protection value the caller must store and verify on callback.
func (p *Provider) AuthCodeURL(state string) string {
	return p.config.AuthCodeURL(state, oauth2.AccessTypeOnline)
}

// Exchange swaps the authorization code for tokens and returns the provider
// user profile.
func (p *Provider) Exchange(ctx context.Context, code string) (*oauth2.Token, *UserInfo, error) {
	token, err := p.config.Exchange(ctx, code)
	if err != nil {
		return nil, nil, fmt.Errorf("oauth %s: exchange code: %w", p.Name, err)
	}
	info, err := p.fetchUser(ctx, token)
	if err != nil {
		return nil, nil, err
	}
	return token, info, nil
}

// Providers holds the enabled OAuth providers (keyed by lowercase name).
type Providers map[string]*Provider

// Load initialises the enabled OAuth providers from cfg.
// Only providers whose credentials are present are returned (decisions A6).
func Load(cfg *config.Config, publicURL string) Providers {
	providers := make(Providers)

	if cfg.Auth.Providers.Google.Enabled {
		providers["google"] = newGoogle(cfg.Auth.Providers.Google, publicURL)
	}
	if cfg.Auth.Providers.Microsoft.Enabled {
		providers["microsoft"] = newMicrosoft(cfg.Auth.Providers.Microsoft, publicURL)
	}

	return providers
}

// GenerateState creates a cryptographically random, hex-encoded 16-byte state token
// for CSRF protection in the OAuth flow.
func GenerateState() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("oauth: generate state: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// ── Google ────────────────────────────────────────────────────────────────────

func newGoogle(cfg config.GoogleConfig, publicURL string) *Provider {
	oc := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  publicURL + "/auth/oauth/google/callback",
		Scopes:       []string{"openid", "email", "profile"},
		Endpoint:     endpoints.Google,
	}
	return &Provider{
		Name:      "google",
		config:    oc,
		fetchUser: fetchGoogleUser,
	}
}

type googleUserInfo struct {
	Sub     string `json:"sub"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
}

func fetchGoogleUser(ctx context.Context, token *oauth2.Token) (*UserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://openidconnect.googleapis.com/v1/userinfo", nil)
	if err != nil {
		return nil, fmt.Errorf("oauth google: build userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth google: userinfo request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("oauth google: read userinfo body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth google: userinfo status %d: %s", resp.StatusCode, body)
	}

	var u googleUserInfo
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("oauth google: decode userinfo: %w", err)
	}
	if u.Email == "" {
		return nil, fmt.Errorf("oauth google: userinfo missing email")
	}

	return &UserInfo{
		Sub:       u.Sub,
		Email:     u.Email,
		Name:      u.Name,
		AvatarURL: u.Picture,
	}, nil
}

// ── Microsoft ─────────────────────────────────────────────────────────────────

func newMicrosoft(cfg config.MicrosoftConfig, publicURL string) *Provider {
	tenant := cfg.Tenant
	if tenant == "" {
		tenant = "common"
	}
	oc := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  publicURL + "/auth/oauth/microsoft/callback",
		Scopes:       []string{"openid", "email", "profile", "User.Read"},
		Endpoint:     microsoft.AzureADEndpoint(tenant),
	}
	return &Provider{
		Name:      "microsoft",
		config:    oc,
		fetchUser: fetchMicrosoftUser,
	}
}

type microsoftUserInfo struct {
	ID                string `json:"id"`
	UserPrincipalName string `json:"userPrincipalName"`
	DisplayName       string `json:"displayName"`
	Mail              string `json:"mail"`
}

func fetchMicrosoftUser(ctx context.Context, token *oauth2.Token) (*UserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://graph.microsoft.com/v1.0/me", nil)
	if err != nil {
		return nil, fmt.Errorf("oauth microsoft: build userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth microsoft: userinfo request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("oauth microsoft: read userinfo body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth microsoft: userinfo status %d: %s", resp.StatusCode, body)
	}

	var u microsoftUserInfo
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("oauth microsoft: decode userinfo: %w", err)
	}

	// Microsoft may return the email in Mail or UserPrincipalName.
	email := u.Mail
	if email == "" {
		email = u.UserPrincipalName
	}
	if email == "" {
		return nil, fmt.Errorf("oauth microsoft: userinfo missing email")
	}

	return &UserInfo{
		Sub:   u.ID,
		Email: email,
		Name:  u.DisplayName,
	}, nil
}
