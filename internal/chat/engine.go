package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/llm"
)

// engine.go — the agentic loop. The engine drives a ChatProvider (the llm
// gateway client) in a tool-calling loop: it streams assistant tokens, executes
// any tool calls the model emits (org-scoped, via the Registry), feeds the tool
// results back as tool messages, and loops until the model produces a final
// answer or the iteration cap is hit. Every step is surfaced to the caller as a
// typed Event so the HTTP layer can serialise it as SSE.

// maxIterations caps the tool-calling loop so a misbehaving model can't spin.
const maxIterations = 6

// defaultMaxTokens bounds each model turn's output.
const defaultMaxTokens = 1500

// ChatProvider is the subset of the llm client the engine needs: a single
// ChatStream call. The real implementation is *llm.openAIClient via
// llm.Service; tests inject a fake that returns scripted results. Keeping it an
// interface is what makes the agentic loop testable without a live LLM.
type ChatProvider interface {
	ChatStream(ctx context.Context, req llm.ChatRequest) (llm.ChatResult, error)
}

// EventType enumerates the SSE event kinds the engine emits. The string values
// are the SSE "event:" names the UI subscribes to.
type EventType string

const (
	// EventToken is an incremental assistant text delta. Data: {"text": "..."}.
	EventToken EventType = "token"
	// EventToolCall marks a tool invocation starting. Data: {"id","name","args"}.
	EventToolCall EventType = "tool_call"
	// EventToolResult is a compact tool result/summary. Data: {"id","name","result"} or {"id","name","error"}.
	EventToolResult EventType = "tool_result"
	// EventAction is a proposed confirmable action (a button). Data: the Action.
	EventAction EventType = "action"
	// EventDone is the terminal success event. Data: {"content": "<final text>"}.
	EventDone EventType = "done"
	// EventError is a terminal error event. Data: {"error": "..."}.
	EventError EventType = "error"
)

// Event is one item in the chat stream. Data is an already-JSON-encodable value
// matching the EventType's documented shape (see the EventType consts).
type Event struct {
	Type EventType `json:"type"`
	Data any       `json:"data"`
}

// Emit is the sink the engine writes events to. The HTTP handler passes an
// implementation that serialises each Event as an SSE frame and flushes; tests
// pass a collector. Returning an error (e.g. client disconnected) aborts the run.
type Emit func(Event) error

// ── event payloads (documented SSE data shapes) ─────────────────────────────

type tokenData struct {
	Text string `json:"text"`
}
type toolCallData struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}
type toolResultData struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}
type doneData struct {
	Content string `json:"content"`
}
type errorData struct {
	Error string `json:"error"`
}

// Engine runs the agentic chat loop.
type Engine struct {
	provider ChatProvider
	registry *Registry
	model    string
	db       *db.DB
}

// NewEngine builds an Engine. model is the model id to request (the request's
// model, or a default when empty — the caller resolves that). registry defaults
// to NewRegistry() when nil.
func NewEngine(provider ChatProvider, database *db.DB, registry *Registry, model string) *Engine {
	if registry == nil {
		registry = NewRegistry()
	}
	return &Engine{provider: provider, registry: registry, model: model, db: database}
}

