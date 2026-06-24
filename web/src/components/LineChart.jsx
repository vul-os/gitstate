/**
 * Hand-rolled SVG line chart — no external deps. Theme-aware (dark + light),
 * reduced-motion safe. Supports a single series (legacy `points`/`color`) or
 * multiple series via `series` using the `--chart-*` categorical palette.
 *
 * Props (single-series, legacy):
 *   points: Array<{ x: number|string, y: number, label?: string, raw?: any }>
 *   color:  string (default teal)
 *   areaColor: string — override the gradient under-fill
 *
 * Props (multi-series):
 *   series: Array<{ name: string, color?: string, points: Array<{x,y,raw?}> }>
 *           the first series' x-values drive the axis; series share an index.
 *
 * Shared:
 *   width, height: number
 *   xLabel: (point) => string   — x axis tick formatter
 *   yLabel: (value) => string   — y axis tick formatter
 *   tooltip: (point, seriesName?) => string — single-series tooltip text
 *   fill: boolean (default true) — area under-fill (single series only)
 *   legend: boolean (default true for multi-series)
 *   emptyText, emptyIcon
 *
 * Polish props (opt-in, default to the legacy look so existing callers render
 * unchanged):
 *   curve: 'linear' (default) | 'monotone' — smooth, rounded line interpolation
 *          (monotone Catmull-Rom that never overshoots, so counts stay ≥ their data).
 *   legendBelow: boolean — render a rich HTML legend UNDER the chart (rounded
 *          swatch + truncated name, wraps, theme-aware) instead of the cramped
 *          inline SVG legend. Multi-series only.
 *   areaFill: boolean — also draw a faint area under EACH series (multi-series),
 *          for the soft filled look. Single-series already fills via `fill`.
 *   tooltipRows: (idx) => Array<{ name, color, value }> — when set (multi-series),
 *          hovering shows a shared tooltip listing each series' value at that x.
 */
import { useState, useCallback, useId, useRef, useLayoutEffect } from 'react'

const PAD = { top: 18, right: 22, bottom: 38, left: 50 }
const PALETTE = ['var(--chart-1)', 'var(--chart-2)', 'var(--chart-3)', 'var(--chart-4)', 'var(--chart-5)', 'var(--chart-6)']

function clamp(v, lo, hi) { return Math.max(lo, Math.min(hi, v)) }

// Truncate a legend/label to keep the legend tidy without clipping mid-word ugly.
function truncate(s, n = 18) {
  s = String(s ?? '')
  return s.length > n ? s.slice(0, n - 1) + '…' : s
}

