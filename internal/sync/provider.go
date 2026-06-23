// Package sync provides platform-abstracted syncing of GitHub and GitLab
// issues and pull requests into the gitstate database, plus auto-progress
// computation from linked git activity (decisions P1: derived-not-entered).
//
// Token plumbing: each connected repo stores a per-org access token in the
// repos.clone_url field is NOT used for tokens — instead, callers supply a
// token string to NewGitHubProvider / NewGitLabProvider. In the API layer
// the token comes from the connect-repo POST body and is stored in a config
// record (or passed directly). Keep it simple: the API handler holds the
// token per-repo-connection and passes it to the syncer.
package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	gogithub "github.com/google/go-github/v66/github"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
	"golang.org/x/oauth2"
)

// ── Rate-limit / retry plumbing ───────────────────────────────────────────────
//
// Large orgs blow GitHub's 5000 req/hr (and trip secondary "abuse" limits); the
// GitLab API answers HTTP 429 under load. The OLD code was best-effort, so the
// first rate-limited call simply errored and the sync DROPPED data silently. The
// helpers below make EVERY API call site rate-limit-aware: on a primary/secondary
// rate limit they SLEEP (ctx-aware, capped, logged) until the limit clears and
// RETRY, and they retry transient 5xx/timeouts with a short backoff. The result
// is COMPLETE-but-slower instead of fast-but-truncated.

const (
	// retryAttempts is how many times a single API call is retried before the
	// fetch is considered failed. Rate-limit waits do not count against this —
	// only transient-error backoffs do — so a genuinely rate-limited call keeps
	// waiting until the window resets.
	retryAttempts = 5
	// maxRateWait caps a single rate-limit sleep so a bogus/far-future reset can
	// never wedge a sync indefinitely. GitHub's primary window is <= 1h.
	maxRateWait = time.Hour + time.Minute
)

// sleepCtx sleeps for d or until ctx is cancelled, whichever comes first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// ghDo runs a single GitHub API call fn and, on a rate-limit or transient error,
// waits and retries. It returns once fn succeeds, the error is non-retryable, the
// retry budget is exhausted, or ctx is cancelled. Wrapping every paginated loop's
// call in ghDo is what guarantees completeness under the 5000/hr cap.
func ghDo[T any](ctx context.Context, fn func() (T, *gogithub.Response, error)) (T, *gogithub.Response, error) {
	var zero T
	transient := 0
	for {
		v, resp, err := fn()
		if err == nil {
			return v, resp, nil
		}
		if cerr := ctx.Err(); cerr != nil {
			return zero, resp, cerr
		}

		// Primary rate limit: sleep until the window resets (capped, ctx-aware).
		var rle *gogithub.RateLimitError
		if errors.As(err, &rle) {
			wait := time.Until(rle.Rate.Reset.Time) + time.Second
			if wait > maxRateWait {
				wait = maxRateWait
			}
			if wait < 0 {
				wait = time.Second
			}
			slog.Info("github: rate limited, waiting", "dur", wait.Round(time.Second), "reset", rle.Rate.Reset.Time)
			if serr := sleepCtx(ctx, wait); serr != nil {
				return zero, resp, serr
			}
			continue // not counted against the transient budget
		}

		// Secondary ("abuse") rate limit: honour Retry-After when present.
		var arle *gogithub.AbuseRateLimitError
		if errors.As(err, &arle) {
			wait := arle.GetRetryAfter()
			if wait <= 0 {
				wait = time.Minute
			}
			if wait > maxRateWait {
				wait = maxRateWait
			}
			slog.Info("github: rate limited, waiting", "dur", wait.Round(time.Second), "kind", "secondary")
			if serr := sleepCtx(ctx, wait); serr != nil {
				return zero, resp, serr
			}
			continue
		}

		// Transient 5xx / network timeout: short capped backoff, finite retries.
		if isTransient(resp, err) && transient < retryAttempts {
			transient++
			back := time.Duration(transient) * 500 * time.Millisecond
			slog.Info("github: transient error, retrying", "attempt", transient, "backoff", back, "err", err)
			if serr := sleepCtx(ctx, back); serr != nil {
				return zero, resp, serr
			}
			continue
		}

		return zero, resp, err
	}
}

// isTransient reports whether an error is worth a short-backoff retry: a 5xx
// response or a non-rate-limit network/timeout error.
func isTransient(resp *gogithub.Response, err error) bool {
	if resp != nil && resp.StatusCode >= 500 {
		return true
	}
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return false // the outer ctx budget is up; don't keep retrying
	}
	var terr interface{ Timeout() bool }
	if errors.As(err, &terr) && terr.Timeout() {
		return true
	}
	return false
}

// glDo runs a single GitLab API call fn and, on HTTP 429 (honouring Retry-After)
// or a transient 5xx/timeout, waits and retries. GitLab has no typed rate-limit
// error, so the 429 is detected on the response status code.
func glDo[T any](ctx context.Context, fn func() (T, *gogitlab.Response, error)) (T, *gogitlab.Response, error) {
	var zero T
	transient := 0
	for {
		v, resp, err := fn()
		if err == nil {
			return v, resp, nil
		}
		if cerr := ctx.Err(); cerr != nil {
			return zero, resp, cerr
		}

		if resp != nil && resp.Response != nil && resp.StatusCode == 429 {
			wait := retryAfter(resp.Header.Get("Retry-After"))
			if wait <= 0 {
				wait = time.Minute
			}
			if wait > maxRateWait {
				wait = maxRateWait
			}
			slog.Info("gitlab: rate limited, waiting", "dur", wait.Round(time.Second))
			if serr := sleepCtx(ctx, wait); serr != nil {
				return zero, resp, serr
			}
			continue
		}

		if (resp != nil && resp.Response != nil && resp.StatusCode >= 500) && transient < retryAttempts {
			transient++
			back := time.Duration(transient) * 500 * time.Millisecond
			slog.Info("gitlab: transient error, retrying", "attempt", transient, "backoff", back, "err", err)
			if serr := sleepCtx(ctx, back); serr != nil {
				return zero, resp, serr
			}
			continue
		}

		return zero, resp, err
	}
}

// retryAfter parses a Retry-After header value, which is an integer number of
// seconds. Returns zero on an empty/unparseable value.
func retryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}

// ── Platform-neutral types ────────────────────────────────────────────────────

