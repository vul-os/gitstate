-- 20260624_003_billing_lifecycle
-- Subscription / dunning lifecycle columns + idempotency guards (forward-only).
--
-- Adds the state the billing Scheduler (internal/billing) drives under an
-- INJECTABLE clock: the dunning machine (active → past_due → suspended →
-- canceled), the auto-charge card flag, and the explicit current-period window.
--
-- Integrity closures backed here:
--   #5  FX locked at charge — fx_rate/fx_rate_id already on invoices (unchanged).
--   #7  Dunning — billing_status + dunning_attempts + next_retry_at + suspended_at/canceled_at.
--   #8  Idempotency — ONE invoice per (org, period): a partial unique index keyed on
--       (org_id, period_start, period_end) for non-void invoices.
--   #2/#3 Card-gated managed-LLM / overage — payment_method_on_file bool.
--
-- RLS: subscriptions already has org_isolation + FORCE ROW LEVEL SECURITY from the
-- baseline; the new columns inherit it. We re-assert FORCE RLS defensively (idempotent).

ALTER TABLE subscriptions
  ADD COLUMN IF NOT EXISTS billing_status          text NOT NULL DEFAULT 'active',  -- active | past_due | suspended | canceled
  ADD COLUMN IF NOT EXISTS dunning_attempts        int  NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS next_retry_at           timestamptz,
  ADD COLUMN IF NOT EXISTS suspended_at            timestamptz,
  ADD COLUMN IF NOT EXISTS canceled_at             timestamptz,
  ADD COLUMN IF NOT EXISTS payment_method_on_file  boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS current_period_start    timestamptz;

-- Backfill billing_status from the legacy status column so existing rows are coherent.
UPDATE subscriptions SET billing_status = 'canceled'  WHERE status = 'canceled'  AND billing_status = 'active';
UPDATE subscriptions SET billing_status = 'past_due'  WHERE status = 'past_due'  AND billing_status = 'active';

-- Re-assert RLS on subscriptions (idempotent; baseline already enabled+forced it).
ALTER TABLE subscriptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE subscriptions FORCE  ROW LEVEL SECURITY;

-- #8 idempotency: at most ONE non-void invoice per (org, billing period).
-- Re-running RunBillingCycle for an org whose period already produced an invoice
-- is a no-op (the store guard checks this index first; the index is the backstop).
CREATE UNIQUE INDEX IF NOT EXISTS invoices_org_period_uniq
  ON invoices (org_id, period_start, period_end)
  WHERE status <> 'void';

-- Helpful lookup for the scheduler's "whose period ended" and "whose retry is due" scans.
CREATE INDEX IF NOT EXISTS subscriptions_next_retry_idx ON subscriptions (next_retry_at)
  WHERE next_retry_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS subscriptions_period_end_idx ON subscriptions (current_period_end)
  WHERE current_period_end IS NOT NULL;
