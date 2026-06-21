/**
 * StatCard — a polished quick-stat tile for dashboards.
 *
 * A big display-font number (tabular-nums), an uppercase mono label, an optional
 * accent icon in a tinted chip, an optional delta chip (▲ ok / ▼ bad with %),
 * and an optional sparkline (gradient under-fill, accent-colored).
 *
 * Usage:
 *   <StatCard
 *     label="Throughput"
 *     value="18/wk"
 *     accent="var(--chart-1)"
 *     icon={<GitMerge size={15} />}
 *     delta={{ value: 12, dir: 'up' }}
 *     spark={[3,5,4,6,7,6,9,8,11,10,12,14]}
 *     sublabel="issues closed per week"
 *   />
 */
import { Sparkline } from './Sparkline.jsx'

function DeltaChip({ delta }) {
  if (!delta || delta.value == null) return null
  // dir: 'up' | 'down' | 'neutral'. up→good unless `goodWhenDown`.
  const dir = delta.dir ?? (delta.value > 0 ? 'up' : delta.value < 0 ? 'down' : 'neutral')
  const good = delta.goodWhenDown ? dir === 'down' : dir === 'up'
  const isNeutral = dir === 'neutral'
  const color = isNeutral ? 'var(--text-muted)' : good ? 'var(--ok)' : 'var(--bad)'
  const arrow = dir === 'up' ? '▲' : dir === 'down' ? '▼' : '–'
  const text = delta.label ?? `${Math.abs(delta.value)}%`

  return (
    <span
      className="inline-flex items-center gap-1 rounded-full px-1.5 py-0.5 text-[10.5px] font-mono font-semibold tabular-nums leading-none"
      style={{ color, background: `color-mix(in srgb, ${color} 12%, transparent)` }}
      title={delta.title}
    >
      <span style={{ fontSize: 8, lineHeight: 1 }}>{arrow}</span>
      {text}
    </span>
  )
}

export function StatCard({
  label,
  value,
  sublabel,
  accent = 'var(--brand-teal)',
  icon,
  delta,
  spark,
  className = '',
}) {
  const hasSpark = Array.isArray(spark) && spark.filter(v => typeof v === 'number').length >= 2

  return (
    <div
      className={[
        'group relative flex flex-col rounded-[var(--radius-card)] border border-[var(--border)]',
        'bg-[var(--bg-surface)] p-5 overflow-hidden transition-all duration-200',
        'hover:border-[var(--border2)] hover:shadow-[var(--shadow-card-hover)]',
        className,
      ].join(' ')}
    >
      {/* hairline accent edge */}
      <span
        aria-hidden="true"
        className="absolute inset-y-0 left-0 w-[2px] opacity-70 transition-opacity group-hover:opacity-100"
        style={{ background: accent }}
      />

      <div className="flex items-start justify-between gap-3">
        <div className="flex items-center gap-2 min-w-0">
          {icon != null && (
            <span
              className="grid place-items-center w-6 h-6 rounded-[6px] shrink-0"
              style={{ color: accent, background: `color-mix(in srgb, ${accent} 14%, transparent)` }}
            >
              {icon}
            </span>
          )}
          <span className="text-[10.5px] font-mono uppercase tracking-[0.14em] text-[var(--text-faint)] truncate">
            {label}
          </span>
        </div>
        <DeltaChip delta={delta} />
      </div>

      <div className="mt-3 flex items-end justify-between gap-3">
        <span className="font-display text-[2rem] leading-[1.05] font-semibold text-[var(--text)] tabular-nums tracking-tight">
          {value}
        </span>
        {hasSpark && (
          <div className="shrink-0 pb-1 opacity-90">
            <Sparkline data={spark} color={accent} width={88} height={30} />
          </div>
        )}
      </div>

      {sublabel && (
        <span className="mt-1.5 text-xs text-[var(--text-faint)] leading-snug">{sublabel}</span>
      )}
    </div>
  )
}
