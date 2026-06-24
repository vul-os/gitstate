/**
 * CostEstimator — interactive "what will I actually pay?" panel.
 *
 * A single builders slider (1–50) live-updates the monthly cost of every priced
 * tier, computed from the live /api/plans numbers and formatted through
 * useCurrency() (USD + the converted local amount). A managed↔BYOK segmented
 * control swaps each tier's per-builder rate. The recommended tier is surfaced
 * as a headline number; tiers whose builder cap is exceeded are flagged.
 *
 * Presentation-only: it never charges anything — it mirrors the live ladder so
 * buyers see real numbers before signing up.
 */
import { useMemo, useState } from 'react'
import { Users, Sparkles, KeyRound, TrendingUp } from 'lucide-react'
import { Card, Glow } from '../ui'
import { isEnterprise, RECOMMENDED_KEY } from './planData.js'

function Segmented({ value, onChange }) {
  const opts = [
    { key: 'managed', label: 'Managed AI', Icon: Sparkles },
    { key: 'byok', label: 'BYOK', Icon: KeyRound },
  ]
  return (
    <div className="inline-flex p-0.5 rounded-full border border-[var(--border)] bg-[var(--bg-surface3)]">
      {opts.map(({ key, label, Icon }) => {
        const active = value === key
        return (
          <button
            key={key}
            onClick={() => onChange(key)}
            className={[
              'inline-flex items-center gap-1.5 px-3.5 py-1.5 rounded-full text-xs font-medium transition-all duration-200 cursor-pointer',
              active
                ? 'text-[#0B1120] shadow-sm'
                : 'text-[var(--text-muted)] hover:text-[var(--text)]',
            ].join(' ')}
            style={active ? { background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' } : undefined}
          >
            <Icon size={12} strokeWidth={2.2} /> {label}
          </button>
        )
      })}
    </div>
  )
}

export default function CostEstimator({ plans, format }) {
  const [builders, setBuilders] = useState(8)
  const [mode, setMode] = useState('managed')

  const priced = useMemo(() => plans.filter(p => !isEnterprise(p)), [plans])
  const pct = ((builders - 1) / (50 - 1)) * 100

  const rows = useMemo(() => priced.map(plan => {
    const byok = mode === 'byok' && typeof plan.byokPerBuilderUsd === 'number'
    const rate = byok ? plan.byokPerBuilderUsd : (plan.perBuilderUsd ?? 0)
    const total = rate * builders
    const cap = plan.builders // 0 = unlimited
    const over = cap > 0 && builders > cap
    return { plan, rate, total, over, cap }
  }), [priced, mode, builders])

  const hero = rows.find(r => r.plan.key === RECOMMENDED_KEY) ?? rows[1] ?? rows[0]

  return (
    <Card padding="lg" className="relative overflow-hidden border-[var(--border2)]" glow>
      <Glow variant="indigo" size={420} className="-bottom-20 -right-10 opacity-25" />
      <div className="relative z-[1] grid grid-cols-1 lg:grid-cols-[minmax(0,360px)_1fr] gap-8 lg:gap-10">
        {/* Controls + headline */}
        <div className="flex flex-col gap-6">
          <div className="flex items-center justify-between gap-3">
            <div className="flex items-center gap-2 text-xs font-mono uppercase tracking-wider text-[var(--text-faint)]">
              <Users size={14} className="text-[#2DD4BF]" /> Builders
            </div>
            <Segmented value={mode} onChange={setMode} />
          </div>

          <div className="flex items-baseline gap-2">
            <span className="font-display text-5xl font-semibold text-[var(--text)] tabular-nums leading-none">{builders}</span>
            <span className="text-sm text-[var(--text-muted)]">builder{builders === 1 ? '' : 's'}</span>
          </div>

          <div className="flex flex-col gap-2">
            <input
              type="range"
              min={1}
              max={50}
              step={1}
              value={builders}
              onChange={e => setBuilders(Number(e.target.value))}
              aria-label="Number of builders"
              aria-valuetext={`${builders} builders`}
              className="w-full h-1.5 rounded-full appearance-none cursor-pointer outline-none
                focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--bg-surface)]
                [&::-webkit-slider-thumb]:appearance-none [&::-webkit-slider-thumb]:w-4 [&::-webkit-slider-thumb]:h-4
                [&::-webkit-slider-thumb]:rounded-full [&::-webkit-slider-thumb]:bg-[var(--brand-teal)]
                [&::-webkit-slider-thumb]:border-2 [&::-webkit-slider-thumb]:border-[var(--brand-indigo)]
                [&::-webkit-slider-thumb]:shadow-[var(--shadow-card)]
                [&::-webkit-slider-thumb]:transition-transform motion-reduce:[&::-webkit-slider-thumb]:transition-none
                [&::-webkit-slider-thumb]:hover:scale-110
                [&::-moz-range-thumb]:w-4 [&::-moz-range-thumb]:h-4 [&::-moz-range-thumb]:rounded-full
                [&::-moz-range-thumb]:bg-[var(--brand-teal)] [&::-moz-range-thumb]:border-2 [&::-moz-range-thumb]:border-solid
                [&::-moz-range-thumb]:border-[var(--brand-indigo)]"
              style={{
                background: `linear-gradient(90deg, #2DD4BF 0%, #6366F1 ${pct}%, var(--bg-surface3) ${pct}%)`,
              }}
            />
            <div className="flex justify-between text-[10px] font-mono text-[var(--text-faint)]">
              <span>1</span><span>50</span>
            </div>
          </div>

          {/* Headline — recommended tier */}
          {hero && (
            <div
              className="rounded-[var(--radius-card)] border border-[#2DD4BF]/30 p-4"
              style={{ background: 'linear-gradient(135deg, rgba(45,212,191,0.08), rgba(99,102,241,0.06))' }}
            >
              <div className="flex items-center gap-1.5 text-[10px] font-mono uppercase tracking-wider text-[#2DD4BF] mb-1.5">
                <TrendingUp size={11} /> At {builders} builders · {hero.plan.name}
              </div>
              <div className="flex items-baseline gap-2 flex-wrap">
                <span className="font-display text-3xl font-semibold text-[var(--text)] tabular-nums leading-none">
                  {format(hero.total, { minimumFractionDigits: 0, maximumFractionDigits: 0 })}
                </span>
                <span className="text-sm text-[var(--text-muted)]">/ mo</span>
              </div>
              <p className="text-[11px] text-[var(--text-faint)] mt-2">
                {format(hero.rate)} / builder · {mode === 'byok' ? 'your own AI key' : 'AI included'} · stakeholders free
              </p>
            </div>
          )}
        </div>

        {/* Per-tier breakdown */}
        <div className="flex flex-col gap-2.5">
          <div className="flex items-center justify-between text-[10px] font-mono uppercase tracking-wider text-[var(--text-faint)] px-1">
            <span>Plan</span><span>Monthly total</span>
          </div>
          {rows.map(({ plan, rate, total, over, cap }) => {
            const rec = plan.key === RECOMMENDED_KEY
            const free = plan.key === 'free'
            return (
              <div
                key={plan.key}
                className={[
                  'flex items-center justify-between gap-3 rounded-[var(--radius-badge)] border px-4 py-3 transition-colors',
                  rec ? 'border-[#2DD4BF]/35 bg-[#2DD4BF]/[0.05]' : 'border-[var(--border)] bg-[var(--bg-surface2)]/40',
                ].join(' ')}
              >
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <span className={['text-sm font-semibold', rec ? 'text-[#2DD4BF]' : 'text-[var(--text)]'].join(' ')}>{plan.name}</span>
                    {over && (
                      <span className="text-[9px] font-mono uppercase tracking-wide text-yellow-400 border border-yellow-500/30 bg-yellow-500/10 rounded px-1.5 py-0.5">
                        max {cap}
                      </span>
                    )}
                  </div>
                  <span className="text-[11px] text-[var(--text-faint)]">
                    {free ? 'free for ≤ 2 builders' : `${format(rate)} / builder`}
                  </span>
                </div>
                <div className="text-right shrink-0">
                  <div className="font-display text-lg font-semibold text-[var(--text)] tabular-nums leading-none">
                    {over ? '—' : format(total, { minimumFractionDigits: 0, maximumFractionDigits: 0 })}
                  </div>
                  {!over && !free && <span className="text-[10px] text-[var(--text-faint)]">/ mo</span>}
                </div>
              </div>
            )
          })}
          <p className="text-[10px] font-mono text-[var(--text-faint)] mt-1 px-1 leading-relaxed">
            Estimates exclude managed-AI usage beyond the included credit (metered at each model&apos;s standard rate). Stakeholders are always free.
          </p>
        </div>
      </div>
    </Card>
  )
}
