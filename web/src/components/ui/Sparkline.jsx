/**
 * Sparkline — tiny hand-rolled SVG trend line, no deps, reduced-motion safe.
 *
 * Renders ~12 points into a compact accent-colored line with a soft gradient
 * under-fill and an end dot. Designed to sit inside a StatCard. Theme-aware
 * via the `--chart-*` / brand CSS vars passed through `color`.
 *
 * Props:
 *   data:   number[]            series values (>=2 to draw; else nothing)
 *   width:  number  (default 96)
 *   height: number  (default 28)
 *   color:  string  (default 'var(--brand-teal)')
 *   strokeWidth: number (default 1.75)
 *   fill:   boolean (default true) — gradient under-fill
 *   dot:    boolean (default true) — end-of-series dot
 */
import { useId } from 'react'

export function Sparkline({
  data = [],
  width = 96,
  height = 28,
  color = 'var(--brand-teal)',
  strokeWidth = 1.75,
  fill = true,
  dot = true,
}) {
  const gid = useId().replace(/:/g, '')
  const vals = (data || []).filter(v => typeof v === 'number' && isFinite(v))
  if (vals.length < 2) {
    return <svg width={width} height={height} aria-hidden="true" />
  }

  const pad = strokeWidth + 1
  const W = width - pad * 2
  const H = height - pad * 2
  const min = Math.min(...vals)
  const max = Math.max(...vals)
  const range = max - min || 1

  const toX = i => pad + (W / (vals.length - 1)) * i
  const toY = v => pad + H - ((v - min) / range) * H

  const line = vals.map((v, i) => `${i === 0 ? 'M' : 'L'} ${toX(i).toFixed(1)} ${toY(v).toFixed(1)}`).join(' ')
  const area = `${line} L ${toX(vals.length - 1).toFixed(1)} ${(pad + H).toFixed(1)} L ${toX(0).toFixed(1)} ${(pad + H).toFixed(1)} Z`

  const lastX = toX(vals.length - 1)
  const lastY = toY(vals[vals.length - 1])

  return (
    <svg width={width} height={height} viewBox={`0 0 ${width} ${height}`} aria-hidden="true" style={{ display: 'block', overflow: 'visible' }}>
      <defs>
        <linearGradient id={`spark-${gid}`} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity="0.28" />
          <stop offset="100%" stopColor={color} stopOpacity="0" />
        </linearGradient>
      </defs>
      {fill && <path d={area} fill={`url(#spark-${gid})`} />}
      <path d={line} fill="none" stroke={color} strokeWidth={strokeWidth} strokeLinejoin="round" strokeLinecap="round" />
      {dot && (
        <circle cx={lastX.toFixed(1)} cy={lastY.toFixed(1)} r={strokeWidth + 0.5} fill={color} stroke="var(--chart-dot-ring)" strokeWidth="1.5" />
      )}
    </svg>
  )
}
