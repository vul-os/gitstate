// internal/billing/service_test.go — DB-backed tests for the billing Service.
//
// These exercise GenerateInvoice end-to-end against a real Postgres (gated on
// DATABASE_URL, skipped cleanly when unset — matching the rest of the suite):
//   - seat lines count only builders (owner/admin/member); stakeholders are free,
//   - the managed-LLM allowance (builders × included_llm_cents) is subtracted before
//     overage and billed at overage_markup,
//   - sub-cent usage costs are ROUNDED (not floored) into cents,
//   - builders with zero git activity are flagged is_estimated with confirmation_required,
//   - builders with git activity produce proven (non-estimated) lines,
//   - the invoice USD total equals the sum of its lines.
//
// Each test creates a throwaway org and DELETEs it at the end (FK cascade cleans
// members/usage/invoices), so the suite stays hermetic.
package billing

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/store"
)

func testDB(t *testing.T) *db.DB {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set — skipping billing integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database, err := db.New(ctx, &config.Config{Database: config.DatabaseConfig{URL: url}})
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	t.Cleanup(database.Close)
	return database
}

// seedOrg creates an org and returns its id plus a cleanup func.
func seedOrg(t *testing.T, ctx context.Context, database *db.DB) (string, func()) {
	t.Helper()
	ns := time.Now().UnixNano()
	var orgID string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO organizations (slug, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("bill-%d", ns), "Billing Test Org").Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	cleanup := func() {
		_, _ = database.Pool().Exec(context.Background(), `DELETE FROM organizations WHERE id = $1`, orgID)
	}
	return orgID, cleanup
}

// addMember inserts a user + org_member with the given role and returns the user id.
func addMember(t *testing.T, ctx context.Context, database *db.DB, orgID, role, name string) string {
	t.Helper()
	ns := time.Now().UnixNano()
	var uid string
	if err := database.Pool().QueryRow(ctx,
		`INSERT INTO users (email, name) VALUES ($1,$2) RETURNING id`,
		fmt.Sprintf("%s-%d@ex.io", role, ns), name).Scan(&uid); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := database.Pool().Exec(ctx,
		`INSERT INTO org_members (org_id, user_id, role) VALUES ($1,$2,$3)`, orgID, uid, role); err != nil {
		t.Fatalf("add member: %v", err)
	}
	return uid
}

func planFor(t *testing.T, ctx context.Context, svc *Service, key string) *store.Plan {
	t.Helper()
	p, err := svc.GetPlan(ctx, key)
	if err != nil {
		t.Fatalf("GetPlan(%q): %v — is the plans table seeded?", key, err)
	}
	return p
}

// TestGenerateInvoice_SeatsExcludeStakeholders verifies stakeholders never get a
// seat line and the seat total equals builders × per_builder_cents.
func TestGenerateInvoice_SeatsExcludeStakeholders(t *testing.T) {
	database := testDB(t)
	ctx := context.Background()
	svc := New(database, &config.Config{})

	orgID, cleanup := seedOrg(t, ctx, database)
	defer cleanup()

	plan := planFor(t, ctx, svc, "team")

	// 3 builders (owner/admin/member) + 2 stakeholders (free).
	addMember(t, ctx, database, orgID, "owner", "Alice")
	addMember(t, ctx, database, orgID, "admin", "Bob")
	addMember(t, ctx, database, orgID, "member", "Carol")
	addMember(t, ctx, database, orgID, "stakeholder", "Dora")
	addMember(t, ctx, database, orgID, "stakeholder", "Eve")

	// Subscribe to team.
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		return store.UpsertSubscription(ctx, tx, orgID, "team", "active", nil, "")
	}); err != nil {
		t.Fatalf("upsert subscription: %v", err)
	}

	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 31, 23, 59, 59, 0, time.UTC)
	inv, err := svc.GenerateInvoice(ctx, orgID, start, end)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}

	// Count seat lines.
	var lines []store.InvoiceLine
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		_, ls, e := store.GetInvoice(ctx, tx, orgID, inv.ID)
		lines = ls
		return e
	}); err != nil {
		t.Fatalf("get invoice: %v", err)
	}

	seatLines := 0
	for _, l := range lines {
		if role, _ := l.Evidence["role"].(string); role != "" {
			seatLines++
			if role == "stakeholder" {
				t.Errorf("stakeholder seat line present: %s", l.Description)
			}
		}
	}
	if seatLines != 3 {
		t.Errorf("seat lines = %d, want 3 (stakeholders excluded)", seatLines)
	}
	// 3 builders × per_builder_cents == seat subtotal.
	wantSeatCents := 3 * plan.PerBuilderCents
	if inv.USDCents != wantSeatCents {
		t.Errorf("invoice total = %d cents, want %d (3 builders × %d, no LLM usage)",
			inv.USDCents, wantSeatCents, plan.PerBuilderCents)
	}
}

