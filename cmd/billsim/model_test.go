// cmd/billsim/model_test.go — table-driven tests for the per-builder billing model.
//
// These tests are pure (no DB, no network) and assert the billing POLICY the rest
// of the stack must stay consistent with:
//   - the plan ladder (Team $6 / BYOK $3 / incl $3; Business $14 / BYOK $8 / incl $6;
//     overage markup 1.00),
//   - BYOK = managed − included-LLM, for both paid tiers,
//   - per-builder × builders subscription revenue (the MRR primitive),
//   - included-LLM allowance subtraction before overage,
//   - overage at markup 1.00 == list (no visible markup),
//   - the profitability sweep stays strictly > 0 contribution at the default mix,
//   - the volume-discount spread covers COGS at and beyond the allowance at each tier.
package main

import (
	"math"
	"testing"
)

const eps = 1e-9

func approx(a, b float64) bool { return math.Abs(a-b) <= 1e-6 }

// defaultParams mirrors the base SimParams constructed in main.go so the tests
// exercise the shipped defaults rather than an invented configuration.
func defaultParams() SimParams {
	return SimParams{
		TotalOrgs:  1000,
		ConvPct:    6.0,
		ChurnPctMo: 3.0,

		FXRate:         18.5,
		PaystackPctFee: 2.9,
		PaystackFlat:   1.50,

		PaidMix:        [2]float64{0.78, 0.22},
		BuildersPerOrg: [2]float64{4, 12},

		BYOKFrac:           0.10,
		LLMUsagePerBuilder: [2]float64{5.0, 14.0},
		LLMVolumeDiscount:  0.65,
		LLMRetailMultiple:  1.25,

		InfraFreeUSD:     0.15,
		InfraPaidBaseUSD: 0.50,
		InfraPerBuilder:  0.08,

		SupportFreeUSD:    0.03,
		SupportPerBuilder: 0.30,
		SyncPerBuilder:    0.05,
	}
}

// ── Plan ladder shape ─────────────────────────────────────────────────────────

func TestDefaultTiers_LadderMatchesPolicy(t *testing.T) {
	// The canonical ladder the DB migration (016), billsim, and /api/plans must agree on.
	want := []struct {
		key            string
		perBuilder     float64
		includedLLM    float64
		overageMarkup  float64
		byokDerivation float64 // perBuilder - includedLLM
	}{
		{"free", 0, 0, 0, 0},
		{"team", 6, 3, 1.00, 3},
		{"business", 14, 6, 1.00, 8},
	}
	if len(DefaultTiers) != len(want) {
		t.Fatalf("DefaultTiers len = %d, want %d", len(DefaultTiers), len(want))
	}
	for i, w := range want {
		got := DefaultTiers[i]
		if got.Key != w.key {
			t.Errorf("tier[%d] key = %q, want %q", i, got.Key, w.key)
		}
		if !approx(got.PerBuilderUSD, w.perBuilder) {
			t.Errorf("%s PerBuilderUSD = %v, want %v", w.key, got.PerBuilderUSD, w.perBuilder)
		}
		if !approx(got.IncludedLLMUSD, w.includedLLM) {
			t.Errorf("%s IncludedLLMUSD = %v, want %v", w.key, got.IncludedLLMUSD, w.includedLLM)
		}
		if !approx(got.OverageMarkup, w.overageMarkup) {
			t.Errorf("%s OverageMarkup = %v, want %v", w.key, got.OverageMarkup, w.overageMarkup)
		}
		// BYOK = managed base − included-LLM (the price a BYOK builder pays).
		if byok := got.PerBuilderUSD - got.IncludedLLMUSD; !approx(byok, w.byokDerivation) {
			t.Errorf("%s BYOK (base-included) = %v, want %v", w.key, byok, w.byokDerivation)
		}
	}
}

func TestOverageMarkup_IsExactlyOne_NoVisibleMarkup(t *testing.T) {
	for _, tr := range DefaultTiers[1:] { // skip free (markup 0)
		if tr.OverageMarkup != 1.00 {
			t.Errorf("%s overage markup = %v, want exactly 1.00 (no visible markup)", tr.Key, tr.OverageMarkup)
		}
	}
}

// ── BYOK derivation & subscription revenue (the MRR primitive) ────────────────

