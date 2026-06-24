package chat

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/exo/gitstate/internal/db"
)

// tools_action.go — the ACTION tools. These NEVER mutate. Each returns an
// *Action describing a one-click, confirmable button that hits an existing
// authenticated API endpoint. The model can PROPOSE an upgrade / sync / invoice
// / exclusion, but the actual write only happens when the human clicks and the
// UI calls Endpoint with the user's own session. Result JSON is a short
// machine-readable echo so the model can confirm what it proposed.

// actionResult is the compact JSON a tool returns alongside an *Action, so the
// model knows the proposal succeeded (a button was surfaced) without mutating.
func actionResult(a *Action) (json.RawMessage, error) {
	return jsonResult(map[string]any{
		"proposed": true,
		"action":   a,
		"note":     "A confirmable button was surfaced to the user. No change was made; it runs only if the user clicks.",
	})
}

// ── propose_plan_upgrade ────────────────────────────────────────────────────

func proposePlanUpgradeTool() Tool {
	return Tool{
		Name:        "propose_plan_upgrade",
		Description: "Propose upgrading the org's subscription plan. Returns a confirmable 'Upgrade' button that, when clicked, starts the Paystack checkout for the given plan. Does NOT charge or change the plan — the user must confirm. Use when the user asks to upgrade/downgrade/change plan.",
		JSONSchema: objectSchema(map[string]any{
			"planKey": stringProp("the target plan key, e.g. hobby | pro | team | scale | enterprise"),
		}, "planKey"),
		Handler: func(_ context.Context, _ *db.DB, _ string, args json.RawMessage) (json.RawMessage, *Action, error) {
			var a struct {
				PlanKey string `json:"planKey"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, nil, fmt.Errorf("invalid args: %w", err)
			}
			if a.PlanKey == "" {
				return nil, nil, fmt.Errorf("planKey is required")
			}
			action := &Action{
				Type:     "plan_upgrade",
				Label:    fmt.Sprintf("Upgrade to %s", titleCasePlan(a.PlanKey)),
				Endpoint: "/api/billing/checkout",
				Method:   "POST",
				Payload:  map[string]any{"plan": a.PlanKey},
				Confirm:  true,
			}
			out, _ := actionResult(action)
			return out, action, nil
		},
	}
}

// ── propose_sync_repo ───────────────────────────────────────────────────────

func proposeSyncRepoTool() Tool {
	return Tool{
		Name:        "propose_sync_repo",
		Description: "Propose re-syncing a repository so its git data is refreshed. Returns a confirmable 'Sync' button hitting the repo-sync endpoint. Does NOT start the sync — the user must confirm. Use repo ids from list_repos.",
		JSONSchema: objectSchema(map[string]any{
			"repoId": stringProp("the repository id (UUID) to sync — get it from list_repos"),
		}, "repoId"),
		Handler: func(_ context.Context, _ *db.DB, _ string, args json.RawMessage) (json.RawMessage, *Action, error) {
			var a struct {
				RepoID string `json:"repoId"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, nil, fmt.Errorf("invalid args: %w", err)
			}
			if a.RepoID == "" {
				return nil, nil, fmt.Errorf("repoId is required")
			}
			action := &Action{
				Type:     "sync_repo",
				Label:    "Sync repository",
				Endpoint: fmt.Sprintf("/api/repos/%s/sync", a.RepoID),
				Method:   "POST",
				Payload:  map[string]any{},
				Confirm:  true,
			}
			out, _ := actionResult(action)
			return out, action, nil
		},
	}
}

// ── propose_generate_invoice ────────────────────────────────────────────────

func proposeGenerateInvoiceTool() Tool {
	return Tool{
		Name:        "propose_generate_invoice",
		Description: "Propose generating a client invoice from merged-PR git activity over a date range. Returns a confirmable 'Generate invoice' button hitting the from-git invoice builder. Does NOT create the invoice — the user must confirm.",
		JSONSchema: objectSchema(map[string]any{
			"client": stringProp("client id (UUID), optional — leave empty for the default/unassigned client"),
			"from":   stringProp("period start, YYYY-MM-DD"),
			"to":     stringProp("period end, YYYY-MM-DD"),
		}, "from", "to"),
		Handler: func(_ context.Context, _ *db.DB, _ string, args json.RawMessage) (json.RawMessage, *Action, error) {
			var a struct {
				Client string `json:"client"`
				From   string `json:"from"`
				To     string `json:"to"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, nil, fmt.Errorf("invalid args: %w", err)
			}
			if a.From == "" || a.To == "" {
				return nil, nil, fmt.Errorf("from and to dates are required")
			}
			payload := map[string]any{"from": a.From, "to": a.To}
			if a.Client != "" {
				payload["clientId"] = a.Client
			}
			action := &Action{
				Type:     "generate_invoice",
				Label:    fmt.Sprintf("Generate invoice (%s → %s)", a.From, a.To),
				Endpoint: "/api/invoices/from-git",
				Method:   "POST",
				Payload:  payload,
				Confirm:  true,
			}
			out, _ := actionResult(action)
			return out, action, nil
		},
	}
}

// ── propose_exclude_contributor ─────────────────────────────────────────────

func proposeExcludeContributorTool() Tool {
	return Tool{
		Name:        "propose_exclude_contributor",
		Description: "Propose excluding a contributor (e.g. a bot or duplicate identity) from analytics and leaderboards. Returns a confirmable 'Exclude' button hitting the contributor-update endpoint with excluded=true. Does NOT exclude anyone — the user must confirm.",
		JSONSchema: objectSchema(map[string]any{
			"id": stringProp("the contributor id (UUID) to exclude"),
		}, "id"),
		Handler: func(_ context.Context, _ *db.DB, _ string, args json.RawMessage) (json.RawMessage, *Action, error) {
			var a struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, nil, fmt.Errorf("invalid args: %w", err)
			}
			if a.ID == "" {
				return nil, nil, fmt.Errorf("id is required")
			}
			action := &Action{
				Type:     "exclude_contributor",
				Label:    "Exclude contributor",
				Endpoint: fmt.Sprintf("/api/contributors/%s", a.ID),
				Method:   "PATCH",
				Payload:  map[string]any{"excluded": true},
				Confirm:  true,
			}
			out, _ := actionResult(action)
			return out, action, nil
		},
	}
}

// titleCasePlan upper-cases the first rune of a plan key for the button label.
func titleCasePlan(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]-32*btoi(s[0] >= 'a' && s[0] <= 'z')) + s[1:]
}

func btoi(b bool) byte {
	if b {
		return 1
	}
	return 0
}
