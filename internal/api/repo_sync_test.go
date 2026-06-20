// Package api — repo_sync_test.go
// DB-backed HTTP test for the repo connect/sync path under FORCE RLS. Regression
// guard for the P0 bug where ConnectRepo / GetRepoByIDPool / UpdateRepoSyncedAt
// ran on the bare (non-superuser) pool and so saw zero rows — making
// POST /api/repos/{id}/sync return 404 "repo not found" for an OWNED repo.
//
// The fix wraps the connect/lookup/update in db.WithOrg. This test seeds an org +
// owner, connects a repo via the store inside WithOrg, then drives the real
// /api/repos/{id}/sync handler and asserts 202 (not 404). Skips when DATABASE_URL
// is unset.
package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

func TestRepoSync_OwnedRepoFoundUnderRLS(t *testing.T) {
	database := apiTestDB(t)
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const signingKey = "test-signing-key-for-repo-sync"
	cfg := &config.Config{}
	cfg.Auth.JWTSigningKey = signingKey

	ns := time.Now().UnixNano()
	var orgID string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("repo-sync-%d", ns), "Repo Sync Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	defer func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM organizations WHERE id = $1`, orgID)
	}()

	_, ownerTok := seedMember(t, ctx, database, signingKey, orgID, "owner")

	// Connect a repo via the store INSIDE db.WithOrg (the fixed path). Under FORCE
	// RLS this would return no row / fail on the bare pool.
	var repo *store.Repo
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		r, e := store.ConnectRepo(ctx, tx, orgID, "github", fmt.Sprintf("ext-%d", ns), "acme/web", "main", "")
		repo = r
		return e
	}); err != nil {
		t.Fatalf("connect repo under WithOrg: %v", err)
	}
	if repo == nil || repo.ID == "" {
		t.Fatal("ConnectRepo returned no repo id")
	}

	// GetRepo inside WithOrg must FIND the owned repo (the lookup that used to 404).
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		got, e := store.GetRepo(ctx, tx, orgID, repo.ID)
		if e != nil {
			return e
		}
		if got.ID != repo.ID {
			t.Errorf("GetRepo id = %q, want %q", got.ID, repo.ID)
		}
		return nil
	}); err != nil {
		t.Fatalf("GetRepo under WithOrg: %v", err)
	}

	// UpdateRepoSyncedAt inside WithOrg must succeed and set last_synced_at.
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		return store.UpdateRepoSyncedAt(ctx, tx, orgID, repo.ID)
	}); err != nil {
		t.Fatalf("UpdateRepoSyncedAt under WithOrg: %v", err)
	}

	// Drive the real handler: POST /api/repos/{id}/sync must be 202 (was 404).
	mux := http.NewServeMux()
	RegisterSyncRoutes(mux, database, cfg)

	req := httptest.NewRequest(http.MethodPost, "/api/repos/"+repo.ID+"/sync", nil)
	req.Header.Set("Authorization", "Bearer "+ownerTok)
	req.Header.Set("X-Org-ID", orgID)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST /api/repos/{id}/sync: status = %d, want 202 (body=%s)", rec.Code, rec.Body.String())
	}

	t.Logf("repo sync OK: owned repo found under RLS, sync queued (202)")
}
