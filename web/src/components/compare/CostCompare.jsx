/**
 * CostCompare — grounded competitor monthly-cost calculator.
 *
 * Inputs: # builders, # stakeholders, "need AI features" toggle.
 * Computes monthly cost for gitstate (builders × $12, stakeholders free, AI
 * included) vs each competitor (total seats × per-seat price, plus a per-seat
 * AI add-on for ClickUp / GitHub when AI is on). Renders a sorted, hand-rolled
 * SVG bar chart with gitstate highlighted, the $ / % saved vs each tool, a
 * headline, and a transparent "how this is calculated" disclosure.
 *
 * All competitor numbers are the researched 2026 figures — kept exactly as
 * given, never inflated. Currency-aware via useCurrency().
 */
import { useMemo, useState } from 'react'
import {
  Users, Eye, Sparkles, Info, TrendingDown, Check, Minus, ArrowRight,
} from 'lucide-react'
import { Link } from 'react-router-dom'
import { useCurrency } from '../../lib/currency.jsx'

// ── Grounded 2026 pricing (per total seat unless noted) ──────────────────────
// gitstate: per BUILDER, stakeholders free, managed AI included.
// Competitors: per TOTAL seat (builders + stakeholders); AI is free (Linear),
// bundled (Jira / ZenHub) or a separate per-seat add-on (ClickUp, GitHub).
const GITSTATE = { perBuilder: 12, label: 'gitstate', note: 'Team plan' }

const COMPETITORS = [
  {
    key: 'linear',
    label: 'Linear',
    perSeat: 8,
    aiAddOn: 0,
    aiKind: 'free',
    note: 'Standard · AI included free',
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
    key: 'clickup',
    label: 'ClickUp',
    perSeat: 7,
    aiAddOn: 9,
    aiKind: 'addon',
    note: 'Paid · Brain AI +$9/seat',
  },
  {
    key: 'zenhub',
    label: 'ZenHub',
    perSeat: 8.33,
    aiAddOn: 0,
    aiKind: 'bundled',
    note: 'Annual · AI bundled',
  },
  {
    key: 'github',
    label: 'GitHub Projects',
    perSeat: 3.67,
    aiAddOn: 10,
    aiKind: 'addon',
    note: 'Team · Copilot +$10/seat',
  },
]

// ── Compute monthly cost for everyone given the inputs ───────────────────────
function computeRows({ builders, stakeholders, needsAi }) {
  const totalSeats = builders + stakeholders

  const gitstate = {
    key: 'gitstate',
    label: GITSTATE.label,
    isGs: true,
    total: builders * GITSTATE.perBuilder,
    seatBasis: builders,
    seatLabel: 'builders',
    aiKind: 'included',
    note: GITSTATE.note,
    breakdown: `${builders} builder${builders === 1 ? '' : 's'} × $${GITSTATE.perBuilder} · ${stakeholders} stakeholder${stakeholders === 1 ? '' : 's'} free · AI included`,
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
      seatLabel: 'seats',
      aiKind: c.aiKind,
      aiCost,
      perSeat: c.perSeat,
      aiAddOn: c.aiAddOn,
      addonsNote: c.addonsNote,
      note: c.note,
      breakdown:
        `${totalSeats} seat${totalSeats === 1 ? '' : 's'} × $${c.perSeat}` +
        (aiCost > 0 ? ` + AI ${totalSeats} × $${c.aiAddOn}` : ''),
    }
  })

  const all = [gitstate, ...competitors]
  // Sort ascending by cost (cheapest first) — gitstate should lead.
  all.sort((a, b) => a.total - b.total)
  return all
}

