// Package notifications — pure unit tests for the digest renderers (Slack Block
// Kit, plain-text, summary), mrkdwn escaping, empty-state messages, and the pure
// helpers (truncate, formatLeaveSpan, ValidKind, startOfWeek, sanitizeHeader).
// No DB or network: digests are built as small in-memory structs.
package notifications

import (
	"strings"
	"testing"
	"time"

	"github.com/exo/gitstate/internal/store"
)

func genAt() time.Time { return time.Date(2026, 6, 15, 14, 30, 0, 0, time.UTC) }

func sampleDigest() *Digest {
	return &Digest{
		Kind:        KindWeeklyStatus,
		Title:       "Weekly status",
		Subtitle:    "Week of Jun 15, 2026",
		GeneratedAt: genAt(),
		Metrics: []Metric{
			{Label: "Issues shipped", Value: "3"},
			{Label: "PRs merged", Value: "5"},
		},
		Sections: []Section{
			{Heading: "Open work", Lines: []Line{{Text: "2 open · 1 in progress"}}},
			{Heading: "Recent movement", Lines: []Line{
				{Text: "Fixed login bug", Meta: "pr · ada"},
				{Text: "Empty section below should be skipped"},
			}},
			{Heading: "Skipped", Lines: nil}, // empty section omitted in both renders
		},
	}
}

// ── ValidKind ───────────────────────────────────────────────────────────────

func TestValidKind(t *testing.T) {
	for _, k := range []string{KindWeeklyStatus, KindStalePRs, KindOOO} {
		if !ValidKind(k) {
			t.Errorf("ValidKind(%q) = false, want true", k)
		}
	}
	for _, k := range []string{"", "nope", "weekly"} {
		if ValidKind(k) {
			t.Errorf("ValidKind(%q) = true, want false", k)
		}
	}
}

// ── renderSummary ───────────────────────────────────────────────────────────

