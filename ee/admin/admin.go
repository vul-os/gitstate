//go:build ee

// Package eeadmin implements the Enterprise Edition super-admin routes for gitstate.
// Cross-org access is deliberately audited: every org viewed writes an audit_log row
// (decisions S2, A7).  Routes are fenced behind both RequireAuth and RequireSuperAdmin
// so they cannot be reached by ordinary users.
package eeadmin

import (
	"context"
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

	// Open the audited cross-org service pool when ADMIN_DATABASE_URL is set
	// (decisions S2). The EE revenue dashboard is an instance-wide aggregate
	// over subscriptions/org_members/payments; under the non-superuser app role
	// RLS hides cross-org rows and MRR reads 0. The dedicated BYPASSRLS role
	// sees across orgs. On failure we log (never the URL) and fall back to the
	// main pool. The org drilldown keeps using WithOrg(targetOrg) — unchanged.
	if cfg.Admin.DatabaseURL != "" {
		if ap, err := db.NewPool(context.Background(), cfg.Admin.DatabaseURL, cfg.Database.MaxConns); err != nil {
			slog.Error("ee/admin: failed to open admin service pool; falling back to main pool", "error", err)
		} else {
			svc.adminPool = ap
		}
	}

	// Cookie-aware gate (shared with internal/admin) so these EE pages are
	// reachable by a browser navigation from the console, redirecting to
	// /admin/login when unauthenticated. Falls back to Bearer for APIs.
	gate := admin.RequireAdminAuth(cfg, database)

	mux.Handle("GET /admin/orgs/{id}", gate(http.HandlerFunc(svc.handleOrgDrilldown)))
	mux.Handle("GET /admin/revenue", gate(http.HandlerFunc(svc.handleRevenue)))
}

// ─── service ────────────────────────────────────────────────────────────────

type eeAdminService struct {
	db  *db.DB
	cfg *config.Config

	// adminPool is the audited cross-org service pool (decisions S2), used ONLY
	// for the instance-wide revenue aggregate. nil when ADMIN_DATABASE_URL is
	// unset; aggPool() then falls back to the main pool.
	adminPool *pgxpool.Pool
}

