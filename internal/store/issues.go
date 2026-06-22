package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Issue mirrors the issues table.
type Issue struct {
	ID           string
	OrgID        string
	ProjectID    string
	RepoID       string
	Source       string // git | native
	Platform     string // github | gitlab (when source=git)
	ExternalID   string
	Number       int
	Title        string
	Body         string
	State        string // open | in_progress | done | closed
	DerivedState string // computed from linked git activity
	AssigneeID   string
	Labels       []string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// IssueUpsert is the input for UpsertIssue (source='git' path from sync).
type IssueUpsert struct {
	OrgID      string
	RepoID     string
	Source     string
	Platform   string
	ExternalID string
	Number     int
	Title      string
	Body       string
	State      string
	Labels     []string
}

// IssueFilter holds optional filters for ListIssues.
type IssueFilter struct {
	Source    string // "git" | "native" | "" (all)
	State     string // "open" | "in_progress" | "done" | "closed" | "" (all)
	ProjectID string // filter by project; "" means all
}

// UpsertIssue inserts or updates a synced (source='git') issue.
// Uses ON CONFLICT (org_id, platform, external_id) to update.
//
// The sync goroutine runs outside a request context, so we open our own tx and
// set a transaction-LOCAL app.current_org (RLS scope) — NOT a session-level SET
// (which would leak the org to the next pool user) and NOT a bind param in a bare
// `SET` (which Postgres rejects, SQLSTATE 42601 — a bug this code previously had).
func UpsertIssue(ctx context.Context, pool *pgxpool.Pool, orgID string, u IssueUpsert) error {
	labels := u.Labels
	if labels == nil {
		labels = []string{}
	}

	// A transaction with a transaction-LOCAL org GUC: a bind param is invalid in a
	// bare `SET` (SQLSTATE 42601), and a session-level set_config would leak the org
	// to the next pool user — so use set_config(...,true) inside a tx (like WithOrg).
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: upsert issue: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org', $1, true)", orgID); err != nil {
		return fmt.Errorf("store: upsert issue: set org: %w", err)
	}

	const q = `
		INSERT INTO issues
			(org_id, repo_id, source, platform, external_id, number, title, body, state, labels)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (org_id, platform, external_id)
		DO UPDATE SET
			title      = EXCLUDED.title,
			body       = EXCLUDED.body,
			state      = EXCLUDED.state,
			labels     = EXCLUDED.labels,
			updated_at = now()
		WHERE issues.source = 'git'`

	var repoID *string
	if u.RepoID != "" {
		repoID = &u.RepoID
	}

	_, err = tx.Exec(ctx, q,
		orgID, repoID, u.Source, u.Platform, u.ExternalID,
		u.Number, u.Title, u.Body, u.State, labels,
	)
	if err != nil {
		return fmt.Errorf("store: upsert issue %s/%s: %w", u.Platform, u.ExternalID, err)
	}
	return tx.Commit(ctx)
}

// ListIssues returns issues for an org, optionally filtered.
// Runs inside a db.WithOrg tx so RLS is applied via SET LOCAL.
func ListIssues(ctx context.Context, tx pgx.Tx, orgID string, f IssueFilter) ([]Issue, error) {
	args := []any{orgID}
	conds := []string{"org_id = $1"}

	if f.Source != "" {
		args = append(args, f.Source)
		conds = append(conds, fmt.Sprintf("source = $%d", len(args)))
	}
	if f.State != "" {
		args = append(args, f.State)
		// Match either the persisted state or the derived state.
		conds = append(conds, fmt.Sprintf("(state = $%d OR derived_state = $%d)", len(args), len(args)))
	}
	if f.ProjectID != "" {
		args = append(args, f.ProjectID)
		conds = append(conds, fmt.Sprintf("project_id = $%d", len(args)))
	}

	q := fmt.Sprintf(`
		SELECT id, org_id, COALESCE(project_id::text,''), COALESCE(repo_id::text,''),
		       source, COALESCE(platform,''), COALESCE(external_id,''),
		       COALESCE(number,0), title, COALESCE(body,''),
		       state, COALESCE(derived_state,''), COALESCE(assignee_id::text,''),
		       labels, created_at, updated_at
		FROM issues
		WHERE %s
		ORDER BY updated_at DESC`, strings.Join(conds, " AND "))

	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list issues: %w", err)
	}
	defer rows.Close()

	return scanIssues(rows)
}

// ListIssuesByRepo returns all issues for a specific repo.
// Used by the sync goroutine to map issue numbers → IDs for derived-state writes.
// Runs in a tx with a transaction-local app.current_org (RLS-scoped, leak-safe).
func ListIssuesByRepo(ctx context.Context, pool *pgxpool.Pool, orgID, repoID string) ([]Issue, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: list issues by repo: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org', $1, true)", orgID); err != nil {
		return nil, fmt.Errorf("store: list issues by repo: set org: %w", err)
	}

	const q = `
		SELECT id, org_id, COALESCE(project_id::text,''), COALESCE(repo_id::text,''),
		       source, COALESCE(platform,''), COALESCE(external_id,''),
		       COALESCE(number,0), title, COALESCE(body,''),
		       state, COALESCE(derived_state,''), COALESCE(assignee_id::text,''),
		       labels, created_at, updated_at
		FROM issues
		WHERE org_id = $1 AND repo_id = $2`

	rows, err := tx.Query(ctx, q, orgID, repoID)
	if err != nil {
		return nil, fmt.Errorf("store: list issues by repo: %w", err)
	}
	out, err := scanIssues(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("store: list issues by repo: commit: %w", err)
	}
	return out, nil
}

