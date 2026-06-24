package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/exo/gitstate/internal/analytics"
	"github.com/exo/gitstate/internal/billing"
	"github.com/exo/gitstate/internal/config"
	"github.com/exo/gitstate/internal/contribution"
	"github.com/exo/gitstate/internal/db"
	"github.com/exo/gitstate/internal/store"
	"github.com/jackc/pgx/v5"
)

// tools_read.go — the read tools. Each reuses an existing service/store query
// (no SQL reinvented) and returns compact JSON for the model. All reads are
// org-scoped: service methods call db.WithOrg internally, or the handler opens a
// db.WithOrg tx for direct store calls. None mutate; none return an *Action.

// rangeArgs is the shared optional {from,to} window most read tools accept.
type rangeArgs struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// parseRange turns the optional from/to args into an analytics.Filter, applying
// the service's default look-back window when absent.
func parseRange(a rangeArgs) (analytics.Filter, error) {
	return analytics.ParseFilter(analytics.FilterInput{From: a.From, To: a.To}, time.Now().UTC())
}

func rangeSchema() map[string]any {
	return objectSchema(map[string]any{
		"from": stringProp("ISO date (YYYY-MM-DD) lower bound; optional, defaults to ~9 months ago"),
		"to":   stringProp("ISO date (YYYY-MM-DD) upper bound; optional, defaults to now"),
	})
}

func unmarshalRange(args json.RawMessage) (analytics.Filter, error) {
	var a rangeArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return analytics.Filter{}, fmt.Errorf("invalid args: %w", err)
		}
	}
	return parseRange(a)
}

// ── get_analytics_summary ───────────────────────────────────────────────────

func getAnalyticsSummaryTool() Tool {
	return Tool{
		Name:        "get_analytics_summary",
		Description: "Org-wide git analytics totals and averages for a date range: total commits, repos, contributors, active days, lines added/deleted/net, and per-commit/per-contributor averages.",
		JSONSchema:  rangeSchema(),
		Handler: func(ctx context.Context, database *db.DB, orgID string, args json.RawMessage) (json.RawMessage, *Action, error) {
			f, err := unmarshalRange(args)
			if err != nil {
				return nil, nil, err
			}
			res, err := analytics.New(database).Summary(ctx, orgID, f)
			if err != nil {
				return nil, nil, err
			}
			out, _ := jsonResult(res)
			return out, nil, nil
		},
	}
}

// ── commits_over_time ───────────────────────────────────────────────────────

func commitsOverTimeTool() Tool {
	schema := rangeSchema()
	schema["properties"].(map[string]any)["bucket"] = stringProp("bucket granularity: day | week (default day)")
	return Tool{
		Name:        "commits_over_time",
		Description: "Commit counts bucketed over time (day or week) for the date range — the time series behind the activity chart.",
		JSONSchema:  schema,
		Handler: func(ctx context.Context, database *db.DB, orgID string, args json.RawMessage) (json.RawMessage, *Action, error) {
			var a struct {
				rangeArgs
				Bucket string `json:"bucket"`
			}
			if len(args) > 0 {
				if err := json.Unmarshal(args, &a); err != nil {
					return nil, nil, fmt.Errorf("invalid args: %w", err)
				}
			}
			f, err := parseRange(a.rangeArgs)
			if err != nil {
				return nil, nil, err
			}
			res, err := analytics.New(database).CommitsOverTime(ctx, orgID, f, a.Bucket)
			if err != nil {
				return nil, nil, err
			}
			out, _ := jsonResult(res)
			return out, nil, nil
		},
	}
}

// ── top_contributors ────────────────────────────────────────────────────────

func topContributorsTool() Tool {
	schema := rangeSchema()
	schema["properties"].(map[string]any)["limit"] = intProp("max contributors to return (default 10)")
	return Tool{
		Name:        "top_contributors",
		Description: "Leaderboard of top contributors by commits for the range: login, name, email, commits, additions, deletions, active days, projects, agent flag.",
		JSONSchema:  schema,
		Handler: func(ctx context.Context, database *db.DB, orgID string, args json.RawMessage) (json.RawMessage, *Action, error) {
			var a struct {
				rangeArgs
				Limit int `json:"limit"`
			}
			if len(args) > 0 {
				if err := json.Unmarshal(args, &a); err != nil {
					return nil, nil, fmt.Errorf("invalid args: %w", err)
				}
			}
			f, err := parseRange(a.rangeArgs)
			if err != nil {
				return nil, nil, err
			}
			res, err := analytics.New(database).Contributors(ctx, orgID, f)
			if err != nil {
				return nil, nil, err
			}
			limit := a.Limit
			if limit <= 0 || limit > 50 {
				limit = 10
			}
			if len(res) > limit {
				res = res[:limit]
			}
			out, _ := jsonResult(res)
			return out, nil, nil
		},
	}
}

