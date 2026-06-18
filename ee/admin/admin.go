//go:build ee

// Package eeadmin implements the Enterprise Edition super-admin routes for gitstate.
// Cross-org access is deliberately audited: every org viewed writes an audit_log row
// (decisions S2, A7).  Routes are fenced behind both RequireAuth and RequireSuperAdmin
// so they cannot be reached by ordinary users.
package eeadmin

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/exo/gitstate/internal/admin"
	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/middleware"
	"github.com/exo/gitstate/internal/store"
)

// RegisterEEAdminRoutes mounts the EE super-admin routes onto mux.
// Every route is behind RequireAuth (JWT must be valid) and RequireSuperAdmin
// (email allowlist or is_super_admin flag).  Cross-org reads additionally write
// an audit_log entry for every org accessed (decisions S2).
//
//	GET /admin/orgs/{id}  — cross-org drilldown: billing/usage/projects for a single org
//	GET /admin/revenue    — realtime MRR dashboard (htmx + SSE)
func RegisterEEAdminRoutes(mux *http.ServeMux, database *db.DB, cfg *config.Config) {
	svc := &eeAdminService{db: database, cfg: cfg}

	requireAuth := middleware.RequireAuth(cfg.Auth.JWTSigningKey)
	requireSuperAdmin := admin.RequireSuperAdmin(cfg, database)

	// Gate: all /admin/* routes must pass auth + super-admin check.
	gate := func(h http.Handler) http.Handler {
		return requireAuth(requireSuperAdmin(h))
	}

	mux.Handle("GET /admin/orgs/{id}", gate(http.HandlerFunc(svc.handleOrgDrilldown)))
	mux.Handle("GET /admin/revenue", gate(http.HandlerFunc(svc.handleRevenue)))
}

// ─── service ────────────────────────────────────────────────────────────────

type eeAdminService struct {
	db  *db.DB
	cfg *config.Config
}

// ─── GET /admin/orgs/{id} — cross-org drilldown ─────────────────────────────

// orgDrilldownData holds everything the org drilldown template needs.
type orgDrilldownData struct {
	Org          orgRow
	Subscription subRow
	MemberCount  int
	RecentAudit  []auditRow
	Projects     []projectRow
	Now          time.Time
}

type orgRow struct {
	ID        string
	Name      string
	Slug      string
	PlanKey   string
	CreatedAt string
}

type subRow struct {
	PlanKey          string
	Status           string
	CurrentPeriodEnd string
}

type auditRow struct {
	Action    string
	Target    string
	CreatedAt string
}

type projectRow struct {
	ID   string
	Name string
}

