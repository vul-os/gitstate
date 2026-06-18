package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	CreatedAt     time.Time
	// Token is NOT stored in the DB — supplied at connect time and held in memory.
	// For persisted connections the caller must re-supply the token on sync.
	Token string `db:"-"`
}

// ConnectRepo upserts a repo connection for an org.
// On conflict (org_id, platform, external_id) it updates full_name and clone_url.
// Uses the raw pool (not org-scoped) because the org is set via RLS; callers
// should call this inside a db.WithOrg block if they need RLS enforcement,
// OR use the pool directly with a prior SET LOCAL app.current_org call.
//
// For simplicity (no separate token table yet) the token is not persisted here.
// The API layer stores it in memory and passes it on each sync call.
func ConnectRepo(ctx context.Context, pool *pgxpool.Pool, orgID, platform, externalID, fullName, defaultBranch, cloneURL string) (*Repo, error) {
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
		          last_synced_at, created_at`

	var r Repo
	var lastSynced *time.Time
	err := pool.QueryRow(ctx, q,
		orgID, platform, externalID, fullName, defaultBranch, cloneURL,
	).Scan(
		&r.ID, &r.OrgID, &r.Platform, &r.ExternalID, &r.FullName,
		&r.DefaultBranch, &r.CloneURL, &lastSynced, &r.CreatedAt,
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
		       last_synced_at, created_at
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
			&r.DefaultBranch, &r.CloneURL, &lastSynced, &r.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan repo: %w", err)
		}
		r.LastSyncedAt = lastSynced
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRepo fetches a single repo by ID. Runs inside an org-scoped tx (RLS enforces
// org ownership). Returns ErrNotFound if the repo doesn't exist or belongs to
// another org.
func GetRepo(ctx context.Context, tx pgx.Tx, orgID, repoID string) (*Repo, error) {
	const q = `
		SELECT id, org_id, platform, external_id, full_name,
		       COALESCE(default_branch,''), COALESCE(clone_url,''),
		       last_synced_at, created_at
		FROM repos
		WHERE id = $1 AND org_id = $2`

	var r Repo
	var lastSynced *time.Time
	err := tx.QueryRow(ctx, q, repoID, orgID).Scan(
		&r.ID, &r.OrgID, &r.Platform, &r.ExternalID, &r.FullName,
		&r.DefaultBranch, &r.CloneURL, &lastSynced, &r.CreatedAt,
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

// GetRepoByIDPool fetches a repo using the raw pool (no org-scoped tx needed
// when the caller guarantees org ownership via the request auth chain).
func GetRepoByIDPool(ctx context.Context, pool *pgxpool.Pool, orgID, repoID string) (*Repo, error) {
	const q = `
		SELECT id, org_id, platform, external_id, full_name,
		       COALESCE(default_branch,''), COALESCE(clone_url,''),
		       last_synced_at, created_at
		FROM repos
		WHERE id = $1 AND org_id = $2`

	var r Repo
	var lastSynced *time.Time
	err := pool.QueryRow(ctx, q, repoID, orgID).Scan(
		&r.ID, &r.OrgID, &r.Platform, &r.ExternalID, &r.FullName,
		&r.DefaultBranch, &r.CloneURL, &lastSynced, &r.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get repo by id: %w", err)
	}
	r.LastSyncedAt = lastSynced
	return &r, nil
}

// UpdateRepoSyncedAt sets last_synced_at = now() for the given repo.
// Uses the raw pool; relies on the caller to ensure org ownership.
func UpdateRepoSyncedAt(ctx context.Context, pool *pgxpool.Pool, orgID, repoID string) error {
	const q = `
		UPDATE repos SET last_synced_at = now()
		WHERE id = $1 AND org_id = $2`
	_, err := pool.Exec(ctx, q, repoID, orgID)
	if err != nil {
		return fmt.Errorf("store: update repo synced_at: %w", err)
	}
	return nil
}