func TestRenderSummary(t *testing.T) {
	d := sampleDigest()
	got := renderSummary(d)
	want := "Weekly status — Issues shipped: 3, PRs merged: 5"
	if got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

func TestRenderSummary_Empty(t *testing.T) {
	d := &Digest{Title: "Stale PRs", Empty: true}
	if got := renderSummary(d); got != "Stale PRs — nothing to report" {
		t.Errorf("empty summary = %q", got)
	}
}

func TestRenderSummary_NoMetrics(t *testing.T) {
	d := &Digest{Title: "Title only"}
	if got := renderSummary(d); got != "Title only" {
		t.Errorf("no-metrics summary = %q", got)
	}
}

// ── renderText ──────────────────────────────────────────────────────────────

func TestRenderText_HappyPath(t *testing.T) {
	d := sampleDigest()
	out := renderText(d)

	if !strings.HasPrefix(out, "Weekly status\nWeek of Jun 15, 2026\n") {
		t.Errorf("text missing title/subtitle header:\n%s", out)
	}
	// Underline length = rune length of the title (13 chars).
	if !strings.Contains(out, strings.Repeat("=", len([]rune("Weekly status")))) {
		t.Error("text missing title underline")
	}
	// Metrics joined with the separator.
	if !strings.Contains(out, "Issues shipped: 3  |  PRs merged: 5") {
		t.Errorf("text metrics line wrong:\n%s", out)
	}
	// Section line with meta in parentheses.
	if !strings.Contains(out, "  • Fixed login bug  (pr · ada)") {
		t.Errorf("text section line wrong:\n%s", out)
	}
	// Line without meta has no trailing parens.
	if strings.Contains(out, "Empty section below should be skipped  (") {
		t.Error("line without meta should not render empty parens")
	}
	// Empty-lines section ("Skipped") must NOT appear.
	if strings.Contains(out, "Skipped") {
		t.Error("empty section heading leaked into text")
	}
	// Footer with formatted timestamp.
	if !strings.Contains(out, "generated Jun 15, 2026 14:30 UTC") {
		t.Errorf("text footer wrong:\n%s", out)
	}
}

func TestRenderText_EmptyState(t *testing.T) {
	d := &Digest{Title: "Stale PRs", GeneratedAt: genAt(), Empty: true, EmptyReason: "Nothing is blocked."}
	out := renderText(d)
	if !strings.Contains(out, "Nothing is blocked.") {
		t.Errorf("empty reason missing:\n%s", out)
	}
	// No footer for the empty branch (it returns early).
	if strings.Contains(out, "generated") {
		t.Error("empty text should not include footer")
	}
}

func TestRenderText_EmptyDefaultReason(t *testing.T) {
	d := &Digest{Title: "X", GeneratedAt: genAt(), Empty: true}
	if !strings.Contains(renderText(d), "Nothing to report.") {
		t.Error("empty digest with no reason should use default")
	}
}

// ── renderSlack ─────────────────────────────────────────────────────────────

func TestRenderSlack_Structure(t *testing.T) {
	d := sampleDigest()
	payload := renderSlack(d)

	if payload["text"] == "" {
		t.Error("slack payload missing top-level fallback text")
	}
	blocks, ok := payload["blocks"].([]map[string]any)
	if !ok || len(blocks) == 0 {
		t.Fatalf("blocks missing or wrong type: %T", payload["blocks"])
	}
	// First block is the header with the title.
	if blocks[0]["type"] != "header" {
		t.Errorf("first block type = %v, want header", blocks[0]["type"])
	}
	hdrText := blocks[0]["text"].(map[string]any)
	if hdrText["text"] != "Weekly status" {
		t.Errorf("header text = %v", hdrText["text"])
	}
	// A metrics fields-section exists.
	foundFields := false
	for _, b := range blocks {
		if f, ok := b["fields"]; ok {
			if fields, ok := f.([]map[string]any); ok && len(fields) == 2 {
				foundFields = true
			}
		}
	}
	if !foundFields {
		t.Error("expected a metrics fields section with 2 fields")
	}
	// A divider precedes each non-empty section.
	dividers := 0
	for _, b := range blocks {
		if b["type"] == "divider" {
			dividers++
		}
	}
	if dividers != 2 { // "Open work" + "Recent movement"; "Skipped" omitted
		t.Errorf("dividers = %d, want 2", dividers)
	}
}

func TestRenderSlack_EmptyState(t *testing.T) {
	d := &Digest{Title: "Who's out", GeneratedAt: genAt(), Empty: true, EmptyReason: "Full team availability."}
	payload := renderSlack(d)
	if payload["text"] != "Who's out — nothing to report" {
		t.Errorf("empty slack fallback = %v", payload["text"])
	}
	blocks := payload["blocks"].([]map[string]any)
	// header + a section carrying the reason. No divider/footer.
	joined := flatten(blocks)
	if !strings.Contains(joined, "Full team availability.") {
		t.Errorf("empty reason missing from blocks: %s", joined)
	}
}

func TestRender_BothRepresentations(t *testing.T) {
	r := Render(sampleDigest())
	if r.Text == "" || r.Summary == "" || r.SlackPayload == nil {
		t.Error("Render should populate Text, Summary, and SlackPayload")
	}
}

// ── slackEscape ─────────────────────────────────────────────────────────────

func TestSlackEscape(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"a & b", "a &amp; b"},
		{"<tag>", "&lt;tag&gt;"},
		{"x < y > z & w", "x &lt; y &gt; z &amp; w"},
		{"", ""},
	}
	for _, c := range cases {
		if got := slackEscape(c.in); got != c.want {
			t.Errorf("slackEscape(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRenderSlack_EscapesSectionContent(t *testing.T) {
	d := &Digest{
		Title:       "T",
		GeneratedAt: genAt(),
		Sections: []Section{
			{Heading: "A & B", Lines: []Line{{Text: "x<y>z", Meta: "by <bot>"}}},
		},
	}
	joined := flatten(renderSlack(d)["blocks"].([]map[string]any))
	if !strings.Contains(joined, "A &amp; B") {
		t.Errorf("heading not escaped: %s", joined)
	}
	if !strings.Contains(joined, "x&lt;y&gt;z") {
		t.Errorf("line text not escaped: %s", joined)
	}
	if !strings.Contains(joined, "by &lt;bot&gt;") {
		t.Errorf("meta not escaped: %s", joined)
	}
}

// ── truncate ────────────────────────────────────────────────────────────────

func TestTruncate(t *testing.T) {
	cases := []struct {
		s    string
		n    int
		want string
	}{
		{"short", 90, "short"},
		{"exactly5", 8, "exactly5"},
		{"abcdef", 5, "abcd…"}, // n-1 runes + ellipsis
		{"abcdef", 1, "a"},     // n<=1: hard cut, no ellipsis
		{"αβγδε", 3, "αβ…"},    // rune-aware (not byte-aware)
		{"", 5, ""},
	}
	for _, c := range cases {
		if got := truncate(c.s, c.n); got != c.want {
			t.Errorf("truncate(%q,%d) = %q, want %q", c.s, c.n, got, c.want)
		}
	}
}

// ── formatLeaveSpan + countDistinctPeople ───────────────────────────────────

func TestFormatLeaveSpan(t *testing.T) {
	// Same calendar day → single date.
	e := &store.OOOEntry{StartDate: time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC), EndDate: time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)}
	if got := formatLeaveSpan(e); got != "Mon Jun 15" {
		t.Errorf("single-day span = %q, want 'Mon Jun 15'", got)
	}
	// Multi-day → range with en dash.
	e2 := &store.OOOEntry{StartDate: time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC), EndDate: time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)}
	if got := formatLeaveSpan(e2); got != "Mon Jun 15 – Wed Jun 17" {
		t.Errorf("multi-day span = %q", got)
	}
}

