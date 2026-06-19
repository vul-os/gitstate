/**
 * Shared hand-rolled SVG components for the Contribution page.
 * Components only (pure helpers live in ./helpers.js). No chart deps —
 * every glyph is raw SVG, theme-aware via CSS tokens.
 */
import { DIMENSIONS, dimColor } from '../../lib/useContribution.js'
import { clamp01to100, hueFromStr, fmtNum } from './helpers.js'

// ── avatar ──────────────────────────────────────────────────────────────────

export function Avatar({ name, isAgentBot = false, size = 36 }) {
  const initials =
    (name || '?').split(/[\s@._-]+/).filter(Boolean).slice(0, 2).map((w) => w[0]).join('').toUpperCase() || '?'
  if (isAgentBot) {
    return (
      <div
        className="rounded-[10px] flex items-center justify-center select-none shrink-0 border border-[var(--brand-indigo)]/40 text-[var(--brand-indigo)]"
        style={{ width: size, height: size, background: 'rgba(99,102,241,0.12)' }}
        aria-hidden
      >
        <svg width={size * 0.5} height={size * 0.5} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9">
          <rect x="4" y="8" width="16" height="11" rx="2.5" />
          <path strokeLinecap="round" d="M12 8V4M9 4.5h6M9 13.5h.01M15 13.5h.01" />
        </svg>
      </div>
    )
  }
  const hue = hueFromStr(name)
  return (
    <div
      className="rounded-full flex items-center justify-center font-bold text-[var(--bg)] select-none shrink-0"
      style={{
        width: size, height: size, fontSize: size * 0.36,
        background: `linear-gradient(135deg, hsl(${hue} 68% 58%), hsl(${(hue + 48) % 360} 68% 52%))`,
      }}
      aria-hidden
    >
      {initials}
    </div>
  )
}

// ── composite ring ────────────────────────────────────────────────────────────

/** Gradient-accented progress ring with the 0–100 composite at its centre. */
export function CompositeRing({ value, size = 88, stroke = 7, delta = null }) {
  const v = clamp01to100(value)
  const r = (size - stroke) / 2
  const c = 2 * Math.PI * r
  const dash = (v / 100) * c
  const gid = `ring-grad-${size}`
  return (
    <div className="relative shrink-0" style={{ width: size, height: size }}>
      <svg width={size} height={size} className="rotate-[-90deg]" aria-hidden>
        <defs>
          <linearGradient id={gid} x1="0%" y1="0%" x2="100%" y2="100%">
            <stop offset="0%" stopColor="var(--brand-teal)" />
            <stop offset="100%" stopColor="var(--brand-indigo)" />
          </linearGradient>
        </defs>
        <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="var(--border)" strokeWidth={stroke} />
        <circle
          cx={size / 2} cy={size / 2} r={r} fill="none"
          stroke={`url(#${gid})`} strokeWidth={stroke} strokeLinecap="round"
          strokeDasharray={`${dash} ${c - dash}`}
          style={{ transition: 'stroke-dasharray 0.5s cubic-bezier(0.22,1,0.36,1)' }}
        />
      </svg>
      <div className="absolute inset-0 flex flex-col items-center justify-center">
        <span className="font-display font-semibold tabular-nums leading-none text-[var(--text)]" style={{ fontSize: size * 0.3 }}>
          {Math.round(v)}
        </span>
        {delta != null && Math.abs(delta) >= 0.5 && (
          <span
            className="font-mono leading-none mt-0.5 tabular-nums"
            style={{ fontSize: size * 0.12, color: delta > 0 ? 'var(--brand-teal)' : 'var(--text-faint)' }}
          >
            {delta > 0 ? '▲' : '▼'}{Math.abs(delta).toFixed(0)}
          </span>
        )}
      </div>
    </div>
  )
}

// ── radar (5-axis) ──────────────────────────────────────────────────────────

