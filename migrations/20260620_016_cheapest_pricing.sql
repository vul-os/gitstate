-- 20260620_016_cheapest_pricing
-- Reprice so gitstate is the cheapest option at ANY team size.
-- Rationale: competitors price per SEAT and charge extra for AI. gitstate prices
-- per BUILDER (stakeholders free) with AI included. Setting Team to $6 managed
-- (AI included) beats the cheapest AI-inclusive competitor (Linear $8) even for a
-- pure-builder team; BYOK $3 (= $6 − $3 included-LLM) beats the cheapest no-AI
-- per-seat tool (GitHub Projects $3.67). With free stakeholders, gitstate wins at
-- every team shape. forward-only.

UPDATE plans SET per_builder_cents = 600,  included_llm_cents = 300  WHERE key = 'team';
UPDATE plans SET per_builder_cents = 1400, included_llm_cents = 600  WHERE key = 'business';

-- Managed LLM is presented to clients at the model's standard rate (no visible
-- markup, no per-seat AI tax). Our margin is the bulk/committed-use discount we
-- get on tokens (~35%), plus a silent ≤5% gateway buffer — hence overage_markup
-- drops from 1.30 to 1.05. The real profit is the wholesale spread, not a markup.
UPDATE plans SET overage_markup = 1.05 WHERE key IN ('team','business');