// RemoteIssue is a normalised issue fetched from a platform (GitHub or GitLab).
type RemoteIssue struct {
	ExternalID string // platform-specific string id
	Number     int
	Title      string
	Body       string
	State      string // "open" | "closed"
	Labels     []string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// RemotePR is a normalised pull request fetched from a platform.
type RemotePR struct {
	ExternalID   string
	Number       int
	Title        string
	Body         string
	State        string // "open" | "merged" | "closed"
	AuthorLogin  string
	Additions    int
	Deletions    int
	ChangedFiles int
	MergedAt     *time.Time
	CreatedAt    time.Time
	// FirstCommitAt is the author/commit date of the EARLIEST commit on the PR
	// branch. It is the start of the DORA lead time (first commit → merged_at)
	// computed by metrics.ComputeCycleTimes; zero when the PR has no commits.
	FirstCommitAt time.Time
}

// RemoteReview is a normalised PR/MR review event fetched from a platform.
// On GitHub it maps to a PullRequest review; on GitLab it is approximated from
// approvals + reviewer notes (GitLab has no first-class "review" object).
type RemoteReview struct {
	ReviewerLogin string
	State         string // "approved" | "changes_requested" | "commented" | "dismissed"
	SubmittedAt   time.Time
	ExternalID    string // platform review id (idempotency); may be empty
}

// RemoteDeployment is a normalised CI/CD deployment fetched from a platform.
// It feeds the deployments table → DORA deploy frequency + change-failure rate.
type RemoteDeployment struct {
	ExternalID  string
	Environment string
	Status      string // "success" | "failure"
	SHA         string
	DeployedAt  time.Time
}

// RemoteCommit is a normalised commit fetched from a platform's commits API
// (NOT a clone). It carries enough to populate the commits table that feeds
// Analytics, the heatmap, and Contribution. GitHub is fetched via the GraphQL
// history connection, which DOES return per-commit additions/deletions — so
// churn is accurate immediately, without waiting for the deep clone pass.
type RemoteCommit struct {
	SHA         string
	AuthorLogin string // platform login, falling back to the git author name
	AuthorEmail string
	Message     string
	CommittedAt time.Time
	Additions   int
	Deletions   int
}

// RemoteRepo is a normalised repository record from a platform.
type RemoteRepo struct {
	ExternalID    string
	FullName      string // owner/name
	DefaultBranch string
	CloneURL      string
}

// Provider is the platform abstraction for GitHub and GitLab.
// Implementations must be safe for concurrent use.
type Provider interface {
	// Platform returns "github" or "gitlab".
	Platform() string

	// ListRepos returns all repositories accessible with the configured token.
	// Used during repo discovery; not called on hot sync path.
	ListRepos(ctx context.Context) ([]RemoteRepo, error)

	// ListIssues returns all issues for the given repo full name ("owner/name" for
	// GitHub; "namespace/project" for GitLab).
	ListIssues(ctx context.Context, fullName string) ([]RemoteIssue, error)

	// ListPullRequests returns all PRs (open + merged + closed) for the repo.
	// Each RemotePR carries FirstCommitAt (earliest commit on the branch) so the
	// metrics layer can compute DORA lead time.
	ListPullRequests(ctx context.Context, fullName string) ([]RemotePR, error)

	// ListReviews returns the review events for one PR/MR number on the repo.
	// GitHub: real PR reviews. GitLab: approvals + reviewer notes (approximation).
	ListReviews(ctx context.Context, fullName string, prNumber int) ([]RemoteReview, error)

	// ListCommits returns repository commits via the platform commits API (no
	// clone). When since is non-zero only commits at/after it are returned (an
	// INCREMENTAL pull keyed off the repo's last_synced_at); a zero since pulls
	// the full history. Results carry no churn (the list endpoint omits it).
	ListCommits(ctx context.Context, fullName string, since time.Time) ([]RemoteCommit, error)

	// ListDeployments returns the CI/CD deployments for the repo (newest-first ok).
	ListDeployments(ctx context.Context, fullName string) ([]RemoteDeployment, error)

	// UpdateIssueState writes an issue state change back to the platform.
	// state should be "open" or "closed"; platforms may reject other values.
	UpdateIssueState(ctx context.Context, fullName string, number int, state string) error
}

// prReviewLister is an OPTIONAL capability some providers implement to fetch PRs,
// their reviews, AND each PR's first-commit date in ONE batched GraphQL request
// per page (50 PRs) instead of the REST fan-out (1 list call + 1 first-commit call
// per merged PR + 1 reviews call per merged PR). The syncer type-asserts for it:
// when present and it succeeds, the syncer uses the returned reviews directly and
// skips the per-PR REST review calls; on any GraphQL error the provider itself
// falls back to the REST ListPullRequests path so a query bug never breaks a sync.
//
// The returned map is keyed by PR number → that PR's reviews (may be nil/empty).
// usedGraphQL reports whether the GraphQL path actually produced the data (true)
// or the REST fallback did (false); the syncer uses it to decide whether reviews
// were supplied with the PRs (true → don't re-fetch reviews per PR).
type prReviewLister interface {
	ListPullRequestsWithReviews(ctx context.Context, fullName string) (prs []RemotePR, reviews map[int][]RemoteReview, usedGraphQL bool, err error)
}

// ── GraphQL transport ─────────────────────────────────────────────────────────
//
// We do NOT pull in a GraphQL client library (no go.mod churn): a GraphQL request
// is just an HTTP POST of {"query":...,"variables":...} with a JSON response. The
// helper below performs that POST, honours HTTP 403/429 rate-limit waits the same
// way ghDo does, decodes into the caller's typed value, and surfaces top-level
// GraphQL `errors` so the caller can fall back to REST.

// graphQLError is a single entry in a GraphQL response's top-level "errors" array.
type graphQLError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

func (e graphQLError) Error() string { return e.Message }

// graphQLErrors aggregates the top-level errors of a GraphQL response.
type graphQLErrors []graphQLError

func (es graphQLErrors) Error() string {
	parts := make([]string, 0, len(es))
	for _, e := range es {
		parts = append(parts, e.Message)
	}
	return "graphql: " + strings.Join(parts, "; ")
}

// graphQLDo POSTs a GraphQL query+variables to endpoint with the given auth header
// value and decodes the response "data" into out. It waits+retries on HTTP 403/429
// (rate limits) and transient 5xx, mirroring ghDo's policy, and returns a
// graphQLErrors when the response carries top-level GraphQL errors so the caller
// can fall back to REST. token is the raw token; authPrefix is "bearer" (GitHub)
// or "Bearer" (GitLab).
func graphQLDo(ctx context.Context, client *http.Client, endpoint, authPrefix, token, query string, variables map[string]any, out any) error {
	if client == nil {
		client = http.DefaultClient
	}
	type reqBody struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables,omitempty"`
	}
	payload, err := json.Marshal(reqBody{Query: query, Variables: variables})
	if err != nil {
		return fmt.Errorf("graphql: marshal request: %w", err)
	}

	transient := 0
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("graphql: new request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		if token != "" {
			req.Header.Set("Authorization", authPrefix+" "+token)
		}

		resp, err := client.Do(req)
		if err != nil {
			if cerr := ctx.Err(); cerr != nil {
				return cerr
			}
			// Network/timeout: short capped backoff, finite retries.
			if transient < retryAttempts {
				transient++
				back := time.Duration(transient) * 500 * time.Millisecond
				slog.Info("graphql: transient error, retrying", "attempt", transient, "backoff", back, "err", err)
				if serr := sleepCtx(ctx, back); serr != nil {
					return serr
				}
				continue
			}
			return fmt.Errorf("graphql: do request: %w", err)
		}

		// Rate-limited (primary 403 with a reset header, or 429): wait + retry.
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden {
			wait := graphQLWait(resp.Header)
			_ = resp.Body.Close()
			if wait <= 0 {
				wait = time.Minute
			}
			if wait > maxRateWait {
				wait = maxRateWait
			}
			slog.Info("graphql: rate limited, waiting", "dur", wait.Round(time.Second), "status", resp.StatusCode)
			if serr := sleepCtx(ctx, wait); serr != nil {
				return serr
			}
			continue
		}

		if resp.StatusCode >= 500 && transient < retryAttempts {
			_ = resp.Body.Close()
			transient++
			back := time.Duration(transient) * 500 * time.Millisecond
			slog.Info("graphql: transient 5xx, retrying", "attempt", transient, "backoff", back, "status", resp.StatusCode)
			if serr := sleepCtx(ctx, back); serr != nil {
				return serr
			}
			continue
		}

		body, rerr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if rerr != nil {
			return fmt.Errorf("graphql: read body: %w", rerr)
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("graphql: http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return decodeGraphQL(body, out)
	}
}

// decodeGraphQL unmarshals a GraphQL HTTP body into out, surfacing top-level
// GraphQL errors as a graphQLErrors so the caller can fall back to REST. It is
// split out so parsing can be unit-tested without an HTTP round-trip.
func decodeGraphQL(body []byte, out any) error {
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors graphQLErrors   `json:"errors"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("graphql: decode envelope: %w", err)
	}
	if len(envelope.Errors) > 0 {
		return envelope.Errors
	}
	if len(envelope.Data) == 0 {
		return errors.New("graphql: empty data")
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("graphql: decode data: %w", err)
	}
	return nil
}

// graphQLWait derives a rate-limit wait from response headers. It honours
// Retry-After (seconds), then GitHub's x-ratelimit-reset (unix seconds).
func graphQLWait(h http.Header) time.Duration {
	if d := retryAfter(h.Get("Retry-After")); d > 0 {
		return d + time.Second
	}
	if v := h.Get("x-ratelimit-reset"); v != "" {
		if secs, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			wait := time.Until(time.Unix(secs, 0)) + time.Second
			if wait > 0 {
				return wait
			}
		}
	}
	return 0
}

// ── GitHub implementation ─────────────────────────────────────────────────────

// githubProvider implements Provider using go-github, plus the optional
// prReviewLister capability via the GitHub GraphQL API (one batched query per 50
// PRs for PRs + reviews + first-commit, killing the REST per-PR fan-out).
type githubProvider struct {
	client *gogithub.Client
	token  string       // raw token, reused for the GraphQL POST Authorization header
	http   *http.Client // plain client for GraphQL (no oauth transport needed)
}

// githubGraphQLEndpoint is the GitHub GraphQL v4 endpoint.
const githubGraphQLEndpoint = "https://api.github.com/graphql"

// NewGitHubProvider returns a Provider backed by the GitHub REST API.
// token is a personal access token or OAuth token with repo scope.
func NewGitHubProvider(ctx context.Context, token string) Provider {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	return &githubProvider{
		client: gogithub.NewClient(tc),
		token:  token,
		http:   &http.Client{Timeout: 60 * time.Second},
	}
}

func (g *githubProvider) Platform() string { return "github" }

func (g *githubProvider) ListRepos(ctx context.Context) ([]RemoteRepo, error) {
	var out []RemoteRepo
	seen := map[int64]bool{}
	add := func(r *gogithub.Repository) {
		id := r.GetID()
		if id == 0 || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, RemoteRepo{
			ExternalID:    fmt.Sprintf("%d", id),
			FullName:      r.GetFullName(),
			DefaultBranch: r.GetDefaultBranch(),
			CloneURL:      r.GetCloneURL(),
		})
	}

	// 0. Installation token path: a GitHub App installation token can ONLY use the
	//    installation endpoints — `/user/repos` returns 403 "Resource not accessible
	//    by integration". `/installation/repositories` returns exactly the repos the
	//    App was granted on this install (the scoped list we want). Try it first; if
	//    this is an OAuth user token instead, it 403/404s and we fall through to the
	//    user+orgs enumeration below.
	iopts := &gogithub.ListOptions{PerPage: 100}
	installOK := false
	for {
		page, resp, err := ghDo(ctx, func() (*gogithub.ListRepositories, *gogithub.Response, error) {
			return g.client.Apps.ListRepos(ctx, iopts)
		})
		if err != nil {
			break // not an installation token (OAuth) → use the user/orgs path
		}
		installOK = true
		for _, r := range page.Repositories {
			add(r)
		}
		if resp.NextPage == 0 {
			break
		}
		iopts.Page = resp.NextPage
	}
	if installOK {
		return out, nil
	}

	// 1. Repos the authenticated user owns / collaborates on. (/user/repos does NOT
	//    reliably return all repos of an org the user belongs to — see step 2.)
	uopts := &gogithub.RepositoryListByAuthenticatedUserOptions{ListOptions: gogithub.ListOptions{PerPage: 100}}
	for {
		repos, resp, err := ghDo(ctx, func() ([]*gogithub.Repository, *gogithub.Response, error) {
			return g.client.Repositories.ListByAuthenticatedUser(ctx, uopts)
		})
		if err != nil {
			return nil, fmt.Errorf("github: list user repos: %w", err)
		}
		for _, r := range repos {
			add(r)
		}
		if resp.NextPage == 0 {
			break
		}
		uopts.Page = resp.NextPage
	}

	// 2. ALL repos in each org the user belongs to. This is the fix for "only got
	//    43 of 100+": /user/repos misses org repos the user isn't directly affiliated
	//    with, so enumerate the user's orgs and page through /orgs/{org}/repos.
	var orgs []*gogithub.Organization
	oopts := &gogithub.ListOptions{PerPage: 100}
	for {
		page, resp, err := ghDo(ctx, func() ([]*gogithub.Organization, *gogithub.Response, error) {
			return g.client.Organizations.List(ctx, "", oopts)
		})
		if err != nil {
			break // best-effort: user repos are already collected
		}
		orgs = append(orgs, page...)
		if resp.NextPage == 0 {
			break
		}
		oopts.Page = resp.NextPage
	}
	for _, org := range orgs {
		ropts := &gogithub.RepositoryListByOrgOptions{ListOptions: gogithub.ListOptions{PerPage: 100}}
		for {
			repos, resp, err := ghDo(ctx, func() ([]*gogithub.Repository, *gogithub.Response, error) {
				return g.client.Repositories.ListByOrg(ctx, org.GetLogin(), ropts)
			})
			if err != nil {
				break // org may restrict the OAuth app; skip it, keep the rest
			}
			for _, r := range repos {
				add(r)
			}
			if resp.NextPage == 0 {
				break
			}
			ropts.Page = resp.NextPage
		}
	}

	return out, nil
}

// githubIssuesQuery pages issues (100/page) with number/title/body/state/labels and
// the REAL created/updated timestamps in ONE request — replacing the REST issues
// fan-out (a 1727-issue repo is 18 sequential REST pages). It excludes PRs natively
// (the issues connection returns only issues, unlike the REST issues endpoint).
const githubIssuesQuery = `query($owner:String!,$name:String!,$cur:String){
  rateLimit { cost remaining resetAt }
  repository(owner:$owner, name:$name) {
    issues(first:100, after:$cur, states:[OPEN,CLOSED], orderBy:{field:CREATED_AT, direction:ASC}) {
      pageInfo { hasNextPage endCursor }
      nodes {
        number title body state databaseId createdAt updatedAt
        labels(first:50) { nodes { name } }
      }
    }
  }
}`

// githubIssuesResponse mirrors githubIssuesQuery's "data".
type githubIssuesResponse struct {
	RateLimit struct {
		Cost      int    `json:"cost"`
		Remaining int    `json:"remaining"`
		ResetAt   string `json:"resetAt"`
	} `json:"rateLimit"`
	Repository struct {
		Issues struct {
			PageInfo struct {
				HasNextPage bool   `json:"hasNextPage"`
				EndCursor   string `json:"endCursor"`
			} `json:"pageInfo"`
			Nodes []githubIssueNode `json:"nodes"`
		} `json:"issues"`
	} `json:"repository"`
}

type githubIssueNode struct {
	Number     int       `json:"number"`
	Title      string    `json:"title"`
	Body       string    `json:"body"`
	State      string    `json:"state"` // OPEN | CLOSED
	DatabaseID int64     `json:"databaseId"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	Labels     struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
}

// mapGitHubIssueNode maps one GraphQL issue node to a RemoteIssue, carrying the real
// platform created/updated timestamps so issue dates reflect the platform.
func mapGitHubIssueNode(n githubIssueNode) RemoteIssue {
	ri := RemoteIssue{
		ExternalID: fmt.Sprintf("%d", n.DatabaseID),
		Number:     n.Number,
		Title:      n.Title,
		Body:       n.Body,
		State:      normaliseState(strings.ToLower(n.State)),
		CreatedAt:  n.CreatedAt,
		UpdatedAt:  n.UpdatedAt,
	}
	for _, l := range n.Labels.Nodes {
		if l.Name != "" {
			ri.Labels = append(ri.Labels, l.Name)
		}
	}
	return ri
}

// ListIssues fetches all issues for the repo. It PREFERS the GraphQL path (one
// query per 100 issues vs the REST per-page fan-out); on ANY GraphQL error it logs a
// WARN and falls back to the REST path (listIssuesREST) so a query bug or schema
// drift never fails a sync. RemoteIssue carries the real created/updated timestamps
// either way.
func (g *githubProvider) ListIssues(ctx context.Context, fullName string) ([]RemoteIssue, error) {
	owner, name, err := splitFullName(fullName)
	if err != nil {
		return nil, err
	}
	issues, gerr := g.graphQLIssues(ctx, owner, name)
	if gerr != nil {
		slog.Warn("github: graphql issue fetch failed, falling back to REST", "repo", fullName, "err", gerr)
		return g.listIssuesREST(ctx, fullName)
	}
	return issues, nil
}

// graphQLIssues runs the paged GraphQL issues query and maps the result to
// []RemoteIssue. Returns an error on any GraphQL/transport failure so the caller can
// fall back to REST.
func (g *githubProvider) graphQLIssues(ctx context.Context, owner, name string) ([]RemoteIssue, error) {
	var (
		out    []RemoteIssue
		cursor string
	)
	for {
		vars := map[string]any{"owner": owner, "name": name}
		if cursor != "" {
			vars["cur"] = cursor
		} else {
			vars["cur"] = nil
		}
		var data githubIssuesResponse
		if err := graphQLDo(ctx, g.http, githubGraphQLEndpoint, "bearer", g.token, githubIssuesQuery, vars, &data); err != nil {
			return nil, err
		}
		for _, n := range data.Repository.Issues.Nodes {
			out = append(out, mapGitHubIssueNode(n))
		}
		// Polite throttle: if the GraphQL budget is nearly spent, wait for resetAt.
		if data.RateLimit.Remaining > 0 && data.RateLimit.Remaining <= data.RateLimit.Cost {
			if reset, perr := time.Parse(time.RFC3339, data.RateLimit.ResetAt); perr == nil {
				wait := time.Until(reset) + time.Second
				if wait > 0 {
					if wait > maxRateWait {
						wait = maxRateWait
					}
					slog.Info("github: graphql issue budget low, waiting for reset", "dur", wait.Round(time.Second))
					if serr := sleepCtx(ctx, wait); serr != nil {
						return nil, serr
					}
				}
			}
		}
		if !data.Repository.Issues.PageInfo.HasNextPage {
			break
		}
		cursor = data.Repository.Issues.PageInfo.EndCursor
	}
	return out, nil
}

// listIssuesREST is the REST fallback for ListIssues (paginated GitHub issues API,
// skipping PRs). Used only when the GraphQL path errors.
func (g *githubProvider) listIssuesREST(ctx context.Context, fullName string) ([]RemoteIssue, error) {
	owner, name, err := splitFullName(fullName)
	if err != nil {
		return nil, err
	}

	opts := &gogithub.IssueListByRepoOptions{
		State:       "all",
		ListOptions: gogithub.ListOptions{PerPage: 100},
	}
	var out []RemoteIssue
	for {
		issues, resp, err := ghDo(ctx, func() ([]*gogithub.Issue, *gogithub.Response, error) {
			return g.client.Issues.ListByRepo(ctx, owner, name, opts)
		})
		if err != nil {
			return nil, fmt.Errorf("github: list issues %s: %w", fullName, err)
		}
		for _, i := range issues {
			// GitHub returns PRs in the issues API — skip them here.
			if i.IsPullRequest() {
				continue
			}
			ri := RemoteIssue{
				ExternalID: fmt.Sprintf("%d", i.GetID()),
				Number:     i.GetNumber(),
				Title:      i.GetTitle(),
				Body:       i.GetBody(),
				State:      normaliseState(i.GetState()),
				CreatedAt:  i.GetCreatedAt().Time,
				UpdatedAt:  i.GetUpdatedAt().Time,
			}
			for _, l := range i.Labels {
				ri.Labels = append(ri.Labels, l.GetName())
			}
			out = append(out, ri)
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

func (g *githubProvider) ListPullRequests(ctx context.Context, fullName string) ([]RemotePR, error) {
	owner, name, err := splitFullName(fullName)
	if err != nil {
		return nil, err
	}

	opts := &gogithub.PullRequestListOptions{
		State:       "all",
		ListOptions: gogithub.ListOptions{PerPage: 100},
	}
	var out []RemotePR
	for {
		prs, resp, err := ghDo(ctx, func() ([]*gogithub.PullRequest, *gogithub.Response, error) {
			return g.client.PullRequests.List(ctx, owner, name, opts)
		})
		if err != nil {
			return nil, fmt.Errorf("github: list prs %s: %w", fullName, err)
		}
		for _, pr := range prs {
			rpr := RemotePR{
				ExternalID:   fmt.Sprintf("%d", pr.GetID()),
				Number:       pr.GetNumber(),
				Title:        pr.GetTitle(),
				Body:         pr.GetBody(),
				AuthorLogin:  pr.GetUser().GetLogin(),
				Additions:    pr.GetAdditions(),
				Deletions:    pr.GetDeletions(),
				ChangedFiles: pr.GetChangedFiles(),
				CreatedAt:    pr.GetCreatedAt().Time,
			}
			switch pr.GetState() {
			case "closed":
				if pr.GetMerged() {
					rpr.State = "merged"
					t := pr.GetMergedAt().Time
					rpr.MergedAt = &t
				} else {
					rpr.State = "closed"
				}
			default:
				rpr.State = "open"
			}
			// DORA lead time starts at the FIRST commit on the branch. Cycle time is
			// only computed for MERGED PRs, so gate this per-PR commits fetch on
			// state=="merged": that removes a per-PR API call for every open/closed-
			// unmerged PR, roughly halving the call volume on a busy repo without
			// losing any metric. Best-effort: a commits-fetch failure leaves
			// FirstCommitAt zero (this PR's cycle time is skipped) but never aborts.
			if rpr.State == "merged" {
				if first, ferr := g.firstCommitAt(ctx, owner, name, pr.GetNumber()); ferr == nil && !first.IsZero() {
					rpr.FirstCommitAt = first
				}
			}
			out = append(out, rpr)
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

// githubPRsQuery pages PRs (50/page) with their reviews and first commit in ONE
// request. cost/remaining come back via rateLimit so we can throttle politely.
const githubPRsQuery = `query($owner:String!,$name:String!,$cur:String){
  rateLimit { cost remaining resetAt }
  repository(owner:$owner, name:$name) {
    pullRequests(first:50, after:$cur, states:[OPEN,CLOSED,MERGED], orderBy:{field:CREATED_AT, direction:ASC}) {
      pageInfo { hasNextPage endCursor }
      nodes {
        number title body state databaseId
        author { login }
        additions deletions changedFiles
        mergedAt createdAt
        commits(first:1) { nodes { commit { authoredDate committedDate } } }
        reviews(first:50) { nodes { databaseId state submittedAt author { login } } }
      }
    }
  }
}`

// githubPRsResponse mirrors githubPRsQuery's "data".
type githubPRsResponse struct {
	RateLimit struct {
		Cost      int    `json:"cost"`
		Remaining int    `json:"remaining"`
		ResetAt   string `json:"resetAt"`
	} `json:"rateLimit"`
	Repository struct {
		PullRequests struct {
			PageInfo struct {
				HasNextPage bool   `json:"hasNextPage"`
				EndCursor   string `json:"endCursor"`
			} `json:"pageInfo"`
			Nodes []githubPRNode `json:"nodes"`
		} `json:"pullRequests"`
	} `json:"repository"`
}

type githubPRNode struct {
	Number     int    `json:"number"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	State      string `json:"state"` // OPEN | CLOSED | MERGED
	DatabaseID int64  `json:"databaseId"`
	Author     *struct {
		Login string `json:"login"`
	} `json:"author"`
	Additions    int        `json:"additions"`
	Deletions    int        `json:"deletions"`
	ChangedFiles int        `json:"changedFiles"`
	MergedAt     *time.Time `json:"mergedAt"`
	CreatedAt    time.Time  `json:"createdAt"`
	Commits      struct {
		Nodes []struct {
			Commit struct {
				AuthoredDate  *time.Time `json:"authoredDate"`
				CommittedDate *time.Time `json:"committedDate"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
	Reviews struct {
		Nodes []struct {
			DatabaseID  int64      `json:"databaseId"`
			State       string     `json:"state"`
			SubmittedAt *time.Time `json:"submittedAt"`
			Author      *struct {
				Login string `json:"login"`
			} `json:"author"`
		} `json:"nodes"`
	} `json:"reviews"`
}

// ListPullRequestsWithReviews fetches all PRs + their reviews + each PR's first
// commit date via GraphQL (50 PRs/request). On ANY GraphQL error it logs a WARN
// and falls back to the REST ListPullRequests path (usedGraphQL=false), so a query
// bug can never break the sync.
func (g *githubProvider) ListPullRequestsWithReviews(ctx context.Context, fullName string) ([]RemotePR, map[int][]RemoteReview, bool, error) {
	owner, name, err := splitFullName(fullName)
	if err != nil {
		return nil, nil, false, err
	}

	prs, reviews, err := g.graphQLPRs(ctx, owner, name)
	if err != nil {
		// GraphQL failed (query bug, schema drift, auth) → fall back to REST so the
		// sync still completes. The reviews map is nil so the syncer reverts to the
		// per-PR REST review path.
		slog.Warn("github: graphql PR fetch failed, falling back to REST", "repo", fullName, "err", err)
		restPRs, rerr := g.ListPullRequests(ctx, fullName)
		if rerr != nil {
			return nil, nil, false, rerr
		}
		return restPRs, nil, false, nil
	}
	return prs, reviews, true, nil
}

// graphQLPRs runs the paged GraphQL PR query and maps the result to RemotePR plus
// a per-PR review map. Returns an error on any GraphQL/transport failure so the
// caller can fall back to REST.
func (g *githubProvider) graphQLPRs(ctx context.Context, owner, name string) ([]RemotePR, map[int][]RemoteReview, error) {
	var (
		prs     []RemotePR
		reviews = map[int][]RemoteReview{}
		cursor  string
	)
	for {
		vars := map[string]any{"owner": owner, "name": name}
		if cursor != "" {
			vars["cur"] = cursor
		} else {
			vars["cur"] = nil
		}
		var data githubPRsResponse
		if err := graphQLDo(ctx, g.http, githubGraphQLEndpoint, "bearer", g.token, githubPRsQuery, vars, &data); err != nil {
			return nil, nil, err
		}
		for _, n := range data.Repository.PullRequests.Nodes {
			pr, revs := mapGitHubPRNode(n)
			prs = append(prs, pr)
			if len(revs) > 0 {
				reviews[pr.Number] = revs
			}
		}
		// Polite throttle: if the GraphQL budget is nearly spent, wait for resetAt.
		if data.RateLimit.Remaining > 0 && data.RateLimit.Remaining <= data.RateLimit.Cost {
			if reset, perr := time.Parse(time.RFC3339, data.RateLimit.ResetAt); perr == nil {
				wait := time.Until(reset) + time.Second
				if wait > 0 {
					if wait > maxRateWait {
						wait = maxRateWait
					}
					slog.Info("github: graphql budget low, waiting for reset", "dur", wait.Round(time.Second))
					if serr := sleepCtx(ctx, wait); serr != nil {
						return nil, nil, serr
					}
				}
			}
		}
		if !data.Repository.PullRequests.PageInfo.HasNextPage {
			break
		}
		cursor = data.Repository.PullRequests.PageInfo.EndCursor
	}
	return prs, reviews, nil
}

// mapGitHubPRNode maps one GraphQL PR node to a RemotePR (with FirstCommitAt from
// its first commit) and the PR's []RemoteReview.
func mapGitHubPRNode(n githubPRNode) (RemotePR, []RemoteReview) {
	pr := RemotePR{
		ExternalID:   fmt.Sprintf("%d", n.DatabaseID),
		Number:       n.Number,
		Title:        n.Title,
		Body:         n.Body,
		Additions:    n.Additions,
		Deletions:    n.Deletions,
		ChangedFiles: n.ChangedFiles,
		CreatedAt:    n.CreatedAt,
	}
	if n.Author != nil {
		pr.AuthorLogin = n.Author.Login
	}
	switch strings.ToUpper(n.State) {
	case "MERGED":
		pr.State = "merged"
		if n.MergedAt != nil {
			t := *n.MergedAt
			pr.MergedAt = &t
		}
	case "CLOSED":
		pr.State = "closed"
	default:
		pr.State = "open"
	}
	// FirstCommitAt: the commits(first:1) node, ordered oldest-first by GitHub.
	if len(n.Commits.Nodes) > 0 {
		c := n.Commits.Nodes[0].Commit
		switch {
		case c.AuthoredDate != nil:
			pr.FirstCommitAt = *c.AuthoredDate
		case c.CommittedDate != nil:
			pr.FirstCommitAt = *c.CommittedDate
		}
	}
	var revs []RemoteReview
	for _, rv := range n.Reviews.Nodes {
		if rv.Author == nil || rv.Author.Login == "" {
			continue
		}
		r := RemoteReview{
			ReviewerLogin: rv.Author.Login,
			State:         normaliseReviewState(rv.State),
		}
		if rv.SubmittedAt != nil {
			r.SubmittedAt = *rv.SubmittedAt
		}
		if rv.DatabaseID != 0 {
			r.ExternalID = fmt.Sprintf("%d", rv.DatabaseID)
		}
		revs = append(revs, r)
	}
	return pr, revs
}

// firstCommitAt returns the earliest author/commit date among a PR's commits.
func (g *githubProvider) firstCommitAt(ctx context.Context, owner, name string, number int) (time.Time, error) {
	opts := &gogithub.ListOptions{PerPage: 100}
	var earliest time.Time
	for {
		commits, resp, err := ghDo(ctx, func() ([]*gogithub.RepositoryCommit, *gogithub.Response, error) {
			return g.client.PullRequests.ListCommits(ctx, owner, name, number, opts)
		})
		if err != nil {
			return time.Time{}, err
		}
		for _, c := range commits {
			t := commitTime(c)
			if t.IsZero() {
				continue
			}
			if earliest.IsZero() || t.Before(earliest) {
				earliest = t
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return earliest, nil
}

// commitTime extracts the author date (falling back to committer date) of a
// RepositoryCommit returned by the GitHub pulls API.
func commitTime(c *gogithub.RepositoryCommit) time.Time {
	if c == nil || c.Commit == nil {
		return time.Time{}
	}
	if a := c.Commit.Author; a != nil && a.Date != nil {
		return a.Date.Time
	}
	if cm := c.Commit.Committer; cm != nil && cm.Date != nil {
		return cm.Date.Time
	}
	return time.Time{}
}

// githubCommitsQuery pages the default-branch history (100/commit) WITH per-commit
// additions/deletions in ONE GraphQL request per page — so churn is accurate from
// the fast sync, no clone needed. Default-branch only (matching the old REST path):
// enumerating every branch would multiply cost for marginal data.
const githubCommitsQuery = `query($owner:String!,$name:String!,$cur:String,$since:GitTimestamp){
  rateLimit { cost remaining resetAt }
  repository(owner:$owner, name:$name) {
    defaultBranchRef {
      target {
        ... on Commit {
          history(first:100, after:$cur, since:$since) {
            pageInfo { hasNextPage endCursor }
            nodes {
              oid additions deletions committedDate message
              author { name email user { login } }
            }
          }
        }
      }
    }
  }
}`

type githubCommitsResponse struct {
	RateLimit struct {
		Cost      int    `json:"cost"`
		Remaining int    `json:"remaining"`
		ResetAt   string `json:"resetAt"`
	} `json:"rateLimit"`
	Repository struct {
		DefaultBranchRef struct {
			Target struct {
				History struct {
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
					Nodes []struct {
						OID           string    `json:"oid"`
						Additions     int       `json:"additions"`
						Deletions     int       `json:"deletions"`
						CommittedDate time.Time `json:"committedDate"`
						Message       string    `json:"message"`
						Author        *struct {
							Name  string `json:"name"`
							Email string `json:"email"`
							User  *struct {
								Login string `json:"login"`
							} `json:"user"`
						} `json:"author"`
					} `json:"nodes"`
				} `json:"history"`
			} `json:"target"`
		} `json:"defaultBranchRef"`
	} `json:"repository"`
}

func (g *githubProvider) ListCommits(ctx context.Context, fullName string, since time.Time) ([]RemoteCommit, error) {
	owner, name, err := splitFullName(fullName)
	if err != nil {
		return nil, err
	}
	var (
		out    []RemoteCommit
		cursor string
	)
	for {
		vars := map[string]any{"owner": owner, "name": name}
		if cursor != "" {
			vars["cur"] = cursor
		} else {
			vars["cur"] = nil
		}
		if !since.IsZero() {
			vars["since"] = since.UTC().Format(time.RFC3339)
		} else {
			vars["since"] = nil
		}
		var data githubCommitsResponse
		if err := graphQLDo(ctx, g.http, githubGraphQLEndpoint, "bearer", g.token, githubCommitsQuery, vars, &data); err != nil {
			return nil, fmt.Errorf("github: list commits %s: %w", fullName, err)
		}
		hist := data.Repository.DefaultBranchRef.Target.History
		for _, n := range hist.Nodes {
			if n.OID == "" {
				continue
			}
			rc := RemoteCommit{
				SHA:         n.OID,
				Message:     n.Message,
				CommittedAt: n.CommittedDate,
				Additions:   n.Additions,
				Deletions:   n.Deletions,
			}
			if a := n.Author; a != nil {
				rc.AuthorEmail = a.Email
				if a.User != nil {
					rc.AuthorLogin = a.User.Login
				}
				if rc.AuthorLogin == "" {
					rc.AuthorLogin = a.Name // fall back to git author name
				}
			}
			out = append(out, rc)
		}
		// Polite throttle when the GraphQL budget is nearly spent.
		if data.RateLimit.Remaining > 0 && data.RateLimit.Remaining <= data.RateLimit.Cost {
			if reset, perr := time.Parse(time.RFC3339, data.RateLimit.ResetAt); perr == nil {
				wait := time.Until(reset) + time.Second
				if wait > maxRateWait {
					wait = maxRateWait
				}
				if wait > 0 {
					slog.Info("github: graphql budget low, waiting for reset", "dur", wait.Round(time.Second))
					if serr := sleepCtx(ctx, wait); serr != nil {
						return nil, serr
					}
				}
			}
		}
		if !hist.PageInfo.HasNextPage {
			break
		}
		cursor = hist.PageInfo.EndCursor
	}
	return out, nil
}

func (g *githubProvider) ListReviews(ctx context.Context, fullName string, prNumber int) ([]RemoteReview, error) {
	owner, name, err := splitFullName(fullName)
	if err != nil {
		return nil, err
	}
	opts := &gogithub.ListOptions{PerPage: 100}
	var out []RemoteReview
	for {
		reviews, resp, err := ghDo(ctx, func() ([]*gogithub.PullRequestReview, *gogithub.Response, error) {
			return g.client.PullRequests.ListReviews(ctx, owner, name, prNumber, opts)
		})
		if err != nil {
			return nil, fmt.Errorf("github: list reviews %s#%d: %w", fullName, prNumber, err)
		}
		for _, rv := range reviews {
			login := rv.GetUser().GetLogin()
			if login == "" {
				continue
			}
			out = append(out, RemoteReview{
				ReviewerLogin: login,
				State:         normaliseReviewState(rv.GetState()),
				SubmittedAt:   rv.GetSubmittedAt().Time,
				ExternalID:    fmt.Sprintf("%d", rv.GetID()),
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

func (g *githubProvider) ListDeployments(ctx context.Context, fullName string) ([]RemoteDeployment, error) {
	owner, name, err := splitFullName(fullName)
	if err != nil {
		return nil, err
	}
	opts := &gogithub.DeploymentsListOptions{ListOptions: gogithub.ListOptions{PerPage: 100}}
	var out []RemoteDeployment
	for {
		deps, resp, err := ghDo(ctx, func() ([]*gogithub.Deployment, *gogithub.Response, error) {
			return g.client.Repositories.ListDeployments(ctx, owner, name, opts)
		})
		if err != nil {
			return nil, fmt.Errorf("github: list deployments %s: %w", fullName, err)
		}
		for _, d := range deps {
			rd := RemoteDeployment{
				ExternalID:  fmt.Sprintf("%d", d.GetID()),
				Environment: d.GetEnvironment(),
				SHA:         d.GetSHA(),
				Status:      "success", // default until a status says otherwise
				DeployedAt:  d.GetCreatedAt().Time,
			}
			// The deployment object carries no terminal status — the latest
			// deployment STATUS does. Best-effort: a status-fetch failure leaves the
			// optimistic "success" default rather than dropping the deployment.
			statuses, _, serr := ghDo(ctx, func() ([]*gogithub.DeploymentStatus, *gogithub.Response, error) {
				return g.client.Repositories.ListDeploymentStatuses(ctx, owner, name, d.GetID(), &gogithub.ListOptions{PerPage: 1})
			})
			if serr == nil && len(statuses) > 0 {
				rd.Status = normaliseDeployStatus(statuses[0].GetState())
				if t := statuses[0].GetUpdatedAt(); !t.Time.IsZero() {
					rd.DeployedAt = t.Time
				}
			}
			out = append(out, rd)
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

func (g *githubProvider) UpdateIssueState(ctx context.Context, fullName string, number int, state string) error {
	owner, name, err := splitFullName(fullName)
	if err != nil {
		return err
	}
	ghState := state // "open" or "closed"
	req := &gogithub.IssueRequest{State: &ghState}
	_, _, err = ghDo(ctx, func() (*gogithub.Issue, *gogithub.Response, error) {
		return g.client.Issues.Edit(ctx, owner, name, number, req)
	})
	if err != nil {
		return fmt.Errorf("github: update issue state %s#%d: %w", fullName, number, err)
	}
	return nil
}

// ── GitLab implementation ─────────────────────────────────────────────────────

// gitlabProvider implements Provider using the gitlab client, plus the optional
// prReviewLister capability via the GitLab GraphQL API (one batched query per 50
// MRs for MRs + approvals + first-commit, killing the REST per-MR fan-out).
type gitlabProvider struct {
	client          *gogitlab.Client
	token           string
	http            *http.Client
	graphQLEndpoint string // derived from baseURL; defaults to gitlab.com
}

// gitlabDefaultGraphQLEndpoint is the gitlab.com GraphQL endpoint.
const gitlabDefaultGraphQLEndpoint = "https://gitlab.com/api/graphql"

// NewGitLabProvider returns a Provider backed by the GitLab REST API.
// token is a personal access token or OAuth token with api scope.
// baseURL may be empty to use gitlab.com.
func NewGitLabProvider(token string, baseURL string) (Provider, error) {
	var opts []gogitlab.ClientOptionFunc
	if baseURL != "" {
		opts = append(opts, gogitlab.WithBaseURL(baseURL))
	}
	client, err := gogitlab.NewOAuthClient(token, opts...)
	if err != nil {
		return nil, fmt.Errorf("gitlab: create client: %w", err)
	}
	gqlEndpoint := gitlabDefaultGraphQLEndpoint
	if baseURL != "" {
		gqlEndpoint = gitlabGraphQLEndpointFor(baseURL)
	}
	return &gitlabProvider{
		client:          client,
		token:           token,
		http:            &http.Client{Timeout: 60 * time.Second},
		graphQLEndpoint: gqlEndpoint,
	}, nil
}

// gitlabGraphQLEndpointFor derives the GraphQL endpoint for a self-hosted base URL.
// GitLab's REST base is ".../api/v4"; GraphQL lives at ".../api/graphql".
func gitlabGraphQLEndpointFor(baseURL string) string {
	b := strings.TrimRight(baseURL, "/")
	b = strings.TrimSuffix(b, "/api/v4")
	b = strings.TrimRight(b, "/")
	return b + "/api/graphql"
}

func (gl *gitlabProvider) Platform() string { return "gitlab" }

func (gl *gitlabProvider) ListRepos(ctx context.Context) ([]RemoteRepo, error) {
	opts := &gogitlab.ListProjectsOptions{
		ListOptions: gogitlab.ListOptions{PerPage: 100},
	}
	var out []RemoteRepo
	for {
		projects, resp, err := glDo(ctx, func() ([]*gogitlab.Project, *gogitlab.Response, error) {
			return gl.client.Projects.ListProjects(opts, gogitlab.WithContext(ctx))
		})
		if err != nil {
			return nil, fmt.Errorf("gitlab: list repos: %w", err)
		}
		for _, p := range projects {
			out = append(out, RemoteRepo{
				ExternalID:    fmt.Sprintf("%d", p.ID),
				FullName:      p.PathWithNamespace,
				DefaultBranch: p.DefaultBranch,
				CloneURL:      p.HTTPURLToRepo,
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

func (gl *gitlabProvider) ListIssues(ctx context.Context, fullName string) ([]RemoteIssue, error) {
	stateAll := "all"
	opts := &gogitlab.ListProjectIssuesOptions{
		State:       &stateAll,
		ListOptions: gogitlab.ListOptions{PerPage: 100},
	}
	var out []RemoteIssue
	for {
		issues, resp, err := glDo(ctx, func() ([]*gogitlab.Issue, *gogitlab.Response, error) {
			return gl.client.Issues.ListProjectIssues(fullName, opts, gogitlab.WithContext(ctx))
		})
		if err != nil {
			return nil, fmt.Errorf("gitlab: list issues %s: %w", fullName, err)
		}
		for _, i := range issues {
			ri := RemoteIssue{
				ExternalID: fmt.Sprintf("%d", i.ID),
				Number:     int(i.IID),
				Title:      i.Title,
				Body:       i.Description,
				State:      normaliseState(i.State),
			}
			if i.CreatedAt != nil {
				ri.CreatedAt = *i.CreatedAt
			}
			if i.UpdatedAt != nil {
				ri.UpdatedAt = *i.UpdatedAt
			}
			for _, l := range i.Labels {
				ri.Labels = append(ri.Labels, l)
			}
			out = append(out, ri)
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

func (gl *gitlabProvider) ListPullRequests(ctx context.Context, fullName string) ([]RemotePR, error) {
	stateAll := "all"
	opts := &gogitlab.ListProjectMergeRequestsOptions{
		State:       &stateAll,
		ListOptions: gogitlab.ListOptions{PerPage: 100},
	}
	var out []RemotePR
	for {
		mrs, resp, err := glDo(ctx, func() ([]*gogitlab.BasicMergeRequest, *gogitlab.Response, error) {
			return gl.client.MergeRequests.ListProjectMergeRequests(fullName, opts, gogitlab.WithContext(ctx))
		})
		if err != nil {
			return nil, fmt.Errorf("gitlab: list mrs %s: %w", fullName, err)
		}
		for _, mr := range mrs {
			rpr := RemotePR{
				ExternalID:  fmt.Sprintf("%d", mr.ID),
				Number:      int(mr.IID),
				Title:       mr.Title,
				Body:        mr.Description,
				AuthorLogin: mr.Author.Username,
			}
			if mr.CreatedAt != nil {
				rpr.CreatedAt = *mr.CreatedAt
			}
			switch mr.State {
			case "merged":
				rpr.State = "merged"
				rpr.MergedAt = mr.MergedAt
			case "closed":
				rpr.State = "closed"
			default:
				rpr.State = "open"
			}
			// DORA lead time starts at the MR's first commit and is only computed for
			// MERGED MRs, so gate this per-MR commits fetch on state=="merged" to drop
			// a per-MR API call for every open/closed MR (cuts the call volume).
			// Best-effort.
			if rpr.State == "merged" {
				if first, ferr := gl.firstCommitAt(ctx, fullName, mr.IID); ferr == nil && !first.IsZero() {
					rpr.FirstCommitAt = first
				}
			}
			out = append(out, rpr)
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

// gitlabMRsQuery pages MRs (50/page) with their approvers and first commit in ONE
// request. iid/state come back as strings on the GitLab GraphQL schema.
const gitlabMRsQuery = `query($path:ID!,$cur:String){
  project(fullPath:$path) {
    mergeRequests(first:50, after:$cur, sort:CREATED_ASC) {
      pageInfo { hasNextPage endCursor }
      nodes {
        iid title description state createdAt mergedAt
        author { username }
        diffStatsSummary { additions deletions fileCount }
        commitsWithoutMergeCommits(first:1) { nodes { authoredDate committedDate } }
        approvedBy { nodes { username } }
      }
    }
  }
}`

// gitlabMRsResponse mirrors gitlabMRsQuery's "data".
type gitlabMRsResponse struct {
	Project struct {
		MergeRequests struct {
			PageInfo struct {
				HasNextPage bool   `json:"hasNextPage"`
				EndCursor   string `json:"endCursor"`
			} `json:"pageInfo"`
			Nodes []gitlabMRNode `json:"nodes"`
		} `json:"mergeRequests"`
	} `json:"project"`
}

type gitlabMRNode struct {
	IID         string     `json:"iid"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	State       string     `json:"state"` // opened | closed | merged | locked
	CreatedAt   *time.Time `json:"createdAt"`
	MergedAt    *time.Time `json:"mergedAt"`
	Author      *struct {
		Username string `json:"username"`
	} `json:"author"`
	DiffStatsSummary *struct {
		Additions int `json:"additions"`
		Deletions int `json:"deletions"`
		FileCount int `json:"fileCount"`
	} `json:"diffStatsSummary"`
	CommitsWithoutMergeCommits struct {
		Nodes []struct {
			AuthoredDate  *time.Time `json:"authoredDate"`
			CommittedDate *time.Time `json:"committedDate"`
		} `json:"nodes"`
	} `json:"commitsWithoutMergeCommits"`
	ApprovedBy struct {
		Nodes []struct {
			Username string `json:"username"`
		} `json:"nodes"`
	} `json:"approvedBy"`
}

// ListPullRequestsWithReviews fetches all MRs + their approvals (→ reviews) + each
// MR's first commit via GraphQL (50 MRs/request). On ANY GraphQL error it logs a
// WARN and falls back to the REST ListPullRequests path (usedGraphQL=false).
func (gl *gitlabProvider) ListPullRequestsWithReviews(ctx context.Context, fullName string) ([]RemotePR, map[int][]RemoteReview, bool, error) {
	prs, reviews, err := gl.graphQLMRs(ctx, fullName)
	if err != nil {
		slog.Warn("gitlab: graphql MR fetch failed, falling back to REST", "repo", fullName, "err", err)
		restPRs, rerr := gl.ListPullRequests(ctx, fullName)
		if rerr != nil {
			return nil, nil, false, rerr
		}
		return restPRs, nil, false, nil
	}
	return prs, reviews, true, nil
}

// graphQLMRs runs the paged GraphQL MR query and maps the result to RemotePR plus
// a per-MR review map derived from approvals.
func (gl *gitlabProvider) graphQLMRs(ctx context.Context, fullName string) ([]RemotePR, map[int][]RemoteReview, error) {
	var (
		prs     []RemotePR
		reviews = map[int][]RemoteReview{}
		cursor  string
	)
	for {
		vars := map[string]any{"path": fullName}
		if cursor != "" {
			vars["cur"] = cursor
		} else {
			vars["cur"] = nil
		}
		var data gitlabMRsResponse
		if err := graphQLDo(ctx, gl.http, gl.graphQLEndpoint, "Bearer", gl.token, gitlabMRsQuery, vars, &data); err != nil {
			return nil, nil, err
		}
		for _, n := range data.Project.MergeRequests.Nodes {
			pr, revs := mapGitLabMRNode(n)
			prs = append(prs, pr)
			if len(revs) > 0 {
				reviews[pr.Number] = revs
			}
		}
		if !data.Project.MergeRequests.PageInfo.HasNextPage {
			break
		}
		cursor = data.Project.MergeRequests.PageInfo.EndCursor
	}
	return prs, reviews, nil
}

// mapGitLabMRNode maps one GraphQL MR node to a RemotePR (with FirstCommitAt) and
// the MR's []RemoteReview derived from approvals (→ "approved").
func mapGitLabMRNode(n gitlabMRNode) (RemotePR, []RemoteReview) {
	pr := RemotePR{
		ExternalID: n.IID, // GraphQL has no global DB id here; iid is the stable per-project ref
		Number:     atoiSafe(n.IID),
		Title:      n.Title,
		Body:       n.Description,
	}
	if n.Author != nil {
		pr.AuthorLogin = n.Author.Username
	}
	if n.CreatedAt != nil {
		pr.CreatedAt = *n.CreatedAt
	}
	if n.DiffStatsSummary != nil {
		pr.Additions = n.DiffStatsSummary.Additions
		pr.Deletions = n.DiffStatsSummary.Deletions
		pr.ChangedFiles = n.DiffStatsSummary.FileCount
	}
	switch strings.ToLower(n.State) {
	case "merged":
		pr.State = "merged"
		if n.MergedAt != nil {
			t := *n.MergedAt
			pr.MergedAt = &t
		}
	case "closed", "locked":
		pr.State = "closed"
	default:
		pr.State = "open"
	}
	if len(n.CommitsWithoutMergeCommits.Nodes) > 0 {
		c := n.CommitsWithoutMergeCommits.Nodes[0]
		switch {
		case c.AuthoredDate != nil:
			pr.FirstCommitAt = *c.AuthoredDate
		case c.CommittedDate != nil:
			pr.FirstCommitAt = *c.CommittedDate
		}
	}
	// Reviews from approvals → "approved". Submission time is unavailable on the
	// approvedBy node, so fall back to mergedAt/createdAt (best-effort, matches the
	// REST approximation's timestamp behaviour).
	when := time.Now().UTC()
	switch {
	case n.MergedAt != nil:
		when = *n.MergedAt
	case n.CreatedAt != nil:
		when = *n.CreatedAt
	}
	var revs []RemoteReview
	seen := map[string]bool{}
	for _, a := range n.ApprovedBy.Nodes {
		if a.Username == "" || seen[a.Username] {
			continue
		}
		seen[a.Username] = true
		revs = append(revs, RemoteReview{
			ReviewerLogin: a.Username,
			State:         "approved",
			SubmittedAt:   when,
		})
	}
	return pr, revs
}

// atoiSafe parses an integer string, returning 0 on error (GitLab iid).
func atoiSafe(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

// firstCommitAt returns the earliest authored/committed date among an MR's commits.
func (gl *gitlabProvider) firstCommitAt(ctx context.Context, fullName string, mrIID int64) (time.Time, error) {
	opts := &gogitlab.GetMergeRequestCommitsOptions{ListOptions: gogitlab.ListOptions{PerPage: 100}}
	var earliest time.Time
	for {
		commits, resp, err := glDo(ctx, func() ([]*gogitlab.Commit, *gogitlab.Response, error) {
			return gl.client.MergeRequests.GetMergeRequestCommits(fullName, mrIID, opts, gogitlab.WithContext(ctx))
		})
		if err != nil {
			return time.Time{}, err
		}
		for _, c := range commits {
			var t time.Time
			switch {
			case c.AuthoredDate != nil:
				t = *c.AuthoredDate
			case c.CommittedDate != nil:
				t = *c.CommittedDate
			case c.CreatedAt != nil:
				t = *c.CreatedAt
			}
			if t.IsZero() {
				continue
			}
			if earliest.IsZero() || t.Before(earliest) {
				earliest = t
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return earliest, nil
}

func (gl *gitlabProvider) ListCommits(ctx context.Context, fullName string, since time.Time) ([]RemoteCommit, error) {
	opts := &gogitlab.ListCommitsOptions{ListOptions: gogitlab.ListOptions{PerPage: 100}}
	if !since.IsZero() {
		s := since
		opts.Since = &s
	}
	var out []RemoteCommit
	for {
		commits, resp, err := glDo(ctx, func() ([]*gogitlab.Commit, *gogitlab.Response, error) {
			return gl.client.Commits.ListCommits(fullName, opts, gogitlab.WithContext(ctx))
		})
		if err != nil {
			return nil, fmt.Errorf("gitlab: list commits %s: %w", fullName, err)
		}
		for _, c := range commits {
			if c == nil || c.ID == "" {
				continue
			}
			rc := RemoteCommit{
				SHA:         c.ID,
				AuthorLogin: c.AuthorName,
				AuthorEmail: c.AuthorEmail,
				Message:     c.Title,
			}
			switch {
			case c.CommittedDate != nil:
				rc.CommittedAt = *c.CommittedDate
			case c.AuthoredDate != nil:
				rc.CommittedAt = *c.AuthoredDate
			case c.CreatedAt != nil:
				rc.CommittedAt = *c.CreatedAt
			}
			out = append(out, rc)
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

// ListReviews approximates GitLab reviews from (1) MR approvals and (2) review
// notes/discussions left by users other than the author. GitLab has no
// first-class review object, so approvals map to "approved" and non-author,
// non-system notes map to "commented". Submission time uses the approval/note
// timestamp where available, falling back to the MR's last-update time.
func (gl *gitlabProvider) ListReviews(ctx context.Context, fullName string, prNumber int) ([]RemoteReview, error) {
	mrIID := int64(prNumber)
	var out []RemoteReview
	// dedupe (login, state) so a reviewer who both approved and commented yields at
	// most one row per state; the store's unique key further protects idempotency.
	seen := map[string]bool{}

	// (1) Approvals → "approved". The configuration endpoint returns approved_by +
	// an updated_at we can use as the approval time.
	appr, _, apprErr := glDo(ctx, func() (*gogitlab.MergeRequestApprovals, *gogitlab.Response, error) {
		return gl.client.MergeRequestApprovals.GetConfiguration(fullName, mrIID, gogitlab.WithContext(ctx))
	})
	if apprErr == nil && appr != nil {
		when := time.Now().UTC()
		if appr.UpdatedAt != nil {
			when = *appr.UpdatedAt
		}
		for _, a := range appr.ApprovedBy {
			if a == nil || a.User == nil || a.User.Username == "" {
				continue
			}
			key := a.User.Username + "|approved"
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, RemoteReview{
				ReviewerLogin: a.User.Username,
				State:         "approved",
				SubmittedAt:   when,
			})
		}
	}

	// (2) Reviewer notes → "commented". Non-system notes authored by someone other
	// than the MR author are review activity.
	nopts := &gogitlab.ListMergeRequestNotesOptions{ListOptions: gogitlab.ListOptions{PerPage: 100}}
	for {
		notes, resp, err := glDo(ctx, func() ([]*gogitlab.Note, *gogitlab.Response, error) {
			return gl.client.Notes.ListMergeRequestNotes(fullName, mrIID, nopts, gogitlab.WithContext(ctx))
		})
		if err != nil {
			break // best-effort: approvals may already have produced rows
		}
		for _, n := range notes {
			if n == nil || n.System || n.Author.Username == "" {
				continue
			}
			key := n.Author.Username + "|commented"
			if seen[key] {
				continue
			}
			seen[key] = true
			when := time.Now().UTC()
			if n.CreatedAt != nil {
				when = *n.CreatedAt
			}
			out = append(out, RemoteReview{
				ReviewerLogin: n.Author.Username,
				State:         "commented",
				SubmittedAt:   when,
			})
		}
		if resp.NextPage == 0 {
			break
		}
		nopts.Page = resp.NextPage
	}
	return out, nil
}

func (gl *gitlabProvider) ListDeployments(ctx context.Context, fullName string) ([]RemoteDeployment, error) {
	opts := &gogitlab.ListProjectDeploymentsOptions{ListOptions: gogitlab.ListOptions{PerPage: 100}}
	var out []RemoteDeployment
	for {
		deps, resp, err := glDo(ctx, func() ([]*gogitlab.Deployment, *gogitlab.Response, error) {
			return gl.client.Deployments.ListProjectDeployments(fullName, opts, gogitlab.WithContext(ctx))
		})
		if err != nil {
			return nil, fmt.Errorf("gitlab: list deployments %s: %w", fullName, err)
		}
		for _, d := range deps {
			rd := RemoteDeployment{
				ExternalID: fmt.Sprintf("%d", d.ID),
				Status:     normaliseDeployStatus(d.Status),
				SHA:        d.SHA,
			}
			if d.Environment != nil {
				rd.Environment = d.Environment.Name
			}
			switch {
			case d.Deployable.FinishedAt != nil:
				rd.DeployedAt = *d.Deployable.FinishedAt
			case d.UpdatedAt != nil:
				rd.DeployedAt = *d.UpdatedAt
			case d.CreatedAt != nil:
				rd.DeployedAt = *d.CreatedAt
			}
			out = append(out, rd)
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

func (gl *gitlabProvider) UpdateIssueState(ctx context.Context, fullName string, number int, state string) error {
	// GitLab uses "close" / "reopen" as state_event, but the API also accepts
	// StateEvent field. Map our canonical states.
	var se string
	switch state {
	case "closed":
		se = "close"
	default:
		se = "reopen"
	}
	opts := &gogitlab.UpdateIssueOptions{StateEvent: &se}
	_, _, err := glDo(ctx, func() (*gogitlab.Issue, *gogitlab.Response, error) {
		return gl.client.Issues.UpdateIssue(fullName, int64(number), opts, gogitlab.WithContext(ctx))
	})
	if err != nil {
		return fmt.Errorf("gitlab: update issue state %s#%d: %w", fullName, number, err)
	}
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// splitFullName splits "owner/name" into its two components.
func splitFullName(fullName string) (owner, name string, err error) {
	for i := 0; i < len(fullName); i++ {
		if fullName[i] == '/' {
			return fullName[:i], fullName[i+1:], nil
		}
	}
	return "", "", fmt.Errorf("sync: invalid repo full name %q (expected owner/name)", fullName)
}

// normaliseState maps platform-specific state strings to our canonical set.
func normaliseState(s string) string {
	switch s {
	case "closed", "merged":
		return s
	default:
		return "open"
	}
}

// normaliseReviewState maps a platform review state to the lowercase canonical
// set used by the pr_reviews table: approved | changes_requested | commented |
// dismissed. GitHub emits UPPER_CASE (APPROVED, CHANGES_REQUESTED, COMMENTED,
// DISMISSED); unknown values fall back to "commented" (a review happened).
func normaliseReviewState(s string) string {
	switch strings.ToUpper(s) {
	case "APPROVED":
		return "approved"
	case "CHANGES_REQUESTED":
		return "changes_requested"
	case "DISMISSED":
		return "dismissed"
	case "COMMENTED":
		return "commented"
	default:
		return "commented"
	}
}

// normaliseDeployStatus maps platform deployment/status strings to the
// deployments.status set: "success" | "failure". Anything that is not an
// explicit failure/error is treated as success (matches store.InsertDeployment).
func normaliseDeployStatus(s string) string {
	switch strings.ToLower(s) {
	case "failure", "failed", "error", "canceled", "cancelled":
		return "failure"
	default:
		return "success"
	}
}
