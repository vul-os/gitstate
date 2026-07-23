// Package report — pure unit tests for the NL→SQL safety validator, markdown
// fence stripping, and value marshalling. No DB or LLM required.
package report

import (
	"errors"
	"testing"
	"time"
)

func TestValidateSQL_AllowsPlainSelect(t *testing.T) {
	ok := []string{
		"SELECT * FROM issues LIMIT 100",
		"  select count(*) from commits  ",
		"SELECT title FROM pull_requests WHERE state = 'merged' LIMIT 100",
		"SELECT a, b FROM projects ORDER BY name DESC LIMIT 10",
	}
	for _, q := range ok {
		if err := validateSQL(q); err != nil {
			t.Errorf("validateSQL(%q) = %v, want nil", q, err)
		}
	}
}

func TestValidateSQL_RejectsNonSelect(t *testing.T) {
	bad := []struct {
		name string
		sql  string
	}{
		{"not select", "WITH x AS (SELECT 1) SELECT * FROM x"}, // begins with WITH
		{"insert", "INSERT INTO issues VALUES (1)"},
		{"update", "UPDATE issues SET state='done'"},
		{"delete", "DELETE FROM commits"},
		{"drop", "DROP TABLE issues"},
		{"create", "CREATE TABLE x (a int)"},
		{"alter", "ALTER TABLE issues ADD COLUMN x int"},
		{"truncate", "TRUNCATE issues"},
		{"semicolon", "SELECT 1; DROP TABLE issues"},
		{"trailing semicolon", "SELECT 1 FROM issues;"},
		{"embedded delete in cte", "SELECT * FROM x WHERE id IN (DELETE FROM y RETURNING id)"},
		{"set", "SELECT 1; SET statement_timeout = 0"},
		{"pg_sleep", "SELECT pg_sleep(10)"},
		{"pg_read_file", "SELECT pg_read_file('/etc/passwd')"},
		{"copy", "SELECT 1 FROM issues COPY"},
		{"grant", "SELECT 1; GRANT ALL"},
		{"explain", "EXPLAIN SELECT * FROM issues"}, // doesn't begin with SELECT anyway, but keyword too
		{"empty", ""},
		{"whitespace only", "   "},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			err := validateSQL(c.sql)
			if err == nil {
				t.Errorf("validateSQL(%q) = nil, want rejection", c.sql)
				return
			}
			if !errors.Is(err, ErrQueryRejected) {
				t.Errorf("error should wrap ErrQueryRejected, got %v", err)
			}
		})
	}
}

// TestValidateSQL_TimestampColumnsAllowed verifies the word-boundary validator
// ACCEPTS ordinary timestamp columns that merely contain a keyword substring
// (created_at/updated_at/deleted_at, and the OFFSET clause) — these are on
// every table and were previously false-rejected by a substring scan.
func TestValidateSQL_TimestampColumnsAllowed(t *testing.T) {
	ok := []string{
		"SELECT created_at FROM issues LIMIT 100",
		"SELECT id FROM issues WHERE deleted_at IS NULL LIMIT 100",
		"SELECT title, updated_at FROM pull_requests ORDER BY updated_at DESC LIMIT 50",
		"SELECT id FROM commits ORDER BY committed_at DESC LIMIT 10 OFFSET 20",
	}
	for _, q := range ok {
		if err := validateSQL(q); err != nil {
			t.Errorf("expected valid query to pass, got %v\n  %s", err, q)
		}
	}
	// But real mutations on a word boundary are still rejected.
	bad := []string{
		"SELECT * FROM x WHERE id IN (DELETE FROM y RETURNING id)",
		"SELECT 1; UPDATE issues SET state='done'",
	}
	for _, q := range bad {
		if err := validateSQL(q); err == nil {
			t.Errorf("expected mutation query to be rejected: %s", q)
		}
	}
}

