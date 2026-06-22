package main

import (
	"fmt"
	"io"
	"strings"
)

// fmtDuration renders a seconds count as a compact human duration (e.g. 2d 3h).
// Zero or negative values render as "—" since they usually mean "not yet
// measurable" (e.g. an unmerged PR has no lead time).
func fmtDuration(secs int64) string {
	if secs <= 0 {
		return "—"
	}
	d := secs
	days := d / 86400
	d %= 86400
	hours := d / 3600
	d %= 3600
	mins := d / 60

	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if mins > 0 && days == 0 { // omit minutes once we're into days
		parts = append(parts, fmt.Sprintf("%dm", mins))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%ds", secs)
	}
	return strings.Join(parts, " ")
}

// truncate shortens s to at most n runes, appending an ellipsis when cut.
func truncate(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// issueRef renders a stable "#<number>" label, falling back to the id when an
// issue has no platform number (native issues).
func issueRef(number int, id string) string {
	if number > 0 {
		return fmt.Sprintf("#%d", number)
	}
	return id
}

// renderIssueContext writes the human-readable issue bundle summary.
func renderIssueContext(w io.Writer, c *issueContext) {
	iss := c.Issue
	fmt.Fprintf(w, "%s  %s\n", issueRef(iss.Number, iss.ID), iss.Title)

	state := iss.State
	if iss.DerivedState != "" && iss.DerivedState != iss.State {
		state = fmt.Sprintf("%s (derived: %s)", iss.State, iss.DerivedState)
	}
	fmt.Fprintf(w, "state: %s", state)
	if iss.Assignee != "" {
		fmt.Fprintf(w, "   assignee: %s", iss.Assignee)
	}
	if len(iss.Labels) > 0 {
		fmt.Fprintf(w, "   labels: %s", strings.Join(iss.Labels, ", "))
	}
	fmt.Fprintln(w)

	if body := strings.TrimSpace(iss.Body); body != "" {
		fmt.Fprintf(w, "\n%s\n", truncate(body, 280))
	}

	if len(c.RelatedPRs) > 0 {
		fmt.Fprintf(w, "\nRelated PRs (%d):\n", len(c.RelatedPRs))
		for _, pr := range c.RelatedPRs {
			merged := ""
			if pr.Merged {
				merged = fmt.Sprintf(" merged in %s", fmtDuration(pr.LeadTimeSecs))
			}
			fmt.Fprintf(w, "  %s [%s]%s  %s\n",
				issueRef(pr.Number, pr.ID), pr.State, merged, truncate(pr.Title, 72))
		}
	}

	if len(c.Commits) > 0 {
		fmt.Fprintf(w, "\nRecent commits (%d):\n", len(c.Commits))
		for _, cm := range c.Commits {
			sha := cm.SHA
			if len(sha) > 8 {
				sha = sha[:8]
			}
			fmt.Fprintf(w, "  %-8s %s\n", sha, truncate(cm.Subject, 72))
		}
	}

	if len(c.TouchedPaths) > 0 {
		fmt.Fprintf(w, "\nHistorically-touched paths (%d):\n", len(c.TouchedPaths))
		for _, p := range c.TouchedPaths {
			fmt.Fprintf(w, "  %s\n", p)
		}
	}

	if len(c.Similar) > 0 {
		fmt.Fprintf(w, "\nSimilar past issues (%d):\n", len(c.Similar))
		for _, s := range c.Similar {
			fmt.Fprintf(w, "  %s [%s] %s\n",
				issueRef(s.Number, s.ID), s.State, truncate(s.Title, 64))
			if s.ResolvingPR != nil {
				pr := s.ResolvingPR
				fmt.Fprintf(w, "      resolved by %s (%s)\n",
					issueRef(pr.Number, pr.ID), pr.State)
			}
		}
	}
}

// renderPRContext writes the human-readable PR bundle summary.
func renderPRContext(w io.Writer, c *prContext) {
	pr := c.PR
	fmt.Fprintf(w, "%s  %s\n", issueRef(pr.Number, pr.ID), pr.Title)

	state := pr.State
	if pr.Merged {
		state += " (merged)"
	}
	fmt.Fprintf(w, "state: %s", state)
	if pr.AuthorLogin != "" {
		fmt.Fprintf(w, "   author: %s", pr.AuthorLogin)
	}
	d := c.DiffSummary
	fmt.Fprintf(w, "   +%d/-%d across %d files\n", d.Additions, d.Deletions, d.ChangedFiles)

	if c.CycleTimeSecs != nil {
		fmt.Fprintf(w, "cycle time: %s", fmtDuration(*c.CycleTimeSecs))
	} else {
		fmt.Fprint(w, "cycle time: —")
	}
	if c.Estimate != nil && c.Estimate.PredictedSecs != nil {
		fmt.Fprintf(w, "   estimated effort: %s", fmtDuration(int64(*c.Estimate.PredictedSecs)))
		if c.Estimate.SizeBucket != "" {
			fmt.Fprintf(w, " (%s)", c.Estimate.SizeBucket)
		}
	}
	fmt.Fprintln(w)
}

// renderRunList writes a compact table of logged agent runs.
func renderRunList(w io.Writer, runs []agentRun) {
	if len(runs) == 0 {
		fmt.Fprintln(w, "no agent runs")
		return
	}
	fmt.Fprintf(w, "%-12s %-14s %-10s %s\n", "ID", "AGENT", "ACTION", "GOAL")
	for _, r := range runs {
		id := r.ID
		if len(id) > 12 {
			id = id[:12]
		}
		action := r.HumanAction
		if action == "" {
			action = "—"
		}
		fmt.Fprintf(w, "%-12s %-14s %-10s %s\n",
			id, truncate(r.AgentName, 14), action, truncate(r.Goal, 54))
	}
}

// renderIssueList writes a compact table of issues.
func renderIssueList(w io.Writer, issues []issue) {
	if len(issues) == 0 {
		fmt.Fprintln(w, "no issues")
		return
	}
	fmt.Fprintf(w, "%-7s %-13s %s\n", "REF", "STATE", "TITLE")
	for _, iss := range issues {
		state := iss.State
		if iss.DerivedState != "" && iss.DerivedState != iss.State {
			state = iss.DerivedState
		}
		fmt.Fprintf(w, "%-7s %-13s %s\n",
			issueRef(iss.Number, iss.ID), truncate(state, 13), truncate(iss.Title, 70))
	}
}
