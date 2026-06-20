// cmd/billsim/model.go — billing viability math for gitstate.
//
// Pricing model (v2): per-BUILDER tiers with included LLM credits + overage, BYOK
// default, and a minimized free tier. Stakeholders are always free (decisions P6).
//
// The key change vs v1: LLM is no longer a pure subsidized COST. It is one of:
//   - BYOK         → customer brings their own provider key; $0 cost to us.
//   - Included     → the subscription bundles a per-builder credit allowance; we
//                    absorb the provider cost up to that allowance (already priced in).
//   - Overage      → usage beyond the allowance is billed at provider-cost × markup
//                    (via the LiteLLM gateway), so heavy usage is REVENUE, not a hole.
// The free tier is BYOK-only and scale-to-zero, so it has near-zero COGS — which is
// what lets the model break even almost immediately instead of drowning in free drag.
//
// All prices are USD; customers are charged in ZAR at a captured FX rate, through
// Paystack (% + flat fee). This is a steady-state (per-month) model, not a time series.

package main

// Tier is a per-builder pricing tier.
type Tier struct {
	Key            string
	Name           string
	PerBuilderUSD  float64 // monthly price per BILLABLE builder (stakeholders free)
	IncludedLLMUSD float64 // included managed-LLM allowance per builder/mo, at OUR provider cost
	OverageMarkup  float64 // markup on managed-LLM usage beyond the allowance (e.g. 1.30 = +30%)
}

// DefaultTiers — competitive per-builder ladder. Enterprise (custom/self-host/BYOK)
// is excluded from the simulation.
// NOTE on the LLM model: we do NOT show clients a markup. Managed AI is presented
// as "run the model at its standard rate" — the displayed price ≈ provider list.
// Our margin comes from the BULK / committed-use discount (we buy at ~0.65× list,
// see LLMVolumeDiscount) plus a silent ≤5% gateway buffer. So the client-facing
// "overage markup" is just 1.05 (effectively invisible), and the real profit is
// the wholesale spread — cheapest for the customer AND most profitable for us.
var DefaultTiers = []Tier{
	{"free", "Free", 0, 0, 0},              // BYOK-only, ≤2 builders, scale-to-zero
	{"team", "Team", 6, 3, 1.05},          // $6 managed (AI incl) / $3 BYOK; AI metered at ~list
	{"business", "Business", 14, 6, 1.05}, // $14 managed / $8 BYOK, SSO/audit; AI metered at ~list
}

// SimParams holds all tunable inputs (defaults set in main.go via flags).
type SimParams struct {
	TotalOrgs  int     // organisations simulated
	ConvPct    float64 // % of orgs on a PAID tier (after churn settles)
	ChurnPctMo float64 // monthly churn % of paid orgs

	FXRate         float64 // USD → ZAR
	PaystackPctFee float64 // % fee on ZAR amount
	PaystackFlat   float64 // flat ZAR fee per charge

	// Paid-tier mix (aligns with DefaultTiers[1:] → Team, Business). Normalised internally.
	PaidMix [2]float64

	// Average billable builders per paid org, per tier (Team, Business).
	BuildersPerOrg [2]float64

	// BYOK adoption: fraction of managed-eligible builders that bring their own key
	// (→ $0 LLM cost AND $0 LLM revenue for us).
	BYOKFrac float64

	// Average managed-LLM usage per non-BYOK builder, in USD at OUR list/charge rate (per tier).
	LLMUsagePerBuilder [2]float64

	// Our actual LLM cost is a fraction of the charged rate, thanks to committed-use /
	// volume discounts from the provider (e.g. 0.65 = we pay 65% of what we charge).
	// This is what makes managed LLM a PROFIT CENTER and lets us price BELOW the retail
	// rate a customer would pay running their own key (BYOK / self-host), while still
	// making money. BYOK is therefore a small enterprise opt-out, not the default.
	LLMVolumeDiscount float64

	// Retail $/unit a customer would pay buying tokens directly (for the self-host
	// comparison). Our charged rate is below this; our cost is below our charge.
	LLMRetailMultiple float64 // retail = charged × this (e.g. 1.25)

	// Infra (scale-to-zero compute + DB), USD/mo.
	InfraFreeUSD     float64 // dormant free org
	InfraPaidBaseUSD float64 // per paid org
	InfraPerBuilder  float64 // per builder

	// Support + sync, USD/mo.
	SupportFreeUSD    float64
	SupportPerBuilder float64
	SyncPerBuilder    float64
}

