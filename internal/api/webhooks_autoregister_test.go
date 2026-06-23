// Package api — webhooks_autoregister_test.go
// Covers the webhook ongoing-sync layer added on top of the receiver:
//
//   - A signature-verified `pull_request_review` GitHub delivery writes a
//     pr_reviews row mapped to the stored PR (DB-backed httptest against the
//     PUBLIC receiver; self-reviews are skipped).
//   - Auto-registration is SKIPPED (logged, no error) when PublicURL is empty or
//     localhost — webhooks only deliver to a public URL (Fly), not localhost.
//   - The publicly-reachable gate + URL-equality helpers that make re-register
//     idempotent (no duplicate hooks).
package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/store"
)

// TestWebhookPullRequestReviewHTTP drives a signature-verified
// pull_request_review delivery and asserts a pr_reviews row lands, while a
// self-review (reviewer == PR author) is skipped.
func TestWebhookPullRequestReviewHTTP(t *testing.T) {
	database := apiTestDB(t)
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ns := time.Now().UnixNano()
	var orgID string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("wh-review-%d", ns), "Webhook Review Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	defer func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM organizations WHERE id = $1`, orgID)
	}()

	secret := fmt.Sprintf("hmac-secret-%d", ns)
	fullName := "acme/web"
	prExternalID := fmt.Sprintf("%d", ns) // the PR's platform id (UpsertPR/GitHub pull_request.id)
	var repoID, prID string
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		if _, e := store.UpsertWebhookSecret(ctx, tx, orgID, "github", secret); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx,
			`INSERT INTO repos (org_id, platform, external_id, full_name) VALUES ($1,'github',$2,$2) RETURNING id`,
			orgID, fullName).Scan(&repoID); e != nil {
			return e
		}
		// Seed the PR the review references (keyed on external_id like the receiver).
		pr := &store.PullRequest{
			OrgID: orgID, RepoID: repoID, Platform: "github",
			ExternalID: prExternalID, Number: 7, Title: "feat", AuthorLogin: "author", State: "open",
			CreatedAt: time.Now().UTC(),
		}
		if e := store.UpsertPR(ctx, tx, pr); e != nil {
			return e
		}
		return tx.QueryRow(ctx,
			`SELECT id FROM pull_requests WHERE org_id=$1 AND repo_id=$2 AND external_id=$3`,
			orgID, repoID, prExternalID).Scan(&prID)
	}); err != nil {
		t.Fatalf("seed secret+repo+pr: %v", err)
	}

	mux := http.NewServeMux()
	RegisterWebhookReceiver(mux, database)

	post := func(body []byte, event string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/webhooks/github?org="+orgID, strings.NewReader(string(body)))
		req.Header.Set("X-Hub-Signature-256", ghSign(secret, body))
		req.Header.Set("X-GitHub-Event", event)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	reviewBody := func(reviewer string) []byte {
		return []byte(fmt.Sprintf(`{
			"action": "submitted",
			"repository": {"full_name": %q},
			"review": {"id": %d, "state": "approved", "submitted_at": "2026-03-10T12:00:00Z", "user": {"login": %q}},
			"pull_request": {"id": %s, "number": 7, "user": {"login": "author"}}
		}`, fullName, ns, reviewer, prExternalID))
	}

	// ── a real reviewer (≠ author) → 200 + one pr_reviews row ──
	rec := post(reviewBody("reviewer"), "pull_request_review")
	if rec.Code != http.StatusOK {
		t.Fatalf("review: status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		revs, e := store.ListPRReviewsForPR(ctx, tx, orgID, prID)
		if e != nil {
			return e
		}
		if len(revs) != 1 {
			t.Fatalf("pr_reviews rows = %d, want 1", len(revs))
		}
		if revs[0].ReviewerLogin != "reviewer" || revs[0].State != "approved" {
			t.Errorf("review = %q/%q, want reviewer/approved", revs[0].ReviewerLogin, revs[0].State)
		}
		return nil
	}); err != nil {
		t.Fatalf("verify review row: %v", err)
	}

	// ── self-review (reviewer == author) → skipped, still exactly one row ──
	recSelf := post(reviewBody("author"), "pull_request_review")
	if recSelf.Code != http.StatusOK {
		t.Errorf("self-review: status = %d, want 200", recSelf.Code)
	}
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		revs, e := store.ListPRReviewsForPR(ctx, tx, orgID, prID)
		if e != nil {
			return e
		}
		if len(revs) != 1 {
			t.Errorf("pr_reviews after self-review = %d, want 1 (self-review skipped)", len(revs))
		}
		return nil
	}); err != nil {
		t.Fatalf("verify post-self-review: %v", err)
	}

	t.Logf("review webhook OK: signed pull_request_review→pr_reviews row, self-review skipped")
}

// TestAutoRegisterSkippedOnLocalhost asserts auto-registration does NOT attempt a
// platform call (and returns no error) when PublicURL is empty/localhost — the
// localhost gate. It needs no DB: the gate is evaluated before any DB/secret work.
func TestAutoRegisterSkippedOnLocalhost(t *testing.T) {
	for _, pu := range []string{"", "http://localhost:8080", "http://127.0.0.1:3000", "https://myapp.local"} {
		cfg := &config.Config{}
		cfg.App.PublicURL = pu
		// A nil *db.DB is safe here: a skipped registration must short-circuit before
		// touching the DB. If it didn't, this would panic — which is the assertion.
		err := autoRegisterRepoWebhook(context.Background(), nil, cfg,
			"org-1", "github", "acme/web", "123", "tok", "")
		if err != nil {
			t.Errorf("PublicURL=%q: autoRegister returned err %v, want nil (skip)", pu, err)
		}
	}
}

// TestPubliclyReachable pins the localhost/private-host gate.
func TestPubliclyReachable(t *testing.T) {
	reachable := []string{"https://app.gitstate.io", "https://gitstate.fly.dev", "http://203.0.113.5"}
	notReachable := []string{
		"", "not a url", "ftp://x", "http://localhost", "https://localhost:8080",
		"http://127.0.0.1", "http://10.0.0.1", "http://192.168.1.1", "https://foo.local",
		"http://host.docker.internal", "http://[::1]",
	}
	for _, u := range reachable {
		if !publiclyReachable(u) {
			t.Errorf("publiclyReachable(%q) = false, want true", u)
		}
	}
	for _, u := range notReachable {
		if publiclyReachable(u) {
			t.Errorf("publiclyReachable(%q) = true, want false", u)
		}
	}
}

// TestSameWebhookURL pins the idempotency comparison: an identical target (modulo
// trailing slash / scheme case) is detected as the same hook so a re-register
// updates rather than duplicates.
func TestSameWebhookURL(t *testing.T) {
	base := "https://app.gitstate.io/api/webhooks/github?org=abc"
	same := []string{
		"https://app.gitstate.io/api/webhooks/github?org=abc",
		"https://app.gitstate.io/api/webhooks/github/?org=abc",
		"HTTPS://APP.GITSTATE.IO/api/webhooks/github?org=abc",
	}
	diff := []string{
		"https://app.gitstate.io/api/webhooks/gitlab?org=abc",
		"https://app.gitstate.io/api/webhooks/github?org=other",
		"https://elsewhere.example/api/webhooks/github?org=abc",
	}
	for _, u := range same {
		if !sameWebhookURL(base, u) {
			t.Errorf("sameWebhookURL(base, %q) = false, want true", u)
		}
	}
	for _, u := range diff {
		if sameWebhookURL(base, u) {
			t.Errorf("sameWebhookURL(base, %q) = true, want false", u)
		}
	}
}
