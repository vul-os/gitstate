/**
 * CompetitorCalculator — the shared cost calculator used on both /compare and
 * /pricing.
 *
 * Inputs (all SLIDERS + a toggle): # builders (1–100), # stakeholders (0–500),
 * and a single managed↔BYOK segmented control. Managed includes AI (so rivals
 * pay their per-seat AI add-on); BYOK drops it (rivals use their base seat
 * price). Dragging any control updates everything instantly.
 *
 * Math (in ../../lib/competitorPricing.js): gitstate = builders × (managed $6 or
 * BYOK $3), stakeholders free. Competitors = totalSeats × per-seat (+ per-seat
 * AI add-on for ClickUp / GitHub when AI is on; Jira flagged "+add-ons run
 * 30–50%"). After the 2026 reprice gitstate is the cheapest at EVERY team shape,
 * so the result confidently says so and quantifies the savings vs the
 * next-cheapest rival and vs the most-expensive option — no break-even, no
 * "a competitor wins" case.
 *
 * Currency-aware via useCurrency(). Hand-rolled SVG bar chart (no new deps).
 * Reduced-motion-safe (transitions gated on motion-reduce). Both themes.
 */
import { useMemo, useState } from 'react'
import {
  Users, Eye, Sparkles, Info, ArrowRight, TrendingDown,
  KeyRound, GitBranch, Trophy,
} from 'lucide-react'
import { Link } from 'react-router-dom'
import { useCurrency } from '../../lib/currency.jsx'
import {
  COMPETITORS, computeCosts, gitstatePricing, GITSTATE_DEFAULT,
} from '../../lib/competitorPricing.js'

// ── Range slider with a live value bubble ────────────────────────────────────
function SliderField({ icon: Icon, label, sublabel, value, setValue, min, max, accent }) {
  const teal = accent === 'teal'
  const color = teal ? '#2DD4BF' : '#818cf8'
  const pct = ((value - min) / (max - min)) * 100
  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <span
            className="flex items-center justify-center w-7 h-7 rounded-md shrink-0"
            style={{
              background: teal ? 'rgba(45,212,191,0.10)' : 'rgba(99,102,241,0.10)',
              color,
            }}
          >
            <Icon size={15} strokeWidth={2} />
          </span>
          <div className="flex flex-col leading-tight">
            <span className="text-sm font-medium text-[var(--text-dim)]">{label}</span>
            <span className="text-[11px] text-[var(--text-faint)]">{sublabel}</span>
          </div>
        </div>
        {/* live value bubble */}
        <span
          className="font-mono text-base font-semibold tabular-nums px-2.5 py-1 rounded-md min-w-[3.25rem] text-center"
          style={{ background: teal ? 'rgba(45,212,191,0.10)' : 'rgba(99,102,241,0.10)', color }}
          aria-live="polite"
        >
          {value}
        </span>
      </div>
      <input
        type="range"
        min={min}
        max={max}
        step={1}
        value={value}
        aria-label={label}
        aria-valuetext={`${value} ${label}`}
        onChange={(e) => setValue(Number(e.target.value))}
        className="w-full h-1.5 rounded-full appearance-none cursor-pointer outline-none
          focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--bg-surface)]
          [&::-webkit-slider-thumb]:appearance-none [&::-webkit-slider-thumb]:w-4 [&::-webkit-slider-thumb]:h-4
          [&::-webkit-slider-thumb]:rounded-full [&::-webkit-slider-thumb]:bg-[var(--brand-teal)]
          [&::-webkit-slider-thumb]:border-2 [&::-webkit-slider-thumb]:border-[var(--slider-accent)]
          [&::-webkit-slider-thumb]:shadow-[var(--shadow-card)]
          [&::-webkit-slider-thumb]:transition-transform motion-reduce:[&::-webkit-slider-thumb]:transition-none
          [&::-webkit-slider-thumb]:hover:scale-110
          [&::-moz-range-thumb]:w-4 [&::-moz-range-thumb]:h-4 [&::-moz-range-thumb]:rounded-full
          [&::-moz-range-thumb]:bg-[var(--brand-teal)] [&::-moz-range-thumb]:border-2 [&::-moz-range-thumb]:border-solid
          [&::-moz-range-thumb]:border-[var(--slider-accent)]"
        style={{
          background: `linear-gradient(90deg, ${color} ${pct}%, var(--bg-surface3) ${pct}%)`,
          '--slider-accent': color,
        }}
      />
      <div className="flex justify-between text-[10px] font-mono text-[var(--text-faint)] tabular-nums -mt-1">
        <span>{min}</span>
        <span>{max}</span>
      </div>
    </div>
  )
}

