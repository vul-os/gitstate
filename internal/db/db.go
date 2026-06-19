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

// NewPool opens a standalone pgxpool.Pool from an arbitrary connection URL.
// It is used for the audited super-admin cross-org service pool (decisions S2):
// a second pool, connected as a dedicated BYPASSRLS role, that the admin console
// uses ONLY for instance-wide aggregate reads (MRR/revenue/plan-distribution),
// never for normal org-scoped app traffic.
//
// The caller owns the returned pool and must Close() it on shutdown. maxConns
// of 0 leaves the pgx default in place. The URL (which embeds the password) is
// never logged here — callers must not log it either.
func NewPool(ctx context.Context, url string, maxConns int) (*pgxpool.Pool, error) {
	if url == "" {
		return nil, fmt.Errorf("db: NewPool: empty connection URL")
	}

	poolCfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		// Do not wrap with the URL — it contains the password.
		return nil, fmt.Errorf("db: NewPool: parse connection URL: %w", err)
	}

	if maxConns > 0 {
		poolCfg.MaxConns = int32(maxConns)
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("db: NewPool: open pool: %w", err)
	}
	return pool, nil
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
	// NOTE: SQL `SET LOCAL` does not accept bind parameters ($1); set_config(...,true)
	// is the parameterized, transaction-local equivalent.
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org', $1, true)", orgID); err != nil {
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