func TestSubscriptionRevenue_PerBuilderTimesBuilders(t *testing.T) {
	p := defaultParams()
	p.BYOKFrac = 0 // all managed → sub = builders × perBuilder exactly

	// Team: 4 builders × $6 = $24.
	team := paidOrgResult(DefaultTiers[1], 0, 1, p)
	if !approx(team.SubRevUSD, 4*6) {
		t.Errorf("Team sub/org = %v, want %v", team.SubRevUSD, 4*6.0)
	}
	// Business: 12 builders × $14 = $168.
	biz := paidOrgResult(DefaultTiers[2], 1, 1, p)
	if !approx(biz.SubRevUSD, 12*14) {
		t.Errorf("Business sub/org = %v, want %v", biz.SubRevUSD, 12*14.0)
	}

	// Scaling: n orgs multiplies linearly.
	team10 := paidOrgResult(DefaultTiers[1], 0, 10, p)
	if !approx(team10.SubRevUSD, 10*team.SubRevUSD) {
		t.Errorf("Team sub for 10 orgs = %v, want %v", team10.SubRevUSD, 10*team.SubRevUSD)
	}
}

func TestSubscriptionRevenue_BYOKBuildersPayBaseMinusIncluded(t *testing.T) {
	p := defaultParams()
	p.BYOKFrac = 1.0 // all BYOK
	// Team: 4 builders × ($6 − $3) = $12.
	team := paidOrgResult(DefaultTiers[1], 0, 1, p)
	if !approx(team.SubRevUSD, 4*(6-3)) {
		t.Errorf("all-BYOK Team sub = %v, want %v", team.SubRevUSD, 4*3.0)
	}
	// All-BYOK → no managed builders → zero LLM cost and zero overage.
	if !approx(team.LLMCostUSD, 0) {
		t.Errorf("all-BYOK Team LLM cost = %v, want 0", team.LLMCostUSD)
	}
	if !approx(team.OverageRev, 0) {
		t.Errorf("all-BYOK Team overage = %v, want 0", team.OverageRev)
	}
}

func TestSubscriptionRevenue_MixedBYOK(t *testing.T) {
	p := defaultParams()
	p.BYOKFrac = 0.5
	// Team 4 builders: 2 managed × $6 + 2 BYOK × $3 = $12 + $6 = $18.
	team := paidOrgResult(DefaultTiers[1], 0, 1, p)
	if !approx(team.SubRevUSD, 2*6+2*3) {
		t.Errorf("50%% BYOK Team sub = %v, want %v", team.SubRevUSD, 18.0)
	}
}

// ── Included-LLM allowance subtraction & overage at list ──────────────────────

func TestOverage_AllowanceSubtractedBeforeMarkup(t *testing.T) {
	p := defaultParams()
	p.BYOKFrac = 0

	// Team: usage $5/builder, allowance $3 → overage $2/builder × markup 1.0 × 4 builders = $8.
	team := paidOrgResult(DefaultTiers[1], 0, 1, p)
	if !approx(team.OverageRev, 4*(5-3)*1.0) {
		t.Errorf("Team overage = %v, want %v", team.OverageRev, 8.0)
	}
}

func TestOverage_ZeroWhenUsageBelowAllowance(t *testing.T) {
	p := defaultParams()
	p.BYOKFrac = 0
	p.LLMUsagePerBuilder = [2]float64{2.0, 4.0} // below the $3 / $6 allowances

	team := paidOrgResult(DefaultTiers[1], 0, 1, p)
	if !approx(team.OverageRev, 0) {
		t.Errorf("Team overage (usage<allowance) = %v, want 0", team.OverageRev)
	}
	biz := paidOrgResult(DefaultTiers[2], 1, 1, p)
	if !approx(biz.OverageRev, 0) {
		t.Errorf("Business overage (usage<allowance) = %v, want 0", biz.OverageRev)
	}
}

func TestOverage_AtMarkupOne_EqualsListSpread(t *testing.T) {
	// With markup exactly 1.00, overage revenue == (usage − allowance) charged at list.
	p := defaultParams()
	p.BYOKFrac = 0
	usage := 9.0
	p.LLMUsagePerBuilder = [2]float64{usage, usage}
	team := paidOrgResult(DefaultTiers[1], 0, 1, p)
	wantOverage := 4 * (usage - 3) // 4 builders, allowance $3
	if !approx(team.OverageRev, wantOverage) {
		t.Errorf("overage at markup 1.0 = %v, want %v (list spread)", team.OverageRev, wantOverage)
	}
}

// ── COGS: the volume-discount spread covers LLM cost at/above the allowance ────

func TestLLMCost_IsVolumeDiscountedUsage(t *testing.T) {
	p := defaultParams()
	p.BYOKFrac = 0
	// Team: 4 builders × $5 usage × 0.65 = $13.
	team := paidOrgResult(DefaultTiers[1], 0, 1, p)
	if !approx(team.LLMCostUSD, 4*5*0.65) {
		t.Errorf("Team LLM cost = %v, want %v", team.LLMCostUSD, 13.0)
	}
}

