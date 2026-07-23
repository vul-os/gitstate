// Package importer — unit tests for Jira + Linear field mapping, ADF→text
// flattening, and error mapping. Canned payloads only; no real network.
package importer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// ── Jira status → gitstate state ────────────────────────────────────────────

func TestMapJiraStatus(t *testing.T) {
	cat := func(key string) *jiraStatus {
		return &jiraStatus{Name: "X", StatusCategory: &jiraStatusCateg{Key: key}}
	}
	cases := []struct {
		name string
		in   *jiraStatus
		want string
	}{
		{"nil status defaults open", nil, "open"},
		{"new → open", cat("new"), "open"},
		{"indeterminate → in_progress", cat("indeterminate"), "in_progress"},
		{"done → done", cat("done"), "done"},
		{"unknown category → open", cat("weird"), "open"},
		{"no category → open", &jiraStatus{Name: "Backlog"}, "open"},
		{"cancelled name → closed", &jiraStatus{Name: "Cancelled", StatusCategory: &jiraStatusCateg{Key: "done"}}, "closed"},
		{"won't do → closed", &jiraStatus{Name: "Won't Do"}, "closed"},
		{"wont fix → closed", &jiraStatus{Name: "Wont Fix"}, "closed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mapJiraStatus(c.in); got != c.want {
				t.Errorf("mapJiraStatus = %q, want %q", got, c.want)
			}
		})
	}
}

// ── Jira issue normalization ────────────────────────────────────────────────

func TestNormalizeJiraIssue_Mapping(t *testing.T) {
	ji := jiraIssue{
		Key: "ENG-42",
		Fields: jiraIssueField{
			Summary:     "Fix the widget",
			Description: json.RawMessage(`"plain text body"`),
			Labels:      []string{"backend", "urgent"},
			Status:      &jiraStatus{Name: "In Progress", StatusCategory: &jiraStatusCateg{Key: "indeterminate"}},
			IssueType:   &jiraNamed{Name: "Bug"},
			Project:     &jiraProject{Key: "ENG", Name: "Engineering"},
		},
	}
	ni := normalizeJiraIssue(ji)

	if ni.ExternalID != "ENG-42" {
		t.Errorf("ExternalID = %q, want ENG-42 (key)", ni.ExternalID)
	}
	if ni.Title != "Fix the widget" {
		t.Errorf("Title = %q", ni.Title)
	}
	if ni.Body != "plain text body" {
		t.Errorf("Body = %q", ni.Body)
	}
	if ni.State != "in_progress" {
		t.Errorf("State = %q, want in_progress", ni.State)
	}
	if ni.ProjectKey != "ENG" || ni.ProjectName != "Engineering" {
		t.Errorf("Project = %q/%q", ni.ProjectKey, ni.ProjectName)
	}
	// issue type "bug" is folded into labels (lowercased).
	want := []string{"backend", "urgent", "bug"}
	if !reflect.DeepEqual(ni.Labels, want) {
		t.Errorf("Labels = %v, want %v", ni.Labels, want)
	}
}

func TestNormalizeJiraIssue_TaskTypeNotLabeled(t *testing.T) {
	// "task" is noise and must NOT be added as a label.
	ji := jiraIssue{
		Key: "ENG-1",
		Fields: jiraIssueField{
			Summary:   "thing",
			Labels:    []string{"x"},
			IssueType: &jiraNamed{Name: "Task"},
		},
	}
	ni := normalizeJiraIssue(ji)
	for _, l := range ni.Labels {
		if l == "task" {
			t.Errorf("'task' issue type should not be a label: %v", ni.Labels)
		}
	}
	if !reflect.DeepEqual(ni.Labels, []string{"x"}) {
		t.Errorf("Labels = %v, want [x]", ni.Labels)
	}
}

func TestNormalizeJiraIssue_EmptyTitleFallsBackToKey(t *testing.T) {
	ji := jiraIssue{Key: "ENG-9", Fields: jiraIssueField{Summary: ""}}
	ni := normalizeJiraIssue(ji)
	if ni.Title != "ENG-9" {
		t.Errorf("empty summary should fall back to key, got %q", ni.Title)
	}
	// nil status defaults to open.
	if ni.State != "open" {
		t.Errorf("State = %q, want open", ni.State)
	}
}

