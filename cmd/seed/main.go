// Command seed inserts a coherent demo dataset into gitstate so the UI is
// populated for demos and local development.
//
// Usage:
//
//	go run ./cmd/seed [-reset]
//
// Flags:
//
//	-reset   wipe all demo data (matched by org slug "acme-dev") before re-seeding.
//
// The command is idempotent-ish: without -reset it uses ON CONFLICT DO NOTHING /
// DO UPDATE for most inserts, so re-running is safe. With -reset it deletes the
// demo org (cascade clears everything org-scoped) then re-seeds from scratch.
//
// Requires DATABASE_URL to be set (directly or via config.yaml / .env file).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/exo/gitstate/internal/auth"
	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/store"
)

// ── constants ─────────────────────────────────────────────────────────────────

const (
	demoOrgSlug  = "acme-dev"
	demoOrgName  = "Acme Dev Shop"
	demoPassword = "demo1234"
	demoPlanKey  = "pro"
)

// seed-time reference for building relative timestamps.
var now = time.Now()

func ago(d time.Duration) time.Time { return now.Add(-d) }

// ── entry point ───────────────────────────────────────────────────────────────

func main() {
	reset := flag.Bool("reset", false, "wipe demo org before re-seeding")
	flag.Parse()

	ctx := context.Background()

	cfg, err := config.Load()
	must(err, "load config")

	database, err := db.New(ctx, cfg)
	if err != nil {
		// Provide a clear actionable message rather than a panic.
		fmt.Fprintf(os.Stderr, "seed: cannot connect to database: %v\n", err)
		fmt.Fprintln(os.Stderr, "  → Set DATABASE_URL or add it to config.yaml / .env")
		os.Exit(1)
	}
	defer database.Close()

	if err := database.Ping(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "seed: database ping failed: %v\n", err)
		os.Exit(1)
	}

	// Resolve admin email: SUPER_ADMIN_EMAILS first element, fallback to demo address.
	adminEmail := "demo@gitstate.dev"
	if cfg.Admin.SuperAdminEmails != "" {
		parts := strings.Split(cfg.Admin.SuperAdminEmails, ",")
		if e := strings.TrimSpace(parts[0]); e != "" {
			adminEmail = e
		}
	}

	s := &seeder{db: database, pool: database.Pool(), ctx: ctx}

	if *reset {
		fmt.Println("→ Wiping demo org …")
		s.must(s.wipeDemo())
	}

	fmt.Println("→ Seeding demo data …")

	// ── 1. Users (not org-scoped) ──────────────────────────────────────────
	passwordHash, err := auth.HashPassword(demoPassword)
	must(err, "hash password")

	adminUser := s.upsertUser(adminEmail, "Alex Demo (admin)", passwordHash, true)
	devUser := s.upsertUser("dev@acme.dev", "Jordan Dev", passwordHash, false)
	agentUser := s.upsertUser("agent@acme.dev", "Claude Agent", passwordHash, false)
	stakeholderUser := s.upsertUser("stakeholder@acme.dev", "Sam Stakeholder", passwordHash, false)
	pmUser := s.upsertUser("pm@acme.dev", "Riley PM", passwordHash, false)

	// ── 2. Organization ────────────────────────────────────────────────────
	org := s.upsertOrg(demoOrgSlug, demoOrgName, adminUser.ID)

	// ── 3. Org members (inside db.WithOrg for RLS) ────────────────────────
	s.must(database.WithOrg(ctx, org.ID, func(tx pgx.Tx) error {
		// admin already added as owner by upsertOrg; ensure idempotent
		if err := store.AddMember(ctx, tx, org.ID, adminUser.ID, "owner"); err != nil {
			return err
		}
		if err := store.AddMember(ctx, tx, org.ID, devUser.ID, "member"); err != nil {
			return err
		}
		if err := store.AddMember(ctx, tx, org.ID, agentUser.ID, "member"); err != nil {
			return err
		}
		if err := store.AddMember(ctx, tx, org.ID, pmUser.ID, "admin"); err != nil {
			return err
		}
		// stakeholder = free seat (wedge P6)
		if err := store.AddMember(ctx, tx, org.ID, stakeholderUser.ID, "stakeholder"); err != nil {
			return err
		}
		return nil
	}))

	// ── 4. Subscription on paid plan ──────────────────────────────────────
	periodEnd := now.Add(30 * 24 * time.Hour)
	s.must(database.WithOrg(ctx, org.ID, func(tx pgx.Tx) error {
		return store.UpsertSubscription(ctx, tx, org.ID, demoPlanKey, "active", &periodEnd, "")
	}))

	// ── 5. Repos ──────────────────────────────────────────────────────────
	// ConnectRepo uses pool directly (no org-scoped tx needed per contract).
	// We seed via pool after SET LOCAL in a short tx to satisfy RLS.
	repoGH := s.upsertRepo(org.ID, "github", "acme/frontend", "acme/frontend", "main",
		"https://github.com/acme/frontend.git")
	repoGL := s.upsertRepo(org.ID, "gitlab", "acme/api-service", "acme/api-service", "main",
		"https://gitlab.com/acme/api-service.git")

	// ── 6. Projects ───────────────────────────────────────────────────────
	projWebApp := s.upsertProject(org.ID, "Web App Rewrite", "WEBAPP")
	projAPI := s.upsertProject(org.ID, "API v2", "APIV2")

	// ── 7. Issues ─────────────────────────────────────────────────────────
	// Mix of source='git' (synced from GitHub) and source='native' (manually created).
	ghID := repoGH.ID
	glID := repoGL.ID
	issueAuth := s.upsertIssue(org.ID, projWebApp.ID, &ghID, "git", "github", "101",
		101, "Implement OAuth login flow", "Add Google + Microsoft OAuth",
		"done", "merged", &devUser.ID, []string{"auth", "feature"})
	issueDark := s.upsertIssue(org.ID, projWebApp.ID, &ghID, "git", "github", "102",
		102, "Dark mode support", "System preference detection + toggle",
		"in_progress", "in_progress", &devUser.ID, []string{"ui"})
	issuePerf := s.upsertIssue(org.ID, projAPI.ID, &glID, "git", "gitlab", "201",
		201, "Reduce API latency p99", "Profile slow queries, add indexes",
		"open", "open", &pmUser.ID, []string{"performance", "backend"})
	issueDocs := s.upsertIssue(org.ID, projAPI.ID, nil, "native", "", "",
		0, "Update API documentation", "Swagger + changelog for v2 endpoints",
		"open", "", &adminUser.ID, []string{"docs"})
	issueAgent := s.upsertIssue(org.ID, projWebApp.ID, &ghID, "git", "github", "103",
		103, "Agent: Generate test suite for auth module", "Auto-generated by Claude agent",
		"done", "merged", &agentUser.ID, []string{"testing", "agent"})

	_ = issueDocs // referenced implicitly via issueIDs in time entries below

	// ── 8. Pull requests ──────────────────────────────────────────────────
	prAuth := s.upsertPR(org.ID, repoGH.ID, "github", "pr-101",
		101, "feat(auth): OAuth login flow", devUser.Email, "merged",
		420, 55, 12,
		ago(14*24*time.Hour), ptr(ago(13*24*time.Hour)))
	prDark := s.upsertPR(org.ID, repoGH.ID, "github", "pr-102",
		102, "feat(ui): dark mode toggle", devUser.Email, "open",
		210, 18, 6,
		ago(3*24*time.Hour), nil)
	prAgent := s.upsertPR(org.ID, repoGH.ID, "github", "pr-103",
		103, "test(auth): agent-generated test suite", agentUser.Email, "merged",
		880, 12, 24,
		ago(7*24*time.Hour), ptr(ago(6*24*time.Hour)))
	prPerf := s.upsertPR(org.ID, repoGL.ID, "gitlab", "pr-201",
		201, "perf(api): add composite indexes for slow queries", devUser.Email, "merged",
		95, 10, 3,
		ago(5*24*time.Hour), ptr(ago(4*24*time.Hour)))

	// ── 9. Commits ────────────────────────────────────────────────────────
	s.upsertCommit(org.ID, repoGH.ID, "a1b2c3d4e5f6aa11", devUser.Email, false,
		"feat(auth): scaffold OAuth flow", 320, 10, ago(14*24*time.Hour+2*time.Hour))
	s.upsertCommit(org.ID, repoGH.ID, "b2c3d4e5f6a7bb22", devUser.Email, false,
		"feat(auth): add Google provider callback", 100, 45, ago(14*24*time.Hour))
	s.upsertCommit(org.ID, repoGH.ID, "cc3344aabbdd5566", agentUser.Email, true,
		"test(auth): agent-generated unit tests for oauth handlers", 880, 12, ago(7*24*time.Hour))
	s.upsertCommit(org.ID, repoGH.ID, "dd4455bbccee6677", devUser.Email, false,
		"feat(ui): add dark mode toggle component", 210, 18, ago(3*24*time.Hour))
	s.upsertCommit(org.ID, repoGL.ID, "ee5566ccdeff7788", devUser.Email, false,
		"perf(api): add composite index on events(org_id, occurred_at)", 95, 10, ago(5*24*time.Hour))
	s.upsertCommit(org.ID, repoGL.ID, "ff6677ddeeff8899", agentUser.Email, true,
		"chore(agent): auto-refactor duplicate query helpers", 140, 260, ago(2*24*time.Hour))

	// ── 10. Cycle times (DORA) ────────────────────────────────────────────
	// lead_time_secs = first_commit → merged_at
	s.must(database.WithOrg(ctx, org.ID, func(tx pgx.Tx) error {
		const q = `
			INSERT INTO cycle_times (org_id, pr_id, lead_time_secs, review_secs, computed_at)
			VALUES ($1, $2, $3, $4, now())
			ON CONFLICT DO NOTHING`
		rows := []struct {
			prID        string
			leadTimeSec int64
			reviewSec   int64
		}{
			{prAuth.ID, int64((13 * 24 * time.Hour).Seconds()), int64((4 * time.Hour).Seconds())},
			{prAgent.ID, int64((1 * 24 * time.Hour).Seconds()), int64((30 * time.Minute).Seconds())},
			{prPerf.ID, int64((1 * 24 * time.Hour).Seconds()), int64((2 * time.Hour).Seconds())},
		}
		for _, r := range rows {
			if _, err := tx.Exec(ctx, q, org.ID, r.prID, r.leadTimeSec, r.reviewSec); err != nil {
				return fmt.Errorf("seed: insert cycle_time: %w", err)
			}
		}
		return nil
	}))

	// ── 11. Effort estimates (LLM diff-difficulty) ─────────────────────────
	s.must(database.WithOrg(ctx, org.ID, func(tx pgx.Tx) error {
		const q = `
			INSERT INTO effort_estimates (org_id, pr_id, issue_id, difficulty, rationale, evidence, model)
			VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7)
			ON CONFLICT DO NOTHING`
		estimates := []struct {
			prID       *string
			issueID    *string
			difficulty float64
			rationale  string
			evidence   string
		}{
			{&prAuth.ID, &issueAuth.ID, 7.5,
				"Complex OAuth flow with multiple providers, token rotation, and CSRF protection.",
				`{"files_changed":12,"additions":420,"deletions":55,"pr_number":101}`},
			{&prAgent.ID, &issueAgent.ID, 3.0,
				"Agent-generated test suite; well-structured but straightforward coverage.",
				`{"files_changed":24,"additions":880,"deletions":12,"pr_number":103,"is_agent":true}`},
			{&prPerf.ID, &issuePerf.ID, 4.0,
				"Targeted index additions. Low risk, focused scope.",
				`{"files_changed":3,"additions":95,"deletions":10,"pr_number":201}`},
		}
		for _, e := range estimates {
			if _, err := tx.Exec(ctx, q,
				org.ID, e.prID, e.issueID, e.difficulty, e.rationale, e.evidence, "claude-sonnet-4-6",
			); err != nil {
				return fmt.Errorf("seed: insert effort_estimate: %w", err)
			}
		}
		return nil
	}))

	// ── 12. Involvement texture ────────────────────────────────────────────
	periodStart := now.AddDate(0, -1, 0).Truncate(24 * time.Hour)
	s.must(database.WithOrg(ctx, org.ID, func(tx pgx.Tx) error {
		const q = `
			INSERT INTO involvement
			    (org_id, project_id, user_id, period_start,
			     features_shipped, reviews_done, areas_owned, active, dimensions)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb)
			ON CONFLICT (org_id, project_id, user_id, period_start) DO UPDATE SET
			    features_shipped = EXCLUDED.features_shipped,
			    reviews_done     = EXCLUDED.reviews_done,
			    areas_owned      = EXCLUDED.areas_owned,
			    dimensions       = EXCLUDED.dimensions`
		rows := []struct {
			projectID string
			userID    string
			features  int
			reviews   int
			areas     int
			dims      string
		}{
			{projWebApp.ID, devUser.ID, 2, 3, 2,
				`{"commit_count":4,"pr_count":2,"lines_added":740,"lines_deleted":73}`},
			{projWebApp.ID, agentUser.ID, 1, 0, 1,
				`{"commit_count":2,"pr_count":1,"lines_added":880,"lines_deleted":12,"is_agent":true}`},
			{projAPI.ID, devUser.ID, 1, 1, 1,
				`{"commit_count":1,"pr_count":1,"lines_added":95,"lines_deleted":10}`},
			{projWebApp.ID, pmUser.ID, 0, 4, 0,
				`{"reviews_given":4,"comments_left":12,"issues_triaged":3}`},
		}
		for _, r := range rows {
			if _, err := tx.Exec(ctx, q,
				org.ID, r.projectID, r.userID, periodStart.Format("2006-01-02"),
				r.features, r.reviews, r.areas, true, r.dims,
			); err != nil {
				return fmt.Errorf("seed: insert involvement: %w", err)
			}
		}
		return nil
	}))

	// ── 13. Leave entries ─────────────────────────────────────────────────
	s.must(database.WithOrg(ctx, org.ID, func(tx pgx.Tx) error {
		const q = `
			INSERT INTO leave_entries
			    (org_id, user_id, kind, start_date, end_date, status, note)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT DO NOTHING`
		// Jordan takes PTO next week; Sam already has a holiday this month.
		entries := []struct {
			userID string
			kind   string
			start  string
			end    string
			status string
			note   string
		}{
			{devUser.ID, "pto",
				now.AddDate(0, 0, 7).Format("2006-01-02"),
				now.AddDate(0, 0, 11).Format("2006-01-02"),
				"approved", "Annual leave"},
			{stakeholderUser.ID, "holiday",
				now.AddDate(0, 0, -5).Format("2006-01-02"),
				now.AddDate(0, 0, -5).Format("2006-01-02"),
				"approved", "Public holiday"},
			{pmUser.ID, "sick",
				now.AddDate(0, 0, -2).Format("2006-01-02"),
				now.AddDate(0, 0, -2).Format("2006-01-02"),
				"approved", ""},
		}
		for _, e := range entries {
			if _, err := tx.Exec(ctx, q,
				org.ID, e.userID, e.kind, e.start, e.end, e.status, e.note,
			); err != nil {
				return fmt.Errorf("seed: insert leave_entry: %w", err)
			}
		}
		return nil
	}))

	// ── 14. Availability ──────────────────────────────────────────────────
	s.must(database.WithOrg(ctx, org.ID, func(tx pgx.Tx) error {
		const q = `
			INSERT INTO availability
			    (org_id, user_id, weekly_hours, working_days, effective_from)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT DO NOTHING`
		// Mon–Fri (ISO 1–5) for everyone; agent runs 24/7 nominally at 40h
		std := "{1,2,3,4,5}"
		epoch := "2026-01-01"
		avails := []struct {
			userID string
			hours  float64
		}{
			{adminUser.ID, 40},
			{devUser.ID, 40},
			{agentUser.ID, 40},
			{pmUser.ID, 32}, // part-time PM
			{stakeholderUser.ID, 0}, // stakeholder: read-only, no capacity tracked
		}
		for _, a := range avails {
			if a.hours == 0 {
				continue // skip stakeholder — no capacity row needed
			}
			if _, err := tx.Exec(ctx, q,
				org.ID, a.userID, a.hours, std, epoch,
			); err != nil {
				return fmt.Errorf("seed: insert availability: %w", err)
			}
		}
		return nil
	}))

	// ── 15. Time entries ──────────────────────────────────────────────────
	s.must(database.WithOrg(ctx, org.ID, func(tx pgx.Tx) error {
		const q = `
			INSERT INTO time_entries
			    (org_id, user_id, issue_id, source, minutes, occurred_on, note)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT DO NOTHING`
		today := now.Format("2006-01-02")
		yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
		entries := []struct {
			userID  string
			issueID *string
			source  string
			minutes int
			day     string
			note    string
		}{
			{devUser.ID, &issueAuth.ID, "git", 180, yesterday, "OAuth implementation session"},
			{devUser.ID, &issueDark.ID, "manual", 120, today, "Dark mode styling pass"},
			{agentUser.ID, &issueAgent.ID, "git", 60, yesterday, "Agent test generation run"},
			{pmUser.ID, nil, "manual", 90, today, "Sprint planning + stakeholder review"},
			{devUser.ID, &issuePerf.ID, "git", 150, yesterday, "Index profiling + deployment"},
		}
		for _, e := range entries {
			if _, err := tx.Exec(ctx, q,
				org.ID, e.userID, e.issueID, e.source, e.minutes, e.day, e.note,
			); err != nil {
				return fmt.Errorf("seed: insert time_entry: %w", err)
			}
		}
		return nil
	}))

	// ── Summary ───────────────────────────────────────────────────────────
	fmt.Printf(`
╔══════════════════════════════════════════════════════╗
║             gitstate demo seed — complete            ║
╠══════════════════════════════════════════════════════╣
║  Org:          %s (%s)
║  Plan:         Pro ($39/mo) — active subscription
║
║  Users created / ensured:
║    %-40s  owner / super-admin
║    %-40s  member (dev)
║    %-40s  member (agent)
║    %-40s  admin  (PM)
║    %-40s  stakeholder (free seat)
║  Password for all:  %s
║
║  Repos:   2  (1 GitHub · 1 GitLab)
║  Projects: 2  (%s · %s)
║  Issues:  5  (3 git-synced · 2 native)
║  PRs:     4  (3 merged · 1 open)
║  Commits: 6  (2 agent · 4 human)
║  Cycle times: 3  effort estimates: 3
║  Leave entries: 3  time entries: 5
║
║  → Open http://localhost:8080/login
║    Email:    %s
║    Password: %s
╚══════════════════════════════════════════════════════╝
`,
		org.Name, org.Slug,
		adminEmail, devUser.Email, agentUser.Email, pmUser.Email, stakeholderUser.Email,
		demoPassword,
		projWebApp.Name, projAPI.Name,
		adminEmail, demoPassword,
	)

	// Also echo the PR pair IDs that don't get printed above (keep compiler happy).
	_ = prDark
	_ = projWebApp
	_ = projAPI
}

