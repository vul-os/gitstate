// Package config loads gitstate configuration from config.yaml and environment variables.
// Environment variables always win over config file values (twelve-factor, decisions A11).
// OAuth providers are auto-enabled when their client ID and secret are both present (decisions A6).
package config

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration structure mirroring config.example.yaml.
type Config struct {
	App      AppConfig      `yaml:"app"`
	Database DatabaseConfig `yaml:"database"`
	Auth     AuthConfig     `yaml:"auth"`
	Git      GitConfig      `yaml:"git"`
	LLM      LLMConfig      `yaml:"llm"`
	Billing  BillingConfig  `yaml:"billing"`
	Admin    AdminConfig    `yaml:"admin"`
}

// AppConfig holds core application settings.
type AppConfig struct {
	Name      string `yaml:"name"`
	Env       string `yaml:"env"` // dev | prod
	PublicURL string `yaml:"public_url"`
	HTTPAddr  string `yaml:"http_addr"`
}

// DatabaseConfig holds Postgres connection settings.
type DatabaseConfig struct {
	URL      string `yaml:"url"`
	MaxConns int    `yaml:"max_conns"`
	RLS      bool   `yaml:"rls"`
}

// AuthConfig holds JWT and OAuth provider settings.
type AuthConfig struct {
	JWTSigningKey   string          `yaml:"jwt_signing_key"`
	AccessTokenTTL  time.Duration   `yaml:"access_token_ttl"`
	RefreshTokenTTL time.Duration   `yaml:"refresh_token_ttl"`
	Password        bool            `yaml:"password"`
	Providers       ProvidersConfig `yaml:"providers"`
}

// ProvidersConfig holds the OAuth provider sub-configs.
type ProvidersConfig struct {
	Google    GoogleConfig    `yaml:"google"`
	Microsoft MicrosoftConfig `yaml:"microsoft"`
}

// GoogleConfig holds Google OAuth credentials.
// Enabled is derived: true iff ClientID and ClientSecret are both non-empty.
type GoogleConfig struct {
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	Enabled      bool   `yaml:"-"` // derived
}

// MicrosoftConfig holds Microsoft OAuth credentials.
// Enabled is derived: true iff ClientID and ClientSecret are both non-empty.
type MicrosoftConfig struct {
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	Tenant       string `yaml:"tenant"`
	Enabled      bool   `yaml:"-"` // derived
}

// GitConfig holds GitHub and GitLab integration settings.
type GitConfig struct {
	GitHub GitHubConfig `yaml:"github"`
	GitLab GitLabConfig `yaml:"gitlab"`
}

// GitHubConfig holds GitHub OAuth app credentials.
type GitHubConfig struct {
	OAuthClientID     string `yaml:"oauth_client_id"`
	OAuthClientSecret string `yaml:"oauth_client_secret"`
}

// GitLabConfig holds GitLab OAuth app credentials.
type GitLabConfig struct {
	OAuthClientID     string `yaml:"oauth_client_id"`
	OAuthClientSecret string `yaml:"oauth_client_secret"`
}

// LLMConfig holds LLM provider settings.
type LLMConfig struct {
	Provider        string `yaml:"provider"`
	Model           string `yaml:"model"`
	AnthropicAPIKey string `yaml:"anthropic_api_key"`
}

// BillingConfig holds EE billing settings.
type BillingConfig struct {
	Enabled        bool           `yaml:"enabled"`
	BillCurrency   string         `yaml:"bill_currency"`
	ChargeCurrency string         `yaml:"charge_currency"`
	Paystack       PaystackConfig `yaml:"paystack"`
	Exchange       ExchangeConfig `yaml:"exchange"`
	Plans          []PlanConfig   `yaml:"plans"`
}

// PaystackConfig holds Paystack API credentials.
type PaystackConfig struct {
	SecretKey     string `yaml:"secret_key"`
	PublicKey     string `yaml:"public_key"`
	WebhookSecret string `yaml:"webhook_secret"`
}

// ExchangeConfig holds exchange rate provider settings.
type ExchangeConfig struct {
	Provider string        `yaml:"provider"`
	APIKey   string        `yaml:"api_key"`
	TTL      time.Duration `yaml:"ttl"`
}

// PlanConfig describes a billing plan tier (per-builder model).
type PlanConfig struct {
	Key            string  `yaml:"key"`
	Name           string  `yaml:"name"`
	USD            int     `yaml:"usd"`              // legacy flat price (kept for back-compat)
	PerBuilderUSD  int     `yaml:"per_builder_usd"`  // monthly price per billable builder
	IncludedLLMUSD int     `yaml:"included_llm_usd"` // included managed-LLM allowance per builder/mo
	OverageMarkup  float64 `yaml:"overage_markup"`   // markup on managed-LLM usage beyond allowance
	Builders       int     `yaml:"builders"`         // cap: 0 = unlimited
	MaxConns       int     `yaml:"max_conns"`
}

