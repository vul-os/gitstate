package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ─── OpenAI Chat-Completions client ────────────────────────────────────────
//
// openAIClient talks the OpenAI /chat/completions wire format against any
// OpenAI-compatible base URL — in gitstate's case the in-process llmux gateway
// (Gateway.BaseURL(), …/v1). It implements the existing Provider interface
// (Complete) so service.New can swap it in for the Anthropic-direct client,
// and additionally exposes ChatStream — a richer messages-array + tools +
// streaming surface the chat engine (a later agent) will drive.
//
// Stdlib net/http only; no SDK dependency.

// openAIClient is an OpenAI Chat-Completions provider.
type openAIClient struct {
	baseURL string // …/v1 (no trailing slash)
	apiKey  string // bearer token; may be empty for the gateway (keys live server-side)
	model   string
	httpCli *http.Client
}

// newOpenAIClient constructs a client against an OpenAI-compatible base URL.
// baseURL is expected to be the gateway's …/v1 URL; any trailing slash is
// trimmed so request paths join cleanly.
func newOpenAIClient(baseURL, apiKey, model string) *openAIClient {
	return &openAIClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		httpCli: &http.Client{Timeout: defaultHTTPTimeout},
	}
}

// ─── Wire types ────────────────────────────────────────────────────────────

// ChatMessage is one entry in the messages array (role + content). Optional
// tool-calling fields support assistant tool-call turns and tool result turns.
type ChatMessage struct {
	Role       string         `json:"role"` // "system" | "user" | "assistant" | "tool"
	Content    string         `json:"content,omitempty"`
	Name       string         `json:"name,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"` // for role:"tool"
	ToolCalls  []ChatToolCall `json:"tool_calls,omitempty"`   // for role:"assistant"
}

// ChatTool is a function-calling tool definition (OpenAI "tools" shape).
type ChatTool struct {
	Type     string           `json:"type"` // always "function"
	Function ChatToolFunction `json:"function"`
}

// ChatToolFunction is the function schema inside a ChatTool.
type ChatToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"` // JSON Schema
}

// ChatToolCall is a tool invocation the model emitted.
type ChatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON-encoded args (string)
	} `json:"function"`
}

// ChatRequest is the input to ChatStream. When Stream is true the response is
// consumed as SSE and Stream callbacks fire; otherwise a single response is
// returned. Tools enables function calling.
type ChatRequest struct {
	Model     string        `json:"model,omitempty"` // defaults to the client's model
	Messages  []ChatMessage `json:"messages"`
	Tools     []ChatTool    `json:"tools,omitempty"`
	MaxTokens int           `json:"max_tokens,omitempty"`
	Stream    bool          `json:"stream,omitempty"`
	// OnDelta, when set on a streaming request, receives each incremental
	// content token as it arrives over SSE.
	OnDelta func(token string) `json:"-"`
}

// ChatResult is the assembled result of a ChatStream call: the full assistant
// text, any tool calls, and the finish reason.
type ChatResult struct {
	Content      string
	ToolCalls    []ChatToolCall
	FinishReason string
}

// chatCompletionRequest is the JSON body actually sent to /chat/completions.
type chatCompletionRequest struct {
	Model     string        `json:"model"`
	Messages  []ChatMessage `json:"messages"`
	Tools     []ChatTool    `json:"tools,omitempty"`
	MaxTokens int           `json:"max_tokens,omitempty"`
	Stream    bool          `json:"stream,omitempty"`
}

// chatCompletionResponse is the non-streaming response shape.
type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content   string         `json:"content"`
			ToolCalls []ChatToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// chatCompletionChunk is one SSE "data:" frame of a streamed response.
type chatCompletionChunk struct {
	Choices []struct {
		Delta struct {
			Content   string         `json:"content"`
			ToolCalls []ChatToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// ─── Provider: Complete ────────────────────────────────────────────────────

// Complete implements Provider. It maps the (system, user) pair onto a two-turn
// messages array and POSTs a non-streaming /chat/completions request, returning
// the assistant text of the first choice.
func (c *openAIClient) Complete(ctx context.Context, system, user string) (string, error) {
	msgs := make([]ChatMessage, 0, 2)
	if system != "" {
		msgs = append(msgs, ChatMessage{Role: "system", Content: system})
	}
	msgs = append(msgs, ChatMessage{Role: "user", Content: user})

	res, err := c.ChatStream(ctx, ChatRequest{
		Messages:  msgs,
		MaxTokens: maxOutputTokens,
	})
	if err != nil {
		return "", err
	}
	return res.Content, nil
}

// ─── ChatStream: messages + tools + streaming ──────────────────────────────

// ChatStream POSTs req to {baseURL}/chat/completions. When req.Stream is true
// it consumes the SSE response, invoking req.OnDelta for each content token and
// assembling the full ChatResult; otherwise it parses a single JSON response.
func (c *openAIClient) ChatStream(ctx context.Context, req ChatRequest) (ChatResult, error) {
	model := req.Model
	if model == "" {
		model = c.model
	}
	body := chatCompletionRequest{
		Model:     model,
		Messages:  req.Messages,
		Tools:     req.Tools,
		MaxTokens: req.MaxTokens,
		Stream:    req.Stream,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return ChatResult{}, fmt.Errorf("llm: marshal chat request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return ChatResult{}, fmt.Errorf("llm: build chat request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	if req.Stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}

	resp, err := c.httpCli.Do(httpReq)
	if err != nil {
		return ChatResult{}, fmt.Errorf("llm: chat http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return ChatResult{}, fmt.Errorf("llm: openai API error: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	if req.Stream {
		return c.readStream(resp.Body, req.OnDelta)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return ChatResult{}, fmt.Errorf("llm: read chat response: %w", err)
	}
	var parsed chatCompletionResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return ChatResult{}, fmt.Errorf("llm: parse chat response: %w", err)
	}
	if parsed.Error != nil {
		return ChatResult{}, fmt.Errorf("llm: openai API error: %s: %s", parsed.Error.Type, parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return ChatResult{}, fmt.Errorf("llm: openai response had no choices")
	}
	ch := parsed.Choices[0]
	return ChatResult{
		Content:      ch.Message.Content,
		ToolCalls:    ch.Message.ToolCalls,
		FinishReason: ch.FinishReason,
	}, nil
}

// readStream consumes an SSE /chat/completions stream, calling onDelta for each
// content token and assembling the final ChatResult.
func (c *openAIClient) readStream(body io.Reader, onDelta func(string)) (ChatResult, error) {
	var (
		sb        strings.Builder
		toolCalls []ChatToolCall
		finish    string
	)
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk chatCompletionChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue // skip malformed frames (e.g. comments/heartbeats)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		ch := chunk.Choices[0]
		if ch.Delta.Content != "" {
			sb.WriteString(ch.Delta.Content)
			if onDelta != nil {
				onDelta(ch.Delta.Content)
			}
		}
		toolCalls = append(toolCalls, ch.Delta.ToolCalls...)
		if ch.FinishReason != "" {
			finish = ch.FinishReason
		}
	}
	if err := sc.Err(); err != nil {
		return ChatResult{}, fmt.Errorf("llm: read chat stream: %w", err)
	}
	return ChatResult{Content: sb.String(), ToolCalls: toolCalls, FinishReason: finish}, nil
}

// compile-time guard: openAIClient satisfies Provider.
var _ Provider = (*openAIClient)(nil)
