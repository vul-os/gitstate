package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestClientRequestBuilding verifies that do() sets the Authorization header,
// joins the base URL + path correctly, and surfaces the body for 2xx.
func TestClientRequestBuilding(t *testing.T) {
	var gotAuth, gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	cl := newClient(srv.URL+"/", "gsk_secret123") // trailing slash must be trimmed
	body, err := cl.do("GET", "/api/issues?state=open")
	if err != nil {
		t.Fatalf("do: %v", err)
	}

	if gotAuth != "Bearer gsk_secret123" {
		t.Errorf("Authorization = %q, want Bearer gsk_secret123", gotAuth)
	}
	if gotPath != "/api/issues" {
		t.Errorf("path = %q, want /api/issues", gotPath)
	}
	if gotQuery != "state=open" {
		t.Errorf("query = %q, want state=open", gotQuery)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("body = %q", body)
	}
}

// TestClientErrorBody verifies non-2xx responses surface the server's error
// body via *apiError.
func TestClientErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid token"}`))
	}))
	defer srv.Close()

	cl := newClient(srv.URL, "gsk_bad")
	_, err := cl.do("GET", "/api/issues")
	if err == nil {
		t.Fatal("expected error for 401")
	}
	ae, ok := err.(*apiError)
	if !ok {
		t.Fatalf("error type = %T, want *apiError", err)
	}
	if ae.status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", ae.status)
	}
	if !strings.Contains(err.Error(), "invalid token") {
		t.Errorf("error %q should include server body", err.Error())
	}
}

// TestResolveClientMissingToken ensures a missing token is a hard error and the
// token is never echoed in the message.
func TestResolveClientMissingToken(t *testing.T) {
	t.Setenv(envToken, "")
	t.Setenv(envURL, "")
	_, err := resolveClient("", "")
	if err == nil {
		t.Fatal("expected error when no token set")
	}
	if !strings.Contains(err.Error(), envToken) {
		t.Errorf("error should mention %s, got %q", envToken, err.Error())
	}
}

// TestResolveClientPrecedence checks flag > env > default resolution for URL.
func TestResolveClientPrecedence(t *testing.T) {
	t.Setenv(envURL, "http://env-host:9000")
	t.Setenv(envToken, "gsk_env")

	// Flag overrides env.
	cl, err := resolveClient("http://flag-host:1", "gsk_flag")
	if err != nil {
		t.Fatal(err)
	}
	if cl.baseURL != "http://flag-host:1" {
		t.Errorf("baseURL = %q, want flag value", cl.baseURL)
	}
	if cl.token != "gsk_flag" {
		t.Errorf("token = %q, want flag value", cl.token)
	}

	// Env used when no flag.
	cl2, err := resolveClient("", "")
	if err != nil {
		t.Fatal(err)
	}
	if cl2.baseURL != "http://env-host:9000" || cl2.token != "gsk_env" {
		t.Errorf("env resolution wrong: url=%q token=%q", cl2.baseURL, cl2.token)
	}
}

// TestMaskTokenNeverLeaks ensures the secret body is never fully printed.
func TestMaskTokenNeverLeaks(t *testing.T) {
	tok := "gsk_abcdefghijklmnop"
	masked := maskToken(tok)
	if masked == tok {
		t.Fatal("mask returned the full token")
	}
	if strings.Contains(masked, "efghijklmnop") {
		t.Errorf("mask %q leaks secret body", masked)
	}
	if !strings.HasPrefix(masked, "gsk_") {
		t.Errorf("mask %q should keep the gsk_ prefix", masked)
	}
	if got := maskToken(""); got != "(none)" {
		t.Errorf("empty token mask = %q, want (none)", got)
	}
}

// TestRunDispatch covers top-level arg parsing exit codes.
func TestRunDispatch(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"no args", nil, 2},
		{"help", []string{"help"}, 0},
		{"help flag", []string{"-h"}, 0},
		{"unknown", []string{"frobnicate"}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := run(tc.args); got != tc.want {
				t.Errorf("run(%v) = %d, want %d", tc.args, got, tc.want)
			}
		})
	}
}

