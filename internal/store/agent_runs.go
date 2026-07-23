// Package store — agent_runs.go
// Wave 3 of the AI/agent flywheel: the WRITE PATH that lets AI agents record what
// they did. A logged run captures the goal, a diff summary, whether tests passed,
// the human's verdict (accepted/edited/reverted) and optional links to the PR or
// issue it produced — closing the loop into attribution + estimation.
//
// Org-scoped + FORCE RLS: every read/write here runs inside a db.WithOrg tx so the
// non-superuser app role only ever touches its own org's rows.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// DiffSummary (defined in context_bundle.go) is reused here: it is the structured
// size of the change an agent run produced and maps to the agent_runs.diff_summary
// jsonb column. An empty summary persists as the column default '{}'.

// AgentRun mirrors the agent_runs row (camelCase JSON for the API + CLI). Nullable
// columns surface as pointers so "unset" is distinguishable from a zero value.
type AgentRun struct {
	ID           string      `json:"id"`
	OrgID        string      `json:"orgId"`
	RepoID       *string     `json:"repoId,omitempty"`
	Goal         string      `json:"goal"`
	DiffSummary  DiffSummary `json:"diffSummary"`
	TestsPassed  *bool       `json:"testsPassed,omitempty"`
	HumanAction  string      `json:"humanAction,omitempty"`
	Iterations   *int        `json:"iterations,omitempty"`
	CostUSD      *float64    `json:"costUsd,omitempty"`
	SupervisorID *string     `json:"supervisorId,omitempty"`
	PRID         *string     `json:"prId,omitempty"`
	IssueID      *string     `json:"issueId,omitempty"`
	AgentName    string      `json:"agentName,omitempty"`
	Branch       string      `json:"branch,omitempty"`
	CreatedAt    time.Time   `json:"createdAt"`
}

// AgentRunInput carries the fields needed to log a new agent run. RepoID/PRID/
// IssueID/SupervisorID are optional (nil → SQL NULL). HumanAction is validated.
type AgentRunInput struct {
	RepoID       *string
	PRID         *string
	IssueID      *string
	SupervisorID *string
	Goal         string
	AgentName    string
	Branch       string
	DiffSummary  DiffSummary
	TestsPassed  *bool
	HumanAction  string
	Iterations   *int
	CostUSD      *float64
}

// validHumanActions is the closed set accepted for human_action. The empty string
// means "no human verdict yet" and is allowed.
var validHumanActions = map[string]bool{
	"":         true,
	"accepted": true,
	"edited":   true,
	"reverted": true,
}

// ErrInvalidHumanAction is returned by CreateAgentRun when human_action is not one
// of the accepted values. The API layer maps it to a 400.
var ErrInvalidHumanAction = errors.New("store: human_action must be one of accepted, edited, reverted")

// CreateAgentRun inserts one agent_runs row inside an existing org-scoped tx (from
// db.WithOrg). It validates human_action and persists diff_summary as jsonb.
func CreateAgentRun(ctx context.Context, tx pgx.Tx, orgID string, in AgentRunInput) (*AgentRun, error) {
	if !validHumanActions[in.HumanAction] {
		return nil, ErrInvalidHumanAction
	}

	diffJSON, err := json.Marshal(in.DiffSummary)
	if err != nil {
		return nil, fmt.Errorf("store: marshal diff summary: %w", err)
	}

	// human_action: persist NULL for the empty string so it reads back as "".
	var humanAction *string
	if in.HumanAction != "" {
		ha := in.HumanAction
		humanAction = &ha
	}
	var agentName, branch *string
	if in.AgentName != "" {
		agentName = &in.AgentName
	}
	if in.Branch != "" {
		branch = &in.Branch
	}

	const q = `
		INSERT INTO agent_runs
		    (org_id, repo_id, goal, diff_summary, tests_passed, human_action,
		     iterations, cost_usd, supervisor_id, pr_id, issue_id, agent_name, branch)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id, org_id, repo_id::text, COALESCE(goal,''), diff_summary,
		          tests_passed, COALESCE(human_action,''), iterations,
		          cost_usd::float8, supervisor_id::text, pr_id::text, issue_id::text,
		          COALESCE(agent_name,''), COALESCE(branch,''), created_at`

	row := tx.QueryRow(ctx, q,
		orgID,
		in.RepoID,
		in.Goal,
		diffJSON,
		in.TestsPassed,
		humanAction,
		in.Iterations,
		in.CostUSD,
		in.SupervisorID,
		in.PRID,
		in.IssueID,
		agentName,
		branch,
	)
	return scanAgentRun(row)
}

// AgentRunFilter narrows ListAgentRuns. Empty string fields are ignored. Limit is
// capped to agentRunMaxLimit (and defaulted when <= 0).
type AgentRunFilter struct {
	RepoID  string
	PRID    string
	IssueID string
	Agent   string
	Limit   int
}

const (
	agentRunDefaultLimit = 50
	agentRunMaxLimit     = 200
)

// ListAgentRuns returns agent runs for the org newest-first, applying the optional
// repo/pr/issue/agent filters. Runs inside an org-scoped tx (from db.WithOrg).
func ListAgentRuns(ctx context.Context, tx pgx.Tx, orgID string, f AgentRunFilter) ([]*AgentRun, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = agentRunDefaultLimit
	}
	if limit > agentRunMaxLimit {
		limit = agentRunMaxLimit
	}

	// Build the predicate with positional args. org_id is always bound first; RLS
	// also scopes it, but the explicit predicate keeps the query honest.
	args := []any{orgID}
	where := "org_id = $1"
	add := func(col, val string) {
		if val == "" {
			return
		}
		args = append(args, val)
		where += fmt.Sprintf(" AND %s = $%d", col, len(args))
	}
	add("repo_id", f.RepoID)
	add("pr_id", f.PRID)
	add("issue_id", f.IssueID)
	add("agent_name", f.Agent)

	args = append(args, limit)
	q := fmt.Sprintf(`
		SELECT id, org_id, repo_id::text, COALESCE(goal,''), diff_summary,
		       tests_passed, COALESCE(human_action,''), iterations,
		       cost_usd::float8, supervisor_id::text, pr_id::text, issue_id::text,
		       COALESCE(agent_name,''), COALESCE(branch,''), created_at
		FROM agent_runs
		WHERE %s
		ORDER BY created_at DESC, id DESC
		LIMIT $%d`, where, len(args))

	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list agent runs: %w", err)
	}
	defer rows.Close()

	out := make([]*AgentRun, 0, limit)
	for rows.Next() {
		r, err := scanAgentRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list agent runs rows: %w", err)
	}
	return out, nil
}

// scanAgentRun reads a single agent_runs row from any pgx.Row (QueryRow or Rows).
func scanAgentRun(row pgx.Row) (*AgentRun, error) {
	var r AgentRun
	var diffRaw []byte
	err := row.Scan(
		&r.ID,
		&r.OrgID,
		&r.RepoID,
		&r.Goal,
		&diffRaw,
		&r.TestsPassed,
		&r.HumanAction,
		&r.Iterations,
		&r.CostUSD,
		&r.SupervisorID,
		&r.PRID,
		&r.IssueID,
		&r.AgentName,
		&r.Branch,
		&r.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: scan agent run: %w", err)
	}
	if len(diffRaw) > 0 {
		if err := json.Unmarshal(diffRaw, &r.DiffSummary); err != nil {
			return nil, fmt.Errorf("store: unmarshal diff summary: %w", err)
		}
	}
	return &r, nil
}
