// Package chat — tools_read_db_test.go
// DB-backed test that a read tool returns real data computed from seeded git
// activity. Seeds one org + repo + commits via the pool (org RLS context set),
// then drives the get_analytics_summary, top_contributors, and list_repos tools
// through their handlers (which open their own db.WithOrg transactions) and
// asserts the totals. Skips cleanly when DATABASE_URL is unset, matching the
// other integration tests.
package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
)

func TestReadToolReturnsSeededData(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping chat read-tool integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	database, err := db.New(ctx, &config.Config{Database: config.DatabaseConfig{URL: dbURL}})
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	defer database.Close()

	ns := time.Now().UnixNano()
	var orgID string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("chat-read-%d", ns), "Chat Read Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	defer func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM organizations WHERE id = $1`, orgID)
	}()

	// Org RLS context for the seed inserts on the pool.
	if _, err := database.Pool().Exec(ctx, `SELECT set_config('app.current_org', $1, false)`, orgID); err != nil {
		t.Fatalf("set current_org: %v", err)
	}

	var repoID string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO repos (org_id, platform, external_id, full_name)
		 VALUES ($1,'github',$2,'acme/alpha') RETURNING id`,
		orgID, fmt.Sprintf("ext-%d", ns)).Scan(&repoID); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	// Three commits by jane, one by bob — all within the default look-back window.
	at := time.Now().UTC().AddDate(0, 0, -7)
	seed := []struct {
		sha, login, email string
		adds, dels        int
	}{
		{"c1", "jane", "jane@work", 100, 10},
		{"c2", "jane", "jane@work", 50, 5},
		{"c3", "jane", "jane@work", 20, 0},
		{"c4", "bob", "bob@work", 10, 2},
	}
	for i, s := range seed {
		if _, err := database.Pool().Exec(ctx,
			`INSERT INTO commits (org_id, repo_id, sha, author_login, author_email,
			 is_agent, message, additions, deletions, committed_at)
			 VALUES ($1,$2,$3,$4,$5,false,$6,$7,$8,$9)`,
			orgID, repoID, fmt.Sprintf("%s-%d", s.sha, ns), s.login, s.email,
			"msg", s.adds, s.dels, at.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatalf("insert commit %s: %v", s.sha, err)
		}
	}

	reg := NewRegistry()

	// ── get_analytics_summary ────────────────────────────────────────────────
	summaryTool, _ := reg.Lookup("get_analytics_summary")
	res, action, err := summaryTool.Handler(ctx, database, orgID, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("get_analytics_summary: %v", err)
	}
	if action != nil {
		t.Error("read tool must not return an Action")
	}
	var summary struct {
		TotalCommits int   `json:"totalCommits"`
		Repos        int   `json:"repos"`
		Contributors int   `json:"contributors"`
		Additions    int64 `json:"additions"`
	}
	if err := json.Unmarshal(res, &summary); err != nil {
		t.Fatalf("decode summary: %v (%s)", err, res)
	}
	if summary.TotalCommits != 4 {
		t.Errorf("totalCommits = %d, want 4", summary.TotalCommits)
	}
	if summary.Repos != 1 {
		t.Errorf("repos = %d, want 1", summary.Repos)
	}
	if summary.Contributors != 2 {
		t.Errorf("contributors = %d, want 2", summary.Contributors)
	}
	if summary.Additions != 180 {
		t.Errorf("additions = %d, want 180", summary.Additions)
	}

	// ── top_contributors ─────────────────────────────────────────────────────
	contribTool, _ := reg.Lookup("top_contributors")
	res, _, err = contribTool.Handler(ctx, database, orgID, json.RawMessage(`{"limit":5}`))
	if err != nil {
		t.Fatalf("top_contributors: %v", err)
	}
	var contributors []struct {
		Login   string `json:"login"`
		Commits int    `json:"commits"`
	}
	if err := json.Unmarshal(res, &contributors); err != nil {
		t.Fatalf("decode contributors: %v", err)
	}
	if len(contributors) != 2 {
		t.Fatalf("contributors = %d, want 2", len(contributors))
	}
	if contributors[0].Login != "jane" || contributors[0].Commits != 3 {
		t.Errorf("top contributor = %+v, want jane with 3 commits", contributors[0])
	}

	// ── list_repos ───────────────────────────────────────────────────────────
	reposTool, _ := reg.Lookup("list_repos")
	res, _, err = reposTool.Handler(ctx, database, orgID, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("list_repos: %v", err)
	}
	var repos []struct {
		ID       string `json:"id"`
		FullName string `json:"fullName"`
	}
	if err := json.Unmarshal(res, &repos); err != nil {
		t.Fatalf("decode repos: %v", err)
	}
	if len(repos) != 1 || repos[0].FullName != "acme/alpha" {
		t.Errorf("repos = %+v, want one acme/alpha", repos)
	}
}