// AdminConfig holds super-admin settings.
type AdminConfig struct {
	SuperAdminEmails string `yaml:"super_admin_emails"`
	Realtime         bool   `yaml:"realtime"`

	// DatabaseURL is an optional, separate Postgres connection string for the
	// super-admin's legitimate CROSS-ORG aggregate reads (MRR, revenue, plan
	// distribution, signups-by-day, org list). It is expected to point at a
	// dedicated BYPASSRLS role (e.g. gitstate_admin) so these instance-wide
	// aggregates can read RLS-protected tables (subscriptions, usage_events,
	// org_members, …) without a single org context — which the non-superuser
	// app role cannot do (RLS hides cross-org rows, so MRR/revenue read 0).
	//
	// This is the audited service path of decisions S2 — used ONLY for admin
	// cross-org aggregates, never for normal org-scoped app traffic. When empty,
	// admin reads fall back to the main pool (current behavior; no special bypass).
	// Populated from the ADMIN_DATABASE_URL env var.
	DatabaseURL string `yaml:"-"`
}

// envVarRe matches ${ENV_VAR} references in YAML values.
var envVarRe = regexp.MustCompile(`\$\{([^}]+)\}`)

// expandEnv replaces ${VAR} references in s with values from os.Getenv.
func expandEnv(s string) string {
	return envVarRe.ReplaceAllStringFunc(s, func(m string) string {
		key := m[2 : len(m)-1] // strip ${ and }
		return os.Getenv(key)
	})
}

// loadDotEnv loads .env.<GITSTATE_ENV> (default dev) then .env from the working
// directory, setting only vars that aren't already present in the environment.
func loadDotEnv() {
	env := os.Getenv("GITSTATE_ENV")
	if env == "" {
		env = "dev"
	}
	for _, path := range []string{".env." + env, ".env"} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			k = strings.TrimSpace(k)
			v = strings.Trim(strings.TrimSpace(v), `"'`)
			if k != "" {
				if _, exists := os.LookupEnv(k); !exists {
					_ = os.Setenv(k, v)
				}
			}
		}
		_ = f.Close()
	}
}

// Load reads config from the file named by CONFIG_FILE env var (defaults to "config.yaml"),
// expands ${ENV_VAR} references, then overlays raw env vars. Env always wins.
// Returns a valid *Config even if the file is absent (pure env-var config is fine).
func Load() (*Config, error) {
	// Load .env.<GITSTATE_ENV> then .env (like cmd/migrate), so `go run ./cmd/gitstate`
	// and `go run ./cmd/seed` pick up local config without exporting vars by hand.
	// Existing env vars are never overwritten (real env still wins).
	loadDotEnv()

	cfg := defaultConfig()

	configFile := os.Getenv("CONFIG_FILE")
	if configFile == "" {
		configFile = "config.yaml"
	}

	data, err := os.ReadFile(configFile)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("config: read %s: %w", configFile, err)
	}

	if err == nil {
		// Expand ${ENV_VAR} references before parsing YAML.
		expanded := expandEnv(string(data))
		if yamlErr := yaml.Unmarshal([]byte(expanded), cfg); yamlErr != nil {
			return nil, fmt.Errorf("config: parse %s: %w", configFile, yamlErr)
		}
	}

	// Overlay raw env vars — env always wins over file values.
	overlayEnv(cfg)

	// Derive OAuth enabled flags (decisions A6).
	cfg.Auth.Providers.Google.Enabled =
		cfg.Auth.Providers.Google.ClientID != "" &&
			cfg.Auth.Providers.Google.ClientSecret != ""

	cfg.Auth.Providers.Microsoft.Enabled =
		cfg.Auth.Providers.Microsoft.ClientID != "" &&
			cfg.Auth.Providers.Microsoft.ClientSecret != ""

	return cfg, nil
}

// defaultConfig returns sensible defaults matching config.example.yaml.
func defaultConfig() *Config {
	return &Config{
		App: AppConfig{
			Name:     "gitstate",
			Env:      "dev",
			HTTPAddr: ":8080",
		},
		Database: DatabaseConfig{
			MaxConns: 10,
			RLS:      true,
		},
		Auth: AuthConfig{
			Password:        true,
			AccessTokenTTL:  15 * time.Minute,
			RefreshTokenTTL: 720 * time.Hour,
		},
		Billing: BillingConfig{
			BillCurrency:   "USD",
			ChargeCurrency: "ZAR",
		},
		Admin: AdminConfig{
			Realtime: true,
		},
	}
}

