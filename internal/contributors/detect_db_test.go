package contributors

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestDetectAndUpsert_DB seeds a small org with several multi-identity people
// plus a bot, runs DetectAndUpsert, and asserts the clustering collapses the raw
// identities into the right contributors and is idempotent + manual-edit-safe.
func TestDetectAndUpsert_DB(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping detect DB test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer conn.Release()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orgID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("detect-test-%d", time.Now().UnixNano()), "Detect Test Org").Scan(&orgID); err != nil {
		t.Fatalf("org: %v", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org',$1,true)", orgID); err != nil {
		t.Fatalf("set org: %v", err)
	}
	var repo string
	if err := tx.QueryRow(ctx,
		`INSERT INTO repos (org_id, platform, external_id, full_name)
		 VALUES ($1,'github',$2,'acme/x') RETURNING id`,
		orgID, fmt.Sprintf("ext-%d", time.Now().UnixNano())).Scan(&repo); err != nil {
		t.Fatalf("repo: %v", err)
	}

	// Seed commits: jane (login + 2 emails), bob (login + email), a bot.
	commits := []struct {
		sha, login, email string
		agent             bool
	}{
		{"c1", "jane", "jane@gmail.com", false},
		{"c2", "jane", "1+jane@users.noreply.github.com", false},
		{"c3", "bob", "bob@corp.com", false},
		{"c4", "dependabot[bot]", "49+dependabot[bot]@users.noreply.github.com", true},
	}
	for i, c := range commits {
		if _, err := tx.Exec(ctx,
			`INSERT INTO commits (org_id, repo_id, sha, author_login, author_email, is_agent, committed_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			orgID, repo, c.sha, c.login, c.email, c.agent, time.Now().Add(-time.Duration(i)*time.Hour)); err != nil {
			t.Fatalf("commit %s: %v", c.sha, err)
		}
	}

	// Run detection.
	res, err := DetectAndUpsert(ctx, tx, orgID)
	if err != nil {
		t.Fatalf("DetectAndUpsert: %v", err)
	}
	if res.Contributors == 0 || res.Identities == 0 {
		t.Fatalf("detect result = %+v, want some contributors+identities", res)
	}

	// jane's three identities (login + 2 emails) → ONE contributor.
	var janeContribs int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(DISTINCT contributor_id) FROM contributor_identities
		WHERE org_id=$1 AND value IN ('jane','jane@gmail.com','1+jane@users.noreply.github.com')`,
		orgID).Scan(&janeContribs); err != nil {
		t.Fatalf("jane query: %v", err)
	}
	if janeContribs != 1 {
		t.Errorf("jane mapped to %d contributors, want 1", janeContribs)
	}

	// Total distinct contributors should be 3 (jane, bob, bot) — << 6 raw identities.
	var total int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM contributors WHERE org_id=$1`, orgID).Scan(&total); err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 3 {
		t.Errorf("distinct contributors = %d, want 3 (jane, bob, bot)", total)
	}

	// The bot cluster is flagged is_bot.
	var botFlagged bool
	if err := tx.QueryRow(ctx, `
		SELECT c.is_bot FROM contributors c
		JOIN contributor_identities ci ON ci.contributor_id=c.id
		WHERE ci.org_id=$1 AND ci.value='dependabot[bot]'`, orgID).Scan(&botFlagged); err != nil {
		t.Fatalf("bot query: %v", err)
	}
	if !botFlagged {
		t.Errorf("dependabot contributor not flagged is_bot")
	}

	// jane's primary email is the real gmail (not the noreply).
	var janeEmail string
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(c.primary_email,'') FROM contributors c
		JOIN contributor_identities ci ON ci.contributor_id=c.id
		WHERE ci.org_id=$1 AND ci.value='jane' LIMIT 1`, orgID).Scan(&janeEmail); err != nil {
		t.Fatalf("jane email query: %v", err)
	}
	if janeEmail != "jane@gmail.com" {
		t.Errorf("jane primary_email=%q, want jane@gmail.com", janeEmail)
	}

	// Idempotency: a second run attaches nothing new.
	res2, err := DetectAndUpsert(ctx, tx, orgID)
	if err != nil {
		t.Fatalf("DetectAndUpsert (2nd): %v", err)
	}
	if res2.Contributors != 0 || res2.Identities != 0 {
		t.Errorf("2nd run = %+v, want all-zero (idempotent)", res2)
	}

	// Manual-edit safety: rename jane, add a NEW identity that clusters onto her,
	// re-detect → her manual name survives and the new identity attaches to HER.
	var janeID string
	if err := tx.QueryRow(ctx, `
		SELECT contributor_id::text FROM contributor_identities
		WHERE org_id=$1 AND value='jane' LIMIT 1`, orgID).Scan(&janeID); err != nil {
		t.Fatalf("jane id: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE contributors SET display_name='Jane Manual' WHERE org_id=$1 AND id=$2`, orgID, janeID); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO commits (org_id, repo_id, sha, author_login, author_email, is_agent, committed_at)
		 VALUES ($1,$2,'c5','jane','jane@othercorp.com',false,now())`, orgID, repo); err != nil {
		t.Fatalf("new commit: %v", err)
	}
	res3, err := DetectAndUpsert(ctx, tx, orgID)
	if err != nil {
		t.Fatalf("DetectAndUpsert (3rd): %v", err)
	}
	if res3.Merged == 0 {
		t.Errorf("3rd run merged=%d, want >=1 (new email attached to existing jane)", res3.Merged)
	}
	var name string
	var newEmailContrib string
	if err := tx.QueryRow(ctx, `SELECT display_name FROM contributors WHERE org_id=$1 AND id=$2`, orgID, janeID).Scan(&name); err != nil {
		t.Fatalf("name check: %v", err)
	}
	if name != "Jane Manual" {
		t.Errorf("manual display name clobbered: %q", name)
	}
	if err := tx.QueryRow(ctx, `
		SELECT contributor_id::text FROM contributor_identities
		WHERE org_id=$1 AND value='jane@othercorp.com'`, orgID).Scan(&newEmailContrib); err != nil {
		t.Fatalf("new email contrib: %v", err)
	}
	if newEmailContrib != janeID {
		t.Errorf("new email attached to %s, want existing jane %s", newEmailContrib, janeID)
	}
}
