// Package store — pull_requests.go
// Org-scoped queries for the pull_requests table.
// All writes run inside db.WithOrg so RLS enforces the org boundary.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PullRequest mirrors the columns returned by PR queries.
type PullRequest struct {
	ID             string
	OrgID          string
	RepoID         string
	Platform       string // github | gitlab
	ExternalID     string
	Number         int
	Title          string
	AuthorLogin    string
	State          string // open | merged | closed
	Additions      int
	Deletions      int
	ChangedFiles   int
	FirstCommitAt  time.Time
	MergedAt       time.Time
	CreatedAt      time.Time
}

// UpsertPR inserts or updates a pull_request row identified by (org_id, repo_id, external_id).
// The caller must supply an already-opened org-scoped transaction (from db.WithOrg).
// On conflict, all mutable fields are updated so a re-sync is safe.
func UpsertPR(ctx context.Context, tx pgx.Tx, pr *PullRequest) error {
	const q = `
		INSERT INTO pull_requests
			(org_id, repo_id, platform, external_id, number, title, author_login,
			 state, additions, deletions, changed_files,
			 first_commit_at, merged_at)
		VALUES
			($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (org_id, repo_id, external_id) DO UPDATE SET
			number         = EXCLUDED.number,
			title          = EXCLUDED.title,
			author_login   = EXCLUDED.author_login,
			state          = EXCLUDED.state,
			additions      = EXCLUDED.additions,
			deletions      = EXCLUDED.deletions,
			changed_files  = EXCLUDED.changed_files,
			first_commit_at = EXCLUDED.first_commit_at,
			merged_at      = EXCLUDED.merged_at`

	// Use nullable time for first_commit_at and merged_at (may be zero).
	var firstAt, mergedAt *time.Time
	if !pr.FirstCommitAt.IsZero() {
		t := pr.FirstCommitAt.UTC()
		firstAt = &t
	}
	if !pr.MergedAt.IsZero() {
		t := pr.MergedAt.UTC()
		mergedAt = &t
	}

	_, err := tx.Exec(ctx, q,
		pr.OrgID,
		pr.RepoID,
		pr.Platform,
		pr.ExternalID,
		pr.Number,
		pr.Title,
		pr.AuthorLogin,
		pr.State,
		pr.Additions,
		pr.Deletions,
		pr.ChangedFiles,
		firstAt,
		mergedAt,
	)
	if err != nil {
		return fmt.Errorf("store: upsert PR %s: %w", pr.ExternalID, err)
	}
	return nil
}

