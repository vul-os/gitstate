// Package api — chat.go
// The agentic chat endpoint: POST /api/chat streams a Claude-Code-style
// assistant over Server-Sent Events. The model can call read tools (analytics,
// contribution, eng-health) and PROPOSE confirmable actions (repo sync,
// contributor exclusion) — it never mutates state itself. Everything routes
// through the in-process llmux gateway (the same one GET /api/models uses),
// so it honours LLM_GATEWAY=llmux.
//
// # SSE event contract (for the UI)
//
// Each event is one SSE frame:
//
//	event: <type>
//	data:  <json>
//
// Types and their data shapes:
//   - token        {"text": "..."}                              incremental assistant text
//   - tool_call    {"id","name","args":{...}}                   a tool invocation started
//   - tool_result  {"id","name","result":{...}}  OR  {...,"error":"..."}
//   - action       {"type","label","endpoint","method","payload":{...},"confirm":true}
//     a confirmable button to render
//   - done         {"content":"<final assistant text>"}         terminal success
//   - error        {"error":"..."}                              terminal failure
//
// Destructive operations are ALWAYS surfaced as `action` events (buttons the
// user clicks); the model cannot execute them.
package api

import (
	"encoding/json"
	"net/http"

	"github.com/exo/gitstate/internal/chat"
	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/llm"
	"github.com/exo/gitstate/internal/middleware"
)

// RegisterChatRoutes wires POST /api/chat behind RequireAuth + OrgScope. It is
// folded into RegisterReportRoutes (an existing registrar) so no router.go edit
// is needed — see that function. The chat engine sources its LLM client from the
// package-level modelGateway injected by main.go's SetModelGateway (called
// before NewRouter), the same gateway GET /api/models uses.
func RegisterChatRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	h := &chatHandlers{db: database, cfg: cfg}

	requireAuth := middleware.RequireAuth(cfg.Auth.JWTSigningKey)
	orgScope := middleware.OrgScope(database.Pool())

	mux.Handle("POST /api/chat", requireAuth(orgScope(http.HandlerFunc(h.chat))))
}

type chatHandlers struct {
	db  *db.DB
	cfg *config.Config
}

// chatRequest is the POST /api/chat body. messages is the prior conversation
// (user/assistant turns); model is a model id from GET /api/models (optional —
// falls back to the gateway default when empty).
type chatRequest struct {
	Messages []chatMessageIn `json:"messages"`
	Model    string          `json:"model"`
}

type chatMessageIn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (h *chatHandlers) chat(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.OrgFromContext(r.Context())
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "org context required")
		return
	}

	var req chatRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages is required")
		return
	}

	// Resolve the LLM client from the running gateway. When the gateway is off,
	// chat is unavailable — report it cleanly (mirrors the report endpoint).
	client, ok := llm.NewChatClient(modelGateway, req.Model)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "chat is unavailable: LLM gateway not configured (set LLM_GATEWAY=llmux)")
		return
	}

	// Map the wire messages onto the llm message array (user/assistant only;
	// the engine prepends the system prompt and manages tool/assistant turns).
	msgs := make([]llm.ChatMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		role := m.Role
		if role != "user" && role != "assistant" {
			role = "user" // be lenient with unexpected roles
		}
		msgs = append(msgs, llm.ChatMessage{Role: role, Content: m.Content})
	}

	// SSE headers + flusher.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering (nginx)
	flusher, canFlush := w.(http.Flusher)
	w.WriteHeader(http.StatusOK)

	emit := sseEmitter(w, flusher, canFlush)

	engine := chat.NewEngine(client, h.db, chat.NewRegistry(), req.Model)
	if _, err := engine.Run(r.Context(), orgID, msgs, emit); err != nil {
		// The engine already emitted an `error` event for model/tool failures;
		// emit-failures (client gone) need nothing further. Nothing else to do.
		return
	}
}

// sseEmitter returns a chat.Emit that serialises each Event as an SSE frame
// (event: <type>\n data: <json>\n\n) and flushes so tokens stream live.
func sseEmitter(w http.ResponseWriter, flusher http.Flusher, canFlush bool) chat.Emit {
	return func(ev chat.Event) error {
		if err := writeSSE(w, string(ev.Type), ev.Data); err != nil {
			return err
		}
		if canFlush {
			flusher.Flush()
		}
		return nil
	}
}

// writeSSE writes one SSE frame: an event line plus a single-line JSON data
// line. Exported-ish helper kept here so chat tests can round-trip the encoding.
func writeSSE(w http.ResponseWriter, event string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte("event: " + event + "\n")); err != nil {
		return err
	}
	if _, err := w.Write([]byte("data: ")); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	_, err = w.Write([]byte("\n\n"))
	return err
}
