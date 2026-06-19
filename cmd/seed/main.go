// Command seed inserts a coherent, *rich* demo dataset into gitstate so the UI
// and analytics dashboards look like a real, active engineering org rather than
// a toy fixture.
//
// Usage:
//
//	go run ./cmd/seed [-reset]
//
// Flags:
//
//	-reset   wipe all demo data (matched by org slug "acme-dev") before re-seeding.
//
// Scale (deterministic seed → reproducible):
//
//	~12 members · 4 repos (github + gitlab) · 5 projects ·
//	~3,000 commits over the last ~9 months with weekday-heavy daily clustering
//	(heatmap-worthy) · ~200 pull requests with varied lead times ·
//	~120 issues (git + native) · involvement / cycle_times / effort_estimates /
//	leave / availability / time_entries · an active `team` subscription.
//
// The command is idempotent: without -reset it relies on ON CONFLICT DO NOTHING /
// DO UPDATE for every insert (commits/PRs/issues keyed on deterministic external
// ids), so re-running is safe. With -reset it deletes the demo org (cascade
// clears everything org-scoped) then re-seeds from scratch.
//
// Every org-scoped insert runs inside db.WithOrg(ctx, orgID, …) so RLS applies.
//
// Requires DATABASE_URL to be set (directly or via config.yaml / .env file).
package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"math/rand"
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
	demoPlanKey  = "team"

	// historyDays is how far back synthetic git history reaches (~9 months).
	historyDays = 274

	// seedRNG makes the whole dataset reproducible across runs.
	seedRNG = 0x6175746f70696c // "autopil"
)

// seed-time reference for building relative timestamps.
var now = time.Now()

func ago(d time.Duration) time.Time { return now.Add(-d) }

// rng is the deterministic source of all randomness in this seeder.
var rng = rand.New(rand.NewSource(seedRNG))

// ── people ────────────────────────────────────────────────────────────────────

// member describes a synthetic org member and their behavioural profile.
type member struct {
	email   string
	name    string
	login   string  // git author_login (split-part of email by default)
	role    string  // owner | admin | member | stakeholder | agent
	isAgent bool    // contributes is_agent=true commits
	weight  float64 // relative share of commit/PR volume (0 ⇒ no git output)
	user    *store.User
}

// members defines ~12 people: a few heavy builders, a long tail, two PMs/stake-
// holders, and two bot-ish agent identities. weight drives commit distribution.
var members = []*member{
	{email: "demo@gitstate.dev", name: "Alex Rivera", login: "arivera", role: "owner", weight: 1.6},
	{email: "priya.nair@acme.dev", name: "Priya Nair", login: "pnair", role: "admin", weight: 1.9},
	{email: "marcus.lee@acme.dev", name: "Marcus Lee", login: "mlee", role: "member", weight: 2.2},
	{email: "sofia.gomez@acme.dev", name: "Sofia Gómez", login: "sgomez", role: "member", weight: 1.4},
	{email: "tom.fischer@acme.dev", name: "Tom Fischer", login: "tfischer", role: "member", weight: 1.1},
	{email: "aisha.khan@acme.dev", name: "Aisha Khan", login: "akhan", role: "member", weight: 1.3},
	{email: "diego.santos@acme.dev", name: "Diego Santos", login: "dsantos", role: "member", weight: 0.7},
	{email: "yuki.tanaka@acme.dev", name: "Yuki Tanaka", login: "ytanaka", role: "member", weight: 0.5},
	{email: "noah.brooks@acme.dev", name: "Noah Brooks", login: "nbrooks", role: "member", weight: 0.35},
	{email: "riley.pm@acme.dev", name: "Riley Okonkwo", login: "rokonkwo", role: "admin", weight: 0.15},
	{email: "sam.stake@acme.dev", name: "Sam Whitfield", login: "swhitfield", role: "stakeholder", weight: 0},
	{email: "claude-bot@acme.dev", name: "Claude Agent", login: "claude-bot", role: "agent", isAgent: true, weight: 1.2},
	{email: "dependabot@acme.dev", name: "Acme Build Bot", login: "acme-bot", role: "agent", isAgent: true, weight: 0.6},
}

// ── entry point ───────────────────────────────────────────────────────────────

