// Package api wires the HTTP router and registers all API handlers.
// Routing uses stdlib net/http.ServeMux with Go 1.22 pattern syntax (METHOD /path).
package api

import (
	"encoding/json"
	"net/http"

	"github.com/exo/gitstate/internal/admin"
	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/middleware"
	"github.com/exo/gitstate/internal/web"

	eeadmin "github.com/exo/gitstate/ee/admin"
	eebilling "github.com/exo/gitstate/ee/billing"
)

// NewRouter builds and returns the fully wired http.Handler for gitstate.
// Middleware chain (outermost → innermost): Recoverer → Logger → CORS → AuthContext → mux.
func NewRouter(cfg *config.Config, database *db.DB) http.Handler {
	mux := http.NewServeMux()

	// Register handlers.
	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.HandleFunc("GET /api/config", handleAPIConfig(cfg))

	// Public docs API (embedded markdown; no DB required).
	RegisterDocsRoutes(mux)

	// Feature route registration (orchestrator-wired; see PROGRESS.md route-wiring rule).
	// These need the DB; in dev-without-DB boots we skip them rather than nil-panic.
	if database != nil {
		RegisterPublicPlans(mux, database, cfg) // public pricing data
		RegisterAuthRoutes(mux, database, cfg)
		RegisterOAuthRoutes(mux, database, cfg)
		RegisterOrgRoutes(mux, database, cfg)
		RegisterProjectRoutes(mux, database, cfg)
		RegisterSyncRoutes(mux, database, cfg)
		RegisterConnectRoutes(mux, database, cfg)
		RegisterLLMSettingsRoutes(mux, database, cfg)
		RegisterCalendarRoutes(mux, database, cfg)
		RegisterMetricsRoutes(mux, database, cfg)
		RegisterAnalyticsRoutes(mux, database, cfg)
		RegisterReportRoutes(mux, database, cfg)
		RegisterCapacityRoutes(mux, database, cfg)
		RegisterBillingRoutes(mux, database, cfg)
		// EE Paystack charging: real when built with -tags ee, no-op stub otherwise.
		eebilling.RegisterPaystackRoutes(mux, database, cfg)
		// Super-admin console (server-rendered HTML) + EE cross-org (audited).
		admin.RegisterAdminRoutes(mux, database, cfg)
		eeadmin.RegisterEEAdminRoutes(mux, database, cfg)
	}

	// Catch-all: serve the embedded React app (SPA fallback). Registered last so all
	// /api, /auth, /admin, /healthz patterns match first. Serves a dev placeholder if
	// no web build has been embedded.
	mux.Handle("/", web.Handler())

	// Apply middleware chain.
	corsOrigin := cfg.App.PublicURL
	if corsOrigin == "" {
		corsOrigin = "http://localhost:5173" // Vite default
	}

	return middleware.Chain(
		mux,
		middleware.Recoverer,
		middleware.Logger,
		middleware.RateLimit(300), // per-IP global rate limit (token bucket)
		middleware.CORS(corsOrigin),
		middleware.AuthContext,
	)
}

// handleHealthz returns a simple liveness probe.
func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// publicConfigResponse is the exact JSON shape the frontend expects.
// The web agent depends on this contract — do not change field names.
type publicConfigResponse struct {
	PublicURL string            `json:"publicUrl"`
	Auth      publicAuthConfig  `json:"auth"`
	Billing   publicBillingInfo `json:"billing"`
}

type publicAuthConfig struct {
	Password  bool                   `json:"password"`
	Providers publicProviderStatuses `json:"providers"`
}

type publicProviderStatuses struct {
	Google    bool `json:"google"`
	Microsoft bool `json:"microsoft"`
}

type publicBillingInfo struct {
	ChargeCurrency string `json:"chargeCurrency"`
}

// handleAPIConfig returns the public configuration the frontend needs to render
// the login page (which OAuth buttons to show, billing currency, etc.).
func handleAPIConfig(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		resp := publicConfigResponse{
			PublicURL: cfg.App.PublicURL,
			Auth: publicAuthConfig{
				Password: cfg.Auth.Password,
				Providers: publicProviderStatuses{
					Google:    cfg.Auth.Providers.Google.Enabled,
					Microsoft: cfg.Auth.Providers.Microsoft.Enabled,
				},
			},
			Billing: publicBillingInfo{
				ChargeCurrency: cfg.Billing.ChargeCurrency,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			http.Error(w, "failed to encode config", http.StatusInternalServerError)
		}
	}
}
