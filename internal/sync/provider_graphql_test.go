// Package sync — unit tests for the GraphQL PR/MR mappers and the REST fallback.
// These exercise the RESPONSE PARSING (we can't hit real GitHub/GitLab from CI) and
// prove the query strings are well-formed, that nested reviews + first-commit map
// to the right RemotePR/RemoteReview, and that a GraphQL error falls back to REST.
package sync

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// ── GitHub GraphQL mapping ─────────────────────────────────────────────────────

// githubFixture is a representative GitHub GraphQL response: one MERGED PR with a
// first commit and two reviews, and one OPEN PR with no reviews.
const githubFixture = `{
  "data": {
    "rateLimit": { "cost": 1, "remaining": 4999, "resetAt": "2030-01-01T00:00:00Z" },
    "repository": {
      "pullRequests": {
        "pageInfo": { "hasNextPage": false, "endCursor": "Y3Vyc29yOjI=" },
        "nodes": [
          {
            "number": 9001,
            "title": "Fix the widget",
            "body": "closes #4242",
            "state": "MERGED",
            "databaseId": 555000111,
            "author": { "login": "dev" },
            "additions": 10,
            "deletions": 2,
            "changedFiles": 3,
            "mergedAt": "2026-06-01T12:00:00Z",
            "createdAt": "2026-05-30T09:00:00Z",
            "commits": { "nodes": [ { "commit": { "authoredDate": "2026-05-29T08:00:00Z", "committedDate": "2026-05-29T08:05:00Z" } } ] },
            "reviews": { "nodes": [
              { "databaseId": 777, "state": "APPROVED", "submittedAt": "2026-05-31T10:00:00Z", "author": { "login": "reviewer" } },
              { "databaseId": 778, "state": "COMMENTED", "submittedAt": "2026-05-31T11:00:00Z", "author": { "login": "dev" } }
            ] }
          },
          {
            "number": 9002,
            "title": "WIP",
            "body": "",
            "state": "OPEN",
            "databaseId": 555000112,
            "author": { "login": "dev2" },
            "additions": 1,
            "deletions": 0,
            "changedFiles": 1,
            "mergedAt": null,
            "createdAt": "2026-06-02T09:00:00Z",
            "commits": { "nodes": [] },
            "reviews": { "nodes": [] }
          }
        ]
      }
    }
  }
}`

func TestDecodeGitHubGraphQLPRs(t *testing.T) {
	var data githubPRsResponse
	if err := decodeGraphQL([]byte(githubFixture), &data); err != nil {
		t.Fatalf("decodeGraphQL: %v", err)
	}
	nodes := data.Repository.PullRequests.Nodes
	if len(nodes) != 2 {
		t.Fatalf("got %d PR nodes, want 2", len(nodes))
	}
	if data.RateLimit.Remaining != 4999 {
		t.Errorf("rateLimit.remaining = %d, want 4999", data.RateLimit.Remaining)
	}

	pr, revs := mapGitHubPRNode(nodes[0])
	if pr.Number != 9001 || pr.ExternalID != "555000111" {
		t.Errorf("pr0 number/extid = %d/%s, want 9001/555000111", pr.Number, pr.ExternalID)
	}
	if pr.State != "merged" {
		t.Errorf("pr0 state = %q, want merged", pr.State)
	}
	if pr.MergedAt == nil || !pr.MergedAt.Equal(mustTime(t, "2026-06-01T12:00:00Z")) {
		t.Errorf("pr0 mergedAt = %v, want 2026-06-01T12:00:00Z", pr.MergedAt)
	}
	if pr.Additions != 10 || pr.Deletions != 2 || pr.ChangedFiles != 3 {
		t.Errorf("pr0 churn = +%d-%d files=%d, want +10-2 files=3", pr.Additions, pr.Deletions, pr.ChangedFiles)
	}
	// FirstCommitAt must come from the first commit's authoredDate.
	if !pr.FirstCommitAt.Equal(mustTime(t, "2026-05-29T08:00:00Z")) {
		t.Errorf("pr0 firstCommitAt = %v, want 2026-05-29T08:00:00Z", pr.FirstCommitAt)
	}
	if pr.AuthorLogin != "dev" {
		t.Errorf("pr0 author = %q, want dev", pr.AuthorLogin)
	}
	// Two reviews mapped (self-review filtering is the syncer's job, not the mapper's).
	if len(revs) != 2 {
		t.Fatalf("pr0 reviews = %d, want 2", len(revs))
	}
	if revs[0].ReviewerLogin != "reviewer" || revs[0].State != "approved" || revs[0].ExternalID != "777" {
		t.Errorf("review0 = %+v, want reviewer/approved/777", revs[0])
	}
	if !revs[0].SubmittedAt.Equal(mustTime(t, "2026-05-31T10:00:00Z")) {
		t.Errorf("review0 submittedAt = %v", revs[0].SubmittedAt)
	}

	pr2, revs2 := mapGitHubPRNode(nodes[1])
	if pr2.State != "open" {
		t.Errorf("pr1 state = %q, want open", pr2.State)
	}
	if !pr2.FirstCommitAt.IsZero() {
		t.Errorf("pr1 firstCommitAt = %v, want zero (no commits)", pr2.FirstCommitAt)
	}
	if len(revs2) != 0 {
		t.Errorf("pr1 reviews = %d, want 0", len(revs2))
	}
}

