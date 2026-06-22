package config_test

import (
	"testing"

	"github.com/exo/gitstate/internal/config"
)

// GitHub/GitLab "Sign in with" reuse the git connect creds: LoginEnabled is derived
// true iff both the oauth client id and secret are present.
func TestGitLoginEnabledDerivation(t *testing.T) {
	clearConfigEnv(t)
	for _, k := range []string{
		"GITHUB_OAUTH_CLIENT_ID", "GITHUB_OAUTH_CLIENT_SECRET",
		"GITLAB_OAUTH_CLIENT_ID", "GITLAB_OAUTH_CLIENT_SECRET",
	} {
		t.Setenv(k, "")
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Git.GitHub.LoginEnabled || cfg.Git.GitLab.LoginEnabled {
		t.Fatal("git login should be disabled with no creds")
	}

	// GitHub creds present, GitLab absent → only GitHub enabled.
	t.Setenv("GITHUB_OAUTH_CLIENT_ID", "gh-id")
	t.Setenv("GITHUB_OAUTH_CLIENT_SECRET", "gh-secret")
	cfg, err = config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Git.GitHub.LoginEnabled {
		t.Error("GitHub login should be enabled when id+secret set")
	}
	if cfg.Git.GitLab.LoginEnabled {
		t.Error("GitLab login should stay disabled without its creds")
	}

	// Only an id (no secret) must NOT enable.
	t.Setenv("GITHUB_OAUTH_CLIENT_SECRET", "")
	cfg, _ = config.Load()
	if cfg.Git.GitHub.LoginEnabled {
		t.Error("GitHub login should require BOTH id and secret")
	}
}
