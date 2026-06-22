package oauth

import (
	"net/url"
	"strings"
	"testing"

	"github.com/exo/gitstate/internal/config"
)

// GitHub/GitLab "Sign in with" must register from the git connect creds (one app,
// incremental scopes) and ask for IDENTITY scopes only — never repo scopes.
func TestLoadRegistersGitHubGitLabLogin(t *testing.T) {
	cfg := &config.Config{}
	cfg.Git.GitHub.OAuthClientID = "gh-id"
	cfg.Git.GitHub.OAuthClientSecret = "gh-secret"
	cfg.Git.GitHub.LoginEnabled = true
	cfg.Git.GitLab.OAuthClientID = "gl-id"
	cfg.Git.GitLab.OAuthClientSecret = "gl-secret"
	cfg.Git.GitLab.LoginEnabled = true

	ps := Load(cfg, "https://app.example.com")

	gh, ok := ps["github"]
	if !ok {
		t.Fatal("github login provider not registered")
	}
	gl, ok := ps["gitlab"]
	if !ok {
		t.Fatal("gitlab login provider not registered")
	}
	// Google/Microsoft must NOT be registered without their own creds.
	if _, ok := ps["google"]; ok {
		t.Error("google registered without creds")
	}

	ghURL, _ := url.Parse(gh.AuthCodeURL("state123"))
	q := ghURL.Query()
	if q.Get("client_id") != "gh-id" {
		t.Errorf("github client_id = %q", q.Get("client_id"))
	}
	if q.Get("state") != "state123" {
		t.Errorf("github state = %q", q.Get("state"))
	}
	if !strings.HasSuffix(q.Get("redirect_uri"), "/auth/oauth/github/callback") {
		t.Errorf("github redirect_uri = %q", q.Get("redirect_uri"))
	}
	scope := q.Get("scope")
	if !strings.Contains(scope, "read:user") || !strings.Contains(scope, "user:email") {
		t.Errorf("github scope = %q, want identity scopes", scope)
	}
	if strings.Contains(scope, "repo") {
		t.Errorf("github login must NOT request repo scope, got %q", scope)
	}

	glURL, _ := url.Parse(gl.AuthCodeURL("s"))
	if s := glURL.Query().Get("scope"); s != "read_user" {
		t.Errorf("gitlab scope = %q, want read_user only", s)
	}
}

// When the git creds are absent, no github/gitlab login providers are registered.
func TestLoadOmitsGitLoginWhenUnconfigured(t *testing.T) {
	ps := Load(&config.Config{}, "https://app.example.com")
	if _, ok := ps["github"]; ok {
		t.Error("github registered without creds")
	}
	if _, ok := ps["gitlab"]; ok {
		t.Error("gitlab registered without creds")
	}
}