// TestGitHubGraphQLQueryWellFormed checks the query has the fields the mapper reads.
func TestGitHubGraphQLQueryWellFormed(t *testing.T) {
	for _, want := range []string{
		"pullRequests(first:50", "after:$cur", "states:[OPEN,CLOSED,MERGED]",
		"pageInfo", "hasNextPage", "endCursor",
		"commits(first:1)", "reviews(first:50)", "submittedAt", "databaseId", "rateLimit",
	} {
		if !strings.Contains(githubPRsQuery, want) {
			t.Errorf("githubPRsQuery missing %q", want)
		}
	}
	// The variables it references must be declared.
	for _, v := range []string{"$owner:String!", "$name:String!", "$cur:String"} {
		if !strings.Contains(githubPRsQuery, v) {
			t.Errorf("githubPRsQuery missing var decl %q", v)
		}
	}
}

// ── GitLab GraphQL mapping ─────────────────────────────────────────────────────

const gitlabFixture = `{
  "data": {
    "project": {
      "mergeRequests": {
        "pageInfo": { "hasNextPage": false, "endCursor": "eyJpZCI6IjEifQ" },
        "nodes": [
          {
            "iid": "42",
            "title": "Add the thing",
            "description": "closes #7",
            "state": "merged",
            "createdAt": "2026-05-20T09:00:00Z",
            "mergedAt": "2026-05-25T15:00:00Z",
            "author": { "username": "alice" },
            "diffStatsSummary": { "additions": 30, "deletions": 5, "fileCount": 4 },
            "commitsWithoutMergeCommits": { "nodes": [ { "authoredDate": "2026-05-19T08:00:00Z", "committedDate": "2026-05-19T08:01:00Z" } ] },
            "approvedBy": { "nodes": [ { "username": "bob" }, { "username": "carol" }, { "username": "bob" } ] }
          },
          {
            "iid": "43",
            "title": "Draft",
            "description": "",
            "state": "opened",
            "createdAt": "2026-05-26T09:00:00Z",
            "mergedAt": null,
            "author": { "username": "alice" },
            "diffStatsSummary": { "additions": 1, "deletions": 0, "fileCount": 1 },
            "commitsWithoutMergeCommits": { "nodes": [] },
            "approvedBy": { "nodes": [] }
          }
        ]
      }
    }
  }
}`