// ── get_contribution ────────────────────────────────────────────────────────

func getContributionTool() Tool {
	return Tool{
		Name:        "get_contribution",
		Description: "The multi-dimension contribution leaderboard (composite score plus the gaming-resistant dimensions) for each org member over a period. Use this for 'who contributed most' questions grounded in outcomes, not raw commits.",
		JSONSchema:  rangeSchema(),
		Handler: func(ctx context.Context, database *db.DB, orgID string, args json.RawMessage) (json.RawMessage, *Action, error) {
			var a rangeArgs
			if len(args) > 0 {
				if err := json.Unmarshal(args, &a); err != nil {
					return nil, nil, fmt.Errorf("invalid args: %w", err)
				}
			}
			now := time.Now().UTC()
			from, to := parseDateOr(a.From, time.Time{}), parseDateOr(a.To, time.Time{})
			period := contribution.ResolvePeriod(from, to, now)
			report, err := contribution.New(database).Compute(ctx, orgID, period)
			if err != nil {
				return nil, nil, err
			}
			out, _ := jsonResult(report)
			return out, nil, nil
		},
	}
}

// parseDateOr parses YYYY-MM-DD (or RFC3339), returning fallback on empty/error.
func parseDateOr(s string, fallback time.Time) time.Time {
	if s == "" {
		return fallback
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC()
	}
	return fallback
}

// ── cycle_time_summary ──────────────────────────────────────────────────────

func cycleTimeSummaryTool() Tool {
	return Tool{
		Name:        "cycle_time_summary",
		Description: "Pull-request cycle-time summary for the range: count of PRs with lead/review times plus median (p50) and p90 lead time in hours.",
		JSONSchema:  rangeSchema(),
		Handler: func(ctx context.Context, database *db.DB, orgID string, args json.RawMessage) (json.RawMessage, *Action, error) {
			var a rangeArgs
			if len(args) > 0 {
				if err := json.Unmarshal(args, &a); err != nil {
					return nil, nil, fmt.Errorf("invalid args: %w", err)
				}
			}
			now := time.Now().UTC()
			win := store.CycleTimeFilter{
				From: parseDateOr(a.From, now.AddDate(0, 0, -90)),
				To:   parseDateOr(a.To, now),
			}
			var rows []*store.CycleTime
			if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
				var e error
				rows, e = store.ListCycleTimes(ctx, tx, orgID, win)
				return e
			}); err != nil {
				return nil, nil, err
			}
			out, _ := jsonResult(summariseCycleTimes(rows, win))
			return out, nil, nil
		},
	}
}

// cycleTimeSummaryResult is the compact shape the model reasons over.
type cycleTimeSummaryResult struct {
	From            string   `json:"from"`
	To              string   `json:"to"`
	PRs             int      `json:"prs"`
	WithLeadTime    int      `json:"withLeadTime"`
	LeadP50Hours    *float64 `json:"leadP50Hours"`
	LeadP90Hours    *float64 `json:"leadP90Hours"`
	ReviewP50Hours  *float64 `json:"reviewP50Hours"`
}

func summariseCycleTimes(rows []*store.CycleTime, win store.CycleTimeFilter) cycleTimeSummaryResult {
	res := cycleTimeSummaryResult{
		From: win.From.Format("2006-01-02"),
		To:   win.To.Format("2006-01-02"),
		PRs:  len(rows),
	}
	var lead, review []float64
	for _, ct := range rows {
		if ct.LeadTimeSecs != nil {
			lead = append(lead, float64(*ct.LeadTimeSecs)/3600.0)
		}
		if ct.ReviewSecs != nil {
			review = append(review, float64(*ct.ReviewSecs)/3600.0)
		}
	}
	res.WithLeadTime = len(lead)
	if len(lead) > 0 {
		p50 := store.Percentile(lead, 0.5)
		p90 := store.Percentile(lead, 0.9)
		res.LeadP50Hours = &p50
		res.LeadP90Hours = &p90
	}
	if len(review) > 0 {
		p50 := store.Percentile(review, 0.5)
		res.ReviewP50Hours = &p50
	}
	return res
}

// ── list_repos ──────────────────────────────────────────────────────────────

