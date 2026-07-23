// Package store — llm_settings.go
//
// Per-org LLM configuration persistence (migration 20260618_006_org_llm_settings).
//
// Each org is one of two LLM key modes:
//   - "managed": uses the platform Anthropic key.
//   - "byok":    brings its own provider API key. The key is stored AES-256-GCM
//     encrypted (internal/crypto).
//
// All functions run inside an org-scoped transaction (pgx.Tx); callers MUST use
// db.WithOrg so the RLS policy (org_id = current_org()) is enforced and cross-org
// settings (and the encrypted BYOK key) cannot leak.
//
// SECURITY: the raw/decrypted BYOK key is NEVER returned to API callers. Only
// GetDecryptedKey (server-side, for the LLM client) ever returns plaintext, and
// that value must never be logged or serialised into a response.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// LLMSettings is the non-secret, API-safe view of an org's LLM configuration.
// It deliberately does NOT carry the decrypted key — only HasKey signals whether
// a BYOK key is stored.
type LLMSettings struct {
	OrgID    string
	Mode     string // "managed" | "byok"
	Provider string // e.g. "anthropic"
	Model    string // optional override; "" falls back to the config default
	HasKey   bool   // true when an encrypted BYOK key is stored
}

// GetLLMSettings returns the org's LLM settings, never the raw key.
// When no row exists yet it returns sensible defaults (managed / anthropic) with
// HasKey=false rather than ErrNotFound, so callers always get a usable value.
// Must be called inside db.WithOrg (tx already has the RLS context set).
func GetLLMSettings(ctx context.Context, tx pgx.Tx, orgID string) (LLMSettings, error) {
	const q = `
		SELECT mode,
		       provider,
		       COALESCE(model, ''),
		       (api_key_encrypted IS NOT NULL AND length(api_key_encrypted) > 0) AS has_key
		FROM org_llm_settings
		WHERE org_id = $1`

	s := LLMSettings{OrgID: orgID, Mode: "managed", Provider: "anthropic"}
	err := tx.QueryRow(ctx, q, orgID).Scan(&s.Mode, &s.Provider, &s.Model, &s.HasKey)
	if errors.Is(err, pgx.ErrNoRows) {
		// No settings configured yet — return defaults.
		return s, nil
	}
	if err != nil {
		return LLMSettings{}, fmt.Errorf("store: get llm settings: %w", err)
	}
	return s, nil
}

// UpsertLLMSettings inserts or updates the org's LLM settings.
//
// keyEncrypted semantics:
//   - non-nil, non-empty → store these encrypted bytes as the BYOK key.
//   - nil                → leave any existing stored key untouched (e.g. PUT that
//     only changes the model while keeping the saved key).
//   - empty slice ([]byte{}) → explicitly clear the stored key (used when switching
//     to managed mode).
//
// updatedBy is the acting user's UUID (may be "" if unknown).
// Must be called inside db.WithOrg (tx already has the RLS context set).
func UpsertLLMSettings(ctx context.Context, tx pgx.Tx, orgID, mode, provider, model, updatedBy string, keyEncrypted []byte) error {
	// Normalise empty model to NULL so the COALESCE in reads behaves predictably.
	var modelArg any
	if model != "" {
		modelArg = model
	}
	var updatedByArg any
	if updatedBy != "" {
		updatedByArg = updatedBy
	}

	if keyEncrypted == nil {
		// Preserve the existing key; update everything else.
		const q = `
			INSERT INTO org_llm_settings (org_id, mode, provider, model, updated_by, updated_at)
			VALUES ($1, $2, $3, $4, $5, now())
			ON CONFLICT (org_id) DO UPDATE SET
				mode       = EXCLUDED.mode,
				provider   = EXCLUDED.provider,
				model      = EXCLUDED.model,
				updated_by = EXCLUDED.updated_by,
				updated_at = now()`
		if _, err := tx.Exec(ctx, q, orgID, mode, provider, modelArg, updatedByArg); err != nil {
			return fmt.Errorf("store: upsert llm settings: %w", err)
		}
		return nil
	}

	// Set (or clear, when empty slice) the encrypted key as well.
	var keyArg any
	if len(keyEncrypted) > 0 {
		keyArg = keyEncrypted
	} // else leave nil → NULL (clears the key)

	const q = `
		INSERT INTO org_llm_settings (org_id, mode, provider, api_key_encrypted, model, updated_by, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (org_id) DO UPDATE SET
			mode              = EXCLUDED.mode,
			provider          = EXCLUDED.provider,
			api_key_encrypted = EXCLUDED.api_key_encrypted,
			model             = EXCLUDED.model,
			updated_by        = EXCLUDED.updated_by,
			updated_at        = now()`
	if _, err := tx.Exec(ctx, q, orgID, mode, provider, keyArg, modelArg, updatedByArg); err != nil {
		return fmt.Errorf("store: upsert llm settings: %w", err)
	}
	return nil
}

// GetDecryptedKey returns the stored encrypted BYOK key bytes for an org, or nil
// when none is stored. This is the ONLY function that surfaces the key material;
// the caller (internal/llm) decrypts it via internal/crypto and uses it strictly
// server-side. The plaintext must never be logged or returned over the API.
//
// Must be called inside db.WithOrg (tx already has the RLS context set).
func GetDecryptedKey(ctx context.Context, tx pgx.Tx, orgID string) ([]byte, error) {
	const q = `SELECT api_key_encrypted FROM org_llm_settings WHERE org_id = $1`

	var encrypted []byte
	err := tx.QueryRow(ctx, q, orgID).Scan(&encrypted)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: get decrypted key: %w", err)
	}
	return encrypted, nil
}