/** Five-axis radar of the per-dimension scores. Pure SVG. */
export function Radar({ dimensions, size = 132, showLabels = true }) {
  const cx = size / 2
  const cy = size / 2
  const R = size / 2 - (showLabels ? 22 : 6)
  const n = DIMENSIONS.length
  const angleFor = (i) => -Math.PI / 2 + (i * 2 * Math.PI) / n
  const pt = (i, radius) => [cx + radius * Math.cos(angleFor(i)), cy + radius * Math.sin(angleFor(i))]

  const rings = [0.25, 0.5, 0.75, 1]
  const axisPts = DIMENSIONS.map((_, i) => pt(i, R))
  const valuePts = DIMENSIONS.map((d, i) => pt(i, R * (clamp01to100(dimensions?.[d.key]?.score) / 100)))
  const poly = (pts) => pts.map((p) => p.join(',')).join(' ')

  return (
    <svg width={size} height={size} className="overflow-visible" aria-hidden>
      <defs>
        <radialGradient id="radar-fill" cx="50%" cy="50%" r="50%">
          <stop offset="0%" stopColor="var(--brand-teal)" stopOpacity="0.28" />
          <stop offset="100%" stopColor="var(--brand-indigo)" stopOpacity="0.16" />
        </radialGradient>
      </defs>
      {rings.map((rr, ri) => (
        <polygon
          key={ri}
          points={poly(DIMENSIONS.map((_, i) => pt(i, R * rr)))}
          fill="none" stroke="var(--border)" strokeWidth="1"
          opacity={ri === rings.length - 1 ? 0.9 : 0.5}
        />
      ))}
      {axisPts.map((p, i) => (
        <line key={i} x1={cx} y1={cy} x2={p[0]} y2={p[1]} stroke="var(--border)" strokeWidth="1" opacity="0.5" />
      ))}
      <polygon
        points={poly(valuePts)}
        fill="url(#radar-fill)"
        stroke="var(--brand-teal)" strokeWidth="1.5" strokeLinejoin="round"
        style={{ transition: 'all 0.45s cubic-bezier(0.22,1,0.36,1)' }}
      />
      {valuePts.map((p, i) => (
        <circle key={i} cx={p[0]} cy={p[1]} r="2.4" fill={dimColor(DIMENSIONS[i].key, 62)} />
      ))}
      {showLabels && DIMENSIONS.map((d, i) => {
        const [lx, ly] = pt(i, R + 13)
        const anchor = Math.abs(lx - cx) < 4 ? 'middle' : lx > cx ? 'start' : 'end'
        return (
          <text
            key={d.key} x={lx} y={ly} textAnchor={anchor} dominantBaseline="middle"
            className="font-mono" fontSize="8.5" fill="var(--text-faint)" letterSpacing="0.04em"
          >
            {d.short.toUpperCase()}
          </text>
        )
      })}
    </svg>
  )
}

// ── stacked dimension bars (compact, for cards) ───────────────────────────────

export function DimensionBars({ dimensions, compact = false }) {
  return (
    <div className={compact ? 'space-y-1.5' : 'space-y-2'}>
      {DIMENSIONS.map((d) => {
        const score = clamp01to100(dimensions?.[d.key]?.score)
        return (
          <div key={d.key} className="flex items-center gap-2">
            <span className="w-[52px] shrink-0 text-[10px] font-mono uppercase tracking-wide text-[var(--text-faint)]">
              {d.short}
            </span>
            <div className="flex-1 h-1.5 rounded-full bg-[var(--bg-surface3)] overflow-hidden">
              <div
                className="h-full rounded-full"
                style={{
                  width: `${score}%`,
                  background: `linear-gradient(90deg, ${dimColor(d.key, 52)}, ${dimColor(d.key, 66)})`,
                  transition: 'width 0.45s cubic-bezier(0.22,1,0.36,1)',
                }}
              />
            </div>
            <span className="w-7 shrink-0 text-right text-[11px] font-mono tabular-nums text-[var(--text-muted)]">
              {Math.round(score)}
            </span>
          </div>
        )
      })}
    </div>
  )
}

// ── authorship split bar (human vs agent commits) ─────────────────────────────

export function AuthorshipBar({ authorship, withLabel = true }) {
  const human = Math.max(0, authorship?.humanCommits ?? 0)
  const agent = Math.max(0, authorship?.agentCommits ?? 0)
  const total = human + agent
  const agentPct = authorship?.agentPct != null ? authorship.agentPct : total ? (agent / total) * 100 : 0
  const humanPct = 100 - agentPct
  return (
    <div className="space-y-1">
      <div className="flex h-1.5 w-full rounded-full overflow-hidden bg-[var(--bg-surface3)]" title={`${human} human · ${agent} agent commits`}>
        <div style={{ width: `${humanPct}%`, background: 'var(--brand-teal)', transition: 'width 0.4s ease' }} />
        <div style={{ width: `${agentPct}%`, background: 'var(--brand-indigo)', transition: 'width 0.4s ease' }} />
      </div>
      {withLabel && (
        <div className="flex items-center justify-between text-[10px] font-mono text-[var(--text-faint)]">
          <span className="text-[var(--brand-teal)]">{fmtNum(human)} authored</span>
          <span className="text-[var(--brand-indigo)]">{Math.round(agentPct)}% agent-assisted</span>
        </div>
      )}
    </div>
  )
}