func TestCountDistinctPeople(t *testing.T) {
	entries := []*store.OOOEntry{
		{UserID: "a"}, {UserID: "b"}, {UserID: "a"}, {UserID: "c"},
	}
	if got := countDistinctPeople(entries); got != 3 {
		t.Errorf("distinct people = %d, want 3", got)
	}
	if got := countDistinctPeople(nil); got != 0 {
		t.Errorf("empty = %d, want 0", got)
	}
}

// ── startOfWeek ─────────────────────────────────────────────────────────────

func TestStartOfWeek(t *testing.T) {
	// All days within the week of Mon 2026-06-15 map back to that Monday 00:00 UTC.
	want := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	for _, d := range []time.Time{
		time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC),   // Mon
		time.Date(2026, 6, 17, 23, 59, 0, 0, time.UTC), // Wed
		time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC),  // Sun (still same ISO week)
	} {
		if got := startOfWeek(d); !got.Equal(want) {
			t.Errorf("startOfWeek(%s) = %v, want %v", d.Format("Mon"), got, want)
		}
	}
	// The next Monday rolls over to a new week start.
	if got := startOfWeek(time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC)); !got.Equal(time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("next Monday = %v", got)
	}
}

// ── SMTP config + sanitizeHeader ────────────────────────────────────────────

func TestSMTPConfig_Configured(t *testing.T) {
	if (SMTPConfig{Host: "smtp.x", From: "a@b"}).Configured() != true {
		t.Error("host+from should be configured")
	}
	if (SMTPConfig{Host: "smtp.x"}).Configured() {
		t.Error("missing From should be not-configured")
	}
	if (SMTPConfig{From: "a@b"}).Configured() {
		t.Error("missing Host should be not-configured")
	}
}

func TestLoadSMTPConfig_Defaults(t *testing.T) {
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_PORT", "")
	t.Setenv("SMTP_USER", "user@example.com")
	t.Setenv("SMTP_FROM", "")
	c := LoadSMTPConfig()
	if c.Port != "587" {
		t.Errorf("default port = %q, want 587", c.Port)
	}
	if c.From != "user@example.com" {
		t.Errorf("From should default to User, got %q", c.From)
	}
}

func TestSanitizeHeader(t *testing.T) {
	cases := []struct{ in, want string }{
		{"normal subject", "normal subject"},
		{"inject\r\nBcc: evil@x", "inject  Bcc: evil@x"},
		{"with\nnewline", "with newline"},
		{"with\rcarriage", "with carriage"},
	}
	for _, c := range cases {
		if got := sanitizeHeader(c.in); got != c.want {
			t.Errorf("sanitizeHeader(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDeliver_UnknownKind(t *testing.T) {
	err := Deliver(nil, "carrier-pigeon", "target", Rendered{})
	if err == nil {
		t.Error("unknown channel kind should error")
	}
}

// flatten concatenates all string text inside a block list for substring checks.
func flatten(blocks []map[string]any) string {
	var b strings.Builder
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			b.WriteString(x)
			b.WriteByte('\n')
		case map[string]any:
			for _, vv := range x {
				walk(vv)
			}
		case []map[string]any:
			for _, vv := range x {
				walk(vv)
			}
		case []any:
			for _, vv := range x {
				walk(vv)
			}
		}
	}
	for _, blk := range blocks {
		walk(blk)
	}
	return b.String()
}