func main() {
	reset := flag.Bool("reset", false, "wipe demo org before re-seeding")
	flag.Parse()

	ctx := context.Background()

	cfg, err := config.Load()
	must(err, "load config")

	database, err := db.New(ctx, cfg)
	if err != nil {
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
	adminEmail := members[0].email
	if cfg.Admin.SuperAdminEmails != "" {
		parts := strings.Split(cfg.Admin.SuperAdminEmails, ",")
		if e := strings.TrimSpace(parts[0]); e != "" {
			adminEmail = e
		}
	}
	members[0].email = adminEmail
	members[0].login = loginFromEmail(adminEmail)

	s := &seeder{db: database, pool: database.Pool(), ctx: ctx}

	if *reset {
		fmt.Println("→ Wiping demo org …")
		s.must(s.wipeDemo())
	}

	fmt.Println("→ Seeding rich demo data …")

	// ── 1. Users (not org-scoped) ──────────────────────────────────────────
	passwordHash, err := auth.HashPassword(demoPassword)
	must(err, "hash password")

	for _, m := range members {
		m.user = s.upsertUser(m.email, m.name, passwordHash, m.email == adminEmail)
	}

	// ── 2. Organization ────────────────────────────────────────────────────
	org := s.upsertOrg(demoOrgSlug, demoOrgName, members[0].user.ID)

	// ── 3. Org members (inside db.WithOrg for RLS) ────────────────────────
	s.must(database.WithOrg(ctx, org.ID, func(tx pgx.Tx) error {
		for _, m := range members {
			if err := store.AddMember(ctx, tx, org.ID, m.user.ID, m.role); err != nil {
				return err
			}
		}
		return nil
	}))

	// ── 4. Subscription on paid plan ──────────────────────────────────────
	periodEnd := now.Add(30 * 24 * time.Hour)
	s.must(database.WithOrg(ctx, org.ID, func(tx pgx.Tx) error {
		return store.UpsertSubscription(ctx, tx, org.ID, demoPlanKey, "active", &periodEnd, "")
	}))

	// ── 5. Repos (mix of github + gitlab) ─────────────────────────────────
	repos := []*store.Repo{
		s.upsertRepo(org.ID, "github", "acme/frontend", "acme/frontend", "main",
			"https://github.com/acme/frontend.git"),
		s.upsertRepo(org.ID, "github", "acme/platform", "acme/platform", "main",
			"https://github.com/acme/platform.git"),
		s.upsertRepo(org.ID, "gitlab", "acme/api-service", "acme/api-service", "main",
			"https://gitlab.com/acme/api-service.git"),
		s.upsertRepo(org.ID, "gitlab", "acme/data-pipeline", "acme/data-pipeline", "main",
			"https://gitlab.com/acme/data-pipeline.git"),
	}

	// ── 6. Projects ───────────────────────────────────────────────────────
	projects := []*projectRow{
		s.upsertProject(org.ID, "Web App Rewrite", "WEBAPP"),
		s.upsertProject(org.ID, "Platform Core", "CORE"),
		s.upsertProject(org.ID, "API v2", "APIV2"),
		s.upsertProject(org.ID, "Data Pipeline", "DATA"),
		s.upsertProject(org.ID, "Growth & Analytics", "GROWTH"),
	}

	// ── 7. Commits (the heatmap fuel) ─────────────────────────────────────
	commitStats := s.seedCommits(org.ID, repos)

	// ── 8. Pull requests + cycle times + effort estimates ─────────────────
	prStats := s.seedPullRequests(org.ID, repos, projects)

	// ── 9. Issues (git-synced + native) for the Kanban board ──────────────
	issueIDs, issueStats := s.seedIssues(org.ID, repos, projects)

	// ── 10. Involvement texture (per member/project, monthly) ─────────────
	s.seedInvolvement(org.ID, projects, commitStats)

	// ── 11. Capacity: availability · leave types · leave · balances · time ─
	s.seedAvailability(org.ID)
	leaveTypes := s.seedLeaveTypes(org.ID)
	leaveCount := s.seedLeave(org.ID, leaveTypes)
	s.seedLeaveBalances(org.ID, leaveTypes)
	timeCount := s.seedTimeEntries(org.ID, issueIDs)

	// ── Summary ───────────────────────────────────────────────────────────
	s.printSummary(org, adminEmail, len(repos), len(projects),
		commitStats, prStats, issueStats, leaveCount, timeCount)
}

// ── commit generation ───────────────────────────────────────────────────────

type commitTally struct {
	total      int
	agent      int
	human      int
	perUser    map[string]int // userID → commit count
	perUserAdd map[string]int // userID → lines added
	perUserDel map[string]int // userID → lines deleted
}

// commitVerbs / scopes / subjects compose plausible conventional-commit messages.
var (
	commitTypes  = []string{"feat", "fix", "refactor", "chore", "test", "docs", "perf", "style", "build", "ci"}
	commitScopes = []string{
		"auth", "api", "ui", "db", "billing", "search", "cache", "metrics",
		"router", "config", "worker", "pipeline", "schema", "deps", "ci",
		"dashboard", "export", "webhook", "rls", "session", "queue", "report",
	}
	commitSubjects = []string{
		"handle nil pointer on empty result set",
		"add pagination to the results endpoint",
		"wire up the new settings panel",
		"reduce p99 latency on the hot path",
		"extract shared validation helper",
		"upgrade to the latest pinned dependencies",
		"add integration tests for the sync flow",
		"document the public API surface",
		"cache the expensive aggregate query",
		"fix flaky test in the worker suite",
		"introduce feature flag for the rollout",
		"correct timezone handling on reports",
		"add composite index for slow lookups",
		"tighten input validation on the form",
		"refactor the retry/backoff logic",
		"support dark mode preference",
		"stream large exports instead of buffering",
		"add structured logging to the handler",
		"deduplicate webhook deliveries",
		"backfill missing created_at timestamps",
		"split the monolithic service module",
		"add rate limiting to the public routes",
		"migrate to the new connection pool",
		"fix off-by-one in the pagination cursor",
		"add metrics for queue depth",
		"improve error messages on 4xx responses",
		"guard against duplicate submissions",
		"add a health check for the dependency",
		"normalize email casing on signup",
		"prefetch related rows to avoid N+1",
	}
	agentSubjects = []string{
		"auto-generate test suite for the changed module",
		"apply automated lint fixes across the package",
		"bump transitive dependencies to patched versions",
		"regenerate API client from the updated schema",
		"auto-refactor duplicated query helpers",
		"format codebase with the new style config",
		"sweep unused imports and dead code",
		"update generated mocks after interface change",
	}
)

func (s *seeder) commitMessage(m *member) (string, int, int) {
	typ := commitTypes[rng.Intn(len(commitTypes))]
	scope := commitScopes[rng.Intn(len(commitScopes))]
	var subj string
	if m.isAgent {
		subj = agentSubjects[rng.Intn(len(agentSubjects))]
	} else {
		subj = commitSubjects[rng.Intn(len(commitSubjects))]
	}
	msg := fmt.Sprintf("%s(%s): %s", typ, scope, subj)

	// Size distribution: mostly small, occasionally large; agents skew bigger
	// on additions (generated code) and bigger on deletions for refactors.
	add := 5 + rng.Intn(60)
	del := rng.Intn(25)
	switch {
	case rng.Float64() < 0.08: // big change
		add = 200 + rng.Intn(900)
		del = rng.Intn(400)
	case rng.Float64() < 0.20: // medium
		add = 60 + rng.Intn(180)
		del = rng.Intn(120)
	}
	if m.isAgent {
		add = int(float64(add) * (1.3 + rng.Float64()))
		if typ == "refactor" || typ == "chore" {
			del = int(float64(del) * (1.5 + rng.Float64()))
		}
	}
	return msg, add, del
}

// dayIntensity returns a 0..1 activity multiplier for a given calendar day so
// the contribution heatmap looks organic: weekday-heavy, weekends sparse,
// occasional dead days and occasional bursts, plus a slow ramp over the period.
func dayIntensity(day time.Time, idx, totalDays int) float64 {
	wd := day.Weekday()
	base := 1.0
	switch wd {
	case time.Saturday:
		base = 0.18
	case time.Sunday:
		base = 0.12
	case time.Monday:
		base = 0.85
	case time.Friday:
		base = 0.8
	}
	// Slow ramp: the org gets busier over time (0.55 → 1.15).
	ramp := 0.55 + 0.6*float64(idx)/float64(totalDays)

	// Deterministic per-day jitter.
	j := 0.4 + rng.Float64()*1.2

	// Sprinkle some near-dead days (vacations / quiet weeks).
	if rng.Float64() < 0.07 {
		j *= 0.05
	}
	// And occasional crunch-day bursts.
	if rng.Float64() < 0.05 {
		j *= 2.6
	}
	return base * ramp * j
}

// seedCommits generates ~3k commits across repos/members over historyDays with
// realistic per-day clustering. Inserted in batched pgx.Batch chunks for speed.
func (s *seeder) seedCommits(orgID string, repos []*store.Repo) commitTally {
	tally := commitTally{
		perUser:    map[string]int{},
		perUserAdd: map[string]int{},
		perUserDel: map[string]int{},
	}

	// Build a weighted picker over members that actually commit.
	var contributors []*member
	var weights []float64
	var wsum float64
	for _, m := range members {
		if m.weight <= 0 {
			continue
		}
		contributors = append(contributors, m)
		wsum += m.weight
		weights = append(weights, wsum)
	}
	pickMember := func() *member {
		r := rng.Float64() * wsum
		for i, w := range weights {
			if r <= w {
				return contributors[i]
			}
		}
		return contributors[len(contributors)-1]
	}

	// Each member tends to work in particular repos; precompute a primary repo.
	primaryRepo := map[string]int{}
	for _, m := range contributors {
		primaryRepo[m.user.ID] = rng.Intn(len(repos))
	}

	type pending struct {
		repoID, sha, login, email, msg string
		isAgent                        bool
		add, del                       int
		at                             time.Time
	}
	var buf []pending

	flush := func() {
		if len(buf) == 0 {
			return
		}
		s.must(s.db.WithOrg(s.ctx, orgID, func(tx pgx.Tx) error {
			batch := &pgx.Batch{}
			const q = `
				INSERT INTO commits
				    (org_id, repo_id, sha, author_login, author_email, is_agent,
				     message, additions, deletions, committed_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
				ON CONFLICT (org_id, repo_id, sha) DO NOTHING`
			for _, c := range buf {
				batch.Queue(q, orgID, c.repoID, c.sha, c.login, c.email,
					c.isAgent, c.msg, c.add, c.del, c.at)
			}
			br := tx.SendBatch(s.ctx, batch)
			defer br.Close()
			for range buf {
				if _, err := br.Exec(); err != nil {
					return fmt.Errorf("seed: batch insert commit: %w", err)
				}
			}
			return nil
		}))
		buf = buf[:0]
	}

	seq := 0
	start := now.AddDate(0, 0, -historyDays)
	for d := 0; d < historyDays; d++ {
		day := start.AddDate(0, 0, d)
		intensity := dayIntensity(day, d, historyDays)
		// Target ~11 commits/active-weekday at peak ⇒ 0..~20 per day.
		count := int(math.Round(intensity * 11))
		if count < 0 {
			count = 0
		}
		for c := 0; c < count; c++ {
			m := pickMember()

			// Most commits land in the member's primary repo; some spill over.
			ri := primaryRepo[m.user.ID]
			if rng.Float64() < 0.25 {
				ri = rng.Intn(len(repos))
			}
			repo := repos[ri]

			// committed_at: spread across working hours with some late-night.
			hour := 8 + rng.Intn(11) // 08:00–18:59 mostly
			if rng.Float64() < 0.12 {
				hour = rng.Intn(24)
			}
			at := time.Date(day.Year(), day.Month(), day.Day(),
				hour, rng.Intn(60), rng.Intn(60), 0, day.Location())

			msg, add, del := s.commitMessage(m)
			isAgent := m.isAgent
			// A small share of human commits are co-authored/automated agent runs.
			if !isAgent && rng.Float64() < 0.04 {
				isAgent = true
			}

			seq++
			sha := fmt.Sprintf("%040x", rng.Int63()^int64(seq)<<20^int64(d))[:40]

			buf = append(buf, pending{
				repoID:  repo.ID,
				sha:     sha,
				login:   m.login,
				email:   m.email,
				isAgent: isAgent,
				msg:     msg,
				add:     add,
				del:     del,
				at:      at,
			})

			tally.total++
			if isAgent {
				tally.agent++
			} else {
				tally.human++
			}
			tally.perUser[m.user.ID]++
			tally.perUserAdd[m.user.ID] += add
			tally.perUserDel[m.user.ID] += del

			if len(buf) >= 500 {
				flush()
			}
		}
	}
	flush()
	return tally
}

// ── pull requests ───────────────────────────────────────────────────────────

type prTally struct {
	total, merged, open, closed int
	cycleTimes                  int
	estimates                   int
}

var prTitles = []string{
	"feat(auth): OAuth login flow with provider fallback",
	"fix(api): correct cursor pagination on large datasets",
	"refactor(billing): extract invoice line builder",
	"perf(search): add composite index for filtered queries",
	"feat(ui): dark mode + system preference detection",
	"chore(deps): bump runtime and test dependencies",
	"feat(export): streaming CSV export for large reports",
	"fix(webhook): deduplicate deliveries via idempotency key",
	"feat(dashboard): contribution heatmap widget",
	"refactor(worker): retry/backoff with jitter",
	"feat(rls): per-org row-level isolation policies",
	"perf(db): replace N+1 with batched prefetch",
	"fix(session): rotate refresh tokens on reuse detection",
	"feat(metrics): DORA cycle-time computation",
	"chore(ci): cache build artifacts between stages",
	"feat(report): NL→SQL query box with safety layers",
	"fix(pipeline): handle late-arriving events gracefully",
	"feat(queue): backpressure on the ingest worker",
	"refactor(config): derive OAuth enabled from credentials",
	"feat(billing): per-builder seat pricing model",
}

// seedPullRequests creates ~200 PRs with varied lead times → feeds cycle-time
// charts. Most are merged; a fraction stay open; a few are closed-unmerged.
func (s *seeder) seedPullRequests(orgID string, repos []*store.Repo, projects []*projectRow) prTally {
	var tally prTally

	// Contributors eligible to author PRs (everyone with weight, plus PMs).
	var authors []*member
	for _, m := range members {
		if m.weight > 0 || m.role == "admin" {
			authors = append(authors, m)
		}
	}

	const targetPRs = 210

	type prGen struct {
		repoID, platform, extID, title, login, state string
		number                                       int
		add, del, files                              int
		firstCommit                                  time.Time
		merged                                       *time.Time
		leadSecs, reviewSecs                         int64
	}
	var gens []prGen

	for i := 0; i < targetPRs; i++ {
		repo := repos[rng.Intn(len(repos))]
		author := authors[rng.Intn(len(authors))]

		// Spread first_commit_at across the whole history window.
		daysBack := rng.Intn(historyDays - 1)
		firstCommit := now.AddDate(0, 0, -daysBack).
			Add(time.Duration(rng.Intn(10)) * time.Hour)

		add := 20 + rng.Intn(600)
		del := rng.Intn(300)
		if rng.Float64() < 0.1 {
			add += 400 + rng.Intn(1500) // occasional large PR
		}
		files := 1 + rng.Intn(18)

		// State distribution: ~80% merged, ~13% open, ~7% closed.
		var state string
		var merged *time.Time
		var leadSecs, reviewSecs int64
		roll := rng.Float64()
		switch {
		case roll < 0.80 && daysBack > 1:
			state = "merged"
			tally.merged++
			// Lead time: bimodal — fast PRs (hours→2d) and slow ones (up to ~3w).
			var lead time.Duration
			if rng.Float64() < 0.7 {
				lead = time.Duration(2+rng.Intn(46)) * time.Hour // 2h–2d
			} else {
				lead = time.Duration(2+rng.Intn(19)) * 24 * time.Hour // 2d–3w
			}
			// Never merge in the future.
			mt := firstCommit.Add(lead)
			if mt.After(now) {
				mt = now.Add(-time.Duration(rng.Intn(6)) * time.Hour)
			}
			merged = &mt
			leadSecs = int64(mt.Sub(firstCommit).Seconds())
			if leadSecs < 0 {
				leadSecs = 3600
			}
			reviewSecs = int64(float64(leadSecs) * (0.1 + 0.3*rng.Float64()))
		case roll < 0.93:
			state = "open"
			tally.open++
		default:
			state = "closed"
			tally.closed++
		}

		gens = append(gens, prGen{
			repoID:      repo.ID,
			platform:    repo.Platform,
			extID:       fmt.Sprintf("pr-%s-%d", repo.Platform, 1000+i),
			number:      1000 + i,
			title:       prTitles[rng.Intn(len(prTitles))],
			login:       author.login,
			state:       state,
			add:         add,
			del:         del,
			files:       files,
			firstCommit: firstCommit,
			merged:      merged,
			leadSecs:    leadSecs,
			reviewSecs:  reviewSecs,
		})
		tally.total++
	}

	// Batched insert of PRs, capturing generated ids for cycle_times/estimates.
	prIDs := make([]string, len(gens))
	s.must(s.db.WithOrg(s.ctx, orgID, func(tx pgx.Tx) error {
		const q = `
			INSERT INTO pull_requests
			    (org_id, repo_id, platform, external_id, number, title, author_login,
			     state, additions, deletions, changed_files, first_commit_at, merged_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			ON CONFLICT (org_id, repo_id, external_id) DO UPDATE SET
			    title = EXCLUDED.title, state = EXCLUDED.state, merged_at = EXCLUDED.merged_at
			RETURNING id`
		batch := &pgx.Batch{}
		for _, g := range gens {
			batch.Queue(q, orgID, g.repoID, g.platform, g.extID, g.number, g.title,
				g.login, g.state, g.add, g.del, g.files, g.firstCommit, g.merged)
		}
		br := tx.SendBatch(s.ctx, batch)
		defer br.Close()
		for i := range gens {
			if err := br.QueryRow().Scan(&prIDs[i]); err != nil {
				return fmt.Errorf("seed: batch insert PR: %w", err)
			}
		}
		return nil
	}))

	// Cycle times for merged PRs (drives DORA / lead-time charts).
	s.must(s.db.WithOrg(s.ctx, orgID, func(tx pgx.Tx) error {
		const q = `
			INSERT INTO cycle_times (org_id, pr_id, lead_time_secs, review_secs, computed_at)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT DO NOTHING`
		batch := &pgx.Batch{}
		n := 0
		for i, g := range gens {
			if g.state != "merged" {
				continue
			}
			batch.Queue(q, orgID, prIDs[i], g.leadSecs, g.reviewSecs, *g.merged)
			n++
		}
		if n == 0 {
			return nil
		}
		br := tx.SendBatch(s.ctx, batch)
		defer br.Close()
		for j := 0; j < n; j++ {
			if _, err := br.Exec(); err != nil {
				return fmt.Errorf("seed: batch insert cycle_time: %w", err)
			}
		}
		tally.cycleTimes = n
		return nil
	}))

	// Effort estimates for a representative subset of PRs (LLM diff-difficulty).
	s.must(s.db.WithOrg(s.ctx, orgID, func(tx pgx.Tx) error {
		const q = `
			INSERT INTO effort_estimates (org_id, pr_id, difficulty, rationale, evidence, model)
			VALUES ($1, $2, $3, $4, $5::jsonb, $6)
			ON CONFLICT DO NOTHING`
		rationales := []string{
			"Focused change, low blast radius and good test coverage.",
			"Touches shared infrastructure; moderate cross-cutting risk.",
			"Complex flow with several edge cases and external integration.",
			"Large diff but mechanical; mostly generated or repetitive.",
			"Performance-sensitive path requiring careful benchmarking.",
		}
		batch := &pgx.Batch{}
		n := 0
		for i, g := range gens {
			if rng.Float64() > 0.55 { // estimate ~55% of PRs
				continue
			}
			diff := 1.5 + rng.Float64()*8.0
			if g.add+g.del > 800 {
				diff = math.Min(10, diff+2)
			}
			ev := fmt.Sprintf(
				`{"files_changed":%d,"additions":%d,"deletions":%d,"pr_number":%d}`,
				g.files, g.add, g.del, g.number)
			batch.Queue(q, orgID, prIDs[i],
				math.Round(diff*10)/10,
				rationales[rng.Intn(len(rationales))], ev, "claude-sonnet-4-6")
			n++
		}
		if n == 0 {
			return nil
		}
		br := tx.SendBatch(s.ctx, batch)
		defer br.Close()
		for j := 0; j < n; j++ {
			if _, err := br.Exec(); err != nil {
				return fmt.Errorf("seed: batch insert effort_estimate: %w", err)
			}
		}
		tally.estimates = n
		return nil
	}))

	_ = projects
	return tally
}

// ── issues ──────────────────────────────────────────────────────────────────

type issueTally struct {
	total, git, native             int
	open, inProgress, done, closed int
}

var (
	issueTitles = []string{
		"Implement OAuth login flow",
		"Dark mode support",
		"Reduce API latency p99",
		"Update API documentation",
		"Add contribution heatmap to dashboard",
		"Fix flaky integration tests",
		"Migrate jobs to the new queue",
		"Add export to CSV/Excel",
		"Improve onboarding empty states",
		"Rate-limit the public API",
		"Backfill missing timestamps",
		"Add audit logging to admin actions",
		"Support GitLab self-hosted instances",
		"Webhook delivery retries",
		"Per-org usage metering",
		"Kanban drag-and-drop persistence",
		"Slow query on the reports page",
		"Add keyboard shortcuts",
		"Internationalize date formatting",
		"Cache invalidation on settings change",
		"Add SSO via Microsoft Entra",
		"Pagination on the members list",
		"Investigate memory growth in the worker",
		"Add health checks to all services",
		"Schema migration for per-builder pricing",
	}
	issueLabels = [][]string{
		{"feature"}, {"bug"}, {"performance", "backend"}, {"docs"},
		{"ui", "feature"}, {"testing"}, {"infra"}, {"auth", "security"},
		{"agent"}, {"good-first-issue"}, {"tech-debt"},
	}
	issueStates = []struct {
		state, derived string
		weight         float64
	}{
		{"done", "merged", 0.34},
		{"closed", "closed", 0.10},
		{"in_progress", "in_progress", 0.22},
		{"open", "open", 0.34},
	}
)

func pickIssueState() (string, string) {
	r := rng.Float64()
	var acc float64
	for _, st := range issueStates {
		acc += st.weight
		if r <= acc {
			return st.state, st.derived
		}
	}
	return "open", "open"
}

// seedIssues creates ~120 issues: a git-synced majority (with derived_state and
// platform/external_id) plus a native minority, assigned across members and
// spread across projects/labels so the Kanban board and triage views fill out.
func (s *seeder) seedIssues(orgID string, repos []*store.Repo, projects []*projectRow) ([]string, issueTally) {
	var tally issueTally

	// Assignees: builders + PMs (not stakeholders, not bots most of the time).
	var assignees []*member
	for _, m := range members {
		if m.role == "stakeholder" {
			continue
		}
		assignees = append(assignees, m)
	}

	const targetIssues = 120
	type issueGen struct {
		projectID string
		repoID    *string
		source    string
		platform  string
		extID     string
		number    int
		title     string
		body      string
		state     string
		derived   string
		assignee  *string
		labels    []string
	}
	var gens []issueGen

	for i := 0; i < targetIssues; i++ {
		proj := projects[rng.Intn(len(projects))]
		assignee := assignees[rng.Intn(len(assignees))]
		state, derived := pickIssueState()
		labels := issueLabels[rng.Intn(len(issueLabels))]
		title := fmt.Sprintf("%s (#%d)", issueTitles[rng.Intn(len(issueTitles))], 100+i)

		g := issueGen{
			projectID: proj.ID,
			number:    100 + i,
			title:     title,
			body:      "Synthetic demo issue generated by the seed command.",
			state:     state,
			assignee:  &assignee.user.ID,
			labels:    labels,
		}
		// ~70% git-synced, ~30% native.
		if rng.Float64() < 0.70 {
			repo := repos[rng.Intn(len(repos))]
			g.source = "git"
			g.platform = repo.Platform
			g.extID = fmt.Sprintf("%s-issue-%d", repo.Platform, 100+i)
			g.repoID = &repo.ID
			g.derived = derived
			tally.git++
		} else {
			g.source = "native"
			g.derived = ""
			tally.native++
		}

		switch state {
		case "done":
			tally.done++
		case "closed":
			tally.closed++
		case "in_progress":
			tally.inProgress++
		default:
			tally.open++
		}
		gens = append(gens, g)
		tally.total++
	}

	issueIDs := make([]string, 0, len(gens))
	s.must(s.db.WithOrg(s.ctx, orgID, func(tx pgx.Tx) error {
		const q = `
			INSERT INTO issues
			    (org_id, project_id, repo_id, source, platform, external_id, number,
			     title, body, state, derived_state, assignee_id, labels)
			VALUES ($1, $2, $3, $4,
			        NULLIF($5,''), NULLIF($6,''), NULLIF($7::int,0),
			        $8, $9, $10, NULLIF($11,''), $12, $13)
			ON CONFLICT (org_id, platform, external_id) WHERE platform IS NOT NULL
			DO UPDATE SET title = EXCLUDED.title, state = EXCLUDED.state,
			              derived_state = EXCLUDED.derived_state
			RETURNING id`
		batch := &pgx.Batch{}
		for _, g := range gens {
			batch.Queue(q, orgID, g.projectID, g.repoID, g.source, g.platform,
				g.extID, g.number, g.title, g.body, g.state, g.derived,
				g.assignee, g.labels)
		}
		br := tx.SendBatch(s.ctx, batch)
		defer br.Close()
		for range gens {
			var id string
			if err := br.QueryRow().Scan(&id); err != nil {
				return fmt.Errorf("seed: batch insert issue: %w", err)
			}
			issueIDs = append(issueIDs, id)
		}
		return nil
	}))

	return issueIDs, tally
}

// ── involvement texture ─────────────────────────────────────────────────────

// seedInvolvement writes monthly involvement rows per member/project derived
// from their commit volume, so the texture cards (never a single score) fill in.
func (s *seeder) seedInvolvement(orgID string, projects []*projectRow, ct commitTally) {
	s.must(s.db.WithOrg(s.ctx, orgID, func(tx pgx.Tx) error {
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

		batch := &pgx.Batch{}
		n := 0
		// Last 9 months, one row per member per (primary) project per month.
		for monthOffset := 0; monthOffset < 9; monthOffset++ {
			periodStart := now.AddDate(0, -monthOffset, 0).
				Truncate(24 * time.Hour)
			ps := time.Date(periodStart.Year(), periodStart.Month(), 1, 0, 0, 0, 0, periodStart.Location())

			for mi, m := range members {
				if m.weight <= 0 && m.role != "admin" {
					continue
				}
				// Assign each member a stable home project.
				proj := projects[mi%len(projects)]

				total := ct.perUser[m.user.ID]
				// Scale per-month roughly from total/9 with jitter.
				monthly := int(float64(total)/9.0*(0.6+rng.Float64()*0.9)) + 1
				features := monthly/12 + rng.Intn(3)
				reviews := monthly/8 + rng.Intn(5)
				if m.role == "admin" { // PMs review more, ship less
					features = rng.Intn(2)
					reviews = 4 + rng.Intn(10)
				}
				areas := 1 + rng.Intn(3)
				added := ct.perUserAdd[m.user.ID] / 9
				deleted := ct.perUserDel[m.user.ID] / 9
				dims := fmt.Sprintf(
					`{"commit_count":%d,"lines_added":%d,"lines_deleted":%d,"is_agent":%t}`,
					monthly, added, deleted, m.isAgent)

				batch.Queue(q, orgID, proj.ID, m.user.ID, ps.Format("2006-01-02"),
					features, reviews, areas, true, dims)
				n++
			}
		}
		br := tx.SendBatch(s.ctx, batch)
		defer br.Close()
		for j := 0; j < n; j++ {
			if _, err := br.Exec(); err != nil {
				return fmt.Errorf("seed: batch insert involvement: %w", err)
			}
		}
		return nil
	}))
}

// ── capacity ────────────────────────────────────────────────────────────────

func (s *seeder) seedAvailability(orgID string) {
	s.must(s.db.WithOrg(s.ctx, orgID, func(tx pgx.Tx) error {
		const q = `
			INSERT INTO availability (org_id, user_id, weekly_hours, working_days, effective_from)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT DO NOTHING`
		std := "{1,2,3,4,5}"
		epoch := now.AddDate(0, 0, -historyDays).Format("2006-01-02")
		batch := &pgx.Batch{}
		n := 0
		for _, m := range members {
			if m.role == "stakeholder" {
				continue // read-only seat, no capacity tracked
			}
			hours := 40.0
			if m.role == "admin" {
				hours = 32.0 // part-time PM
			}
			batch.Queue(q, orgID, m.user.ID, hours, std, epoch)
			n++
		}
		br := tx.SendBatch(s.ctx, batch)
		defer br.Close()
		for j := 0; j < n; j++ {
			if _, err := br.Exec(); err != nil {
				return fmt.Errorf("seed: batch insert availability: %w", err)
			}
		}
		return nil
	}))
}

// leaveTypeSeed is a configurable leave type plus the legacy `kind` it maps to
// (so existing capacity math, which still keys off `kind`, stays coherent).
type leaveTypeSeed struct {
	id          string  // filled in after insert
	name        string  // Vacation, Sick, Personal, Parental
	kind        string  // legacy classifier: pto | sick | holiday
	color       string  // hex
	defaultDays float64 // annual entitlement
	carryover   float64
	paid        bool
}

// seedLeaveTypes inserts the configurable leave-type catalogue for the org and
// returns it with ids resolved. Vacation/Sick/Personal/Parental, each colour-
// coded so the team calendar reads at a glance.
func (s *seeder) seedLeaveTypes(orgID string) []*leaveTypeSeed {
	types := []*leaveTypeSeed{
		{name: "Vacation", kind: "pto", color: "#2DD4BF", defaultDays: 25, carryover: 5, paid: true},
		{name: "Sick", kind: "sick", color: "#F59E0B", defaultDays: 10, carryover: 0, paid: true},
		{name: "Personal", kind: "pto", color: "#6366F1", defaultDays: 5, carryover: 0, paid: true},
		{name: "Parental", kind: "holiday", color: "#EC4899", defaultDays: 90, carryover: 0, paid: true},
	}
	s.must(s.db.WithOrg(s.ctx, orgID, func(tx pgx.Tx) error {
		const q = `
			INSERT INTO leave_types
			    (org_id, name, color, default_days, requires_approval, accrues, carryover_max, paid)
			VALUES ($1, $2, $3, $4, true, false, $5, $6)
			ON CONFLICT (org_id, name) DO UPDATE SET
			    color         = EXCLUDED.color,
			    default_days  = EXCLUDED.default_days,
			    carryover_max = EXCLUDED.carryover_max,
			    paid          = EXCLUDED.paid
			RETURNING id`
		for _, t := range types {
			if err := tx.QueryRow(s.ctx, q,
				orgID, t.name, t.color, t.defaultDays, t.carryover, t.paid,
			).Scan(&t.id); err != nil {
				return fmt.Errorf("seed: insert leave_type %s: %w", t.name, err)
			}
		}
		return nil
	}))
	return types
}

// seedLeave writes a scattering of typed leave entries across members and across
// the period so the team calendar shows real gaps. Each entry carries both the
// legacy `kind` (for capacity math) and a `leave_type_id` (for the richer UI),
// and a minority are half-days.
func (s *seeder) seedLeave(orgID string, types []*leaveTypeSeed) int {
	type leave struct {
		userID, kind, typeID, start, end, note, portion string
		halfDay                                         bool
	}
	notes := map[string]string{
		"Vacation": "Annual leave", "Sick": "Out sick",
		"Personal": "Personal day", "Parental": "Parental leave",
	}
	var rows []leave
	for _, m := range members {
		if m.role == "stakeholder" || m.isAgent {
			continue
		}
		// 2–4 leave entries per person across the window.
		for k := 0; k < 2+rng.Intn(3); k++ {
			t := types[rng.Intn(len(types))]
			daysBack := rng.Intn(historyDays) - 14 // can be slightly future
			start := now.AddDate(0, 0, -daysBack)

			// ~20% of non-parental leave is a single half-day.
			halfDay := t.name != "Parental" && rng.Float64() < 0.2
			portion := "full"
			span := 0
			switch {
			case halfDay:
				portion = []string{"am", "pm"}[rng.Intn(2)]
			case t.name == "Parental":
				span = 20 + rng.Intn(40) // long block
			case t.kind == "pto":
				span = rng.Intn(5)
			}
			end := start.AddDate(0, 0, span)
			rows = append(rows, leave{
				userID: m.user.ID, kind: t.kind, typeID: t.id,
				start:   start.Format("2006-01-02"),
				end:     end.Format("2006-01-02"),
				note:    notes[t.name],
				halfDay: halfDay, portion: portion,
			})
		}
	}
	s.must(s.db.WithOrg(s.ctx, orgID, func(tx pgx.Tx) error {
		const q = `
			INSERT INTO leave_entries
			    (org_id, user_id, kind, leave_type_id, start_date, end_date, half_day, portion, status, note)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'approved', $9)
			ON CONFLICT DO NOTHING`
		batch := &pgx.Batch{}
		for _, r := range rows {
			batch.Queue(q, orgID, r.userID, r.kind, r.typeID, r.start, r.end, r.halfDay, r.portion, r.note)
		}
		br := tx.SendBatch(s.ctx, batch)
		defer br.Close()
		for range rows {
			if _, err := br.Exec(); err != nil {
				return fmt.Errorf("seed: batch insert leave_entry: %w", err)
			}
		}
		return nil
	}))
	return len(rows)
}

// seedLeaveBalances writes a per-member, per-type balance for the current year.
// entitled_days comes from the type default (with a little jitter for realism),
// carried_days from prior-year carryover, and used_days is computed from the
// approved leave just seeded — so remaining (entitled+carried−used) reads true.
func (s *seeder) seedLeaveBalances(orgID string, types []*leaveTypeSeed) {
	year := now.Year()
	s.must(s.db.WithOrg(s.ctx, orgID, func(tx pgx.Tx) error {
		const upsert = `
			INSERT INTO leave_balances
			    (org_id, user_id, leave_type_id, year, entitled_days, carried_days)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (org_id, user_id, leave_type_id, year) DO UPDATE SET
			    entitled_days = EXCLUDED.entitled_days,
			    carried_days  = EXCLUDED.carried_days`
		// used_days is the SUM of approved leave days of this type this year
		// (half-days count as 0.5), matching store.RecomputeUsedDays.
		const recompute = `
			UPDATE leave_balances b SET used_days = COALESCE((
			    SELECT SUM(CASE WHEN e.half_day THEN 0.5 ELSE (e.end_date - e.start_date) + 1 END)
			    FROM leave_entries e
			    WHERE e.org_id = b.org_id AND e.user_id = b.user_id
			      AND e.leave_type_id = b.leave_type_id
			      AND e.status = 'approved'
			      AND EXTRACT(YEAR FROM e.start_date) = b.year
			), 0)
			WHERE b.org_id = $1 AND b.year = $2`

		batch := &pgx.Batch{}
		n := 0
		for _, m := range members {
			if m.role == "stakeholder" || m.isAgent {
				continue
			}
			for _, t := range types {
				// Jitter entitlement slightly so cards vary; carryover only for
				// types that allow it.
				entitled := t.defaultDays
				if t.name == "Vacation" {
					entitled = t.defaultDays - float64(rng.Intn(4)) // some used last year
				}
				carried := 0.0
				if t.carryover > 0 && rng.Float64() < 0.6 {
					carried = math.Round(rng.Float64()*t.carryover*10) / 10
				}
				batch.Queue(upsert, orgID, m.user.ID, t.id, year, entitled, carried)
				n++
			}
		}
		br := tx.SendBatch(s.ctx, batch)
		for j := 0; j < n; j++ {
			if _, err := br.Exec(); err != nil {
				br.Close()
				return fmt.Errorf("seed: batch insert leave_balance: %w", err)
			}
		}
		br.Close()
		// Backfill used_days from the approved leave entries.
		if _, err := tx.Exec(s.ctx, recompute, orgID, year); err != nil {
			return fmt.Errorf("seed: recompute leave_balance used_days: %w", err)
		}
		return nil
	}))
}

// seedTimeEntries logs manual/git time across the last ~8 weeks against issues.
func (s *seeder) seedTimeEntries(orgID string, issueIDs []string) int {
	var builders []*member
	for _, m := range members {
		if m.role == "stakeholder" {
			continue
		}
		builders = append(builders, m)
	}
	type te struct {
		userID  string
		issueID *string
		source  string
		minutes int
		day     string
		note    string
	}
	notes := []string{
		"Implementation session", "Code review + pairing", "Debugging production issue",
		"Spec + design discussion", "Refactor and cleanup", "Writing tests",
		"Sprint planning", "Investigation / spike",
	}
	var rows []te
	for d := 0; d < 56; d++ { // last 8 weeks
		day := now.AddDate(0, 0, -d)
		if wd := day.Weekday(); wd == time.Saturday || wd == time.Sunday {
			if rng.Float64() > 0.2 {
				continue
			}
		}
		entriesToday := 2 + rng.Intn(6)
		for e := 0; e < entriesToday; e++ {
			m := builders[rng.Intn(len(builders))]
			var iss *string
			if len(issueIDs) > 0 && rng.Float64() < 0.8 {
				id := issueIDs[rng.Intn(len(issueIDs))]
				iss = &id
			}
			src := "manual"
			if rng.Float64() < 0.4 {
				src = "git"
			}
			rows = append(rows, te{
				userID:  m.user.ID,
				issueID: iss,
				source:  src,
				minutes: 30 + rng.Intn(7)*30, // 30–210 min
				day:     day.Format("2006-01-02"),
				note:    notes[rng.Intn(len(notes))],
			})
		}
	}
	s.must(s.db.WithOrg(s.ctx, orgID, func(tx pgx.Tx) error {
		const q = `
			INSERT INTO time_entries (org_id, user_id, issue_id, source, minutes, occurred_on, note)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT DO NOTHING`
		batch := &pgx.Batch{}
		for _, r := range rows {
			batch.Queue(q, orgID, r.userID, r.issueID, r.source, r.minutes, r.day, r.note)
		}
		br := tx.SendBatch(s.ctx, batch)
		defer br.Close()
		for range rows {
			if _, err := br.Exec(); err != nil {
				return fmt.Errorf("seed: batch insert time_entry: %w", err)
			}
		}
		return nil
	}))
	return len(rows)
}

// ── summary ─────────────────────────────────────────────────────────────────

func (s *seeder) printSummary(
	org *store.Org, adminEmail string, repos, projects int,
	ct commitTally, pr prTally, iss issueTally, leave, timeEntries int,
) {
	fmt.Printf(`
╔═══════════════════════════════════════════════════════════════╗
║              gitstate demo seed — complete                    ║
╠═══════════════════════════════════════════════════════════════╣
║  Org:        %s (%s)
║  Plan:       Team — active subscription
║  Members:    %d  (builders + PMs + 1 stakeholder + 2 agents)
║
║  Repos:      %d   (GitHub + GitLab)
║  Projects:   %d
║  Commits:    %d   (%d human · %d agent)  over ~%d months
║  Pull reqs:  %d   (%d merged · %d open · %d closed)
║  Cycle times:%d   Effort estimates: %d
║  Issues:     %d   (%d git · %d native)
║              open %d · in-progress %d · done %d · closed %d
║  Leave:      %d   Time entries: %d
║
║  → Open http://localhost:8080/login
║    Email:    %s
║    Password: %s
╚═══════════════════════════════════════════════════════════════╝
`,
		org.Name, org.Slug, len(members),
		repos, projects,
		ct.total, ct.human, ct.agent, historyDays/30,
		pr.total, pr.merged, pr.open, pr.closed,
		pr.cycleTimes, pr.estimates,
		iss.total, iss.git, iss.native,
		iss.open, iss.inProgress, iss.done, iss.closed,
		leave, timeEntries,
		adminEmail, demoPassword,
	)
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

func loginFromEmail(email string) string {
	if i := strings.IndexByte(email, '@'); i > 0 {
		return email[:i]
	}
	return email
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
	const q = `
		INSERT INTO organizations (slug, name, plan_key)
		VALUES ($1, $2, $3)
		ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name, plan_key = EXCLUDED.plan_key
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

// upsertRepo connects a repo for the org via a short org-scoped tx (RLS).
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
		const fetch = `SELECT id, name, COALESCE(key,'') FROM projects WHERE org_id=$1 AND name=$2`
		return tx.QueryRow(s.ctx, fetch, orgID, name).Scan(&p.ID, &p.Name, &p.Key)
	})
	s.must(wrapErr("upsert project "+name, err))
	return &p
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