// ── Stepper input ────────────────────────────────────────────────────────────
function Stepper({ icon: Icon, label, sublabel, value, setValue, min, max, accent }) {
  const clamp = (n) => Math.max(min, Math.min(max, n))
  return (
    <div className="flex flex-col gap-2.5">
      <div className="flex items-center gap-2">
        <span
          className="flex items-center justify-center w-7 h-7 rounded-md shrink-0"
          style={{
            background: accent === 'teal' ? 'rgba(45,212,191,0.10)' : 'rgba(99,102,241,0.10)',
            color: accent === 'teal' ? '#2DD4BF' : '#818cf8',
          }}
        >
          <Icon size={15} strokeWidth={2} />
        </span>
        <div className="flex flex-col leading-tight">
          <span className="text-sm font-medium text-[var(--text-dim)]">{label}</span>
          <span className="text-[11px] text-[var(--text-faint)]">{sublabel}</span>
        </div>
      </div>
      <div className="flex items-center gap-2">
        <button
          type="button"
          aria-label={`Decrease ${label}`}
          onClick={() => setValue(clamp(value - 1))}
          disabled={value <= min}
          className="flex items-center justify-center w-9 h-9 rounded-[var(--radius-btn)] border border-[var(--border)] text-[var(--text-muted)] hover:border-[var(--border2)] hover:text-[var(--text)] disabled:opacity-30 disabled:pointer-events-none transition-colors cursor-pointer"
        >
          <Minus size={15} strokeWidth={2.5} />
        </button>
        <input
          type="number"
          inputMode="numeric"
          aria-label={label}
          value={value}
          min={min}
          max={max}
          onChange={(e) => {
            const n = parseInt(e.target.value, 10)
            setValue(Number.isNaN(n) ? min : clamp(n))
          }}
          className="flex-1 h-9 min-w-0 text-center font-mono text-base font-semibold text-[var(--text)] bg-[var(--bg-surface3)] border border-[var(--border)] rounded-[var(--radius-btn)] focus:outline-none focus:border-[#2DD4BF]/50 tabular-nums [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
        />
        <button
          type="button"
          aria-label={`Increase ${label}`}
          onClick={() => setValue(clamp(value + 1))}
          disabled={value >= max}
          className="flex items-center justify-center w-9 h-9 rounded-[var(--radius-btn)] border border-[var(--border)] text-[var(--text-muted)] hover:border-[var(--border2)] hover:text-[var(--text)] disabled:opacity-30 disabled:pointer-events-none transition-colors cursor-pointer"
        >
          <span className="text-base leading-none font-semibold">+</span>
        </button>
      </div>
    </div>
  )
}

// ── Hand-rolled SVG bar chart ────────────────────────────────────────────────
function BarChart({ rows, format }) {
  const max = Math.max(...rows.map((r) => r.total), 1)
  const rowH = 52
  const labelW = 132
  const barAreaW = 'calc(100% - var(--label-w))'

  return (
    <div className="w-full" style={{ '--label-w': `${labelW}px` }}>
      {rows.map((r, i) => {
        const pct = (r.total / max) * 100
        const isFree = r.total === 0
        return (
          <div
            key={r.key}
            className="group flex items-center"
            style={{ height: rowH }}
          >
            {/* Label */}
            <div
              className="flex items-center gap-2 pr-3 shrink-0"
              style={{ width: labelW }}
            >
              <span className="font-mono text-[10px] text-[var(--text-faint)] tabular-nums w-4 text-right">
                {i + 1}
              </span>
              <span
                className={[
                  'text-[13px] truncate',
                  r.isGs ? 'font-semibold' : 'font-medium',
                ].join(' ')}
                style={{ color: r.isGs ? '#2DD4BF' : 'var(--text-dim)' }}
              >
                {r.label}
              </span>
            </div>

            {/* Bar track */}
            <div className="relative flex-1 h-full flex items-center" style={{ width: barAreaW }}>
              <div className="relative w-full h-7 rounded-md overflow-hidden bg-[var(--bg-surface3)]/50">
                <div
                  className="absolute inset-y-0 left-0 rounded-md transition-[width] duration-500 ease-out"
                  style={{
                    width: `${Math.max(pct, isFree ? 0 : 2)}%`,
                    background: r.isGs
                      ? 'linear-gradient(90deg, #2DD4BF, #6366F1)'
                      : 'linear-gradient(90deg, var(--bg-surface3), var(--border2))',
                    boxShadow: r.isGs ? '0 0 18px rgba(45,212,191,0.35)' : 'none',
                  }}
                />
              </div>
              {/* Value */}
              <span
                className={[
                  'absolute right-2 text-[13px] font-mono font-semibold tabular-nums pointer-events-none',
                  r.isGs ? 'text-[#0B1120]' : 'text-[var(--text-dim)]',
                ].join(' ')}
                style={{
                  // place value inside bar if it's gitstate (which is shorter), else at end
                  left: r.isGs && pct > 22 ? `calc(${Math.min(pct, 100)}% - 4.5rem)` : 'auto',
                  right: r.isGs && pct > 22 ? 'auto' : '0.5rem',
                  color: r.isGs && pct > 22 ? '#0B1120' : undefined,
                }}
              >
                {format(r.total)}
              </span>
            </div>
          </div>
        )
      })}
    </div>
  )
}

