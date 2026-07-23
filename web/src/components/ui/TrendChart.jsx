/**
 * TrendChart — responsive line/area chart with a crosshair tooltip.
 *
 * One y-scale, always. Two measures of different magnitude get two charts, not
 * a second axis. Multi-series input therefore assumes a shared unit (merged PRs
 * vs closed issues — both counts), and any series count ≥ 2 renders a legend so
 * identity is never carried by colour alone.
 *
 * Props:
 *   series   [{ key, label, color, points: [{ x, y }] }]   x = label string
 *   height   number (default 220)
 *   area     boolean — gradient under-fill (single series only)
 *   yFormat  (n) => string
 *   xFormat  (x, index) => string   axis tick text
 *   valueSuffix string — appended in the tooltip
 */
import { useMemo, useState, useId } from 'react'
import { useMeasure } from '../../lib/useMeasure.js'

const PAD = { top: 12, right: 12, bottom: 22, left: 40 }

/** "Nice" axis ceiling so gridlines land on round numbers. */
function niceMax(max) {
  if (max <= 0) return 1
  const pow = Math.pow(10, Math.floor(Math.log10(max)))
  const n = max / pow
  const step = n <= 1 ? 1 : n <= 2 ? 2 : n <= 5 ? 5 : 10
  return step * pow
}