// TestCmdContextEndpointAndSummary drives cmdContext against an httptest server
// and verifies (a) it hits the right URL with auth, (b) summary output mode.
func TestCmdContextEndpointAndSummary(t *testing.T) {
	var gotPath, gotAuth string
	bundle := issueContext{
		Issue: issue{Number: 42, Title: "Login bug", State: "open", Labels: []string{"bug"}},
		RelatedPRs: []relatedPR{
			{Number: 7, Title: "Fix login", State: "merged", Merged: true, LeadTimeSecs: 90000},
		},
		Commits:      []relatedCommit{{SHA: "abcdef1234567890", Subject: "patch auth"}},
		TouchedPaths: []string{"internal/auth/login.go"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(bundle)
	}))
	defer srv.Close()

	out := captureStdout(t, func() int {
		return cmdContext([]string{"42", "--url", srv.URL, "--token", "gsk_t"})
	})

	if gotPath != "/api/context/issue/42" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer gsk_t" {
		t.Errorf("auth = %q", gotAuth)
	}
	for _, want := range []string{"#42", "Login bug", "Related PRs", "#7", "internal/auth/login.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q in:\n%s", want, out)
		}
	}
	// Summary mode should NOT be raw JSON.
	if strings.Contains(out, `"title":`) {
		t.Errorf("summary output looks like raw JSON:\n%s", out)
	}
}

// TestCmdContextJSON verifies --json streams the server payload (raw fields).
func TestCmdContextJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"issue":{"number":9,"title":"raw"},"extraField":"kept"}`))
	}))
	defer srv.Close()

	out := captureStdout(t, func() int {
		return cmdContext([]string{"9", "--json", "--url", srv.URL, "--token", "gsk_t"})
	})

	if !strings.Contains(out, `"extraField"`) {
		t.Errorf("--json should preserve unknown server fields:\n%s", out)
	}
	if !strings.Contains(out, `"title": "raw"`) {
		t.Errorf("--json should pretty-print server payload:\n%s", out)
	}
}

// TestCmdIssuesLimitAndState verifies --state is sent as a query param and
// --limit caps the table.
func TestCmdIssuesLimitAndState(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		json.NewEncoder(w).Encode([]issue{
			{Number: 1, Title: "one", State: "open"},
			{Number: 2, Title: "two", State: "open"},
			{Number: 3, Title: "three", State: "open"},
		})
	}))
	defer srv.Close()

	out := captureStdout(t, func() int {
		return cmdIssues([]string{"--state", "open", "--limit", "2", "--url", srv.URL, "--token", "gsk_t"})
	})

	if gotQuery != "state=open" {
		t.Errorf("query = %q, want state=open", gotQuery)
	}
	if !strings.Contains(out, "#1") || !strings.Contains(out, "#2") {
		t.Errorf("expected first two issues:\n%s", out)
	}
	if strings.Contains(out, "#3") {
		t.Errorf("--limit 2 should drop #3:\n%s", out)
	}
}

// TestCmdWhoami exercises the validation path and confirms the token body is
// never printed.
func TestCmdWhoami(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	out := captureStdout(t, func() int {
		return cmdWhoami([]string{"--url", srv.URL, "--token", "gsk_supersecretbody"})
	})

	if !strings.Contains(out, "OK") {
		t.Errorf("whoami should report OK:\n%s", out)
	}
	if strings.Contains(out, "supersecretbody") {
		t.Errorf("whoami leaked the token body:\n%s", out)
	}
}

// TestCmdPRSummaryDecodesServerShape drives cmdPR against the EXACT wire shape
// store.PRContextBundle emits: diffSummary is a nested object (not a string) and
// the effort estimate lives under "estimate". A regression to the old type
// (DiffSummary string / PredictedSecs int64) makes json.Unmarshal fail with
// "cannot unmarshal object into Go struct field prContext.diffSummary of type
// string", which this test guards against.
func TestCmdPRSummaryDecodesServerShape(t *testing.T) {
	const body = `{
	  "pr": {"id":"pr-1","number":1001,"title":"chore(ci): cache build","state":"merged","merged":true,"authorLogin":"mlee"},
	  "diffSummary": {"additions":492,"deletions":101,"changedFiles":8},
	  "cycleTimeSecs": 30780,
	  "estimate": {"predictedSecs":22903.3,"sizeBucket":"m","changeType":"fix"}
	}`
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(body))
	}))
	defer srv.Close()

	out := captureStdout(t, func() int {
		if code := cmdPR([]string{"pr-1", "--url", srv.URL, "--token", "gsk_t"}); code != 0 {
			t.Errorf("cmdPR exit = %d, want 0 (decode must not fail)", code)
		}
		return 0
	})

	if gotPath != "/api/context/pr/pr-1" {
		t.Errorf("path = %q", gotPath)
	}
	for _, want := range []string{"#1001", "merged", "+492/-101 across 8 files", "estimated effort"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "cannot unmarshal") {
		t.Errorf("summary shows a decode error:\n%s", out)
	}
}

// TestCmdLogRunRequestBuilding verifies cmdLogRun POSTs to /api/agent-runs with
// the auth header and folds flags into the JSON body — including diffSummary —
// while leaving un-set optional fields out so server defaults apply.
func TestCmdLogRunRequestBuilding(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotCT string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"run-123"}`))
	}))
	defer srv.Close()

	out := captureStdout(t, func() int {
		return cmdLogRun([]string{
			"--goal", "fix the bug",
			"--agent", "claude-code",
			"--pr", "pr-9",
			"--action", "accepted",
			"--tests-passed",
			"--iterations", "3",
			"--cost", "0.42",
			"--additions", "12", "--deletions", "3", "--files", "2",
			"--url", srv.URL, "--token", "gsk_t",
		})
	})

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/agent-runs" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer gsk_t" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotCT != "application/json" {
		t.Errorf("content-type = %q", gotCT)
	}
	if gotBody["goal"] != "fix the bug" || gotBody["agentName"] != "claude-code" {
		t.Errorf("body goal/agent wrong: %v", gotBody)
	}
	if gotBody["prId"] != "pr-9" || gotBody["humanAction"] != "accepted" {
		t.Errorf("body pr/action wrong: %v", gotBody)
	}
	if gotBody["testsPassed"] != true {
		t.Errorf("body testsPassed = %v, want true", gotBody["testsPassed"])
	}
	// JSON numbers decode to float64.
	if gotBody["iterations"] != float64(3) {
		t.Errorf("body iterations = %v, want 3", gotBody["iterations"])
	}
	if gotBody["costUsd"] != 0.42 {
		t.Errorf("body costUsd = %v, want 0.42", gotBody["costUsd"])
	}
	ds, ok := gotBody["diffSummary"].(map[string]any)
	if !ok {
		t.Fatalf("diffSummary missing/wrong type: %v", gotBody["diffSummary"])
	}
	if ds["additions"] != float64(12) || ds["deletions"] != float64(3) || ds["changedFiles"] != float64(2) {
		t.Errorf("diffSummary wrong: %v", ds)
	}
	if !strings.Contains(out, "run-123") {
		t.Errorf("expected created run id in output:\n%s", out)
	}
}

