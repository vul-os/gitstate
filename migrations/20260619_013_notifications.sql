-- 20260619_013_notifications
-- Notification channels + digest config: evidence-based weekly status, stale/blocked PRs,
-- who's OOO — delivered to Slack/webhook/email. forward-only.

CREATE TABLE notification_channels (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    kind        text NOT NULL,                 -- slack | webhook | email
    target      text NOT NULL,                 -- webhook URL or email address
    label       text,
    enabled     boolean NOT NULL DEFAULT true,
    -- which digests this channel receives, e.g. {"weeklyStatus":true,"stalePRs":true,"ooo":true}
    digests     jsonb NOT NULL DEFAULT '{"weeklyStatus":true,"stalePRs":true,"ooo":true}',
    schedule    text NOT NULL DEFAULT 'weekly', -- weekly | daily
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON notification_channels (org_id);

CREATE TABLE notification_log (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    channel_id  uuid REFERENCES notification_channels(id) ON DELETE SET NULL,
    kind        text NOT NULL,                 -- which digest
    status      text NOT NULL,                 -- sent | failed | preview
    summary     text,
    sent_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON notification_log (org_id, sent_at);

DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['notification_channels','notification_log'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY;', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY;', t);
    EXECUTE format($p$CREATE POLICY org_isolation ON %I
        USING (org_id = current_org()) WITH CHECK (org_id = current_org());$p$, t);
  END LOOP;
END $$;
