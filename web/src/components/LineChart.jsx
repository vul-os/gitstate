/**
 * Hand-rolled SVG line chart — no external deps. Theme-aware (dark + light).
 * Props:
 *   points: Array<{ x: number|string, y: number, label?: string }>
 *   width: number (default 600)
 *   height: number (default 200)
 *   color: string (default 'var(--brand-teal)')
 *   areaColor: string (default rgba(45,212,191,0.07))
 *   xLabel: (point) => string  — optional x axis tick formatter
 *   yLabel: (value) => string  — optional y axis tick formatter
 *   tooltip: (point) => string — optional tooltip text
 *   emptyText: string
 *   emptyIcon: ReactNode — optional icon shown above empty text
 */
import { useState, useCallback, useId } from 'react'

const PAD = { top: 16, right: 20, bottom: 36, left: 48 }

function clamp(v, lo, hi) { return Math.max(lo, Math.min(hi, v)) }

export function LineChart({
  points = [],
  width = 600,
  height = 200,
  color = '#2DD4BF',
  areaColor,
  xLabel,
  yLabel,
  tooltip,
  emptyText = 'No data yet.',
  emptyIcon = null,
}) {
  const [hovered, setHovered] = useState(null)
  const gid = useId().replace(/:/g, '')

  const W = width - PAD.left - PAD.right
  const H = height - PAD.top - PAD.bottom

  const handleMouseMove = useCallback((e) => {
    const svg = e.currentTarget
    const rect = svg.getBoundingClientRect()
    // account for responsive scaling (svg may render narrower than its width attr)
    const scale = rect.width / svg.viewBox.baseVal.width || 1
    const mouseX = (e.clientX - rect.left) / scale - PAD.left
    const n = svg.dataset.pointCount ? Number(svg.dataset.pointCount) : 1
    const w = svg.dataset.innerW   ? Number(svg.dataset.innerW)    : 1
    const idx = clamp(Math.round(mouseX / (w / Math.max(n - 1, 1))), 0, n - 1)
    setHovered(idx)
  }, [])

  if (!points.length) {
    return (
      <div
        className="flex flex-col items-center justify-center gap-2 rounded-[var(--radius-card)] text-center text-xs text-[var(--text-faint)] font-mono border border-dashed border-[var(--border)]"
        style={{ width: '100%', maxWidth: width, height, background: 'var(--bg)' }}
      >
        {emptyIcon}
        <span className="max-w-[80%]">{emptyText}</span>
      </div>
    )
  }

  const ys = points.map(p => p.y)
  const yMin = Math.min(...ys, 0)
  const yMax = Math.max(...ys)
  const yRange = yMax - yMin || 1

  const toX = i => PAD.left + (W / Math.max(points.length - 1, 1)) * i
  const toY = v => PAD.top + H - ((v - yMin) / yRange) * H

  const pathD = points
    .map((p, i) => `${i === 0 ? 'M' : 'L'} ${toX(i).toFixed(1)} ${toY(p.y).toFixed(1)}`)
    .join(' ')

  const areaD = [
    `M ${toX(0).toFixed(1)} ${(PAD.top + H).toFixed(1)}`,
    ...points.map((p, i) => `L ${toX(i).toFixed(1)} ${toY(p.y).toFixed(1)}`),
    `L ${toX(points.length - 1).toFixed(1)} ${(PAD.top + H).toFixed(1)}`,
    'Z',
  ].join(' ')

  const yTicks = Array.from({ length: 5 }, (_, i) => {
    const v = yMin + (yRange / 4) * i
    return { v, y: toY(v) }
  })

  const step = Math.max(1, Math.floor(points.length / 6))
  const xTicks = points
    .map((p, i) => ({ p, i }))
    .filter(({ i }) => i === 0 || i === points.length - 1 || i % step === 0)

  const hovPoint = hovered != null ? points[hovered] : null
  const fillArea = areaColor || `url(#area-${gid})`

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
          <linearGradient id={`area-${gid}`} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={color} stopOpacity="0.22" />
            <stop offset="100%" stopColor={color} stopOpacity="0.01" />
          </linearGradient>
        </defs>

        {/* Y-axis grid lines */}
        {yTicks.map(({ v, y }, i) => (
          <g key={i}>
            <line
              x1={PAD.left} y1={y.toFixed(1)}
              x2={PAD.left + W} y2={y.toFixed(1)}
              stroke="var(--border)" strokeWidth="1"
              strokeDasharray={i === 0 ? undefined : '2 3'}
            />
            <text
              x={PAD.left - 8} y={y.toFixed(1)}
              textAnchor="end" dominantBaseline="middle"
              fontSize="10" className="font-mono" fill="var(--text-faint)"
            >
              {yLabel ? yLabel(v) : Math.round(v)}
            </text>
          </g>
        ))}

        {/* Area fill */}
        <path d={areaD} fill={fillArea} />

        {/* Line */}
        <path
          d={pathD}
          fill="none"
          stroke={color}
          strokeWidth="2"
          strokeLinejoin="round"
          strokeLinecap="round"
        />

        {/* X axis ticks — anchor the first/last labels inward so they don't
            clip past the chart edges (last tick sits at the right plot edge). */}
        {xTicks.map(({ p, i }) => {
          const isFirst = i === 0
          const isLast = i === points.length - 1
          const anchor = isLast ? 'end' : isFirst ? 'start' : 'middle'
          return (
            <text
              key={i}
              x={toX(i).toFixed(1)} y={PAD.top + H + 18}
              textAnchor={anchor}
              fontSize="10" className="font-mono" fill="var(--text-faint)"
            >
              {xLabel ? xLabel(p) : p.x}
            </text>
          )
        })}

        {/* Hover crosshair + dot */}
        {hovered != null && hovPoint && (
          <>
            <line
              x1={toX(hovered).toFixed(1)} y1={PAD.top}
              x2={toX(hovered).toFixed(1)} y2={PAD.top + H}
              stroke={color} strokeWidth="1" strokeOpacity="0.4" strokeDasharray="3 3"
            />
            <circle
              cx={toX(hovered).toFixed(1)} cy={toY(hovPoint.y).toFixed(1)}
              r="4" fill={color} stroke="var(--bg)" strokeWidth="2"
            />
          </>
        )}
      </svg>

      {/* Tooltip */}
      {hovered != null && hovPoint && (
        <div
          style={{
            position: 'absolute',
            left: clamp(toX(hovered) + 10, 0, width - 160),
            top: clamp(toY(hovPoint.y) - 38, 0, height - 30),
            pointerEvents: 'none',
            background: 'var(--bg)',
            border: '1px solid var(--border2)',
            borderRadius: 'var(--radius-badge)',
            padding: '5px 10px',
            fontSize: 11,
            color: 'var(--text)',
            whiteSpace: 'nowrap',
            zIndex: 10,
            fontFamily: 'var(--font-mono)',
            boxShadow: 'var(--shadow-float)',
            maxWidth: 220,
            overflow: 'hidden',
            textOverflow: 'ellipsis',
          }}
        >
          {tooltip ? tooltip(hovPoint) : `${yLabel ? yLabel(hovPoint.y) : hovPoint.y}`}
        </div>
      )}
    </div>
  )
}
