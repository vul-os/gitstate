// Package store — regression test: GetIssue / BuildIssueContext must report a
// missing issue as ErrNotFound (→ HTTP 404), not a wrapped pgx.ErrNoRows that
// the API layer turns into a 500.
package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestGetIssueUnknownIDReturnsErrNotFound(t *testing.T) {
	database := tokensTestDB(t)
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ns := time.Now().UnixNano()
	var orgID string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("nf-%d", ns), "NotFound Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, orgID)
	})

	// A well-formed UUID that does not exist in this org.
	const missing = "00000000-0000-0000-0000-000000000000"

	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		// GetIssue must surface the sentinel, not a wrapped pgx.ErrNoRows.
		if _, err := GetIssue(ctx, tx, orgID, missing); !errors.Is(err, ErrNotFound) {
			return fmt.Errorf("GetIssue unknown id = %v, want ErrNotFound", err)
		}
		// BuildIssueContext (the agent context endpoint's core) must propagate it.
		if _, err := BuildIssueContext(ctx, tx, orgID, missing); !errors.Is(err, ErrNotFound) {
			return fmt.Errorf("BuildIssueContext unknown id = %v, want ErrNotFound", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