// TierResult holds computed metrics for one tier.
type TierResult struct {
	Tier         Tier
	Orgs         int
	Builders     float64
	SubRevUSD    float64 // subscription revenue (USD, gross)
	OverageRev   float64 // managed-LLM overage revenue (USD, gross)
	GrossRevUSD  float64 // sub + overage
	FeesUSD      float64 // Paystack fees (USD-equivalent)
	NetRevUSD    float64 // gross − fees
	LLMCostUSD   float64 // managed-LLM provider cost we absorb
	InfraUSD     float64
	OtherCOGSUSD float64 // support + sync
	COGSUSD      float64 // LLM + infra + other
	Contribution float64 // NetRev − COGS
	MarginPct    float64
}

// SimResult is the full simulation output.
type SimResult struct {
	Tiers          []TierResult
	TotalOrgs      int
	PaidOrgs       int
	FreeOrgs       int
	FreeDragUSD    float64 // total free-tier COGS (the only real drag)
	TotalNetRev    float64
	TotalCOGS      float64
	Contribution   float64
	MarginPct      float64
	PerPaidContrib float64 // blended contribution per paid org
	BreakEvenPaid  int     // paid orgs needed for total contribution ≥ 0 (1 = from first customer)
}

func paystackFeeUSD(grossUSD float64, p SimParams) float64 {
	if grossUSD <= 0 {
		return 0
	}
	zar := grossUSD * p.FXRate
	feeZAR := zar*(p.PaystackPctFee/100) + p.PaystackFlat
	return feeZAR / p.FXRate
}

// paidOrgResult computes one paid tier's economics for n orgs.
func paidOrgResult(t Tier, tierIdx, n int, p SimParams) TierResult {
	builders := p.BuildersPerOrg[tierIdx]
	managedBuilders := builders * (1 - p.BYOKFrac)
	byokBuilders := builders * p.BYOKFrac

	// Revenue. BYOK builders pay the base MINUS the included-LLM value (they don't
	// pay for managed AI they don't use), so they're cheaper for the customer — but
	// the included LLM was never our margin, so the subscription stays profitable.
	subPerOrg := managedBuilders*t.PerBuilderUSD + byokBuilders*(t.PerBuilderUSD-t.IncludedLLMUSD)
	usage := p.LLMUsagePerBuilder[tierIdx] // provider $ per managed builder
	overagePerBuilder := usage - t.IncludedLLMUSD
	if overagePerBuilder < 0 {
		overagePerBuilder = 0
	}
	overagePerOrg := managedBuilders * overagePerBuilder * t.OverageMarkup
	grossPerOrg := subPerOrg + overagePerOrg

	// Costs. Our LLM cost is the volume-discounted fraction of usage (we buy cheaper
	// than retail), so the included allowance AND overage both carry margin.
	disc := p.LLMVolumeDiscount
	if disc <= 0 {
		disc = 1
	}
	llmCostPerOrg := managedBuilders * usage * disc
	infraPerOrg := p.InfraPaidBaseUSD + builders*p.InfraPerBuilder
	otherPerOrg := builders * (p.SupportPerBuilder + p.SyncPerBuilder)

	feePerOrg := paystackFeeUSD(grossPerOrg, p)
	netPerOrg := grossPerOrg - feePerOrg
	cogsPerOrg := llmCostPerOrg + infraPerOrg + otherPerOrg
	contribPerOrg := netPerOrg - cogsPerOrg

	nf := float64(n)
	margin := 0.0
	if netPerOrg > 0 {
		margin = (contribPerOrg / netPerOrg) * 100
	}
	return TierResult{
		Tier:         t,
		Orgs:         n,
		Builders:     builders * nf,
		SubRevUSD:    subPerOrg * nf,
		OverageRev:   overagePerOrg * nf,
		GrossRevUSD:  grossPerOrg * nf,
		FeesUSD:      feePerOrg * nf,
		NetRevUSD:    netPerOrg * nf,
		LLMCostUSD:   llmCostPerOrg * nf,
		InfraUSD:     infraPerOrg * nf,
		OtherCOGSUSD: otherPerOrg * nf,
		COGSUSD:      cogsPerOrg * nf,
		Contribution: contribPerOrg * nf,
		MarginPct:    margin,
	}
}

