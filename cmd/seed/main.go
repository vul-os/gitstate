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
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"sort"
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

// profile encodes a member's *behavioural archetype* so the multi-dimensional
// Contribution view shows clearly different leaders per dimension instead of a
// flat field of near-equal scores. Each field is a multiplier/level applied
// deterministically on top of the commit-derived baseline:
//
//   - ship      → features_shipped + merged-PR footprint (the "shipper")
//   - review    → reviews_done (the senior who unblocks everyone)
//   - own       → areas_owned (the deep specialist)
//   - effort    → difficulty bias on this author's merged PRs (lands hard work)
//   - revertPct → share of this member's commits that are reverts/hotfixes/
//     rollbacks (drives the quality dimension: higher ⇒ noisier ⇒ lower quality)
//   - cycleBias → multiplier on merged-PR lead time (>1 slower, <1 faster) so the
//     quality dimension also reflects how cleanly/quickly their work lands.
//
// Profiles are intentionally spiky: a strong shipper is a weak reviewer, the
// senior reviewer ships little, the specialist owns many areas but ships fewer
// features, one member carries visible quality debt, and the agent bots commit
// plenty while their merged-PR / review footprint stays modest (gaming-resistant
// story: raw agent activity must NOT inflate human-style credit).
type profile struct {
	ship      float64
	review    float64
	own       float64
	effort    float64
	revertPct float64
	cycleBias float64
}

// member describes a synthetic org member and their behavioural profile.
type member struct {
	email   string
	name    string
	login   string  // git author_login (split-part of email by default)
	role    string  // owner | admin | member | stakeholder | agent
	isAgent bool    // contributes is_agent=true commits
	weight  float64 // relative share of commit/PR volume (0 ⇒ no git output)
	prof    profile // behavioural archetype driving per-dimension spread
	user    *store.User
}

