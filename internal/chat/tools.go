// Package chat implements gitstate's agentic chat engine: a Claude-Code-style
// assistant that streams tokens, calls TOOLS to read gitstate data, and PROPOSES
// confirmable actions (buttons the UI renders) for any destructive mutation. It
// never mutates state itself — the model can only read, and any write is
// surfaced as an *Action the human must click.
//
// The engine (engine.go) drives an llm.openAIClient (through the in-process
// llmux gateway) in a tool-calling loop. tools.go defines the Tool contract and
// the concrete read + action tools, each reusing the existing analytics /
// contribution / store services so no query logic is reinvented.
//
// # Security model
//
//   - Every tool Handler receives the authenticated orgID and runs its reads via
//     db.WithOrg, so Postgres RLS scopes the data to the caller's org.
//   - READ tools return compact JSON the model reasons over.
//   - ACTION tools NEVER mutate. They return an *Action describing a one-click
//     button (Type/Label/Endpoint/Method/Payload/Confirm). The real mutation only
//     happens when the USER clicks and the UI calls the named endpoint with its
//     own auth — the model cannot trigger a sync or exclusion directly.
package chat

import (
	"context"
	"encoding/json"

	"github.com/exo/gitstate/internal/db"
)

// Action is a proposed, confirmable mutation the chat surfaces to the user as a
// one-click button. The chat engine emits it as an "action" SSE event; the UI
// renders Label as a button and, on click, issues Method Endpoint with Payload
// using the user's own session — so the model proposes, the human confirms, and
// the mutation runs through the normal authorize-on-the-endpoint path. The chat
// engine itself performs no writes.
type Action struct {
	// Type is a stable machine key for the action family (e.g. "sync_repo",
	// "exclude_contributor"). The UI can switch on it to pick an icon/confirmation
	// copy.
	Type string `json:"type"`
	// Label is the human button text, e.g. "Upgrade to Pro".
	Label string `json:"label"`
	// Endpoint is the existing API path the UI POSTs/PATCHes when the user clicks.
	Endpoint string `json:"endpoint"`
	// Method is the HTTP method for Endpoint (POST | PATCH | DELETE).
	Method string `json:"method"`
	// Payload is the JSON request body the UI sends to Endpoint.
	Payload map[string]any `json:"payload,omitempty"`
	// Confirm is always true: actions are confirmable, never auto-executed.
	Confirm bool `json:"confirm"`
}

// Tool is one function-calling tool the model may invoke. JSONSchema is the
// OpenAI/JSON-Schema "parameters" object describing Args. Handler runs the tool:
// reads are org-scoped via db.WithOrg; it returns a compact JSON result for the
// model, and/or an *Action to propose to the user. A non-nil Action with a nil
// error means "no mutation happened — here is a button the user may click".
type Tool struct {
	Name        string
	Description string
	JSONSchema  map[string]any
	Handler     func(ctx context.Context, database *db.DB, orgID string, args json.RawMessage) (resultJSON json.RawMessage, action *Action, err error)
}

// Registry is the ordered set of tools exposed to the model. Order is stable so
// the system prompt and the OpenAI tools array are deterministic.
type Registry struct {
	tools  []Tool
	byName map[string]Tool
}

// NewRegistry builds the default gitstate tool registry: the read tools
// (analytics, contribution, eng-health, cycle-time, repos) plus the action
// tools (repo sync, exclude contributor). All handlers are org-scoped and
// side-effect-free except that action tools return an *Action (still no
// mutation).
func NewRegistry() *Registry {
	tools := []Tool{
		// ── read tools ──────────────────────────────────────────────────────
		getAnalyticsSummaryTool(),
		commitsOverTimeTool(),
		topContributorsTool(),
		getContributionTool(),
		cycleTimeSummaryTool(),
		listReposTool(),
		repoStatsTool(),
		engHealthTool(),
		// ── action tools (PROPOSE only — never mutate) ──────────────────────
		proposeSyncRepoTool(),
		proposeExcludeContributorTool(),
	}
	r := &Registry{tools: tools, byName: make(map[string]Tool, len(tools))}
	for _, t := range tools {
		r.byName[t.Name] = t
	}
	return r
}

// Tools returns the registry's tools in stable order.
func (r *Registry) Tools() []Tool { return r.tools }

// Lookup returns the tool with name and whether it exists.
func (r *Registry) Lookup(name string) (Tool, bool) {
	t, ok := r.byName[name]
	return t, ok
}

// ── small schema helpers ────────────────────────────────────────────────────

// objectSchema builds a JSON-Schema object node. props maps property name →
// its schema; required lists required property names.
func objectSchema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		s["required"] = required
	} else {
		// An empty object schema still needs "properties" present; required omitted.
		s["required"] = []string{}
	}
	return s
}

func stringProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
func intProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

// jsonResult marshals v into a json.RawMessage for a tool result. It never
// fails for the value types we use; on the off chance marshalling errors it
// returns a small JSON error object so the loop can continue.
func jsonResult(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{"error":"failed to encode tool result"}`), nil
	}
	return json.RawMessage(b), nil
}
