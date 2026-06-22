// Package store holds hand-written SQL queries over the gitstate schema (decisions A3).
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

// User mirrors the users table columns returned by queries in this file.
type User struct {
	ID           string
	Email        string
	Name         string
	AvatarURL    string
	PasswordHash string // may be empty for OAuth-only accounts
	IsSuperAdmin bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ErrNotFound is returned when a query produces no rows.
var ErrNotFound = errors.New("store: not found")

// CreateUser inserts a new user row and returns the fully populated User.
// The users table is NOT org-scoped, so we use the raw pool rather than WithOrg.
func CreateUser(ctx context.Context, pool *pgxpool.Pool, email, name, passwordHash string) (*User, error) {
	const q = `
		INSERT INTO users (email, name, password_hash)
		VALUES ($1, $2, $3)
		RETURNING id, email, name, COALESCE(avatar_url,''), COALESCE(password_hash,''),
		          is_super_admin, created_at, updated_at`

	row := pool.QueryRow(ctx, q, email, name, passwordHash)
	return scanUser(row)
}

// GetUserByEmail looks up a user by their email address (case-insensitive via citext).
func GetUserByEmail(ctx context.Context, pool *pgxpool.Pool, email string) (*User, error) {
	const q = `
		SELECT id, email, name, COALESCE(avatar_url,''), COALESCE(password_hash,''),
		       is_super_admin, created_at, updated_at
		FROM users WHERE email = $1`

	row := pool.QueryRow(ctx, q, email)
	u, err := scanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// GetUserByID looks up a user by their UUID primary key.
func GetUserByID(ctx context.Context, pool *pgxpool.Pool, id string) (*User, error) {
	const q = `
		SELECT id, email, name, COALESCE(avatar_url,''), COALESCE(password_hash,''),
		       is_super_admin, created_at, updated_at
		FROM users WHERE id = $1`

	row := pool.QueryRow(ctx, q, id)
	u, err := scanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// ErrEmailTaken is returned when updating a profile to an email another user holds.
var ErrEmailTaken = errors.New("store: email already in use")

// UpdateUserProfile updates a user's display name and/or contact email and returns
// the updated row. Empty name/email leave that field unchanged. The users.email
// citext UNIQUE constraint is surfaced as ErrEmailTaken so callers return 409.
func UpdateUserProfile(ctx context.Context, pool *pgxpool.Pool, id, name, email string) (*User, error) {
	const q = `
		UPDATE users SET
			name       = COALESCE(NULLIF($2,''), name),
			email      = COALESCE(NULLIF($3,'')::citext, email),
			updated_at = now()
		WHERE id = $1
		RETURNING id, email, name, COALESCE(avatar_url,''), COALESCE(password_hash,''),
		          is_super_admin, created_at, updated_at`

	row := pool.QueryRow(ctx, q, id, name, email)
	u, err := scanUser(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		// 23505 = unique_violation on the email citext index.
		if strings.Contains(err.Error(), "23505") || strings.Contains(err.Error(), "users_email_key") {
			return nil, ErrEmailTaken
		}
		return nil, err
	}
	return u, nil
}

// scanUser reads a single user row.
func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(
		&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.PasswordHash,
		&u.IsSuperAdmin, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("store: scan user: %w", err)
	}
	return &u, nil
}