// SetDerivedState updates the derived_state column for a single issue.
// Runs in a tx with a transaction-local app.current_org (RLS-scoped, leak-safe).
func SetDerivedState(ctx context.Context, pool *pgxpool.Pool, orgID, issueID, derivedState string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: set derived state: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org', $1, true)", orgID); err != nil {
		return fmt.Errorf("store: set derived state: set org: %w", err)
	}

	const q = `
		UPDATE issues SET derived_state = $3, updated_at = now()
		WHERE id = $1 AND org_id = $2`
	tag, err := tx.Exec(ctx, q, issueID, orgID, derivedState)
	if err != nil {
		return fmt.Errorf("store: set derived state %s: %w", issueID, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// NativeIssueInput is the input for CreateNativeIssue (source='native', decisions P1).
type NativeIssueInput struct {
	OrgID     string
	ProjectID string // optional
	Title     string
	Body      string
	Labels    []string
}

// CreateNativeIssue inserts a manually-created issue (non-git work, decisions P1).
// Runs inside a db.WithOrg tx so RLS is enforced.
func CreateNativeIssue(ctx context.Context, tx pgx.Tx, input NativeIssueInput) (*Issue, error) {
	labels := input.Labels
	if labels == nil {
		labels = []string{}
	}

	var projectID *string
	if input.ProjectID != "" {
		projectID = &input.ProjectID
	}

	const q = `
		INSERT INTO issues (org_id, project_id, source, title, body, labels, state)
		VALUES ($1, $2, 'native', $3, $4, $5, 'open')
		RETURNING id, org_id, COALESCE(project_id::text,''), COALESCE(repo_id::text,''),
		          source, COALESCE(platform,''), COALESCE(external_id,''),
		          COALESCE(number,0), title, COALESCE(body,''),
		          state, COALESCE(derived_state,''), COALESCE(assignee_id::text,''),
		          labels, created_at, updated_at`

	row := tx.QueryRow(ctx, q, input.OrgID, projectID, input.Title, input.Body, labels)
	out, err := scanIssues(singleRowToRows(row))
	if err != nil {
		return nil, fmt.Errorf("store: create native issue: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("store: create native issue: no row returned")
	}
	return &out[0], nil
}

// GetIssue fetches a single issue by ID inside an org-scoped tx.
// Returns ErrNotFound if not found or belonging to another org.
func GetIssue(ctx context.Context, tx pgx.Tx, orgID, issueID string) (*Issue, error) {
	const q = `
		SELECT id, org_id, COALESCE(project_id::text,''), COALESCE(repo_id::text,''),
		       source, COALESCE(platform,''), COALESCE(external_id,''),
		       COALESCE(number,0), title, COALESCE(body,''),
		       state, COALESCE(derived_state,''), COALESCE(assignee_id::text,''),
		       labels, created_at, updated_at
		FROM issues
		WHERE id = $1 AND org_id = $2`

	row := tx.QueryRow(ctx, q, issueID, orgID)
	out, err := scanIssues(singleRowToRows(row))
	if err != nil {
		// A missing row surfaces as pgx.ErrNoRows from Scan() inside the scan
		// loop — before singleRowAdapter.Err() gets a chance to remap it — so
		// collapse it to the sentinel callers expect. Without this, GetIssue on
		// an unknown id wraps ErrNoRows and the context handler returns 500
		// instead of a clean 404.
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: get issue: %w", err)
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return &out[0], nil
}

// UpdateIssueState sets the state of an issue in the DB.
// Runs inside a db.WithOrg tx.
func UpdateIssueState(ctx context.Context, tx pgx.Tx, orgID, issueID, state string) error {
	const q = `UPDATE issues SET state = $3, updated_at = now() WHERE id = $1 AND org_id = $2`
	tag, err := tx.Exec(ctx, q, issueID, orgID, state)
	if err != nil {
		return fmt.Errorf("store: update issue state: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ── scan helpers ──────────────────────────────────────────────────────────────

// rowScanner is satisfied by pgx.Rows and by singleRowAdapter below.
type rowScanner interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close()
}

func scanIssues(rows rowScanner) ([]Issue, error) {
	defer rows.Close()
	var out []Issue
	for rows.Next() {
		var iss Issue
		var labels []string
		if err := rows.Scan(
			&iss.ID, &iss.OrgID, &iss.ProjectID, &iss.RepoID,
			&iss.Source, &iss.Platform, &iss.ExternalID,
			&iss.Number, &iss.Title, &iss.Body,
			&iss.State, &iss.DerivedState, &iss.AssigneeID,
			&labels, &iss.CreatedAt, &iss.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan issue: %w", err)
		}
		if labels == nil {
			labels = []string{}
		}
		iss.Labels = labels
		out = append(out, iss)
	}
	return out, rows.Err()
}

// singleRowAdapter adapts pgx.Row (QueryRow result) into a rowScanner so
// CreateNativeIssue and GetIssue can reuse scanIssues.
type singleRowAdapter struct {
	row  pgx.Row
	used bool
	err  error
}

func singleRowToRows(row pgx.Row) rowScanner {
	return &singleRowAdapter{row: row}
}

func (a *singleRowAdapter) Next() bool {
	if a.used {
		return false
	}
	a.used = true
	return true
}

func (a *singleRowAdapter) Scan(dest ...any) error {
	a.err = a.row.Scan(dest...)
	return a.err
}

func (a *singleRowAdapter) Err() error {
	if errors.Is(a.err, pgx.ErrNoRows) {
		return nil // len==0 signals not-found to the caller
	}
	return a.err
}

func (a *singleRowAdapter) Close() {}
