package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Project mirrors a row in the projects table (org-scoped).
type Project struct {
	ID        string
	OrgID     string
	Name      string
	Key       string
	Archived  bool
	CreatedAt time.Time
}

// ListProjects returns the org's non-archived-first projects. Run inside db.WithOrg
// so RLS (app.current_org) is set.
func ListProjects(ctx context.Context, tx pgx.Tx, orgID string) ([]*Project, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, org_id, name, COALESCE(key,''), archived, created_at
		FROM projects WHERE org_id = $1
		ORDER BY archived ASC, created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.OrgID, &p.Name, &p.Key, &p.Archived, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// CreateProject inserts a project for the org. Run inside db.WithOrg.
func CreateProject(ctx context.Context, tx pgx.Tx, orgID, name, key string) (*Project, error) {
	var p Project
	err := tx.QueryRow(ctx, `
		INSERT INTO projects (org_id, name, key)
		VALUES ($1, $2, NULLIF($3, ''))
		RETURNING id, org_id, name, COALESCE(key,''), archived, created_at`,
		orgID, name, key).
		Scan(&p.ID, &p.OrgID, &p.Name, &p.Key, &p.Archived, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}
