package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/exo/gitstate/internal/config"
)

// ─── Prompt constants ──────────────────────────────────────────────────────
//
// These are the canonical prompt strings for each LLM operation. Keeping them
// as named constants makes them easy to audit, update, and test in isolation.

const systemEstimateDifficulty = `You are an expert software engineer assessing the semantic difficulty of a code change.

Your task: read a git diff and return a difficulty estimate on a 1-10 scale based on SEMANTIC complexity — not line count. A 4-line concurrency fix can be harder than 400 lines of boilerplate. Judge based on:
- Cognitive load required to understand the change
- Risk of subtle bugs or regressions
- Domain expertise required
- Breadth of impact across the codebase
- Algorithmic or architectural complexity

Respond ONLY with valid JSON matching exactly this schema (no markdown, no preamble):
{
  "difficulty": <number 1.0–10.0>,
  "rationale": "<2–3 sentences explaining the score>",
  "evidence": {
    "key_changes": ["<concise description of each major change>"],
    "risk_factors": ["<risks or complexity drivers observed>"],
    "complexity_signals": "<e.g. concurrency, state mutation, security boundary, perf-critical>"
  }
}`

const systemSynthesizeStatus = `You are a senior engineering lead writing a concise project status summary for non-technical leadership.

Summarize the provided recent activity (PRs, issues, commits) into a leadership-readable status update. Focus on:
- What shipped and is ready to use
- What is at risk or blocked
- Key open questions requiring decisions
- No individual rankings or performance judgments — show work patterns, not worker scores (decisions P2)

Write 3–5 short paragraphs. Plain prose, no bullet lists. Be direct and specific.`

// ─── Types ─────────────────────────────────────────────────────────────────

// DiffMeta carries optional metadata about the diff being estimated. Callers
// may leave fields empty; the service includes non-empty values in context.
type DiffMeta struct {
	PRID     string // pull_request UUID (optional)
	PRTitle  string // human-readable PR title (optional)
	RepoName string // e.g. "owner/repo" (optional)
	// Area is the cohort area (top-level dir) the change touches, used to frame
	// the exemplars (optional).
	Area string
	// Exemplars are similar past merged PRs in the same cohort, injected as
	// calibration anchors ("ran ~Xh predicted vs ~Yh actual"). Optional.
	Exemplars []Exemplar
}

// Exemplar is a past merged PR used as a difficulty anchor in the prompt. It
// gives the model concrete predicted-vs-actual ground truth from this org so its
// difficulty judgment is grounded in how long similar work actually took.
type Exemplar struct {
	Title          string
	Difficulty     float64
	PredictedHours float64
	ActualHours    float64
}

// DifficultySummary is the parsed result of EstimateDifficulty.
type DifficultySummary struct {
	// Difficulty is the model-judged semantic complexity on a 1–10 scale.
	Difficulty float64
	// Rationale is the model's brief justification.
	Rationale string
	// Evidence is the structured supporting detail returned by the model.
	Evidence map[string]interface{}
	// Model records which model produced this estimate.
	Model string
}

// ActivityItem is one unit of recent git activity passed to SynthesizeStatus.
type ActivityItem struct {
	Kind    string // "pr" | "issue" | "commit"
	Title   string
	Author  string
	State   string // e.g. "merged", "open", "closed"
	Summary string // optional additional context
}

// ─── Service ───────────────────────────────────────────────────────────────

// Service wraps a Provider and exposes higher-level LLM operations for
// gitstate. Create one per application lifecycle via New.
type Service struct {
	provider Provider
	model    string // recorded on each estimate for traceability
}

// defaultModel is the model recorded on estimates (and used as the gateway's
// requested model) when cfg.LLM.Model is unset: the cheapest current model for
// bulk effort scoring.
const defaultModel = "claude-haiku-4-5-20251001"

// New constructs a Service from config. Returns a service with a nil provider
// (all operations return ErrLLMNotConfigured) when no provider can be built —
// the service never panics on missing config.
//
// Gateway-off behavior is unchanged: it builds the Anthropic-direct client when
// an Anthropic API key is present, else a nil-provider Service. To route through
// the in-process llmux gateway, use NewWithGateway with a started *Gateway's
// base URL (see main.go wiring).
func New(cfg *config.Config) *Service {
	if cfg.LLM.AnthropicAPIKey == "" {
		return &Service{}
	}
	model := cfg.LLM.Model
	if model == "" {
		model = defaultModel
	}
	return &Service{
		provider: newAnthropicClient(cfg.LLM.AnthropicAPIKey, model),
		model:    model,
	}
}