// ── helpers ───────────────────────────────────────────────────────────────────

// seeder holds shared context so individual upsert methods stay concise.
type seeder struct {
	db   *db.DB
	pool *pgxpool.Pool
	ctx  context.Context
}

func (s *seeder) must(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "seed error: %v\n", err)
		os.Exit(1)
	}
}

// wipeDemo deletes the demo org (CASCADE removes everything org-scoped).
func (s *seeder) wipeDemo() error {
	_, err := s.pool.Exec(s.ctx,
		`DELETE FROM organizations WHERE slug = $1`, demoOrgSlug)
	return err
}

// upsertUser ensures a user row exists. On conflict it updates the password
// hash and super-admin flag so re-runs stay coherent.
func (s *seeder) upsertUser(email, name, passwordHash string, isSuperAdmin bool) *store.User {
	const q = `
		INSERT INTO users (email, name, password_hash, is_super_admin)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (email) DO UPDATE SET
			name          = EXCLUDED.name,
			password_hash = EXCLUDED.password_hash,
			is_super_admin = EXCLUDED.is_super_admin
		RETURNING id, email, name, COALESCE(avatar_url,''), COALESCE(password_hash,''),
		          is_super_admin, created_at, updated_at`

	var u store.User
	err := s.pool.QueryRow(s.ctx, q, email, name, passwordHash, isSuperAdmin).Scan(
		&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.PasswordHash,
		&u.IsSuperAdmin, &u.CreatedAt, &u.UpdatedAt,
	)
	s.must(wrapErr("upsert user "+email, err))
	return &u
}

