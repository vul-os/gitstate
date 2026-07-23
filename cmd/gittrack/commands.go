package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// commonFlags holds the global flags shared by every subcommand. Each command
// registers them on its own FlagSet so per-command -h works and flags may
// appear after positional args.
type commonFlags struct {
	json  bool
	url   string
	token string
}

func registerCommon(fs *flag.FlagSet, c *commonFlags) {
	fs.BoolVar(&c.json, "json", false, "emit raw server JSON")
	fs.StringVar(&c.url, "url", "", "API base URL (overrides $GITSTATE_URL)")
	fs.StringVar(&c.token, "token", "", "API token (overrides $GITSTATE_TOKEN)")
}

// reorderArgs moves positional (non-flag) arguments after the flag arguments so
// that Go's flag package — which stops at the first non-flag token — still sees
// every flag. This lets users write `gittrack context 42 --json` naturally.
// Everything following a literal "--" is treated as positional verbatim.
func reorderArgs(args []string) []string {
	var flags, positional []string
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			// A "--flag value" pair (no '=') consumes the next token, unless
			// it is a known bool flag.
			if !strings.Contains(a, "=") && !isBoolFlag(a) && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
		} else {
			positional = append(positional, a)
		}
		i++
	}
	return append(flags, positional...)
}

// isBoolFlag reports whether a flag token is one of gittrack's boolean flags,
// which do not consume a following value.
func isBoolFlag(a string) bool {
	name := strings.TrimLeft(a, "-")
	switch name {
	case "json", "h", "help", "tests-passed":
		return true
	}
	return false
}

// printRaw pretty-prints raw server JSON when it parses, otherwise echoes the
// bytes verbatim. Either way the output is a faithful representation of the
// server payload so it pipes cleanly into an agent.
func printRaw(body []byte) {
	var pretty any
	if err := json.Unmarshal(body, &pretty); err == nil {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		_ = enc.Encode(pretty)
		return
	}
	os.Stdout.Write(body)
	if len(body) == 0 || body[len(body)-1] != '\n' {
		fmt.Fprintln(os.Stdout)
	}
}

// ── gittrack context <issue-id> ───────────────────────────────────────────────

func cmdContext(args []string) int {
	fs := flag.NewFlagSet("context", flag.ContinueOnError)
	var cf commonFlags
	registerCommon(fs, &cf)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: gittrack context <issue-id> [--json] [--url U] [--token T]")
		fmt.Fprintln(os.Stderr, "Fetch the full issue context bundle for an AI agent.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	id := fs.Arg(0)

	cl, err := resolveClient(cf.url, cf.token)
	if err != nil {
		return fail(err)
	}

	body, err := cl.do("GET", "/api/context/issue/"+url.PathEscape(id))
	if err != nil {
		return fail(err)
	}

	if cf.json {
		printRaw(body)
		return 0
	}

	var ctx issueContext
	if err := json.Unmarshal(body, &ctx); err != nil {
		return fail(fmt.Errorf("decode context bundle: %w", err))
	}
	renderIssueContext(os.Stdout, &ctx)
	return 0
}

// ── gittrack pr <id> ──────────────────────────────────────────────────────────

func cmdPR(args []string) int {
	fs := flag.NewFlagSet("pr", flag.ContinueOnError)
	var cf commonFlags
	registerCommon(fs, &cf)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: gittrack pr <id> [--json] [--url U] [--token T]")
		fmt.Fprintln(os.Stderr, "Fetch a PR context bundle (diff summary, cycle time, effort estimate).")
		fs.PrintDefaults()
	}
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	id := fs.Arg(0)

	cl, err := resolveClient(cf.url, cf.token)
	if err != nil {
		return fail(err)
	}

	body, err := cl.do("GET", "/api/context/pr/"+url.PathEscape(id))
	if err != nil {
		return fail(err)
	}

	if cf.json {
		printRaw(body)
		return 0
	}

	var ctx prContext
	if err := json.Unmarshal(body, &ctx); err != nil {
		return fail(fmt.Errorf("decode PR bundle: %w", err))
	}
	renderPRContext(os.Stdout, &ctx)
	return 0
}

// ── gittrack issues [--state open] [--limit N] ────────────────────────────────

