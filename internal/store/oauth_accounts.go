package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// OAuthAccount mirrors the oauth_accounts table row.
type OAuthAccount struct {
	ID          string
	UserID      string
	Provider    string
	ProviderUID string
	Email       string
}

// GetOAuthAccount looks up an existing OAuth account by provider + providerUID.
// Returns ErrNotFound when no matching row exists.
func GetOAuthAccount(ctx context.Context, pool *pgxpool.Pool, provider, providerUID string) (*OAuthAccount, error) {
	const q = `
		SELECT id, user_id, provider, provider_uid, COALESCE(email::text, '')
		FROM oauth_accounts
		WHERE provider = $1 AND provider_uid = $2`

	row := pool.QueryRow(ctx, q, provider, providerUID)
	var a OAuthAccount
	err := row.Scan(&a.ID, &a.UserID, &a.Provider, &a.ProviderUID, &a.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: scan oauth_account: %w", err)
	}
	return &a, nil
}

// LinkOAuthAccount inserts an oauth_accounts row linking a user to a provider
// identity.  On conflict (same provider + provider_uid) it does nothing and
// returns the existing row's id.
func LinkOAuthAccount(ctx context.Context, pool *pgxpool.Pool, userID, provider, providerUID, email string) (*OAuthAccount, error) {
	const q = `
		INSERT INTO oauth_accounts (user_id, provider, provider_uid, email)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (provider, provider_uid) DO UPDATE
		  SET email = EXCLUDED.email
		RETURNING id, user_id, provider, provider_uid, COALESCE(email::text, '')`

	row := pool.QueryRow(ctx, q, userID, provider, providerUID, email)
	var a OAuthAccount
	if err := row.Scan(&a.ID, &a.UserID, &a.Provider, &a.ProviderUID, &a.Email); err != nil {
		return nil, fmt.Errorf("store: link oauth account: %w", err)
	}
	return &a, nil
}

// FindOrCreateOAuthUser finds or creates a user for an OAuth login.
// Strategy (account linking by verified email, decisions A6):
//  1. Look up by provider + providerUID → found: return user.
//  2. Look up by email → found: link this OAuth account to existing user, return user.
//  3. Neither found: create user, link OAuth account, return (user, isNew=true).
func FindOrCreateOAuthUser(
	ctx context.Context,
	pool *pgxpool.Pool,
	provider, providerUID, email, name, avatarURL string,
) (u *User, isNew bool, err error) {
	// 1. Existing OAuth account → fast path.
	acct, err := GetOAuthAccount(ctx, pool, provider, providerUID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, false, fmt.Errorf("store: find oauth account: %w", err)
	}
	if err == nil {
		// OAuth account exists — fetch the linked user.
		u, err = GetUserByID(ctx, pool, acct.UserID)
		if err != nil {
			return nil, false, fmt.Errorf("store: get user for oauth account: %w", err)
		}
		return u, false, nil
	}

	// 2. Same email already registered → link this provider to that account.
	existing, err := GetUserByEmail(ctx, pool, strings.ToLower(email))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, false, fmt.Errorf("store: look up user by email: %w", err)
	}
	if err == nil {
		// User exists; link the OAuth account.
		if _, linkErr := LinkOAuthAccount(ctx, pool, existing.ID, provider, providerUID, email); linkErr != nil {
			return nil, false, fmt.Errorf("store: link oauth to existing user: %w", linkErr)
		}
		return existing, false, nil
	}

	// 3. Brand new user.
	newUser, err := createOAuthUser(ctx, pool, email, name, avatarURL)
	if err != nil {
		return nil, false, err
	}
	if _, linkErr := LinkOAuthAccount(ctx, pool, newUser.ID, provider, providerUID, email); linkErr != nil {
		return nil, false, fmt.Errorf("store: link oauth to new user: %w", linkErr)
	}
	return newUser, true, nil
}

// createOAuthUser inserts a users row without a password_hash (OAuth-only, or linkable later).
func createOAuthUser(ctx context.Context, pool *pgxpool.Pool, email, name, avatarURL string) (*User, error) {
	const q = `
		INSERT INTO users (email, name, avatar_url, password_hash)
		VALUES ($1, $2, NULLIF($3,''), NULL)
		RETURNING id, email, name, COALESCE(avatar_url,''), COALESCE(password_hash,''),
		          is_super_admin, created_at, updated_at`

	row := pool.QueryRow(ctx, q, strings.ToLower(email), name, avatarURL)
	return scanUser(row)
}
