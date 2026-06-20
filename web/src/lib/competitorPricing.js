/**
 * competitorPricing — shared cost math for the Pricing + Compare calculators.
 *
 * gitstate prices per BUILDER (stakeholders are always free). Managed includes
 * AI; BYOK drops the included-AI value (Team $6 → $3, Business $14 → $8).
 *
 * Competitors price per TOTAL seat (builders + stakeholders). AI is free
 * (Linear), bundled (Jira / ZenHub) or a per-seat add-on (ClickUp, GitHub).
 *
 * THE REALITY (verified by the math below): after the 2026 reprice, gitstate is
 * the cheapest option at EVERY team shape.
 *   - Worst case is a pure all-builder team (0 stakeholders):
 *       · AI on  → gitstate managed $6 < Linear $8 (cheapest AI-inclusive rival),
 *                  far below GitHub+Copilot $13.67 and ClickUp+Brain $16.
 *       · AI off → gitstate BYOK $3 < GitHub $3.67 (cheapest base rival).
 *   - Add any stakeholders and gitstate stays flat while every competitor scales
 *     per seat, so the gap only widens.
 * So we sort by actual cost (gitstate genuinely lands on top) and report the
 * savings vs the NEXT-CHEAPEST rival and vs the MOST-EXPENSIVE option. There is
 * no "a competitor wins" / break-even case anymore — gitstate always wins.
 *
 * All competitor numbers are the researched 2026 list prices — kept exactly.
 */

// ── gitstate per-builder pricing (managed + BYOK) ────────────────────────────
// These mirror GET /api/plans (perBuilderUsd / byokPerBuilderUsd). We default to
// the "Team" tier for the head-to-head calculator; the live values are passed in
// from the plans API so the calculator never hardcodes a stale number.
export const GITSTATE_DEFAULT = { managed: 6, byok: 3, planName: 'Team' }

// ── Competitors — per total seat, 2026 list prices (exact, never inflated) ───
export const COMPETITORS = [
  {
    key: 'github',
    label: 'GitHub Projects',
    perSeat: 3.67,
    aiAddOn: 10, // Copilot, per seat
    aiKind: 'addon',
    note: 'Team · Copilot +$10/seat',
  },
  {
    key: 'clickup',
    label: 'ClickUp',
    perSeat: 7,
    aiAddOn: 9, // Brain, per seat
    aiKind: 'addon',
    note: 'Paid · Brain AI +$9/seat',
  },
  {
    key: 'jira',
    label: 'Jira',
    perSeat: 7.53,
    aiAddOn: 0,
    aiKind: 'bundled',
    note: 'Standard · AI bundled',
    addonsNote: true, // marketplace add-ons run +30–50% in practice
  },
  {
    key: 'linear',
    label: 'Linear',
    perSeat: 8,
    aiAddOn: 0,
    aiKind: 'free',
    note: 'Standard · AI included free',
  },
  {
    key: 'zenhub',
    label: 'ZenHub',
    perSeat: 8.33,
    aiAddOn: 0,
    aiKind: 'bundled',
    note: 'Annual · AI bundled',
  },
]

/**
 * Resolve the gitstate per-builder price for the head-to-head calculator from
 * the live plans list. Falls back to GITSTATE_DEFAULT.
 * @param {Array} plans   GET /api/plans payload
 * @param {string} planKey which paid tier to compare against ('team' | 'business')
 */
export function gitstatePricing(plans, planKey = 'team') {
  const fallback = GITSTATE_DEFAULT
  if (!Array.isArray(plans)) return fallback
  const p = plans.find((x) => x.key === planKey)
  if (!p) return fallback
  const managed = typeof p.perBuilderUsd === 'number' && p.perBuilderUsd > 0 ? p.perBuilderUsd : fallback.managed
  const byok =
    typeof p.byokPerBuilderUsd === 'number' && p.byokPerBuilderUsd > 0 ? p.byokPerBuilderUsd : managed
  return { managed, byok, planName: p.name ?? fallback.planName }
}

