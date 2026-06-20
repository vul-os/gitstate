// Package report implements the reporting layer for gitstate (roadmap §3 reporting 🎯).
// It provides two capabilities:
//
//  1. Dashboard aggregations: project state rollup, burndown series, weekly
//     throughput, and optional LLM status synthesis (via llm.SynthesizeStatus).
//
//  2. NL→report (AnswerQuery): translates a natural-language question into a
//     read-only SELECT over a constrained, documented set of tables, executes it
//     inside db.WithOrg (so RLS enforces org isolation), and returns both the
//     rendered answer and the SQL used for transparency.
//
// # NL→report security model
//
// Allowed tables (the LLM is given ONLY these and their columns):
//
//	issues           — org_id, project_id, title, state, derived_state, labels, created_at, updated_at
//	pull_requests    — org_id, repo_id, title, author_login, state, additions, deletions, merged_at, created_at
//	commits          — org_id, repo_id, sha, author_login, is_agent, message, additions, deletions, committed_at
//	projects         — org_id, name, key, archived, created_at
//	repos            — org_id, platform, full_name, last_synced_at, created_at
//	effort_estimates — org_id, pr_id, issue_id, difficulty, rationale, model, created_at
//	cycle_times      — org_id, pr_id, lead_time_secs, review_secs, computed_at
//	involvement      — org_id, project_id, user_id, period_start, features_shipped, reviews_done, areas_owned, active
//	agent_runs       — org_id, repo_id, goal, tests_passed, human_action, iterations, cost_usd, created_at
//
// Enforcement layers (defence in depth):
//  1. System prompt instructs the model to emit ONLY a single SELECT with no
//     semicolons, DDL, DML, or subquery-based tricks.
//  2. validateSQL() scans the generated SQL before execution and rejects anything
//     that: (a) is not a single statement, (b) contains a semicolon, (c) has a
//     keyword that implies mutation or schema change.
//  3. Execution uses a READ ONLY transaction with a hard statement_timeout of 5 s
//     so even a pathological query cannot hold the connection or perform side effects.
//  4. The org_id is NEVER interpolated into the SQL string; org isolation is
//     enforced entirely by db.WithOrg → SET LOCAL app.current_org, which activates
//     the RLS policy on every allowed table.
package report

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/llm"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// ── Dashboard types ────────────────────────────────────────────────────────────

// CycleTrendPoint is one point on the dashboard's lead-time trend (days per PR,
// oldest→newest), matching the frontend's {date, days} shape.
type CycleTrendPoint struct {
	Date string  `json:"date"`
	Days float64 `json:"days"`
}

// DashboardResult is the response for GET /api/reports/dashboard.
type DashboardResult struct {
	StateCounts    store.IssueStateCounts     `json:"stateCounts"`
	Throughput     []store.ThroughputPoint    `json:"throughput"`
	RecentActivity []store.RecentActivityItem `json:"recentActivity"`
	CycleTrend     []CycleTrendPoint          `json:"cycleTrend"`
	// SynthesizedStatus is the LLM-written prose summary (empty when LLM is not
	// configured or the caller did not request synthesis).
	SynthesizedStatus string `json:"synthesizedStatus,omitempty"`
}

// BurndownResult is the response for GET /api/reports/burndown.
type BurndownResult struct {
	ProjectID string              `json:"projectId,omitempty"`
	Days      int                 `json:"days"`
	Series    []store.BurndownPoint `json:"series"`
}

// ── NL→report types ────────────────────────────────────────────────────────────

// QueryResult is the response for POST /api/reports/query.
type QueryResult struct {
	Answer string                   `json:"answer"`
	SQL    string                   `json:"sql"`
	Rows   []map[string]interface{} `json:"rows"`
}

// ── Service ────────────────────────────────────────────────────────────────────

// Service encapsulates the report domain. Create one per application lifecycle.
type Service struct {
	db  *db.DB
	llm *llm.Service
}

