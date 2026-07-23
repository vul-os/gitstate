// Package store — audit_log writer.
// WriteAudit is the single write path for the audit_log table (decisions S2).
// It is intentionally simple: fire-and-forget within the calling request context.
package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// WriteAudit appends one row to audit_log.
//
//   - actorID — UUID string of the acting user; may be empty for system actions.
//   - orgID   — UUID string of the org being touched; may be empty for global actions
//     (e.g. super-admin listing all orgs).
//   - action  — short verb, e.g. "super_admin.view_org", "billing.generate_invoice".
//   - target  — the specific resource identifier, e.g. a repo ID or org slug.
//   - meta    — arbitrary JSON-serialisable context to accompany the event.
//
// audit_log is NOT org-scoped via RLS (it is a platform table), so this function
// uses the raw pool rather than a WithOrg transaction.
func WriteAudit(ctx context.Context, pool *pgxpool.Pool, actorID, orgID, action, target string, meta map[string]any) error {
	// Serialize meta to JSON; a nil map becomes '{}'.
	var metaJSON []byte
	if len(meta) == 0 {
		metaJSON = []byte("{}")
	} else {
		var err error
		metaJSON, err = json.Marshal(meta)
		if err != nil {
			return fmt.Errorf("store: audit: marshal meta: %w", err)
		}
	}

	// Use NULL for empty actor/org IDs so foreign-key constraints are satisfied.
	var actorArg, orgArg *string
	if actorID != "" {
		actorArg = &actorID
	}
	if orgID != "" {
		orgArg = &orgID
	}

	const q = `
		INSERT INTO audit_log (actor_id, org_id, action, target, meta)
		VALUES ($1, $2, $3, $4, $5)`

	if _, err := pool.Exec(ctx, q, actorArg, orgArg, action, target, metaJSON); err != nil {
		return fmt.Errorf("store: audit: insert: %w", err)
	}
	return nil
}
