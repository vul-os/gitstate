// Package store — context_bundle.go
// Curated, token-efficient context assembly for the LLM/agent issue- and
// PR-context endpoints (Wave 2 of the AI/agent flywheel).
//
// There is no explicit issue↔PR link table in the schema, so relatedness is
// derived heuristically and CHEAPLY: shared repo, shared labels, and keyword
// overlap between the issue title and PR/commit subjects. Everything runs inside
// a db.WithOrg tx so RLS scopes results to the caller's org. Results are capped
// and trimmed so the bundle fits a context window.
package store

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Caps keep the bundle compact for an LLM context window.
const (
	maxRelatedPRs     = 5
	maxRelatedCommits = 8
	maxSimilarIssues  = 3
	maxCodeAreas      = 10
	bodyTrimChars     = 800
	commitSubjectMax  = 100
)

// IssueContextBundle is the curated payload an agent can start work from.
type IssueContextBundle struct {
	Issue         IssueSummary   `json:"issue"`
	Estimate      *EstimateBrief `json:"estimate,omitempty"`
	RelatedPRs    []PRBrief      `json:"relatedPRs"`
	RecentCommits []CommitBrief  `json:"recentCommits"`
	CodeAreas     []string       `json:"codeAreas"`
	SimilarIssues []SimilarIssue `json:"similarIssues"`
}

// IssueSummary is the trimmed issue itself.
type IssueSummary struct {
	ID         string   `json:"id"`
	Number     int      `json:"number,omitempty"`
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	State      string   `json:"state"`
	Labels     []string `json:"labels"`
	AssigneeID string   `json:"assigneeId,omitempty"`
	RepoID     string   `json:"repoId,omitempty"`
}

// PRBrief is a one-line PR with the signals an agent cares about.
type PRBrief struct {
	ID           string `json:"id"`
	Number       int    `json:"number,omitempty"`
	Title        string `json:"title"`
	State        string `json:"state"`
	Merged       bool   `json:"merged"`
	LeadTimeSecs *int64 `json:"leadTimeSecs,omitempty"`
}

// CommitBrief is a short-sha + subject line.
type CommitBrief struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	Author  string `json:"author,omitempty"`
	IsAgent bool   `json:"isAgent"`
}

// SimilarIssue is a past issue + how it was resolved (its merged PR, if any).
type SimilarIssue struct {
	ID           string   `json:"id"`
	Number       int      `json:"number,omitempty"`
	Title        string   `json:"title"`
	State        string   `json:"state"`
	SharedLabels []string `json:"sharedLabels"`
	ResolvedByPR *PRBrief `json:"resolvedByPR,omitempty"`
}

// EstimateBrief is the calibrated effort estimate for the issue/PR.
type EstimateBrief struct {
	Difficulty    float64  `json:"difficulty"`
	PredictedSecs *float64 `json:"predictedSecs,omitempty"`
	ActualSecs    *int64   `json:"actualSecs,omitempty"`
	SizeBucket    string   `json:"sizeBucket,omitempty"`
	ChangeType    string   `json:"changeType,omitempty"`
}

