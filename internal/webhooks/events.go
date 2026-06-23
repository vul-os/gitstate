// Package webhooks — events.go
// Parses inbound GitHub/GitLab webhook payloads and applies them as real-time
// updates inside an org-scoped transaction (db.WithOrg → RLS active):
//
//   - push           → upsert commits           (REUSE store.UpsertCommit)
//   - pull_request   → upsert PR                 (REUSE store.UpsertPR)
//   - issues         → upsert issue              (REUSE store.UpsertIssue, pool-based)
//   - deployment_status / workflow_run (GitHub),
//     Pipeline/Deployment Hook (GitLab)          → store.InsertDeployment + MTTR
//     incident open/close on failure→recovery.
//
// Unknown events are ignored (the caller returns 200). Bodies/secrets are never
// logged. is_agent is derived from author identity using the same conservative
// heuristic as the git engine.
package webhooks

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/store"
)

// issueRefRe matches issue references in PR/MR text — both bare "#123" and the
// closing-keyword forms ("Closes #123"). Mirrors the syncer's auto-progress
// parser so a webhook-driven PR re-derives the same linked-issue states the
// backfill would.
var issueRefRe = regexp.MustCompile(`(?i)(?:closes?|fixes?|resolves?)?\s*#(\d+)`)

// Result summarises what a delivery did (for the receiver's structured log — no
// payload contents, just counts).
type Result struct {
	Provider    string
	Event       string
	Commits     int
	PRs         int
	Issues      int
	Reviews     int
	Deployments int
	Incidents   int // opened(+) / resolved are reported via IncidentsClosed
	Closed      int
	Ignored     bool
}

// Process verifies-already-done; it dispatches a parsed event for an org. orgID
// is the resolved org; the tx is org-scoped. provider is "github"|"gitlab",
// event is the platform event name, body is the raw JSON.
func Process(ctx context.Context, database *db.DB, orgID, provider, event string, body []byte) (Result, error) {
	res := Result{Provider: provider, Event: event}
	switch provider {
	case "github":
		return processGitHub(ctx, database, orgID, event, body, res)
	case "gitlab":
		return processGitLab(ctx, database, orgID, event, body, res)
	default:
		res.Ignored = true
		return res, nil
	}
}

// ── GitHub ──────────────────────────────────────────────────────────────────────

type ghRepo struct {
	FullName string `json:"full_name"`
}

type ghCommit struct {
	ID        string    `json:"id"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
	Author    struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Username string `json:"username"`
	} `json:"author"`
	Added    []string `json:"added"`
	Removed  []string `json:"removed"`
	Modified []string `json:"modified"`
}

type ghPushPayload struct {
	Repository ghRepo     `json:"repository"`
	Commits    []ghCommit `json:"commits"`
}

type ghPRPayload struct {
	Action      string `json:"action"`
	Number      int    `json:"number"`
	Repository  ghRepo `json:"repository"`
	PullRequest struct {
		ID           int64      `json:"id"`
		Number       int        `json:"number"`
		Title        string     `json:"title"`
		Body         string     `json:"body"`
		State        string     `json:"state"`
		Merged       bool       `json:"merged"`
		MergedAt     *time.Time `json:"merged_at"`
		CreatedAt    time.Time  `json:"created_at"`
		Additions    int        `json:"additions"`
		Deletions    int        `json:"deletions"`
		ChangedFiles int        `json:"changed_files"`
		User         struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"pull_request"`
}

