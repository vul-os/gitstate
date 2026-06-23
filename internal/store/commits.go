// Package store — commits.go
// Org-scoped queries for the commits table.
// All writes/reads run inside db.WithOrg so RLS enforces the org boundary.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Commit mirrors the columns returned by commit queries.
type Commit struct {
	ID          string
	OrgID       string
	RepoID      string
	SHA         string
	AuthorLogin string // display name / git author name
	AuthorEmail string
	IsAgent     bool
	Message     string
	Additions   int
	Deletions   int
	CommittedAt time.Time
}

// UpsertCommit inserts or updates a commit row identified by (org_id, repo_id, sha).
// The caller must supply an already-opened org-scoped transaction (from db.WithOrg).
// Conflict on the unique key (org_id, repo_id, sha) updates mutable fields so that
// a re-sync is safe to call repeatedly.
func UpsertCommit(ctx context.Context, tx pgx.Tx, c *Commit) error {
	const q = `
		INSERT INTO commits
			(org_id, repo_id, sha, author_login, author_email, is_agent,
			 message, additions, deletions, committed_at)
		VALUES
			($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (org_id, repo_id, sha) DO UPDATE SET
			author_login  = EXCLUDED.author_login,
			author_email  = EXCLUDED.author_email,
			is_agent      = EXCLUDED.is_agent,
			message       = EXCLUDED.message,
			-- Only overwrite churn when the new source actually has it, so a
			-- zero-churn re-sync (e.g. a path that didn't compute numstat) never
			-- wipes good additions/deletions a clone/GraphQL already supplied.
			additions     = CASE WHEN EXCLUDED.additions <> 0 OR EXCLUDED.deletions <> 0 THEN EXCLUDED.additions ELSE commits.additions END,
			deletions     = CASE WHEN EXCLUDED.additions <> 0 OR EXCLUDED.deletions <> 0 THEN EXCLUDED.deletions ELSE commits.deletions END,
			committed_at  = EXCLUDED.committed_at`

	_, err := tx.Exec(ctx, q,
		c.OrgID,
		c.RepoID,
		c.SHA,
		c.AuthorLogin,
		c.AuthorEmail,
		c.IsAgent,
		c.Message,
		c.Additions,
		c.Deletions,
		c.CommittedAt,
	)
	if err != nil {
		return fmt.Errorf("store: upsert commit %s: %w", c.SHA, err)
	}
	return nil
}

// ListCommits returns all commits for a repo ordered newest-first.
// Uses the raw pool (RLS is already set for the session by db.WithOrg when this
// is called inside the same transaction; callers outside a tx must ensure the
// connection carries the org context, or wrap in db.WithOrg themselves).
//
// The pool overload here is provided for convenience in read-only list calls
// where the caller has already set the RLS context separately (e.g. middleware).
func ListCommits(ctx context.Context, pool *pgxpool.Pool, repoID string) ([]*Commit, error) {
	const q = `
		SELECT id, org_id, repo_id, sha,
		       COALESCE(author_login,''), COALESCE(author_email,''),
		       is_agent, COALESCE(message,''),
		       COALESCE(additions,0), COALESCE(deletions,0),
		       COALESCE(committed_at, now())
		FROM commits
		WHERE repo_id = $1
		ORDER BY committed_at DESC`

	rows, err := pool.Query(ctx, q, repoID)
	if err != nil {
		return nil, fmt.Errorf("store: list commits for repo %s: %w", repoID, err)
	}
	defer rows.Close()

	var commits []*Commit
	for rows.Next() {
		c := &Commit{}
		if err := rows.Scan(
			&c.ID, &c.OrgID, &c.RepoID, &c.SHA,
			&c.AuthorLogin, &c.AuthorEmail,
			&c.IsAgent, &c.Message,
			&c.Additions, &c.Deletions,
			&c.CommittedAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan commit: %w", err)
		}
		commits = append(commits, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list commits rows: %w", err)
	}
	return commits, nil
}

// ListCommitsTx is the same as ListCommits but runs inside an existing transaction.
// Use this inside db.WithOrg callbacks.
func ListCommitsTx(ctx context.Context, tx pgx.Tx, repoID string) ([]*Commit, error) {
	const q = `
		SELECT id, org_id, repo_id, sha,
		       COALESCE(author_login,''), COALESCE(author_email,''),
		       is_agent, COALESCE(message,''),
		       COALESCE(additions,0), COALESCE(deletions,0),
		       COALESCE(committed_at, now())
		FROM commits
		WHERE repo_id = $1
		ORDER BY committed_at DESC`

	rows, err := tx.Query(ctx, q, repoID)
	if err != nil {
		return nil, fmt.Errorf("store: list commits tx for repo %s: %w", repoID, err)
	}
	defer rows.Close()

	var commits []*Commit
	for rows.Next() {
		c := &Commit{}
		if err := rows.Scan(
			&c.ID, &c.OrgID, &c.RepoID, &c.SHA,
			&c.AuthorLogin, &c.AuthorEmail,
			&c.IsAgent, &c.Message,
			&c.Additions, &c.Deletions,
			&c.CommittedAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan commit tx: %w", err)
		}
		commits = append(commits, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list commits tx rows: %w", err)
	}
	return commits, nil
}

// GetLatestCommitAt returns the committed_at of the most recent commit for a
// repo, or the zero time if none exist. Used by the sync agent to determine the
// since parameter for incremental WalkCommits calls.
func GetLatestCommitAt(ctx context.Context, pool *pgxpool.Pool, repoID string) (time.Time, error) {
	const q = `
		SELECT COALESCE(MAX(committed_at), '1970-01-01'::timestamptz)
		FROM commits
		WHERE repo_id = $1`

	var ts time.Time
	err := pool.QueryRow(ctx, q, repoID).Scan(&ts)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("store: get latest commit_at for repo %s: %w", repoID, err)
	}
	return ts, nil
}
