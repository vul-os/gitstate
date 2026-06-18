-- 20260618_006_org_llm_settings
-- Per-org LLM configuration: BYOK (bring your own provider key → $0 managed cost)
-- or managed (use the platform key, metered + billed as overage on the per-builder tier).
-- forward-only.

CREATE TABLE org_llm_settings (
    org_id            uuid PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    mode              text NOT NULL DEFAULT 'managed',  -- managed | byok
    provider          text NOT NULL DEFAULT 'anthropic',
    api_key_encrypted bytea,                            -- AES-256-GCM encrypted BYOK key (internal/crypto)
    model             text,                             -- optional override; falls back to config default
    updated_by        uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_at        timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE org_llm_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE org_llm_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY org_isolation ON org_llm_settings
    USING (org_id = current_org())
    WITH CHECK (org_id = current_org());