type ghIssuePayload struct {
	Action     string `json:"action"`
	Repository ghRepo `json:"repository"`
	Issue      struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		State  string `json:"state"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
		PullRequest *struct{} `json:"pull_request"` // present → it's actually a PR, skip
	} `json:"issue"`
}

type ghDeploymentStatusPayload struct {
	Repository       ghRepo `json:"repository"`
	DeploymentStatus struct {
		State       string    `json:"state"` // success | failure | error | pending | in_progress
		Environment string    `json:"environment"`
		CreatedAt   time.Time `json:"created_at"`
		ID          int64     `json:"id"`
	} `json:"deployment_status"`
	Deployment struct {
		SHA         string `json:"sha"`
		Environment string `json:"environment"`
		ID          int64  `json:"id"`
	} `json:"deployment"`
}

type ghWorkflowRunPayload struct {
	Action      string `json:"action"`
	Repository  ghRepo `json:"repository"`
	WorkflowRun struct {
		ID         int64     `json:"id"`
		Name       string    `json:"name"`
		HeadSHA    string    `json:"head_sha"`
		Status     string    `json:"status"`     // completed | in_progress | queued
		Conclusion string    `json:"conclusion"` // success | failure | cancelled | ...
		UpdatedAt  time.Time `json:"updated_at"`
		Event      string    `json:"event"`
	} `json:"workflow_run"`
}

// ghPRReviewPayload is the pull_request_review event. The review's submitter is
// review.user.login; the PR author (for self-review skipping) is
// pull_request.user.login. external_id keys the review row idempotently.
type ghPRReviewPayload struct {
	Action      string `json:"action"` // submitted | edited | dismissed
	Repository  ghRepo `json:"repository"`
	Review      struct {
		ID          int64      `json:"id"`
		State       string     `json:"state"` // approved | changes_requested | commented | dismissed
		SubmittedAt *time.Time `json:"submitted_at"`
		User        struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"review"`
	PullRequest struct {
		ID     int64 `json:"id"`
		Number int   `json:"number"`
		User   struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"pull_request"`
}

func processGitHub(ctx context.Context, database *db.DB, orgID, event string, body []byte, res Result) (Result, error) {
	switch event {
	case "push":
		var p ghPushPayload
		if err := json.Unmarshal(body, &p); err != nil {
			return res, fmt.Errorf("webhooks: parse github push: %w", err)
		}
		return ingestPush(ctx, database, orgID, "github", p.Repository.FullName, ghCommitsToRecords(p.Commits), res)

	case "pull_request":
		var p ghPRPayload
		if err := json.Unmarshal(body, &p); err != nil {
			return res, fmt.Errorf("webhooks: parse github pr: %w", err)
		}
		state := "open"
		if p.PullRequest.Merged || p.PullRequest.MergedAt != nil {
			state = "merged"
		} else if p.PullRequest.State == "closed" {
			state = "closed"
		}
		pr := store.PullRequest{
			OrgID:        orgID,
			Platform:     "github",
			ExternalID:   strconv.FormatInt(p.PullRequest.ID, 10),
			Number:       p.PullRequest.Number,
			Title:        p.PullRequest.Title,
			AuthorLogin:  p.PullRequest.User.Login,
			State:        state,
			Additions:    p.PullRequest.Additions,
			Deletions:    p.PullRequest.Deletions,
			ChangedFiles: p.PullRequest.ChangedFiles,
			CreatedAt:    p.PullRequest.CreatedAt,
		}
		if p.PullRequest.MergedAt != nil {
			pr.MergedAt = *p.PullRequest.MergedAt
		}
		// Re-derive linked-issue auto-progress from the PR title+body (open PR →
		// in_progress, merged PR → done), mirroring the backfill syncer.
		return ingestPR(ctx, database, orgID, "github", p.Repository.FullName, pr,
			p.PullRequest.Title+"\n"+p.PullRequest.Body, res)

	case "pull_request_review":
		var p ghPRReviewPayload
		if err := json.Unmarshal(body, &p); err != nil {
			return res, fmt.Errorf("webhooks: parse github pull_request_review: %w", err)
		}
		// Only a freshly submitted review is a new review event. edited/dismissed
		// don't add review work (dismissal is reflected via state on re-submit).
		if p.Action != "submitted" {
			res.Ignored = true
			return res, nil
		}
		var submitted time.Time
		if p.Review.SubmittedAt != nil {
			submitted = *p.Review.SubmittedAt
		}
		rv := reviewRecord{
			PRExternalID:  strconv.FormatInt(p.PullRequest.ID, 10),
			PRAuthorLogin: p.PullRequest.User.Login,
			ReviewerLogin: p.Review.User.Login,
			State:         normaliseReviewState(p.Review.State),
			ExternalID:    strconv.FormatInt(p.Review.ID, 10),
			SubmittedAt:   submitted,
		}
		return ingestReview(ctx, database, orgID, "github", p.Repository.FullName, rv, res)

	case "issues":
		var p ghIssuePayload
		if err := json.Unmarshal(body, &p); err != nil {
			return res, fmt.Errorf("webhooks: parse github issue: %w", err)
		}
		if p.Issue.PullRequest != nil {
			res.Ignored = true
			return res, nil // it's a PR masquerading as an issue event
		}
		labels := make([]string, 0, len(p.Issue.Labels))
		for _, l := range p.Issue.Labels {
			labels = append(labels, l.Name)
		}
		iss := store.IssueUpsert{
			OrgID:      orgID,
			Source:     "git",
			Platform:   "github",
			ExternalID: strconv.Itoa(p.Issue.Number),
			Number:     p.Issue.Number,
			Title:      p.Issue.Title,
			Body:       p.Issue.Body,
			State:      p.Issue.State,
			Labels:     labels,
		}
		return ingestIssue(ctx, database, orgID, "github", p.Repository.FullName, iss, res)

	case "deployment_status":
		var p ghDeploymentStatusPayload
		if err := json.Unmarshal(body, &p); err != nil {
			return res, fmt.Errorf("webhooks: parse github deployment_status: %w", err)
		}
		st := p.DeploymentStatus.State
		// Only terminal states map to a deployment row.
		if st != "success" && st != "failure" && st != "error" {
			res.Ignored = true
			return res, nil
		}
		env := p.DeploymentStatus.Environment
		if env == "" {
			env = p.Deployment.Environment
		}
		dep := store.DeploymentInput{
			OrgID:       orgID,
			Environment: env,
			Status:      normalizeStatus(st),
			SHA:         p.Deployment.SHA,
			Source:      "github_actions",
			ExternalID:  "ghds-" + strconv.FormatInt(p.DeploymentStatus.ID, 10),
			DeployedAt:  firstNonZeroTime(p.DeploymentStatus.CreatedAt),
		}
		return ingestDeployment(ctx, database, orgID, "github", p.Repository.FullName, dep, res)

	case "workflow_run":
		var p ghWorkflowRunPayload
		if err := json.Unmarshal(body, &p); err != nil {
			return res, fmt.Errorf("webhooks: parse github workflow_run: %w", err)
		}
		// Only completed deploy-ish workflows become deployments.
		if p.Action != "completed" || p.WorkflowRun.Status != "completed" {
			res.Ignored = true
			return res, nil
		}
		if !looksLikeDeploy(p.WorkflowRun.Name) {
			res.Ignored = true
			return res, nil
		}
		// A cancelled/skipped run is neither a success nor a failure — recording it
		// as a "failure" would inflate the change-failure rate and open a spurious
		// incident. Ignore it (200 no-op).
		if isNonOutcomeConclusion(p.WorkflowRun.Conclusion) {
			res.Ignored = true
			return res, nil
		}
		dep := store.DeploymentInput{
			OrgID:       orgID,
			Environment: "production",
			Status:      normalizeStatus(p.WorkflowRun.Conclusion),
			SHA:         p.WorkflowRun.HeadSHA,
			Source:      "github_actions",
			ExternalID:  "ghwf-" + strconv.FormatInt(p.WorkflowRun.ID, 10),
			DeployedAt:  firstNonZeroTime(p.WorkflowRun.UpdatedAt),
		}
		return ingestDeployment(ctx, database, orgID, "github", p.Repository.FullName, dep, res)

	default:
		res.Ignored = true
		return res, nil
	}
}

func ghCommitsToRecords(cs []ghCommit) []commitRecord {
	out := make([]commitRecord, 0, len(cs))
	for _, c := range cs {
		login := c.Author.Username
		if login == "" {
			login = c.Author.Name
		}
		out = append(out, commitRecord{
			SHA:         c.ID,
			Message:     c.Message,
			AuthorLogin: login,
			AuthorEmail: c.Author.Email,
			AuthorName:  c.Author.Name,
			CommittedAt: c.Timestamp,
			Changed:     len(c.Added) + len(c.Removed) + len(c.Modified),
		})
	}
	return out
}

// ── GitLab ──────────────────────────────────────────────────────────────────────

type glProject struct {
	PathWithNamespace string `json:"path_with_namespace"`
}

type glPushPayload struct {
	Project glProject `json:"project"`
	Commits []struct {
		ID        string    `json:"id"`
		Message   string    `json:"message"`
		Timestamp time.Time `json:"timestamp"`
		Author    struct {
			Name  string `json:"name"`
			Email string `json:"email"`
		} `json:"author"`
		Added    []string `json:"added"`
		Removed  []string `json:"removed"`
		Modified []string `json:"modified"`
	} `json:"commits"`
}

type glMRPayload struct {
	Project          glProject `json:"project"`
	ObjectAttributes struct {
		IID         int     `json:"iid"`
		ID          int64   `json:"id"`
		Title       string  `json:"title"`
		Description string  `json:"description"`
		State       string  `json:"state"` // opened | merged | closed | locked
		Action      string  `json:"action"`
		AuthorID    int64   `json:"author_id"`
		CreatedAt   string  `json:"created_at"`
		MergedAt    *string `json:"merged_at"`
	} `json:"object_attributes"`
	User struct {
		Username string `json:"username"`
	} `json:"user"`
}

// glNotePayload is the GitLab "Note Hook" (a comment). Only comments whose
// noteable_type is "MergeRequest" are treated as PR review activity. The MR's
// numeric id (object_attributes.noteable_id == merge_request.id) keys the review
// to the stored MR. GitLab's note payload does not carry the MR author's
// username, so AuthorLogin() returns "" and the self-review skip simply can't
// match here (a commenter is virtually never auto-skipped — acceptable).
type glNoteMergeRequest struct {
	ID  int64 `json:"id"`
	IID int   `json:"iid"`
}

// AuthorLogin returns the MR author's username if the note payload carries it.
// GitLab's note payload does not include it, so this is "" and the self-review
// skip simply can't match for note-driven reviews (acceptable — a commenter is
// virtually never the MR author in practice, and the worst case is one extra
// row, not a wrong one).
func (glNoteMergeRequest) AuthorLogin() string { return "" }

type glNotePayload struct {
	Project          glProject `json:"project"`
	ObjectAttributes struct {
		ID           int64  `json:"id"`
		NoteableType string `json:"noteable_type"` // MergeRequest | Issue | Commit | Snippet
		CreatedAt    string `json:"created_at"`
	} `json:"object_attributes"`
	MergeRequest glNoteMergeRequest `json:"merge_request"`
	User         struct {
		Username string `json:"username"`
	} `json:"user"`
}

type glIssuePayload struct {
	Project          glProject `json:"project"`
	ObjectAttributes struct {
		IID         int    `json:"iid"`
		Title       string `json:"title"`
		Description string `json:"description"`
		State       string `json:"state"` // opened | closed
	} `json:"object_attributes"`
	Labels []struct {
		Title string `json:"title"`
	} `json:"labels"`
}

type glPipelinePayload struct {
	Project          glProject `json:"project"`
	ObjectAttributes struct {
		ID         int64   `json:"id"`
		Status     string  `json:"status"` // success | failed | running | ...
		SHA        string  `json:"sha"`
		FinishedAt *string `json:"finished_at"`
	} `json:"object_attributes"`
}

type glDeploymentPayload struct {
	Project         glProject `json:"project"`
	Status          string    `json:"status"` // success | failed | running | ...
	DeployableID    int64     `json:"deployable_id"`
	Environment     string    `json:"environment"`
	ShortSHA        string    `json:"short_sha"`
	SHA             string    `json:"sha"`
	StatusChangedAt string    `json:"status_changed_at"`
}

func processGitLab(ctx context.Context, database *db.DB, orgID, event string, body []byte, res Result) (Result, error) {
	switch event {
	case "Push Hook", "Tag Push Hook":
		var p glPushPayload
		if err := json.Unmarshal(body, &p); err != nil {
			return res, fmt.Errorf("webhooks: parse gitlab push: %w", err)
		}
		recs := make([]commitRecord, 0, len(p.Commits))
		for _, c := range p.Commits {
			recs = append(recs, commitRecord{
				SHA:         c.ID,
				Message:     c.Message,
				AuthorLogin: c.Author.Name,
				AuthorEmail: c.Author.Email,
				AuthorName:  c.Author.Name,
				CommittedAt: c.Timestamp,
				Changed:     len(c.Added) + len(c.Removed) + len(c.Modified),
			})
		}
		return ingestPush(ctx, database, orgID, "gitlab", p.Project.PathWithNamespace, recs, res)

	case "Merge Request Hook":
		var p glMRPayload
		if err := json.Unmarshal(body, &p); err != nil {
			return res, fmt.Errorf("webhooks: parse gitlab mr: %w", err)
		}
		state := "open"
		switch p.ObjectAttributes.State {
		case "merged":
			state = "merged"
		case "closed", "locked":
			state = "closed"
		}
		pr := store.PullRequest{
			OrgID:       orgID,
			Platform:    "gitlab",
			ExternalID:  strconv.FormatInt(p.ObjectAttributes.ID, 10),
			Number:      p.ObjectAttributes.IID,
			Title:       p.ObjectAttributes.Title,
			AuthorLogin: p.User.Username,
			State:       state,
			CreatedAt:   parseGLTime(p.ObjectAttributes.CreatedAt),
		}
		if p.ObjectAttributes.MergedAt != nil {
			pr.MergedAt = parseGLTime(*p.ObjectAttributes.MergedAt)
		}
		// An approval/unapproval action on an MR is a review event (GitLab folds
		// approvals into the Merge Request Hook rather than a separate webhook).
		if act := p.ObjectAttributes.Action; act == "approved" || act == "unapproved" {
			rv := reviewRecord{
				PRExternalID:  strconv.FormatInt(p.ObjectAttributes.ID, 10),
				PRAuthorLogin: pr.AuthorLogin,
				ReviewerLogin: p.User.Username,
				State:         normaliseReviewState(act),
				ExternalID:    fmt.Sprintf("glapproval-%d-%s", p.ObjectAttributes.ID, p.User.Username),
				SubmittedAt:   time.Now().UTC(),
			}
			// Persist the PR state too (the MR may have changed), then the review.
			if r2, err := ingestPR(ctx, database, orgID, "gitlab", p.Project.PathWithNamespace, pr,
				p.ObjectAttributes.Title+"\n"+p.ObjectAttributes.Description, res); err == nil {
				res = r2
			}
			res.Ignored = false
			return ingestReview(ctx, database, orgID, "gitlab", p.Project.PathWithNamespace, rv, res)
		}
		return ingestPR(ctx, database, orgID, "gitlab", p.Project.PathWithNamespace, pr,
			p.ObjectAttributes.Title+"\n"+p.ObjectAttributes.Description, res)

	case "Note Hook":
		var p glNotePayload
		if err := json.Unmarshal(body, &p); err != nil {
			return res, fmt.Errorf("webhooks: parse gitlab note: %w", err)
		}
		// Only comments ON a merge request count as review activity; issue/commit/
		// snippet notes are not PR reviews.
		if !strings.EqualFold(p.ObjectAttributes.NoteableType, "MergeRequest") || p.MergeRequest.ID == 0 {
			res.Ignored = true
			return res, nil
		}
		rv := reviewRecord{
			PRExternalID:  strconv.FormatInt(p.MergeRequest.ID, 10),
			PRAuthorLogin: p.MergeRequest.AuthorLogin(),
			ReviewerLogin: p.User.Username,
			State:         "commented",
			ExternalID:    "glnote-" + strconv.FormatInt(p.ObjectAttributes.ID, 10),
			SubmittedAt:   parseGLTime(p.ObjectAttributes.CreatedAt),
		}
		return ingestReview(ctx, database, orgID, "gitlab", p.Project.PathWithNamespace, rv, res)

	case "Issue Hook":
		var p glIssuePayload
		if err := json.Unmarshal(body, &p); err != nil {
			return res, fmt.Errorf("webhooks: parse gitlab issue: %w", err)
		}
		state := "open"
		if p.ObjectAttributes.State == "closed" {
			state = "closed"
		}
		labels := make([]string, 0, len(p.Labels))
		for _, l := range p.Labels {
			labels = append(labels, l.Title)
		}
		iss := store.IssueUpsert{
			OrgID:      orgID,
			Source:     "git",
			Platform:   "gitlab",
			ExternalID: strconv.Itoa(p.ObjectAttributes.IID),
			Number:     p.ObjectAttributes.IID,
			Title:      p.ObjectAttributes.Title,
			Body:       p.ObjectAttributes.Description,
			State:      state,
			Labels:     labels,
		}
		return ingestIssue(ctx, database, orgID, "gitlab", p.Project.PathWithNamespace, iss, res)

	case "Deployment Hook":
		var p glDeploymentPayload
		if err := json.Unmarshal(body, &p); err != nil {
			return res, fmt.Errorf("webhooks: parse gitlab deployment: %w", err)
		}
		if p.Status != "success" && p.Status != "failed" {
			res.Ignored = true
			return res, nil
		}
		sha := p.SHA
		if sha == "" {
			sha = p.ShortSHA
		}
		env := p.Environment
		if env == "" {
			env = "production"
		}
		dep := store.DeploymentInput{
			OrgID:       orgID,
			Environment: env,
			Status:      normalizeStatus(p.Status),
			SHA:         sha,
			Source:      "gitlab_ci",
			ExternalID:  "gldep-" + strconv.FormatInt(p.DeployableID, 10),
			DeployedAt:  parseGLTime(p.StatusChangedAt),
		}
		return ingestDeployment(ctx, database, orgID, "gitlab", p.Project.PathWithNamespace, dep, res)

	case "Pipeline Hook":
		var p glPipelinePayload
		if err := json.Unmarshal(body, &p); err != nil {
			return res, fmt.Errorf("webhooks: parse gitlab pipeline: %w", err)
		}
		if p.ObjectAttributes.Status != "success" && p.ObjectAttributes.Status != "failed" {
			res.Ignored = true
			return res, nil
		}
		var at time.Time
		if p.ObjectAttributes.FinishedAt != nil {
			at = parseGLTime(*p.ObjectAttributes.FinishedAt)
		}
		dep := store.DeploymentInput{
			OrgID:       orgID,
			Environment: "production",
			Status:      normalizeStatus(p.ObjectAttributes.Status),
			SHA:         p.ObjectAttributes.SHA,
			Source:      "gitlab_ci",
			ExternalID:  "glpipe-" + strconv.FormatInt(p.ObjectAttributes.ID, 10),
			DeployedAt:  at,
		}
		return ingestDeployment(ctx, database, orgID, "gitlab", p.Project.PathWithNamespace, dep, res)

	default:
		res.Ignored = true
		return res, nil
	}
}

// ── shared ingestion (org-scoped) ───────────────────────────────────────────────

// commitRecord is the minimal commit shape both providers map into.
type commitRecord struct {
	SHA         string
	Message     string
	AuthorLogin string
	AuthorEmail string
	AuthorName  string
	CommittedAt time.Time
	Changed     int
}

func ingestPush(ctx context.Context, database *db.DB, orgID, platform, fullName string, commits []commitRecord, res Result) (Result, error) {
	if len(commits) == 0 {
		res.Ignored = true
		return res, nil
	}
	err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		repoID, e := store.RepoIDByExternal(ctx, tx, orgID, platform, fullName)
		if e != nil {
			return e // repo not connected → caller treats ErrNotFound as ignored
		}
		for _, c := range commits {
			if c.SHA == "" {
				continue
			}
			rec := &store.Commit{
				OrgID:       orgID,
				RepoID:      repoID,
				SHA:         c.SHA,
				AuthorLogin: c.AuthorLogin,
				AuthorEmail: c.AuthorEmail,
				IsAgent:     isAgentAuthor(c.AuthorName, c.AuthorEmail, c.Message),
				Message:     c.Message,
				CommittedAt: firstNonZeroTime(c.CommittedAt),
			}
			if e := store.UpsertCommit(ctx, tx, rec); e != nil {
				return e
			}
			res.Commits++
		}
		return store.TouchWebhookLastEvent(ctx, tx, orgID, platform)
	})
	if isRepoNotFound(err) {
		return Result{Provider: res.Provider, Event: res.Event, Ignored: true}, nil
	}
	return res, err
}

func ingestPR(ctx context.Context, database *db.DB, orgID, platform, fullName string, pr store.PullRequest, progressText string, res Result) (Result, error) {
	var repoID string
	err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		rid, e := store.RepoIDByExternal(ctx, tx, orgID, platform, fullName)
		if e != nil {
			return e
		}
		repoID = rid
		pr.RepoID = repoID
		if e := store.UpsertPR(ctx, tx, &pr); e != nil {
			return e
		}
		res.PRs++
		return store.TouchWebhookLastEvent(ctx, tx, orgID, platform)
	})
	if isRepoNotFound(err) {
		return Result{Provider: res.Provider, Event: res.Event, Ignored: true}, nil
	}
	if err != nil {
		return res, err
	}
	// Auto-progress: a PR linked to issue #N drives that issue's derived_state
	// (open PR → in_progress, merged PR → done). Best-effort — a derivation miss
	// must not fail the delivery (the PR itself is already persisted).
	applyIssueAutoProgress(ctx, database, orgID, repoID, pr.State, progressText)
	return res, nil
}

// applyIssueAutoProgress mirrors the backfill syncer: it parses issue references
// out of a PR's title+body and sets the linked issues' derived_state. "merged"
// wins over "open"; other PR states leave the issue untouched. Errors are
// swallowed (best-effort) so a derivation hiccup can't fail the webhook.
func applyIssueAutoProgress(ctx context.Context, database *db.DB, orgID, repoID, prState, text string) {
	var derived string
	switch prState {
	case "merged":
		derived = "done"
	case "open":
		derived = "in_progress"
	default:
		return
	}
	refs := parseIssueRefs(text)
	if len(refs) == 0 {
		return
	}
	wanted := map[int]bool{}
	for _, n := range refs {
		wanted[n] = true
	}
	issues, err := store.ListIssuesByRepo(ctx, database.Pool(), orgID, repoID)
	if err != nil {
		return
	}
	for _, iss := range issues {
		if !wanted[iss.Number] {
			continue
		}
		_ = store.SetDerivedState(ctx, database.Pool(), orgID, iss.ID, derived)
	}
}

// reviewRecord is the minimal review shape both providers map into. PRExternalID
// is the platform PR/MR id (UpsertPR keys on it); the receiver resolves it to the
// internal pr_id before writing the review.
type reviewRecord struct {
	PRExternalID  string
	PRAuthorLogin string
	ReviewerLogin string
	State         string
	ExternalID    string
	SubmittedAt   time.Time
}

// ingestReview resolves the PR's internal id and writes one review row. Self-
// reviews (reviewer == PR author) and empty reviewers are skipped — they are not
// the invisible review work Involvement credits. A PR not yet stored (review
// arrived before the PR event) is treated as ignored rather than an error.
func ingestReview(ctx context.Context, database *db.DB, orgID, platform, fullName string, rv reviewRecord, res Result) (Result, error) {
	if rv.ReviewerLogin == "" || strings.EqualFold(rv.ReviewerLogin, rv.PRAuthorLogin) {
		res.Ignored = true
		return res, nil
	}
	err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		repoID, e := store.RepoIDByExternal(ctx, tx, orgID, platform, fullName)
		if e != nil {
			return e
		}
		// Resolve the PR's internal UUID (reviews FK to pr_id). UpsertPR keys on
		// external_id, so a review for an as-yet-unsynced PR finds no row.
		var prID string
		e = tx.QueryRow(ctx,
			`SELECT id FROM pull_requests WHERE org_id=$1 AND repo_id=$2 AND external_id=$3`,
			orgID, repoID, rv.PRExternalID).Scan(&prID)
		if e != nil {
			if e == pgx.ErrNoRows {
				return store.ErrNotFound
			}
			return e
		}
		if e := store.UpsertPRReview(ctx, tx, store.PRReviewInput{
			OrgID:         orgID,
			RepoID:        repoID,
			PRID:          prID,
			ReviewerLogin: rv.ReviewerLogin,
			State:         rv.State,
			ExternalID:    rv.ExternalID,
			SubmittedAt:   rv.SubmittedAt,
		}); e != nil {
			return e
		}
		res.Reviews++
		return store.TouchWebhookLastEvent(ctx, tx, orgID, platform)
	})
	if isRepoNotFound(err) {
		return Result{Provider: res.Provider, Event: res.Event, Ignored: true}, nil
	}
	return res, err
}

// parseIssueRefs returns the unique issue numbers referenced in text (bare #N and
// closing-keyword forms). Mirrors the syncer's parser.
func parseIssueRefs(text string) []int {
	matches := issueRefRe.FindAllStringSubmatch(text, -1)
	seen := map[int]bool{}
	var out []int
	for _, m := range matches {
		n, err := strconv.Atoi(strings.TrimSpace(m[1]))
		if err != nil || n <= 0 || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// normaliseReviewState maps a platform review state to the pr_reviews canonical
// set (approved | changes_requested | commented | dismissed). GitHub emits
// UPPER_CASE; GitLab approval actions map to approved/changes_requested. Unknown
// values fall back to "commented" (a review still happened).
func normaliseReviewState(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "APPROVED", "APPROVAL":
		return "approved"
	case "CHANGES_REQUESTED", "UNAPPROVED", "UNAPPROVAL":
		return "changes_requested"
	case "DISMISSED":
		return "dismissed"
	case "COMMENTED", "COMMENT":
		return "commented"
	default:
		return "commented"
	}
}

func ingestIssue(ctx context.Context, database *db.DB, orgID, platform, fullName string, iss store.IssueUpsert, res Result) (Result, error) {
	// First resolve repo id + touch last_event under RLS.
	var repoID string
	err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		rid, e := store.RepoIDByExternal(ctx, tx, orgID, platform, fullName)
		if e != nil {
			return e
		}
		repoID = rid
		return store.TouchWebhookLastEvent(ctx, tx, orgID, platform)
	})
	if isRepoNotFound(err) {
		return Result{Provider: res.Provider, Event: res.Event, Ignored: true}, nil
	}
	if err != nil {
		return res, err
	}
	iss.RepoID = repoID
	// UpsertIssue is pool-based (sets app.current_org on its own connection).
	if e := store.UpsertIssue(ctx, database.Pool(), orgID, iss); e != nil {
		return res, e
	}
	res.Issues++
	return res, nil
}

// ingestDeployment inserts a deployment row and manages the MTTR incident
// lifecycle: a failure opens an incident (if none open for the repo); a success
// resolves any open incident for the repo (failure→recovery → MTTR sample).
func ingestDeployment(ctx context.Context, database *db.DB, orgID, platform, fullName string, dep store.DeploymentInput, res Result) (Result, error) {
	err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		// Repo is optional — a deployment can land even if the repo isn't tracked.
		if fullName != "" {
			if rid, e := store.RepoIDByExternal(ctx, tx, orgID, platform, fullName); e == nil {
				dep.RepoID = rid
			}
		}
		d, e := store.InsertDeployment(ctx, tx, dep)
		if e != nil {
			return e
		}
		res.Deployments++

		if d.Status == "failure" {
			open, e := store.HasOpenIncidentForRepo(ctx, tx, orgID, d.RepoID)
			if e != nil {
				return e
			}
			if !open {
				title := fmt.Sprintf("Failed deploy to %s", d.Environment)
				if _, e := store.InsertIncident(ctx, tx, store.IncidentInput{
					OrgID:    orgID,
					RepoID:   d.RepoID,
					Title:    title,
					Severity: "major",
					OpenedAt: d.DeployedAt,
				}); e != nil {
					return e
				}
				res.Incidents++
			}
		} else { // success → recovery: close open incidents for the repo
			n, e := store.ResolveOpenIncidentsForRepo(ctx, tx, orgID, d.RepoID, d.DeployedAt)
			if e != nil {
				return e
			}
			res.Closed += n
		}
		return store.TouchWebhookLastEvent(ctx, tx, orgID, platform)
	})
	return res, err
}

// ── helpers ───────────────────────────────────────────────────────────────────

func isRepoNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), store.ErrNotFound.Error())
}

func normalizeStatus(s string) string {
	switch strings.ToLower(s) {
	case "success", "succeeded", "passed":
		return "success"
	default:
		return "failure"
	}
}

// isNonOutcomeConclusion reports whether a workflow_run conclusion represents
// "didn't actually run to a pass/fail" — these must not be recorded as a failure
// (which would inflate change-failure rate / open spurious incidents).
func isNonOutcomeConclusion(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "cancelled", "canceled", "skipped":
		return true
	default:
		return false
	}
}

// looksLikeDeploy keeps workflow_run noise out: only deploy/release/ship-named
// workflows count as deployments. The name is split on non-alphanumeric runs into
// word tokens, so unambiguous keywords ("deploy"/"release"/"publish"/
// "production") match as a token prefix. Short ambiguous keywords ("cd"/"prod"/
// "ship") are too noisy to match mid-name — a "cd" token inside an unrelated name
// like "load-cd-tests" must NOT count — so they only match when the name is
// EXACTLY that one token (e.g. a workflow simply called "CD" or "Ship").
func looksLikeDeploy(name string) bool {
	prefixKW := []string{"deploy", "release", "publish", "production"}
	ambiguousKW := map[string]bool{"ship": true, "prod": true, "cd": true}

	tokens := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
	// Single-token name that IS an ambiguous deploy keyword → deploy.
	if len(tokens) == 1 && ambiguousKW[tokens[0]] {
		return true
	}
	for _, tok := range tokens {
		for _, kw := range prefixKW {
			if strings.HasPrefix(tok, kw) {
				return true
			}
		}
	}
	return false
}

func firstNonZeroTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t.UTC()
}

// parseGLTime parses GitLab's timestamp formats, falling back to now().
func parseGLTime(s string) time.Time {
	if s == "" {
		return time.Now().UTC()
	}
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02 15:04:05 -0700",
		"2006-01-02 15:04:05 MST",
		"2006-01-02T15:04:05.000-07:00",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Now().UTC()
}

// isAgentAuthor mirrors the conservative is_agent heuristic from the git engine
// (author identity + known agent trailer strings). Kept local so the webhooks
// package has no dependency on internal/git.
func isAgentAuthor(name, email, message string) bool {
	nameLower := strings.ToLower(name)
	emailLower := strings.ToLower(email)
	if strings.Contains(nameLower, "[bot]") {
		return true
	}
	if strings.HasSuffix(emailLower, "[bot]@users.noreply.github.com") {
		return true
	}
	patterns := []string{"[bot]", "claude", "copilot", "cursor", "devin", "codeium", "aider", "gitstate-agent", "github-actions", "dependabot"}
	msgLower := strings.ToLower(message)
	for _, line := range strings.Split(message, "\n") {
		lower := strings.ToLower(strings.TrimSpace(line))
		if strings.HasPrefix(lower, "co-authored-by:") {
			for _, pat := range patterns {
				if strings.Contains(lower, pat) {
					return true
				}
			}
		}
	}
	return strings.Contains(msgLower, "🤖 generated with")
}