// ── How-it's-calculated disclosure ───────────────────────────────────────────
function MathDisclosure({ format }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="mt-6 border-t border-[var(--border)] pt-4">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="flex items-center gap-2 text-xs font-mono text-[var(--text-muted)] hover:text-[var(--text)] transition-colors cursor-pointer"
      >
        <Info size={13} />
        How this is calculated
        <span className={`transition-transform duration-300 ${open ? 'rotate-180' : ''}`}>▾</span>
      </button>
      <div
        className="grid transition-all duration-300 ease-out"
        style={{ gridTemplateRows: open ? '1fr' : '0fr' }}
      >
        <div className="overflow-hidden">
          <div className="pt-3 text-[12px] text-[var(--text-faint)] leading-relaxed space-y-2.5">
            <p>
              <span className="text-[#2DD4BF] font-medium">gitstate</span> bills only{' '}
              <span className="text-[var(--text-muted)]">builders</span> at {format(GITSTATE.perBuilder)}/mo
              (Team). Stakeholders are free and managed AI is included — no per-seat AI tax.
            </p>
            <p>
              Every competitor bills <span className="text-[var(--text-muted)]">per total seat</span> (builders{' '}
              <em className="not-italic">plus</em> stakeholders). 2026 list prices:
            </p>
            <ul className="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-1 font-mono pl-1">
              <li>Linear — {format(8)}/seat · AI free</li>
              <li>Jira — {format(7.53)}/seat · AI bundled</li>
              <li>ClickUp — {format(7)}/seat · Brain AI +{format(9)}/seat</li>
              <li>ZenHub — {format(8.33)}/seat · AI bundled</li>
              <li>GitHub Projects — {format(3.67)}/seat · Copilot +{format(10)}/seat</li>
            </ul>
            <p>
              When <span className="text-[var(--text-muted)]">“need AI features”</span> is on, ClickUp and GitHub
              add their per-seat AI add-on to every seat. Jira&apos;s marketplace add-ons commonly add{' '}
              <span className="text-[var(--text-muted)]">+30–50%</span> in practice — not included in the bars
              above, so Jira&apos;s real cost is typically higher than shown.
            </p>
          </div>
        </div>
      </div>
    </div>
  )
}

