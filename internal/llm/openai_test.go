package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/exo/gitstate/internal/config"
)

// TestOpenAIClientBuildsChatRequest asserts the OpenAI client POSTs a correct
// /chat/completions request (URL path, headers, body shape) against an httptest
// server — no real API call.
func TestOpenAIClientBuildsChatRequest(t *testing.T) {
	var (
		gotPath   string
		gotAuth   string
		gotCT     string
		gotBody   chatCompletionRequest
		bodyBytes []byte
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		bodyBytes, _ = io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"hello world"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	c := newOpenAIClient(srv.URL+"/v1", "sk-test-123", "claude-haiku-4-5")
	out, err := c.Complete(context.Background(), "be terse", "say hi")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if out != "hello world" {
		t.Errorf("content = %q, want %q", out, "hello world")
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("path = %q, want /v1/chat/completions", gotPath)
	}
	if gotAuth != "Bearer sk-test-123" {
		t.Errorf("Authorization = %q, want Bearer sk-test-123", gotAuth)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotCT)
	}
	if gotBody.Model != "claude-haiku-4-5" {
		t.Errorf("body.model = %q, want claude-haiku-4-5", gotBody.Model)
	}
	if len(gotBody.Messages) != 2 ||
		gotBody.Messages[0].Role != "system" || gotBody.Messages[0].Content != "be terse" ||
		gotBody.Messages[1].Role != "user" || gotBody.Messages[1].Content != "say hi" {
		t.Errorf("messages = %+v, want [system:be terse, user:say hi]", gotBody.Messages)
	}
}

// TestOpenAIClientNoAuthHeaderWhenKeyEmpty asserts that an empty API key (the
// gateway case — keys live server-side) omits the Authorization header.
func TestOpenAIClientNoAuthHeaderWhenKeyEmpty(t *testing.T) {
	var hadAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadAuth = r.Header["Authorization"]
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	c := newOpenAIClient(srv.URL+"/v1", "", "m")
	if _, err := c.Complete(context.Background(), "", "hi"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if hadAuth {
		t.Error("Authorization header should be absent when apiKey is empty")
	}
}

// TestChatStreamSSE asserts ChatStream consumes an SSE stream and assembles the
// full content via OnDelta.
func TestChatStreamSSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := newOpenAIClient(srv.URL+"/v1", "", "m")
	var deltas []string
	res, err := c.ChatStream(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		Stream:   true,
		OnDelta:  func(tok string) { deltas = append(deltas, tok) },
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if res.Content != "Hello" {
		t.Errorf("content = %q, want Hello", res.Content)
	}
	if res.FinishReason != "stop" {
		t.Errorf("finish = %q, want stop", res.FinishReason)
	}
	if strings.Join(deltas, "") != "Hello" {
		t.Errorf("deltas = %v, want [Hel lo]", deltas)
	}
}

// TestNewWithGatewayFallsBackWhenOff asserts that with the gateway off, the
// service is built from the Anthropic-direct path (or nil-provider) — never the
// OpenAI client. We verify via behavior: an Anthropic key yields a configured
// service; no key yields ErrLLMNotConfigured.
func TestNewWithGatewayFallsBackWhenOff(t *testing.T) {
	// Gateway off + no key → nil-provider service.
	cfg := &config.Config{}
	svc := NewWithGateway(cfg, nil)
	if _, err := svc.Complete(context.Background(), "s", "u"); err != ErrLLMNotConfigured {
		t.Errorf("gateway-off no-key: err = %v, want ErrLLMNotConfigured", err)
	}

	// Gateway off + Anthropic key → Anthropic-direct provider is wired
	// (provider is non-nil; we don't make a network call).
	cfg2 := &config.Config{}
	cfg2.LLM.AnthropicAPIKey = "sk-ant-test"
	svc2 := NewWithGateway(cfg2, nil)
	if svc2.provider == nil {
		t.Error("gateway-off with key: expected a non-nil Anthropic-direct provider")
	}
	if _, ok := svc2.provider.(*anthropicClient); !ok {
		t.Errorf("gateway-off with key: provider = %T, want *anthropicClient", svc2.provider)
	}
}

// TestStartGatewayOffReturnsNil asserts StartGateway returns a nil Gateway when
// the gateway is unset (so callers fall back), and that nil Gateway methods are
// safe.
func TestStartGatewayOffReturnsNil(t *testing.T) {
	cfg := &config.Config{} // Gateway unset
	gw, err := StartGateway(context.Background(), cfg)
	if err != nil {
		t.Fatalf("StartGateway: %v", err)
	}
	if gw != nil {
		t.Fatal("expected nil Gateway when gateway is off")
	}
	// nil-safe accessors
	if gw.Enabled() {
		t.Error("nil gateway should report Enabled()=false")
	}
	if gw.BaseURL() != "" {
		t.Error("nil gateway should report empty BaseURL()")
	}
	gw.Close() // must not panic
}