// ListPRs returns all pull requests for a repo ordered newest-first.
// Runs on the raw pool; the caller is responsible for ensuring the RLS context
// is set (either via db.WithOrg or by the request-scoped OrgScope middleware).
func ListPRs(ctx context.Context, pool *pgxpool.Pool, repoID string) ([]*PullRequest, error) {
	const q = `
		SELECT id, org_id, repo_id, platform, external_id,
		       COALESCE(number,0),
		       COALESCE(title,''),
		       COALESCE(author_login,''),
		       COALESCE(state,''),
		       COALESCE(additions,0), COALESCE(deletions,0), COALESCE(changed_files,0),
		       first_commit_at, merged_at, created_at
		FROM pull_requests
		WHERE repo_id = $1
		ORDER BY created_at DESC`

	rows, err := pool.Query(ctx, q, repoID)
	if err != nil {
		return nil, fmt.Errorf("store: list PRs for repo %s: %w", repoID, err)
	}
	defer rows.Close()

	var prs []*PullRequest
	for rows.Next() {
		pr := &PullRequest{}
		var firstAt, mergedAt *time.Time
		if err := rows.Scan(
			&pr.ID, &pr.OrgID, &pr.RepoID, &pr.Platform, &pr.ExternalID,
			&pr.Number, &pr.Title, &pr.AuthorLogin, &pr.State,
			&pr.Additions, &pr.Deletions, &pr.ChangedFiles,
			&firstAt, &mergedAt, &pr.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan PR: %w", err)
		}
		if firstAt != nil {
			pr.FirstCommitAt = *firstAt
		}
		if mergedAt != nil {
			pr.MergedAt = *mergedAt
		}
		prs = append(prs, pr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list PRs rows: %w", err)
	}
	return prs, nil
}

// ListPRsTx is ListPRs but runs inside an existing transaction.
func ListPRsTx(ctx context.Context, tx pgx.Tx, repoID string) ([]*PullRequest, error) {
	const q = `
		SELECT id, org_id, repo_id, platform, external_id,
		       COALESCE(number,0),
		       COALESCE(title,''),
		       COALESCE(author_login,''),
		       COALESCE(state,''),
		       COALESCE(additions,0), COALESCE(deletions,0), COALESCE(changed_files,0),
		       first_commit_at, merged_at, created_at
		FROM pull_requests
		WHERE repo_id = $1
		ORDER BY created_at DESC`

	rows, err := tx.Query(ctx, q, repoID)
	if err != nil {
		return nil, fmt.Errorf("store: list PRs tx for repo %s: %w", repoID, err)
	}
	defer rows.Close()

	var prs []*PullRequest
	for rows.Next() {
		pr := &PullRequest{}
		var firstAt, mergedAt *time.Time
		if err := rows.Scan(
			&pr.ID, &pr.OrgID, &pr.RepoID, &pr.Platform, &pr.ExternalID,
			&pr.Number, &pr.Title, &pr.AuthorLogin, &pr.State,
			&pr.Additions, &pr.Deletions, &pr.ChangedFiles,
			&firstAt, &mergedAt, &pr.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan PR tx: %w", err)
		}
		if firstAt != nil {
			pr.FirstCommitAt = *firstAt
		}
		if mergedAt != nil {
			pr.MergedAt = *mergedAt
		}
		prs = append(prs, pr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list PRs tx rows: %w", err)
	}
	return prs, nil
}

// prRowScanner is satisfied by both *pgxpool.Pool and pgx.Tx for a single-row
// lookup, so GetPR can run on the bare pool OR inside a db.WithOrg tx (required
// under FORCE RLS, where a bare-pool read returns no rows).
type prRowScanner interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// GetPR looks up a single pull request by its internal UUID. Pass a db.WithOrg
// tx so RLS (app.current_org) is set; a bare pool returns ErrNotFound under
// forced RLS.
func GetPR(ctx context.Context, qr prRowScanner, id string) (*PullRequest, error) {
	const q = `
		SELECT id, org_id, repo_id, platform, external_id,
		       COALESCE(number,0),
		       COALESCE(title,''),
		       COALESCE(author_login,''),
		       COALESCE(state,''),
		       COALESCE(additions,0), COALESCE(deletions,0), COALESCE(changed_files,0),
		       first_commit_at, merged_at, created_at
		FROM pull_requests
		WHERE id = $1`

	pr := &PullRequest{}
	var firstAt, mergedAt *time.Time
	err := qr.QueryRow(ctx, q, id).Scan(
		&pr.ID, &pr.OrgID, &pr.RepoID, &pr.Platform, &pr.ExternalID,
		&pr.Number, &pr.Title, &pr.AuthorLogin, &pr.State,
		&pr.Additions, &pr.Deletions, &pr.ChangedFiles,
		&firstAt, &mergedAt, &pr.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get PR %s: %w", id, err)
	}
	if firstAt != nil {
		pr.FirstCommitAt = *firstAt
	}
	if mergedAt != nil {
		pr.MergedAt = *mergedAt
	}
	return pr, nil
}

// GetPRByExternal looks up a PR by (repo_id, external_id) — useful during sync
// to check if a PR already exists before upserting.
func GetPRByExternal(ctx context.Context, pool *pgxpool.Pool, repoID, externalID string) (*PullRequest, error) {
	const q = `
		SELECT id, org_id, repo_id, platform, external_id,
		       COALESCE(number,0),
		       COALESCE(title,''),
		       COALESCE(author_login,''),
		       COALESCE(state,''),
		       COALESCE(additions,0), COALESCE(deletions,0), COALESCE(changed_files,0),
		       first_commit_at, merged_at, created_at
		FROM pull_requests
		WHERE repo_id = $1 AND external_id = $2`

	pr := &PullRequest{}
	var firstAt, mergedAt *time.Time
	err := pool.QueryRow(ctx, q, repoID, externalID).Scan(
		&pr.ID, &pr.OrgID, &pr.RepoID, &pr.Platform, &pr.ExternalID,
		&pr.Number, &pr.Title, &pr.AuthorLogin, &pr.State,
		&pr.Additions, &pr.Deletions, &pr.ChangedFiles,
		&firstAt, &mergedAt, &pr.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get PR by external %s/%s: %w", repoID, externalID, err)
	}
	if firstAt != nil {
		pr.FirstCommitAt = *firstAt
	}
	if mergedAt != nil {
		pr.MergedAt = *mergedAt
	}
	return pr, nil
}
