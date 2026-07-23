package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/exo/gitstate/internal/config"
)

// clearConfigEnv unsets every env var Load consults so one test can't leak into
// another (t.Setenv restores afterwards, but vars set in the real environment
// would otherwise bleed in).
func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"CONFIG_FILE", "GITSTATE_ENV", "PUBLIC_URL", "HTTP_ADDR",
		"DATABASE_URL", "DATABASE_MAX_CONNS",
		"JWT_SIGNING_KEY", "ACCESS_TOKEN_TTL", "REFRESH_TOKEN_TTL",
		"OAUTH_GOOGLE_CLIENT_ID", "OAUTH_GOOGLE_CLIENT_SECRET",
		"OAUTH_MICROSOFT_CLIENT_ID", "OAUTH_MICROSOFT_CLIENT_SECRET", "OAUTH_MICROSOFT_TENANT",
		"BILLING_ENABLED", "SUPER_ADMIN_EMAILS",
	} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	// Run from a temp dir so stray .env files in the package dir aren't read.
	dir := t.TempDir()
	wd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
}

// TestDefaults verifies Load returns sane defaults with no file and no env.
func TestDefaults(t *testing.T) {
	clearConfigEnv(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.App.Name != "gitstate" {
		t.Errorf("App.Name = %q, want gitstate", cfg.App.Name)
	}
	if cfg.App.Env != "dev" {
		t.Errorf("App.Env = %q, want dev", cfg.App.Env)
	}
	if !cfg.Database.RLS {
		t.Error("Database.RLS should default true")
	}
	if !cfg.Auth.Password {
		t.Error("Auth.Password should default true")
	}
	if cfg.Auth.Providers.Google.Enabled || cfg.Auth.Providers.Microsoft.Enabled {
		t.Error("no provider should be enabled by default")
	}
}

// TestOverlayEnvWins verifies env vars override file/default values across the
// string/bool/int/duration setters.
func TestOverlayEnvWins(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("HTTP_ADDR", ":9999")
	t.Setenv("DATABASE_URL", "postgres://env/db")
	t.Setenv("DATABASE_MAX_CONNS", "42")
	t.Setenv("ACCESS_TOKEN_TTL", "5m")
	t.Setenv("BILLING_ENABLED", "true")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.App.HTTPAddr != ":9999" {
		t.Errorf("HTTPAddr = %q, want :9999 (env should win)", cfg.App.HTTPAddr)
	}
	if cfg.Database.URL != "postgres://env/db" {
		t.Errorf("Database.URL = %q, want env value", cfg.Database.URL)
	}
	if cfg.Database.MaxConns != 42 {
		t.Errorf("MaxConns = %d, want 42", cfg.Database.MaxConns)
	}
	if cfg.Auth.AccessTokenTTL.String() != "5m0s" {
		t.Errorf("AccessTokenTTL = %v, want 5m", cfg.Auth.AccessTokenTTL)
	}
	if !cfg.Billing.Enabled {
		t.Error("Billing.Enabled should be true from env")
	}
}

// TestEnvWinsOverFile verifies that a value present in BOTH a config file and an
// env var resolves to the env var.
func TestEnvWinsOverFile(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("app:\n  http_addr: \":1111\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CONFIG_FILE", cfgPath)
	t.Setenv("HTTP_ADDR", ":2222")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.App.HTTPAddr != ":2222" {
		t.Errorf("HTTPAddr = %q, want :2222 (env beats file)", cfg.App.HTTPAddr)
	}
}

// TestFileUsedWhenEnvUnset verifies the file value survives when no env override
// is present (overlayEnv only sets on non-empty env).
func TestFileUsedWhenEnvUnset(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("app:\n  http_addr: \":3333\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CONFIG_FILE", cfgPath)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.App.HTTPAddr != ":3333" {
		t.Errorf("HTTPAddr = %q, want :3333 (file value)", cfg.App.HTTPAddr)
	}
}

// TestProviderEnabledDerivation verifies Enabled is true iff BOTH id and secret
// are set, for every derived provider flag.
func TestProviderEnabledDerivation(t *testing.T) {
	cases := []struct {
		name   string
		idKey  string
		secKey string
		get    func(*config.Config) bool
	}{
		{"google", "OAUTH_GOOGLE_CLIENT_ID", "OAUTH_GOOGLE_CLIENT_SECRET", func(c *config.Config) bool { return c.Auth.Providers.Google.Enabled }},
		{"microsoft", "OAUTH_MICROSOFT_CLIENT_ID", "OAUTH_MICROSOFT_CLIENT_SECRET", func(c *config.Config) bool { return c.Auth.Providers.Microsoft.Enabled }},
	}

	for _, tc := range cases {
		t.Run(tc.name+"/both-set", func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv(tc.idKey, "id")
			t.Setenv(tc.secKey, "secret")
			cfg, err := config.Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if !tc.get(cfg) {
				t.Error("Enabled should be true when id+secret both set")
			}
		})
		t.Run(tc.name+"/id-only", func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv(tc.idKey, "id")
			cfg, err := config.Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if tc.get(cfg) {
				t.Error("Enabled should be false when secret missing")
			}
		})
		t.Run(tc.name+"/secret-only", func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv(tc.secKey, "secret")
			cfg, err := config.Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if tc.get(cfg) {
				t.Error("Enabled should be false when id missing")
			}
		})
		t.Run(tc.name+"/neither", func(t *testing.T) {
			clearConfigEnv(t)
			cfg, err := config.Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if tc.get(cfg) {
				t.Error("Enabled should be false when neither set")
			}
		})
	}
}

// TestDotEnvDoesNotOverwriteRealEnv verifies loadDotEnv only fills vars that are
// NOT already in the real environment — a real env var must win over .env.
func TestDotEnvDoesNotOverwriteRealEnv(t *testing.T) {
	clearConfigEnv(t)
	// We are in a temp cwd; write a .env that tries to set HTTP_ADDR.
	if err := os.WriteFile(".env", []byte("HTTP_ADDR=:7777\nPUBLIC_URL=https://from-dotenv\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	// Real env sets HTTP_ADDR — it must win over the .env value.
	t.Setenv("HTTP_ADDR", ":8888")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.App.HTTPAddr != ":8888" {
		t.Errorf("HTTPAddr = %q, want :8888 (real env beats .env)", cfg.App.HTTPAddr)
	}
	// PUBLIC_URL was NOT in real env, so the .env value should be picked up.
	if cfg.App.PublicURL != "https://from-dotenv" {
		t.Errorf("PublicURL = %q, want https://from-dotenv (from .env)", cfg.App.PublicURL)
	}
}

// TestExpandEnvInYAML verifies ${VAR} references in the config file are expanded
// from the environment before YAML parsing.
func TestExpandEnvInYAML(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("auth:\n  jwt_signing_key: \"${MY_SECRET}\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CONFIG_FILE", cfgPath)
	t.Setenv("MY_SECRET", "s3cr3t-from-env")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Auth.JWTSigningKey != "s3cr3t-from-env" {
		t.Errorf("JWTSigningKey = %q, want expanded value", cfg.Auth.JWTSigningKey)
	}
}