// TestCmdLogRunOmitsUnsetFields ensures optional metric flags the user did NOT
// pass are absent from the body (so the server applies its defaults), while the
// default --agent value is still sent.
func TestCmdLogRunOmitsUnsetFields(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"r1"}`))
	}))
	defer srv.Close()

	_ = captureStdout(t, func() int {
		return cmdLogRun([]string{"--goal", "minimal", "--url", srv.URL, "--token", "gsk_t"})
	})

	for _, k := range []string{"iterations", "costUsd", "testsPassed", "diffSummary", "prId", "issueId", "repoId"} {
		if _, present := gotBody[k]; present {
			t.Errorf("body should omit unset %q, got %v", k, gotBody[k])
		}
	}
	if gotBody["agentName"] != "gittrack" {
		t.Errorf("default agent = %v, want gittrack", gotBody["agentName"])
	}
	if gotBody["goal"] != "minimal" {
		t.Errorf("goal = %v", gotBody["goal"])
	}
}

// TestCmdLogRunRequiresGoal ensures a missing --goal is a usage error (exit 2)
// and no request is sent.
func TestCmdLogRunRequiresGoal(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	code := cmdLogRun([]string{"--agent", "x", "--url", srv.URL, "--token", "gsk_t"})
	if code != 2 {
		t.Errorf("missing goal exit = %d, want 2", code)
	}
	if called {
		t.Error("no request should be sent when --goal is missing")
	}
}

// TestCmdRunsFilters verifies cmdRuns sends repo/agent/limit as query params.
func TestCmdRunsFilters(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		json.NewEncoder(w).Encode([]agentRun{
			{ID: "run-aaaaaaaaaaaa", Goal: "do a thing", AgentName: "cursor", HumanAction: "edited"},
		})
	}))
	defer srv.Close()

	out := captureStdout(t, func() int {
		return cmdRuns([]string{"--agent", "cursor", "--limit", "5", "--url", srv.URL, "--token", "gsk_t"})
	})

	if gotPath != "/api/agent-runs" {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.Contains(gotQuery, "agent=cursor") || !strings.Contains(gotQuery, "limit=5") {
		t.Errorf("query = %q, want agent + limit", gotQuery)
	}
	for _, want := range []string{"run-aaaaaaaa", "cursor", "edited", "do a thing"} {
		if !strings.Contains(out, want) {
			t.Errorf("runs table missing %q in:\n%s", want, out)
		}
	}
}

// captureStdout redirects os.Stdout for the duration of fn and returns what was
// written. It restores the original on return.
func captureStdout(t *testing.T, fn func() int) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		buf.ReadFrom(r)
		done <- buf.String()
	}()

	fn()
	w.Close()
	return <-done
}
