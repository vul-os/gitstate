package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Org mirrors the organizations table.
type Org struct {
	ID        string
	Slug      string
	Name      string
	PlanKey   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// OrgWithRole is an Org plus the calling user's membership role.
type OrgWithRole struct {
	Org
	Role string
}

// OrgMember represents a row joined from org_members + users.
type OrgMember struct {
	UserID string
	Email  string
	Name   string
	Role   string
}

// OrgInvite represents a row from org_invites.
type OrgInvite struct {
	ID        string
	OrgID     string
	Email     string
	Role      string
	ExpiresAt time.Time
	CreatedAt time.Time
}

// ── Org queries ──────────────────────────────────────────────────────────────

// ListOrgsForUser returns all organizations the user belongs to, with their role.
// Not org-scoped (reads across orgs by user_id index); uses raw pool.
func ListOrgsForUser(ctx context.Context, pool *pgxpool.Pool, userID string) ([]OrgWithRole, error) {
	const q = `
		SELECT o.id, o.slug, o.name, o.plan_key, o.created_at, o.updated_at, m.role
		FROM organizations o
		JOIN org_members m ON m.org_id = o.id
		WHERE m.user_id = $1
		ORDER BY o.name`

	rows, err := pool.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("store: list orgs for user: %w", err)
	}
	defer rows.Close()

	var out []OrgWithRole
	for rows.Next() {
		var r OrgWithRole
		if err := rows.Scan(
			&r.ID, &r.Slug, &r.Name, &r.PlanKey, &r.CreatedAt, &r.UpdatedAt, &r.Role,
		); err != nil {
			return nil, fmt.Errorf("store: scan org+role: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateOrg inserts a new organization and immediately adds the creator as owner.
// Runs two statements in the same transaction via the raw pool (not org-scoped;
// the org does not exist yet when we begin).
func CreateOrg(ctx context.Context, pool *pgxpool.Pool, name, slug, ownerUserID string) (*OrgWithRole, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: create org: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const insertOrg = `
		INSERT INTO organizations (slug, name)
		VALUES ($1, $2)
		RETURNING id, slug, name, plan_key, created_at, updated_at`

	var o OrgWithRole
	o.Role = "owner"
	if err := tx.QueryRow(ctx, insertOrg, slug, name).Scan(
		&o.ID, &o.Slug, &o.Name, &o.PlanKey, &o.CreatedAt, &o.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("store: create org: insert org: %w", err)
	}

	const insertMember = `
		INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'owner')`
	if _, err := tx.Exec(ctx, insertMember, o.ID, ownerUserID); err != nil {
		return nil, fmt.Errorf("store: create org: add owner: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("store: create org: commit: %w", err)
	}
	return &o, nil
}

// GetOrg fetches a single org by ID using the supplied transaction (inside WithOrg).
func GetOrg(ctx context.Context, tx pgx.Tx, orgID string) (*Org, error) {
	const q = `
		SELECT id, slug, name, plan_key, created_at, updated_at
		FROM organizations WHERE id = $1`

	var o Org
	err := tx.QueryRow(ctx, q, orgID).Scan(
		&o.ID, &o.Slug, &o.Name, &o.PlanKey, &o.CreatedAt, &o.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get org: %w", err)
	}
	return &o, nil
}

// ── Membership queries ───────────────────────────────────────────────────────

// GetMemberRole returns a user's role in the org, or ErrNotFound if not a member.
// Uses the raw pool (reads org_members by (org_id, user_id) without RLS context).
func GetMemberRole(ctx context.Context, pool *pgxpool.Pool, orgID, userID string) (string, error) {
	const q = `SELECT role FROM org_members WHERE org_id = $1 AND user_id = $2`
	var role string
	err := pool.QueryRow(ctx, q, orgID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("store: get member role: %w", err)
	}
	return role, nil
}

// ListMembers returns all members of an org with user details.
// Runs inside a WithOrg transaction so RLS applies on org_members.
func ListMembers(ctx context.Context, tx pgx.Tx, orgID string) ([]OrgMember, error) {
	const q = `
		SELECT m.user_id, u.email, COALESCE(u.name,''), m.role
		FROM org_members m
		JOIN users u ON u.id = m.user_id
		WHERE m.org_id = $1
		ORDER BY u.email`

	rows, err := tx.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("store: list members: %w", err)
	}
	defer rows.Close()

	var out []OrgMember
	for rows.Next() {
		var m OrgMember
		if err := rows.Scan(&m.UserID, &m.Email, &m.Name, &m.Role); err != nil {
			return nil, fmt.Errorf("store: scan member: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddMember inserts a member into an org. If the user is already a member the
// call is a no-op (ON CONFLICT DO NOTHING).
func AddMember(ctx context.Context, tx pgx.Tx, orgID, userID, role string) error {
	const q = `
		INSERT INTO org_members (org_id, user_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (org_id, user_id) DO NOTHING`
	if _, err := tx.Exec(ctx, q, orgID, userID, role); err != nil {
		return fmt.Errorf("store: add member: %w", err)
	}
	return nil
}

// RemoveMember deletes a membership row. Returns ErrNotFound if the user was
// not a member (or the org doesn't exist under RLS).
func RemoveMember(ctx context.Context, tx pgx.Tx, orgID, userID string) error {
	const q = `DELETE FROM org_members WHERE org_id = $1 AND user_id = $2`
	tag, err := tx.Exec(ctx, q, orgID, userID)
	if err != nil {
		return fmt.Errorf("store: remove member: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateMemberRole changes the role of an existing member.
func UpdateMemberRole(ctx context.Context, tx pgx.Tx, orgID, userID, role string) error {
	const q = `UPDATE org_members SET role = $3 WHERE org_id = $1 AND user_id = $2`
	tag, err := tx.Exec(ctx, q, orgID, userID, role)
	if err != nil {
		return fmt.Errorf("store: update member role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ── Invite queries ───────────────────────────────────────────────────────────

// CreateInvite inserts a new pending invite.
// tokenHash is the SHA-256 hex of the raw invite token (same pattern as refresh tokens).
func CreateInvite(ctx context.Context, pool *pgxpool.Pool, orgID, email, role, tokenHash string, expiresAt time.Time) (*OrgInvite, error) {
	const q = `
		INSERT INTO org_invites (org_id, email, role, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, org_id, email, role, expires_at, created_at`

	var inv OrgInvite
	err := pool.QueryRow(ctx, q, orgID, email, role, tokenHash, expiresAt).Scan(
		&inv.ID, &inv.OrgID, &inv.Email, &inv.Role, &inv.ExpiresAt, &inv.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("store: create invite: %w", err)
	}
	return &inv, nil
}

// GetInviteByTokenHash looks up a pending (unaccepted, unexpired) invite by token hash.
func GetInviteByTokenHash(ctx context.Context, pool *pgxpool.Pool, tokenHash string) (*OrgInvite, error) {
	const q = `
		SELECT id, org_id, email, role, expires_at, created_at
		FROM org_invites
		WHERE token_hash = $1 AND accepted_at IS NULL AND expires_at > now()`

	var inv OrgInvite
	err := pool.QueryRow(ctx, q, tokenHash).Scan(
		&inv.ID, &inv.OrgID, &inv.Email, &inv.Role, &inv.ExpiresAt, &inv.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get invite by token: %w", err)
	}
	return &inv, nil
}

// AcceptInvite marks the invite as accepted and adds the user as a member.
// Runs both operations in a single transaction.
func AcceptInvite(ctx context.Context, pool *pgxpool.Pool, inviteID, orgID, userID, role string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: accept invite: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const markAccepted = `UPDATE org_invites SET accepted_at = now() WHERE id = $1`
	if _, err := tx.Exec(ctx, markAccepted, inviteID); err != nil {
		return fmt.Errorf("store: accept invite: mark accepted: %w", err)
	}

	const addMember = `
		INSERT INTO org_members (org_id, user_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (org_id, user_id) DO UPDATE SET role = EXCLUDED.role`
	if _, err := tx.Exec(ctx, addMember, orgID, userID, role); err != nil {
		return fmt.Errorf("store: accept invite: add member: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: accept invite: commit: %w", err)
	}
	return nil
}