func cmdIssues(args []string) int {
	fs := flag.NewFlagSet("issues", flag.ContinueOnError)
	var cf commonFlags
	registerCommon(fs, &cf)
	var state string
	var limit int
	fs.StringVar(&state, "state", "", "filter by state (open, in_progress, done, closed)")
	fs.IntVar(&limit, "limit", 0, "maximum number of issues to print (0 = all)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: gittrack issues [--state open] [--limit N] [--json]")
		fmt.Fprintln(os.Stderr, "List issues for the org resolved from the API token.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return 2
	}

	cl, err := resolveClient(cf.url, cf.token)
	if err != nil {
		return fail(err)
	}

	path := "/api/issues"
	if state != "" {
		q := url.Values{}
		q.Set("state", state)
		path += "?" + q.Encode()
	}

	body, err := cl.do("GET", path)
	if err != nil {
		return fail(err)
	}

	var issues []issue
	if err := json.Unmarshal(body, &issues); err != nil {
		return fail(fmt.Errorf("decode issues: %w", err))
	}
	if limit > 0 && len(issues) > limit {
		issues = issues[:limit]
	}

	if cf.json {
		// Re-encode the (possibly limited) slice so --limit is honoured in
		// JSON mode too, keeping the contract "what you see is what you pipe".
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		if err := enc.Encode(issues); err != nil {
			return fail(err)
		}
		return 0
	}

	renderIssueList(os.Stdout, issues)
	return 0
}

// ── gittrack whoami ───────────────────────────────────────────────────────────

func cmdWhoami(args []string) int {
	fs := flag.NewFlagSet("whoami", flag.ContinueOnError)
	var cf commonFlags
	registerCommon(fs, &cf)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: gittrack whoami [--url U] [--token T]")
		fmt.Fprintln(os.Stderr, "Validate the API token against gitstate and print the configured URL.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return 2
	}

	cl, err := resolveClient(cf.url, cf.token)
	if err != nil {
		return fail(err)
	}

	// No dedicated identity endpoint exists, so hit a cheap authed read. A 2xx
	// proves the token is valid and the org resolved server-side.
	if _, err := cl.do("GET", "/api/issues?state=open"); err != nil {
		return fail(fmt.Errorf("token validation failed: %w", err))
	}

	fmt.Fprintf(os.Stdout, "OK — token valid\n")
	fmt.Fprintf(os.Stdout, "url:   %s\n", cl.baseURL)
	fmt.Fprintf(os.Stdout, "token: %s\n", maskToken(cl.token))
	return 0
}

// maskToken returns a safe-to-print identifier for a token: its prefix up to
// and including the first underscore plus a few following chars, never the
// secret body. e.g. "gsk_ab12…". It must never reveal enough to reconstruct
// the token.
func maskToken(tok string) string {
	if tok == "" {
		return "(none)"
	}
	prefix := ""
	if i := strings.IndexByte(tok, '_'); i >= 0 && i+1 < len(tok) {
		prefix = tok[:i+1]
		rest := tok[i+1:]
		n := len(rest)
		if n > 4 {
			n = 4
		}
		return prefix + rest[:n] + "…"
	}
	// No recognisable prefix: reveal at most the first 4 chars.
	n := len(tok)
	if n > 4 {
		n = 4
	}
	return tok[:n] + "…"
}

// ── gittrack log-run --goal "…" [links/metrics] ───────────────────────────────

