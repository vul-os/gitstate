// Package store — rls_newtables_test.go
// S1 cross-org isolation proof for the NEW org-scoped tables added after the
// original projects-only RLS proof (rls_test.go). For each table we insert a row
// under org A's RLS context, switch the context to org B, and assert that org B
// sees ZERO of org A's rows. A sanity read under org A confirms RLS is wired
// (not simply filtering everything).
//
// Mirrors rls_test.go / analytics_test.go: skips cleanly when DATABASE_URL is
// unset, and does ALL work inside one transaction that is ALWAYS rolled back so
// the DB stays clean. RLS is enforced under the non-superuser app role, so the
// inserts MUST set app.current_org first (the WITH CHECK policy requires it).
package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRLSNewTablesCrossOrgIsolation(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set — skipping RLS-new-tables integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer conn.Release()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // always roll back → DB stays clean

	ns := time.Now().UnixNano()

	// Two orgs (organizations has no RLS).
	var orgA, orgB string
	if err := tx.QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("rlsnt-a-%d", ns), "RLS New A").Scan(&orgA); err != nil {
		t.Fatalf("create org A: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("rlsnt-b-%d", ns), "RLS New B").Scan(&orgB); err != nil {
		t.Fatalf("create org B: %v", err)
	}

	setOrg := func(org string) {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_org', $1, true)", org); err != nil {
			t.Fatalf("set org %s: %v", org, err)
		}
	}

	// A shared user + repo for org A (some tables FK to users/repos).
	setOrg(orgA)
	var userA, repoA, prA, projA string
	if err := tx.QueryRow(ctx,
		`INSERT INTO users (email, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("rlsnt-userA-%d@x.io", ns), "User A").Scan(&userA); err != nil {
		t.Fatalf("create user A: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO org_members (org_id, user_id, role) VALUES ($1,$2,'member')`, orgA, userA); err != nil {
		t.Fatalf("member A: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO repos (org_id, platform, external_id, full_name) VALUES ($1,'github',$2,'a/repo') RETURNING id`,
		orgA, fmt.Sprintf("rlsnt-repoA-%d", ns)).Scan(&repoA); err != nil {
		t.Fatalf("create repo A: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO pull_requests (org_id, repo_id, platform, external_id, number, title, state)
		 VALUES ($1,$2,'github',$3,1,'pr','merged') RETURNING id`,
		orgA, repoA, fmt.Sprintf("rlsnt-prA-%d", ns)).Scan(&prA); err != nil {
		t.Fatalf("create pr A: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO projects (org_id, name) VALUES ($1,'Proj A') RETURNING id`, orgA).Scan(&projA); err != nil {
		t.Fatalf("create proj A: %v", err)
	}

	year := time.Now().Year()
	periodStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)

	// leave_type for org A (FK'd by leave_balances).
	var leaveTypeA string
	if err := tx.QueryRow(ctx,
		`INSERT INTO leave_types (org_id, name) VALUES ($1,'PTO') RETURNING id`, orgA).Scan(&leaveTypeA); err != nil {
		t.Fatalf("create leave_type A: %v", err)
	}

	// ── Each entry: insert ONE org-A row, then count rows visible to org B. ──
	// `insert` runs under org A's context (already set per the surrounding setOrg
	// calls); `table` is the relation we re-read under org B.
	type tc struct {
		table  string
		insert string
		args   []any
	}
	cases := []tc{
		{"client_invoices",
			`INSERT INTO client_invoices (org_id, number, period_start, period_end) VALUES ($1,$2,$3,$4)`,
			[]any{orgA, fmt.Sprintf("INV-%d-001", ns), periodStart, periodEnd}},
		{"deployments",
			`INSERT INTO deployments (org_id, repo_id, environment, status, source) VALUES ($1,$2,'production','success','manual')`,
			[]any{orgA, repoA}},
		{"incidents",
			`INSERT INTO incidents (org_id, repo_id, title, severity) VALUES ($1,$2,'down','major')`,
			[]any{orgA, repoA}},
		{"contribution_snapshots",
			`INSERT INTO contribution_snapshots (org_id, user_id, period_start, period_end, composite) VALUES ($1,$2,$3,$4,75)`,
			[]any{orgA, userA, periodStart, periodEnd}},
		{"notification_channels",
			`INSERT INTO notification_channels (org_id, kind, target) VALUES ($1,'slack','https://hooks/x')`,
			[]any{orgA}},
		{"webhook_configs",
			`INSERT INTO webhook_configs (org_id, provider, secret) VALUES ($1,'github',$2)`,
			[]any{orgA, fmt.Sprintf("sek-%d", ns)}},
		{"commit_files",
			`INSERT INTO commit_files (org_id, repo_id, commit_sha, path) VALUES ($1,$2,'sha1','internal/x.go')`,
			[]any{orgA, repoA}},
		{"author_survival",
			`INSERT INTO author_survival (org_id, repo_id, author_email, surviving_lines, authored_lines) VALUES ($1,$2,'a@x.io',10,20)`,
			[]any{orgA, repoA}},
		{"bug_introductions",
			`INSERT INTO bug_introductions (org_id, repo_id, author_email, introduced_sha, fix_sha, lines) VALUES ($1,$2,'a@x.io','isha','fsha',3)`,
			[]any{orgA, repoA}},
		{"leave_types",
			`INSERT INTO leave_types (org_id, name) VALUES ($1,'Sick-A')`,
			[]any{orgA}},
		{"leave_balances",
			`INSERT INTO leave_balances (org_id, user_id, leave_type_id, year, entitled_days) VALUES ($1,$2,$3,$4,20)`,
			[]any{orgA, userA, leaveTypeA, year}},
		{"calendar_connections",
			`INSERT INTO calendar_connections (org_id, user_id, provider, token_encrypted) VALUES ($1,$2,'google',$3)`,
			[]any{orgA, userA, []byte("ciphertext")}},
		{"kudos",
			`INSERT INTO kudos (org_id, from_user, to_user, message) VALUES ($1,$2,$2,'nice')`,
			[]any{orgA, userA}},
		{"client_invoice_lines", "", nil}, // filled below (needs the invoice id)
	}

	// Insert all org-A rows under org A's context.
	setOrg(orgA)
	var invoiceA string
	for i := range cases {
		c := &cases[i]
		switch c.table {
		case "client_invoices":
			if err := tx.QueryRow(ctx, c.insert+" RETURNING id", c.args...).Scan(&invoiceA); err != nil {
				t.Fatalf("insert %s: %v", c.table, err)
			}
		case "client_invoice_lines":
			c.insert = `INSERT INTO client_invoice_lines (org_id, invoice_id, description, amount_cents) VALUES ($1,$2,'line',1000)`
			c.args = []any{orgA, invoiceA}
			if _, err := tx.Exec(ctx, c.insert, c.args...); err != nil {
				t.Fatalf("insert %s: %v", c.table, err)
			}
		default:
			if _, err := tx.Exec(ctx, c.insert, c.args...); err != nil {
				t.Fatalf("insert %s: %v", c.table, err)
			}
		}
	}

	// ── Proof: under org B's context every table must show ZERO org-A rows. ──
	setOrg(orgB)
	for _, c := range cases {
		var n int
		if err := tx.QueryRow(ctx,
			fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE org_id = $1", c.table), orgA).Scan(&n); err != nil {
			t.Fatalf("count %s under org B: %v", c.table, err)
		}
		if n != 0 {
			t.Errorf("S1 VIOLATION: %s leaked %d org-A row(s) to org B", c.table, n)
		}
		// And the bare count (RLS-filtered) must also be zero for org B.
		var total int
		if err := tx.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", c.table)).Scan(&total); err != nil {
			t.Fatalf("bare count %s under org B: %v", c.table, err)
		}
		if total != 0 {
			t.Errorf("S1 VIOLATION: %s shows %d row(s) under org B (RLS not filtering)", c.table, total)
		}
	}

	// ── Sanity: under org A's context the rows ARE visible (RLS is wired). ──
	setOrg(orgA)
	for _, c := range cases {
		var n int
		if err := tx.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", c.table)).Scan(&n); err != nil {
			t.Fatalf("count %s under org A: %v", c.table, err)
		}
		if n < 1 {
			t.Errorf("%s: org A sees %d rows, want >=1 — RLS may be misconfigured", c.table, n)
		}
	}

	t.Logf("RLS isolation OK across %d new tables; org B saw 0 org-A rows", len(cases))
}
