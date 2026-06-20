// Package store — deployments.go
// Org-scoped persistence for CI/CD deployments + incidents, the two facts git
// history alone cannot provide. Together they power the REAL DORA metrics on the
// Engineering Health dashboard:
//
//   - deployments → deploy frequency (deploys/week) and change-failure rate
//     (failed deploys ÷ total deploys), a CI-grounded companion to the SZZ signal.
//   - incidents   → MTTR (mean time to restore = mean (resolved_at − opened_at)).
//
// Writes run inside db.WithOrg so the org_isolation RLS policy is enforced; reads
// take a Querier (pool or Tx) and MUST also be called inside db.WithOrg.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Deployment mirrors the deployments table.
type Deployment struct {
	ID          string    `json:"id"`
	OrgID       string    `json:"orgId"`
	RepoID      string    `json:"repoId,omitempty"`
	Environment string    `json:"environment"`
	Status      string    `json:"status"` // success | failure
	SHA         string    `json:"sha,omitempty"`
	Source      string    `json:"source"` // github_actions | gitlab_ci | manual
	ExternalID  string    `json:"externalId,omitempty"`
	DeployedAt  time.Time `json:"deployedAt"`
	CreatedAt   time.Time `json:"createdAt"`
}

// DeploymentInput is the payload for InsertDeployment.
type DeploymentInput struct {
	OrgID       string
	RepoID      string // optional → NULL
	Environment string
	Status      string
	SHA         string
	Source      string
	ExternalID  string // optional; used for idempotency with (org, source, external_id)
	DeployedAt  time.Time
}

// InsertDeployment inserts a deployment row. When ExternalID is set the unique
// (org_id, source, external_id) key makes a re-delivered webhook idempotent
// (ON CONFLICT updates the mutable status/sha/env). Must run inside db.WithOrg.
func InsertDeployment(ctx context.Context, tx pgx.Tx, in DeploymentInput) (*Deployment, error) {
	env := in.Environment
	if env == "" {
		env = "production"
	}
	status := in.Status
	if status != "failure" {
		status = "success"
	}
	source := in.Source
	if source == "" {
		source = "manual"
	}
	deployedAt := in.DeployedAt
	if deployedAt.IsZero() {
		deployedAt = time.Now().UTC()
	}

	var repoID, sha, extID *string
	if in.RepoID != "" {
		repoID = &in.RepoID
	}
	if in.SHA != "" {
		sha = &in.SHA
	}
	if in.ExternalID != "" {
		extID = &in.ExternalID
	}

	const q = `
		INSERT INTO deployments (org_id, repo_id, environment, status, sha, source, external_id, deployed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (org_id, source, external_id) DO UPDATE SET
			repo_id     = COALESCE(EXCLUDED.repo_id, deployments.repo_id),
			environment = EXCLUDED.environment,
			status      = EXCLUDED.status,
			sha         = COALESCE(EXCLUDED.sha, deployments.sha),
			deployed_at = EXCLUDED.deployed_at
		RETURNING id, org_id, COALESCE(repo_id::text,''), environment, status,
		          COALESCE(sha,''), source, COALESCE(external_id,''), deployed_at, created_at`

	var d Deployment
	err := tx.QueryRow(ctx, q,
		in.OrgID, repoID, env, status, sha, source, extID, deployedAt.UTC(),
	).Scan(
		&d.ID, &d.OrgID, &d.RepoID, &d.Environment, &d.Status,
		&d.SHA, &d.Source, &d.ExternalID, &d.DeployedAt, &d.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("store.deployments: insert: %w", err)
	}
	return &d, nil
}

// DeploymentFilter bounds ListDeployments.
type DeploymentFilter struct {
	From        time.Time
	To          time.Time
	Environment string
	Limit       int
}

