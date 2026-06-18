// Package api — settings_llm.go
//
// Per-org LLM settings endpoints: choose BYOK (bring your own provider key →
// $0 managed LLM cost, no overage) vs managed (platform key, metered + billed as
// overage on the per-builder tier). Backed by org_llm_settings (migration 006)
// and AES-256-GCM encryption (internal/crypto).
//
// Routes (RequireAuth + OrgScope; X-Org-ID required):
//
//	GET /api/settings/llm  → {mode, provider, model, hasKey, managedAvailable}
//	PUT /api/settings/llm  → {mode, provider?, apiKey?, model?}
//
// SECURITY: the raw BYOK key is write-only — it is encrypted on receipt and never
// returned, never logged. GET surfaces only hasKey. Only owners/admins may PUT.
package api

import (
	"net/http"
	"strings"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/crypto"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/llm"
	"github.com/exo/gitstate/internal/middleware"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// RegisterLLMSettingsRoutes wires the /api/settings/llm endpoints onto mux.
// Called by the orchestrator from router.go — this package does NOT edit router.go.
func RegisterLLMSettingsRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	h := &llmSettingsHandlers{db: database, cfg: cfg}
	requireAuth := middleware.RequireAuth(cfg.Auth.JWTSigningKey)
	orgScope := middleware.OrgScope(database.Pool())
	auth := func(handler http.Handler) http.Handler {
		return requireAuth(orgScope(handler))
	}

	mux.Handle("GET /api/settings/llm", auth(http.HandlerFunc(h.get)))
	mux.Handle("PUT /api/settings/llm", auth(http.HandlerFunc(h.put)))
}

type llmSettingsHandlers struct {
	db  *db.DB
	cfg *config.Config
}

// ── Response / request types ───────────────────────────────────────────────────

// llmSettingsResponse is the API-safe view. It NEVER carries the raw key.
type llmSettingsResponse struct {
	Mode             string `json:"mode"`     // "managed" | "byok"
	Provider         string `json:"provider"` // e.g. "anthropic"
	Model            string `json:"model"`    // "" → config default
	HasKey           bool   `json:"hasKey"`   // a BYOK key is stored
	ManagedAvailable bool   `json:"managedAvailable"`
}

type llmSettingsRequest struct {
	Mode     string  `json:"mode"`
	Provider *string `json:"provider,omitempty"`
	APIKey   *string `json:"apiKey,omitempty"`
	Model    *string `json:"model,omitempty"`
}

// ── GET /api/settings/llm ──────────────────────────────────────────────────────

func (h *llmSettingsHandlers) get(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())

	var s store.LLMSettings
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		var e error
		s, e = store.GetLLMSettings(r.Context(), tx, orgID)
		return e
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "load llm settings")
		return
	}

	writeJSON(w, http.StatusOK, llmSettingsResponse{
		Mode:             s.Mode,
		Provider:         s.Provider,
		Model:            s.Model,
		HasKey:           s.HasKey,
		ManagedAvailable: llm.ManagedAvailable(h.cfg),
	})
}

// ── PUT /api/settings/llm ──────────────────────────────────────────────────────

func (h *llmSettingsHandlers) put(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	user := middleware.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	// Only owners/admins may change billing-relevant LLM configuration.
	role, err := store.GetMemberRole(r.Context(), h.db.Pool(), orgID, user.ID)
	if err != nil || !canManageMembers(role) {
		writeError(w, http.StatusForbidden, "only owners and admins can change LLM settings")
		return
	}

	var req llmSettingsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	mode := strings.TrimSpace(req.Mode)
	if mode != "managed" && mode != "byok" {
		writeError(w, http.StatusBadRequest, "mode must be 'managed' or 'byok'")
		return
	}

	provider := "anthropic"
	if req.Provider != nil && strings.TrimSpace(*req.Provider) != "" {
		provider = strings.TrimSpace(*req.Provider)
	}
	if provider != "anthropic" {
		writeError(w, http.StatusBadRequest, "unsupported provider (only 'anthropic' is available)")
		return
	}

	var model string
	if req.Model != nil {
		model = strings.TrimSpace(*req.Model)
	}

	// Resolve the encrypted-key argument for the upsert.
	//   managed                       → empty slice → clear any stored key.
	//   byok + apiKey provided        → encrypt and store the new key.
	//   byok + apiKey omitted/empty   → nil → keep the existing key (must already have one).
	var keyEncrypted []byte
	switch {
	case mode == "managed":
		if !llm.ManagedAvailable(h.cfg) {
			writeError(w, http.StatusBadRequest, "managed mode is unavailable: no platform LLM key is configured")
			return
		}
		keyEncrypted = []byte{} // explicit clear
	case req.APIKey != nil && strings.TrimSpace(*req.APIKey) != "":
		key, err := crypto.KeyFromEnv()
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "encryption is not configured on this server")
			return
		}
		ct, err := crypto.Encrypt([]byte(strings.TrimSpace(*req.APIKey)), key)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encrypt api key")
			return
		}
		keyEncrypted = ct
	default:
		// byok with no new key → require an existing one.
		var existing store.LLMSettings
		if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
			var e error
			existing, e = store.GetLLMSettings(r.Context(), tx, orgID)
			return e
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "load llm settings")
			return
		}
		if !existing.HasKey {
			writeError(w, http.StatusBadRequest, "byok mode requires an apiKey")
			return
		}
		keyEncrypted = nil // keep existing
	}

	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		return store.UpsertLLMSettings(r.Context(), tx, orgID, mode, provider, model, user.ID, keyEncrypted)
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "save llm settings")
		return
	}

	// Re-read to return the canonical, key-free view.
	var s store.LLMSettings
	if err := h.db.WithOrg(r.Context(), orgID, func(tx pgx.Tx) error {
		var e error
		s, e = store.GetLLMSettings(r.Context(), tx, orgID)
		return e
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "load llm settings")
		return
	}

	writeJSON(w, http.StatusOK, llmSettingsResponse{
		Mode:             s.Mode,
		Provider:         s.Provider,
		Model:            s.Model,
		HasKey:           s.HasKey,
		ManagedAvailable: llm.ManagedAvailable(h.cfg),
	})
}
