/**
 * Contribution-over-time charts — all hand-rolled SVG, theme-aware via CSS tokens.
 *
 *   - Sparkline   : a tiny inline trend line (for roster cards / drawer).
 *   - TrendsChart : a multi-line chart of composite per member across periods,
 *     with a legend + hover-free, reduced-motion-safe rendering.
 *
 * Series shape (from useContributionTrends): { userId, name, isAgentBot,
 *   points:[{ periodStart, composite }] } (oldest→newest).
 */
import { useState } from 'react'
import { hueFromStr } from './helpers.js'

// ── Sparkline ──────────────────────────────────────────────────────────────

/** A compact trend line over a member's composites. points: number[] (oldest→newest). */
export function Sparkline({ points = [], width = 92, height = 26, stroke = 1.75, color }) {
  const vals = points.map((p) => (typeof p === 'number' ? p : Number(p?.composite) || 0))
  if (vals.length < 2) {
    return (
      <div className="flex items-center justify-center text-[10px] font-mono text-[var(--text-faint)]" style={{ width, height }}>
        —
      </div>
    )
  }
  const min = Math.min(...vals)
  const max = Math.max(...vals)
  const span = max - min || 1
  const pad = stroke + 1
  const x = (i) => pad + (i * (width - 2 * pad)) / (vals.length - 1)
  const y = (v) => height - pad - ((v - min) / span) * (height - 2 * pad)
  const d = vals.map((v, i) => `${i === 0 ? 'M' : 'L'}${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(' ')
  const c = color || 'var(--brand-teal)'
  const rising = vals[vals.length - 1] >= vals[0]
  const last = vals[vals.length - 1]
  return (
    <svg width={width} height={height} className="overflow-visible shrink-0" aria-hidden>
      <path d={d} fill="none" stroke={c} strokeWidth={stroke} strokeLinecap="round" strokeLinejoin="round" opacity="0.85" />
      <circle cx={x(vals.length - 1)} cy={y(last)} r={2.2} fill={rising ? 'var(--brand-teal)' : 'var(--text-faint)'} />
    </svg>
  )
}

// ── Multi-line chart ─────────────────────────────────────────────────────────

function fmtMonth(s) {
  if (!s) return ''
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleDateString(undefined, { month: 'short' })
}

const lineColor = (s, i) =>
  s.isAgentBot ? 'var(--brand-indigo)' : `hsl(${hueFromStr(s.name || s.userId || String(i))} 70% 58%)`

/**
 * Multi-line chart of composite (0–100) per member over the period series.
 * Pure SVG, fixed 0–100 y-domain so lines are comparable. Includes a togglable
 * legend so a busy team can isolate one member.
 */
export function TrendsChart({ series = [], height = 240 }) {
  const [activeId, setActiveId] = useState(null)

  const withPoints = series.filter((s) => Array.isArray(s.points) && s.points.length > 0)
  const maxLen = withPoints.reduce((m, s) => Math.max(m, s.points.length), 0)
  if (maxLen < 2) {
    return (
      <div className="flex items-center justify-center text-xs text-[var(--text-faint)] py-10">
        Not enough history yet — trends appear once a few periods are recorded.
      </div>
    )
  }

  // Layout in a 0..100 viewBox-ish coordinate space (responsive via width=100%).
  const W = 760
  const H = height
  const padL = 30
  const padR = 12
  const padT = 14
  const padB = 26
  const innerW = W - padL - padR
  const innerH = H - padT - padB

  const x = (i) => padL + (i * innerW) / (maxLen - 1)
  const y = (v) => padT + innerH - (Math.max(0, Math.min(100, v)) / 100) * innerH

  // x labels from the longest series' periodStarts.
  const labelSeries = withPoints.reduce((a, b) => (b.points.length >= (a?.points.length ?? 0) ? b : a), null)
  const gridY = [0, 25, 50, 75, 100]

  return (
    <div className="w-full">
      <svg viewBox={`0 0 ${W} ${H}`} className="w-full" style={{ height: H }} preserveAspectRatio="none" aria-hidden>
        {/* y gridlines + labels */}
        {gridY.map((g) => (
          <g key={g}>
            <line x1={padL} y1={y(g)} x2={W - padR} y2={y(g)} stroke="var(--border)" strokeWidth="1" opacity={g === 0 ? 0.9 : 0.4} />
            <text x={padL - 6} y={y(g) + 3} textAnchor="end" fontSize="9" className="font-mono" fill="var(--text-faint)">{g}</text>
          </g>
        ))}
        {/* x labels */}
        {labelSeries?.points.map((p, i) => (
          <text key={i} x={x(i)} y={H - 8} textAnchor="middle" fontSize="9" className="font-mono" fill="var(--text-faint)">
            {fmtMonth(p.periodStart)}
          </text>
        ))}
        {/* lines */}
        {withPoints.map((s, si) => {
          const dimmed = activeId && activeId !== s.userId
          const col = lineColor(s, si)
          const d = s.points
            .map((p, i) => `${i === 0 ? 'M' : 'L'}${x(i).toFixed(1)},${y(p.composite).toFixed(1)}`)
            .join(' ')
          return (
            <g key={s.userId || si} opacity={dimmed ? 0.12 : 1} style={{ transition: 'opacity 0.25s ease' }}>
              <path d={d} fill="none" stroke={col} strokeWidth={activeId === s.userId ? 2.6 : 1.8} strokeLinejoin="round" strokeLinecap="round" />
              {s.points.map((p, i) => (
                <circle key={i} cx={x(i)} cy={y(p.composite)} r={activeId === s.userId ? 2.8 : 1.8} fill={col} />
              ))}
            </g>
          )
        })}
      </svg>

      {/* legend */}
      <div className="mt-3 flex flex-wrap gap-x-4 gap-y-1.5">
        {withPoints.map((s, si) => {
          const active = activeId === s.userId
          return (
            <button
              key={s.userId || si}
              onClick={() => setActiveId(active ? null : s.userId)}
              className={[
                'inline-flex items-center gap-1.5 text-[11px] cursor-pointer transition-opacity',
                activeId && !active ? 'opacity-40 hover:opacity-70' : 'opacity-100',
              ].join(' ')}
              title="Click to isolate this member"
            >
              <span className="h-2 w-2 rounded-full shrink-0" style={{ background: lineColor(s, si) }} />
              <span className="text-[var(--text-dim)]">{s.name || s.email}</span>
              <span className="font-mono tabular-nums text-[var(--text-faint)]">
                {Math.round(s.points[s.points.length - 1]?.composite ?? 0)}
              </span>
            </button>
          )
        })}
      </div>
    </div>
  )
}