// New constructs a Service. llmSvc may wrap a nil provider (when no API key is
// configured); all LLM-dependent paths return a clear error in that case.
func New(database *db.DB, llmSvc *llm.Service) *Service {
	return &Service{db: database, llm: llmSvc}
}

// ── Dashboard ──────────────────────────────────────────────────────────────────

// Dashboard computes the state rollup, throughput, and recent activity for an
// org. When synthesize is true it also calls llm.SynthesizeStatus to produce a
// prose summary; if the LLM is not configured the field is left empty (not an
// error).
func (s *Service) Dashboard(ctx context.Context, orgID string, synthesize bool) (*DashboardResult, error) {
	var result DashboardResult

	err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var err error

		result.StateCounts, err = store.IssueStateRollup(ctx, tx, orgID)
		if err != nil {
			return err
		}

		result.Throughput, err = store.WeeklyThroughput(ctx, tx, orgID, 12)
		if err != nil {
			return err
		}

		result.RecentActivity, err = store.RecentActivity(ctx, tx, orgID, 20)
		if err != nil {
			return err
		}

		// Lead-time trend for the dashboard sparkline: pull cycle_times (RLS-scoped
		// via tx) and map to {date, days}, oldest→newest. ListCycleTimes returns
		// newest-first, so we reverse into chronological order for the chart.
		cts, err := store.ListCycleTimes(ctx, tx, orgID, store.CycleTimeFilter{})
		if err != nil {
			return err
		}
		trend := make([]CycleTrendPoint, 0, len(cts))
		for i := len(cts) - 1; i >= 0; i-- {
			ct := cts[i]
			if ct.LeadTimeSecs == nil {
				continue
			}
			trend = append(trend, CycleTrendPoint{
				Date: ct.ComputedAt.UTC().Format("2006-01-02"),
				Days: float64(*ct.LeadTimeSecs) / 86400.0,
			})
		}
		result.CycleTrend = trend

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("report.Dashboard: %w", err)
	}

	// LLM synthesis is best-effort; a missing key or transient error is non-fatal.
	if synthesize && len(result.RecentActivity) > 0 {
		items := make([]llm.ActivityItem, 0, len(result.RecentActivity))
		for _, a := range result.RecentActivity {
			items = append(items, llm.ActivityItem{
				Kind:   a.Kind,
				Title:  a.Title,
				Author: a.Author,
				State:  a.State,
			})
		}
		text, synErr := s.llm.SynthesizeStatus(ctx, items)
		if synErr != nil && !errors.Is(synErr, llm.ErrLLMNotConfigured) {
			// Propagate real errors; swallow ErrLLMNotConfigured.
			return nil, fmt.Errorf("report.Dashboard: synthesize status: %w", synErr)
		}
		result.SynthesizedStatus = text
	}

	return &result, nil
}

// Burndown returns a daily burndown series for a project (or whole org if
// projectID is ""). days controls the window (default 30).
func (s *Service) Burndown(ctx context.Context, orgID, projectID string, days int) (*BurndownResult, error) {
	if days <= 0 {
		days = 30
	}

	var series []store.BurndownPoint
	err := s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		var err error
		series, err = store.BurndownSeries(ctx, tx, orgID, projectID, days)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("report.Burndown: %w", err)
	}

	return &BurndownResult{
		ProjectID: projectID,
		Days:      days,
		Series:    series,
	}, nil
}

// ── NL→report ─────────────────────────────────────────────────────────────────