// TestAllowance_DoesNotBleedMargin proves the central claim of the model: because
// the volume discount applies to the WHOLE of usage (including the included
// allowance), the LLM revenue attributable to a managed builder (sub LLM portion +
// overage) always exceeds our LLM cost — even when usage exactly equals the
// allowance (overage = 0). Quantified per tier.
func TestAllowance_DoesNotBleedMargin(t *testing.T) {
	cases := []struct {
		tierIdx     int
		allowance   float64
		disc        float64
		usagePoints []float64 // provider $/builder to probe (at, below, above allowance)
	}{
		{0, 3, 0.65, []float64{0, 1.5, 3, 5, 10}},   // Team allowance $3
		{1, 6, 0.65, []float64{0, 3, 6, 14, 25}},    // Business allowance $6
	}
	for _, c := range cases {
		tier := DefaultTiers[c.tierIdx+1]
		for _, usage := range c.usagePoints {
			p := defaultParams()
			p.BYOKFrac = 0
			p.LLMVolumeDiscount = c.disc
			p.LLMUsagePerBuilder[c.tierIdx] = usage

			r := paidOrgResult(tier, c.tierIdx, 1, p)
			builders := p.BuildersPerOrg[c.tierIdx]

			// LLM revenue we can attribute to managed AI for these builders:
			//   = included-LLM portion of the subscription + overage revenue.
			llmSubPortion := builders * tier.IncludedLLMUSD
			llmRevenue := llmSubPortion + r.OverageRev
			if llmRevenue+eps < r.LLMCostUSD {
				t.Errorf("%s usage=%v: LLM revenue %v < LLM cost %v (allowance bleeds margin)",
					tier.Key, usage, llmRevenue, r.LLMCostUSD)
			}
		}
	}
}

// ── Paystack fees ─────────────────────────────────────────────────────────────

func TestPaystackFee_ZeroForNonPositiveGross(t *testing.T) {
	p := defaultParams()
	if f := paystackFeeUSD(0, p); f != 0 {
		t.Errorf("fee(0) = %v, want 0", f)
	}
	if f := paystackFeeUSD(-10, p); f != 0 {
		t.Errorf("fee(-10) = %v, want 0", f)
	}
}

func TestPaystackFee_FXRateCancels(t *testing.T) {
	// Fee is computed in ZAR then converted back to USD; the FX rate must cancel
	// for the percentage component, leaving pct%·gross + flat/fx.
	p := defaultParams()
	gross := 100.0
	want := gross*(p.PaystackPctFee/100) + p.PaystackFlat/p.FXRate
	if got := paystackFeeUSD(gross, p); !approx(got, want) {
		t.Errorf("fee(%v) = %v, want %v", gross, got, want)
	}
}

// ── Per-org contribution & margin ─────────────────────────────────────────────

func TestPaidOrg_ContributionIsNetMinusCOGS(t *testing.T) {
	p := defaultParams()
	team := paidOrgResult(DefaultTiers[1], 0, 1, p)
	wantContrib := team.NetRevUSD - team.COGSUSD
	if !approx(team.Contribution, wantContrib) {
		t.Errorf("Team contribution = %v, want NetRev-COGS = %v", team.Contribution, wantContrib)
	}
	if team.Contribution <= 0 {
		t.Errorf("Team per-org contribution = %v, want > 0", team.Contribution)
	}
	// COGS = LLM + infra + other.
	if !approx(team.COGSUSD, team.LLMCostUSD+team.InfraUSD+team.OtherCOGSUSD) {
		t.Errorf("Team COGS = %v, want LLM+infra+other = %v",
			team.COGSUSD, team.LLMCostUSD+team.InfraUSD+team.OtherCOGSUSD)
	}
}

func TestBothPaidTiers_PositiveMargin(t *testing.T) {
	p := defaultParams()
	for i, tier := range DefaultTiers[1:] {
		r := paidOrgResult(tier, i, 1, p)
		if r.Contribution <= 0 {
			t.Errorf("%s contribution = %v, want > 0", tier.Key, r.Contribution)
		}
		if r.MarginPct <= 0 {
			t.Errorf("%s margin = %v%%, want > 0", tier.Key, r.MarginPct)
		}
	}
}

// ── Full simulation: profitability sweep ──────────────────────────────────────

