-- 20260618_004_per_builder_pricing
-- Per-BUILDER tier model (validated by cmd/billsim, now canonical).
-- Stakeholders are always free (decisions P6). LLM: included per-builder allowance,
-- overage billed at provider-cost × overage_markup; BYOK → $0. Bill USD (decisions A8).
-- forward-only; a rollback is a new migration.

-- New per-builder pricing columns on plans. usd_cents is kept for back-compat
-- (set to 0 for per-builder tiers).
ALTER TABLE plans
  ADD COLUMN IF NOT EXISTS per_builder_cents int     NOT NULL DEFAULT 0,  -- monthly price per billable builder
  ADD COLUMN IF NOT EXISTS included_llm_cents int    NOT NULL DEFAULT 0,  -- included managed-LLM allowance per builder/mo (at our provider cost)
  ADD COLUMN IF NOT EXISTS overage_markup    numeric NOT NULL DEFAULT 1.0; -- markup on managed-LLM usage beyond the allowance

-- New ladder (builders = cap: free 2, others 0 = unlimited).
--   free       — $0/builder, ≤2 builders, BYOK-only (no managed allowance), scale-to-zero.
--   team       — $12/builder, $4/builder included LLM, overage ×1.30.
--   business   — $25/builder, $12/builder included LLM, overage ×1.30, + SSO/audit.
--   enterprise — custom (self-host / BYOK / unlimited).
INSERT INTO plans (key, name, usd_cents, per_builder_cents, included_llm_cents, overage_markup, builders, max_conns, features) VALUES
  ('free',       'Free',       0, 0,    0,    1.00, 2, 10,  '{"byok_only": true, "scale_to_zero": true}'),
  ('team',       'Team',       0, 1200, 400,  1.30, 0, 0,   '{}'),
  ('business',   'Business',   0, 2500, 1200, 1.30, 0, 0,   '{"sso": true, "audit": true}'),
  ('enterprise', 'Enterprise', 0, 0,    0,    1.00, 0, 0,   '{"custom": true, "self_host": true, "byok": true, "unlimited": true}')
ON CONFLICT (key) DO UPDATE SET
  name               = EXCLUDED.name,
  usd_cents          = EXCLUDED.usd_cents,
  per_builder_cents  = EXCLUDED.per_builder_cents,
  included_llm_cents = EXCLUDED.included_llm_cents,
  overage_markup     = EXCLUDED.overage_markup,
  builders           = EXCLUDED.builders,
  max_conns          = EXCLUDED.max_conns,
  features           = EXCLUDED.features;
