/**
 * VelocityReadout — recent delivery rate (merged PRs / closed issues per week),
 * the team's blended mean, and a least-squares trend, with a small SVG sparkline.
 */
import { useMemo } from 'react'
import { TrendingUp, TrendingDown, Minus, Gauge } from 'lucide-react'
import { Card } from '../ui/index.js'

function Sparkline({ points = [], width = 240, height = 56 }) {
  const view = useMemo(() => {
    const ys = points.map(p => Math.max(p.issues ?? 0, p.prs ?? 0))
    const max = Math.max(1, ...ys)
    return { ys, max }
  }, [points])

  if (points.length < 2) return null
  const pad = 4
  const w = width - pad * 2
  const h = height - pad * 2
  const step = w / (view.ys.length - 1)
  const coords = view.ys.map((y, i) => [pad + i * step, pad + h - (y / view.max) * h])
  const line = coords.map(([x, y], i) => `${i === 0 ? 'M' : 'L'}${x.toFixed(1)},${y.toFixed(1)}`).join(' ')
  const area = `${line} L${(pad + w).toFixed(1)},${(pad + h).toFixed(1)} L${pad.toFixed(1)},${(pad + h).toFixed(1)} Z`

  return (
    <svg viewBox={`0 0 ${width} ${height}`} width="100%" height={height} className="overflow-visible">
      <defs>
        <linearGradient id="velArea" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor="var(--brand-teal)" stopOpacity="0.28" />
          <stop offset="100%" stopColor="var(--brand-teal)" stopOpacity="0" />
        </linearGradient>
      </defs>
      <path d={area} fill="url(#velArea)" />
      <path d={line} fill="none" stroke="var(--brand-teal)" strokeWidth="2" strokeLinejoin="round" strokeLinecap="round" />
      {coords.map(([x, y], i) => (
        <circle key={i} cx={x} cy={y} r={i === coords.length - 1 ? 3 : 1.6} fill="var(--brand-teal)" />
      ))}
    </svg>
  )
}

const TREND = {
  accelerating: { Icon: TrendingUp, color: 'var(--ok)', label: 'accelerating' },
  slowing: { Icon: TrendingDown, color: 'var(--warn)', label: 'slowing' },
  steady: { Icon: Minus, color: 'var(--text-muted)', label: 'steady' },
}

export function VelocityReadout({ velocity }) {
  const v = velocity ?? {}
  const trend = TREND[v.trendLabel] ?? TREND.steady
  const TrendIcon = trend.Icon

  return (
    <Card padding="md" className="flex flex-col gap-3">
      <div className="flex items-center justify-between">
        <span className="text-[10px] font-mono uppercase tracking-widest text-[var(--text-faint)] flex items-center gap-1.5">
          <Gauge size={11} /> Velocity
        </span>
        <span className="inline-flex items-center gap-1 text-[11px] font-medium" style={{ color: trend.color }}>
          <TrendIcon size={12} /> {trend.label}
        </span>
      </div>

      <div className="flex items-end justify-between gap-3">
        <div className="flex items-baseline gap-1.5">
          <span className="text-3xl font-display font-semibold text-[var(--text)] tabular-nums leading-none">
            {v.hasData ? (v.meanPerWeek ?? 0) : '—'}
          </span>
          <span className="text-xs text-[var(--text-faint)] font-mono">items / week</span>
        </div>
      </div>

      {v.hasData ? (
        <Sparkline points={v.points ?? []} />
      ) : (
        <p className="text-xs text-[var(--text-faint)] py-2">
          No completed work in the recent window yet — ship a PR or close an issue to seed the rate.
        </p>
      )}

      <div className="flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-[var(--text-muted)] font-mono tabular-nums pt-1 border-t border-[var(--border)]">
        <span>{v.meanPRs ?? 0} PRs/wk</span>
        <span>{v.meanIssues ?? 0} issues/wk</span>
        <span className="text-[var(--text-faint)]">over {v.sampleWeeks ?? 0} wks</span>
      </div>
    </Card>
  )
}
