package config_test

import (
	"strings"
	"testing"

	"github.com/exo/gitstate/internal/config"
)

// AppEnabled is derived true iff BOTH the App ID and the App private key are set.
func TestGitHubAppEnabledDerivation(t *testing.T) {
	clearConfigEnv(t)
	for _, k := range []string{"GITHUB_APP_ID", "GITHUB_APP_PRIVATE_KEY", "GITHUB_APP_SLUG"} {
		t.Setenv(k, "")
	}

	// Neither set → disabled.
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Git.GitHub.AppEnabled {
		t.Fatal("AppEnabled should be false with no App creds")
	}

	// Only the ID set → still disabled.
	t.Setenv("GITHUB_APP_ID", "123456")
	cfg, err = config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Git.GitHub.AppEnabled {
		t.Fatal("AppEnabled should be false with only App ID set")
	}

	// Both set → enabled.
	t.Setenv("GITHUB_APP_PRIVATE_KEY", "-----BEGIN RSA PRIVATE KEY-----\\nabc\\n-----END RSA PRIVATE KEY-----")
	t.Setenv("GITHUB_APP_SLUG", "my-app")
	cfg, err = config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Git.GitHub.AppEnabled {
		t.Fatal("AppEnabled should be true when App ID + private key are set")
	}
	if cfg.Git.GitHub.AppSlug != "my-app" {
		t.Fatalf("AppSlug = %q, want my-app", cfg.Git.GitHub.AppSlug)
	}
	// Literal \n in the env value must be normalized to real newlines.
	if strings.Contains(cfg.Git.GitHub.AppPrivateKey, `\n`) {
		t.Fatal("private key still contains literal \\n escapes")
	}
	if !strings.Contains(cfg.Git.GitHub.AppPrivateKey, "\n") {
		t.Fatal("private key has no real newlines after normalization")
	}
}