// TestGenerateInvoice_LLMAllowanceAndOverage seeds managed-LLM usage above the
// org's included allowance and checks the overage line is (cost − allowance) ×
// markup, and that the invoice total includes it.
func TestGenerateInvoice_LLMAllowanceAndOverage(t *testing.T) {
	database := testDB(t)
	ctx := context.Background()
	svc := New(database, &config.Config{})

	orgID, cleanup := seedOrg(t, ctx, database)
	defer cleanup()

	plan := planFor(t, ctx, svc, "team")

	addMember(t, ctx, database, orgID, "owner", "Alice")
	addMember(t, ctx, database, orgID, "member", "Bob")
	numBuilders := 2

	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		return store.UpsertSubscription(ctx, tx, orgID, "team", "active", nil, "")
	}); err != nil {
		t.Fatalf("upsert subscription: %v", err)
	}

	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 31, 23, 59, 59, 0, time.UTC)

	// Allowance = builders × included_llm_cents. Seed usage well above it.
	allowanceUSD := float64(numBuilders*plan.IncludedLLMCents) / 100
	usageUSD := allowanceUSD + 5.00 // $5 of overage (provider cost)
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		// occurred_at defaults to now() — set it inside the period explicitly.
		_, e := tx.Exec(ctx,
			`INSERT INTO usage_events (org_id, kind, quantity, cost_usd, occurred_at)
			 VALUES ($1,'llm_tokens',$2,$3,$4)`,
			orgID, 100000.0, usageUSD, start.Add(24*time.Hour))
		return e
	}); err != nil {
		t.Fatalf("seed usage: %v", err)
	}

	inv, err := svc.GenerateInvoice(ctx, orgID, start, end)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}

	var lines []store.InvoiceLine
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		_, ls, e := store.GetInvoice(ctx, tx, orgID, inv.ID)
		lines = ls
		return e
	}); err != nil {
		t.Fatalf("get invoice: %v", err)
	}

	var llmLine *store.InvoiceLine
	for i := range lines {
		if k, _ := lines[i].Evidence["kind"].(string); k == "llm_tokens" {
			llmLine = &lines[i]
		}
	}
	if llmLine == nil {
		t.Fatalf("no managed-LLM overage line found; lines=%d", len(lines))
	}

	// overage = usage − allowance; billed = overage × markup (1.00 for team).
	overageCents := int((usageUSD - allowanceUSD) * 100)
	wantBilled := int(float64(overageCents) * plan.OverageMarkup)
	if llmLine.USDCents != wantBilled {
		t.Errorf("LLM overage line = %d cents, want %d (overage %d × markup %.2f)",
			llmLine.USDCents, wantBilled, overageCents, plan.OverageMarkup)
	}

	// Invoice total = seats + overage.
	wantTotal := numBuilders*plan.PerBuilderCents + wantBilled
	if inv.USDCents != wantTotal {
		t.Errorf("invoice total = %d, want %d (seats + overage)", inv.USDCents, wantTotal)
	}
}

