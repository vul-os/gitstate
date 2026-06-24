package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Repo mirrors the repos table.
type Repo struct {
	ID            string
	OrgID         string
	Platform      string // github | gitlab
	ExternalID    string
	FullName      string // owner/name
	DefaultBranch string
	CloneURL      string
	LastSyncedAt  *time.Time
	// LastAnalyzedSHA is the HEAD sha the deep blame/SZZ analysis last ran against.
	// AnalyzeRepoDeep skips re-analysis when the current HEAD equals this value, so
	// re-syncs of an unchanged repo never pay the (minutes-long) blame cost again.
	LastAnalyzedSHA string
	LastAnalyzedAt  *time.Time
	ProjectID       *string // user-created project this repo belongs to (nil = unassigned)
	CreatedAt       time.Time
	// Token is NOT stored in the DB — supplied at connect time and held in memory.
	// For persisted connections the caller must re-supply the token on sync.
	Token string `db:"-"`
}

// ConnectRepo upserts a repo connection for an org.
// On conflict (org_id, platform, external_id) it updates full_name and clone_url.
// MUST be called inside a db.WithOrg(ctx, orgID, …) tx so RLS (FORCE RLS on the
// non-superuser role) permits the INSERT/UPDATE (the bare pool would fail the
// WITH CHECK / RETURNING under forced RLS).
//
// For simplicity (no separate token table yet) the token is not persisted here.
// The API layer stores it in memory and passes it on each sync call.
func ConnectRepo(ctx context.Context, tx pgx.Tx, orgID, platform, externalID, fullName, defaultBranch, cloneURL string) (*Repo, error) {
	const q = `
		INSERT INTO repos (org_id, platform, external_id, full_name, default_branch, clone_url)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (org_id, platform, external_id)
		DO UPDATE SET
			full_name      = EXCLUDED.full_name,
			default_branch = COALESCE(EXCLUDED.default_branch, repos.default_branch),
			clone_url      = COALESCE(EXCLUDED.clone_url, repos.clone_url)
		RETURNING id, org_id, platform, external_id, full_name,
		          COALESCE(default_branch,''), COALESCE(clone_url,''),
		          last_synced_at, COALESCE(last_analyzed_sha,''), last_analyzed_at, created_at`

	var r Repo
	var lastSynced *time.Time
	err := tx.QueryRow(ctx, q,
		orgID, platform, externalID, fullName, defaultBranch, cloneURL,
	).Scan(
		&r.ID, &r.OrgID, &r.Platform, &r.ExternalID, &r.FullName,
		&r.DefaultBranch, &r.CloneURL, &lastSynced, &r.LastAnalyzedSHA, &r.LastAnalyzedAt, &r.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("store: connect repo: %w", err)
	}
	r.LastSyncedAt = lastSynced
	return &r, nil
}