// upsertOrg ensures the demo org exists and that ownerUserID is an owner.
func (s *seeder) upsertOrg(slug, name, ownerUserID string) *store.Org {
	// Try insert; on conflict just read the existing row.
	const q = `
		INSERT INTO organizations (slug, name, plan_key)
		VALUES ($1, $2, $3)
		ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name
		RETURNING id, slug, name, plan_key, created_at, updated_at`

	var o store.Org
	err := s.pool.QueryRow(s.ctx, q, slug, name, demoPlanKey).Scan(
		&o.ID, &o.Slug, &o.Name, &o.PlanKey, &o.CreatedAt, &o.UpdatedAt,
	)
	s.must(wrapErr("upsert org", err))

	// Ensure the owner membership exists (not org-scoped, so raw pool is fine).
	_, err = s.pool.Exec(s.ctx,
		`INSERT INTO org_members (org_id, user_id, role)
		 VALUES ($1, $2, 'owner')
		 ON CONFLICT (org_id, user_id) DO UPDATE SET role = 'owner'`,
		o.ID, ownerUserID)
	s.must(wrapErr("upsert org owner", err))

	return &o
}

// upsertRepo connects a repo for the org via the raw pool (ConnectRepo pattern).
func (s *seeder) upsertRepo(orgID, platform, externalID, fullName, branch, cloneURL string) *store.Repo {
	const q = `
		INSERT INTO repos (org_id, platform, external_id, full_name, default_branch, clone_url, last_synced_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (org_id, platform, external_id) DO UPDATE SET
			full_name      = EXCLUDED.full_name,
			default_branch = EXCLUDED.default_branch,
			clone_url      = EXCLUDED.clone_url,
			last_synced_at = now()
		RETURNING id, org_id, platform, external_id, full_name,
		          COALESCE(default_branch,''), COALESCE(clone_url,''), created_at`

	// repos has RLS — we need a short org-scoped tx.
	var r store.Repo
	err := s.db.WithOrg(s.ctx, orgID, func(tx pgx.Tx) error {
		return tx.QueryRow(s.ctx, q,
			orgID, platform, externalID, fullName, branch, cloneURL,
		).Scan(
			&r.ID, &r.OrgID, &r.Platform, &r.ExternalID, &r.FullName,
			&r.DefaultBranch, &r.CloneURL, &r.CreatedAt,
		)
	})
	s.must(wrapErr("upsert repo "+fullName, err))
	return &r
}