// overlayEnv applies individual env vars on top of whatever came from the file.
// Only non-empty env values override (so the file default is preserved when env is unset).
func overlayEnv(cfg *Config) {
	setStr := func(dest *string, key string) {
		if v := os.Getenv(key); v != "" {
			*dest = v
		}
	}
	setBool := func(dest *bool, key string) {
		if v := os.Getenv(key); v != "" {
			if b, err := strconv.ParseBool(v); err == nil {
				*dest = b
			}
		}
	}
	setInt := func(dest *int, key string) {
		if v := os.Getenv(key); v != "" {
			if i, err := strconv.Atoi(v); err == nil {
				*dest = i
			}
		}
	}
	setDuration := func(dest *time.Duration, key string) {
		if v := os.Getenv(key); v != "" {
			if d, err := time.ParseDuration(v); err == nil {
				*dest = d
			}
		}
	}

	// App
	setStr(&cfg.App.Env, "GITSTATE_ENV")
	setStr(&cfg.App.PublicURL, "PUBLIC_URL")
	setStr(&cfg.App.HTTPAddr, "HTTP_ADDR")

	// Database
	setStr(&cfg.Database.URL, "DATABASE_URL")
	setInt(&cfg.Database.MaxConns, "DATABASE_MAX_CONNS")

	// Auth
	setStr(&cfg.Auth.JWTSigningKey, "JWT_SIGNING_KEY")
	setDuration(&cfg.Auth.AccessTokenTTL, "ACCESS_TOKEN_TTL")
	setDuration(&cfg.Auth.RefreshTokenTTL, "REFRESH_TOKEN_TTL")

	// OAuth providers
	setStr(&cfg.Auth.Providers.Google.ClientID, "OAUTH_GOOGLE_CLIENT_ID")
	setStr(&cfg.Auth.Providers.Google.ClientSecret, "OAUTH_GOOGLE_CLIENT_SECRET")
	setStr(&cfg.Auth.Providers.Microsoft.ClientID, "OAUTH_MICROSOFT_CLIENT_ID")
	setStr(&cfg.Auth.Providers.Microsoft.ClientSecret, "OAUTH_MICROSOFT_CLIENT_SECRET")
	setStr(&cfg.Auth.Providers.Microsoft.Tenant, "OAUTH_MICROSOFT_TENANT")

	// Git
	setStr(&cfg.Git.GitHub.OAuthClientID, "GITHUB_OAUTH_CLIENT_ID")
	setStr(&cfg.Git.GitHub.OAuthClientSecret, "GITHUB_OAUTH_CLIENT_SECRET")
	setStr(&cfg.Git.GitLab.OAuthClientID, "GITLAB_OAUTH_CLIENT_ID")
	setStr(&cfg.Git.GitLab.OAuthClientSecret, "GITLAB_OAUTH_CLIENT_SECRET")

	// LLM
	setStr(&cfg.LLM.Provider, "LLM_PROVIDER")
	setStr(&cfg.LLM.Model, "LLM_MODEL")
	setStr(&cfg.LLM.AnthropicAPIKey, "ANTHROPIC_API_KEY")

	// Billing
	setBool(&cfg.Billing.Enabled, "BILLING_ENABLED")
	setStr(&cfg.Billing.BillCurrency, "BILLING_CURRENCY_BILL")
	setStr(&cfg.Billing.ChargeCurrency, "BILLING_CURRENCY_CHARGE")
	setStr(&cfg.Billing.Paystack.SecretKey, "PAYSTACK_SECRET_KEY")
	setStr(&cfg.Billing.Paystack.PublicKey, "PAYSTACK_PUBLIC_KEY")
	setStr(&cfg.Billing.Paystack.WebhookSecret, "PAYSTACK_WEBHOOK_SECRET")
	setStr(&cfg.Billing.Exchange.Provider, "EXCHANGE_PROVIDER")
	setStr(&cfg.Billing.Exchange.APIKey, "EXCHANGE_API_KEY")
	setDuration(&cfg.Billing.Exchange.TTL, "EXCHANGE_TTL")

	// Admin
	setStr(&cfg.Admin.SuperAdminEmails, "SUPER_ADMIN_EMAILS")
	// Separate audited BYPASSRLS service connection for cross-org admin
	// aggregates (decisions S2). Empty → fall back to the main pool.
	setStr(&cfg.Admin.DatabaseURL, "ADMIN_DATABASE_URL")
}
