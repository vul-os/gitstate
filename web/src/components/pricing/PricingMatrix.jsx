/**
 * PricingMatrix — full feature-by-tier comparison table for the Pricing page.
 *
 * Columns are built from the live /api/plans list (so plan names + prices match
 * the ladder), grouped rows come from COMPARE_GROUPS. Price/credit rows read the
 * live per-builder + included-LLM numbers and format through useCurrency().
 * The recommended column is highlighted; the whole thing scrolls horizontally
 * (with a sticky feature column) on narrow screens.
 */
import { Check, Minus } from 'lucide-react'
import { Card } from '../ui'
import {
  COMPARE_GROUPS, RECOMMENDED_KEY, planByKey, isEnterprise,
} from './planData.js'

function priceCell(row, plan, format) {
  if (!plan) return '—'
  if (isEnterprise(plan)) return 'Custom'
  if (row.priceKey === 'managed') {
    return plan.perBuilderUsd === 0 ? 'Free' : `${format(plan.perBuilderUsd, { minimumFractionDigits: 0, maximumFractionDigits: 0 })}`
  }
  if (row.priceKey === 'byok') {
    if (typeof plan.byokPerBuilderUsd !== 'number') return plan.key === 'free' ? 'BYOK' : '—'
    return `${format(plan.byokPerBuilderUsd, { minimumFractionDigits: 0, maximumFractionDigits: 0 })}`
  }
  if (row.creditRow) {
    return plan.includedLlmUsd ? `${format(plan.includedLlmUsd, { minimumFractionDigits: 0, maximumFractionDigits: 0 })}` : '—'
  }
  return '—'
}

function Cell({ value, accent }) {
  if (typeof value === 'boolean') {
    return value
      ? <Check size={16} className="inline text-[#2DD4BF]" strokeWidth={2.5} />
      : <Minus size={14} className="inline text-[var(--text-faint)]/50" />
  }
  if (value === '∞') {
    return <span className="font-mono text-base text-[#818cf8]">∞</span>
  }
  return (
    <span className={['font-mono text-[13px] tabular-nums', accent ? 'text-[#818cf8]' : 'text-[var(--text-dim)]'].join(' ')}>
      {value}
    </span>
  )
}

export default function PricingMatrix({ plans, format }) {
  const cols = plans.map(p => ({ key: p.key, name: p.name, plan: p }))

  return (
    <Card padding="none" className="overflow-hidden border-[var(--border2)]">
      <div className="overflow-x-auto">
        <table className="w-full border-collapse text-sm min-w-[760px]">
          <thead>
            <tr className="border-b border-[var(--border)]">
              <th className="sticky left-0 z-[2] bg-[var(--bg-surface)] text-left font-normal px-5 py-4 text-[11px] font-mono uppercase tracking-widest text-[var(--text-faint)] min-w-[220px]">
                Compare plans
              </th>
              {cols.map(col => {
                const rec = col.key === RECOMMENDED_KEY
                return (
                  <th
                    key={col.key}
                    className="px-4 py-4 text-center min-w-[120px]"
                    style={rec ? { background: 'rgba(45,212,191,0.06)' } : undefined}
                  >
                    <span className={['font-display text-sm font-semibold', rec ? 'text-[#2DD4BF]' : 'text-[var(--text)]'].join(' ')}>
                      {col.name}
                    </span>
                    {rec && (
                      <span className="block text-[9px] font-mono uppercase tracking-wider text-[#2DD4BF]/80 mt-0.5">popular</span>
                    )}
                  </th>
                )
              })}
            </tr>
          </thead>
          <tbody>
            {COMPARE_GROUPS.map(group => (
              <GroupBody key={group.title} group={group} cols={cols} format={format} />
            ))}
          </tbody>
        </table>
      </div>
    </Card>
  )
}

function GroupBody({ group, cols, format }) {
  return (
    <>
      <tr>
        <td
          colSpan={cols.length + 1}
          className="sticky left-0 bg-[var(--bg-surface2)]/60 px-5 py-2.5 text-[10px] font-mono uppercase tracking-[0.16em] text-[var(--text-faint)] border-y border-[var(--border)]"
        >
          {group.title}
        </td>
      </tr>
      {group.rows.map(row => {
        const Icon = row.icon
        return (
          <tr key={row.label} className="border-b border-[var(--border)] last:border-b-0 hover:bg-[var(--bg-surface2)]/40 transition-colors">
            <td className="sticky left-0 z-[1] bg-[var(--bg-surface)] px-5 py-3.5">
              <span className="flex items-center gap-2.5 text-[var(--text-dim)]">
                <Icon size={14} className="text-[var(--text-faint)] shrink-0" strokeWidth={1.8} />
                {row.label}
              </span>
            </td>
            {cols.map(col => {
              const rec = col.key === RECOMMENDED_KEY
              let value
              if (row.priceKey || row.creditRow) {
                value = priceCell(row, planByKey([col.plan], col.key) ?? col.plan, format)
              } else {
                const idx = ['free', 'starter', 'pro', 'scale', 'enterprise'].indexOf(col.key)
                value = row.vals?.[idx] ?? '—'
              }
              return (
                <td
                  key={col.key}
                  className="px-4 py-3.5 text-center"
                  style={rec ? { background: 'rgba(45,212,191,0.045)' } : undefined}
                >
                  <Cell value={value} accent={row.accent} />
                </td>
              )
            })}
          </tr>
        )
      })}
    </>
  )
}