// allowedTablesCatalog documents every table the LLM is permitted to query.
// This string is injected verbatim into the LLM system prompt so the model
// knows the exact schema it can address. It also serves as the authoritative
// allowlist for validateSQL.
const allowedTablesCatalog = `
Allowed tables and their queryable columns:

issues(org_id, project_id, title, state, derived_state, labels, source, created_at, updated_at)
pull_requests(org_id, repo_id, title, author_login, state, additions, deletions, changed_files, merged_at, created_at)
commits(org_id, repo_id, sha, author_login, is_agent, message, additions, deletions, committed_at)
projects(org_id, name, key, archived, created_at)
repos(org_id, platform, full_name, last_synced_at, created_at)
effort_estimates(org_id, pr_id, issue_id, difficulty, rationale, model, created_at)
cycle_times(org_id, pr_id, lead_time_secs, review_secs, computed_at)
involvement(org_id, project_id, user_id, period_start, features_shipped, reviews_done, areas_owned, active)
agent_runs(org_id, repo_id, goal, tests_passed, human_action, iterations, cost_usd, created_at)

IMPORTANT NOTES:
- Every table is protected by Row-Level Security (RLS). The org_id filter is enforced
  automatically by the database; you MUST NOT add "WHERE org_id = ..." yourself.
- Use COALESCE where a column may be NULL.
- All timestamps are timestamptz (UTC). Use now() for the current time.
- labels in issues is a text[] array column.
- is_agent in commits is a boolean.
`

// nlSystemPrompt is the full system prompt for the NL→SQL LLM call.
const nlSystemPrompt = `You are a read-only SQL query generator for the gitstate project-tracking database.

Your job: translate the user's natural-language question into a single, correct PostgreSQL SELECT statement.

Rules (strictly enforced — violations will cause query rejection):
1. Emit ONLY a raw SQL SELECT statement. No markdown, no explanation, no preamble.
2. The statement must be a single SELECT. No semicolons anywhere. No CTEs with INSERT/UPDATE/DELETE. No DDL.
3. Do NOT reference any table not listed below. Do NOT attempt to cross-join users/organizations/billing tables.
4. Do NOT add a WHERE clause filtering by org_id — RLS handles isolation automatically.
5. Limit result rows to at most 100 using LIMIT 100 unless the question asks for a specific count.
6. Use only standard PostgreSQL. No procedural code, no COPY, no pg_read_file, no system catalogs.
` + allowedTablesCatalog

// mutationRe matches mutation/DDL keywords on WORD BOUNDARIES so that a query
// is rejected for an actual `DELETE`/`UPDATE`/`CREATE` statement (including one
// embedded in a CTE) but NOT for ordinary columns that merely contain those
// substrings — e.g. created_at, updated_at, deleted_at, offset. Searching the
// whole (lower-cased) string still catches `... IN (DELETE FROM y)`.
var mutationRe = regexp.MustCompile(`(?i)\b(insert|update|delete|drop|create|alter|truncate|grant|revoke|copy|vacuum|analyze|reset|call|execute|merge|comment|cluster|listen|notify)\b`)

// dangerousFns are function/identifier fragments that are unsafe even as a
// substring (no legitimate reporting column contains them), matched literally.
var dangerousFns = []string{
	"pg_sleep", "pg_read_file", "pg_ls", "lo_import", "lo_export", "dblink", "copy_from",
}

// semiRe matches any semicolon (including inside comments or strings, which we
// reject conservatively — a legitimate query never needs a semicolon).
var semiRe = regexp.MustCompile(`;`)

// ErrQueryRejected is returned when the generated SQL fails the safety checks.
var ErrQueryRejected = errors.New("report: generated SQL rejected by safety validator")

// reportTables is the POSITIVE allowlist of tables the NL→report path may read.
// These are exactly the documented, org-scoped (RLS-protected) reporting tables.
// Anything else — especially the identity tables users/oauth_accounts/refresh_tokens
// which have NO RLS — is rejected, so a prompt-injected query can never reach
// credentials or another org's data even though it runs as the app role.
var reportTables = map[string]bool{
	"issues": true, "pull_requests": true, "commits": true, "projects": true,
	"repos": true, "effort_estimates": true, "cycle_times": true,
	"involvement": true, "agent_runs": true,
}