// TestValidateSQL_TableAllowlist verifies the positive table allowlist: only the
// documented org-scoped reporting tables (and CTEs) are readable; the non-RLS
// identity tables — the prompt-injection credential-exposure risk — are rejected.
func TestValidateSQL_TableAllowlist(t *testing.T) {
	allowed := []string{
		"SELECT count(*) FROM issues WHERE state = 'open'",
		"SELECT c.sha FROM commits c JOIN pull_requests pr ON pr.id = c.pr_id",
		"SELECT difficulty FROM effort_estimates LIMIT 50",
		"SELECT a.* FROM involvement a JOIN projects p ON p.id = a.project_id",
	}
	for _, q := range allowed {
		if err := validateSQL(q); err != nil {
			t.Errorf("expected allowlisted query to pass, got %v\n  %s", err, q)
		}
	}
	blocked := []string{
		"SELECT email, password_hash FROM users",
		"SELECT user_id, org_id FROM org_members",
		"SELECT token_hash FROM refresh_tokens",
		"SELECT * FROM oauth_accounts",
		"SELECT * FROM audit_log",
		"SELECT id FROM issues UNION SELECT password_hash FROM users", // join via second FROM
		"SELECT id FROM issues -- AND password_hash FROM users",       // comment evasion
	}
	for _, q := range blocked {
		if err := validateSQL(q); err == nil {
			t.Errorf("expected non-allowlisted/comment query to be REJECTED: %s", q)
		}
	}
}

// TestValidateSQL_CommaJoinBypass is a regression test for a cross-tenant /
// credential-exfiltration hole: validateSQL only checked the table immediately
// after FROM/JOIN, so an implicit comma join (`FROM issues, users`) or a quoted /
// space-omitted identifier (`FROM "users"`) reached a NON-RLS identity table
// (users / oauth_accounts / refresh_tokens) and could dump password hashes and
// OAuth/refresh tokens. Every table in the FROM list must now be allowlisted.
func TestValidateSQL_CommaJoinBypass(t *testing.T) {
	mustReject := []string{
		"SELECT * FROM issues, users",
		"SELECT u.password_hash FROM issues, users u",
		"SELECT * FROM issues u, oauth_accounts v",
		"SELECT * FROM ISSUES, USERS",
		`SELECT * FROM "users"`,
		"SELECT * FROM issues, refresh_tokens",
		"SELECT * FROM issues JOIN users ON true",
		"SELECT * FROM issues CROSS JOIN users",
		"SELECT * FROM issues, projects, users",
		"SELECT * FROM issues i JOIN users u ON u.id = i.id",
	}
	for _, q := range mustReject {
		if err := validateSQL(q); err == nil {
			t.Errorf("comma-join/quoted bypass NOT rejected: %q", q)
		}
	}
	// Legitimate multi-table reporting queries must still pass.
	mustAllow := []string{
		"SELECT i.id, p.name FROM issues i, projects p WHERE i.project_id = p.id",
		"SELECT id FROM issues, commits, projects",
		"SELECT id, created_at, updated_at FROM issues ORDER BY created_at LIMIT 10",
		"SELECT c.sha FROM commits c JOIN pull_requests pr ON pr.id = c.pr_id",
	}
	for _, q := range mustAllow {
		if err := validateSQL(q); err != nil {
			t.Errorf("legitimate query wrongly rejected: %q: %v", q, err)
		}
	}
}

func TestStripFences(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no fence", "SELECT 1", "SELECT 1"},
		{"sql fence", "```sql\nSELECT 1\n```", "SELECT 1"},
		{"bare fence", "```\nSELECT 2\n```", "SELECT 2"},
		{"leading whitespace", "  ```sql\nSELECT 3\n```  ", "SELECT 3"},
		{"fence with trailing text dropped", "```sql\nSELECT 4\n```\nignored", "SELECT 4"},
		{"multiline body", "```sql\nSELECT a\nFROM t\n```", "SELECT a\nFROM t"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stripFences(c.in); got != c.want {
				t.Errorf("stripFences(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestStripFences_ThenValidate(t *testing.T) {
	// The pipeline: a model wraps its SELECT in fences; after stripping it validates.
	cleaned := stripFences("```sql\nSELECT * FROM issues LIMIT 100\n```")
	if err := validateSQL(cleaned); err != nil {
		t.Errorf("fenced valid select rejected after strip: %v", err)
	}
}

func TestMarshalValue(t *testing.T) {
	// time.Time → RFC3339 UTC string.
	tt := time.Date(2026, 6, 15, 14, 30, 0, 0, time.FixedZone("X", 3600))
	got := marshalValue(tt)
	s, ok := got.(string)
	if !ok {
		t.Fatalf("time should marshal to string, got %T", got)
	}
	if s != "2026-06-15T13:30:00Z" {
		t.Errorf("marshalValue(time) = %q, want UTC RFC3339", s)
	}

	// Non-time values pass through unchanged.
	for _, v := range []interface{}{42, "text", 3.14, true, nil} {
		if marshalValue(v) != v {
			t.Errorf("marshalValue(%v) changed the value", v)
		}
	}
}
