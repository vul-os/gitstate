package chat

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/exo/gitstate/internal/db"
)

// tools_action.go — the ACTION tools. These NEVER mutate. Each returns an
// *Action describing a one-click, confirmable button that hits an existing
// authenticated API endpoint. The model can PROPOSE a sync / exclusion, but the
// actual write only happens when the human clicks and the UI calls Endpoint
// with the user's own session. Result JSON is a short machine-readable echo so
// the model can confirm what it proposed.

// actionResult is the compact JSON a tool returns alongside an *Action, so the
// model knows the proposal succeeded (a button was surfaced) without mutating.
func actionResult(a *Action) (json.RawMessage, error) {
	return jsonResult(map[string]any{
		"proposed": true,
		"action":   a,
		"note":     "A confirmable button was surfaced to the user. No change was made; it runs only if the user clicks.",
	})
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