func trim(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func shortSHA(sha string) string {
	if len(sha) > 10 {
		return sha[:10]
	}
	return sha
}

// BuildIssueContext assembles the curated bundle for one issue. Runs inside an
// org-scoped tx. Returns ErrNotFound if the issue is not visible to the org.
func BuildIssueContext(ctx context.Context, tx pgx.Tx, orgID, issueID string) (*IssueContextBundle, error) {
	iss, err := GetIssue(ctx, tx, orgID, issueID)
	if err != nil {
		return nil, err
	}

	b := &IssueContextBundle{
		Issue: IssueSummary{
			ID:         iss.ID,
			Number:     iss.Number,
			Title:      iss.Title,
			Body:       trim(iss.Body, bodyTrimChars),
			State:      effectiveState(iss),
			Labels:     iss.Labels,
			AssigneeID: iss.AssigneeID,
			RepoID:     iss.RepoID,
		},
		RelatedPRs:    []PRBrief{},
		RecentCommits: []CommitBrief{},
		CodeAreas:     []string{},
		SimilarIssues: []SimilarIssue{},
	}

	// Effort estimate (calibrated) for the issue, if any.
	if est, err := estimateBriefForIssue(ctx, tx, orgID, issueID); err == nil && est != nil {
		b.Estimate = est
	}

	// Related PRs + recent commits live in the same repo. With no link table we
	// scope by repo and rank by recency. (Cross-repo relatedness would need the
	// issue's labels; repo scope is the cheapest strong signal.)
	if iss.RepoID != "" {
		b.RelatedPRs = relatedPRs(ctx, tx, orgID, iss.RepoID)
		b.RecentCommits = recentCommits(ctx, tx, iss.RepoID)
		b.CodeAreas = codeAreas(ctx, tx, orgID, iss.RepoID)
	}

	b.SimilarIssues = similarIssues(ctx, tx, orgID, iss)

	return b, nil
}

func effectiveState(iss *Issue) string {
	if iss.DerivedState != "" {
		return iss.DerivedState
	}
	return iss.State
}

func estimateBriefForIssue(ctx context.Context, tx pgx.Tx, orgID, issueID string) (*EstimateBrief, error) {
	const q = `
		SELECT difficulty::float8, predicted_secs, actual_secs,
		       COALESCE(size_bucket,''), COALESCE(change_type,'')
		FROM effort_estimates
		WHERE org_id = $1 AND issue_id = $2
		ORDER BY created_at DESC
		LIMIT 1`
	var eb EstimateBrief
	if err := tx.QueryRow(ctx, q, orgID, issueID).Scan(
		&eb.Difficulty, &eb.PredictedSecs, &eb.ActualSecs, &eb.SizeBucket, &eb.ChangeType,
	); err != nil {
		return nil, err
	}
	return &eb, nil
}

// relatedPRs returns recent PRs in the repo with their lead time (from
// cycle_times), capped and ordered newest-first.
func relatedPRs(ctx context.Context, tx pgx.Tx, orgID, repoID string) []PRBrief {
	const q = `
		SELECT p.id, COALESCE(p.number,0), COALESCE(p.title,''), COALESCE(p.state,''),
		       (p.merged_at IS NOT NULL) AS merged,
		       ct.lead_time_secs
		FROM pull_requests p
		LEFT JOIN LATERAL (
		    SELECT lead_time_secs FROM cycle_times c
		    WHERE c.pr_id = p.id AND c.lead_time_secs IS NOT NULL
		    ORDER BY computed_at DESC LIMIT 1
		) ct ON true
		WHERE p.org_id = $1 AND p.repo_id = $2
		ORDER BY COALESCE(p.merged_at, p.created_at) DESC
		LIMIT $3`
	rows, err := tx.Query(ctx, q, orgID, repoID, maxRelatedPRs)
	if err != nil {
		return []PRBrief{}
	}
	defer rows.Close()
	out := make([]PRBrief, 0, maxRelatedPRs)
	for rows.Next() {
		var pb PRBrief
		var lead *int64
		if err := rows.Scan(&pb.ID, &pb.Number, &pb.Title, &pb.State, &pb.Merged, &lead); err != nil {
			return out
		}
		pb.Title = trim(pb.Title, commitSubjectMax)
		pb.LeadTimeSecs = lead
		out = append(out, pb)
	}
	return out
}

// recentCommits returns recent commits in the repo as short-sha + subject.
func recentCommits(ctx context.Context, tx pgx.Tx, repoID string) []CommitBrief {
	const q = `
		SELECT sha, COALESCE(message,''), COALESCE(author_login,''), is_agent
		FROM commits
		WHERE repo_id = $1
		ORDER BY committed_at DESC
		LIMIT $2`
	rows, err := tx.Query(ctx, q, repoID, maxRelatedCommits)
	if err != nil {
		return []CommitBrief{}
	}
	defer rows.Close()
	out := make([]CommitBrief, 0, maxRelatedCommits)
	for rows.Next() {
		var sha, msg, author string
		var isAgent bool
		if err := rows.Scan(&sha, &msg, &author, &isAgent); err != nil {
			return out
		}
		out = append(out, CommitBrief{
			SHA:     shortSHA(sha),
			Subject: trim(firstLine(msg), commitSubjectMax),
			Author:  author,
			IsAgent: isAgent,
		})
	}
	return out
}

// codeAreas returns the top-level paths/areas historically touched in this repo,
// drawn from task_files (the projected plan-in-repo). Cheap proxy for "areas".
func codeAreas(ctx context.Context, tx pgx.Tx, orgID, repoID string) []string {
	const q = `
		SELECT DISTINCT path
		FROM task_files
		WHERE org_id = $1 AND repo_id = $2
		ORDER BY path
		LIMIT $3`
	rows, err := tx.Query(ctx, q, orgID, repoID, maxCodeAreas)
	if err != nil {
		return []string{}
	}
	defer rows.Close()
	out := make([]string, 0, maxCodeAreas)
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return out
		}
		out = append(out, p)
	}
	return out
}