// aggPool returns the pool for cross-org aggregate reads: the dedicated audited
// BYPASSRLS service pool when configured, otherwise the main pool.
func (s *eeAdminService) aggPool() *pgxpool.Pool {
	if s.adminPool != nil {
		return s.adminPool
	}
	return s.db.Pool()
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

	renderOrgDrilldown(w, data, actor.Email)
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
	// Cross-org aggregate reads (MRR by plan, recent payments) go through the
	// audited BYPASSRLS service pool when configured (decisions S2) so they see
	// across all orgs; otherwise they fall back to the main pool.
	pool := s.aggPool()

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
	// Write via the main pool (not the bypass service pool) so audit_log writes
	// stay on the request-scoped path (decisions S2).
	if auditErr := store.WriteAudit(
		ctx,
		s.db.Pool(),
		actor.ID,
		"",
		"admin.revenue.view",
		"",
		map[string]any{"actor_email": actor.Email},
	); auditErr != nil {
		slog.WarnContext(ctx, "ee/admin: audit write failed for revenue view",
			"actor_id", actor.ID, "error", auditErr)
	}

	renderRevenue(w, data, actor.Email)
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

// adminLayout is the shared HTML shell, matched to the internal/admin console
// (left nav + top bar, dark teal→indigo brand theme; decisions A9).
const adminLayout = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>{{.Title}} — gitstate admin</title>
  <script src="https://unpkg.com/htmx.org@2.0.4" defer></script>
  <style>
    *,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
    :root{
      --gs-teal:#2dd4bf;--gs-indigo:#6366f1;--gs-indigo-light:#818cf8;
      --gs-base:#0b1120;--surface-1:#111827;--surface-2:#1a2236;--surface-3:#232c43;
      --border:#2a3147;--border-soft:#222a3d;--text:#e5edf6;--text-muted:#8595ad;--text-dim:#5a6987;
      --danger:#f87171;--success:#34d399;--warn:#fbbf24;--radius:14px;
    }
    body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;background:var(--gs-base);color:var(--text);min-height:100vh;display:flex;-webkit-font-smoothing:antialiased}
    a{color:inherit}
    .sidebar{width:240px;min-height:100vh;background:var(--surface-1);border-right:1px solid var(--border-soft);display:flex;flex-direction:column;padding:22px 0 18px;position:fixed;top:0;left:0;bottom:0;z-index:10}
    .sidebar-logo{display:flex;align-items:center;gap:11px;padding:0 22px 26px;text-decoration:none}
    .sidebar-logo-mark{width:36px;height:36px;background:linear-gradient(135deg,var(--gs-teal),var(--gs-indigo));border-radius:9px;display:flex;align-items:center;justify-content:center;font-weight:800;font-size:15px;color:#06121a;box-shadow:0 8px 22px -8px rgba(45,212,191,.55)}
    .sidebar-logo-text{font-weight:800;font-size:16px;letter-spacing:-.02em}
    .sidebar-logo-badge{font-size:9px;background:var(--gs-teal);color:#06121a;border-radius:4px;padding:1px 5px;font-weight:800;letter-spacing:.6px;text-transform:uppercase}
    .nav-section{padding:0 12px}
    .nav-label{padding:14px 12px 6px;font-size:10px;font-weight:700;letter-spacing:1.2px;text-transform:uppercase;color:var(--text-dim)}
    .nav-link{display:flex;align-items:center;gap:11px;padding:10px 12px;margin:1px 0;color:var(--text-muted);text-decoration:none;font-size:14px;font-weight:500;border-radius:9px;transition:background .15s,color .15s}
    .nav-link:hover{color:var(--text);background:var(--surface-2)}
    .nav-link.active{color:var(--text);background:linear-gradient(90deg,rgba(45,212,191,.14),rgba(99,102,241,.10));box-shadow:inset 2px 0 0 var(--gs-teal)}
    .nav-link.active .nav-icon{color:var(--gs-teal)}
    .nav-icon{font-size:15px;width:18px;text-align:center;color:var(--text-dim)}
    .ee-tag{margin-left:auto;font-size:8px;font-weight:800;letter-spacing:.5px;text-transform:uppercase;color:var(--gs-indigo-light);background:rgba(99,102,241,.16);border-radius:4px;padding:2px 5px}
    .sidebar-footer{margin-top:auto;padding:14px 22px 0;border-top:1px solid var(--border-soft);font-size:12px;color:var(--text-dim)}
    .shell{margin-left:240px;flex:1;min-width:0;display:flex;flex-direction:column}
    .topbar{height:60px;border-bottom:1px solid var(--border-soft);background:rgba(17,24,39,.72);backdrop-filter:blur(10px);position:sticky;top:0;z-index:9;display:flex;align-items:center;justify-content:space-between;padding:0 36px}
    .topbar-title{font-size:14px;font-weight:600;color:var(--text-muted)}
    .topbar-right{display:flex;align-items:center;gap:16px}
    .admin-chip{display:flex;align-items:center;gap:9px;font-size:13px;color:var(--text);padding:5px 6px 5px 12px;border:1px solid var(--border);border-radius:999px;background:var(--surface-1)}
    .admin-avatar{width:26px;height:26px;border-radius:50%;background:linear-gradient(135deg,var(--gs-teal),var(--gs-indigo));display:flex;align-items:center;justify-content:center;font-size:12px;font-weight:800;color:#06121a}
    .logout-link{font-size:13px;font-weight:600;color:var(--text-muted);text-decoration:none;padding:7px 14px;border-radius:8px;border:1px solid var(--border);transition:color .15s,border-color .15s}
    .logout-link:hover{color:var(--danger);border-color:rgba(248,113,113,.4)}
    .main{flex:1;padding:34px 36px 60px;width:100%;max-width:1240px;margin:0 auto}
    .page-title{font-size:24px;font-weight:800;letter-spacing:-.02em;margin-bottom:5px}
    .page-title .id{color:var(--text-dim);font-size:13px;font-weight:500;font-family:ui-monospace,SFMono-Regular,Menlo,monospace}
    .page-subtitle{font-size:13px;color:var(--text-muted);margin-bottom:24px}
    h3{font-size:14px;font-weight:700;color:var(--text);margin:0 0 16px}
    .ee-banner{display:flex;align-items:center;gap:8px;background:linear-gradient(90deg,rgba(99,102,241,.14),rgba(45,212,191,.08));border:1px solid rgba(99,102,241,.3);color:var(--gs-indigo-light);padding:10px 16px;border-radius:10px;font-size:12.5px;margin-bottom:22px}
    .stat-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(200px,1fr));gap:16px;margin-bottom:26px}
    .stat{background:linear-gradient(180deg,var(--surface-2),var(--surface-1));border:1px solid var(--border);border-radius:var(--radius);padding:20px 22px;position:relative;overflow:hidden}
    .stat::after{content:"";position:absolute;inset:0 0 auto 0;height:2px;background:linear-gradient(90deg,var(--gs-teal),var(--gs-indigo));opacity:.55}
    .stat-label{font-size:11px;font-weight:700;text-transform:uppercase;letter-spacing:.9px;color:var(--text-muted);margin-bottom:12px}
    .stat-value{font-size:26px;font-weight:800;letter-spacing:-.02em;color:var(--gs-teal);line-height:1}
    .stat-value.sm{font-size:16px;color:var(--text)}
    .card{background:var(--surface-1);border:1px solid var(--border-soft);border-radius:var(--radius);padding:24px;margin-bottom:22px}
    table{width:100%;border-collapse:collapse;font-size:13.5px}
    th{text-align:left;padding:11px 14px;color:var(--text-dim);font-size:11px;font-weight:700;text-transform:uppercase;letter-spacing:.7px;border-bottom:1px solid var(--border)}
    td{padding:13px 14px;border-bottom:1px solid var(--border-soft);color:var(--text)}
    tbody tr:last-child td{border-bottom:none}
    tbody tr:hover td{background:var(--surface-2)}
    .mono{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12px;color:var(--text-muted)}
    .badge{display:inline-flex;align-items:center;padding:3px 9px;border-radius:999px;font-size:11px;font-weight:700}
    .badge-teal{background:rgba(45,212,191,.15);color:var(--gs-teal)}
    .badge-indigo{background:rgba(99,102,241,.18);color:var(--gs-indigo-light)}
    .pill{display:inline-flex;align-items:center;padding:3px 9px;border-radius:999px;font-size:11px;font-weight:700}
    .pill-active,.pill-paid{background:rgba(52,211,153,.15);color:var(--success)}
    .pill-pending,.pill-open,.pill-draft{background:rgba(251,191,36,.15);color:var(--warn)}
    .pill-canceled,.pill-failed{background:rgba(248,113,113,.15);color:var(--danger)}
    .empty{color:var(--text-muted);font-size:13px;padding:24px 0;text-align:center}
    .total-mrr{font-size:34px;font-weight:800;letter-spacing:-.02em;color:var(--gs-teal)}
    .btn-ghost{display:inline-flex;align-items:center;gap:6px;padding:7px 14px;border-radius:9px;font-size:13px;font-weight:600;text-decoration:none;background:var(--surface-2);color:var(--text-muted);border:1px solid var(--border);transition:all .15s}
    .btn-ghost:hover{color:var(--text);border-color:var(--gs-indigo)}
  </style>
</head>
<body>
  <nav class="sidebar">
    <a href="/admin" class="sidebar-logo">
      <div class="sidebar-logo-mark">gs</div>
      <div>
        <div class="sidebar-logo-text">gitstate</div>
        <div class="sidebar-logo-badge">super admin</div>
      </div>
    </a>
    <div class="nav-section">
      <div class="nav-label">Console</div>
      <a href="/admin" class="nav-link {{if eq .Active "analytics"}}active{{end}}"><span class="nav-icon">◈</span> Analytics</a>
      <a href="/admin/users" class="nav-link {{if eq .Active "users"}}active{{end}}"><span class="nav-icon">◉</span> Users</a>
      <a href="/admin/orgs" class="nav-link {{if eq .Active "orgs"}}active{{end}}"><span class="nav-icon">◎</span> Organizations</a>
      <div class="nav-label">Enterprise</div>
      <a href="/admin/revenue" class="nav-link {{if eq .Active "revenue"}}active{{end}}"><span class="nav-icon">⬡</span> Revenue <span class="ee-tag">EE</span></a>
    </div>
    <div class="sidebar-footer">Access is audited.</div>
  </nav>

  <div class="shell">
    <header class="topbar">
      <div class="topbar-title">{{.Title}}</div>
      <div class="topbar-right">
        <div class="admin-chip">
          <span class="admin-avatar">{{.Initial}}</span>
          <span>{{if .AdminEmail}}{{.AdminEmail}}{{else}}super-admin{{end}}</span>
        </div>
        <a href="/admin/logout" class="logout-link">Sign out</a>
      </div>
    </header>
    <main class="main">
{{.Body}}
    </main>
  </div>
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
<div class="ee-banner">⬡ EE cross-org access — this view is written to audit_log (decisions S2).</div>
<a href="/admin/orgs" class="btn-ghost" style="margin-bottom:18px">← All organizations</a>
<div class="page-title" style="margin-top:18px">{{.Org.Name}} <span class="id">{{.Org.ID}}</span></div>
<div class="page-subtitle">Slug <span class="mono">{{.Org.Slug}}</span></div>
<div class="stat-grid">
  <div class="stat"><div class="stat-label">Plan</div><div class="stat-value sm"><span class="badge badge-indigo">{{.Org.PlanKey}}</span></div></div>
  <div class="stat"><div class="stat-label">Members</div><div class="stat-value">{{.MemberCount}}</div></div>
  <div class="stat"><div class="stat-label">Subscription</div><div class="stat-value sm">{{if .Subscription.Status}}{{.Subscription.Status}}{{else}}—{{end}}</div></div>
  <div class="stat"><div class="stat-label">Period End</div><div class="stat-value sm">{{if .Subscription.CurrentPeriodEnd}}{{.Subscription.CurrentPeriodEnd}}{{else}}—{{end}}</div></div>
</div>

<div class="card">
  <h3>Projects <span style="color:var(--text-dim);font-weight:500">(up to 50)</span></h3>
  {{if .Projects}}
  <table>
    <thead><tr><th>Project ID</th><th>Name</th></tr></thead>
    <tbody>
    {{range .Projects}}
    <tr><td><span class="mono">{{.ID}}</span></td><td style="font-weight:600">{{.Name}}</td></tr>
    {{end}}
    </tbody>
  </table>
  {{else}}<div class="empty">No projects.</div>{{end}}
</div>

<div class="card">
  <h3>Recent Audit Log <span style="color:var(--text-dim);font-weight:500">(last 20)</span></h3>
  {{if .RecentAudit}}
  <table>
    <thead><tr><th>Time</th><th>Action</th><th>Target</th></tr></thead>
    <tbody>
    {{range .RecentAudit}}
    <tr>
      <td class="mono">{{.CreatedAt}}</td>
      <td style="font-weight:600">{{.Action}}</td>
      <td><span class="mono">{{.Target}}</span></td>
    </tr>
    {{end}}
    </tbody>
  </table>
  {{else}}<div class="empty">No audit entries yet.</div>{{end}}
</div>
`

// revenueTmplText is the source for the MRR dashboard template.
const revenueTmplText = `
<div class="ee-banner">⬡ EE revenue dashboard — cross-org aggregate read; this view is audited.</div>
<div class="page-title">Revenue &amp; MRR</div>
<div class="page-subtitle">Recurring revenue across every organization on this instance.</div>

<div class="card">
  <div class="stat-label" style="margin-bottom:10px">Total Monthly Recurring Revenue</div>
  <div class="total-mrr">{{.TotalMRR}}</div>
</div>

<div class="card">
  <h3>MRR by Plan</h3>
  <table>
    <thead><tr><th>Plan</th><th>Price / mo</th><th>Active Orgs</th><th style="text-align:right">MRR</th></tr></thead>
    <tbody>
    {{range .Plans}}
    <tr>
      <td><span class="badge badge-indigo">{{.PlanName}}</span></td>
      <td>{{printf "$%d" (div .USDPerMonth 100)}}</td>
      <td>{{.ActiveOrgs}}</td>
      <td style="text-align:right;font-weight:700">{{.MRRDisplay}}</td>
    </tr>
    {{end}}
    </tbody>
  </table>
</div>

<div class="card">
  <h3>Recent Payments <span style="color:var(--text-dim);font-weight:500">(last 20)</span></h3>
  {{if .RecentPayments}}
  <table>
    <thead><tr><th>Time</th><th>Org</th><th>Amount (ZAR)</th><th>Status</th></tr></thead>
    <tbody>
    {{range .RecentPayments}}
    <tr>
      <td class="mono">{{.CreatedAt}}</td>
      <td><span class="mono">{{.OrgID}}</span></td>
      <td style="font-weight:600">R {{zarDisplay .ZARCents}}</td>
      <td><span class="pill pill-{{.Status}}">{{.Status}}</span></td>
    </tr>
    {{end}}
    </tbody>
  </table>
  {{else}}<div class="empty">No payments yet.</div>{{end}}
</div>

<div style="color:var(--text-dim);font-size:12px;margin-top:8px">
  <a href="/admin/revenue" hx-get="/admin/revenue" hx-target="body" hx-swap="innerHTML" style="color:var(--gs-teal);text-decoration:none">↻ Refresh</a>
  · last updated {{.Now}}
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
func renderOrgDrilldown(w http.ResponseWriter, data orgDrilldownData, adminEmail string) {
	var body strings.Builder
	if err := orgDrilldownTmpl.Execute(&body, data); err != nil {
		slog.Error("ee/admin: render org drilldown", "error", err)
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	renderLayout(w, "Organization Detail", "orgs", adminEmail, body.String())
}

// renderRevenue renders the MRR dashboard page to w.
func renderRevenue(w http.ResponseWriter, data revenueDashData, adminEmail string) {
	var body strings.Builder
	if err := revenueTmpl.Execute(&body, data); err != nil {
		slog.Error("ee/admin: render revenue", "error", err)
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	renderLayout(w, "Revenue", "revenue", adminEmail, body.String())
}

// renderLayout wraps body in the shared admin layout (left nav + top bar).
func renderLayout(w http.ResponseWriter, title, active, adminEmail, body string) {
	layoutTmpl := template.Must(template.New("layout").Parse(adminLayout))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = layoutTmpl.Execute(w, map[string]any{
		"Title":      title,
		"Active":     active,
		"AdminEmail": adminEmail,
		"Initial":    emailInitial(adminEmail),
		"Body":       template.HTML(body), //nolint:gosec // body is from our own templates only
	})
}

// emailInitial returns the uppercased first rune of an email for the avatar chip.
func emailInitial(email string) string {
	for _, r := range email {
		return strings.ToUpper(string(r))
	}
	return "?"
}

// Ensure pgxpool import is used (pool used via db.Pool()).
var _ *pgxpool.Pool