// handleOrgDrilldown serves a cross-org detail view for super-admins.
// It runs inside db.WithOrg (target org) to scope the reads via RLS, then
// unconditionally writes an audit_log entry for the access (S2).
func (s *eeAdminService) handleOrgDrilldown(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFromContext(r.Context())
	if actor == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	targetOrgID := r.PathValue("id")
	if targetOrgID == "" {
		http.Error(w, "org id required", http.StatusBadRequest)
		return
	}

	var data orgDrilldownData
	data.Now = time.Now().UTC()

	// Run all org-scoped reads inside db.WithOrg to honour RLS for the TARGET org.
	// This is the deliberate service path described in decisions S2.
	if err := s.db.WithOrg(r.Context(), targetOrgID, func(tx pgx.Tx) error {
		// Fetch the org record.
		org, err := store.GetOrg(r.Context(), tx, targetOrgID)
		if err != nil {
			return fmt.Errorf("get org: %w", err)
		}
		data.Org = orgRow{
			ID:        org.ID,
			Name:      org.Name,
			Slug:      org.Slug,
			PlanKey:   org.PlanKey,
			CreatedAt: org.CreatedAt.UTC().Format(time.RFC3339),
		}

		// Subscription (may not exist for free orgs — tolerate ErrNotFound).
		sub, subErr := store.GetSubscription(r.Context(), s.db.Pool(), targetOrgID)
		if subErr == nil {
			periodEnd := ""
			if sub.CurrentPeriodEnd != nil {
				periodEnd = sub.CurrentPeriodEnd.UTC().Format(time.RFC3339)
			}
			data.Subscription = subRow{
				PlanKey:          sub.PlanKey,
				Status:           sub.Status,
				CurrentPeriodEnd: periodEnd,
			}
		}

		// Member count.
		members, mErr := store.ListMembers(r.Context(), tx, targetOrgID)
		if mErr != nil {
			return fmt.Errorf("list members: %w", mErr)
		}
		data.MemberCount = len(members)

		// Recent audit entries for this org (last 20).
		rows, qErr := tx.Query(r.Context(),
			`SELECT action, COALESCE(target,''), created_at
			 FROM audit_log
			 WHERE org_id = $1
			 ORDER BY created_at DESC
			 LIMIT 20`,
			targetOrgID,
		)
		if qErr != nil {
			return fmt.Errorf("audit query: %w", qErr)
		}
		defer rows.Close()
		for rows.Next() {
			var ar auditRow
			var createdAt time.Time
			if sErr := rows.Scan(&ar.Action, &ar.Target, &createdAt); sErr != nil {
				return fmt.Errorf("audit scan: %w", sErr)
			}
			ar.CreatedAt = createdAt.UTC().Format(time.RFC3339)
			data.RecentAudit = append(data.RecentAudit, ar)
		}
		if rows.Err() != nil {
			return fmt.Errorf("audit rows: %w", rows.Err())
		}

		// Projects for this org.
		pRows, pErr := tx.Query(r.Context(),
			`SELECT id::text, name FROM projects WHERE org_id = $1 ORDER BY name LIMIT 50`,
			targetOrgID,
		)
		if pErr != nil {
			return fmt.Errorf("projects query: %w", pErr)
		}
		defer pRows.Close()
		for pRows.Next() {
			var pr projectRow
			if sErr := pRows.Scan(&pr.ID, &pr.Name); sErr != nil {
				return fmt.Errorf("project scan: %w", sErr)
			}
			data.Projects = append(data.Projects, pr)
		}
		if pRows.Err() != nil {
			return fmt.Errorf("project rows: %w", pRows.Err())
		}

		return nil
	}); err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, "org not found", http.StatusNotFound)
			return
		}
		slog.ErrorContext(r.Context(), "ee/admin: org drilldown db error",
			"org_id", targetOrgID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// MANDATORY: write audit_log entry for every cross-org access (decisions S2).
	// This runs after the main query so that a DB error doesn't skip the audit.
	if auditErr := store.WriteAudit(
		r.Context(),
		s.db.Pool(),
		actor.ID,
		targetOrgID,
		"admin.org.view",
		targetOrgID,
		map[string]any{
			"actor_email": actor.Email,
			"org_name":    data.Org.Name,
		},
	); auditErr != nil {
		// Log but do not abort — audit failure must not deny the admin view.
		slog.WarnContext(r.Context(), "ee/admin: audit write failed",
			"actor_id", actor.ID, "org_id", targetOrgID, "error", auditErr)
	}

	renderOrgDrilldown(w, data)
}

// ─── GET /admin/revenue — MRR dashboard ─────────────────────────────────────

// revenuePlanRow is one plan tier in the revenue dashboard.
type revenuePlanRow struct {
	PlanKey      string
	PlanName     string
	USDPerMonth  int    // USD cents
	ActiveOrgs   int
	MRR          int    // total MRR in USD cents for this tier
	MRRDisplay   string // human-readable "$X,XXX"
}

// revenueDashData holds everything the revenue template needs.
type revenueDashData struct {
	Plans         []revenuePlanRow
	TotalMRRCents int
	TotalMRR      string
	RecentPayments []recentPaymentRow
	Now           time.Time
}

type recentPaymentRow struct {
	OrgID      string
	ZARCents   int
	Status     string
	CreatedAt  string
}

