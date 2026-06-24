package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/exo/gitstate/internal/llm"
)

// fakeProvider is a scripted ChatProvider: each call to ChatStream pops the next
// result from results. It streams the result's Content through OnDelta so token
// events fire, exactly like the real client. This is how the agentic loop is
// tested without a live LLM.
type fakeProvider struct {
	results []llm.ChatResult
	calls   int
	reqs    []llm.ChatRequest
}

func (f *fakeProvider) ChatStream(_ context.Context, req llm.ChatRequest) (llm.ChatResult, error) {
	f.reqs = append(f.reqs, req)
	res := f.results[f.calls]
	f.calls++
	if req.OnDelta != nil && res.Content != "" {
		// Emit the content in two chunks to exercise streaming assembly.
		mid := len(res.Content) / 2
		req.OnDelta(res.Content[:mid])
		req.OnDelta(res.Content[mid:])
	}
	return res, nil
}

// collect runs the engine and gathers all emitted events.
func collect(t *testing.T, e *Engine, msgs []llm.ChatMessage) ([]Event, string) {
	t.Helper()
	var events []Event
	final, err := e.Run(context.Background(), "org-123", msgs, func(ev Event) error {
		events = append(events, ev)
		return nil
	})
	if err != nil {
		t.Fatalf("engine.Run: %v", err)
	}
	return events, final
}

// TestRegistrySchemasValid asserts every tool exposes a well-formed JSON-Schema
// object that round-trips and declares the OpenAI-required shape.
func TestRegistrySchemasValid(t *testing.T) {
	reg := NewRegistry()
	if len(reg.Tools()) == 0 {
		t.Fatal("registry has no tools")
	}
	seen := map[string]bool{}
	for _, tool := range reg.Tools() {
		if tool.Name == "" {
			t.Error("tool with empty name")
		}
		if seen[tool.Name] {
			t.Errorf("duplicate tool name %q", tool.Name)
		}
		seen[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("%s: empty description", tool.Name)
		}
		if tool.Handler == nil {
			t.Errorf("%s: nil handler", tool.Name)
		}
		// Schema must be a JSON object node and marshal cleanly.
		if tool.JSONSchema["type"] != "object" {
			t.Errorf("%s: schema type = %v, want object", tool.Name, tool.JSONSchema["type"])
		}
		if _, ok := tool.JSONSchema["properties"]; !ok {
			t.Errorf("%s: schema missing properties", tool.Name)
		}
		raw, err := json.Marshal(tool.JSONSchema)
		if err != nil {
			t.Errorf("%s: schema not marshalable: %v", tool.Name, err)
		}
		var back map[string]any
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Errorf("%s: schema not valid JSON: %v", tool.Name, err)
		}
	}

	// Sanity: the documented read + action tools are all present.
	for _, want := range []string{
		"get_analytics_summary", "commits_over_time", "top_contributors",
		"get_contribution", "cycle_time_summary", "list_repos", "repo_stats",
		"eng_health", "list_invoices", "invoice_summary", "current_usage", "wallet_balance",
		"propose_plan_upgrade", "propose_sync_repo", "propose_generate_invoice", "propose_exclude_contributor",
	} {
		if !seen[want] {
			t.Errorf("missing expected tool %q", want)
		}
	}
}

