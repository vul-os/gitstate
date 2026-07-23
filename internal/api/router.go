// Package api wires the HTTP router and registers all API handlers.
// Routing uses stdlib net/http.ServeMux with Go 1.22 pattern syntax (METHOD /path).
package api

import (
	"encoding/json"
	"net/http"

	"github.com/exo/gitstate/internal/admin"
	"github.com/exo/gitstate/internal/analytics"
	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/middleware"
	"github.com/exo/gitstate/internal/web"
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

	// Inbound GitHub/GitLab webhook receiver (signature-verified, no session auth).
	if database != nil {
		RegisterWebhookReceiver(mux, database)
	}

	// Feature route registration (orchestrator-wired; see PROGRESS.md route-wiring rule).
	// These need the DB; in dev-without-DB boots we skip them rather than nil-panic.
	if database != nil {
		RegisterPublicPlans(mux, database, cfg) // public pricing data
		RegisterAuthRoutes(mux, database, cfg)
		RegisterOAuthRoutes(mux, database, cfg)
		RegisterOrgRoutes(mux, database, cfg)
		RegisterProfileRoutes(mux, database, cfg)
		RegisterProjectRoutes(mux, database, cfg)
		RegisterSyncRoutes(mux, database, cfg)
		RegisterConnectRoutes(mux, database, cfg)
		RegisterLLMSettingsRoutes(mux, database, cfg)
		RegisterCalendarRoutes(mux, database, cfg)
		RegisterMetricsRoutes(mux, database, cfg)
		RegisterAnalyticsRoutes(mux, database, cfg)
		RegisterContributionRoutes(mux, database, cfg)
		RegisterContributorRoutes(mux, database, cfg)
		RegisterEngHealthRoutes(mux, database, cfg)
		RegisterPlanningRoutes(mux, database, cfg)
		RegisterNotificationRoutes(mux, database, cfg)
		RegisterWebhookRoutes(mux, database, cfg)
		RegisterImportRoutes(mux, database, cfg)
		RegisterReportRoutes(mux, database, cfg)
		RegisterCapacityRoutes(mux, database, cfg)
		RegisterEstimationRoutes(mux, database, cfg)
		RegisterTokenRoutes(mux, database, cfg)
		RegisterContextRoutes(mux, database, cfg)
		RegisterAgentRunRoutes(mux, database, cfg)
		RegisterSearchRoutes(mux, database, cfg)
		// Super-admin console (server-rendered HTML).
		admin.RegisterAdminRoutes(mux, database, cfg)
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

	// Geo-analytics capture (innermost: runs after AuthContext so it sees the
	// principal; records signup/login/pageview events with a salted IP hash +
	// coarse geo, off the request path). Graceful when DB/mmdb/salt are absent.
	geo := analytics.NewGeoResolver(cfg.Admin.GeoIPDBPath)
	capture := analytics.Capture(database, cfg, geo)

	return middleware.Chain(
		mux,
		middleware.Recoverer,
		middleware.Logger,
		middleware.RateLimit(300), // per-IP global rate limit (token bucket)
		middleware.CORS(corsOrigin),
		middleware.AuthContext,
		capture,
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

// publicProviderStatuses lists which "Sign in with" buttons to render. The login
// page surfaces developer identities (GitHub/GitLab); Google/Microsoft are used
// for calendar, not login.
type publicProviderStatuses struct {
	GitHub bool `json:"github"`
	GitLab bool `json:"gitlab"`
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
					GitHub: cfg.Git.GitHub.LoginEnabled,
					GitLab: cfg.Git.GitLab.LoginEnabled,
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
