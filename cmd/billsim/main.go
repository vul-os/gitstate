// cmd/billsim — gitstate billing viability simulator (v2: tiers + overages + BYOK).
//
// Usage:
//
//	go run ./cmd/billsim [flags]
//
// Prints the tier ladder, then a profitability table across customer-base scenarios,
// and the break-even point. The model treats LLM as bounded-cost/revenue (included
// allowance + overage markup + BYOK), with a minimized BYOK-only free tier.
//
// Flags:
//
//	-orgs N        Total orgs for the single-run table (default 1000)
//	-conv N        % of orgs on a paid tier (default 6)
//	-churn N       Monthly paid churn % (default 3)
//	-fx N          USD→ZAR FX rate (default 18.5)
//	-byok N        BYOK adoption fraction among managed builders 0–1 (default 0.35)
//	-llm-team N    Managed LLM $/builder/mo on Team (provider cost) (default 5)
//	-llm-biz  N    Managed LLM $/builder/mo on Business (provider cost) (default 14)
//	-scenarios     Run the 50/100/500/1k/5k sweep (default true)

package main

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
)

func main() {
	orgs := flag.Int("orgs", 1000, "total organisations to simulate")
	conv := flag.Float64("conv", 6.0, "% of orgs on a paid tier (0–100)")
	churn := flag.Float64("churn", 3.0, "monthly paid churn % (0–100)")
	fx := flag.Float64("fx", 18.5, "USD→ZAR FX rate")
	byok := flag.Float64("byok", 0.10, "BYOK adoption fraction (small enterprise opt-out; managed is default)")
	llmTeam := flag.Float64("llm-team", 5.0, "managed LLM $/builder/mo on Team (provider cost)")
	llmBiz := flag.Float64("llm-biz", 14.0, "managed LLM $/builder/mo on Business (provider cost)")
	scenarios := flag.Bool("scenarios", true, "run the 50/100/500/1k/5k scenario sweep")
	flag.Parse()

	base := SimParams{
		TotalOrgs:  *orgs,
		ConvPct:    *conv,
		ChurnPctMo: *churn,

		FXRate:         *fx,
		PaystackPctFee: 2.9,
		PaystackFlat:   1.50,

		// Paid mix: most paying orgs are Team; a minority are Business.
		PaidMix:        [2]float64{0.78, 0.22},
		BuildersPerOrg: [2]float64{4, 12}, // avg billable builders: Team 4, Business 12

		BYOKFrac:           *byok,
		LLMUsagePerBuilder: [2]float64{*llmTeam, *llmBiz},
		LLMVolumeDiscount:  0.65, // we pay ~65% of the charged rate (committed-use discount)
		LLMRetailMultiple:  1.25, // retail (what a BYOK customer pays direct) is ~25% above our charge

		// Infra — scale-to-zero containers + serverless Postgres. Free orgs are dormant.
		InfraFreeUSD:     0.15,
		InfraPaidBaseUSD: 0.50,
		InfraPerBuilder:  0.08,

		SupportFreeUSD:    0.03,
		SupportPerBuilder: 0.30,
		SyncPerBuilder:    0.05,
	}

	printLadder(base)

	if *scenarios {
		for _, n := range []int{50, 100, 500, 1_000, 5_000} {
			p := base
			p.TotalOrgs = n
			printTable(Simulate(p), p, n)
		}
	} else {
		printTable(Simulate(base), base, *orgs)
	}
}

