-- 20260619_009_contribution
-- "Dev contribution to outcomes" — configurable, evidence-backed, gaming-resistant.
-- v1 stores org-level dimension WEIGHTS (the profile itself is computed on demand from
-- commits/PRs/issues/involvement/effort_estimates/cycle_times). Blame-survival + SZZ
-- plug into the same model once real-repo sync exists.
-- forward-only.

CREATE TABLE contribution_weights (
    org_id     uuid PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    -- relative weights per dimension (any non-negative scale; normalized at read time).
    shipped    numeric NOT NULL DEFAULT 30,  -- merged PRs / issues closed / features shipped
    review     numeric NOT NULL DEFAULT 20,  -- reviews + collaboration (the invisible senior work)
    effort     numeric NOT NULL DEFAULT 20,  -- LLM-estimated difficulty (NOT lines of code)
    quality    numeric NOT NULL DEFAULT 15,  -- low revert/hotfix rate, healthy cycle time
    ownership  numeric NOT NULL DEFAULT 15,  -- areas owned / knowledge spread (bus-factor)
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE contribution_weights ENABLE ROW LEVEL SECURITY;
ALTER TABLE contribution_weights FORCE ROW LEVEL SECURITY;
CREATE POLICY org_isolation ON contribution_weights
    USING (org_id = current_org())
    WITH CHECK (org_id = current_org());