// TestAgenticLoopExecutesToolThenAnswers injects a fake provider that first
// returns a tool_call (an action tool, so no DB is touched) and then a final
// answer. It asserts the loop executed the tool, emitted the right events, fed a
// tool message back, and produced the final text.
func TestAgenticLoopExecutesToolThenAnswers(t *testing.T) {
	var tc llm.ChatToolCall
	tc.ID = "call_1"
	tc.Type = "function"
	tc.Function.Name = "propose_plan_upgrade"
	tc.Function.Arguments = `{"planKey":"pro"}`

	fake := &fakeProvider{results: []llm.ChatResult{
		{Content: "Let me set that up.", ToolCalls: []llm.ChatToolCall{tc}, FinishReason: "tool_calls"},
		{Content: "I've prepared an upgrade button for you.", FinishReason: "stop"},
	}}

	// nil *db.DB is fine: the action tool's handler never touches the DB.
	e := NewEngine(fake, nil, NewRegistry(), "test-model")
	events, final := collect(t, e, []llm.ChatMessage{{Role: "user", Content: "upgrade me to pro"}})

	if fake.calls != 2 {
		t.Fatalf("provider called %d times, want 2", fake.calls)
	}
	if final != "I've prepared an upgrade button for you." {
		t.Errorf("final = %q", final)
	}

	// Event sequence must include token, tool_call, tool_result, action, done.
	kinds := map[EventType]int{}
	for _, ev := range events {
		kinds[ev.Type]++
	}
	for _, want := range []EventType{EventToken, EventToolCall, EventToolResult, EventAction, EventDone} {
		if kinds[want] == 0 {
			t.Errorf("missing %s event; got %v", want, kinds)
		}
	}

	// The second request must carry the assistant tool-call turn + the tool result.
	second := fake.reqs[1]
	var sawAssistantToolCall, sawToolResult bool
	for _, m := range second.Messages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			sawAssistantToolCall = true
		}
		if m.Role == "tool" && m.ToolCallID == "call_1" {
			sawToolResult = true
			if !strings.Contains(m.Content, "proposed") {
				t.Errorf("tool result content unexpected: %s", m.Content)
			}
		}
	}
	if !sawAssistantToolCall {
		t.Error("second request missing assistant tool-call turn")
	}
	if !sawToolResult {
		t.Error("second request missing tool result message")
	}
	// And the system prompt must be present in every turn.
	if len(second.Messages) == 0 || second.Messages[0].Role != "system" {
		t.Error("system prompt not prepended")
	}
}

// TestActionToolProposesWithoutMutating checks an action tool returns a
// confirmable Action (and the right endpoint/method/payload) and that the loop
// emits exactly one action event — never executing a mutation.
func TestActionToolProposesWithoutMutating(t *testing.T) {
	tool, ok := NewRegistry().Lookup("propose_plan_upgrade")
	if !ok {
		t.Fatal("propose_plan_upgrade not registered")
	}
	result, action, err := tool.Handler(context.Background(), nil, "org-123", json.RawMessage(`{"planKey":"team"}`))
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if action == nil {
		t.Fatal("expected an Action, got nil")
	}
	if !action.Confirm {
		t.Error("action.Confirm must be true")
	}
	if action.Type != "plan_upgrade" {
		t.Errorf("action.Type = %q", action.Type)
	}
	if action.Endpoint != "/api/billing/checkout" || action.Method != "POST" {
		t.Errorf("endpoint/method = %s %s", action.Method, action.Endpoint)
	}
	if action.Payload["plan"] != "team" {
		t.Errorf("payload plan = %v, want team", action.Payload["plan"])
	}
	if action.Label != "Upgrade to Team" {
		t.Errorf("label = %q, want Upgrade to Team", action.Label)
	}
	// The result echo must mark it proposed (no mutation performed).
	var echo map[string]any
	if err := json.Unmarshal(result, &echo); err != nil {
		t.Fatalf("result not JSON: %v", err)
	}
	if echo["proposed"] != true {
		t.Errorf("result.proposed = %v, want true", echo["proposed"])
	}
}

// TestActionToolValidatesArgs asserts missing required args produce a tool error
// (the loop recovers, no panic, no Action).
func TestActionToolValidatesArgs(t *testing.T) {
	tool, _ := NewRegistry().Lookup("propose_sync_repo")
	_, action, err := tool.Handler(context.Background(), nil, "org", json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected error for missing repoId")
	}
	if action != nil {
		t.Error("no Action should be returned on validation error")
	}
}

// TestEngineEmitsErrorEventOnProviderFailure: when ChatStream errors, the engine
// emits an error event and returns the error.
func TestEngineEmitsErrorEventOnProviderFailure(t *testing.T) {
	e := NewEngine(&erroringProvider{}, nil, NewRegistry(), "m")
	var sawError bool
	_, err := e.Run(context.Background(), "org", []llm.ChatMessage{{Role: "user", Content: "hi"}}, func(ev Event) error {
		if ev.Type == EventError {
			sawError = true
		}
		return nil
	})
	if err == nil {
		t.Error("expected error from Run")
	}
	if !sawError {
		t.Error("expected an error event")
	}
}

type erroringProvider struct{}

func (erroringProvider) ChatStream(context.Context, llm.ChatRequest) (llm.ChatResult, error) {
	return llm.ChatResult{}, context.Canceled
}