func printLadder(p SimParams) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "\n=== Tier ladder (per builder/mo, USD — stakeholders always free) ===")
	fmt.Fprintln(w, "Tier\tPrice/builder\tIncluded LLM/builder\tOverage\tNotes")
	fmt.Fprintln(w, "----\t-------------\t--------------------\t-------\t-----")
	fmt.Fprintln(w, "Free\t$0\t— (BYOK only)\t—\t≤2 builders, scale-to-zero, community")
	fmt.Fprintln(w, "Team\t$6 / BYOK $3\t$3\tat list (no markup)\tunlimited stakeholders, GitHub+GitLab, BYOK option")
	fmt.Fprintln(w, "Business\t$14 / BYOK $8\t$6\tat list (no markup)\t+ SSO, audit, priority, advanced reports")
	fmt.Fprintln(w, "Enterprise\tcustom\tBYOK / unlimited\t—\tself-host, air-gap, SLA")
	w.Flush()
	fmt.Printf("  Competitive note: Linear/Jira charge ~$8–14 per *seat*; gitstate charges per *builder*\n")
	fmt.Printf("  with stakeholders free — so a 6-builder / 20-stakeholder team pays for 6, not 26.\n")
	disc := p.LLMVolumeDiscount
	if disc <= 0 {
		disc = 1
	}
	fmt.Printf("  Managed LLM is the default & a PROFIT CENTER: we charge ~%.0f%% below retail (cheaper than\n",
		(1-1/p.LLMRetailMultiple)*100)
	fmt.Printf("  running your own key) yet our cost is ~%.0f%% of what we charge (volume discount) → ~%.0f%% LLM margin.\n",
		disc*100, (1-disc)*100)
	fmt.Printf("  BYOK stays as an enterprise opt-out (%.0f%% assumed), not the revenue-zeroing default.\n", p.BYOKFrac*100)
}

func printTable(r SimResult, p SimParams, totalOrgs int) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "\n=== %d orgs (conv %.0f%% | churn %.0f%%/mo | BYOK %.0f%% | FX %.1f) ===\n",
		totalOrgs, p.ConvPct, p.ChurnPctMo, p.BYOKFrac*100, p.FXRate)
	fmt.Fprintln(w, "Tier\tOrgs\tSub $\tOverage $\tNet rev $\tLLM cost $\tInfra+ $\tContribution $\tMargin")
	fmt.Fprintln(w, "----\t----\t-----\t---------\t---------\t----------\t--------\t--------------\t------")
	for _, t := range r.Tiers {
		margin := "  —"
		if t.Tier.PerBuilderUSD > 0 && t.NetRevUSD > 0 {
			margin = fmt.Sprintf("%+.0f%%", t.MarginPct)
		}
		fmt.Fprintf(w, "%s\t%d\t$%.0f\t$%.0f\t$%.0f\t$%.0f\t$%.0f\t$%+.0f\t%s\n",
			t.Tier.Name, t.Orgs, t.SubRevUSD, t.OverageRev, t.NetRevUSD,
			t.LLMCostUSD, t.InfraUSD+t.OtherCOGSUSD, t.Contribution, margin)
	}
	fmt.Fprintln(w, "----\t----\t-----\t---------\t---------\t----------\t--------\t--------------\t------")
	fmt.Fprintf(w, "TOTAL\t%d\t—\t—\t$%.0f\t—\t—\t$%+.0f\t%+.0f%%\n",
		r.TotalOrgs, r.TotalNetRev, r.Contribution, r.MarginPct)
	w.Flush()

	status := "PROFITABLE"
	if r.Contribution < 0 {
		status = "LOSS-MAKING"
	}
	fmt.Printf("  Net revenue: $%.0f/mo  ·  Contribution: $%+.0f/mo (%.0f%%)  ·  %s\n",
		r.TotalNetRev, r.Contribution, r.MarginPct, status)
	fmt.Printf("  Free drag: $%.0f/mo over %d free orgs ($%.2f each)  ·  Contribution per paid org: $%.2f\n",
		r.FreeDragUSD, r.FreeOrgs, safeDiv(r.FreeDragUSD, r.FreeOrgs), r.PerPaidContrib)
	if r.BreakEvenPaid == 1 {
		fmt.Printf("  Break-even: from the FIRST paying customer (per-paid contribution > free drag it carries).\n\n")
	} else {
		fmt.Printf("  Break-even: ∞ — per-paid contribution doesn't cover free drag; raise price or cut free LLM/infra.\n\n")
	}
}

func safeDiv(a float64, b int) float64 {
	if b == 0 {
		return 0
	}
	return a / float64(b)
}