// projectRow is the minimal set we need from the projects table.
type projectRow struct {
	ID   string
	Name string
	Key  string
}

// upsertProject ensures a project exists in the org.
func (s *seeder) upsertProject(orgID, name, key string) *projectRow {
	const q = `
		INSERT INTO projects (org_id, name, key)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
		RETURNING id, name, COALESCE(key,'')`

	var p projectRow

	err := s.db.WithOrg(s.ctx, orgID, func(tx pgx.Tx) error {
		scanErr := tx.QueryRow(s.ctx, q, orgID, name, key).Scan(&p.ID, &p.Name, &p.Key)
		if scanErr == nil {
			return nil // inserted, got the row back
		}
		// ON CONFLICT DO NOTHING returns no row — fetch instead.
		const fetch = `SELECT id, name, COALESCE(key,'') FROM projects WHERE org_id=$1 AND name=$2`
		return tx.QueryRow(s.ctx, fetch, orgID, name).Scan(&p.ID, &p.Name, &p.Key)
	})
	s.must(wrapErr("upsert project "+name, err))
	return &p
}

// issueRow holds the minimal fields we need from issues.
type issueRow struct {
	ID string
}

// upsertIssue inserts or updates an issue. repoID and assigneeID may be nil.
func (s *seeder) upsertIssue(
	orgID string, projectID string, repoID *string,
	source, platform, externalID string, number int,
	title, body, state, derivedState string,
	assigneeID *string, labels []string,
) *issueRow {
	var row issueRow
	err := s.db.WithOrg(s.ctx, orgID, func(tx pgx.Tx) error {
		// For native issues (no platform/external_id), try a title-match fetch first
		// to avoid duplicates on re-run, then insert.
		if source == "native" {
			const fetch = `SELECT id FROM issues WHERE org_id=$1 AND title=$2 AND source='native' LIMIT 1`
			scanErr := tx.QueryRow(s.ctx, fetch, orgID, title).Scan(&row.ID)
			if scanErr == nil {
				return nil // already exists
			}
		}

		const q = `
			INSERT INTO issues
			    (org_id, project_id, repo_id, source, platform, external_id, number,
			     title, body, state, derived_state, assignee_id, labels)
			VALUES ($1, $2, $3, $4,
			        NULLIF($5,''), NULLIF($6,''), NULLIF($7::int,0),
			        $8, $9, $10, NULLIF($11,''), $12, $13)
			ON CONFLICT (org_id, platform, external_id) WHERE platform IS NOT NULL
			DO UPDATE SET
			    title         = EXCLUDED.title,
			    state         = EXCLUDED.state,
			    derived_state = EXCLUDED.derived_state
			RETURNING id`

		return tx.QueryRow(s.ctx, q,
			orgID, projectID, repoID,
			source, platform, externalID, number,
			title, body, state, derivedState,
			assigneeID, labels,
		).Scan(&row.ID)
	})
	s.must(wrapErr("upsert issue "+title, err))
	return &row
}

