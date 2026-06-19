package notifications

import (
	"fmt"
	"strings"
)

// Rendered holds both representations of a digest so a channel can pick the one
// it needs: SlackPayload for Slack (and any generic webhook that accepts Slack
// Block Kit JSON), and Text for plain-text consumers (email body, fallback).
type Rendered struct {
	// SlackPayload is a Slack Block Kit message: {text, blocks:[...]}. The top
	// level "text" doubles as the notification fallback / plain summary, so a
	// generic webhook receiver still gets something readable without parsing
	// blocks.
	SlackPayload map[string]any `json:"slackPayload"`
	// Text is the plain-text rendering (used for email bodies and as a fallback).
	Text string `json:"text"`
	// Summary is a one-line summary suitable for a log row.
	Summary string `json:"summary"`
}

// Render produces both the Slack/webhook payload and the plain-text body for a
// digest.
func Render(d *Digest) Rendered {
	return Rendered{
		SlackPayload: renderSlack(d),
		Text:         renderText(d),
		Summary:      renderSummary(d),
	}
}

// renderSummary builds a compact one-line summary for the notification_log.
func renderSummary(d *Digest) string {
	if d.Empty {
		return d.Title + " — nothing to report"
	}
	parts := make([]string, 0, len(d.Metrics))
	for _, m := range d.Metrics {
		parts = append(parts, fmt.Sprintf("%s: %s", m.Label, m.Value))
	}
	if len(parts) == 0 {
		return d.Title
	}
	return d.Title + " — " + strings.Join(parts, ", ")
}

// renderText renders a digest as a plain-text body (email / fallback).
func renderText(d *Digest) string {
	var b strings.Builder
	b.WriteString(d.Title)
	b.WriteByte('\n')
	if d.Subtitle != "" {
		b.WriteString(d.Subtitle)
		b.WriteByte('\n')
	}
	b.WriteString(strings.Repeat("=", len([]rune(d.Title))))
	b.WriteString("\n\n")

	if d.Empty {
		reason := d.EmptyReason
		if reason == "" {
			reason = "Nothing to report."
		}
		b.WriteString(reason)
		b.WriteByte('\n')
		return b.String()
	}

	if len(d.Metrics) > 0 {
		parts := make([]string, 0, len(d.Metrics))
		for _, m := range d.Metrics {
			parts = append(parts, fmt.Sprintf("%s: %s", m.Label, m.Value))
		}
		b.WriteString(strings.Join(parts, "  |  "))
		b.WriteString("\n\n")
	}

	for _, s := range d.Sections {
		if len(s.Lines) == 0 {
			continue
		}
		b.WriteString(s.Heading)
		b.WriteByte('\n')
		for _, ln := range s.Lines {
			b.WriteString("  • ")
			b.WriteString(ln.Text)
			if ln.Meta != "" {
				b.WriteString("  (")
				b.WriteString(ln.Meta)
				b.WriteString(")")
			}
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	b.WriteString("— gitstate · generated ")
	b.WriteString(d.GeneratedAt.Format("Jan 2, 2006 15:04 MST"))
	b.WriteByte('\n')
	return b.String()
}

// renderSlack renders a digest as a Slack Block Kit message. The structure is
// also a perfectly valid generic-webhook JSON payload; the top-level "text"
// field is a usable fallback for any receiver that ignores blocks.
func renderSlack(d *Digest) map[string]any {
	blocks := make([]map[string]any, 0, 8)

	// Header.
	blocks = append(blocks, map[string]any{
		"type": "header",
		"text": map[string]any{"type": "plain_text", "text": d.Title, "emoji": true},
	})
	if d.Subtitle != "" {
		blocks = append(blocks, map[string]any{
			"type":     "context",
			"elements": []map[string]any{{"type": "mrkdwn", "text": d.Subtitle}},
		})
	}

	if d.Empty {
		reason := d.EmptyReason
		if reason == "" {
			reason = "Nothing to report."
		}
		blocks = append(blocks, mrkdwnSection(reason))
		return map[string]any{"text": d.Title + " — nothing to report", "blocks": blocks}
	}

	// Metrics as a single fields section.
	if len(d.Metrics) > 0 {
		fields := make([]map[string]any, 0, len(d.Metrics))
		for _, m := range d.Metrics {
			fields = append(fields, map[string]any{
				"type": "mrkdwn",
				"text": fmt.Sprintf("*%s*\n%s", m.Label, m.Value),
			})
		}
		blocks = append(blocks, map[string]any{"type": "section", "fields": fields})
	}

	for _, s := range d.Sections {
		if len(s.Lines) == 0 {
			continue
		}
		blocks = append(blocks, map[string]any{"type": "divider"})
		blocks = append(blocks, mrkdwnSection("*"+slackEscape(s.Heading)+"*"))
		var sb strings.Builder
		for _, ln := range s.Lines {
			sb.WriteString("• ")
			sb.WriteString(slackEscape(ln.Text))
			if ln.Meta != "" {
				sb.WriteString("  _")
				sb.WriteString(slackEscape(ln.Meta))
				sb.WriteString("_")
			}
			sb.WriteByte('\n')
		}
		blocks = append(blocks, mrkdwnSection(strings.TrimRight(sb.String(), "\n")))
	}

	blocks = append(blocks, map[string]any{
		"type": "context",
		"elements": []map[string]any{
			{"type": "mrkdwn", "text": "gitstate · generated " + d.GeneratedAt.Format("Jan 2, 2006 15:04 MST")},
		},
	})

	return map[string]any{"text": renderSummary(d), "blocks": blocks}
}

func mrkdwnSection(text string) map[string]any {
	return map[string]any{
		"type": "section",
		"text": map[string]any{"type": "mrkdwn", "text": text},
	}
}

// slackEscape escapes the three characters Slack mrkdwn treats specially.
func slackEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