// similarIssues finds past issues sharing ≥1 label with this one, ranked by the
// number of shared labels, capped. For each, attaches a resolving merged PR in
// the same repo when one is plausibly linked (same repo, merged) as a hint at how
// similar work was completed.
func similarIssues(ctx context.Context, tx pgx.Tx, orgID string, iss *Issue) []SimilarIssue {
	if len(iss.Labels) == 0 {
		return []SimilarIssue{}
	}
	const q = `
		SELECT id, COALESCE(number,0), title, state, COALESCE(derived_state,''),
		       COALESCE(repo_id::text,''), labels,
		       cardinality(ARRAY(SELECT unnest(labels) INTERSECT SELECT unnest($2::text[]))) AS shared
		FROM issues
		WHERE org_id = $1
		  AND id <> $3
		  AND labels && $2::text[]
		ORDER BY shared DESC, updated_at DESC
		LIMIT $4`
	rows, err := tx.Query(ctx, q, orgID, iss.Labels, iss.ID, maxSimilarIssues)
	if err != nil {
		return []SimilarIssue{}
	}
	defer rows.Close()

	var cands []similarCand
	for rows.Next() {
		var id, title, state, derived, repoID string
		var number, shared int
		var labels []string
		if err := rows.Scan(&id, &number, &title, &state, &derived, &repoID, &labels, &shared); err != nil {
			return briefsFromCands(cands)
		}
		shareSet := intersect(labels, iss.Labels)
		st := state
		if derived != "" {
			st = derived
		}
		cands = append(cands, similarCand{
			si: SimilarIssue{
				ID:           id,
				Number:       number,
				Title:        trim(title, commitSubjectMax),
				State:        st,
				SharedLabels: shareSet,
			},
			repoID: repoID,
		})
	}
	rows.Close()

	// Attach a resolving merged PR (most-recently-merged in the same repo) as the
	// "how it was resolved" hint. Best-effort; absence is fine.
	for i := range cands {
		if cands[i].repoID == "" {
			continue
		}
		if pr := latestMergedPR(ctx, tx, orgID, cands[i].repoID); pr != nil {
			cands[i].si.ResolvedByPR = pr
		}
	}
	return briefsFromCands(cands)
}

// similarCand pairs a built SimilarIssue with the repo we use to look up its
// resolving PR.
type similarCand struct {
	si     SimilarIssue
	repoID string
}

func briefsFromCands(cands []similarCand) []SimilarIssue {
	out := make([]SimilarIssue, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.si)
	}
	return out
}

func latestMergedPR(ctx context.Context, tx pgx.Tx, orgID, repoID string) *PRBrief {
	const q = `
		SELECT p.id, COALESCE(p.number,0), COALESCE(p.title,''), COALESCE(p.state,''),
		       ct.lead_time_secs
		FROM pull_requests p
		LEFT JOIN LATERAL (
		    SELECT lead_time_secs FROM cycle_times c
		    WHERE c.pr_id = p.id AND c.lead_time_secs IS NOT NULL
		    ORDER BY computed_at DESC LIMIT 1
		) ct ON true
		WHERE p.org_id = $1 AND p.repo_id = $2 AND p.merged_at IS NOT NULL
		ORDER BY p.merged_at DESC
		LIMIT 1`
	var pb PRBrief
	var lead *int64
	if err := tx.QueryRow(ctx, q, orgID, repoID).Scan(
		&pb.ID, &pb.Number, &pb.Title, &pb.State, &lead,
	); err != nil {
		return nil
	}
	pb.Title = trim(pb.Title, commitSubjectMax)
	pb.Merged = true
	pb.LeadTimeSecs = lead
	return &pb
}