func listReposTool() Tool {
	return Tool{
		Name:        "list_repos",
		Description: "List the org's connected repositories: id, full name (owner/repo), platform, default branch, last synced/analyzed timestamps.",
		JSONSchema:  objectSchema(map[string]any{}),
		Handler: func(ctx context.Context, database *db.DB, orgID string, _ json.RawMessage) (json.RawMessage, *Action, error) {
			var repos []store.Repo
			if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
				var e error
				repos, e = store.ListRepos(ctx, tx, orgID)
				return e
			}); err != nil {
				return nil, nil, err
			}
			type repoView struct {
				ID            string     `json:"id"`
				FullName      string     `json:"fullName"`
				Platform      string     `json:"platform"`
				DefaultBranch string     `json:"defaultBranch"`
				LastSyncedAt  *time.Time `json:"lastSyncedAt"`
				LastAnalyzedAt *time.Time `json:"lastAnalyzedAt"`
			}
			views := make([]repoView, 0, len(repos))
			for _, r := range repos {
				views = append(views, repoView{
					ID: r.ID, FullName: r.FullName, Platform: r.Platform,
					DefaultBranch: r.DefaultBranch, LastSyncedAt: r.LastSyncedAt,
					LastAnalyzedAt: r.LastAnalyzedAt,
				})
			}
			out, _ := jsonResult(views)
			return out, nil, nil
		},
	}
}

// ── repo_stats ──────────────────────────────────────────────────────────────

func repoStatsTool() Tool {
	return Tool{
		Name:        "repo_stats",
		Description: "Per-repository activity stats for the range: commits, contributors, additions, deletions, last activity. Sorted by the analytics service.",
		JSONSchema:  rangeSchema(),
		Handler: func(ctx context.Context, database *db.DB, orgID string, args json.RawMessage) (json.RawMessage, *Action, error) {
			f, err := unmarshalRange(args)
			if err != nil {
				return nil, nil, err
			}
			res, err := analytics.New(database).RepoStats(ctx, orgID, f)
			if err != nil {
				return nil, nil, err
			}
			out, _ := jsonResult(res)
			return out, nil, nil
		},
	}
}

// ── eng_health ──────────────────────────────────────────────────────────────

func engHealthTool() Tool {
	return Tool{
		Name:        "eng_health",
		Description: "Engineering-health signals for the last ~90 days (or a given range): DORA-style change-failure rate (SZZ-derived), lead-time p50/p90, deploy frequency, review health, and bus-factor / truck-factor with single-owner risk areas.",
		JSONSchema:  rangeSchema(),
		Handler: func(ctx context.Context, database *db.DB, orgID string, args json.RawMessage) (json.RawMessage, *Action, error) {
			var a rangeArgs
			if len(args) > 0 {
				if err := json.Unmarshal(args, &a); err != nil {
					return nil, nil, fmt.Errorf("invalid args: %w", err)
				}
			}
			now := time.Now().UTC()
			win := store.EngHealthWindow{
				From: parseDateOr(a.From, now.AddDate(0, 0, -90)),
				To:   parseDateOr(a.To, now),
			}
			var (
				lead     store.LeadTimeStats
				delivery store.DeliveryCounts
				review   store.ReviewHealth
				bus      store.BusFactor
			)
			if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
				var e error
				if lead, e = store.LeadTimeSamples(ctx, tx, orgID, win); e != nil {
					return e
				}
				if delivery, e = store.DeliverySignals(ctx, tx, orgID, win); e != nil {
					return e
				}
				if review, e = store.ReviewSignals(ctx, tx, orgID, win); e != nil {
					return e
				}
				bus, e = store.BusFactorAnalysis(ctx, tx, orgID, 2, 60)
				return e
			}); err != nil {
				return nil, nil, err
			}
			out, _ := jsonResult(summariseEngHealth(win, lead, delivery, review, bus))
			return out, nil, nil
		},
	}
}

type engHealthSummary struct {
	From              string   `json:"from"`
	To                string   `json:"to"`
	MergedPRs         int      `json:"mergedPrs"`
	ChangeFailureRate *float64 `json:"changeFailureRate"`
	BugFixChanges     int      `json:"bugFixChanges"`
	LeadP50Hours      *float64 `json:"leadP50Hours"`
	LeadP90Hours      *float64 `json:"leadP90Hours"`
	MergedWithoutReview int    `json:"mergedWithoutReview"`
	TruckFactor       int      `json:"truckFactor"`
	SingleOwnerAreas  []string `json:"singleOwnerAreas"`
}