func TestNormalizeJiraIssue_NoProjectNoType(t *testing.T) {
	ji := jiraIssue{Key: "X-1", Fields: jiraIssueField{Summary: "t"}}
	ni := normalizeJiraIssue(ji)
	if ni.ProjectKey != "" || ni.ProjectName != "" {
		t.Errorf("expected empty project, got %q/%q", ni.ProjectKey, ni.ProjectName)
	}
	// Labels start empty (non-nil slice from the append-copy).
	if len(ni.Labels) != 0 {
		t.Errorf("expected no labels, got %v", ni.Labels)
	}
}

// ── ADF → text flattening ───────────────────────────────────────────────────

func TestADFToText(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"null", `null`, ""},
		{"empty", ``, ""},
		{"plain string", `"hello"`, "hello"},
		{
			name: "single paragraph",
			raw:  `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Hello world"}]}]}`,
			want: "Hello world",
		},
		{
			name: "two paragraphs joined by newline",
			raw: `{"type":"doc","content":[
				{"type":"paragraph","content":[{"type":"text","text":"First."}]},
				{"type":"paragraph","content":[{"type":"text","text":"Second."}]}
			]}`,
			want: "First.\nSecond.",
		},
		{
			name: "heading then paragraph",
			raw: `{"type":"doc","content":[
				{"type":"heading","content":[{"type":"text","text":"Title"}]},
				{"type":"paragraph","content":[{"type":"text","text":"Body"}]}
			]}`,
			want: "Title\nBody",
		},
		{
			name: "nested marks concatenated",
			raw:  `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"a"},{"type":"text","text":"b"},{"type":"text","text":"c"}]}]}`,
			want: "abc",
		},
		{"malformed json → empty", `{not json`, ""},
		{"empty doc → empty", `{"type":"doc","content":[]}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := adfToText(json.RawMessage(c.raw))
			if got != c.want {
				t.Errorf("adfToText(%s) = %q, want %q", c.raw, got, c.want)
			}
		})
	}
}

// ── Linear state → gitstate state ───────────────────────────────────────────

func TestMapLinearState(t *testing.T) {
	st := func(typ string) *struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} {
		return &struct {
			Name string `json:"name"`
			Type string `json:"type"`
		}{Name: "n", Type: typ}
	}
	cases := []struct {
		typ  string
		want string
	}{
		{"backlog", "open"},
		{"unstarted", "open"},
		{"started", "in_progress"},
		{"completed", "done"},
		{"canceled", "closed"},
		{"cancelled", "closed"}, // British spelling tolerated
		{"mystery", "open"},     // unknown → open
	}
	for _, c := range cases {
		if got := mapLinearState(st(c.typ)); got != c.want {
			t.Errorf("mapLinearState(%q) = %q, want %q", c.typ, got, c.want)
		}
	}
	if got := mapLinearState(nil); got != "open" {
		t.Errorf("nil state → %q, want open", got)
	}
}

// ── Linear issue normalization ──────────────────────────────────────────────

func TestNormalizeLinearIssue_Mapping(t *testing.T) {
	li := linearIssue{
		ID:          "uuid-1",
		Identifier:  "ENG-7",
		Title:       "Ship feature",
		Description: "details here",
		State: &struct {
			Name string `json:"name"`
			Type string `json:"type"`
		}{Name: "In Progress", Type: "started"},
		Project: &struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}{ID: "proj-1", Name: "Core"},
		Labels: &struct {
			Nodes []struct {
				Name string `json:"name"`
			} `json:"nodes"`
		}{Nodes: []struct {
			Name string `json:"name"`
		}{{Name: "Backend"}, {Name: "P1"}, {Name: ""}}},
	}
	ni := normalizeLinearIssue(li)

	if ni.ExternalID != "ENG-7" {
		t.Errorf("ExternalID = %q, want ENG-7 (identifier preferred)", ni.ExternalID)
	}
	if ni.Title != "Ship feature" || ni.Body != "details here" {
		t.Errorf("title/body wrong: %q / %q", ni.Title, ni.Body)
	}
	if ni.State != "in_progress" {
		t.Errorf("State = %q", ni.State)
	}
	if ni.ProjectKey != "proj-1" || ni.ProjectName != "Core" {
		t.Errorf("Project = %q/%q", ni.ProjectKey, ni.ProjectName)
	}
	// Labels lowercased; empty names skipped.
	want := []string{"backend", "p1"}
	if !reflect.DeepEqual(ni.Labels, want) {
		t.Errorf("Labels = %v, want %v", ni.Labels, want)
	}
}

func TestNormalizeLinearIssue_FallbacksToID(t *testing.T) {
	// Missing identifier → use ID; missing title → use external id.
	li := linearIssue{ID: "uuid-x", Identifier: "", Title: ""}
	ni := normalizeLinearIssue(li)
	if ni.ExternalID != "uuid-x" {
		t.Errorf("ExternalID = %q, want uuid-x", ni.ExternalID)
	}
	if ni.Title != "uuid-x" {
		t.Errorf("Title = %q, want uuid-x", ni.Title)
	}
	if ni.State != "open" {
		t.Errorf("nil state → %q, want open", ni.State)
	}
}

// ── Error type ──────────────────────────────────────────────────────────────

func TestError_HTTPStatus(t *testing.T) {
	if (&Error{Code: 0}).HTTPStatus() != 502 {
		t.Error("zero code should default to 502")
	}
	if (&Error{Code: 401}).HTTPStatus() != 401 {
		t.Error("explicit code should be preserved")
	}
	e := &Error{Code: 400, Msg: "bad jql"}
	if e.Error() != "bad jql" {
		t.Errorf("Error() = %q", e.Error())
	}
}

// ── Client constructors: credential validation ──────────────────────────────

func TestNewJiraClient_Validation(t *testing.T) {
	cases := []struct {
		name    string
		creds   JiraCredentials
		wantErr bool
	}{
		{"valid", JiraCredentials{BaseURL: "https://acme.atlassian.net", Email: "a@b.com", APIToken: "tok"}, false},
		{"trailing slash trimmed", JiraCredentials{BaseURL: "https://acme.atlassian.net/", Email: "a@b.com", APIToken: "tok"}, false},
		{"empty baseURL", JiraCredentials{Email: "a@b.com", APIToken: "tok"}, true},
		{"non-http baseURL", JiraCredentials{BaseURL: "acme.atlassian.net", Email: "a@b.com", APIToken: "tok"}, true},
		{"missing email", JiraCredentials{BaseURL: "https://x", APIToken: "tok"}, true},
		{"missing token", JiraCredentials{BaseURL: "https://x", Email: "a@b.com"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cl, err := NewJiraClient(c.creds)
			if c.wantErr {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.HasSuffix(cl.baseURL, "/") {
				t.Errorf("baseURL not trimmed: %q", cl.baseURL)
			}
		})
	}
}

func TestNewLinearClient_Validation(t *testing.T) {
	if _, err := NewLinearClient(LinearCredentials{APIKey: ""}); err == nil {
		t.Error("empty apiKey should error")
	}
	cl, err := NewLinearClient(LinearCredentials{APIKey: "  key  ", TeamID: " t "})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if cl.apiKey != "key" || cl.teamID != "t" {
		t.Errorf("creds not trimmed: %q / %q", cl.apiKey, cl.teamID)
	}
}

// ── Jira Fetch + Count over httptest (error mapping + pagination) ────────────

func newJiraTestClient(t *testing.T, ts *httptest.Server) *JiraClient {
	t.Helper()
	cl, err := NewJiraClient(JiraCredentials{BaseURL: ts.URL, Email: "a@b.com", APIToken: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	return cl
}

func TestJiraFetch_HappyPathAndProjects(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := jiraSearchResponse{
			StartAt: 0, MaxResults: 100, Total: 2,
			Issues: []jiraIssue{
				{Key: "ENG-1", Fields: jiraIssueField{Summary: "one", Project: &jiraProject{Key: "ENG", Name: "Eng"}, Status: &jiraStatus{StatusCategory: &jiraStatusCateg{Key: "done"}}}},
				{Key: "ENG-2", Fields: jiraIssueField{Summary: "two", Project: &jiraProject{Key: "ENG", Name: "Eng"}}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	cl := newJiraTestClient(t, ts)
	data, err := cl.Fetch(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(data.Issues) != 2 {
		t.Fatalf("got %d issues, want 2", len(data.Issues))
	}
	if data.Issues[0].State != "done" {
		t.Errorf("issue 0 state = %q, want done", data.Issues[0].State)
	}
	// Distinct project deduped to one ProjectRef.
	if len(data.Projects) != 1 || data.Projects[0].Key != "ENG" {
		t.Errorf("Projects = %+v, want one ENG", data.Projects)
	}
}

func TestJiraFetch_LimitStops(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always claims a big total but only this page is returned.
		json.NewEncoder(w).Encode(jiraSearchResponse{
			StartAt: 0, Total: 1000,
			Issues: []jiraIssue{{Key: "A-1", Fields: jiraIssueField{Summary: "x"}}},
		})
	}))
	defer ts.Close()
	cl := newJiraTestClient(t, ts)
	data, err := cl.Fetch(context.Background(), "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Issues) != 1 {
		t.Errorf("limit=1 returned %d issues", len(data.Issues))
	}
}

func TestJiraSearch_ErrorMapping(t *testing.T) {
	cases := []struct {
		status   int
		wantCode int
	}{
		{http.StatusUnauthorized, http.StatusUnauthorized},
		{http.StatusForbidden, http.StatusUnauthorized},
		{http.StatusBadRequest, http.StatusBadRequest},
		{http.StatusInternalServerError, http.StatusBadGateway},
	}
	for _, c := range cases {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
		}))
		cl := newJiraTestClient(t, ts)
		_, err := cl.Count(context.Background(), "")
		ts.Close()
		if err == nil {
			t.Errorf("status %d: expected error", c.status)
			continue
		}
		ie, ok := err.(*Error)
		if !ok {
			t.Errorf("status %d: error type = %T, want *Error", c.status, err)
			continue
		}
		if ie.Code != c.wantCode {
			t.Errorf("status %d → code %d, want %d", c.status, ie.Code, c.wantCode)
		}
	}
}

func TestJiraCount(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jiraSearchResponse{Total: 137})
	}))
	defer ts.Close()
	cl := newJiraTestClient(t, ts)
	n, err := cl.Count(context.Background(), "project = ENG")
	if err != nil {
		t.Fatal(err)
	}
	if n != 137 {
		t.Errorf("Count = %d, want 137", n)
	}
}

// ── Linear Fetch over httptest (GraphQL errors + auth + happy path) ─────────

func newLinearTestClient(t *testing.T, ts *httptest.Server) *LinearClient {
	t.Helper()
	cl, err := NewLinearClient(LinearCredentials{APIKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	// Redirect every request to the test server.
	cl.http.Transport = redirectTransport{base: ts.URL}
	return cl
}

type redirectTransport struct{ base string }

func (r redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ts, _ := http.NewRequest(http.MethodGet, r.base, nil)
	u := *req.URL
	u.Scheme = ts.URL.Scheme
	u.Host = ts.URL.Host
	req.URL = &u
	return http.DefaultTransport.RoundTrip(req)
}

func TestLinearFetch_HappyPath(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"issues":{
			"pageInfo":{"hasNextPage":false,"endCursor":""},
			"nodes":[
				{"id":"u1","identifier":"ENG-1","title":"a","state":{"name":"Done","type":"completed"},"project":{"id":"p1","name":"Proj"}},
				{"id":"u2","identifier":"ENG-2","title":"b","state":{"name":"Todo","type":"backlog"}}
			]
		}}}`))
	}))
	defer ts.Close()
	cl := newLinearTestClient(t, ts)
	data, err := cl.Fetch(context.Background(), 0)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(data.Issues) != 2 {
		t.Fatalf("got %d issues", len(data.Issues))
	}
	if data.Issues[0].State != "done" || data.Issues[1].State != "open" {
		t.Errorf("states = %q,%q", data.Issues[0].State, data.Issues[1].State)
	}
	if len(data.Projects) != 1 || data.Projects[0].Key != "p1" {
		t.Errorf("Projects = %+v", data.Projects)
	}
}