// members defines ~12 people: a few heavy builders, a long tail, two PMs/stake-
// holders, and two bot-ish agent identities. weight drives commit distribution;
// prof drives the per-dimension Contribution spread (see profile docs).
var members = []*member{
	// Alex — balanced owner, light quality debt.
	{email: "demo@gitstate.dev", name: "Alex Rivera", login: "arivera", role: "owner", weight: 1.6,
		prof: profile{ship: 1.0, review: 1.1, own: 1.2, effort: 1.0, revertPct: 0.05, cycleBias: 1.0}},
	// Priya — senior reviewer + deep owner, ships fewer features (the unblocker).
	{email: "priya.nair@acme.dev", name: "Priya Nair", login: "pnair", role: "admin", weight: 1.9,
		prof: profile{ship: 0.5, review: 2.6, own: 2.2, effort: 1.2, revertPct: 0.02, cycleBias: 0.85}},
	// Marcus — the heavy shipper: tons of merged features, light on review.
	{email: "marcus.lee@acme.dev", name: "Marcus Lee", login: "mlee", role: "member", weight: 2.2,
		prof: profile{ship: 2.4, review: 0.5, own: 0.9, effort: 1.0, revertPct: 0.08, cycleBias: 0.95}},
	// Sofia — the high-effort, high-quality engineer: lands hard PRs cleanly.
	{email: "sofia.gomez@acme.dev", name: "Sofia Gómez", login: "sgomez", role: "member", weight: 1.4,
		prof: profile{ship: 1.1, review: 1.0, own: 1.0, effort: 2.3, revertPct: 0.02, cycleBias: 0.7}},
	// Tom — the member carrying visible quality debt: many reverts/hotfixes, slow.
	{email: "tom.fischer@acme.dev", name: "Tom Fischer", login: "tfischer", role: "member", weight: 1.1,
		prof: profile{ship: 0.9, review: 0.6, own: 0.8, effort: 0.9, revertPct: 0.28, cycleBias: 1.6}},
	// Aisha — deep specialist/owner of many areas, moderate shipping.
	{email: "aisha.khan@acme.dev", name: "Aisha Khan", login: "akhan", role: "member", weight: 1.3,
		prof: profile{ship: 0.7, review: 0.9, own: 2.6, effort: 1.3, revertPct: 0.04, cycleBias: 0.9}},
	// Diego — solid mid shipper, clean record.
	{email: "diego.santos@acme.dev", name: "Diego Santos", login: "dsantos", role: "member", weight: 0.7,
		prof: profile{ship: 1.3, review: 0.7, own: 0.9, effort: 1.0, revertPct: 0.03, cycleBias: 0.95}},
	// Yuki — secondary reviewer, light shipping.
	{email: "yuki.tanaka@acme.dev", name: "Yuki Tanaka", login: "ytanaka", role: "member", weight: 0.5,
		prof: profile{ship: 0.6, review: 1.8, own: 1.0, effort: 1.1, revertPct: 0.05, cycleBias: 0.9}},
	// Noah — junior: low volume, some quality debt.
	{email: "noah.brooks@acme.dev", name: "Noah Brooks", login: "nbrooks", role: "member", weight: 0.35,
		prof: profile{ship: 0.8, review: 0.4, own: 0.6, effort: 0.8, revertPct: 0.18, cycleBias: 1.3}},
	// Riley — PM/admin: reviews lots, ships almost nothing.
	{email: "riley.pm@acme.dev", name: "Riley Okonkwo", login: "rokonkwo", role: "admin", weight: 0.15,
		prof: profile{ship: 0.2, review: 2.2, own: 1.4, effort: 1.0, revertPct: 0.0, cycleBias: 1.0}},
	// Sam — read-only stakeholder, no git output.
	{email: "sam.stake@acme.dev", name: "Sam Whitfield", login: "swhitfield", role: "stakeholder", weight: 0,
		prof: profile{ship: 0, review: 0, own: 0, effort: 1.0, revertPct: 0.0, cycleBias: 1.0}},
	// Claude Agent — commits a LOT but merged-PR/review footprint is gated/modest.
	{email: "claude-bot@acme.dev", name: "Claude Agent", login: "claude-bot", role: "agent", isAgent: true, weight: 1.2,
		prof: profile{ship: 0.25, review: 0.15, own: 0.4, effort: 0.7, revertPct: 0.12, cycleBias: 1.0}},
	// Acme Build Bot — dependency bumps; tons of churn, negligible credit.
	{email: "dependabot@acme.dev", name: "Acme Build Bot", login: "acme-bot", role: "agent", isAgent: true, weight: 0.6,
		prof: profile{ship: 0.1, review: 0.05, own: 0.2, effort: 0.4, revertPct: 0.06, cycleBias: 1.0}},
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

	// ── 10a. Contribution extras: trends snapshots · kudos · equity ledger ─
	s.seedContributionExtras(org.ID)

	// ── 10b. Client invoicing: demo clients + one generated invoice ───────
	s.seedInvoicing(org.ID, projects)

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
	reverts    int            // revert/hotfix/rollback commits (quality signal)
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

func (s *seeder) commitMessage(m *member) (msgOut string, add, del int, isRevert bool) {
	typ := commitTypes[rng.Intn(len(commitTypes))]
	scope := commitScopes[rng.Intn(len(commitScopes))]
	var subj string
	if m.isAgent {
		subj = agentSubjects[rng.Intn(len(agentSubjects))]
	} else {
		subj = commitSubjects[rng.Intn(len(commitSubjects))]
	}
	msg := fmt.Sprintf("%s(%s): %s", typ, scope, subj)

	// Quality signal: a member-specific share of commits are reverts / hotfixes /
	// rollbacks. The Contribution "quality" dimension keys off these prefixes, so
	// members with a higher prof.revertPct (e.g. Tom, Noah) read as noisier while
	// clean operators (Priya, Sofia) read as higher quality. Prefixes match what
	// the scorer scans for: "Revert " / "hotfix:" / "rollback".
	if m.prof.revertPct > 0 && rng.Float64() < m.prof.revertPct {
		isRevert = true
		switch rng.Intn(3) {
		case 0:
			msg = fmt.Sprintf("Revert \"%s(%s): %s\"", typ, scope, subj)
		case 1:
			msg = fmt.Sprintf("hotfix: %s(%s): %s", typ, scope, subj)
		default:
			msg = fmt.Sprintf("rollback %s change after regression", scope)
		}
	}

	// Size distribution: mostly small, occasionally large; agents skew bigger
	// on additions (generated code) and bigger on deletions for refactors.
	add = 5 + rng.Intn(60)
	del = rng.Intn(25)
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
	return msg, add, del, isRevert
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

			msg, add, del, isRevert := s.commitMessage(m)
			if isRevert {
				tally.reverts++
			}
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
	// Authorship share is weighted by the member's shipping archetype so the
	// MERGED-PR footprint differentiates: strong shippers (Marcus, Diego) author
	// the most PRs while agent bots — though they commit plenty — author very few
	// (gaming-resistance: raw agent commit churn must not become merged credit).
	var authors []*member
	var authorW []float64
	var awSum float64
	for _, m := range members {
		if m.weight <= 0 && m.role != "admin" {
			continue
		}
		// Share ∝ shipping level; agents are damped hard so they stay modest.
		w := 0.3 + m.prof.ship
		if m.isAgent {
			w *= 0.2
		}
		authors = append(authors, m)
		awSum += w
		authorW = append(authorW, awSum)
	}
	pickAuthor := func() *member {
		r := rng.Float64() * awSum
		for i, w := range authorW {
			if r <= w {
				return authors[i]
			}
		}
		return authors[len(authors)-1]
	}

	const targetPRs = 210

	type prGen struct {
		repoID, platform, extID, title, login, state string
		number                                       int
		add, del, files                              int
		firstCommit                                  time.Time
		merged                                       *time.Time
		leadSecs, reviewSecs                         int64
		effortBias                                   float64 // author archetype → difficulty bias
	}
	var gens []prGen

	for i := 0; i < targetPRs; i++ {
		repo := repos[rng.Intn(len(repos))]
		author := pickAuthor()

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
			// Quality bias: clean operators (cycleBias<1) land work faster; members
			// carrying debt (cycleBias>1) drag cycle time out. Feeds the quality
			// dimension (faster, fewer reverts ⇒ higher quality).
			lead = time.Duration(float64(lead) * author.prof.cycleBias)
			if lead < time.Hour {
				lead = time.Hour
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
			effortBias:  author.prof.effort,
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
			// Author archetype shifts difficulty so the "effort" dimension isn't
			// flat: high-effort engineers (Sofia, Aisha) land harder PRs; bots and
			// dependency-bump churn skew easy. Base spread stays, bias re-centres it.
			diff := 1.5 + rng.Float64()*6.0
			diff *= g.effortBias
			if g.add+g.del > 800 {
				diff = math.Min(10, diff+2)
			}
			if diff < 1 {
				diff = 1
			}
			if diff > 10 {
				diff = 10
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

// issueTimestamps returns a deterministic (created_at, updated_at) pair for a
// seeded issue. Creation dates are spread across the whole ~9-month history and
// clustered like real work — biased toward more recent weeks (an active, growing
// backlog) with weekday-heavy timing — so the analytics "issues opened over
// time" series fills multiple buckets instead of collapsing to one. updated_at
// reflects the last state change: terminal issues (done/closed) get an
// updated_at some working days after creation (a believable cycle time, clamped
// to "now"); active issues (open/in_progress) are touched at/just after
// creation. All randomness flows through the shared seeded rng.
func issueTimestamps(state string) (time.Time, time.Time) {
	// Bias creation toward recency: square of a uniform pushes mass toward 0
	// days-back. Range is the full history window (minus a small head so even
	// the oldest issues sit inside the commit history).
	frac := rng.Float64() * rng.Float64() // skewed toward 0 (recent)
	daysBack := int(frac * float64(historyDays-7))

	day := now.AddDate(0, 0, -daysBack)
	// Nudge weekend creations onto an adjacent weekday so the series tracks the
	// weekday-heavy commit cadence.
	switch day.Weekday() {
	case time.Saturday:
		day = day.AddDate(0, 0, -1)
	case time.Sunday:
		day = day.AddDate(0, 0, 1)
	}

	hour := 9 + rng.Intn(9) // 09:00–17:59, working hours
	createdAt := time.Date(day.Year(), day.Month(), day.Day(),
		hour, rng.Intn(60), rng.Intn(60), 0, day.Location())

	switch state {
	case "done", "closed":
		// Cycle time: 1–21 calendar days after creation, clamped to now.
		cycleDays := 1 + rng.Intn(21)
		updatedAt := createdAt.AddDate(0, 0, cycleDays).
			Add(time.Duration(rng.Intn(8)) * time.Hour)
		if updatedAt.After(now) {
			updatedAt = now
		}
		return createdAt, updatedAt
	case "in_progress":
		// Picked up shortly after being opened.
		updatedAt := createdAt.Add(time.Duration(2+rng.Intn(72)) * time.Hour)
		if updatedAt.After(now) {
			updatedAt = now
		}
		return createdAt, updatedAt
	default: // open — untouched since creation
		return createdAt, createdAt
	}
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
		createdAt time.Time
		updatedAt time.Time
	}
	var gens []issueGen

	for i := 0; i < targetIssues; i++ {
		proj := projects[rng.Intn(len(projects))]
		assignee := assignees[rng.Intn(len(assignees))]
		state, derived := pickIssueState()
		labels := issueLabels[rng.Intn(len(issueLabels))]
		title := fmt.Sprintf("%s (#%d)", issueTitles[rng.Intn(len(issueTitles))], 100+i)

		createdAt, updatedAt := issueTimestamps(state)

		g := issueGen{
			projectID: proj.ID,
			number:    100 + i,
			title:     title,
			body:      "Synthetic demo issue generated by the seed command.",
			state:     state,
			assignee:  &assignee.user.ID,
			labels:    labels,
			createdAt: createdAt,
			updatedAt: updatedAt,
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
			     title, body, state, derived_state, assignee_id, labels,
			     created_at, updated_at)
			VALUES ($1, $2, $3, $4,
			        NULLIF($5,''), NULLIF($6,''), NULLIF($7::int,0),
			        $8, $9, $10, NULLIF($11,''), $12, $13,
			        $14, $15)
			ON CONFLICT (org_id, platform, external_id) WHERE platform IS NOT NULL
			DO UPDATE SET title = EXCLUDED.title, state = EXCLUDED.state,
			              derived_state = EXCLUDED.derived_state,
			              created_at = EXCLUDED.created_at,
			              updated_at = EXCLUDED.updated_at
			RETURNING id`
		batch := &pgx.Batch{}
		for _, g := range gens {
			batch.Queue(q, orgID, g.projectID, g.repoID, g.source, g.platform,
				g.extID, g.number, g.title, g.body, g.state, g.derived,
				g.assignee, g.labels, g.createdAt, g.updatedAt)
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

// seedContributionExtras backfills the three CONTRIBUTION extensions so the new
// views (trends · equity · kudos) are never empty:
//
//   - ~6 monthly contribution_snapshots per HUMAN member, with composites that
//     VARY over time (some rising, some flat, one declining) so the over-time
//     chart + sparklines read like a real team, not a flat line.
//   - a handful of peer kudos between members (the human "satisfaction" signal).
//   - a couple of equity_allocations with actual_pct set, so the advisory ledger
//     shows suggested-vs-actual divergence out of the box.
//
// Idempotent: snapshots/equity upsert on their UNIQUE keys; kudos are cleared for
// the org first (they have no natural key) so re-running doesn't pile up dupes.
func (s *seeder) seedContributionExtras(orgID string) {
	// Human members only (agent bots are excluded from equity and don't get a
	// personal trend). Order matters for deterministic archetypes below.
	humans := make([]*member, 0, len(members))
	for _, m := range members {
		if !m.isAgent && m.role != "stakeholder" {
			humans = append(humans, m)
		}
	}

	// trendShape returns a 0–100 composite for a member at a given month-offset
	// back from now (0 = current month). Each archetype gets a base level plus a
	// per-month trajectory so trends look real:
	//   - rising   : climbs steadily toward "now"
	//   - flat     : stable with light jitter
	//   - declining: was strong, drifting down
	const periods = 6
	trendShape := func(mi, monthOffset int) (float64, map[string]float64) {
		base := 45.0 + float64((mi*13)%40) // spread members across 45..85
		// monthsAgo: 5 (oldest) … 0 (newest)
		recency := float64(periods-1-monthOffset) / float64(periods-1) // 0 oldest → 1 newest
		var traj float64
		switch mi % 3 {
		case 0: // rising
			traj = (recency - 0.5) * 30
		case 1: // flat
			traj = (rng.Float64() - 0.5) * 6
		default: // declining
			traj = (0.5 - recency) * 26
		}
		comp := clamp0to100(base + traj + (rng.Float64()-0.5)*4)
		// Per-dimension scores loosely orbit the composite, biased by archetype so
		// the snapshot dimensions aren't all identical.
		d := func(bias float64) float64 { return clamp0to100(comp*bias + (rng.Float64()-0.5)*10) }
		dims := map[string]float64{
			"shipped":    d(1.05),
			"review":     d(0.9),
			"effort":     d(1.0),
			"quality":    d(0.95),
			"ownership":  d(0.85),
			"durability": d(1.0),
		}
		return comp, dims
	}

	// 1) Snapshots — ~6 monthly windows per human member.
	s.must(s.db.WithOrg(s.ctx, orgID, func(tx pgx.Tx) error {
		for mi, m := range humans {
			for monthOffset := 0; monthOffset < periods; monthOffset++ {
				start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).
					AddDate(0, -monthOffset, 0)
				end := start.AddDate(0, 1, 0)
				comp, dims := trendShape(mi, monthOffset)
				if err := store.UpsertContributionSnapshot(s.ctx, tx, orgID, m.user.ID, start, end, comp, dims); err != nil {
					return err
				}
			}
		}
		return nil
	}))

	// 2) Kudos — a handful of believable peer recognitions between members.
	type kudoSeed struct{ from, to, dim, msg string }
	pick := func(i int) *member { return humans[i%len(humans)] }
	kudoSeeds := []kudoSeed{
		{pick(0).user.ID, pick(1).user.ID, "review", "Your review on the auth refactor caught a subtle race — saved us a prod incident."},
		{pick(2).user.ID, pick(1).user.ID, "review", "Thanks for the deep walkthrough on the pipeline PR, learned a lot."},
		{pick(1).user.ID, pick(3).user.ID, "effort", "Landed the hardest part of the migration cleanly. Incredible work."},
		{pick(3).user.ID, pick(2).user.ID, "shipped", "Shipped three features this sprint without breaking a sweat. 🚀"},
		{pick(5).user.ID, pick(3).user.ID, "quality", "Your tests on the billing module are the gold standard now."},
		{pick(0).user.ID, pick(5).user.ID, "ownership", "You owned the data-pipeline rewrite end to end — thank you."},
		{pick(6).user.ID, pick(0).user.ID, "", "Always available to unblock people. The glue that keeps us moving."},
		{pick(1).user.ID, pick(2).user.ID, "shipped", "Relentless shipper. The roadmap moves because of you."},
	}
	s.must(s.db.WithOrg(s.ctx, orgID, func(tx pgx.Tx) error {
		// Clear prior kudos for the org so re-seeding stays idempotent.
		if _, err := tx.Exec(s.ctx, `DELETE FROM kudos WHERE org_id = $1`, orgID); err != nil {
			return err
		}
		for _, k := range kudoSeeds {
			if k.from == k.to {
				continue
			}
			if _, err := store.InsertKudo(s.ctx, tx, orgID, k.from, k.to, k.dim, k.msg); err != nil {
				return err
			}
		}
		return nil
	}))

	// 3) Equity ledger — record a couple of admin-entered actual grants for the
	//    CURRENT month so the advisory ledger shows suggested-vs-actual out of the
	//    box. suggested_pct is left at 0 here; the live GET /api/equity recomputes
	//    it from contribution, so the stored value is just a fallback.
	curStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	curEnd := curStart.AddDate(0, 1, 0)
	pct := func(v float64) *float64 { return &v }
	s.must(s.db.WithOrg(s.ctx, orgID, func(tx pgx.Tx) error {
		grants := []store.EquityAllocation{
			{UserID: pick(0).user.ID, ActualPct: pct(22.5), PoolLabel: "FY26 contribution pool",
				Note: "Founding owner; weighting reviewed with the team."},
			{UserID: pick(1).user.ID, ActualPct: pct(18.0), PoolLabel: "FY26 contribution pool",
				Note: "Senior reviewer — share reflects the invisible unblocking work."},
			{UserID: pick(2).user.ID, ActualPct: pct(16.0), PoolLabel: "FY26 contribution pool", Note: ""},
		}
		for _, g := range grants {
			g.PeriodStart, g.PeriodEnd = curStart, curEnd
			if err := store.UpsertEquityAllocation(s.ctx, tx, g, orgID); err != nil {
				return err
			}
		}
		return nil
	}))
}

// clamp0to100 bounds a float to the 0–100 score range.
func clamp0to100(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return math.Round(v*10) / 10
}

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

				// Per-dimension texture is shaped by the member's archetype so the
				// Contribution view shows different leaders per dimension instead of
				// a flat field. Baselines come from commit volume; prof.* multipliers
				// pull each dimension apart (a shipper ≠ a reviewer ≠ an owner).
				p := m.prof
				features := int(float64(monthly)/12.0*p.ship) + rng.Intn(2)
				reviews := int(float64(monthly)/8.0*p.review) + rng.Intn(2)
				// areas_owned: small integer (1..~7) scaled by the ownership level.
				areas := int(math.Round(float64(1+rng.Intn(2)) * p.own))
				if m.role == "admin" { // PMs review heavily, ship almost nothing
					features = rng.Intn(2)
					reviews = int(float64(6+rng.Intn(8)) * p.review)
				}
				if m.isAgent {
					// Gaming-resistance: bots churn commits but earn little human-
					// style credit. Cap their merged-feature / review footprint hard
					// regardless of raw commit volume.
					if features > 1 {
						features = rng.Intn(2)
					}
					if reviews > 1 {
						reviews = rng.Intn(2)
					}
				}
				if areas < 1 && (m.weight > 0 || m.role == "admin") {
					areas = 1
				}
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

// ── client invoicing ──────────────────────────────────────────────────────────
//
// Creates 1–2 demo clients, links the first project to the primary client, then
// GENERATES one demo invoice straight from the seeded merged PRs + their LLM
// effort estimates — exactly as the /api/invoices/generate endpoint would. Each
// line groups a repo's merged PRs, prices effort×rate, and carries the real git
// evidence ([{prTitle, repo, mergedAt, sha}]). This is the "…and the invoice"
// wedge in action so the page is never empty.
func (s *seeder) seedInvoicing(orgID string, projects []*projectRow) {
	const rateCents = 18000 // $180 / effort point for the demo client

	// 1. Clients (idempotent on (org_id, name)).
	var primaryClientID string
	s.must(s.db.WithOrg(s.ctx, orgID, func(tx pgx.Tx) error {
		const q = `
			INSERT INTO clients (org_id, name, contact_email, rate_cents, notes)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (org_id, name) DO UPDATE SET
				contact_email = EXCLUDED.contact_email,
				rate_cents    = EXCLUDED.rate_cents,
				notes         = EXCLUDED.notes
			RETURNING id`
		if err := tx.QueryRow(s.ctx, q, orgID,
			"Northwind Trading Co.", "ap@northwind.example",
			rateCents, "Retainer client — billed monthly off merged delivery.").Scan(&primaryClientID); err != nil {
			return err
		}
		var second string
		return tx.QueryRow(s.ctx, q, orgID,
			"Helix Robotics", "finance@helix.example",
			15000, "Project-based engagement.").Scan(&second)
	}))

	// 2. Link the first project to the primary client.
	if len(projects) > 0 {
		s.must(s.db.WithOrg(s.ctx, orgID, func(tx pgx.Tx) error {
			_, err := tx.Exec(s.ctx,
				`UPDATE projects SET client_id = $2 WHERE org_id = $1 AND id = $3`,
				orgID, primaryClientID, projects[0].ID)
			return err
		}))
	}

	// 3. Gather merged PRs in the last 30 days with their effort estimates and a
	//    representative commit sha, grouped per repo into invoice line items.
	type evItem struct {
		PRTitle  string `json:"prTitle"`
		Repo     string `json:"repo"`
		MergedAt string `json:"mergedAt"`
		SHA      string `json:"sha"`
	}
	type lineAgg struct {
		repo     string
		points   float64
		count    int
		evidence []evItem
	}
	periodStart := now.AddDate(0, 0, -30)
	aggByRepo := map[string]*lineAgg{}
	var order []string

	s.must(s.db.WithOrg(s.ctx, orgID, func(tx pgx.Tx) error {
		const q = `
			SELECT r.full_name, COALESCE(pr.title,''),
			       COALESCE((SELECT c.sha FROM commits c
			                  WHERE c.org_id = pr.org_id AND c.repo_id = pr.repo_id
			                  ORDER BY c.committed_at DESC LIMIT 1), ''),
			       pr.merged_at, COALESCE(ee.difficulty, 0)
			FROM pull_requests pr
			JOIN repos r ON r.id = pr.repo_id
			LEFT JOIN LATERAL (
			    SELECT difficulty FROM effort_estimates e
			    WHERE e.org_id = pr.org_id AND e.pr_id = pr.id
			    ORDER BY e.created_at DESC LIMIT 1
			) ee ON true
			WHERE pr.org_id = $1 AND pr.state = 'merged'
			  AND pr.merged_at IS NOT NULL AND pr.merged_at >= $2
			ORDER BY r.full_name, pr.merged_at`
		rows, err := tx.Query(s.ctx, q, orgID, periodStart)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var repo, title, sha string
			var mergedAt time.Time
			var diff float64
			if err := rows.Scan(&repo, &title, &sha, &mergedAt, &diff); err != nil {
				return err
			}
			g := aggByRepo[repo]
			if g == nil {
				g = &lineAgg{repo: repo}
				aggByRepo[repo] = g
				order = append(order, repo)
			}
			pts := diff
			if pts <= 0 {
				pts = 1
			}
			g.points += pts
			g.count++
			g.evidence = append(g.evidence, evItem{
				PRTitle: title, Repo: repo, MergedAt: mergedAt.Format(time.RFC3339), SHA: sha,
			})
		}
		return rows.Err()
	}))

	if len(order) == 0 {
		return // no merged delivery in window — skip the demo invoice
	}
	sort.Strings(order)

	// 4. Insert the invoice header (status 'sent' with a share token so the demo
	//    "Copy share link" + public view work out of the box) + its lines.
	tokenBytes := make([]byte, 32)
	_, _ = cryptorand.Read(tokenBytes)
	shareToken := base64.RawURLEncoding.EncodeToString(tokenBytes)

	subtotal := 0
	type lineRow struct {
		desc     string
		points   float64
		amount   int
		evidence []evItem
	}
	var lineRows []lineRow
	for _, repo := range order {
		g := aggByRepo[repo]
		pts := math.Round(g.points*10) / 10
		amount := int(math.Round(pts * float64(rateCents)))
		subtotal += amount
		noun := "merged PR"
		if g.count != 1 {
			noun = "merged PRs"
		}
		lineRows = append(lineRows, lineRow{
			desc:     fmt.Sprintf("%s — %d %s delivered", repo, g.count, noun),
			points:   pts,
			amount:   amount,
			evidence: g.evidence,
		})
	}

	var projectID *string
	if len(projects) > 0 {
		projectID = &projects[0].ID
	}

	s.must(s.db.WithOrg(s.ctx, orgID, func(tx pgx.Tx) error {
		const insHdr = `
			INSERT INTO client_invoices
				(org_id, client_id, project_id, number, status, period_start, period_end,
				 currency, subtotal_cents, total_cents, share_token, issued_at, notes)
			VALUES ($1, $2, $3, $4, 'sent', $5, $6, 'USD', $7, $7, $8, now(),
			        'Auto-generated from merged delivery — every line is backed by git evidence.')
			ON CONFLICT (org_id, number) DO UPDATE SET status = EXCLUDED.status
			RETURNING id`
		var invoiceID string
		if err := tx.QueryRow(s.ctx, insHdr, orgID, primaryClientID, projectID,
			fmt.Sprintf("INV-%d-001", now.Year()),
			periodStart, now, subtotal, shareToken).Scan(&invoiceID); err != nil {
			return err
		}

		// Clear any prior lines (idempotent re-seed) then insert fresh.
		if _, err := tx.Exec(s.ctx,
			`DELETE FROM client_invoice_lines WHERE org_id = $1 AND invoice_id = $2`,
			orgID, invoiceID); err != nil {
			return err
		}
		const insLine = `
			INSERT INTO client_invoice_lines
				(org_id, invoice_id, description, effort_points, quantity,
				 unit_rate_cents, amount_cents, evidence, sort)
			VALUES ($1, $2, $3, $4, 1, $5, $6, $7::jsonb, $8)`
		batch := &pgx.Batch{}
		for i, lr := range lineRows {
			ev, _ := json.Marshal(lr.evidence)
			batch.Queue(insLine, orgID, invoiceID, lr.desc, lr.points,
				rateCents, lr.amount, string(ev), i)
		}
		br := tx.SendBatch(s.ctx, batch)
		defer br.Close()
		for range lineRows {
			if _, err := br.Exec(); err != nil {
				return fmt.Errorf("seed: insert invoice line: %w", err)
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
║  Commits:    %d   (%d human · %d agent · %d revert/hotfix)  over ~%d months
║  Pull reqs:  %d   (%d merged · %d open · %d closed)
║  Cycle times:%d   Effort estimates: %d
║  Issues:     %d   (%d git · %d native)
║              open %d · in-progress %d · done %d · closed %d
║  Leave:      %d   Time entries: %d
║  Contribution: ~6mo trend snapshots · peer kudos · advisory equity ledger
║
║  → Open http://localhost:8080/login
║    Email:    %s
║    Password: %s
╚═══════════════════════════════════════════════════════════════╝
`,
		org.Name, org.Slug, len(members),
		repos, projects,
		ct.total, ct.human, ct.agent, ct.reverts, historyDays/30,
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