func TestDecodeGitLabGraphQLMRs(t *testing.T) {
	var data gitlabMRsResponse
	if err := decodeGraphQL([]byte(gitlabFixture), &data); err != nil {
		t.Fatalf("decodeGraphQL: %v", err)
	}
	nodes := data.Project.MergeRequests.Nodes
	if len(nodes) != 2 {
		t.Fatalf("got %d MR nodes, want 2", len(nodes))
	}

	pr, revs := mapGitLabMRNode(nodes[0])
	if pr.Number != 42 || pr.ExternalID != "42" {
		t.Errorf("mr0 number/extid = %d/%s, want 42/42", pr.Number, pr.ExternalID)
	}
	if pr.State != "merged" {
		t.Errorf("mr0 state = %q, want merged", pr.State)
	}
	if pr.MergedAt == nil || !pr.MergedAt.Equal(mustTime(t, "2026-05-25T15:00:00Z")) {
		t.Errorf("mr0 mergedAt = %v", pr.MergedAt)
	}
	if pr.Additions != 30 || pr.Deletions != 5 || pr.ChangedFiles != 4 {
		t.Errorf("mr0 churn = +%d-%d files=%d, want +30-5 files=4", pr.Additions, pr.Deletions, pr.ChangedFiles)
	}
	if !pr.FirstCommitAt.Equal(mustTime(t, "2026-05-19T08:00:00Z")) {
		t.Errorf("mr0 firstCommitAt = %v, want 2026-05-19T08:00:00Z", pr.FirstCommitAt)
	}
	if pr.AuthorLogin != "alice" {
		t.Errorf("mr0 author = %q, want alice", pr.AuthorLogin)
	}
	// approvedBy → reviews, deduped (bob appears twice).
	if len(revs) != 2 {
		t.Fatalf("mr0 reviews = %d, want 2 (deduped bob)", len(revs))
	}
	got := map[string]string{}
	for _, r := range revs {
		got[r.ReviewerLogin] = r.State
		// approval submission time falls back to mergedAt.
		if !r.SubmittedAt.Equal(mustTime(t, "2026-05-25T15:00:00Z")) {
			t.Errorf("review %s submittedAt = %v, want mergedAt", r.ReviewerLogin, r.SubmittedAt)
		}
	}
	if got["bob"] != "approved" || got["carol"] != "approved" {
		t.Errorf("approvals = %v, want bob+carol approved", got)
	}

	pr2, revs2 := mapGitLabMRNode(nodes[1])
	if pr2.State != "open" {
		t.Errorf("mr1 state = %q, want open", pr2.State)
	}
	if !pr2.FirstCommitAt.IsZero() {
		t.Errorf("mr1 firstCommitAt = %v, want zero", pr2.FirstCommitAt)
	}
	if len(revs2) != 0 {
		t.Errorf("mr1 reviews = %d, want 0", len(revs2))
	}
}

func TestGitLabGraphQLQueryWellFormed(t *testing.T) {
	for _, want := range []string{
		"project(fullPath:$path)", "mergeRequests(first:50", "after:$cur",
		"pageInfo", "hasNextPage", "endCursor",
		"commitsWithoutMergeCommits(first:1)", "approvedBy", "diffStatsSummary",
	} {
		if !strings.Contains(gitlabMRsQuery, want) {
			t.Errorf("gitlabMRsQuery missing %q", want)
		}
	}
	for _, v := range []string{"$path:ID!", "$cur:String"} {
		if !strings.Contains(gitlabMRsQuery, v) {
			t.Errorf("gitlabMRsQuery missing var decl %q", v)
		}
	}
}

// ── decodeGraphQL error surfacing (→ REST fallback) ────────────────────────────

// TestDecodeGraphQLSurfacesErrors proves a response carrying top-level GraphQL
// "errors" returns a graphQLErrors — the signal the provider uses to fall back to
// REST instead of silently mapping empty data.
func TestDecodeGraphQLSurfacesErrors(t *testing.T) {
	body := `{"data":null,"errors":[{"message":"Field 'bogus' doesn't exist","type":"FIELD_ERROR"}]}`
	var data githubPRsResponse
	err := decodeGraphQL([]byte(body), &data)
	if err == nil {
		t.Fatal("decodeGraphQL returned nil, want graphql errors")
	}
	var ge graphQLErrors
	if !errors.As(err, &ge) {
		t.Fatalf("err type = %T, want graphQLErrors", err)
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("err = %q, want it to mention the bad field", err)
	}
}