// TestGenerateInvoice_LLMUnderAllowance_NoOverageLine: usage below allowance must
// not produce any LLM overage line (the allowance fully absorbs it).
func TestGenerateInvoice_LLMUnderAllowance_NoOverageLine(t *testing.T) {
	database := testDB(t)
	ctx := context.Background()
	svc := New(database, &config.Config{})

	orgID, cleanup := seedOrg(t, ctx, database)
	defer cleanup()

	plan := planFor(t, ctx, svc, "team")
	addMember(t, ctx, database, orgID, "owner", "Alice")
	numBuilders := 1

	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		return store.UpsertSubscription(ctx, tx, orgID, "team", "active", nil, "")
	}); err != nil {
		t.Fatalf("upsert subscription: %v", err)
	}

	start := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 4, 30, 23, 59, 59, 0, time.UTC)

	allowanceUSD := float64(numBuilders*plan.IncludedLLMCents) / 100
	usageUSD := allowanceUSD / 2 // half the allowance
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`INSERT INTO usage_events (org_id, kind, quantity, cost_usd, occurred_at)
			 VALUES ($1,'llm_tokens',$2,$3,$4)`,
			orgID, 1000.0, usageUSD, start.Add(time.Hour))
		return e
	}); err != nil {
		t.Fatalf("seed usage: %v", err)
	}

	inv, err := svc.GenerateInvoice(ctx, orgID, start, end)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}

	var lines []store.InvoiceLine
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		_, ls, e := store.GetInvoice(ctx, tx, orgID, inv.ID)
		lines = ls
		return e
	}); err != nil {
		t.Fatalf("get invoice: %v", err)
	}
	for _, l := range lines {
		if k, _ := l.Evidence["kind"].(string); k == "llm_tokens" {
			t.Errorf("unexpected LLM overage line when usage < allowance: %+v", l)
		}
	}
	// Total is just the single seat.
	if inv.USDCents != numBuilders*plan.PerBuilderCents {
		t.Errorf("invoice total = %d, want %d (seat only)", inv.USDCents, numBuilders*plan.PerBuilderCents)
	}
}

// TestGenerateInvoice_SubCentUsageRounds proves non-LLM usage costs round (not
// floor) to the nearest cent: $0.125 → 13¢ (round-half-up via math.Round), and a
// cost that floors to 0 but rounds to 1¢ still appears.
func TestGenerateInvoice_SubCentUsageRounds(t *testing.T) {
	database := testDB(t)
	ctx := context.Background()
	svc := New(database, &config.Config{})

	orgID, cleanup := seedOrg(t, ctx, database)
	defer cleanup()

	planFor(t, ctx, svc, "team")
	addMember(t, ctx, database, orgID, "owner", "Alice")

	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		return store.UpsertSubscription(ctx, tx, orgID, "team", "active", nil, "")
	}); err != nil {
		t.Fatalf("upsert subscription: %v", err)
	}

	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC)

	// $0.125 of "sync" usage → math.Round(12.5) = 13 cents (rounds up, not floor 12).
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`INSERT INTO usage_events (org_id, kind, quantity, cost_usd, occurred_at)
			 VALUES ($1,'sync',$2,$3,$4)`,
			orgID, 1.0, 0.125, start.Add(time.Hour))
		return e
	}); err != nil {
		t.Fatalf("seed usage: %v", err)
	}

	inv, err := svc.GenerateInvoice(ctx, orgID, start, end)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}
	var lines []store.InvoiceLine
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		_, ls, e := store.GetInvoice(ctx, tx, orgID, inv.ID)
		lines = ls
		return e
	}); err != nil {
		t.Fatalf("get invoice: %v", err)
	}
	var syncCents int
	for _, l := range lines {
		if k, _ := l.Evidence["kind"].(string); k == "sync" {
			syncCents = l.USDCents
		}
	}
	if syncCents != 13 {
		t.Errorf("sync line = %d cents, want 13 (round 12.5 up, not floor 12)", syncCents)
	}
}

