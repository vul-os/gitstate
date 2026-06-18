// Package db provides the pgx connection pool and RLS session helpers for gitstate.
// Every org-scoped query runs inside WithOrg which sets SET LOCAL app.current_org
// before executing the callback, enforcing the RLS boundary (decisions A2/S1).
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/exo/gitstate/internal/config"
)

// DB wraps a pgxpool.Pool and provides org-scoped transaction helpers.
type DB struct {
	pool *pgxpool.Pool
}

// New opens a pgx connection pool using the DATABASE_URL in cfg.
// Returns an error if the pool cannot be created; the caller may still choose
// to boot the server with a nil DB for dev convenience (see cmd/gitstate/main.go).
func New(ctx context.Context, cfg *config.Config) (*DB, error) {
	if cfg.Database.URL == "" {
		return nil, fmt.Errorf("db: DATABASE_URL is not set")
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.Database.URL)
	if err != nil {
		return nil, fmt.Errorf("db: parse DATABASE_URL: %w", err)
	}

	if cfg.Database.MaxConns > 0 {
		poolCfg.MaxConns = int32(cfg.Database.MaxConns)
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("db: open pool: %w", err)
	}

	return &DB{pool: pool}, nil
}

// Pool returns the underlying pgxpool.Pool for callers that need direct access
// (e.g. store queries that run outside an org-scoped tx).
func (d *DB) Pool() *pgxpool.Pool {
	return d.pool
}

// Ping verifies that the database is reachable.
func (d *DB) Ping(ctx context.Context) error {
	if err := d.pool.Ping(ctx); err != nil {
		return fmt.Errorf("db: ping: %w", err)
	}
	return nil
}

// Close closes the connection pool. Should be called on shutdown.
func (d *DB) Close() {
	d.pool.Close()
}

// WithOrg opens a transaction, sets the RLS boundary via
// SET LOCAL app.current_org = orgID, then calls fn with the transaction.
// Commits on success; rolls back on fn error or panic.
// This is the required entry point for every org-scoped database operation.
func (d *DB) WithOrg(ctx context.Context, orgID string, fn func(pgx.Tx) error) error {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db: begin tx: %w", err)
	}

	// Set the RLS parameter so org_isolation policies fire correctly.
	if _, err := tx.Exec(ctx, "SET LOCAL app.current_org = $1", orgID); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("db: set app.current_org: %w", err)
	}

	// Run the caller's work.
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit tx: %w", err)
	}
	return nil
}