func TestSimulate_ProfitabilitySweep_PositiveAtEachScale(t *testing.T) {
	base := defaultParams()
	for _, n := range []int{50, 100, 500, 1_000, 5_000, 10_000} {
		p := base
		p.TotalOrgs = n
		r := Simulate(p)
		if r.Contribution <= 0 {
			t.Errorf("Simulate(%d orgs): total contribution = %v, want > 0", n, r.Contribution)
		}
		if r.MarginPct <= 0 {
			t.Errorf("Simulate(%d orgs): margin = %v%%, want > 0", n, r.MarginPct)
		}
		// Break-even must be reachable (1 = profitable from the first paying customer).
		if r.BreakEvenPaid != 1 {
			t.Errorf("Simulate(%d orgs): break-even = %d, want 1", n, r.BreakEvenPaid)
		}
	}
}

func TestSimulate_FreeDragIsTheOnlyLoss(t *testing.T) {
	p := defaultParams()
	p.TotalOrgs = 1000
	r := Simulate(p)
	// Free tier contribution is negative (pure drag); paid tiers positive.
	if r.Tiers[0].Contribution > 0 {
		t.Errorf("free tier contribution = %v, want <= 0 (drag)", r.Tiers[0].Contribution)
	}
	if r.FreeDragUSD < 0 {
		t.Errorf("free drag = %v, want >= 0 (reported as positive cost)", r.FreeDragUSD)
	}
	// Free drag equals the free tier's COGS.
	if !approx(r.FreeDragUSD, r.Tiers[0].COGSUSD) {
		t.Errorf("free drag %v != free COGS %v", r.FreeDragUSD, r.Tiers[0].COGSUSD)
	}
}

func TestSimulate_BlendedContributionMatchesWeightedTiers(t *testing.T) {
	p := defaultParams()
	p.TotalOrgs = 1000
	r := Simulate(p)

	mixSum := p.PaidMix[0] + p.PaidMix[1]
	var want float64
	for i, tier := range DefaultTiers[1:] {
		unit := paidOrgResult(tier, i, 1, p)
		want += (p.PaidMix[i] / mixSum) * unit.Contribution
	}
	if !approx(r.PerPaidContrib, want) {
		t.Errorf("PerPaidContrib = %v, want weighted %v", r.PerPaidContrib, want)
	}
	if r.PerPaidContrib <= 0 {
		t.Errorf("blended per-paid contribution = %v, want > 0", r.PerPaidContrib)
	}
}

func TestSimulate_HighBYOK_StillProfitable(t *testing.T) {
	// Even at 100% BYOK (worst case for LLM profit-center revenue), the
	// subscription alone must keep paid tiers in the black.
	p := defaultParams()
	p.BYOKFrac = 1.0
	p.TotalOrgs = 1000
	r := Simulate(p)
	if r.Contribution <= 0 {
		t.Errorf("100%% BYOK contribution = %v, want > 0 (sub covers infra)", r.Contribution)
	}
}

func TestSimulate_PaidAndFreeOrgsPartition(t *testing.T) {
	p := defaultParams()
	p.TotalOrgs = 1000
	r := Simulate(p)
	if r.PaidOrgs+r.FreeOrgs != r.TotalOrgs {
		t.Errorf("paid %d + free %d != total %d", r.PaidOrgs, r.FreeOrgs, r.TotalOrgs)
	}
	// conv 6% with 3% churn → ~5.82% paid.
	if r.PaidOrgs < 50 || r.PaidOrgs > 60 {
		t.Errorf("paid orgs = %d, want ~58 (6%% conv, 3%% churn)", r.PaidOrgs)
	}
}

func TestSafeDiv(t *testing.T) {
	if got := safeDiv(10, 0); got != 0 {
		t.Errorf("safeDiv(10,0) = %v, want 0", got)
	}
	if got := safeDiv(10, 4); !approx(got, 2.5) {
		t.Errorf("safeDiv(10,4) = %v, want 2.5", got)
	}
}

func TestSimulate_ZeroVolumeDiscountDefaultsToOne(t *testing.T) {
	// A disc of 0 means "no discount" (cost == charge), guarded to 1 in the model.
	p := defaultParams()
	p.BYOKFrac = 0
	p.LLMVolumeDiscount = 0
	team := paidOrgResult(DefaultTiers[1], 0, 1, p)
	// LLM cost should equal full usage (disc treated as 1.0): 4 × $5 = $20.
	if !approx(team.LLMCostUSD, 4*5*1.0) {
		t.Errorf("zero-disc LLM cost = %v, want %v (disc guarded to 1)", team.LLMCostUSD, 20.0)
	}
}