export function LineChart({
  points = [],
  series,
  width = 600,
  height = 200,
  color = 'var(--chart-1)',
  areaColor,
  xLabel,
  yLabel,
  tooltip,
  fill = true,
  legend,
  curve = 'linear',
  legendBelow = false,
  areaFill = false,
  tooltipRows,
  emptyText = 'No data yet.',
  emptyIcon = null,
  ariaLabel,
}) {
  const [hovered, setHovered] = useState(null)
  const gid = useId().replace(/:/g, '')

  // Responsive width: render the chart at its container's actual width so it
  // fills the card (the `width` prop is just the initial/fallback). Without this
  // the fixed viewBox + preserveAspectRatio left dead space in any wide card.
  const wrapRef = useRef(null)
  const [cw, setCw] = useState(width)
  useLayoutEffect(() => {
    const el = wrapRef.current
    if (!el || typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(([entry]) => {
      const w = Math.round(entry.contentRect.width)
      if (w > 0) setCw(w)
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  // Normalise to a series array. Single-series stays the visual default.
  const isMulti = Array.isArray(series) && series.length > 0
  const allSeries = isMulti
    ? series.map((s, i) => ({ name: s.name, color: s.color || PALETTE[i % PALETTE.length], points: s.points || [] }))
    : [{ name: '', color, points }]

  const xCount = Math.max(...allSeries.map(s => s.points.length), 0)
  const showLegend = legend ?? isMulti

  const W = cw - PAD.left - PAD.right
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

  if (!xCount) {
    return (
      <div
        className="flex flex-col items-center justify-center gap-2 rounded-[var(--radius-card)] text-center text-xs text-[var(--text-faint)] font-mono border border-dashed border-[var(--border)]"
        style={{ width: '100%', height, background: 'var(--bg-surface2)' }}
      >
        {emptyIcon}
        <span className="max-w-[80%]">{emptyText}</span>
      </div>
    )
  }

  const allYs = allSeries.flatMap(s => s.points.map(p => p.y).filter(v => typeof v === 'number'))
  const yMin = Math.min(...allYs, 0)
  const yMax = Math.max(...allYs, 1)
  const yRange = yMax - yMin || 1

  const toX = i => PAD.left + (W / Math.max(xCount - 1, 1)) * i
  const toY = v => PAD.top + H - ((v - yMin) / yRange) * H

  // Screen-space coordinates for a series' points (skipping non-numeric y).
  const coords = pts => pts.map((p, i) => ({ x: toX(i), y: typeof p.y === 'number' ? toY(p.y) : null }))

  // Monotone cubic (Fritsch–Carlson style tangents) → smooth, rounded lines that
  // never overshoot the data, so a count curve never dips below 0 between points.
  const monotonePath = (c) => {
    const pts = c.filter(p => p.y != null)
    const n = pts.length
    if (n === 0) return ''
    if (n < 3) return pts.map((p, i) => `${i === 0 ? 'M' : 'L'} ${p.x.toFixed(1)} ${p.y.toFixed(1)}`).join(' ')
    // secant slopes
    const dx = [], dy = [], m = []
    for (let i = 0; i < n - 1; i++) { dx[i] = pts[i + 1].x - pts[i].x; dy[i] = pts[i + 1].y - pts[i].y; m[i] = dy[i] / (dx[i] || 1) }
    const tan = [m[0]]
    for (let i = 1; i < n - 1; i++) {
      if (m[i - 1] * m[i] <= 0) tan[i] = 0
      else {
        const w1 = 2 * dx[i] + dx[i - 1], w2 = dx[i] + 2 * dx[i - 1]
        tan[i] = (w1 + w2) / (w1 / m[i - 1] + w2 / m[i])
      }
    }
    tan[n - 1] = m[n - 2]
    let d = `M ${pts[0].x.toFixed(1)} ${pts[0].y.toFixed(1)}`
    for (let i = 0; i < n - 1; i++) {
      const h = dx[i]
      const x1 = pts[i].x + h / 3, y1 = pts[i].y + (tan[i] * h) / 3
      const x2 = pts[i + 1].x - h / 3, y2 = pts[i + 1].y - (tan[i + 1] * h) / 3
      d += ` C ${x1.toFixed(1)} ${y1.toFixed(1)} ${x2.toFixed(1)} ${y2.toFixed(1)} ${pts[i + 1].x.toFixed(1)} ${pts[i + 1].y.toFixed(1)}`
    }
    return d
  }

  const linePath = pts => {
    const c = coords(pts)
    if (curve === 'monotone') return monotonePath(c)
    return c.filter(p => p.y != null)
      .map((p, i) => `${i === 0 ? 'M' : 'L'} ${p.x.toFixed(1)} ${p.y.toFixed(1)}`)
      .join(' ')
  }

  const areaPath = pts => {
    const c = coords(pts).filter(p => p.y != null)
    if (!c.length) return ''
    const baseY = (PAD.top + H).toFixed(1)
    const top = curve === 'monotone'
      ? monotonePath(c).replace(/^M/, 'L') // reuse the smooth top edge
      : c.map(p => `L ${p.x.toFixed(1)} ${p.y.toFixed(1)}`).join(' ')
    return `M ${c[0].x.toFixed(1)} ${baseY} ${top} L ${c[c.length - 1].x.toFixed(1)} ${baseY} Z`
  }

  const yTicks = Array.from({ length: 5 }, (_, i) => {
    const v = yMin + (yRange / 4) * i
    return { v, y: toY(v) }
  })

  const refPts = allSeries[0].points
  // Thin ticks so labels never overlap: aim for ~6-8 ticks, always keep the
  // first and last, and drop a tick that lands too close to the (right-anchored)
  // last one so the latest date stays readable.
  const targetTicks = clamp(Math.round(W / 90), 4, 9)
  const step = Math.max(1, Math.round(xCount / targetTicks))
  const xTicks = refPts
    .map((p, i) => ({ p, i }))
    .filter(({ i }) => i === 0 || i === xCount - 1 || i % step === 0)
    .filter(({ i }) => i === xCount - 1 || i === 0 || (xCount - 1 - i) >= Math.ceil(step / 2))

  const drawArea = fill && !isMulti

  // Text alternative: caller-provided label, else an auto summary describing the
  // series names and value range so the chart isn't an opaque image to AT.
  const autoLabel = (() => {
    const names = allSeries.map((s) => s.name).filter(Boolean)
    const lo = Number.isFinite(yMin) ? Math.round(yMin) : 0
    const hi = Number.isFinite(yMax) ? Math.round(yMax) : 0
    const who = names.length ? names.join(', ') + ' — ' : ''
    return `Line chart: ${who}${xCount} points, values from ${lo} to ${hi}.`
  })()
  const label = ariaLabel || autoLabel

  return (
    <div ref={wrapRef} style={{ position: 'relative', width: '100%', height }}>
      <svg
        viewBox={`0 0 ${cw} ${height}`}
        width="100%"
        height={height}
        preserveAspectRatio="none"
        role="img"
        aria-label={label}
        data-point-count={xCount}
        data-inner-w={W}
        onMouseMove={handleMouseMove}
        onMouseLeave={() => setHovered(null)}
        style={{ display: 'block', cursor: 'crosshair', maxWidth: '100%' }}
      >
        <defs>
          {allSeries.map((s, si) => (
            <linearGradient key={si} id={`area-${gid}-${si}`} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={s.color} stopOpacity="0.22" />
              <stop offset="100%" stopColor={s.color} stopOpacity="0.01" />
            </linearGradient>
          ))}
        </defs>

        {/* Y-axis hairline grid + faint mono labels */}
        {yTicks.map(({ v, y }, i) => (
          <g key={i}>
            <line
              x1={PAD.left} y1={y.toFixed(1)}
              x2={PAD.left + W} y2={y.toFixed(1)}
              stroke="var(--chart-grid)" strokeWidth="1"
              shapeRendering="crispEdges"
              strokeDasharray={i === 0 ? undefined : '2 4'}
            />
            <text
              x={PAD.left - 10} y={y.toFixed(1)}
              textAnchor="end" dominantBaseline="middle"
              fontSize="10" className="font-mono" fill="var(--chart-axis)"
            >
              {yLabel ? yLabel(v) : Math.round(v)}
            </text>
          </g>
        ))}

        {/* Area fill (single series) */}
        {drawArea && (
          <path d={areaPath(allSeries[0].points)} fill={areaColor || `url(#area-${gid}-0)`} />
        )}

        {/* Soft area fill under EACH series (opt-in, multi-series) */}
        {areaFill && isMulti && allSeries.map((s, si) => (
          <path key={`a${si}`} d={areaPath(s.points)} fill={`url(#area-${gid}-${si})`} />
        ))}

        {/* Lines */}
        {allSeries.map((s, si) => (
          <path
            key={si}
            d={linePath(s.points)}
            fill="none"
            stroke={s.color}
            strokeWidth="2"
            strokeLinejoin="round"
            strokeLinecap="round"
          />
        ))}

        {/* X axis ticks — first/last anchored inward so they don't clip */}
        {xTicks.map(({ p, i }) => {
          const isFirst = i === 0
          const isLast = i === xCount - 1
          const anchor = isLast ? 'end' : isFirst ? 'start' : 'middle'
          return (
            <text
              key={i}
              x={toX(i).toFixed(1)} y={PAD.top + H + 18}
              textAnchor={anchor}
              fontSize="10" className="font-mono" fill="var(--chart-axis)"
            >
              {xLabel ? xLabel(p) : p.x}
            </text>
          )
        })}

        {/* Hover crosshair + focus dot(s) */}
        {hovered != null && (
          <>
            <line
              x1={toX(hovered).toFixed(1)} y1={PAD.top}
              x2={toX(hovered).toFixed(1)} y2={PAD.top + H}
              stroke="var(--text-faint)" strokeWidth="1" strokeOpacity="0.45" strokeDasharray="3 3"
            />
            {allSeries.map((s, si) => {
              const p = s.points[hovered]
              if (!p || typeof p.y !== 'number') return null
              return (
                <circle
                  key={si}
                  cx={toX(hovered).toFixed(1)} cy={toY(p.y).toFixed(1)}
                  r="4" fill={s.color} stroke="var(--chart-dot-ring)" strokeWidth="2"
                />
              )
            })}
          </>
        )}

        {/* Inline SVG legend (multi-series) — suppressed when legendBelow renders
            the richer HTML legend under the chart instead. */}
        {showLegend && isMulti && !legendBelow && (
          <g transform={`translate(${PAD.left + W}, ${PAD.top - 4})`}>
            {allSeries.map((s, si) => {
              const offset = (allSeries.length - si) * -84
              return (
                <g key={si} transform={`translate(${offset}, 0)`}>
                  <line x1="0" y1="0" x2="14" y2="0" stroke={s.color} strokeWidth="2.5" strokeLinecap="round" />
                  <text x="19" y="3.5" fontSize="10" className="font-mono" fill="var(--text-muted)">{truncate(s.name, 16)}</text>
                </g>
              )
            })}
          </g>
        )}
      </svg>

      {/* Tooltip (single-series) */}
      {hovered != null && !isMulti && allSeries[0].points[hovered] && (
        <div
          style={{
            position: 'absolute',
            left: clamp(toX(hovered) + 12, 0, cw - 180),
            top: clamp(toY(allSeries[0].points[hovered].y) - 42, 0, height - 32),
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
            maxWidth: 240,
            overflow: 'hidden',
            textOverflow: 'ellipsis',
          }}
        >
          {tooltip ? tooltip(allSeries[0].points[hovered]) : `${yLabel ? yLabel(allSeries[0].points[hovered].y) : allSeries[0].points[hovered].y}`}
        </div>
      )}

      {/* Tooltip (multi-series) — shared crosshair card listing each series'
          value at the hovered x. Opt-in via tooltipRows so existing multi-series
          callers without it keep their dot-only hover. */}
      {hovered != null && isMulti && tooltipRows && (() => {
        const rows = tooltipRows(hovered) || []
        if (!rows.length) return null
        return (
          <div
            style={{
              position: 'absolute',
              left: clamp(toX(hovered) + 12, 0, cw - 200),
              top: clamp(PAD.top + 4, 0, height - 40),
              pointerEvents: 'none',
              background: 'var(--bg-surface)',
              border: '1px solid var(--border2)',
              borderRadius: 'var(--radius-badge)',
              padding: '7px 10px',
              zIndex: 10,
              boxShadow: 'var(--shadow-float)',
              maxWidth: 240,
            }}
          >
            {rows[0]?.header && (
              <div className="text-[10px] font-mono text-[var(--text-faint)] mb-1.5">{rows[0].header}</div>
            )}
            <div className="flex flex-col gap-1">
              {rows.map((r, i) => (
                <div key={i} className="flex items-center gap-2 text-[11px]" style={{ fontFamily: 'var(--font-mono)' }}>
                  <span className="inline-block w-2.5 h-2.5 rounded-[3px] shrink-0" style={{ background: r.color }} />
                  <span className="text-[var(--text-muted)] truncate" style={{ maxWidth: 120 }}>{truncate(r.name, 16)}</span>
                  <span className="ml-auto text-[var(--text)] tabular-nums">{r.value}</span>
                </div>
              ))}
            </div>
          </div>
        )
      })()}

    </div>
  )
}

// ContributorLegend — a compact, height-bounded legend (rounded swatch + the
// canonical person name) rendered ABOVE a chart. Bounded so it never grows the
// card / causes page scroll; overflows internally for large contributor sets.
export function ContributorLegend({ series }) {
  if (!Array.isArray(series) || series.length === 0) return null
  return (
    <div className="flex flex-wrap items-center gap-x-3.5 gap-y-1.5 mb-3 max-h-[3.25rem] overflow-y-auto pr-1">
      {series.map((s, si) => (
        <span key={si} className="inline-flex items-center gap-1.5 min-w-0" title={s.name}>
          <span className="inline-block w-2.5 h-2.5 rounded-full shrink-0" style={{ background: s.color }} />
          <span className="text-[11px] font-mono text-[var(--text-muted)] truncate max-w-[150px]">{truncate(s.name, 22)}</span>
        </span>
      ))}
    </div>
  )
}