// ── Main ─────────────────────────────────────────────────────────────────────
export default function CostCompare() {
  const { format } = useCurrency()
  const [builders, setBuilders] = useState(6)
  const [stakeholders, setStakeholders] = useState(20)
  const [needsAi, setNeedsAi] = useState(true)

  const rows = useMemo(
    () => computeRows({ builders, stakeholders, needsAi }),
    [builders, stakeholders, needsAi],
  )

  const gs = rows.find((r) => r.isGs)
  const mostExpensive = rows[rows.length - 1]
  const competitors = rows.filter((r) => !r.isGs)
  // Average competitor cost for a fair headline anchor.
  const avgCompetitor =
    competitors.reduce((s, r) => s + r.total, 0) / Math.max(competitors.length, 1)

  const savedVsMax = mostExpensive.total - gs.total
  const pctVsMax =
    mostExpensive.total > 0 ? Math.round((savedVsMax / mostExpensive.total) * 100) : 0
  const savedVsAvg = avgCompetitor - gs.total
  const pctVsAvg = avgCompetitor > 0 ? Math.round((savedVsAvg / avgCompetitor) * 100) : 0

  return (
    <div className="relative overflow-hidden rounded-2xl border border-[var(--border2)] bg-[var(--bg-surface)] grain">
      <div className="absolute inset-0 ambient-brand pointer-events-none" />
      <div className="relative z-10 grid grid-cols-1 lg:grid-cols-[300px_1fr]">

        {/* ── Inputs panel ── */}
        <div className="p-6 md:p-7 border-b lg:border-b-0 lg:border-r border-[var(--border)] bg-[var(--bg-surface2)]/40">
          <div className="flex items-center gap-2 mb-1">
            <span className="text-[11px] font-mono uppercase tracking-widest text-[var(--text-faint)]">
              your team
            </span>
          </div>
          <p className="text-xs text-[var(--text-muted)] mb-6 leading-relaxed">
            Set your team shape and we compute every tool&apos;s real monthly bill.
          </p>

          <div className="space-y-6">
            <Stepper
              icon={Users}
              label="Builders"
              sublabel="ship code · run agents"
              value={builders}
              setValue={setBuilders}
              min={1}
              max={500}
              accent="teal"
            />
            <Stepper
              icon={Eye}
              label="Stakeholders"
              sublabel="read-only · PMs, clients, execs"
              value={stakeholders}
              setValue={setStakeholders}
              min={0}
              max={5000}
              accent="indigo"
            />

            {/* AI toggle */}
            <div className="flex items-start justify-between gap-3 pt-1">
              <div className="flex items-start gap-2">
                <span className="flex items-center justify-center w-7 h-7 rounded-md bg-[#6366F1]/10 text-[#818cf8] shrink-0">
                  <Sparkles size={15} strokeWidth={2} />
                </span>
                <div className="flex flex-col leading-tight">
                  <span className="text-sm font-medium text-[var(--text-dim)]">Need AI features</span>
                  <span className="text-[11px] text-[var(--text-faint)]">
                    add per-seat AI where it&apos;s an add-on
                  </span>
                </div>
              </div>
              <button
                type="button"
                role="switch"
                aria-checked={needsAi}
                aria-label="Need AI features"
                onClick={() => setNeedsAi((v) => !v)}
                className="relative w-11 h-6 rounded-full shrink-0 mt-0.5 transition-colors duration-200 cursor-pointer"
                style={{
                  background: needsAi
                    ? 'linear-gradient(90deg, #2DD4BF, #6366F1)'
                    : 'var(--bg-surface3)',
                  border: '1px solid var(--border2)',
                }}
              >
                <span
                  className="absolute top-0.5 left-0.5 w-4.5 h-4.5 rounded-full bg-white shadow-sm transition-transform duration-200"
                  style={{
                    width: 18,
                    height: 18,
                    transform: needsAi ? 'translateX(20px)' : 'translateX(0)',
                  }}
                />
              </button>
            </div>
          </div>

          {/* gitstate AI-included reassurance */}
          <div className="mt-6 rounded-[var(--radius-badge)] border border-[#2DD4BF]/20 bg-[#2DD4BF]/[0.05] px-3 py-2.5">
            <p className="text-[11px] text-[var(--text-muted)] leading-relaxed flex items-start gap-1.5">
              <Check size={13} className="text-[#2DD4BF] shrink-0 mt-0.5" strokeWidth={2.5} />
              <span>
                gitstate&apos;s AI is <span className="text-[#2DD4BF] font-medium">always included</span> — no
                per-seat tax, on or off.
              </span>
            </p>
          </div>
        </div>

        {/* ── Results panel ── */}
        <div className="p-6 md:p-8">
          {/* Headline */}
          <div className="mb-6">
            <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1 mb-2">
              <span className="inline-flex items-center gap-1.5 text-[11px] font-mono uppercase tracking-widest text-[#2DD4BF]">
                <TrendingDown size={13} /> your savings
              </span>
            </div>
            <h3 className="font-display text-2xl md:text-[28px] font-bold text-[var(--text)] leading-tight tracking-tight">
              gitstate is{' '}
              <span className="gradient-text">~{pctVsMax}% cheaper</span>{' '}
              than {mostExpensive.label}
            </h3>
            <p className="text-sm text-[var(--text-muted)] mt-2 leading-relaxed">
              {gs.total === 0 ? (
                <>That&apos;s <span className="text-[#2DD4BF] font-semibold">{format(0)}/mo</span> on gitstate.</>
              ) : (
                <>
                  <span className="text-[var(--text-dim)] font-semibold">{format(gs.total)}/mo</span> on gitstate vs{' '}
                  <span className="text-[var(--text-dim)] font-semibold">{format(mostExpensive.total)}/mo</span> —
                  saving <span className="text-[#2DD4BF] font-semibold">{format(savedVsMax)}/mo</span>
                  {savedVsMax > 0 && <> (≈ {format(savedVsMax * 12)}/yr)</>}.
                </>
              )}{' '}
              vs the average competitor you save{' '}
              <span className="text-[#2DD4BF] font-semibold">~{pctVsAvg}%</span> ({format(Math.max(savedVsAvg, 0))}/mo).
            </p>
          </div>

          {/* Chart */}
          <BarChart rows={rows} format={format} />

          {/* Per-tool savings table */}
          <div className="mt-6 overflow-x-auto">
            <table className="w-full text-sm border-collapse min-w-[460px]">
              <thead>
                <tr className="text-[10px] font-mono uppercase tracking-widest text-[var(--text-faint)]">
                  <th className="text-left font-medium pb-2">Tool</th>
                  <th className="text-left font-medium pb-2 hidden sm:table-cell">Basis</th>
                  <th className="text-right font-medium pb-2">Monthly</th>
                  <th className="text-right font-medium pb-2">You save</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((r) => {
                  const saved = r.total - gs.total
                  const pct = r.total > 0 ? Math.round((saved / r.total) * 100) : 0
                  return (
                    <tr
                      key={r.key}
                      className={[
                        'border-t border-[var(--border)]',
                        r.isGs ? 'bg-[#2DD4BF]/[0.05]' : '',
                      ].join(' ')}
                    >
                      <td className="py-2.5 pr-2">
                        <div className="flex items-center gap-2">
                          <span
                            className="text-[13px] font-medium"
                            style={{ color: r.isGs ? '#2DD4BF' : 'var(--text-dim)' }}
                          >
                            {r.label}
                          </span>
                          {r.isGs && (
                            <span className="text-[9px] font-mono uppercase tracking-wider px-1.5 py-0.5 rounded bg-[#2DD4BF]/15 text-[#2DD4BF]">
                              you
                            </span>
                          )}
                          {r.addonsNote && (
                            <span className="text-[9px] font-mono text-yellow-400/80" title="Marketplace add-ons add +30–50% in practice">
                              +add-ons*
                            </span>
                          )}
                        </div>
                        <span className="block text-[10px] text-[var(--text-faint)] font-mono mt-0.5">
                          {r.breakdown}
                        </span>
                      </td>
                      <td className="py-2.5 pr-2 hidden sm:table-cell">
                        <span className="text-[11px] font-mono text-[var(--text-muted)] tabular-nums">
                          {r.seatBasis} {r.seatLabel}
                        </span>
                      </td>
                      <td className="py-2.5 text-right">
                        <span
                          className="text-[13px] font-mono font-semibold tabular-nums"
                          style={{ color: r.isGs ? '#2DD4BF' : 'var(--text-dim)' }}
                        >
                          {format(r.total)}
                        </span>
                      </td>
                      <td className="py-2.5 text-right">
                        {r.isGs ? (
                          <span className="text-[11px] font-mono text-[var(--text-faint)]">—</span>
                        ) : (
                          <span className="text-[12px] font-mono font-semibold tabular-nums text-[#2DD4BF]">
                            {format(saved)}
                            <span className="text-[10px] text-[#2DD4BF]/70 ml-1">({pct}%)</span>
                          </span>
                        )}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>

          <MathDisclosure format={format} />

          {/* CTA */}
          <div className="mt-6 flex flex-col sm:flex-row items-center gap-3">
            <Link
              to="/signup"
              className="inline-flex items-center justify-center gap-2 px-5 py-2.5 rounded-[var(--radius-btn)] font-semibold text-sm text-[#0B1120] w-full sm:w-auto transition-all duration-150 hover:opacity-90"
              style={{ background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' }}
            >
              Start saving — it&apos;s free
              <ArrowRight size={15} strokeWidth={2.5} />
            </Link>
            <Link
              to="/pricing"
              className="inline-flex items-center justify-center gap-2 px-5 py-2.5 rounded-[var(--radius-btn)] font-medium text-sm text-[var(--text-muted)] border border-[var(--border)] w-full sm:w-auto hover:border-[var(--border2)] hover:text-[var(--text)] transition-all duration-150"
            >
              See gitstate pricing
            </Link>
          </div>
        </div>
      </div>
    </div>
  )
}
