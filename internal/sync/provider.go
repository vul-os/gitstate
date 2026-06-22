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
	"context"
	"fmt"
	"time"

	gogithub "github.com/google/go-github/v66/github"
	gogitlab "gitlab.com/gitlab-org/api/client-go"
	"golang.org/x/oauth2"
)

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
	ListPullRequests(ctx context.Context, fullName string) ([]RemotePR, error)

	// UpdateIssueState writes an issue state change back to the platform.
	// state should be "open" or "closed"; platforms may reject other values.
	UpdateIssueState(ctx context.Context, fullName string, number int, state string) error
}

// ── GitHub implementation ─────────────────────────────────────────────────────

// githubProvider implements Provider using go-github.
type githubProvider struct {
	client *gogithub.Client
}

// NewGitHubProvider returns a Provider backed by the GitHub REST API.
// token is a personal access token or OAuth token with repo scope.
func NewGitHubProvider(ctx context.Context, token string) Provider {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	return &githubProvider{client: gogithub.NewClient(tc)}
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

	// 1. Repos the authenticated user owns / collaborates on. (/user/repos does NOT
	//    reliably return all repos of an org the user belongs to — see step 2.)
	uopts := &gogithub.RepositoryListByAuthenticatedUserOptions{ListOptions: gogithub.ListOptions{PerPage: 100}}
	for {
		repos, resp, err := g.client.Repositories.ListByAuthenticatedUser(ctx, uopts)
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
		page, resp, err := g.client.Organizations.List(ctx, "", oopts)
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
			repos, resp, err := g.client.Repositories.ListByOrg(ctx, org.GetLogin(), ropts)
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

func (g *githubProvider) ListIssues(ctx context.Context, fullName string) ([]RemoteIssue, error) {
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
		issues, resp, err := g.client.Issues.ListByRepo(ctx, owner, name, opts)
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
		prs, resp, err := g.client.PullRequests.List(ctx, owner, name, opts)
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
			out = append(out, rpr)
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
	_, _, err = g.client.Issues.Edit(ctx, owner, name, number, req)
	if err != nil {
		return fmt.Errorf("github: update issue state %s#%d: %w", fullName, number, err)
	}
	return nil
}

// ── GitLab implementation ─────────────────────────────────────────────────────

// gitlabProvider implements Provider using the gitlab client.
type gitlabProvider struct {
	client *gogitlab.Client
}

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
	return &gitlabProvider{client: client}, nil
}

func (gl *gitlabProvider) Platform() string { return "gitlab" }

func (gl *gitlabProvider) ListRepos(ctx context.Context) ([]RemoteRepo, error) {
	opts := &gogitlab.ListProjectsOptions{
		ListOptions: gogitlab.ListOptions{PerPage: 100},
	}
	var out []RemoteRepo
	for {
		projects, resp, err := gl.client.Projects.ListProjects(opts, gogitlab.WithContext(ctx))
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
		issues, resp, err := gl.client.Issues.ListProjectIssues(fullName, opts, gogitlab.WithContext(ctx))
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
		mrs, resp, err := gl.client.MergeRequests.ListProjectMergeRequests(fullName, opts, gogitlab.WithContext(ctx))
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
			out = append(out, rpr)
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
	_, _, err := gl.client.Issues.UpdateIssue(fullName, int64(number), opts, gogitlab.WithContext(ctx))
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
