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
 */
import { useState, useCallback, useId } from 'react'

const PAD = { top: 18, right: 22, bottom: 38, left: 50 }
const PALETTE = ['var(--chart-1)', 'var(--chart-2)', 'var(--chart-3)', 'var(--chart-4)', 'var(--chart-5)', 'var(--chart-6)']

function clamp(v, lo, hi) { return Math.max(lo, Math.min(hi, v)) }

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
  emptyText = 'No data yet.',
  emptyIcon = null,
}) {
  const [hovered, setHovered] = useState(null)
  const gid = useId().replace(/:/g, '')

  // Normalise to a series array. Single-series stays the visual default.
  const isMulti = Array.isArray(series) && series.length > 0
  const allSeries = isMulti
    ? series.map((s, i) => ({ name: s.name, color: s.color || PALETTE[i % PALETTE.length], points: s.points || [] }))
    : [{ name: '', color, points }]

  const xCount = Math.max(...allSeries.map(s => s.points.length), 0)
  const showLegend = legend ?? isMulti

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

  if (!xCount) {
    return (
      <div
        className="flex flex-col items-center justify-center gap-2 rounded-[var(--radius-card)] text-center text-xs text-[var(--text-faint)] font-mono border border-dashed border-[var(--border)]"
        style={{ width: '100%', maxWidth: width, height, background: 'var(--bg-surface2)' }}
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

  const linePath = pts => pts
    .map((p, i) => `${i === 0 ? 'M' : 'L'} ${toX(i).toFixed(1)} ${toY(p.y).toFixed(1)}`)
    .join(' ')

  const areaPath = pts => [
    `M ${toX(0).toFixed(1)} ${(PAD.top + H).toFixed(1)}`,
    ...pts.map((p, i) => `L ${toX(i).toFixed(1)} ${toY(p.y).toFixed(1)}`),
    `L ${toX(pts.length - 1).toFixed(1)} ${(PAD.top + H).toFixed(1)}`,
    'Z',
  ].join(' ')

  const yTicks = Array.from({ length: 5 }, (_, i) => {
    const v = yMin + (yRange / 4) * i
    return { v, y: toY(v) }
  })

  const refPts = allSeries[0].points
  const step = Math.max(1, Math.floor(xCount / 6))
  const xTicks = refPts
    .map((p, i) => ({ p, i }))
    .filter(({ i }) => i === 0 || i === xCount - 1 || i % step === 0)

  const drawArea = fill && !isMulti

  return (
    <div style={{ position: 'relative', width: '100%', maxWidth: width, height }}>
      <svg
        viewBox={`0 0 ${width} ${height}`}
        width="100%"
        height={height}
        preserveAspectRatio="xMidYMid meet"
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

        {/* Inline legend (multi-series) */}
        {showLegend && isMulti && (
          <g transform={`translate(${PAD.left + W}, ${PAD.top - 4})`}>
            {allSeries.map((s, si) => {
              const offset = (allSeries.length - si) * -84
              return (
                <g key={si} transform={`translate(${offset}, 0)`}>
                  <line x1="0" y1="0" x2="14" y2="0" stroke={s.color} strokeWidth="2.5" strokeLinecap="round" />
                  <text x="19" y="3.5" fontSize="10" className="font-mono" fill="var(--text-muted)">{s.name}</text>
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
            left: clamp(toX(hovered) + 12, 0, width - 180),
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
    </div>
  )
}