// ListDeployments returns deployments for the org newest-first, optionally
// windowed/filtered. Must run inside db.WithOrg.
func ListDeployments(ctx context.Context, qr Querier, orgID string, f DeploymentFilter) ([]Deployment, error) {
	q := `
		SELECT id, org_id, COALESCE(repo_id::text,''), environment, status,
		       COALESCE(sha,''), source, COALESCE(external_id,''), deployed_at, created_at
		FROM deployments
		WHERE org_id = $1`
	args := []any{orgID}
	idx := 2
	if !f.From.IsZero() {
		q += fmt.Sprintf(" AND deployed_at >= $%d", idx)
		args = append(args, f.From)
		idx++
	}
	if !f.To.IsZero() {
		q += fmt.Sprintf(" AND deployed_at < $%d", idx)
		args = append(args, f.To)
		idx++
	}
	if f.Environment != "" {
		q += fmt.Sprintf(" AND environment = $%d", idx)
		args = append(args, f.Environment)
		idx++
	}
	q += " ORDER BY deployed_at DESC"
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT $%d", idx)
		args = append(args, f.Limit)
		idx++ //nolint:ineffassign
	}

	rows, err := qr.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store.deployments: list: %w", err)
	}
	defer rows.Close()

	var out []Deployment
	for rows.Next() {
		var d Deployment
		if err := rows.Scan(
			&d.ID, &d.OrgID, &d.RepoID, &d.Environment, &d.Status,
			&d.SHA, &d.Source, &d.ExternalID, &d.DeployedAt, &d.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("store.deployments: scan: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeploymentStats holds the CI-grounded deploy facts for a window.
type DeploymentStats struct {
	Total      int
	Failures   int
	WindowDays int
}

// DeploymentStatsForWindow counts total + failed deployments in the window so the
// api layer can derive deploy frequency and CI change-failure rate. Must run
// inside db.WithOrg.
func DeploymentStatsForWindow(ctx context.Context, qr pgx.Tx, orgID string, from, to time.Time) (DeploymentStats, error) {
	q := `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE status = 'failure')
		FROM deployments
		WHERE org_id = $1`
	args := []any{orgID}
	idx := 2
	if !from.IsZero() {
		q += fmt.Sprintf(" AND deployed_at >= $%d", idx)
		args = append(args, from)
		idx++
	}
	if !to.IsZero() {
		q += fmt.Sprintf(" AND deployed_at < $%d", idx)
		args = append(args, to)
		idx++ //nolint:ineffassign
	}
	var st DeploymentStats
	if err := qr.QueryRow(ctx, q, args...).Scan(&st.Total, &st.Failures); err != nil {
		return DeploymentStats{}, fmt.Errorf("store.deployments: stats: %w", err)
	}
	if !from.IsZero() && !to.IsZero() {
		st.WindowDays = int(to.Sub(from).Hours()/24) + 1
	}
	return st, nil
}

// ── incidents ───────────────────────────────────────────────────────────────────

// Incident mirrors the incidents table.
type Incident struct {
	ID         string     `json:"id"`
	OrgID      string     `json:"orgId"`
	RepoID     string     `json:"repoId,omitempty"`
	Title      string     `json:"title"`
	OpenedAt   time.Time  `json:"openedAt"`
	ResolvedAt *time.Time `json:"resolvedAt,omitempty"`
	Severity   string     `json:"severity"`
	CreatedAt  time.Time  `json:"createdAt"`
}

// IncidentInput is the payload for InsertIncident.
type IncidentInput struct {
	OrgID    string
	RepoID   string
	Title    string
	Severity string
	OpenedAt time.Time
}

// InsertIncident opens a new incident. Must run inside db.WithOrg.
func InsertIncident(ctx context.Context, tx pgx.Tx, in IncidentInput) (*Incident, error) {
	sev := in.Severity
	if sev == "" {
		sev = "minor"
	}
	openedAt := in.OpenedAt
	if openedAt.IsZero() {
		openedAt = time.Now().UTC()
	}
	var repoID *string
	if in.RepoID != "" {
		repoID = &in.RepoID
	}
	const q = `
		INSERT INTO incidents (org_id, repo_id, title, opened_at, severity)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, org_id, COALESCE(repo_id::text,''), COALESCE(title,''),
		          opened_at, resolved_at, severity, created_at`
	var inc Incident
	err := tx.QueryRow(ctx, q, in.OrgID, repoID, in.Title, openedAt.UTC(), sev).Scan(
		&inc.ID, &inc.OrgID, &inc.RepoID, &inc.Title,
		&inc.OpenedAt, &inc.ResolvedAt, &inc.Severity, &inc.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("store.incidents: insert: %w", err)
	}
	return &inc, nil
}

// ResolveIncident stamps resolved_at on a single open incident. Must run inside
// db.WithOrg. Returns ErrNotFound when no matching open incident.
func ResolveIncident(ctx context.Context, tx pgx.Tx, orgID, id string, at time.Time) (*Incident, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	const q = `
		UPDATE incidents SET resolved_at = $3
		WHERE id = $1 AND org_id = $2
		RETURNING id, org_id, COALESCE(repo_id::text,''), COALESCE(title,''),
		          opened_at, resolved_at, severity, created_at`
	var inc Incident
	err := tx.QueryRow(ctx, q, id, orgID, at.UTC()).Scan(
		&inc.ID, &inc.OrgID, &inc.RepoID, &inc.Title,
		&inc.OpenedAt, &inc.ResolvedAt, &inc.Severity, &inc.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store.incidents: resolve: %w", err)
	}
	return &inc, nil
}

// ResolveOpenIncidentsForRepo closes any still-open incidents for a repo (or
// org-wide when repoID is "") as of `at`, returning how many it resolved. Used by
// the webhook receiver on a failure→success recovery to compute MTTR. Must run
// inside db.WithOrg.
func ResolveOpenIncidentsForRepo(ctx context.Context, tx pgx.Tx, orgID, repoID string, at time.Time) (int, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	q := `UPDATE incidents SET resolved_at = $2 WHERE org_id = $1 AND resolved_at IS NULL`
	args := []any{orgID, at.UTC()}
	if repoID != "" {
		q += " AND repo_id = $3"
		args = append(args, repoID)
	}
	tag, err := tx.Exec(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("store.incidents: resolve open for repo: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// HasOpenIncidentForRepo reports whether an unresolved incident exists for the
// repo (or org-wide when repoID is ""). Used to avoid opening duplicate incidents
// on repeated failure deliveries. Must run inside db.WithOrg.
func HasOpenIncidentForRepo(ctx context.Context, tx pgx.Tx, orgID, repoID string) (bool, error) {
	q := `SELECT EXISTS (SELECT 1 FROM incidents WHERE org_id = $1 AND resolved_at IS NULL`
	args := []any{orgID}
	if repoID != "" {
		q += " AND repo_id = $2"
		args = append(args, repoID)
	}
	q += ")"
	var exists bool
	if err := tx.QueryRow(ctx, q, args...).Scan(&exists); err != nil {
		return false, fmt.Errorf("store.incidents: has open: %w", err)
	}
	return exists, nil
}

// IncidentFilter bounds ListIncidents.
type IncidentFilter struct {
	OpenOnly bool
	Limit    int
}

// ListIncidents returns incidents for the org newest-opened first. Must run
// inside db.WithOrg.
func ListIncidents(ctx context.Context, qr Querier, orgID string, f IncidentFilter) ([]Incident, error) {
	q := `
		SELECT id, org_id, COALESCE(repo_id::text,''), COALESCE(title,''),
		       opened_at, resolved_at, severity, created_at
		FROM incidents
		WHERE org_id = $1`
	args := []any{orgID}
	idx := 2
	if f.OpenOnly {
		q += " AND resolved_at IS NULL"
	}
	q += " ORDER BY opened_at DESC"
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT $%d", idx)
		args = append(args, f.Limit)
		idx++ //nolint:ineffassign
	}
	rows, err := qr.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store.incidents: list: %w", err)
	}
	defer rows.Close()
	var out []Incident
	for rows.Next() {
		var inc Incident
		if err := rows.Scan(
			&inc.ID, &inc.OrgID, &inc.RepoID, &inc.Title,
			&inc.OpenedAt, &inc.ResolvedAt, &inc.Severity, &inc.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("store.incidents: scan: %w", err)
		}
		out = append(out, inc)
	}
	return out, rows.Err()
}

// MTTRStats holds the inputs for mean-time-to-restore.
type MTTRStats struct {
	ResolvedCount int
	OpenCount     int
	MeanHours     float64 // mean (resolved_at − opened_at) over resolved incidents in window
}

// MTTRForWindow computes MTTR over incidents opened in the window. Mean is over
// resolved incidents only; open incidents are reported separately so the api can
// show an honest "N still open". Must run inside db.WithOrg.
func MTTRForWindow(ctx context.Context, qr pgx.Tx, orgID string, from, to time.Time) (MTTRStats, error) {
	q := `
		SELECT
			COUNT(*) FILTER (WHERE resolved_at IS NOT NULL),
			COUNT(*) FILTER (WHERE resolved_at IS NULL),
			COALESCE(AVG(GREATEST(EXTRACT(EPOCH FROM (resolved_at - opened_at)), 0))
				FILTER (WHERE resolved_at IS NOT NULL), 0) / 3600.0
		FROM incidents
		WHERE org_id = $1`
	args := []any{orgID}
	idx := 2
	if !from.IsZero() {
		q += fmt.Sprintf(" AND opened_at >= $%d", idx)
		args = append(args, from)
		idx++
	}
	if !to.IsZero() {
		q += fmt.Sprintf(" AND opened_at < $%d", idx)
		args = append(args, to)
		idx++ //nolint:ineffassign
	}
	var st MTTRStats
	if err := qr.QueryRow(ctx, q, args...).Scan(&st.ResolvedCount, &st.OpenCount, &st.MeanHours); err != nil {
		return MTTRStats{}, fmt.Errorf("store.deployments: mttr: %w", err)
	}
	return st, nil
}