// tableRefRe captures the identifier following FROM/JOIN (the referenced table);
// cteRe captures CTE names defined via `<name> AS (` so they're allowed too.
var (
	tableRefRe = regexp.MustCompile(`(?i)\b(?:from|join)\s+([a-z_][a-z0-9_]*)`)
	cteRe      = regexp.MustCompile(`(?i)\b([a-z_][a-z0-9_]*)\s+as\s*\(`)
)

// validateSQL rejects any SQL that isn't a plain SELECT over the allowlisted
// reporting tables. Returns a descriptive error explaining the violation.
func validateSQL(sql string) error {
	trimmed := strings.TrimSpace(sql)

	// Must begin with SELECT (case-insensitive).
	if !strings.HasPrefix(strings.ToUpper(trimmed), "SELECT") {
		return fmt.Errorf("%w: statement must begin with SELECT", ErrQueryRejected)
	}

	// No semicolons (multi-statement) and no comments (-- or /* */ evasion).
	if semiRe.MatchString(trimmed) {
		return fmt.Errorf("%w: semicolons are not allowed", ErrQueryRejected)
	}
	if strings.Contains(trimmed, "--") || strings.Contains(trimmed, "/*") {
		return fmt.Errorf("%w: SQL comments are not allowed", ErrQueryRejected)
	}

	lower := strings.ToLower(trimmed)

	// Reject mutation/DDL keywords. We search the whole string so that tricks
	// like embedding DELETE inside a WITH clause are caught.
	if m := mutationRe.FindString(lower); m != "" {
		return fmt.Errorf("%w: disallowed keyword %q", ErrQueryRejected, strings.TrimSpace(m))
	}
	for _, fn := range dangerousFns {
		if strings.Contains(lower, fn) {
			return fmt.Errorf("%w: disallowed function %q", ErrQueryRejected, fn)
		}
	}

	// POSITIVE table allowlist: every FROM/JOIN target must be a known reporting
	// table or a CTE defined in this query. Blocks reads of non-RLS identity
	// tables (users/oauth_accounts/refresh_tokens) and any cross-org table.
	allowed := map[string]bool{}
	for _, m := range cteRe.FindAllStringSubmatch(lower, -1) {
		allowed[m[1]] = true
	}
	for _, m := range tableRefRe.FindAllStringSubmatch(lower, -1) {
		t := m[1]
		if reportTables[t] || allowed[t] {
			continue
		}
		return fmt.Errorf("%w: table %q is not in the reporting allowlist", ErrQueryRejected, t)
	}

	return nil
}

// queryStatementTimeout is the per-statement timeout applied inside the
// read-only transaction for NL→report execution. It bounds the impact of
// a slow or pathological LLM-generated query.
const queryStatementTimeout = "5000" // milliseconds