// NewWithGateway constructs a Service that prefers the in-process llmux gateway
// when one is running (cfg.LLM.Gateway == "llmux" and gw is enabled): completions
// route through the gateway's OpenAI-compatible client. Otherwise it falls back
// to the legacy Anthropic-direct path (identical to New). The model string is
// still recorded/logged per estimate.
//
// gw may be nil (gateway off) — callers don't need a nil check.
func NewWithGateway(cfg *config.Config, gw *Gateway) *Service {
	model := cfg.LLM.Model
	if model == "" {
		model = defaultModel
	}
	if cfg.LLM.Gateway == GatewayKind && gw.Enabled() {
		// Gateway holds the provider keys server-side; the client needs no key.
		return &Service{
			provider: newOpenAIClient(gw.BaseURL(), "", model),
			model:    model,
		}
	}
	return New(cfg)
}

// NewWithProvider constructs a Service using a caller-supplied Provider
// (useful for testing or alternative backends).
func NewWithProvider(p Provider, model string) *Service {
	return &Service{provider: p, model: model}
}

// ChatClient is the streaming, tool-calling surface the agentic chat engine
// drives. It is the exported contract over openAIClient.ChatStream so callers
// outside this package (internal/chat) can run a multi-turn function-calling
// loop through the gateway without depending on the unexported client type.
// Tests inject their own ChatClient implementation.
type ChatClient interface {
	ChatStream(ctx context.Context, req ChatRequest) (ChatResult, error)
}

// NewChatClient builds a ChatClient targeting the in-process llmux gateway when
// one is enabled (the gateway holds provider keys server-side, so no API key is
// passed). model is the requested model id; when empty the package default is
// used. Returns (nil, false) when no gateway is available — the caller then
// reports that chat is unavailable rather than calling a real provider directly.
//
// gw may be nil (gateway off) — callers don't need a nil check.
func NewChatClient(gw *Gateway, model string) (ChatClient, bool) {
	if !gw.Enabled() {
		return nil, false
	}
	if model == "" {
		model = defaultModel
	}
	return newOpenAIClient(gw.BaseURL(), "", model), true
}

// ─── EstimateDifficulty ────────────────────────────────────────────────────

// difficultyModelResponse is the JSON shape we ask the model to return.
type difficultyModelResponse struct {
	Difficulty float64                `json:"difficulty"`
	Rationale  string                 `json:"rationale"`
	Evidence   map[string]interface{} `json:"evidence"`
}

// EstimateDifficulty asks the model to judge the semantic difficulty of diff.
// It returns a normalized 1–10 difficulty score, a short rationale, and the
// structured evidence the model used. Returns ErrLLMNotConfigured when no API
// key is configured.
func (s *Service) EstimateDifficulty(ctx context.Context, diff string, meta DiffMeta) (DifficultySummary, error) {
	if s.provider == nil {
		return DifficultySummary{}, ErrLLMNotConfigured
	}

	user := buildDiffPrompt(diff, meta)

	raw, err := s.provider.Complete(ctx, systemEstimateDifficulty, user)
	if err != nil {
		return DifficultySummary{}, fmt.Errorf("llm.EstimateDifficulty: %w", err)
	}

	parsed, err := parseDifficultyResponse(raw)
	if err != nil {
		return DifficultySummary{}, fmt.Errorf("llm.EstimateDifficulty: parse model reply: %w", err)
	}

	// Clamp to the documented 1–10 range regardless of what the model returned.
	if parsed.Difficulty < 1 {
		parsed.Difficulty = 1
	}
	if parsed.Difficulty > 10 {
		parsed.Difficulty = 10
	}

	return DifficultySummary{
		Difficulty: parsed.Difficulty,
		Rationale:  parsed.Rationale,
		Evidence:   parsed.Evidence,
		Model:      s.model,
	}, nil
}

