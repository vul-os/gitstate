// Package store — admin.go
// Instance-aggregate queries for the super-admin console.
// All tables read here are NOT org-scoped (users, organizations, org_members,
// plans, subscriptions, invoices, usage_events are read globally via the raw pool).
// These are intentionally pool-direct — no RLS context is set, matching S2's
// "audited service path, not ambient bypass" intent for super-admin reads.
package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ── Aggregate types ───────────────────────────────────────────────────────────

// AdminStats holds top-level instance analytics for the super-admin dashboard.
type AdminStats struct {
	TotalUsers       int
	TotalOrgs        int
	TotalSuperAdmins int
	NewUsersLast30d  int
	NewOrgsLast30d   int
	MRREstimateCents int // sum of active subscriptions' plan usd_cents
}

// SignupDay is one row from the signups-by-day query.
type SignupDay struct {
	Day   time.Time
	Count int
}

// PlanDist is the count of orgs on each plan.
type PlanDist struct {
	PlanKey  string
	PlanName string
	Count    int
}

// AdminUser is a user row for the admin users list.
type AdminUser struct {
	ID           string
	Email        string
	Name         string
	IsSuperAdmin bool
	Suspended    bool
	CreatedAt    time.Time
}

// AdminOrg is an org row for the admin orgs list.
type AdminOrg struct {
	ID          string
	Slug        string
	Name        string
	PlanKey     string
	MemberCount int
	CreatedAt   time.Time
}

// ── Queries ──────────────────────────────────────────────────────────────────

// GetAdminStats returns aggregate counters for the instance.
func GetAdminStats(ctx context.Context, pool *pgxpool.Pool) (*AdminStats, error) {
	var s AdminStats

	// Total users
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&s.TotalUsers); err != nil {
		return nil, err
	}
	// Total orgs
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM organizations`).Scan(&s.TotalOrgs); err != nil {
		return nil, err
	}
	// Super admins
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE is_super_admin`).Scan(&s.TotalSuperAdmins); err != nil {
		return nil, err
	}
	// New users last 30d
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE created_at >= now() - interval '30 days'`,
	).Scan(&s.NewUsersLast30d); err != nil {
		return nil, err
	}
	// New orgs last 30d
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM organizations WHERE created_at >= now() - interval '30 days'`,
	).Scan(&s.NewOrgsLast30d); err != nil {
		return nil, err
	}
	// MRR estimate (per-builder model): per_builder_cents × billable builders per org
	// for active subscriptions. Stakeholders are free (decisions P6).
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(p.per_builder_cents * bc.cnt), 0)
		FROM subscriptions s
		JOIN plans p ON p.key = s.plan_key
		JOIN (
			SELECT org_id, COUNT(*) AS cnt
			FROM org_members
			WHERE role IN ('owner','admin','member')
			GROUP BY org_id
		) bc ON bc.org_id = s.org_id
		WHERE s.status = 'active'
	`).Scan(&s.MRREstimateCents); err != nil {
		return nil, err
	}

	return &s, nil
}

// GetSignupsByDay returns daily signup counts for the last nDays days.
func GetSignupsByDay(ctx context.Context, pool *pgxpool.Pool, nDays int) ([]SignupDay, error) {
	const q = `
		SELECT date_trunc('day', created_at) AS day, COUNT(*)
		FROM users
		WHERE created_at >= now() - ($1 * interval '1 day')
		GROUP BY day
		ORDER BY day`

	rows, err := pool.Query(ctx, q, nDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SignupDay
	for rows.Next() {
		var d SignupDay
		if err := rows.Scan(&d.Day, &d.Count); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetPlanDistribution returns a count of orgs per plan, ordered by plan price.
func GetPlanDistribution(ctx context.Context, pool *pgxpool.Pool) ([]PlanDist, error) {
	const q = `
		SELECT o.plan_key, COALESCE(p.name, o.plan_key), COUNT(o.id)
		FROM organizations o
		LEFT JOIN plans p ON p.key = o.plan_key
		GROUP BY o.plan_key, p.name, p.usd_cents
		ORDER BY COALESCE(p.usd_cents, 0) ASC`

	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PlanDist
	for rows.Next() {
		var d PlanDist
		if err := rows.Scan(&d.PlanKey, &d.PlanName, &d.Count); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ListAdminUsers returns all users for the admin users list, filtered by optional search query.
// search is matched case-insensitively against email and name; pass "" to list all.
func ListAdminUsers(ctx context.Context, pool *pgxpool.Pool, search string, limit, offset int) ([]AdminUser, error) {
	const q = `
		SELECT id, email, COALESCE(name,''), is_super_admin, false AS suspended, created_at
		FROM users
		WHERE ($1 = '' OR email ILIKE '%' || $1 || '%' OR name ILIKE '%' || $1 || '%')
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`

	rows, err := pool.Query(ctx, q, search, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AdminUser
	for rows.Next() {
		var u AdminUser
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.IsSuperAdmin, &u.Suspended, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CountAdminUsers returns the total matching user count (for pagination).
func CountAdminUsers(ctx context.Context, pool *pgxpool.Pool, search string) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM users
		WHERE ($1 = '' OR email ILIKE '%' || $1 || '%' OR name ILIKE '%' || $1 || '%')`,
		search).Scan(&n)
	return n, err
}

// SetUserSuperAdmin sets or clears the is_super_admin flag for a user.
func SetUserSuperAdmin(ctx context.Context, pool *pgxpool.Pool, userID string, value bool) error {
	_, err := pool.Exec(ctx, `UPDATE users SET is_super_admin = $2 WHERE id = $1`, userID, value)
	return err
}

// ListAdminOrgs returns all organizations for the admin orgs list.
func ListAdminOrgs(ctx context.Context, pool *pgxpool.Pool, limit, offset int) ([]AdminOrg, error) {
	const q = `
		SELECT o.id, o.slug, o.name, o.plan_key,
		       COALESCE((SELECT COUNT(*) FROM org_members m WHERE m.org_id = o.id), 0),
		       o.created_at
		FROM organizations o
		ORDER BY o.created_at DESC
		LIMIT $1 OFFSET $2`

	rows, err := pool.Query(ctx, q, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AdminOrg
	for rows.Next() {
		var o AdminOrg
		if err := rows.Scan(&o.ID, &o.Slug, &o.Name, &o.PlanKey, &o.MemberCount, &o.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// CountAdminOrgs returns the total org count.
func CountAdminOrgs(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM organizations`).Scan(&n)
	return n, err
}