// prRow holds the minimal fields we need from pull_requests.
type prRow struct {
	ID string
}

// upsertPR inserts or updates a pull request. mergedAt may be nil for open PRs.
func (s *seeder) upsertPR(
	orgID, repoID, platform, externalID string,
	number int, title, authorLogin, state string,
	additions, deletions, changedFiles int,
	firstCommitAt time.Time, mergedAt *time.Time,
) *prRow {
	const q = `
		INSERT INTO pull_requests
		    (org_id, repo_id, platform, external_id, number, title, author_login,
		     state, additions, deletions, changed_files, first_commit_at, merged_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (org_id, repo_id, external_id) DO UPDATE SET
		    title       = EXCLUDED.title,
		    state       = EXCLUDED.state,
		    merged_at   = EXCLUDED.merged_at
		RETURNING id`

	var row prRow
	err := s.db.WithOrg(s.ctx, orgID, func(tx pgx.Tx) error {
		return tx.QueryRow(s.ctx, q,
			orgID, repoID, platform, externalID, number, title, authorLogin,
			state, additions, deletions, changedFiles, firstCommitAt, mergedAt,
		).Scan(&row.ID)
	})
	s.must(wrapErr("upsert PR #"+fmt.Sprint(number), err))
	return &row
}

// upsertCommit inserts a commit if not already present.
func (s *seeder) upsertCommit(
	orgID, repoID, sha, authorEmail string, isAgent bool,
	message string, additions, deletions int, committedAt time.Time,
) {
	const q = `
		INSERT INTO commits
		    (org_id, repo_id, sha, author_login, author_email, is_agent,
		     message, additions, deletions, committed_at)
		VALUES ($1, $2, $3,
		        SPLIT_PART($4, '@', 1), $4, $5,
		        $6, $7, $8, $9)
		ON CONFLICT (org_id, repo_id, sha) DO NOTHING`

	err := s.db.WithOrg(s.ctx, orgID, func(tx pgx.Tx) error {
		_, execErr := tx.Exec(s.ctx, q,
			orgID, repoID, sha, authorEmail, isAgent,
			message, additions, deletions, committedAt,
		)
		return execErr
	})
	s.must(wrapErr("upsert commit "+sha[:8], err))
}

// ── util ──────────────────────────────────────────────────────────────────────

func must(err error, label string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "seed: %s: %v\n", label, err)
		os.Exit(1)
	}
}

func wrapErr(label string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("seed: %s: %w", label, err)
}

func ptr[T any](v T) *T { return &v }