// AnswerQuery translates a natural-language question into a SQL SELECT, executes
// it, and returns the raw rows plus an LLM-generated prose answer.
//
// Security guarantees (see package doc for the full model):
//   - Returns ErrLLMNotConfigured immediately if no LLM API key is present.
//   - SQL is generated by the LLM under a restrictive system prompt.
//   - validateSQL() rejects any non-SELECT or suspicious statement before execution.
//   - Execution runs in a READ ONLY transaction with a statement_timeout of 5 s.
//   - db.WithOrg sets SET LOCAL app.current_org so RLS policies fire on every read.
//   - The orgID is never interpolated into the SQL string.
func (s *Service) AnswerQuery(ctx context.Context, orgID, question string) (*QueryResult, error) {
	if question = strings.TrimSpace(question); question == "" {
		return nil, fmt.Errorf("report.AnswerQuery: question is required")
	}

	// Step 1: translate question → SQL.
	rawSQL, err := s.llm.Complete(ctx, nlSystemPrompt, question)
	if err != nil {
		if errors.Is(err, llm.ErrLLMNotConfigured) {
			return nil, err
		}
		return nil, fmt.Errorf("report.AnswerQuery: LLM call failed: %w", err)
	}

	// Strip any accidental markdown fences the model might have added.
	generatedSQL := stripFences(rawSQL)

	// Step 2: safety validation — reject before touching the database.
	if err := validateSQL(generatedSQL); err != nil {
		return nil, err
	}

	// Step 3: execute inside a read-only org-scoped transaction.
	var rows []map[string]interface{}
	var colNames []string

	err = s.db.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		// Hard statement timeout — bounds runaway queries.
		if _, err := tx.Exec(ctx, "SET LOCAL statement_timeout = "+queryStatementTimeout); err != nil {
			return fmt.Errorf("set statement_timeout: %w", err)
		}
		// Read-only mode — database-level enforcement of no writes.
		if _, err := tx.Exec(ctx, "SET LOCAL default_transaction_read_only = true"); err != nil {
			return fmt.Errorf("set read only: %w", err)
		}

		pgRows, err := tx.Query(ctx, generatedSQL)
		if err != nil {
			return fmt.Errorf("execute query: %w", err)
		}
		defer pgRows.Close()

		fields := pgRows.FieldDescriptions()
		colNames = make([]string, len(fields))
		for i, f := range fields {
			colNames[i] = string(f.Name)
		}

		for pgRows.Next() {
			vals, err := pgRows.Values()
			if err != nil {
				return fmt.Errorf("scan row values: %w", err)
			}
			row := make(map[string]interface{}, len(vals))
			for i, v := range vals {
				row[colNames[i]] = marshalValue(v)
			}
			rows = append(rows, row)
		}
		return pgRows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("report.AnswerQuery: %w", err)
	}

	// Step 4: synthesize a prose answer from the rows + original question.
	answer, err := s.synthesizeAnswer(ctx, question, generatedSQL, rows)
	if err != nil {
		// Non-fatal: return rows + SQL even without the prose summary.
		answer = fmt.Sprintf("Query returned %d row(s). (LLM answer generation failed: %s)", len(rows), err)
	}

	return &QueryResult{
		Answer: answer,
		SQL:    generatedSQL,
		Rows:   rows,
	}, nil
}

// synthesizeAnswer asks the LLM to write a brief human-readable answer from the
// query results. This is a best-effort step; errors are non-fatal.
func (s *Service) synthesizeAnswer(ctx context.Context, question, sql string, rows []map[string]interface{}) (string, error) {
	const sysProse = `You are a helpful data analyst summarising database query results for a software project manager.
Given the original question, the SQL that was run, and the resulting rows, write a clear, concise answer in 1–3 sentences.
Do not mention technical details like table names or SQL syntax. Focus on what the data means for the team.`

	rowJSON, _ := json.Marshal(rows)
	user := fmt.Sprintf("Question: %s\n\nSQL:\n%s\n\nRows (%d):\n%s", question, sql, len(rows), string(rowJSON))

	text, err := s.llm.Complete(ctx, sysProse, user)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(text), nil
}

// ── Helpers ────────────────────────────────────────────────────────────────────

// stripFences removes optional ```sql / ``` or ``` / ``` markdown fences that
// the LLM might wrap around its SQL output despite being instructed not to.
func stripFences(s string) string {
	s = strings.TrimSpace(s)
	for _, fence := range []string{"```sql", "```"} {
		if strings.HasPrefix(s, fence) {
			s = strings.TrimPrefix(s, fence)
			if idx := strings.LastIndex(s, "```"); idx >= 0 {
				s = s[:idx]
			}
			s = strings.TrimSpace(s)
			break
		}
	}
	return s
}

// marshalValue converts pgx native types to JSON-serialisable Go values.
// time.Time → RFC3339 string; everything else passes through unchanged.
func marshalValue(v interface{}) interface{} {
	if t, ok := v.(time.Time); ok {
		return t.UTC().Format(time.RFC3339)
	}
	return v
}
