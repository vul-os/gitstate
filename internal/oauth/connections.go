// Package oauth — GitHub/GitLab *connection* providers (distinct from the
// Google/Microsoft *login* providers in providers.go).
//
// A connection provider authorizes the gitstate OAuth app against a user's
// GitHub or GitLab account with repo-read scopes, so that sync can use a stored
// encrypted access token instead of asking the user to re-supply a PAT each
// time (decisions A6 config-gating, S3 secrets).
//
// Login providers find-or-create a gitstate user; connection providers do NOT
// touch the users table — they yield a token + the connected account login,
// which the API layer encrypts (internal/crypto) into platform_connections.
package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/oauth2"

	"github.com/exo/gitstate/internal/config"
)

// ConnAccount is the connected-account profile returned after a connection
// token exchange (the platform login/username only — no gitstate identity).
type ConnAccount struct {
	Login string // github login / gitlab username
}

// ConnProvider authorizes the gitstate OAuth app against GitHub or GitLab for
// repo access and exchanges the code for a token + connected-account login.
type ConnProvider struct {
	Platform  string // "github" | "gitlab"
	BaseURL   string // gitlab self-hosted base ("" => SaaS); empty for github
	config    *oauth2.Config
	scopes    []string
	fetchUser func(ctx context.Context, token *oauth2.Token) (*ConnAccount, error)
}

// AuthCodeURL returns the provider consent URL. state is a CSRF value the caller
// must store (cookie) and verify on callback.
func (p *ConnProvider) AuthCodeURL(state string) string {
	return p.config.AuthCodeURL(state, oauth2.AccessTypeOffline)
}

// Scopes returns the requested OAuth scopes (stored on the connection record).
func (p *ConnProvider) Scopes() string { return strings.Join(p.scopes, ",") }

// Exchange swaps the authorization code for a token and fetches the connected
// account login.
func (p *ConnProvider) Exchange(ctx context.Context, code string) (*oauth2.Token, *ConnAccount, error) {
	token, err := p.config.Exchange(ctx, code)
	if err != nil {
		return nil, nil, fmt.Errorf("oauth %s connect: exchange code: %w", p.Platform, err)
	}
	acct, err := p.fetchUser(ctx, token)
	if err != nil {
		return nil, nil, err
	}
	return token, acct, nil
}

// ConnProviders holds the configured connection providers keyed by platform.
type ConnProviders map[string]*ConnProvider

// LoadConnections initialises the GitHub/GitLab connection providers that have
// OAuth-app credentials configured (decisions A6). Providers without an id +
// secret are omitted, so the API returns 404 for them.
func LoadConnections(cfg *config.Config, publicURL string) ConnProviders {
	providers := make(ConnProviders)

	if cfg.Git.GitHub.OAuthClientID != "" && cfg.Git.GitHub.OAuthClientSecret != "" {
		providers["github"] = newGitHubConnection(cfg.Git.GitHub, publicURL)
	}
	if cfg.Git.GitLab.OAuthClientID != "" && cfg.Git.GitLab.OAuthClientSecret != "" {
		providers["gitlab"] = newGitLabConnection(cfg.Git.GitLab, publicURL)
	}

	return providers
}

// connectRedirectURL builds the OAuth-app callback URL for a platform.
func connectRedirectURL(publicURL, platform string) string {
	return publicURL + "/api/connect/" + platform + "/callback"
}

// ── GitHub connection ───────────────────────────────────────────────────────

func newGitHubConnection(cfg config.GitHubConfig, publicURL string) *ConnProvider {
	scopes := []string{"repo", "read:user"}
	oc := &oauth2.Config{
		ClientID:     cfg.OAuthClientID,
		ClientSecret: cfg.OAuthClientSecret,
		RedirectURL:  connectRedirectURL(publicURL, "github"),
		Scopes:       scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://github.com/login/oauth/authorize",
			TokenURL: "https://github.com/login/oauth/access_token",
		},
	}
	return &ConnProvider{
		Platform:  "github",
		config:    oc,
		scopes:    scopes,
		fetchUser: fetchGitHubAccount,
	}
}

type githubAccount struct {
	Login string `json:"login"`
}

func fetchGitHubAccount(ctx context.Context, token *oauth2.Token) (*ConnAccount, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return nil, fmt.Errorf("oauth github connect: build user request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth github connect: user request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("oauth github connect: read user body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth github connect: user status %d", resp.StatusCode)
	}

	var u githubAccount
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("oauth github connect: decode user: %w", err)
	}
	return &ConnAccount{Login: u.Login}, nil
}

// ── GitLab connection ───────────────────────────────────────────────────────

func newGitLabConnection(cfg config.GitLabConfig, publicURL string) *ConnProvider {
	scopes := []string{"api", "read_repository"}
	oc := &oauth2.Config{
		ClientID:     cfg.OAuthClientID,
		ClientSecret: cfg.OAuthClientSecret,
		RedirectURL:  connectRedirectURL(publicURL, "gitlab"),
		Scopes:       scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://gitlab.com/oauth/authorize",
			TokenURL: "https://gitlab.com/oauth/token",
		},
	}
	return &ConnProvider{
		Platform:  "gitlab",
		config:    oc,
		scopes:    scopes,
		fetchUser: fetchGitLabAccount,
	}
}

type gitlabAccount struct {
	Username string `json:"username"`
}

func fetchGitLabAccount(ctx context.Context, token *oauth2.Token) (*ConnAccount, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://gitlab.com/api/v4/user", nil)
	if err != nil {
		return nil, fmt.Errorf("oauth gitlab connect: build user request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth gitlab connect: user request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("oauth gitlab connect: read user body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth gitlab connect: user status %d", resp.StatusCode)
	}

	var u gitlabAccount
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("oauth gitlab connect: decode user: %w", err)
	}
	return &ConnAccount{Login: u.Username}, nil
}
