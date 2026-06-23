// Package api — contribution_window_test.go
// DB-backed HTTP test proving the contribution report backfills `involvement`
// across the requested [from,to] window before scoring.
//
// Regression guard for the "contribution not right on synced data" bug: nothing
// computed involvement across history (only the Involvement page did, for ~2
// recent months), so the shipped / review / ownership dimensions were empty for
// any historical or all-time window. The fix (contributionHandlers.ensureInvolvement)
// computes involvement for every calendar month the window touches, idempotently.
//
// This test seeds a user with commits + a merged PR in an OLD month (well outside
// the default 90-day window), drives the real GET /api/contribution over an
// all-time window, and asserts ownership/shipped populate AND that an involvement
// row now exists for that old month. Skips when DATABASE_URL is unset.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/exo/gitstate/internal/config"
	"github.com/jackc/pgx/v5"
)

func TestContributionReport_BackfillsInvolvementAcrossWindow(t *testing.T) {
	database := apiTestDB(t)
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	const signingKey = "test-signing-key-for-contrib-window"
	cfg := &config.Config{}
	cfg.Auth.JWTSigningKey = signingKey

	ns := time.Now().UnixNano()
	var orgID string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("contrib-win-%d", ns), "Contrib Window Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	defer func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM organizations WHERE id = $1`, orgID)
	}()

	// Owner who will call the endpoint.
	_, ownerTok := seedMember(t, ctx, database, signingKey, orgID, "owner")

	// The contributor identity: a users row whose email matches the commit/PR
	// author so ComputeInvolvement can resolve login→email→user.
	login := fmt.Sprintf("dev%d", ns)
	email := fmt.Sprintf("%s@example.test", login)
	var devUserID string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO users (email, name) VALUES ($1, 'Old Month Dev') RETURNING id`, email).Scan(&devUserID); err != nil {
		t.Fatalf("create dev user: %v", err)
	}
	defer func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM users WHERE id = $1`, devUserID)
	}()

	// An OLD calendar month, far outside the default 90-day window.
	oldMonth := time.Date(2021, 3, 1, 0, 0, 0, 0, time.UTC)
	commitAt := oldMonth.AddDate(0, 0, 10) // 2021-03-11
	mergedAt := oldMonth.AddDate(0, 0, 12)

	// Seed a repo + commit + merged PR INSIDE db.WithOrg (FORCE RLS).
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var repoID string
		if err := tx.QueryRow(ctx,
			`INSERT INTO repos (org_id, platform, external_id, full_name, default_branch)
			 VALUES ($1,'github',$2,$3,'main') RETURNING id`,
			orgID, fmt.Sprintf("ext-%d", ns), "acme/old").Scan(&repoID); err != nil {
			return fmt.Errorf("insert repo: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO commits (org_id, repo_id, sha, author_login, author_email, additions, deletions, is_agent, committed_at)
			 VALUES ($1,$2,$3,$4,$5,120,8,false,$6)`,
			orgID, repoID, fmt.Sprintf("sha-%d", ns), login, email, commitAt); err != nil {
			return fmt.Errorf("insert commit: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO pull_requests (org_id, repo_id, platform, external_id, number, title, author_login, state, created_at, merged_at)
			 VALUES ($1,$2,'github',$3,7,'Old feature',$4,'merged',$5,$6)`,
			orgID, repoID, fmt.Sprintf("pr-%d", ns), login, commitAt, mergedAt); err != nil {
			return fmt.Errorf("insert PR: %w", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("seed under WithOrg: %v", err)
	}

	// Sanity: no involvement row for the old month yet (read under RLS).
	var before int
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM involvement WHERE org_id=$1 AND period_start=$2 AND user_id=$3`,
			orgID, "2021-03-01", devUserID).Scan(&before)
	}); err != nil {
		t.Fatalf("count involvement before: %v", err)
	}
	if before != 0 {
		t.Fatalf("expected 0 involvement rows for the old month before the request, got %d", before)
	}

	// Drive the real endpoint over an all-time window that includes 2021-03.
	mux := http.NewServeMux()
	RegisterContributionRoutes(mux, database, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/contribution?from=2000-01-01&to=2026-12-31", nil)
	req.Header.Set("Authorization", "Bearer "+ownerTok)
	req.Header.Set("X-Org-ID", orgID)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/contribution: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// The involvement row for the OLD month must now exist (backfilled). involvement
	// has FORCE RLS, so this read must run inside WithOrg to see the org's rows.
	var after, areasOwned, featuresShipped int
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*), COALESCE(max(areas_owned),0), COALESCE(max(features_shipped),0)
			 FROM involvement WHERE org_id=$1 AND period_start=$2 AND user_id=$3`,
			orgID, "2021-03-01", devUserID).Scan(&after, &areasOwned, &featuresShipped)
	}); err != nil {
		t.Fatalf("count involvement after: %v", err)
	}
	if after == 0 {
		t.Fatal("involvement row for the old month was NOT backfilled by the contribution request")
	}
	if areasOwned < 1 {
		t.Errorf("areas_owned = %d, want >= 1 (the dev committed to one repo)", areasOwned)
	}
	if featuresShipped < 1 {
		t.Errorf("features_shipped = %d, want >= 1 (one merged PR)", featuresShipped)
	}

	// And the report itself must surface that dev with non-zero ownership/shipped.
	var resp struct {
		Members []struct {
			Email      string `json:"email"`
			Dimensions struct {
				Ownership struct {
					Raw struct {
						AreasOwned int `json:"areasOwned"`
					} `json:"raw"`
				} `json:"ownership"`
				Shipped struct {
					Raw struct {
						MergedPRs int `json:"mergedPRs"`
					} `json:"raw"`
				} `json:"shipped"`
			} `json:"dimensions"`
		} `json:"members"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	var found bool
	for _, m := range resp.Members {
		if m.Email == email {
			found = true
			if m.Dimensions.Ownership.Raw.AreasOwned < 1 {
				t.Errorf("report ownership.areasOwned = %d, want >= 1", m.Dimensions.Ownership.Raw.AreasOwned)
			}
			if m.Dimensions.Shipped.Raw.MergedPRs < 1 {
				t.Errorf("report shipped.mergedPRs = %d, want >= 1", m.Dimensions.Shipped.Raw.MergedPRs)
			}
		}
	}
	if !found {
		t.Fatalf("dev %q not present in contribution report members", email)
	}

	t.Logf("contribution window backfill OK: involvement rows=%d areas=%d shipped=%d",
		after, areasOwned, featuresShipped)
}