// ListRepos returns all repos for the org. Runs inside an org-scoped context
// (caller must use db.WithOrg or ensure app.current_org is set in session).
func ListRepos(ctx context.Context, tx pgx.Tx, orgID string) ([]Repo, error) {
	const q = `
		SELECT id, org_id, platform, external_id, full_name,
		       COALESCE(default_branch,''), COALESCE(clone_url,''),
		       last_synced_at, COALESCE(last_analyzed_sha,''), last_analyzed_at, project_id::text, created_at
		FROM repos
		WHERE org_id = $1
		ORDER BY full_name`

	rows, err := tx.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("store: list repos: %w", err)
	}
	defer rows.Close()

	var out []Repo
	for rows.Next() {
		var r Repo
		var lastSynced *time.Time
		if err := rows.Scan(
			&r.ID, &r.OrgID, &r.Platform, &r.ExternalID, &r.FullName,
			&r.DefaultBranch, &r.CloneURL, &lastSynced, &r.LastAnalyzedSHA, &r.LastAnalyzedAt, &r.ProjectID, &r.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan repo: %w", err)
		}
		r.LastSyncedAt = lastSynced
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteRepo disconnects a repo and DELETES all of its derived data. Tables with
// repo_id ON DELETE CASCADE (commits, pull_requests, pr_reviews, commit_files,
// author_survival, bug_introductions, …) clear automatically when the repo row is
// removed; the few SET-NULL tables (issues, agent_runs, deployments, incidents)
// and cycle_times (keyed by pr_id) are deleted explicitly first so nothing lingers
// orphaned. Run inside db.WithOrg. Returns ErrNotFound if the repo doesn't exist.
func DeleteRepo(ctx context.Context, tx pgx.Tx, orgID, repoID string) error {
	// cycle_times reference the repo's PRs (which are about to cascade away).
	if _, err := tx.Exec(ctx,
		`DELETE FROM cycle_times WHERE org_id = $1 AND pr_id IN (SELECT id FROM pull_requests WHERE org_id = $1 AND repo_id = $2)`,
		orgID, repoID); err != nil {
		return fmt.Errorf("store: delete repo cycle_times: %w", err)
	}
	// SET-NULL tables: delete the repo's rows so they don't survive repo-less.
	for _, t := range []string{"issues", "agent_runs", "deployments", "incidents"} {
		if _, err := tx.Exec(ctx, `DELETE FROM `+t+` WHERE org_id = $1 AND repo_id = $2`, orgID, repoID); err != nil {
			return fmt.Errorf("store: delete repo %s: %w", t, err)
		}
	}
	tag, err := tx.Exec(ctx, `DELETE FROM repos WHERE org_id = $1 AND id = $2`, orgID, repoID)
	if err != nil {
		return fmt.Errorf("store: delete repo: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	// Remove contributors/identities now orphaned by the deleted data (keeps
	// linked members). So "remove a repo" also removes the people only it carried.
	if _, err := PruneOrphanContributors(ctx, tx, orgID); err != nil {
		return err
	}
	return nil
}

// SetRepoProject assigns a repo to a project (projectID nil/empty ⇒ unassign).
// Run inside db.WithOrg. The FK + RLS ensure the project belongs to the same org.
func SetRepoProject(ctx context.Context, tx pgx.Tx, orgID, repoID string, projectID *string) error {
	var pid any
	if projectID != nil && *projectID != "" {
		pid = *projectID
	}
	tag, err := tx.Exec(ctx,
		`UPDATE repos SET project_id = $3 WHERE org_id = $1 AND id = $2`, orgID, repoID, pid)
	if err != nil {
		return fmt.Errorf("store: set repo project: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetRepo fetches a single repo by ID. Runs inside an org-scoped tx (RLS enforces
// org ownership). Returns ErrNotFound if the repo doesn't exist or belongs to
// another org.
func GetRepo(ctx context.Context, tx pgx.Tx, orgID, repoID string) (*Repo, error) {
	const q = `
		SELECT id, org_id, platform, external_id, full_name,
		       COALESCE(default_branch,''), COALESCE(clone_url,''),
		       last_synced_at, COALESCE(last_analyzed_sha,''), last_analyzed_at, created_at
		FROM repos
		WHERE id = $1 AND org_id = $2`

	var r Repo
	var lastSynced *time.Time
	err := tx.QueryRow(ctx, q, repoID, orgID).Scan(
		&r.ID, &r.OrgID, &r.Platform, &r.ExternalID, &r.FullName,
		&r.DefaultBranch, &r.CloneURL, &lastSynced, &r.LastAnalyzedSHA, &r.LastAnalyzedAt, &r.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get repo: %w", err)
	}
	r.LastSyncedAt = lastSynced
	return &r, nil
}

// UpdateRepoSyncedAt sets last_synced_at = now() for the given repo.
// MUST be called inside a db.WithOrg(ctx, orgID, …) tx so RLS permits the UPDATE
// (the bare pool is blocked by FORCE RLS on the non-superuser role).
func UpdateRepoSyncedAt(ctx context.Context, tx pgx.Tx, orgID, repoID string) error {
	const q = `
		UPDATE repos SET last_synced_at = now()
		WHERE id = $1 AND org_id = $2`
	_, err := tx.Exec(ctx, q, repoID, orgID)
	if err != nil {
		return fmt.Errorf("store: update repo synced_at: %w", err)
	}
	return nil
}

// UpdateRepoAnalyzed records the HEAD sha the deep blame/SZZ analysis last ran
// against (last_analyzed_sha) and stamps last_analyzed_at = now(). A later
// AnalyzeRepoDeep compares the live HEAD to last_analyzed_sha and skips the
// (expensive) re-analysis when they match. MUST be called inside a
// db.WithOrg(ctx, orgID, …) tx so RLS permits the UPDATE.
func UpdateRepoAnalyzed(ctx context.Context, tx pgx.Tx, orgID, repoID, sha string) error {
	const q = `
		UPDATE repos SET last_analyzed_sha = $3, last_analyzed_at = now()
		WHERE id = $1 AND org_id = $2`
	_, err := tx.Exec(ctx, q, repoID, orgID, sha)
	if err != nil {
		return fmt.Errorf("store: update repo analyzed: %w", err)
	}
	return nil
}
