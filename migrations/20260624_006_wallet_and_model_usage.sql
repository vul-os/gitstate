-- 20260624_006_wallet_and_model_usage
-- Adds two billing capabilities, both forward-only:
--
--   1. Per-model usage breakdown — usage_events gains a `model` column so the
--      managed-LLM events (kind='llm_tokens') can be grouped by the model that
--      produced them (effort_estimates.model). Nullable, default '' so existing
--      rows keep working and BYOK/non-LLM events stay blank.
--
--   2. Prepaid wallet / credit balance — a new append-only `wallet_ledger`
--      table. Every credit (top-up / adjustment / refund) and debit (managed-LLM
--      usage draw-down) is an immutable row carrying the running balance_after.
--      The current balance is the balance_after of the newest row (or 0). The
--      wallet is the "extra billing" balance: allowance is consumed first, then
--      the wallet, then any remainder lands on the monthly overage invoice.
--
-- wallet_ledger gets the same org_isolation RLS policy + FORCE RLS as the rest of
-- the billing tables (decisions A2/S1).

-- ── 1. Per-model usage ───────────────────────────────────────────────────────
ALTER TABLE usage_events
    ADD COLUMN IF NOT EXISTS model text NOT NULL DEFAULT '';

-- Group/sum per model+kind within a period (UsageByModel).
CREATE INDEX IF NOT EXISTS usage_events_org_id_model_idx
    ON usage_events (org_id, model);

-- ── 2. Wallet ledger ─────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS wallet_ledger (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- seq gives a total, monotonic order independent of created_at (which is the
    -- transaction-start `now()` and therefore identical for rows written in one tx).
    -- balance/listing/locking order by seq DESC so within-tx rows stay ordered.
    seq                 bigint GENERATED ALWAYS AS IDENTITY,
    org_id              uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    kind                text NOT NULL,            -- topup | usage | adjustment | refund
    amount_cents        bigint NOT NULL,          -- signed: + credit, − debit
    currency            text NOT NULL DEFAULT 'USD',
    balance_after_cents bigint NOT NULL,          -- running balance after this row
    description         text NOT NULL DEFAULT '',
    ref                 text NOT NULL DEFAULT '', -- paystack ref / usage_event id / etc.
    created_at          timestamptz NOT NULL DEFAULT now()
);

-- Newest-row-per-org lookup (balance) + chronological listing, ordered by seq.
CREATE INDEX IF NOT EXISTS wallet_ledger_org_id_seq_idx
    ON wallet_ledger (org_id, seq DESC);

-- Idempotency for Paystack top-ups: at most one topup row per (org, ref) when a
-- non-empty ref is supplied. Partial unique index so empty refs (manual
-- adjustments / usage draw-downs) are never constrained.
CREATE UNIQUE INDEX IF NOT EXISTS wallet_ledger_topup_ref_uniq
    ON wallet_ledger (org_id, ref)
    WHERE kind = 'topup' AND ref <> '';

ALTER TABLE wallet_ledger ENABLE ROW LEVEL SECURITY;
ALTER TABLE wallet_ledger FORCE ROW LEVEL SECURITY;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_policies
        WHERE schemaname = 'public' AND tablename = 'wallet_ledger' AND policyname = 'org_isolation'
    ) THEN
        CREATE POLICY org_isolation ON wallet_ledger
            USING (org_id = current_org())
            WITH CHECK (org_id = current_org());
    END IF;
END $$;