// Simulate runs the full steady-state model.
func Simulate(p SimParams) SimResult {
	convFrac := p.ConvPct / 100
	churnFrac := p.ChurnPctMo / 100
	paidFrac := convFrac * (1 - churnFrac)
	paidOrgs := int(float64(p.TotalOrgs) * paidFrac)
	freeOrgs := p.TotalOrgs - paidOrgs

	var mixSum float64
	for _, m := range p.PaidMix {
		mixSum += m
	}
	if mixSum <= 0 {
		mixSum = 1
	}

	results := make([]TierResult, len(DefaultTiers))

	// Free tier: BYOK-only, scale-to-zero → tiny COGS, no LLM cost, no revenue.
	freeCostPerOrg := p.InfraFreeUSD + p.SupportFreeUSD
	results[0] = TierResult{
		Tier:         DefaultTiers[0],
		Orgs:         freeOrgs,
		InfraUSD:     p.InfraFreeUSD * float64(freeOrgs),
		OtherCOGSUSD: p.SupportFreeUSD * float64(freeOrgs),
		COGSUSD:      freeCostPerOrg * float64(freeOrgs),
		Contribution: -freeCostPerOrg * float64(freeOrgs),
	}

	var totalNetRev, totalCOGS, totalContrib float64
	totalCOGS += results[0].COGSUSD
	totalContrib += results[0].Contribution

	var blendedContribPerPaid float64
	for i, t := range DefaultTiers[1:] {
		weight := p.PaidMix[i] / mixSum
		n := int(float64(paidOrgs) * weight)
		r := paidOrgResult(t, i, n, p)
		results[i+1] = r
		totalNetRev += r.NetRevUSD
		totalCOGS += r.COGSUSD
		totalContrib += r.Contribution

		unit := paidOrgResult(t, i, 1, p) // per-org contribution
		blendedContribPerPaid += weight * unit.Contribution
	}

	margin := 0.0
	if totalNetRev > 0 {
		margin = (totalContrib / totalNetRev) * 100
	}

	// Break-even: total contribution(N) = N·[paidFrac·perPaid − (1−paidFrac)·freeCost].
	// If positive, the model is profitable from the first paying customer.
	breakEven := -1
	freePerPaid := 0.0
	if paidFrac > 0 {
		freePerPaid = (1 - paidFrac) / paidFrac
	}
	netPerPaid := blendedContribPerPaid - freePerPaid*freeCostPerOrg
	if blendedContribPerPaid > 0 && netPerPaid > 0 {
		breakEven = 1
	}

	return SimResult{
		Tiers:          results,
		TotalOrgs:      p.TotalOrgs,
		PaidOrgs:       paidOrgs,
		FreeOrgs:       freeOrgs,
		FreeDragUSD:    results[0].COGSUSD,
		TotalNetRev:    totalNetRev,
		TotalCOGS:      totalCOGS,
		Contribution:   totalContrib,
		MarginPct:      margin,
		PerPaidContrib: blendedContribPerPaid,
		BreakEvenPaid:  breakEven,
	}
}