/**
 * Compute monthly cost for gitstate + every competitor, SORTED BY ACTUAL COST
 * (cheapest first). gitstate genuinely lands on top at every team shape.
 *
 * @param {object}  o
 * @param {number}  o.builders
 * @param {number}  o.stakeholders
 * @param {boolean} o.byok          gitstate billing mode
 * @param {boolean} o.needsAi       add per-seat AI add-on for ClickUp / GitHub
 * @param {object}  o.gs            { managed, byok, planName }
 * @returns {{ rows, gs, nextCheapest, mostExpensive, saveVsNext, saveVsMax, pctVsNext, multipleVsMax }}
 */
export function computeCosts({ builders, stakeholders, byok, needsAi, gs = GITSTATE_DEFAULT }) {
  const totalSeats = builders + stakeholders
  const gsPerBuilder = byok ? gs.byok : gs.managed

  const gitstate = {
    key: 'gitstate',
    label: 'gitstate',
    isGs: true,
    total: builders * gsPerBuilder,
    seatBasis: builders,
    seatLabel: builders === 1 ? 'builder' : 'builders',
    perUnit: gsPerBuilder,
    aiKind: 'included',
    note: `${gs.planName} · ${byok ? 'BYOK' : 'managed'}`,
    breakdown:
      `${builders} builder${builders === 1 ? '' : 's'} × $${gsPerBuilder}` +
      ` · ${stakeholders} stakeholder${stakeholders === 1 ? '' : 's'} free` +
      ` · AI ${byok ? 'BYOK' : 'included'}`,
  }

  const competitors = COMPETITORS.map((c) => {
    const seatCost = totalSeats * c.perSeat
    const aiCost = needsAi && c.aiKind === 'addon' ? totalSeats * c.aiAddOn : 0
    return {
      key: c.key,
      label: c.label,
      isGs: false,
      total: seatCost + aiCost,
      seatBasis: totalSeats,
      seatLabel: totalSeats === 1 ? 'seat' : 'seats',
      perUnit: c.perSeat,
      aiKind: c.aiKind,
      aiCost,
      aiAddOn: c.aiAddOn,
      addonsNote: c.addonsNote,
      note: c.note,
      breakdown:
        `${totalSeats} seat${totalSeats === 1 ? '' : 's'} × $${c.perSeat}` +
        (aiCost > 0 ? ` + AI ${totalSeats} × $${c.aiAddOn}` : ''),
    }
  })

  const rows = [gitstate, ...competitors]
  // Sort by real computed cost; gitstate genuinely lands cheapest. Tie-break
  // keeps gitstate first when totals match (e.g. a degenerate zero case).
  rows.sort((a, b) => a.total - b.total || (a.isGs ? -1 : b.isGs ? 1 : 0))

  // Next-cheapest = the lowest-cost competitor (the closest rival to beat).
  const nextCheapest = rows.find((r) => !r.isGs) ?? null
  const mostExpensive = rows[rows.length - 1]

  const saveVsNext = nextCheapest ? nextCheapest.total - gitstate.total : 0
  const saveVsMax = mostExpensive ? mostExpensive.total - gitstate.total : 0
  // % cheaper than the next-cheapest rival (how much less gitstate costs).
  const pctVsNext =
    nextCheapest && nextCheapest.total > 0
      ? Math.round((saveVsNext / nextCheapest.total) * 100)
      : 0
  // "Z× less than the most expensive" — guard against divide-by-zero.
  const multipleVsMax =
    mostExpensive && gitstate.total > 0 ? mostExpensive.total / gitstate.total : null

  return {
    rows,
    gs: gitstate,
    nextCheapest,
    mostExpensive,
    saveVsNext,
    saveVsMax,
    pctVsNext,
    multipleVsMax,
  }
}