// cmdLogRun records an agent run via POST /api/agent-runs. It folds the
// --additions/--deletions/--files flags into a diffSummary object and only sends
// the optional fields the user actually set (pointers stay nil otherwise), so the
// server's defaults apply. On success it prints the created run id (or --json).
func cmdLogRun(args []string) int {
	fs := flag.NewFlagSet("log-run", flag.ContinueOnError)
	var cf commonFlags
	registerCommon(fs, &cf)

	var (
		goal        string
		repo        string
		pr          string
		issue       string
		agent       string
		branch      string
		action      string
		iterations  int
		cost        float64
		additions   int
		deletions   int
		files       int
		testsPassed bool
	)
	fs.StringVar(&goal, "goal", "", "what the agent set out to do (required)")
	fs.StringVar(&repo, "repo", "", "repo id this run worked on")
	fs.StringVar(&pr, "pr", "", "pull request id this run produced")
	fs.StringVar(&issue, "issue", "", "issue id this run addressed")
	fs.StringVar(&agent, "agent", "gittrack", "agent name (e.g. claude-code, cursor)")
	fs.StringVar(&branch, "branch", "", "branch the run worked on")
	fs.StringVar(&action, "action", "", "human verdict: accepted | edited | reverted")
	fs.IntVar(&iterations, "iterations", 0, "number of agent iterations")
	fs.Float64Var(&cost, "cost", 0, "run cost in USD")
	fs.IntVar(&additions, "additions", 0, "lines added (folded into diffSummary)")
	fs.IntVar(&deletions, "deletions", 0, "lines deleted (folded into diffSummary)")
	fs.IntVar(&files, "files", 0, "files changed (folded into diffSummary)")
	fs.BoolVar(&testsPassed, "tests-passed", false, "mark that tests passed for this run")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: gittrack log-run --goal \"…\" [--repo ID] [--pr ID] [--issue ID]")
		fmt.Fprintln(os.Stderr, "         [--agent NAME] [--branch B] [--action accepted|edited|reverted]")
		fmt.Fprintln(os.Stderr, "         [--iterations N] [--cost F] [--additions N] [--deletions N] [--files N]")
		fmt.Fprintln(os.Stderr, "         [--tests-passed] [--json]")
		fmt.Fprintln(os.Stderr, "Record an agent run so it feeds attribution + estimation.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return 2
	}
	if strings.TrimSpace(goal) == "" {
		fmt.Fprintln(os.Stderr, "gittrack: --goal is required")
		fs.Usage()
		return 2
	}

	// Build the request body. Only set optional fields the user supplied so the
	// server applies its own defaults for the rest. fs.Visit reports flags the
	// user actually passed on the command line.
	body := map[string]any{"goal": goal}
	if repo != "" {
		body["repoId"] = repo
	}
	if pr != "" {
		body["prId"] = pr
	}
	if issue != "" {
		body["issueId"] = issue
	}
	if agent != "" {
		body["agentName"] = agent
	}
	if branch != "" {
		body["branch"] = branch
	}
	if action != "" {
		body["humanAction"] = action
	}

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if set["iterations"] {
		body["iterations"] = iterations
	}
	if set["cost"] {
		body["costUsd"] = cost
	}
	if set["tests-passed"] {
		body["testsPassed"] = testsPassed
	}
	if set["additions"] || set["deletions"] || set["files"] {
		body["diffSummary"] = map[string]int{
			"additions":    additions,
			"deletions":    deletions,
			"changedFiles": files,
		}
	}

	cl, err := resolveClient(cf.url, cf.token)
	if err != nil {
		return fail(err)
	}

	respBody, err := cl.doJSON("POST", "/api/agent-runs", body)
	if err != nil {
		return fail(err)
	}

	if cf.json {
		printRaw(respBody)
		return 0
	}

	var run agentRun
	if err := json.Unmarshal(respBody, &run); err != nil {
		return fail(fmt.Errorf("decode agent run: %w", err))
	}
	fmt.Fprintf(os.Stdout, "logged agent run %s\n", run.ID)
	return 0
}

// ── gittrack runs [--repo ID] [--pr ID] [--issue ID] [--agent N] [--limit N] ──

func cmdRuns(args []string) int {
	fs := flag.NewFlagSet("runs", flag.ContinueOnError)
	var cf commonFlags
	registerCommon(fs, &cf)
	var repo, pr, issue, agent string
	var limit int
	fs.StringVar(&repo, "repo", "", "filter by repo id")
	fs.StringVar(&pr, "pr", "", "filter by pull request id")
	fs.StringVar(&issue, "issue", "", "filter by issue id")
	fs.StringVar(&agent, "agent", "", "filter by agent name")
	fs.IntVar(&limit, "limit", 0, "maximum number of runs to fetch")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: gittrack runs [--repo ID] [--pr ID] [--issue ID] [--agent N] [--limit N] [--json]")
		fmt.Fprintln(os.Stderr, "List logged agent runs newest-first for the org resolved from the API token.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(reorderArgs(args)); err != nil {
		return 2
	}

	cl, err := resolveClient(cf.url, cf.token)
	if err != nil {
		return fail(err)
	}

	q := url.Values{}
	if repo != "" {
		q.Set("repo", repo)
	}
	if pr != "" {
		q.Set("pr", pr)
	}
	if issue != "" {
		q.Set("issue", issue)
	}
	if agent != "" {
		q.Set("agent", agent)
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	path := "/api/agent-runs"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}

	body, err := cl.do("GET", path)
	if err != nil {
		return fail(err)
	}

	if cf.json {
		printRaw(body)
		return 0
	}

	var runs []agentRun
	if err := json.Unmarshal(body, &runs); err != nil {
		return fail(fmt.Errorf("decode agent runs: %w", err))
	}
	renderRunList(os.Stdout, runs)
	return 0
}