func summariseEngHealth(win store.EngHealthWindow, lead store.LeadTimeStats, d store.DeliveryCounts, review store.ReviewHealth, bus store.BusFactor) engHealthSummary {
	s := engHealthSummary{
		From:                win.From.Format("2006-01-02"),
		To:                  win.To.Format("2006-01-02"),
		MergedPRs:           d.MergedPRs,
		BugFixChanges:       d.BugFixes,
		MergedWithoutReview: review.MergedWithoutReview,
		TruckFactor:         bus.TruckFactor,
	}
	if d.MergedPRs > 0 {
		cfr := float64(d.BugFixes) / float64(d.MergedPRs)
		if cfr > 1 {
			cfr = 1
		}
		s.ChangeFailureRate = &cfr
	}
	if len(lead.SampleHours) > 0 {
		p50 := store.Percentile(lead.SampleHours, 0.5)
		p90 := store.Percentile(lead.SampleHours, 0.9)
		s.LeadP50Hours = &p50
		s.LeadP90Hours = &p90
	}
	for _, a := range bus.Areas {
		if a.OwnershipPct >= 0.8 {
			s.SingleOwnerAreas = append(s.SingleOwnerAreas, a.Area)
		}
	}
	return s
}

// ── list_invoices ───────────────────────────────────────────────────────────

func listInvoicesTool() Tool {
	return Tool{
		Name:        "list_invoices",
		Description: "List the org's client invoices: number, status, client/project, period, currency, total cents, issued date.",
		JSONSchema:  objectSchema(map[string]any{}),
		Handler: func(ctx context.Context, database *db.DB, orgID string, _ json.RawMessage) (json.RawMessage, *Action, error) {
			var invs []*store.ClientInvoice
			if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
				var e error
				invs, e = store.ListClientInvoices(ctx, tx, orgID)
				return e
			}); err != nil {
				return nil, nil, err
			}
			out, _ := jsonResult(invs)
			return out, nil, nil
		},
	}
}

// ── invoice_summary ─────────────────────────────────────────────────────────

func invoiceSummaryTool() Tool {
	return Tool{
		Name:        "invoice_summary",
		Description: "Aggregate summary of the org's client invoices: count and total billed cents broken down by status (draft/sent/paid/...), plus the grand total.",
		JSONSchema:  objectSchema(map[string]any{}),
		Handler: func(ctx context.Context, database *db.DB, orgID string, _ json.RawMessage) (json.RawMessage, *Action, error) {
			var invs []*store.ClientInvoice
			if err := database.WithOrg(ctx, orgID, func(tx pgx.Tx) error {
				var e error
				invs, e = store.ListClientInvoices(ctx, tx, orgID)
				return e
			}); err != nil {
				return nil, nil, err
			}
			type bucket struct {
				Count      int `json:"count"`
				TotalCents int `json:"totalCents"`
			}
			byStatus := map[string]*bucket{}
			grand := 0
			for _, inv := range invs {
				b := byStatus[inv.Status]
				if b == nil {
					b = &bucket{}
					byStatus[inv.Status] = b
				}
				b.Count++
				b.TotalCents += inv.TotalCents
				grand += inv.TotalCents
			}
			out, _ := jsonResult(map[string]any{
				"invoices":        len(invs),
				"byStatus":        byStatus,
				"grandTotalCents": grand,
			})
			return out, nil, nil
		},
	}
}

// ── current_usage ───────────────────────────────────────────────────────────

func currentUsageTool() Tool {
	return Tool{
		Name:        "current_usage",
		Description: "Current billing-period metered usage rollup for the org: quantity and cost (USD) per usage kind (e.g. seats, llm tokens). Use for 'how much have we used / what's our usage this month'.",
		JSONSchema:  objectSchema(map[string]any{}),
		Handler: func(ctx context.Context, database *db.DB, orgID string, _ json.RawMessage) (json.RawMessage, *Action, error) {
			rollup, err := billing.New(database, &config.Config{}).CurrentUsage(ctx, orgID)
			if err != nil {
				return nil, nil, err
			}
			out, _ := jsonResult(rollup)
			return out, nil, nil
		},
	}
}

// ── wallet_balance ──────────────────────────────────────────────────────────

func walletBalanceTool() Tool {
	return Tool{
		Name:        "wallet_balance",
		Description: "The org's prepaid wallet balance in cents (and the currency). Use for 'what's our balance / how much credit do we have left'.",
		JSONSchema:  objectSchema(map[string]any{}),
		Handler: func(ctx context.Context, database *db.DB, orgID string, _ json.RawMessage) (json.RawMessage, *Action, error) {
			cents, err := billing.New(database, &config.Config{}).WalletBalance(ctx, orgID)
			if err != nil {
				return nil, nil, err
			}
			out, _ := jsonResult(map[string]any{"balanceCents": cents})
			return out, nil, nil
		},
	}
}