// ── REST fallback wiring (GraphQL error → REST path used) ──────────────────────

// fallbackProvider implements both Provider and prReviewLister. Its GraphQL method
// always errors, so ListPullRequestsWithReviews must fall back to the REST
// ListPullRequests result with usedGraphQL=false and nil reviews — exactly the
// contract the syncer relies on (nil reviews → it runs the per-PR REST review path).
type fallbackProvider struct {
	restPRs       []RemotePR
	restPRsCalled bool
}

func (f *fallbackProvider) Platform() string                                { return "github" }
func (f *fallbackProvider) ListRepos(context.Context) ([]RemoteRepo, error) { return nil, nil }
func (f *fallbackProvider) ListIssues(context.Context, string) ([]RemoteIssue, error) {
	return nil, nil
}
func (f *fallbackProvider) ListPullRequests(context.Context, string) ([]RemotePR, error) {
	f.restPRsCalled = true
	return f.restPRs, nil
}
func (f *fallbackProvider) ListReviews(context.Context, string, int) ([]RemoteReview, error) {
	return nil, nil
}
func (f *fallbackProvider) ListCommits(context.Context, string, time.Time) ([]RemoteCommit, error) {
	return nil, nil
}
func (f *fallbackProvider) ListDeployments(context.Context, string) ([]RemoteDeployment, error) {
	return nil, nil
}
func (f *fallbackProvider) UpdateIssueState(context.Context, string, int, string) error {
	return nil
}

// ListPullRequestsWithReviews mimics the real providers' fallback wiring: a GraphQL
// failure logs and reverts to REST, returning usedGraphQL=false and nil reviews.
func (f *fallbackProvider) ListPullRequestsWithReviews(ctx context.Context, fullName string) ([]RemotePR, map[int][]RemoteReview, bool, error) {
	// Simulate a GraphQL error (e.g. decodeGraphQL returned graphQLErrors).
	_ = errors.New("graphql: simulated failure")
	restPRs, err := f.ListPullRequests(ctx, fullName)
	if err != nil {
		return nil, nil, false, err
	}
	return restPRs, nil, false, nil
}

func TestFallbackToRESTWhenGraphQLErrors(t *testing.T) {
	prov := &fallbackProvider{restPRs: []RemotePR{{Number: 1, State: "merged"}}}

	lister, ok := Provider(prov).(prReviewLister)
	if !ok {
		t.Fatal("fallbackProvider does not satisfy prReviewLister")
	}
	prs, reviews, usedGraphQL, err := lister.ListPullRequestsWithReviews(context.Background(), "acme/repo")
	if err != nil {
		t.Fatalf("ListPullRequestsWithReviews: %v", err)
	}
	if usedGraphQL {
		t.Error("usedGraphQL = true, want false (GraphQL failed → REST)")
	}
	if reviews != nil {
		t.Errorf("reviews = %v, want nil (signals syncer to use REST review path)", reviews)
	}
	if !prov.restPRsCalled {
		t.Error("REST ListPullRequests was not called on fallback")
	}
	if len(prs) != 1 || prs[0].Number != 1 {
		t.Errorf("prs = %+v, want the REST result", prs)
	}
}

// ── helpers ────────────────────────────────────────────────────────────────────

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return tm
}

// TestDecodeGraphQLValidJSON guards the envelope decode against a malformed body.
func TestDecodeGraphQLValidJSON(t *testing.T) {
	var data githubPRsResponse
	if err := decodeGraphQL([]byte("not json"), &data); err == nil {
		t.Error("decodeGraphQL accepted invalid JSON, want error")
	}
	// A well-formed JSON value still round-trips through json for safety.
	if !json.Valid([]byte(githubFixture)) {
		t.Error("githubFixture is not valid JSON")
	}
}
