package llm

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/crypto"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/store"
)

// Configured reports whether this Service has a usable provider (an API key was
// present when it was constructed). The managed/platform Service is "configured"
// only when cfg.LLM.AnthropicAPIKey is set — surfaced to the UI as
// managedAvailable so self-hosters without a platform key see BYOK as the path.
func (s *Service) Configured() bool {
	return s.provider != nil
}

// ForOrg resolves the LLM Service to use for a specific org, honouring the org's
// BYOK-vs-managed choice in org_llm_settings (migration 006):
//
//   - mode == "byok" WITH a stored, decryptable key → returns a Service backed by
//     the org's own provider key. managed=false → we incur $0 managed LLM cost and
//     callers must NOT record llm_tokens usage for billing.
//   - otherwise (mode == "managed", or byok with no/undecryptable key) → returns
//     the platform Service (the receiver s) unchanged. managed=true → callers record
//     managed usage via store.RecordUsage so it flows into billing overage.
//
// The returned bool is "managed". ForOrg never returns an error for the common
// "no settings / no key" cases — it falls back to managed. It only errors on an
// unexpected DB failure. The resolution read runs inside db.WithOrg so RLS fires
// and the encrypted key cannot be read cross-org.
//
// This path is additive: existing callers that use the platform Service directly
// keep working unchanged; only call sites that want per-org routing invoke ForOrg.
func (s *Service) ForOrg(ctx context.Context, database *db.DB, orgID string) (svc *Service, managed bool, err error) {
	var (
		settings store.LLMSettings
		encKey   []byte
	)
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var e error
		settings, e = store.GetLLMSettings(ctx, tx, orgID)
		if e != nil {
			return e
		}
		if settings.Mode == "byok" && settings.HasKey {
			encKey, e = store.GetDecryptedKey(ctx, tx, orgID)
			if e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		return nil, true, fmt.Errorf("llm.ForOrg: load settings: %w", err)
	}

	// BYOK path: build a Service from the org's own key.
	if settings.Mode == "byok" && len(encKey) > 0 {
		key, err := crypto.KeyFromEnv()
		if err != nil {
			// No encryption key available to decrypt — fall back to managed rather
			// than failing the request. (Never log the ciphertext.)
			return s, true, nil
		}
		plain, err := crypto.Decrypt(encKey, key)
		if err != nil {
			// Corrupt/garbled ciphertext — fall back to managed.
			return s, true, nil
		}
		model := settings.Model
		if model == "" {
			model = "claude-sonnet-4-6"
		}
		byok := &Service{
			provider: newAnthropicClient(string(plain), model),
			model:    model,
		}
		return byok, false, nil
	}

	// Managed path: use the platform Service as-is.
	return s, true, nil
}

// ManagedAvailable is a tiny helper for the settings API: reports whether the
// platform key is configured (so the UI can offer the managed option) without
// exposing the key itself.
func ManagedAvailable(cfg *config.Config) bool {
	return cfg.LLM.AnthropicAPIKey != ""
}