func TestLinearFetch_GraphQLErrorMapped(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 200 OK but errors[] present → mapped to 400 with the message.
		w.Write([]byte(`{"errors":[{"message":"invalid team id"}]}`))
	}))
	defer ts.Close()
	cl := newLinearTestClient(t, ts)
	_, err := cl.Fetch(context.Background(), 0)
	if err == nil {
		t.Fatal("expected error")
	}
	ie, ok := err.(*Error)
	if !ok || ie.Code != http.StatusBadRequest {
		t.Errorf("error = %#v, want *Error code 400", err)
	}
	if !strings.Contains(ie.Msg, "invalid team id") {
		t.Errorf("message should carry GraphQL error: %q", ie.Msg)
	}
}

func TestLinearFetch_AuthError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()
	cl := newLinearTestClient(t, ts)
	_, err := cl.Fetch(context.Background(), 0)
	ie, ok := err.(*Error)
	if !ok || ie.Code != http.StatusUnauthorized {
		t.Errorf("error = %#v, want *Error code 401", err)
	}
}

func TestLinearFetch_EmptyData(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"issues":null}}`))
	}))
	defer ts.Close()
	cl := newLinearTestClient(t, ts)
	_, err := cl.Fetch(context.Background(), 0)
	if err == nil {
		t.Error("expected error on empty issues data")
	}
}
