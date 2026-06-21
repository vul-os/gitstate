/**
 * BurndownChart — renders a burndown for a given project.
 * Data from GET /api/reports/burndown?project=<id>
 * Hand-rolled SVG (dual-line: remaining vs ideal), theme-aware via --chart-*,
 * reduced-motion safe, with hairline grid, gradient under-fill, a hover
 * crosshair + focus dots and a tooltip.
 */
import { useState, useCallback, useId } from 'react'
import { useBurndown } from '../lib/useBurndown.js'

function Spinner() {
  return (
    <svg className="animate-spin" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="var(--brand-teal)" strokeWidth="2">
      <path d="M21 12a9 9 0 1 1-6.219-8.56" strokeLinecap="round" />
    </svg>
  )
}

const PAD = { top: 18, right: 22, bottom: 38, left: 50 }
const C_REMAIN = 'var(--chart-1)'
const C_IDEAL = 'var(--chart-2)'

function clamp(v, lo, hi) { return Math.max(lo, Math.min(hi, v)) }

function DualLineChart({ points, width = 600, height = 200 }) {
  const [hovered, setHovered] = useState(null)
  const gid = useId().replace(/:/g, '')

  const W = width - PAD.left - PAD.right
  const H = height - PAD.top - PAD.bottom

  const handleMouseMove = useCallback((e) => {
    const svg = e.currentTarget
    const rect = svg.getBoundingClientRect()
    const scale = rect.width / svg.viewBox.baseVal.width || 1
    const mouseX = (e.clientX - rect.left) / scale - PAD.left
    const n = svg.dataset.pointCount ? Number(svg.dataset.pointCount) : 1
    const w = svg.dataset.innerW ? Number(svg.dataset.innerW) : 1
    const idx = clamp(Math.round(mouseX / (w / Math.max(n - 1, 1))), 0, n - 1)
    setHovered(idx)
  }, [])

  if (!points.length) {
    return (
      <div
        className="flex items-center justify-center rounded-[var(--radius-card)] text-xs text-[var(--text-faint)] font-mono border border-dashed border-[var(--border)]"
        style={{ width: '100%', maxWidth: width, height, background: 'var(--bg-surface2)' }}
      >
        No burndown data yet.
      </div>
    )
  }

  const allVals = points.flatMap(p => [p.remaining, p.ideal].filter(v => v != null))
  const yMax = Math.max(...allVals, 1)
  const yMin = 0

  const toX = i => PAD.left + (W / Math.max(points.length - 1, 1)) * i
  const toY = v => PAD.top + H - ((v - yMin) / (yMax - yMin)) * H

  const remainingPath = points
    .filter(p => p.remaining != null)
    .map((p, i) => `${i === 0 ? 'M' : 'L'} ${toX(points.indexOf(p)).toFixed(1)} ${toY(p.remaining).toFixed(1)}`)
    .join(' ')

  const idealPath = points
    .filter(p => p.ideal != null)
    .map((p, i) => `${i === 0 ? 'M' : 'L'} ${toX(points.indexOf(p)).toFixed(1)} ${toY(p.ideal).toFixed(1)}`)
    .join(' ')

  const areaPath = [
    `M ${toX(0).toFixed(1)} ${(PAD.top + H).toFixed(1)}`,
    ...points.filter(p => p.remaining != null).map((p) => `L ${toX(points.indexOf(p)).toFixed(1)} ${toY(p.remaining).toFixed(1)}`),
    `L ${toX(points.length - 1).toFixed(1)} ${(PAD.top + H).toFixed(1)}`,
    'Z',
  ].join(' ')

  const yTicks = Array.from({ length: 5 }, (_, i) => {
    const v = yMax - (yMax / 4) * i
    return { v, y: toY(v) }
  })

  const step = Math.max(1, Math.floor(points.length / 6))
  const xTicks = points
    .map((p, i) => ({ p, i }))
    .filter(({ i }) => i === 0 || i === points.length - 1 || i % step === 0)

  const fmtDate = (d) => {
    const dt = new Date(d)
    return isNaN(dt) ? d : `${dt.getMonth() + 1}/${dt.getDate()}`
  }

  const hov = hovered != null ? points[hovered] : null

  return (
    <div style={{ position: 'relative', width: '100%', maxWidth: width, height }}>
      <svg
        viewBox={`0 0 ${width} ${height}`}
        width="100%"
        height={height}
        preserveAspectRatio="xMidYMid meet"
        data-point-count={points.length}
        data-inner-w={W}
        onMouseMove={handleMouseMove}
        onMouseLeave={() => setHovered(null)}
        style={{ display: 'block', cursor: 'crosshair', maxWidth: '100%' }}
      >
        <defs>
          <linearGradient id={`bd-${gid}`} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={C_REMAIN} stopOpacity="0.22" />
            <stop offset="100%" stopColor={C_REMAIN} stopOpacity="0.01" />
          </linearGradient>
        </defs>

        {/* Y grid + faint labels */}
        {yTicks.map(({ v, y }, i) => (
          <g key={i}>
            <line
              x1={PAD.left} y1={y.toFixed(1)} x2={PAD.left + W} y2={y.toFixed(1)}
              stroke="var(--chart-grid)" strokeWidth="1" shapeRendering="crispEdges"
              strokeDasharray={i === yTicks.length - 1 ? undefined : '2 4'}
            />
            <text x={PAD.left - 10} y={y.toFixed(1)} textAnchor="end" dominantBaseline="middle" fontSize="10" className="font-mono" fill="var(--chart-axis)">
              {Math.round(v)}
            </text>
          </g>
        ))}

        {/* Remaining area */}
        {remainingPath && <path d={areaPath} fill={`url(#bd-${gid})`} />}

        {/* Ideal line (dashed) */}
        {idealPath && (
          <path d={idealPath} fill="none" stroke={C_IDEAL} strokeWidth="1.5" strokeDasharray="5 4" strokeOpacity="0.7" strokeLinecap="round" />
        )}

        {/* Remaining line */}
        {remainingPath && (
          <path d={remainingPath} fill="none" stroke={C_REMAIN} strokeWidth="2" strokeLinejoin="round" strokeLinecap="round" />
        )}

        {/* X axis ticks */}
        {xTicks.map(({ p, i }) => {
          const isFirst = i === 0
          const isLast = i === points.length - 1
          const anchor = isLast ? 'end' : isFirst ? 'start' : 'middle'
          return (
            <text key={i} x={toX(i).toFixed(1)} y={PAD.top + H + 18} textAnchor={anchor} fontSize="10" className="font-mono" fill="var(--chart-axis)">
              {fmtDate(p.date)}
            </text>
          )
        })}

        {/* Hover crosshair + focus dots */}
        {hov && (
          <>
            <line
              x1={toX(hovered).toFixed(1)} y1={PAD.top} x2={toX(hovered).toFixed(1)} y2={PAD.top + H}
              stroke="var(--text-faint)" strokeWidth="1" strokeOpacity="0.45" strokeDasharray="3 3"
            />
            {hov.ideal != null && (
              <circle cx={toX(hovered).toFixed(1)} cy={toY(hov.ideal).toFixed(1)} r="3.5" fill={C_IDEAL} stroke="var(--chart-dot-ring)" strokeWidth="2" />
            )}
            {hov.remaining != null && (
              <circle cx={toX(hovered).toFixed(1)} cy={toY(hov.remaining).toFixed(1)} r="4" fill={C_REMAIN} stroke="var(--chart-dot-ring)" strokeWidth="2" />
            )}
          </>
        )}

        {/* Legend */}
        <g transform={`translate(${PAD.left + W - 124}, ${PAD.top - 6})`}>
          <line x1="0" y1="6" x2="16" y2="6" stroke={C_REMAIN} strokeWidth="2" strokeLinecap="round" />
          <text x="20" y="9.5" fontSize="10" className="font-mono" fill="var(--text-muted)">remaining</text>
          <line x1="0" y1="20" x2="16" y2="20" stroke={C_IDEAL} strokeWidth="1.5" strokeDasharray="5 4" />
          <text x="20" y="23.5" fontSize="10" className="font-mono" fill="var(--text-muted)">ideal</text>
        </g>
      </svg>

      {/* Tooltip */}
      {hov && (
        <div
          style={{
            position: 'absolute',
            left: clamp(toX(hovered) + 12, 0, width - 190),
            top: clamp(toY(hov.remaining ?? hov.ideal ?? 0) - 50, 0, height - 44),
            pointerEvents: 'none',
            background: 'var(--bg-surface)',
            border: '1px solid var(--border2)',
            borderRadius: 'var(--radius-badge)',
            padding: '6px 10px',
            fontSize: 11,
            color: 'var(--text)',
            whiteSpace: 'nowrap',
            zIndex: 10,
            fontFamily: 'var(--font-mono)',
            boxShadow: 'var(--shadow-float)',
            lineHeight: 1.5,
          }}
        >
          <div className="text-[var(--text-faint)]">{(() => { const dt = new Date(hov.date); return isNaN(dt) ? hov.date : dt.toLocaleDateString() })()}</div>
          {hov.remaining != null && <div style={{ color: C_REMAIN }}>remaining: {Math.round(hov.remaining)}</div>}
          {hov.ideal != null && <div style={{ color: C_IDEAL }}>ideal: {Math.round(hov.ideal)}</div>}
        </div>
      )}
    </div>
  )
}

/**
 * Drop-in burndown chart for a project.
 * @param {{ projectId: string }} props
 */
export function BurndownChart({ projectId }) {
  const { points, loading, error } = useBurndown(projectId)

  if (!projectId) return null

  return (
    <div>
      <div className="flex items-center justify-between mb-5">
        <div>
          <h3 className="text-sm font-semibold text-[var(--text)]">Burndown</h3>
          <p className="text-xs text-[var(--text-faint)] mt-0.5">Remaining work vs ideal — derived from issue state</p>
        </div>
        {loading && <Spinner />}
      </div>

      {error && (
        <div className="rounded-[var(--radius-badge)] px-4 py-3 text-xs text-[var(--bad)] bg-[color-mix(in_srgb,var(--bad)_8%,transparent)] border border-[color-mix(in_srgb,var(--bad)_24%,transparent)]">
          {error}
        </div>
      )}

      {!error && (
        <div className="overflow-x-auto">
          <DualLineChart points={loading ? [] : points} />
        </div>
      )}
    </div>
  )
}