// buildDiffPrompt constructs the user-turn message for difficulty estimation.
func buildDiffPrompt(diff string, meta DiffMeta) string {
	var b strings.Builder
	if meta.PRTitle != "" {
		fmt.Fprintf(&b, "PR title: %s\n", meta.PRTitle)
	}
	if meta.RepoName != "" {
		fmt.Fprintf(&b, "Repository: %s\n", meta.RepoName)
	}
	if meta.PRID != "" {
		fmt.Fprintf(&b, "PR ID: %s\n", meta.PRID)
	}

	// Calibration anchors: similar past merged PRs from this org. These ground the
	// model's difficulty judgment in how long comparable work ACTUALLY took (the
	// self-calibrating loop), without dictating the answer.
	if len(meta.Exemplars) > 0 {
		if meta.Area != "" {
			fmt.Fprintf(&b, "\nFor calibration, here are similar past merged PRs in `%s` (predicted vs actual time taken):\n", meta.Area)
		} else {
			b.WriteString("\nFor calibration, here are similar past merged PRs (predicted vs actual time taken):\n")
		}
		for _, ex := range meta.Exemplars {
			fmt.Fprintf(&b, "- difficulty %.1f: %q ran ~%.1fh predicted vs ~%.1fh actual\n",
				ex.Difficulty, ex.Title, ex.PredictedHours, ex.ActualHours)
		}
		b.WriteString("Use these only as soft anchors for how effort scales here; judge THIS diff on its own merits.\n")
	}

	if b.Len() > 0 {
		b.WriteString("\n")
	}
	b.WriteString("Git diff:\n```\n")
	b.WriteString(diff)
	b.WriteString("\n```")
	return b.String()
}

// parseDifficultyResponse robustly extracts our JSON payload from the model
// reply. The model is instructed to return only JSON, but we defensively strip
// any surrounding markdown code fences in case it doesn't comply.
func parseDifficultyResponse(raw string) (difficultyModelResponse, error) {
	cleaned := stripMarkdownJSON(raw)

	var result difficultyModelResponse
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return difficultyModelResponse{}, fmt.Errorf("json.Unmarshal: %w (raw: %.200s)", err, raw)
	}
	return result, nil
}

// ─── SynthesizeStatus ──────────────────────────────────────────────────────

// SynthesizeStatus summarizes recent PR/issue/commit activity into a
// leadership-readable status paragraph. Returns ErrLLMNotConfigured when no
// API key is configured.
func (s *Service) SynthesizeStatus(ctx context.Context, items []ActivityItem) (string, error) {
	if s.provider == nil {
		return "", ErrLLMNotConfigured
	}

	user := buildStatusPrompt(items)
	text, err := s.provider.Complete(ctx, systemSynthesizeStatus, user)
	if err != nil {
		return "", fmt.Errorf("llm.SynthesizeStatus: %w", err)
	}
	return strings.TrimSpace(text), nil
}

// buildStatusPrompt formats the activity items as a structured list for the
// model to summarize.
func buildStatusPrompt(items []ActivityItem) string {
	var b strings.Builder
	b.WriteString("Recent activity:\n\n")
	for i, item := range items {
		fmt.Fprintf(&b, "%d. [%s] %s", i+1, strings.ToUpper(item.Kind), item.Title)
		if item.Author != "" {
			fmt.Fprintf(&b, " (by %s)", item.Author)
		}
		if item.State != "" {
			fmt.Fprintf(&b, " — %s", item.State)
		}
		if item.Summary != "" {
			fmt.Fprintf(&b, "\n   %s", item.Summary)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// ─── Helpers ───────────────────────────────────────────────────────────────

// stripMarkdownJSON removes optional ```json / ``` fences that a model may
// wrap around an otherwise valid JSON response.
func stripMarkdownJSON(s string) string {
	s = strings.TrimSpace(s)
	// Handle ```json ... ``` and ``` ... ```
	for _, fence := range []string{"```json", "```"} {
		if strings.HasPrefix(s, fence) {
			s = strings.TrimPrefix(s, fence)
			s = strings.TrimSuffix(s, "```")
			s = strings.TrimSpace(s)
			break
		}
	}
	return s
}
