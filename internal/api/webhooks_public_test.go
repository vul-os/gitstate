// Package api — webhooks_public_test.go
// DB-backed HTTP tests for the PUBLIC, unauthenticated inbound webhook receiver:
//   - POST /api/webhooks/github  — the inbound receiver: valid X-Hub-Signature-256
//     HMAC over the raw body → processed (and a deployment event writes a
//     deployments row); a bad signature → 401; an unknown event with a valid
//     signature → 200 no-op.
//
// The handler is built with the real DB pool. The tests seed throwaway orgs
// and DELETE them (cascade) at the end so the DB stays clean. They skip cleanly
// when DATABASE_URL is unset.
package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/store"
)

func apiTestDB(t *testing.T) *db.DB {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping api integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	database, err := db.New(ctx, &config.Config{Database: config.DatabaseConfig{URL: dbURL}})
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	return database
}

func ghSign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestWebhookReceiverHTTP(t *testing.T) {
	database := apiTestDB(t)
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ns := time.Now().UnixNano()
	var orgID string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("wh-recv-%d", ns), "Webhook Recv Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	defer func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM organizations WHERE id = $1`, orgID)
	}()

	secret := fmt.Sprintf("hmac-secret-%d", ns)
	fullName := "acme/web"
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		if _, e := store.UpsertWebhookSecret(ctx, tx, orgID, "github", secret); e != nil {
			return e
		}
		_, e := tx.Exec(ctx,
			`INSERT INTO repos (org_id, platform, external_id, full_name) VALUES ($1,'github',$2,$2)`,
			orgID, fullName)
		return e
	}); err != nil {
		t.Fatalf("seed secret+repo: %v", err)
	}

	mux := http.NewServeMux()
	RegisterWebhookReceiver(mux, database)

	depBody := []byte(fmt.Sprintf(`{
		"repository": {"full_name": %q},
		"deployment_status": {"state": "success", "environment": "production", "id": %d, "created_at": "2026-03-10T12:00:00Z"},
		"deployment": {"sha": "abc123", "environment": "production", "id": %d}
	}`, fullName, ns, ns))

	post := func(body []byte, sig, event string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/webhooks/github?org="+orgID, strings.NewReader(string(body)))
		if sig != "" {
			req.Header.Set("X-Hub-Signature-256", sig)
		}
		req.Header.Set("X-GitHub-Event", event)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	// ── valid signature, deployment_status event → 200 processed ──
	rec := post(depBody, ghSign(secret, depBody), "deployment_status")
	if rec.Code != http.StatusOK {
		t.Fatalf("valid deployment: status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var ok struct {
		OK          bool `json:"ok"`
		Deployments int  `json:"deployments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ok); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !ok.OK || ok.Deployments != 1 {
		t.Errorf("response ok=%v deployments=%d, want true/1", ok.OK, ok.Deployments)
	}
	// A deployments row was persisted under the org.
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM deployments WHERE org_id=$1`, orgID).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Errorf("persisted deployments = %d, want 1", n)
		}
		return nil
	}); err != nil {
		t.Fatalf("verify deployment: %v", err)
	}

	// ── bad signature → 401, nothing else processed ──
	recBad := post(depBody, "sha256=deadbeef", "deployment_status")
	if recBad.Code != http.StatusUnauthorized {
		t.Errorf("bad signature: status = %d, want 401", recBad.Code)
	}

	// ── missing signature → 401 ──
	recNone := post(depBody, "", "deployment_status")
	if recNone.Code != http.StatusUnauthorized {
		t.Errorf("missing signature: status = %d, want 401", recNone.Code)
	}

	// ── unknown event w/ valid signature → 200 ignored no-op ──
	unknownBody := []byte(`{"zen":"hi"}`)
	recUnknown := post(unknownBody, ghSign(secret, unknownBody), "ping")
	if recUnknown.Code != http.StatusOK {
		t.Errorf("unknown event: status = %d, want 200", recUnknown.Code)
	}
	var ig struct {
		Ignored bool `json:"ignored"`
	}
	_ = json.Unmarshal(recUnknown.Body.Bytes(), &ig)
	if !ig.Ignored {
		t.Errorf("unknown event ignored=%v, want true", ig.Ignored)
	}
	// Still exactly one deployment (the unknown event wrote nothing).
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM deployments WHERE org_id=$1`, orgID).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Errorf("deployments after unknown event = %d, want 1", n)
		}
		return nil
	}); err != nil {
		t.Fatalf("verify post-unknown: %v", err)
	}

	t.Logf("webhook receiver OK: valid→200+row, bad sig→401, unknown→200 no-op")
}

func TestGitLabWebhookReceiverHTTP(t *testing.T) {
	database := apiTestDB(t)
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ns := time.Now().UnixNano()
	var orgID string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("gl-recv-%d", ns), "GitLab Recv Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	defer func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM organizations WHERE id = $1`, orgID)
	}()

	// X-Gitlab-Token IS the shared secret. Seed it as the org's gitlab secret plus a
	// connected repo so the deployment ingests.
	token := fmt.Sprintf("gl-token-%d", ns)
	fullName := "acme/api"
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		if _, e := store.UpsertWebhookSecret(ctx, tx, orgID, "gitlab", token); e != nil {
			return e
		}
		_, e := tx.Exec(ctx,
			`INSERT INTO repos (org_id, platform, external_id, full_name) VALUES ($1,'gitlab',$2,$2)`,
			orgID, fullName)
		return e
	}); err != nil {
		t.Fatalf("seed gitlab secret+repo: %v", err)
	}

	mux := http.NewServeMux()
	RegisterWebhookReceiver(mux, database)

	depBody := []byte(fmt.Sprintf(`{
		"project": {"path_with_namespace": %q},
		"status": "success",
		"deployable_id": %d,
		"environment": "production",
		"sha": "deadbeefcafe",
		"status_changed_at": "2026-03-10 12:00:00 +0000"
	}`, fullName, ns))

	post := func(body []byte, tok, event, orgHint string) *httptest.ResponseRecorder {
		url := "/api/webhooks/gitlab"
		if orgHint != "" {
			url += "?org=" + orgHint
		}
		req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(string(body)))
		if tok != "" {
			req.Header.Set("X-Gitlab-Token", tok)
		}
		req.Header.Set("X-Gitlab-Event", event)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	// ── correct token + ?org= hint, Deployment Hook → 200 processed + row ──
	rec := post(depBody, token, "Deployment Hook", orgID)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid gitlab deployment: status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var ok struct {
		OK          bool `json:"ok"`
		Deployments int  `json:"deployments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ok); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !ok.OK || ok.Deployments != 1 {
		t.Errorf("response ok=%v deployments=%d, want true/1", ok.OK, ok.Deployments)
	}
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM deployments WHERE org_id=$1`, orgID).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Errorf("persisted deployments = %d, want 1", n)
		}
		return nil
	}); err != nil {
		t.Fatalf("verify deployment: %v", err)
	}

	// ── wrong token (correct hint) → 401, nothing processed ──
	recBad := post(depBody, token+"-tampered", "Deployment Hook", orgID)
	if recBad.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: status = %d, want 401", recBad.Code)
	}

	// ── missing token → 401 ──
	recNone := post(depBody, "", "Deployment Hook", orgID)
	if recNone.Code != http.StatusUnauthorized {
		t.Errorf("missing token: status = %d, want 401", recNone.Code)
	}

	// ── correct token but wrong org hint → 401 (secret read under the wrong org) ──
	recWrongOrg := post(depBody, token, "Deployment Hook", "00000000-0000-0000-0000-000000000000")
	if recWrongOrg.Code != http.StatusUnauthorized {
		t.Errorf("wrong org hint: status = %d, want 401", recWrongOrg.Code)
	}

	// ── unknown event w/ correct token → 200 ignored no-op ──
	recUnknown := post([]byte(`{"project":{"path_with_namespace":"acme/api"}}`), token, "System Hook", orgID)
	if recUnknown.Code != http.StatusOK {
		t.Errorf("unknown event: status = %d, want 200", recUnknown.Code)
	}
	var ig struct {
		Ignored bool `json:"ignored"`
	}
	_ = json.Unmarshal(recUnknown.Body.Bytes(), &ig)
	if !ig.Ignored {
		t.Errorf("unknown event ignored=%v, want true", ig.Ignored)
	}

	t.Logf("gitlab receiver OK: token+?org=→200+row (constant-time), wrong/missing token→401, unknown→200 no-op")
}