export function TrendChart({
  series = [],
  height = 220,
  area = false,
  yFormat = (n) => (n >= 1000 ? `${(n / 1000).toFixed(1)}k` : String(Math.round(n))),
  xFormat = (x) => x,
  valueSuffix = '',
  emptyLabel = 'No data in this range',
}) {
  const gid = useId().replace(/:/g, '')
  const [ref, width] = useMeasure()
  const [cursor, setCursor] = useState(null)

  const live = series.filter((s) => s.points?.length)
  const n = live[0]?.points.length ?? 0

  const { max, plotW, plotH, ticks } = useMemo(() => {
    const rawMax = Math.max(0, ...live.flatMap((s) => s.points.map((p) => p.y ?? 0)))
    const m = niceMax(rawMax)
    return {
      max: m,
      plotW: Math.max(0, width - PAD.left - PAD.right),
      plotH: Math.max(0, height - PAD.top - PAD.bottom),
      ticks: [0, 0.25, 0.5, 0.75, 1].map((f) => m * f),
    }
  }, [live, width, height])

  if (!live.length || n === 0) {
    return (
      <div
        ref={ref}
        style={{ height }}
        className="flex items-center justify-center text-sm text-[var(--text-faint)]"
      >
        {emptyLabel}
      </div>
    )
  }

  const toX = (i) => PAD.left + (n === 1 ? plotW / 2 : (plotW / (n - 1)) * i)
  const toY = (v) => PAD.top + plotH - (max === 0 ? 0 : ((v ?? 0) / max) * plotH)

  // ~6 evenly spaced x labels, always ending on the last point. A final tick
  // closer than half a stride is REPLACED rather than appended — appending it
  // is what makes the last two labels overlap into gibberish.
  const labelEvery = Math.max(1, Math.ceil(n / 6))
  const xTicks = []
  for (let i = 0; i < n; i += labelEvery) xTicks.push(i)
  const lastTick = xTicks[xTicks.length - 1]
  if (lastTick !== n - 1) {
    if (n - 1 - lastTick < labelEvery / 2) xTicks.pop()
    xTicks.push(n - 1)
  }

  function handleMove(e) {
    if (!plotW) return
    const rect = e.currentTarget.getBoundingClientRect()
    const rel = e.clientX - rect.left - PAD.left
    const i = Math.round((rel / plotW) * (n - 1))
    setCursor(Math.max(0, Math.min(n - 1, i)))
  }

  const showLegend = live.length >= 2

  return (
    <div ref={ref} className="relative">
      {width > 0 && (
        <svg
          width={width}
          height={height}
          role="img"
          aria-label={live.map((s) => s.label).join(', ')}
          // Stable hooks for tests and debugging: icon <svg>s from the icon set
          // also carry role="img", so structural selectors alone are ambiguous.
          data-chart="trend"
          data-series={live.length}
          data-points={n}
          style={{ display: 'block' }}
          onMouseMove={handleMove}
          onMouseLeave={() => setCursor(null)}
        >
          <defs>
            {live.map((s) => (
              <linearGradient key={s.key} id={`fill-${gid}-${s.key}`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={s.color} stopOpacity="0.22" />
                <stop offset="100%" stopColor={s.color} stopOpacity="0" />
              </linearGradient>
            ))}
          </defs>

          {/* Recessive horizontal grid + y labels. No vertical grid: it would
              compete with the data at this density. */}
          {ticks.map((t) => (
            <g key={t}>
              <line
                x1={PAD.left}
                x2={PAD.left + plotW}
                y1={toY(t)}
                y2={toY(t)}
                stroke="var(--chart-grid)"
                strokeWidth="1"
              />
              <text
                x={PAD.left - 6}
                y={toY(t) + 3}
                textAnchor="end"
                className="font-mono"
                fontSize="9.5"
                fill="var(--chart-axis)"
              >
                {yFormat(t)}
              </text>
            </g>
          ))}

          {xTicks.map((i) => (
            <text
              key={i}
              x={toX(i)}
              y={height - 6}
              textAnchor={i === 0 ? 'start' : i === n - 1 ? 'end' : 'middle'}
              className="font-mono"
              fontSize="9.5"
              fill="var(--chart-axis)"
            >
              {xFormat(live[0].points[i]?.x, i)}
            </text>
          ))}

          {live.map((s) => {
            const path = s.points
              .map((p, i) => `${i === 0 ? 'M' : 'L'} ${toX(i).toFixed(1)} ${toY(p.y).toFixed(1)}`)
              .join(' ')
            return (
              <g key={s.key}>
                {area && live.length === 1 && (
                  <path
                    d={`${path} L ${toX(n - 1).toFixed(1)} ${(PAD.top + plotH).toFixed(1)} L ${toX(0).toFixed(1)} ${(PAD.top + plotH).toFixed(1)} Z`}
                    fill={`url(#fill-${gid}-${s.key})`}
                  />
                )}
                <path
                  d={path}
                  data-trend-line={s.key}
                  fill="none"
                  stroke={s.color}
                  strokeWidth="2"
                  strokeLinejoin="round"
                  strokeLinecap="round"
                />
              </g>
            )
          })}

          {cursor != null && (
            <g pointerEvents="none">
              <line
                x1={toX(cursor)}
                x2={toX(cursor)}
                y1={PAD.top}
                y2={PAD.top + plotH}
                stroke="var(--border2)"
                strokeWidth="1"
              />
              {live.map((s) => {
                const p = s.points[cursor]
                if (!p) return null
                return (
                  <circle
                    key={s.key}
                    cx={toX(cursor)}
                    cy={toY(p.y)}
                    r="4"
                    fill={s.color}
                    stroke="var(--chart-dot-ring)"
                    strokeWidth="2"
                  />
                )
              })}
            </g>
          )}
        </svg>
      )}

      {cursor != null && width > 0 && (
        <div
          className="pointer-events-none absolute z-10 rounded-[var(--radius-btn)] border border-[var(--border2)] bg-[var(--bg-surface)] px-2.5 py-1.5 shadow-[var(--shadow-float)]"
          style={{
            left: Math.min(Math.max(toX(cursor) - 60, 0), Math.max(0, width - 140)),
            top: 0,
          }}
        >
          <div className="font-mono text-[10px] text-[var(--text-faint)]">
            {live[0].points[cursor]?.x}
          </div>
          {live.map((s) => (
            <div key={s.key} className="flex items-center gap-1.5 whitespace-nowrap">
              <span className="h-2 w-2 rounded-[2px]" style={{ background: s.color }} />
              <span className="font-mono text-xs tabular-nums text-[var(--text)]">
                {yFormat(s.points[cursor]?.y ?? 0)}
                {valueSuffix}
              </span>
              {showLegend && (
                <span className="text-[10px] text-[var(--text-faint)]">{s.label}</span>
              )}
            </div>
          ))}
        </div>
      )}

      {showLegend && (
        <div className="mt-2 flex flex-wrap items-center gap-4">
          {live.map((s) => (
            <span key={s.key} className="flex items-center gap-1.5 text-[11px] text-[var(--text-muted)]">
              <span className="h-2 w-2 rounded-[2px]" style={{ background: s.color }} />
              {s.label}
            </span>
          ))}
        </div>
      )}
    </div>
  )
}