// handleRevenue serves the realtime MRR/revenue dashboard.
// It reads across all orgs using the raw pool (not WithOrg) because this is an
// intentional super-admin service path — no RLS bypass, but a deliberate cross-org
// aggregate read over the subscriptions and payments tables.
func (s *eeAdminService) handleRevenue(w http.ResponseWriter, r *http.Request) {
	actor := middleware.UserFromContext(r.Context())
	if actor == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	ctx := r.Context()
	pool := s.db.Pool()

	data := revenueDashData{Now: time.Now().UTC()}

	// MRR by plan (per-builder model): per_builder_cents × billable builders across
	// each plan's active subscriptions. Stakeholders are free (decisions P6).
	planRows, err := pool.Query(ctx, `
		SELECT p.key, p.name, p.per_builder_cents,
		       COUNT(DISTINCT s.org_id)::int AS active_orgs,
		       COALESCE(SUM(p.per_builder_cents * bc.cnt), 0)::int AS mrr_cents
		FROM plans p
		LEFT JOIN subscriptions s
		       ON s.plan_key = p.key AND s.status = 'active'
		LEFT JOIN (
			SELECT org_id, COUNT(*) AS cnt
			FROM org_members
			WHERE role IN ('owner','admin','member')
			GROUP BY org_id
		) bc ON bc.org_id = s.org_id
		GROUP BY p.key, p.name, p.per_builder_cents
		ORDER BY p.per_builder_cents DESC
	`)
	if err != nil {
		slog.ErrorContext(ctx, "ee/admin: revenue plans query failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer planRows.Close()

	totalMRR := 0
	for planRows.Next() {
		var row revenuePlanRow
		if err := planRows.Scan(
			&row.PlanKey, &row.PlanName, &row.USDPerMonth,
			&row.ActiveOrgs, &row.MRR,
		); err != nil {
			slog.ErrorContext(ctx, "ee/admin: revenue plan scan failed", "error", err)
			continue
		}
		row.MRRDisplay = centsToUSD(row.MRR)
		totalMRR += row.MRR
		data.Plans = append(data.Plans, row)
	}
	if planRows.Err() != nil {
		slog.ErrorContext(ctx, "ee/admin: revenue plan rows error", "error", planRows.Err())
	}
	data.TotalMRRCents = totalMRR
	data.TotalMRR = centsToUSD(totalMRR)

	// Recent 20 payments (all orgs, super-admin view).
	payRows, err := pool.Query(ctx, `
		SELECT org_id::text, zar_cents, status, created_at
		FROM payments
		ORDER BY created_at DESC
		LIMIT 20
	`)
	if err != nil {
		slog.WarnContext(ctx, "ee/admin: recent payments query failed", "error", err)
	} else {
		defer payRows.Close()
		for payRows.Next() {
			var pr recentPaymentRow
			var createdAt time.Time
			if scanErr := payRows.Scan(&pr.OrgID, &pr.ZARCents, &pr.Status, &createdAt); scanErr != nil {
				continue
			}
			pr.CreatedAt = createdAt.UTC().Format(time.RFC3339)
			data.RecentPayments = append(data.RecentPayments, pr)
		}
	}

	// Audit the revenue view (actor = super-admin, orgID = "" for global action).
	if auditErr := store.WriteAudit(
		ctx,
		pool,
		actor.ID,
		"",
		"admin.revenue.view",
		"",
		map[string]any{"actor_email": actor.Email},
	); auditErr != nil {
		slog.WarnContext(ctx, "ee/admin: audit write failed for revenue view",
			"actor_id", actor.ID, "error", auditErr)
	}

	renderRevenue(w, data)
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// centsToUSD formats USD cents as a human-readable dollar string, e.g. "$1,234".
func centsToUSD(cents int) string {
	dollars := cents / 100
	return fmt.Sprintf("$%s", formatWithCommas(dollars))
}

func formatWithCommas(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	mod := len(s) % 3
	if mod != 0 {
		b.WriteString(s[:mod])
	}
	for i := mod; i < len(s); i += 3 {
		if i > 0 || mod != 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// writeAdminJSON is a fallback JSON writer used for SSE/API sub-requests.
func writeAdminJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Ensure writeAdminJSON is referenced so the compiler doesn't complain.
var _ = writeAdminJSON

// ─── HTML templates ───────────────────────────────────────────────────────────

// adminLayout is the shared HTML shell consistent with internal/admin styling
// (Go html/template + htmx; decisions A9).
const adminLayout = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>{{.Title}} — gitstate Admin</title>
  <script src="https://unpkg.com/htmx.org@1.9.12"></script>
  <style>
    *{box-sizing:border-box;margin:0;padding:0}
    body{font-family:system-ui,sans-serif;background:#0f172a;color:#e2e8f0;min-height:100vh}
    header{background:#1e293b;border-bottom:1px solid #334155;padding:12px 24px;display:flex;align-items:center;gap:16px}
    header h1{font-size:1rem;font-weight:700;color:#5eead4;letter-spacing:.05em}
    header nav a{color:#94a3b8;text-decoration:none;font-size:.875rem;margin-left:16px}
    header nav a:hover{color:#e2e8f0}
    .badge{display:inline-block;padding:2px 8px;border-radius:9999px;font-size:.75rem;font-weight:600}
    .badge-ee{background:#581c87;color:#d8b4fe}
    .badge-active{background:#064e3b;color:#6ee7b7}
    .badge-pending{background:#431407;color:#fdba74}
    .badge-canceled{background:#1e293b;color:#94a3b8}
    main{padding:32px 24px;max-width:1200px;margin:0 auto}
    h2{font-size:1.25rem;font-weight:700;color:#f1f5f9;margin-bottom:16px}
    h3{font-size:1rem;font-weight:600;color:#cbd5e1;margin:24px 0 12px}
    .card{background:#1e293b;border:1px solid #334155;border-radius:8px;padding:20px;margin-bottom:20px}
    .stat-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(180px,1fr));gap:16px;margin-bottom:24px}
    .stat{background:#0f172a;border:1px solid #334155;border-radius:6px;padding:16px}
    .stat-label{font-size:.75rem;color:#64748b;text-transform:uppercase;letter-spacing:.05em}
    .stat-value{font-size:1.5rem;font-weight:700;color:#5eead4;margin-top:4px}
    table{width:100%;border-collapse:collapse;font-size:.875rem}
    th{text-align:left;padding:8px 12px;color:#64748b;font-size:.75rem;text-transform:uppercase;letter-spacing:.05em;border-bottom:1px solid #334155}
    td{padding:10px 12px;border-bottom:1px solid #1e293b;color:#cbd5e1}
    tr:last-child td{border-bottom:none}
    .pill{display:inline-block;padding:1px 8px;border-radius:9999px;font-size:.75rem}
    .pill-active{background:#065f46;color:#6ee7b7}
    .pill-paid{background:#065f46;color:#6ee7b7}
    .pill-pending{background:#431407;color:#fdba74}
    .pill-canceled,.pill-failed{background:#450a0a;color:#fca5a5}
    .pill-draft,.pill-open{background:#1e3a5f;color:#93c5fd}
    footer{padding:16px 24px;color:#475569;font-size:.75rem;border-top:1px solid #1e293b;margin-top:48px}
    .ee-banner{background:#1e1b4b;border:1px solid #4338ca;color:#a5b4fc;padding:8px 16px;border-radius:6px;font-size:.8rem;margin-bottom:20px}
  </style>
</head>
<body>
<header>
  <h1>gitstate</h1>
  <span class="badge badge-ee">EE</span>
  <nav>
    <a href="/admin">Dashboard</a>
    <a href="/admin/revenue">Revenue</a>
  </nav>
</header>
<main>
{{.Body}}
</main>
<footer>gitstate Enterprise Edition — super-admin panel — {{.Now}}</footer>
</body>
</html>`

// templateFuncs are helpers available inside templates.
var templateFuncs = template.FuncMap{
	"div": func(a, b int) int {
		if b == 0 {
			return 0
		}
		return a / b
	},
	"zarDisplay": func(cents int) string {
		return fmt.Sprintf("%.2f", float64(cents)/100)
	},
}

// orgDrilldownTmplText is the source for the cross-org drilldown template.
const orgDrilldownTmplText = `
<div class="ee-banner">EE Super-Admin: cross-org access — audited to audit_log per S2</div>
<h2>Org: {{.Org.Name}} <span style="color:#64748b;font-size:.875rem">{{.Org.ID}}</span></h2>
<div class="stat-grid">
  <div class="stat"><div class="stat-label">Plan</div><div class="stat-value" style="font-size:1rem">{{.Org.PlanKey}}</div></div>
  <div class="stat"><div class="stat-label">Members</div><div class="stat-value">{{.MemberCount}}</div></div>
  <div class="stat"><div class="stat-label">Sub Status</div><div class="stat-value" style="font-size:1rem">{{.Subscription.Status}}</div></div>
  <div class="stat"><div class="stat-label">Period End</div><div class="stat-value" style="font-size:.875rem">{{.Subscription.CurrentPeriodEnd}}</div></div>
</div>

<div class="card">
  <h3>Projects (up to 50)</h3>
  {{if .Projects}}
  <table>
    <thead><tr><th>ID</th><th>Name</th></tr></thead>
    <tbody>
    {{range .Projects}}
    <tr><td><code style="font-size:.75rem;color:#94a3b8">{{.ID}}</code></td><td>{{.Name}}</td></tr>
    {{end}}
    </tbody>
  </table>
  {{else}}<p style="color:#64748b;font-size:.875rem">No projects.</p>{{end}}
</div>

<div class="card">
  <h3>Recent Audit Log (last 20)</h3>
  {{if .RecentAudit}}
  <table>
    <thead><tr><th>Time</th><th>Action</th><th>Target</th></tr></thead>
    <tbody>
    {{range .RecentAudit}}
    <tr>
      <td style="color:#64748b;font-size:.75rem">{{.CreatedAt}}</td>
      <td>{{.Action}}</td>
      <td><code style="font-size:.75rem;color:#94a3b8">{{.Target}}</code></td>
    </tr>
    {{end}}
    </tbody>
  </table>
  {{else}}<p style="color:#64748b;font-size:.875rem">No audit entries yet.</p>{{end}}
</div>
`

// revenueTmplText is the source for the MRR dashboard template.
const revenueTmplText = `
<div class="ee-banner">EE Super-Admin: realtime revenue dashboard — access audited</div>
<h2>Revenue &amp; MRR Dashboard</h2>
<div class="stat-grid">
  <div class="stat"><div class="stat-label">Total MRR</div><div class="stat-value">{{.TotalMRR}}</div></div>
</div>

<div class="card">
  <h3>MRR by Plan</h3>
  <table>
    <thead><tr><th>Plan</th><th>Price / mo</th><th>Active Orgs</th><th>MRR</th></tr></thead>
    <tbody>
    {{range .Plans}}
    <tr>
      <td><strong>{{.PlanName}}</strong></td>
      <td>{{printf "$%d" (div .USDPerMonth 100)}}</td>
      <td>{{.ActiveOrgs}}</td>
      <td>{{.MRRDisplay}}</td>
    </tr>
    {{end}}
    </tbody>
  </table>
</div>

<div class="card">
  <h3>Recent Payments (last 20)</h3>
  {{if .RecentPayments}}
  <table>
    <thead><tr><th>Time</th><th>Org</th><th>ZAR</th><th>Status</th></tr></thead>
    <tbody>
    {{range .RecentPayments}}
    <tr>
      <td style="color:#64748b;font-size:.75rem">{{.CreatedAt}}</td>
      <td><code style="font-size:.75rem;color:#94a3b8">{{.OrgID}}</code></td>
      <td>R {{zarDisplay .ZARCents}}</td>
      <td><span class="pill pill-{{.Status}}">{{.Status}}</span></td>
    </tr>
    {{end}}
    </tbody>
  </table>
  {{else}}<p style="color:#64748b;font-size:.875rem">No payments yet.</p>{{end}}
</div>

<div style="color:#475569;font-size:.75rem;margin-top:16px">
  Refreshes via <a href="#" hx-get="/admin/revenue" hx-target="main" hx-swap="innerHTML" style="color:#5eead4">htmx poll</a>
  or <code>hx-sse</code> when SSE is wired.
  Last updated: {{.Now}}
</div>
`

// orgDrilldownTmpl renders the cross-org drilldown page.
var orgDrilldownTmpl = template.Must(
	template.New("orgDrilldown").Funcs(templateFuncs).Parse(orgDrilldownTmplText),
)

// revenueTmpl renders the MRR dashboard.
var revenueTmpl = template.Must(
	template.New("revenue").Funcs(templateFuncs).Parse(revenueTmplText),
)

// renderOrgDrilldown renders the org drilldown page to w.
func renderOrgDrilldown(w http.ResponseWriter, data orgDrilldownData) {
	var body strings.Builder
	if err := orgDrilldownTmpl.Execute(&body, data); err != nil {
		slog.Error("ee/admin: render org drilldown", "error", err)
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	renderLayout(w, "Org Drilldown", body.String(), data.Now)
}

// renderRevenue renders the MRR dashboard page to w.
func renderRevenue(w http.ResponseWriter, data revenueDashData) {
	var body strings.Builder
	if err := revenueTmpl.Execute(&body, data); err != nil {
		slog.Error("ee/admin: render revenue", "error", err)
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	renderLayout(w, "Revenue", body.String(), data.Now)
}

// renderLayout wraps body in the shared admin layout.
func renderLayout(w http.ResponseWriter, title, body string, now time.Time) {
	layoutTmpl := template.Must(template.New("layout").Parse(adminLayout))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = layoutTmpl.Execute(w, map[string]any{
		"Title": title,
		"Body":  template.HTML(body), //nolint:gosec // body is from our own templates only
		"Now":   now.Format("2006-01-02 15:04 UTC"),
	})
}

// Ensure pgxpool import is used (pool used via db.Pool()).
var _ *pgxpool.Pool