func intersect(a, b []string) []string {
	set := make(map[string]bool, len(b))
	for _, x := range b {
		set[x] = true
	}
	var out []string
	seen := make(map[string]bool)
	for _, x := range a {
		if set[x] && !seen[x] {
			out = append(out, x)
			seen[x] = true
		}
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// ── PR context bundle ─────────────────────────────────────────────────────────

// PRContextBundle is the curated payload for the PR-context endpoint.
type PRContextBundle struct {
	PR            PRDetail       `json:"pr"`
	DiffSummary   DiffSummary    `json:"diffSummary"`
	CycleTimeSecs *int64         `json:"cycleTimeSecs,omitempty"`
	Estimate      *EstimateBrief `json:"estimate,omitempty"`
}

// PRDetail is the trimmed PR header.
type PRDetail struct {
	ID          string     `json:"id"`
	Number      int        `json:"number,omitempty"`
	Title       string     `json:"title"`
	State       string     `json:"state"`
	Merged      bool       `json:"merged"`
	AuthorLogin string     `json:"authorLogin,omitempty"`
	MergedAt    *time.Time `json:"mergedAt,omitempty"`
}

// DiffSummary is the size of the change (no raw diff — just the shape).
type DiffSummary struct {
	Additions    int `json:"additions"`
	Deletions    int `json:"deletions"`
	ChangedFiles int `json:"changedFiles"`
}

// BuildPRContext assembles the curated bundle for one PR. Runs inside an
// org-scoped tx. Returns ErrNotFound if the PR is not visible to the org.
func BuildPRContext(ctx context.Context, tx pgx.Tx, orgID, prID string) (*PRContextBundle, error) {
	pr, err := GetPR(ctx, tx, prID)
	if err != nil {
		return nil, err
	}

	b := &PRContextBundle{
		PR: PRDetail{
			ID:          pr.ID,
			Number:      pr.Number,
			Title:       trim(pr.Title, commitSubjectMax),
			State:       pr.State,
			Merged:      !pr.MergedAt.IsZero(),
			AuthorLogin: pr.AuthorLogin,
		},
		DiffSummary: DiffSummary{
			Additions:    pr.Additions,
			Deletions:    pr.Deletions,
			ChangedFiles: pr.ChangedFiles,
		},
	}
	if !pr.MergedAt.IsZero() {
		t := pr.MergedAt
		b.PR.MergedAt = &t
	}

	// Cycle time (lead time first commit → merge) from cycle_times.
	var lead *int64
	if err := tx.QueryRow(ctx, `
		SELECT lead_time_secs FROM cycle_times
		WHERE org_id = $1 AND pr_id = $2 AND lead_time_secs IS NOT NULL
		ORDER BY computed_at DESC LIMIT 1`, orgID, prID).Scan(&lead); err == nil {
		b.CycleTimeSecs = lead
	}

	// Effort estimate w/ predicted_secs for the PR.
	var eb EstimateBrief
	if err := tx.QueryRow(ctx, `
		SELECT difficulty::float8, predicted_secs, actual_secs,
		       COALESCE(size_bucket,''), COALESCE(change_type,'')
		FROM effort_estimates
		WHERE org_id = $1 AND pr_id = $2
		ORDER BY created_at DESC LIMIT 1`, orgID, prID).Scan(
		&eb.Difficulty, &eb.PredictedSecs, &eb.ActualSecs, &eb.SizeBucket, &eb.ChangeType,
	); err == nil {
		b.Estimate = &eb
	}

	return b, nil
}