// Run executes the agentic loop for one user turn. messages is the prior
// conversation (already including the new user message). orgID scopes every
// tool's data access. It emits token/tool_call/tool_result/action events as it
// goes, then a terminal done (or error) event. It returns the final assistant
// text. A non-nil error is also emitted as an error event before returning.
func (e *Engine) Run(ctx context.Context, orgID string, messages []llm.ChatMessage, emit Emit) (string, error) {
	// Prepend the system prompt (the model's contract: gitstate + tools + the
	// "propose, never execute" rule). Callers pass user/assistant turns only.
	convo := make([]llm.ChatMessage, 0, len(messages)+1)
	convo = append(convo, llm.ChatMessage{Role: "system", Content: SystemPrompt})
	convo = append(convo, messages...)

	tools := e.toolDefs()

	var finalText string
	for iter := 0; iter < maxIterations; iter++ {
		// Stream this turn; tokens are forwarded live via OnDelta.
		var emitErr error
		res, err := e.provider.ChatStream(ctx, llm.ChatRequest{
			Model:     e.model,
			Messages:  convo,
			Tools:     tools,
			MaxTokens: defaultMaxTokens,
			Stream:    true,
			OnDelta: func(tok string) {
				if emitErr != nil {
					return
				}
				if tok != "" {
					emitErr = emit(Event{Type: EventToken, Data: tokenData{Text: tok}})
				}
			},
		})
		if emitErr != nil {
			return "", emitErr
		}
		if err != nil {
			_ = emit(Event{Type: EventError, Data: errorData{Error: err.Error()}})
			return "", err
		}

		// No tool calls → this is the final assistant answer.
		if len(res.ToolCalls) == 0 {
			finalText = res.Content
			break
		}

		// Record the assistant's tool-call turn, then execute each tool and
		// append its result as a tool message for the next iteration.
		convo = append(convo, llm.ChatMessage{
			Role:      "assistant",
			Content:   res.Content,
			ToolCalls: res.ToolCalls,
		})

		for _, tc := range res.ToolCalls {
			toolMsg, err := e.runToolCall(ctx, orgID, tc, emit)
			if err != nil {
				return "", err
			}
			convo = append(convo, toolMsg)
		}

		// If the model also produced prose alongside tool calls, keep it as the
		// running answer in case the loop ends without a clean final turn.
		if res.Content != "" {
			finalText = res.Content
		}
	}

	if err := emit(Event{Type: EventDone, Data: doneData{Content: finalText}}); err != nil {
		return finalText, err
	}
	return finalText, nil
}

// runToolCall emits the tool_call event, executes the tool (org-scoped), emits
// tool_result and any action event, and returns the tool message to feed back.
func (e *Engine) runToolCall(ctx context.Context, orgID string, tc llm.ChatToolCall, emit Emit) (llm.ChatMessage, error) {
	name := tc.Function.Name
	rawArgs := json.RawMessage(strings.TrimSpace(tc.Function.Arguments))
	if len(rawArgs) == 0 {
		rawArgs = json.RawMessage("{}")
	}

	if err := emit(Event{Type: EventToolCall, Data: toolCallData{ID: tc.ID, Name: name, Args: rawArgs}}); err != nil {
		return llm.ChatMessage{}, err
	}

	tool, ok := e.registry.Lookup(name)
	if !ok {
		msg := fmt.Sprintf("unknown tool %q", name)
		if err := emit(Event{Type: EventToolResult, Data: toolResultData{ID: tc.ID, Name: name, Error: msg}}); err != nil {
			return llm.ChatMessage{}, err
		}
		return toolErrorMessage(tc.ID, name, msg), nil
	}

	result, action, err := tool.Handler(ctx, e.db, orgID, rawArgs)
	if err != nil {
		if eerr := emit(Event{Type: EventToolResult, Data: toolResultData{ID: tc.ID, Name: name, Error: err.Error()}}); eerr != nil {
			return llm.ChatMessage{}, eerr
		}
		return toolErrorMessage(tc.ID, name, err.Error()), nil
	}

	if err := emit(Event{Type: EventToolResult, Data: toolResultData{ID: tc.ID, Name: name, Result: result}}); err != nil {
		return llm.ChatMessage{}, err
	}
	// An action tool also surfaces a confirmable button to the UI.
	if action != nil {
		if err := emit(Event{Type: EventAction, Data: action}); err != nil {
			return llm.ChatMessage{}, err
		}
	}

	return llm.ChatMessage{
		Role:       "tool",
		ToolCallID: tc.ID,
		Name:       name,
		Content:    string(result),
	}, nil
}

// toolErrorMessage builds a tool message conveying a tool failure back to the
// model so it can recover (apologise, try another tool) rather than stalling.
func toolErrorMessage(id, name, msg string) llm.ChatMessage {
	body, _ := json.Marshal(map[string]string{"error": msg})
	return llm.ChatMessage{Role: "tool", ToolCallID: id, Name: name, Content: string(body)}
}

// toolDefs maps the registry into the llm.ChatTool function-calling array.
func (e *Engine) toolDefs() []llm.ChatTool {
	defs := make([]llm.ChatTool, 0, len(e.registry.Tools()))
	for _, t := range e.registry.Tools() {
		defs = append(defs, llm.ChatTool{
			Type: "function",
			Function: llm.ChatToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.JSONSchema,
			},
		})
	}
	return defs
}
