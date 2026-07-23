// Package api — agent_runs_test.go
// End-to-end HTTP tests over the REAL middleware chain for POST/GET
// /api/agent-runs:
//   - happy path: an API token with write:agent_runs logs a run (201) and the
//     run lists back (200), with supervisor_id defaulting to the token's user;
//   - scope rejection: a token WITHOUT write:agent_runs is 403 on POST.
//
// Skips when DATABASE_URL is unset.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

func TestAgentRunsHTTP(t *testing.T) {
	database := apiTestDB(t)
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cfg := &config.Config{}
	cfg.Auth.JWTSigningKey = "test-signing-key-for-agent-runs"

	ns := time.Now().UnixNano()
	var orgID, userID string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("arunapi-%d", ns), "AgentRunAPI Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM organizations WHERE id=$1`, orgID)
	})
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO users (email,name) VALUES ($1,'Owner') RETURNING id`,
		fmt.Sprintf("arunapi-%d@x.io", ns)).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM users WHERE id=$1`, userID)
	})
	if _, err := database.Pool().Exec(ctx,
		`INSERT INTO org_members (org_id,user_id,role) VALUES ($1,$2,'owner')`, orgID, userID); err != nil {
		t.Fatalf("member: %v", err)
	}

	// Mint two API tokens directly via the store: one with the write scope, one
	// without (only read:issues) to prove RequireScope rejects the write.
	var writeTok, readOnlyTok string
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		raw, _, e := store.CreateAPIToken(ctx, tx, orgID, userID, "writer",
			[]string{"write:agent_runs", "read:issues"}, nil)
		if e != nil {
			return e
		}
		writeTok = raw
		raw2, _, e := store.CreateAPIToken(ctx, tx, orgID, userID, "reader",
			[]string{"read:issues"}, nil)
		if e != nil {
			return e
		}
		readOnlyTok = raw2
		return nil
	}); err != nil {
		t.Fatalf("mint tokens: %v", err)
	}

	mux := http.NewServeMux()
	RegisterAgentRunRoutes(mux, database, cfg)

	// ── Scope rejection: read-only token cannot POST. ──
	{
		body, _ := json.Marshal(map[string]any{"goal": "should be rejected"})
		req := httptest.NewRequest(http.MethodPost, "/api/agent-runs", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+readOnlyTok)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("read-only POST: want 403, got %d (%s)", rec.Code, rec.Body.String())
		}
	}

	// ── Happy path: write token logs a run (201). ──
	var createdID string
	{
		body, _ := json.Marshal(map[string]any{
			"goal":        "fix flaky test",
			"agentName":   "claude-code",
			"branch":      "fix/flaky",
			"humanAction": "accepted",
			"testsPassed": true,
			"iterations":  2,
			"costUsd":     0.13,
			"diffSummary": map[string]int{"additions": 5, "deletions": 1, "changedFiles": 1},
		})
		req := httptest.NewRequest(http.MethodPost, "/api/agent-runs", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+writeTok)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("write POST: want 201, got %d (%s)", rec.Code, rec.Body.String())
		}
		var run store.AgentRun
		if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
			t.Fatalf("decode created run: %v", err)
		}
		if run.ID == "" {
			t.Fatal("created run has no id")
		}
		if run.Goal != "fix flaky test" || run.AgentName != "claude-code" {
			t.Errorf("created run fields wrong: %+v", run)
		}
		// supervisor_id defaults to the token's user.
		if run.SupervisorID == nil || *run.SupervisorID != userID {
			t.Errorf("supervisor_id = %v, want %s", run.SupervisorID, userID)
		}
		if run.HumanAction != "accepted" {
			t.Errorf("human_action = %q, want accepted", run.HumanAction)
		}
		createdID = run.ID
	}

	// ── Invalid human_action is a 400. ──
	{
		body, _ := json.Marshal(map[string]any{"goal": "x", "humanAction": "approved"})
		req := httptest.NewRequest(http.MethodPost, "/api/agent-runs", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+writeTok)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid action POST: want 400, got %d (%s)", rec.Code, rec.Body.String())
		}
	}

	// ── List: the read-only token CAN list (read:issues). ──
	{
		req := httptest.NewRequest(http.MethodGet, "/api/agent-runs", nil)
		req.Header.Set("Authorization", "Bearer "+readOnlyTok)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("list: want 200, got %d (%s)", rec.Code, rec.Body.String())
		}
		var runs []store.AgentRun
		if err := json.Unmarshal(rec.Body.Bytes(), &runs); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		found := false
		for _, r := range runs {
			if r.ID == createdID {
				found = true
			}
		}
		if !found {
			t.Fatalf("listed runs missing created run %s: %+v", createdID, runs)
		}
	}
}