// ── Pill toggle (segmented, two options) ─────────────────────────────────────
function SegToggle({ label, options, value, onChange }) {
  return (
    <div className="flex flex-col gap-2">
      <span className="text-[11px] font-mono uppercase tracking-widest text-[var(--text-faint)]">{label}</span>
      <div className="inline-flex rounded-[var(--radius-btn)] border border-[var(--border2)] bg-[var(--bg-surface3)] p-0.5">
        {options.map((opt) => {
          const active = opt.value === value
          return (
            <button
              key={String(opt.value)}
              type="button"
              onClick={() => onChange(opt.value)}
              aria-pressed={active}
              className={[
                'flex-1 flex items-center justify-center gap-1.5 px-3 py-1.5 rounded-[6px] text-xs font-medium transition-all duration-150 motion-reduce:transition-none cursor-pointer',
                active ? 'text-[#0B1120]' : 'text-[var(--text-muted)] hover:text-[var(--text)]',
              ].join(' ')}
              style={active ? { background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' } : undefined}
            >
              {opt.icon}
              {opt.label}
            </button>
          )
        })}
      </div>
    </div>
  )
}

// ── Hand-rolled SVG bar chart ────────────────────────────────────────────────
function BarChart({ rows, format }) {
  const max = Math.max(...rows.map((r) => r.total), 1)
  const rowH = 50
  const labelW = 132

  return (
    <div className="w-full">
      {rows.map((r, i) => {
        const pct = (r.total / max) * 100
        const isFree = r.total === 0
        const isCheapest = i === 0
        return (
          <div key={r.key} className="group flex items-center" style={{ height: rowH }}>
            {/* Label */}
            <div className="flex items-center gap-2 pr-3 shrink-0" style={{ width: labelW }}>
              <span className="font-mono text-[10px] text-[var(--text-faint)] tabular-nums w-4 text-right">
                {i + 1}
              </span>
              <span
                className={['text-[13px] truncate', r.isGs ? 'font-semibold' : 'font-medium'].join(' ')}
                style={{ color: r.isGs ? '#2DD4BF' : 'var(--text-dim)' }}
              >
                {r.label}
              </span>
              {isCheapest && (
                <Trophy size={11} className="text-[#fbbf24] shrink-0" strokeWidth={2.2} aria-label="cheapest" />
              )}
            </div>

            {/* Bar track — hand-rolled SVG */}
            <div className="relative flex-1 h-full flex items-center">
              <svg
                className="w-full h-7 overflow-visible"
                viewBox="0 0 100 28"
                preserveAspectRatio="none"
                role="img"
                aria-label={`${r.label}: ${format(r.total)} per month`}
              >
                <defs>
                  <linearGradient id={`gs-bar-${r.key}`} x1="0" y1="0" x2="1" y2="0">
                    <stop offset="0%" stopColor="#2DD4BF" />
                    <stop offset="100%" stopColor="#6366F1" />
                  </linearGradient>
                </defs>
                {/* track */}
                <rect x="0" y="0" width="100" height="28" rx="5" className="fill-[var(--bg-surface3)] opacity-50" />
                {/* value bar */}
                <rect
                  x="0"
                  y="0"
                  width={Math.max(pct, isFree ? 0 : 1.5)}
                  height="28"
                  rx="5"
                  className="transition-[width] duration-500 ease-out motion-reduce:transition-none"
                  fill={
                    r.isGs
                      ? `url(#gs-bar-${r.key})`
                      : isCheapest
                      ? 'rgba(251,191,36,0.45)'
                      : 'var(--border2)'
                  }
                  style={r.isGs ? { filter: 'drop-shadow(0 0 8px rgba(45,212,191,0.35))' } : undefined}
                />
              </svg>
              <span
                className="absolute text-[13px] font-mono font-semibold tabular-nums pointer-events-none text-[var(--text-dim)]"
                style={{
                  left: r.isGs && pct > 24 ? `calc(${Math.min(pct, 100)}% - 4.5rem)` : 'auto',
                  right: r.isGs && pct > 24 ? 'auto' : '0.5rem',
                  color: r.isGs && pct > 24 ? '#0B1120' : undefined,
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
function MathDisclosure({ format, gs }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="mt-6 border-t border-[var(--border)] pt-4">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="flex items-center gap-2 text-xs font-mono text-[var(--text-muted)] hover:text-[var(--text)] transition-colors motion-reduce:transition-none cursor-pointer"
      >
        <Info size={13} />
        How this is calculated
        <span className={`transition-transform duration-300 motion-reduce:transition-none ${open ? 'rotate-180' : ''}`}>▾</span>
      </button>
      <div className="grid transition-all duration-300 ease-out motion-reduce:transition-none" style={{ gridTemplateRows: open ? '1fr' : '0fr' }}>
        <div className="overflow-hidden">
          <div className="pt-3 text-[12px] text-[var(--text-faint)] leading-relaxed space-y-2.5">
            <p>
              <span className="text-[#2DD4BF] font-medium">gitstate</span> bills only{' '}
              <span className="text-[var(--text-muted)]">builders</span> — {format(gs.managed)}/builder managed (AI
              included) or {format(gs.byok)}/builder BYOK (you bring your own LLM key; we drop the included-AI value).
              Stakeholders are always free, so they never touch the bill.
            </p>
            <p>
              Competitors bill <span className="text-[var(--text-muted)]">per seat</span>. Verified 2026 list
              prices, matched to the tier a team would buy for comparable work:
            </p>
            <ul className="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-1 font-mono pl-1">
              <li>GitHub Projects — {format(4)}/seat · Copilot Pro +{format(10)}/builder</li>
              <li>ClickUp — {format(7)}/seat · Brain +{format(9)}/builder · guests free</li>
              <li>Jira — {format(7.53)}/seat · Rovo AI bundled</li>
              <li>Linear — {format(10)}/seat · AI = paid credits</li>
              <li>ZenHub — {format(8.33)}/seat · AI bundled</li>
            </ul>
            <p>
              We keep it honest both ways: AI add-ons (Copilot, Brain) are counted{' '}
              <span className="text-[var(--text-muted)]">per builder</span>, not per seat — read-only stakeholders
              don&apos;t need an AI license — and where a tool offers free read-only viewers
              (<span className="text-[var(--text-muted)]">ClickUp guests</span>), those stakeholders aren&apos;t
              charged a seat. On <span className="text-[var(--text-muted)]">BYOK</span> rivals drop their AI add-on
              (base seat only).
            </p>
            <p className="text-[var(--text-faint)]">
              Rows are sorted by actual computed cost. Because gitstate charges only for builders and bundles AI at
              {' '}{format(gs.managed)} (or {format(gs.byok)} BYOK), it still lands cheapest even for an all-builder
              team with zero stakeholders ({format(gs.managed)} &lt; Jira {format(7.53)} with AI; {format(gs.byok)} BYOK
              &lt; GitHub {format(4)} without) — and the gap only widens as you add free stakeholders.
            </p>
          </div>
        </div>
      </div>
    </div>
  )
}

// ── Main ─────────────────────────────────────────────────────────────────────
/**
 * @param {object}  props
 * @param {Array}   props.plans         GET /api/plans payload (for live gitstate price)
 * @param {string}  props.planKey       'team' | 'business' — which tier to compare
 */
export default function CompetitorCalculator({ plans, planKey = 'team' }) {
  const { format } = useCurrency()
  const [builders, setBuilders] = useState(8)
  const [stakeholders, setStakeholders] = useState(15)
  const [byok, setByok] = useState(false)
  // AI need is derived, not a separate control: Managed ⇒ AI included (rivals
  // pay their per-seat AI add-on); BYOK ⇒ AI off (rivals use base seat price).
  const needsAi = !byok

  const gsPrice = useMemo(() => gitstatePricing(plans, planKey), [plans, planKey])

  const { rows, gs, nextCheapest, mostExpensive, saveVsNext, saveVsMax, pctVsNext, multipleVsMax } = useMemo(
    () => computeCosts({ builders, stakeholders, byok, needsAi, gs: gsPrice }),
    [builders, stakeholders, byok, needsAi, gsPrice],
  )

  // The active per-builder price reflected in the segmented control.
  const gsPerBuilder = byok ? gsPrice.byok : gsPrice.managed

  return (
    <div className="relative overflow-hidden rounded-2xl border border-[var(--border2)] bg-[var(--bg-surface)] grain">
      <div className="absolute inset-0 ambient-brand pointer-events-none" />
      <div className="relative z-10 grid grid-cols-1 lg:grid-cols-[300px_1fr]">

        {/* ── Inputs panel ── */}
        <div className="p-6 md:p-7 border-b lg:border-b-0 lg:border-r border-[var(--border)] bg-[var(--bg-surface2)]/40">
          <span className="text-[11px] font-mono uppercase tracking-widest text-[var(--text-faint)]">
            your team
          </span>
          <p className="text-xs text-[var(--text-muted)] mt-1 mb-6 leading-relaxed">
            Drag the sliders — we compute every tool&apos;s real monthly bill and rank by actual cost.
          </p>

          <div className="space-y-6">
            <SliderField
              icon={Users}
              label="Builders"
              sublabel="ship code · run agents"
              value={builders}
              setValue={setBuilders}
              min={1}
              max={50}
              accent="teal"
            />
            <SliderField
              icon={Eye}
              label="Stakeholders"
              sublabel="read-only · PMs, clients, execs"
              value={stakeholders}
              setValue={setStakeholders}
              min={0}
              max={100}
              accent="indigo"
            />

            <SegToggle
              label="gitstate billing · AI"
              value={byok}
              onChange={setByok}
              options={[
                { value: false, label: `Managed ${format(gsPrice.managed)}`, icon: <Sparkles size={12} /> },
                { value: true, label: `BYOK ${format(gsPrice.byok)}`, icon: <KeyRound size={12} /> },
              ]}
            />
            <p className="text-[11px] text-[var(--text-faint)] leading-relaxed -mt-3">
              <span className="text-[var(--text-muted)]">Managed</span> bundles AI — rivals add their per-seat AI tax.
              {' '}<span className="text-[var(--text-muted)]">BYOK</span> routes AI to your own key — rivals drop to base seat price.
            </p>
          </div>

          {/* gitstate context note */}
          <div className="mt-6 rounded-[var(--radius-badge)] border border-[#2DD4BF]/20 bg-[#2DD4BF]/[0.05] px-3 py-2.5">
            <p className="text-[11px] text-[var(--text-muted)] leading-relaxed flex items-start gap-1.5">
              <Eye size={13} className="text-[#2DD4BF] shrink-0 mt-0.5" strokeWidth={2.2} />
              <span>
                gitstate charges only for builders — <span className="text-[#2DD4BF] font-medium">stakeholders are free</span>.
                {' '}{byok
                  ? `BYOK is ${format(gsPrice.byok)}/builder; you route LLM calls to your own key.`
                  : `Managed is ${format(gsPrice.managed)}/builder with AI included — no per-seat AI tax.`}
              </span>
            </p>
          </div>
        </div>

        {/* ── Results panel ── */}
        <div className="p-6 md:p-8">
          {/* Headline — gitstate is always cheapest */}
          <div className="mb-6">
            <span className="inline-flex items-center gap-1.5 text-[11px] font-mono uppercase tracking-widest text-[#2DD4BF] mb-2">
              <TrendingDown size={13} /> gitstate is the cheapest at every team size
            </span>
            <h3 className="font-display text-2xl md:text-[28px] font-bold text-[var(--text)] leading-tight tracking-tight">
              {format(gs.total)}/mo on gitstate —{' '}
              <span className="gradient-text">
                {saveVsNext > 0
                  ? `${format(saveVsNext)}/mo less`
                  : pctVsNext > 0
                  ? `${pctVsNext}% less`
                  : 'the lowest bill'}
              </span>{' '}
              than the next-cheapest{nextCheapest ? ` (${nextCheapest.label})` : ''}
            </h3>
            <p className="text-sm text-[var(--text-muted)] mt-2 leading-relaxed">
              {pctVsNext > 0 && (
                <>That&apos;s <span className="text-[var(--text-dim)] font-medium">{pctVsNext}% below</span>{' '}
                {nextCheapest?.label}</>
              )}
              {pctVsNext > 0 && multipleVsMax && multipleVsMax >= 1.05 && ' · '}
              {multipleVsMax && multipleVsMax >= 1.05 && (
                <>
                  <span className="text-[var(--text-dim)] font-medium">{multipleVsMax.toFixed(1)}× less</span>{' '}
                  than {mostExpensive.label}
                </>
              )}
              {saveVsMax > 0 && (
                <>
                  {' '}— ≈ {format(saveVsMax * 12)}/yr saved versus the priciest option.
                </>
              )}
              {' '}
              {stakeholders > 0 ? (
                <>Your <span className="text-[var(--text-dim)] font-medium">{stakeholders} stakeholder
                {stakeholders === 1 ? '' : 's'}</span> ride free here and bill on every seat elsewhere.</>
              ) : (
                <>Even with zero stakeholders, {format(gsPerBuilder)}/builder undercuts every per-seat rival.</>
              )}
            </p>
          </div>

          {/* Always-cheapest callout */}
          <div className="flex items-start gap-2.5 rounded-[var(--radius-badge)] border border-[#2DD4BF]/25 bg-[#2DD4BF]/[0.06] px-3.5 py-3 text-xs leading-relaxed">
            <Trophy size={14} className="text-[#2DD4BF] shrink-0 mt-0.5" />
            <span className="text-[var(--text-dim)]">
              gitstate is the cheapest option for{' '}
              <strong className="text-[#2DD4BF]">
                {builders} builder{builders === 1 ? '' : 's'}
                {stakeholders > 0 ? ` + ${stakeholders} stakeholder${stakeholders === 1 ? '' : 's'}` : ' (no stakeholders)'}
              </strong>{' '}
              on {byok ? 'BYOK' : 'managed'} AI — and at every other team shape too. Builders-only is the closest a
              rival ever gets, and gitstate still wins it.
            </span>
          </div>

          {/* Chart */}
          <div className="mt-6">
            <BarChart rows={rows} format={format} />
          </div>

          {/* Per-tool table */}
          <div className="mt-6 overflow-x-auto">
            <table className="w-full text-sm border-collapse min-w-[460px]">
              <thead>
                <tr className="text-[10px] font-mono uppercase tracking-widest text-[var(--text-faint)]">
                  <th className="text-left font-medium pb-2">Tool</th>
                  <th className="text-left font-medium pb-2 hidden sm:table-cell">Basis</th>
                  <th className="text-right font-medium pb-2">Monthly</th>
                  <th className="text-right font-medium pb-2">vs gitstate</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((r) => {
                  const delta = r.total - gs.total // + = costs more than gitstate
                  return (
                    <tr
                      key={r.key}
                      className={['border-t border-[var(--border)]', r.isGs ? 'bg-[#2DD4BF]/[0.05]' : ''].join(' ')}
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
                              you · cheapest
                            </span>
                          )}
                          {r.addonsNote && (
                            <span
                              className="text-[9px] font-mono text-yellow-400/80"
                              title="Marketplace add-ons add +30–50% in practice"
                            >
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
                            +{format(delta)}
                            <span className="text-[10px] text-[#2DD4BF]/70 ml-1">more</span>
                          </span>
                        )}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>

          <MathDisclosure format={format} gs={gsPrice} />

          {/* CTA */}
          <div className="mt-6 flex flex-col sm:flex-row items-center gap-3">
            <Link
              to="/signup"
              className="inline-flex items-center justify-center gap-2 px-5 py-2.5 rounded-[var(--radius-btn)] font-semibold text-sm text-[#0B1120] w-full sm:w-auto transition-all duration-150 motion-reduce:transition-none hover:opacity-90"
              style={{ background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' }}
            >
              Start free — keep the savings
              <ArrowRight size={15} strokeWidth={2.5} />
            </Link>
            <span className="text-[11px] text-[var(--text-faint)] leading-relaxed">
              Self-host is free forever ·{' '}
              <span className="inline-flex items-center gap-1 font-mono text-[var(--text-muted)]">
                <GitBranch size={11} /> AGPL-3.0
              </span>
            </span>
          </div>
        </div>
      </div>
    </div>
  )
}

// Re-export shared constants for any consumer that wants the raw list.
export { COMPETITORS, GITSTATE_DEFAULT }
