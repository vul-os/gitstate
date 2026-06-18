// Package api wires the HTTP router and registers all API handlers.
// Routing uses stdlib net/http.ServeMux with Go 1.22 pattern syntax (METHOD /path).
package api

import (
	"encoding/json"
	"net/http"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/middleware"
)

// NewRouter builds and returns the fully wired http.Handler for gitstate.
// Middleware chain (outermost → innermost): Recoverer → Logger → CORS → AuthContext → mux.
func NewRouter(cfg *config.Config, database *db.DB) http.Handler {
	mux := http.NewServeMux()

	// Register handlers.
	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.HandleFunc("GET /api/config", handleAPIConfig(cfg))

	// Feature route registration (orchestrator-wired; see PROGRESS.md route-wiring rule).
	// These need the DB; in dev-without-DB boots we skip them rather than nil-panic.
	if database != nil {
		RegisterAuthRoutes(mux, database, cfg)
		RegisterOAuthRoutes(mux, database, cfg)
		RegisterOrgRoutes(mux, database, cfg)
		RegisterProjectRoutes(mux, database, cfg)
		RegisterSyncRoutes(mux, database, cfg)
	}

	// Apply middleware chain.
	corsOrigin := cfg.App.PublicURL
	if corsOrigin == "" {
		corsOrigin = "http://localhost:5173" // Vite default
	}

	return middleware.Chain(
		mux,
		middleware.Recoverer,
		middleware.Logger,
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