// TestGenerateInvoice_EstimatedVsProven: a builder with git activity gets a proven
// line; a builder with no commits/PRs gets an is_estimated line carrying
// confirmation_required (decisions P4).
func TestGenerateInvoice_EstimatedVsProven(t *testing.T) {
	database := testDB(t)
	ctx := context.Background()
	svc := New(database, &config.Config{})

	orgID, cleanup := seedOrg(t, ctx, database)
	defer cleanup()

	planFor(t, ctx, svc, "team")

	// Builder with git activity. ListMembers keys evidence lookup by member NAME,
	// and commitsByAuthor is keyed by commit author_login — so to get a "proven"
	// line we set author_login to the member's name.
	proven := "Worker One"
	addMember(t, ctx, database, orgID, "member", proven)
	// Builder with no git activity → estimated.
	addMember(t, ctx, database, orgID, "member", "Idle Two")

	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		return store.UpsertSubscription(ctx, tx, orgID, "team", "active", nil, "")
	}); err != nil {
		t.Fatalf("upsert subscription: %v", err)
	}

	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC)

	// A repo + commit authored by `proven` inside the window.
	var repoID string
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		ns := time.Now().UnixNano()
		if e := tx.QueryRow(ctx,
			`INSERT INTO repos (org_id, platform, external_id, full_name)
			 VALUES ($1,'github',$2,'acme/repo') RETURNING id`,
			orgID, fmt.Sprintf("r-%d", ns)).Scan(&repoID); e != nil {
			return e
		}
		_, e := tx.Exec(ctx,
			`INSERT INTO commits (org_id, repo_id, sha, author_login, author_email, message, committed_at)
			 VALUES ($1,$2,$3,$4,$5,'work',$6)`,
			orgID, repoID, fmt.Sprintf("sha-%d", ns), proven, "w1@ex.io", start.Add(48*time.Hour))
		return e
	}); err != nil {
		t.Fatalf("seed commit: %v", err)
	}

	inv, err := svc.GenerateInvoice(ctx, orgID, start, end)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}
	var lines []store.InvoiceLine
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		_, ls, e := store.GetInvoice(ctx, tx, orgID, inv.ID)
		lines = ls
		return e
	}); err != nil {
		t.Fatalf("get invoice: %v", err)
	}

	var sawProven, sawEstimated bool
	for _, l := range lines {
		role, _ := l.Evidence["role"].(string)
		if role == "" {
			continue // not a seat line
		}
		commits, _ := l.Evidence["commits"].(float64) // JSON numbers decode to float64
		if commits > 0 {
			sawProven = true
			if l.IsEstimated {
				t.Errorf("builder with commits flagged estimated: %s", l.Description)
			}
		} else {
			sawEstimated = true
			if !l.IsEstimated {
				t.Errorf("builder with no git activity not flagged estimated: %s", l.Description)
			}
			if cr, _ := l.Evidence["confirmation_required"].(bool); !cr {
				t.Errorf("estimated line missing confirmation_required: %+v", l.Evidence)
			}
		}
	}
	if !sawProven {
		t.Error("expected a proven (non-estimated) seat line")
	}
	if !sawEstimated {
		t.Error("expected an estimated seat line")
	}
}

// TestGenerateInvoice_TotalEqualsLineSum: the invoice header USDCents must equal
// the sum of all its line USDCents (no double-count, no drop).
func TestGenerateInvoice_TotalEqualsLineSum(t *testing.T) {
	database := testDB(t)
	ctx := context.Background()
	svc := New(database, &config.Config{})

	orgID, cleanup := seedOrg(t, ctx, database)
	defer cleanup()

	plan := planFor(t, ctx, svc, "business")
	addMember(t, ctx, database, orgID, "owner", "A")
	addMember(t, ctx, database, orgID, "member", "B")
	addMember(t, ctx, database, orgID, "stakeholder", "S")

	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		return store.UpsertSubscription(ctx, tx, orgID, "business", "active", nil, "")
	}); err != nil {
		t.Fatalf("upsert subscription: %v", err)
	}

	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 31, 23, 59, 59, 0, time.UTC)

	allowanceUSD := float64(2*plan.IncludedLLMCents) / 100
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx,
			`INSERT INTO usage_events (org_id, kind, quantity, cost_usd, occurred_at)
			 VALUES ($1,'llm_tokens',$2,$3,$4),($1,'sync',$5,$6,$4)`,
			orgID, 100.0, allowanceUSD+3.0, start.Add(time.Hour), 5.0, 0.40)
		return e
	}); err != nil {
		t.Fatalf("seed usage: %v", err)
	}

	inv, err := svc.GenerateInvoice(ctx, orgID, start, end)
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}
	var lines []store.InvoiceLine
	if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
		_, ls, e := store.GetInvoice(ctx, tx, orgID, inv.ID)
		lines = ls
		return e
	}); err != nil {
		t.Fatalf("get invoice: %v", err)
	}
	sum := 0
	for _, l := range lines {
		sum += l.USDCents
	}
	if sum != inv.USDCents {
		t.Errorf("sum(lines) = %d, invoice total = %d (must match)", sum, inv.USDCents)
	}
}
