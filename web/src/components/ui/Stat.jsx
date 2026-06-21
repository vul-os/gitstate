/**
 * Stat — a single inline metric (label + big number + optional delta).
 *
 * The lightweight, chrome-free sibling of <StatCard>: no border/background, for
 * placing inside an existing surface or a dense row. Uses the display font for
 * the number (tabular-nums) and semantic --ok / --bad for delta direction.
 *
 * For the full tile (icon chip, sparkline, accent edge) use <StatCard>.
 *
 * Usage:
 *   <Stat label="Cycle time" value="4.2d" delta="+0.3d" deltaDir="up" />
 *   <Stat label="Open PRs" value={42} />
 */
export function Stat({
  label,
  value,
  delta,
  deltaDir, // 'up' | 'down' | 'neutral'
  sublabel,
  className = '',
}) {
  const deltaColor =
    deltaDir === 'up'   ? 'var(--ok)' :
    deltaDir === 'down' ? 'var(--bad)' :
    'var(--text-muted)'

  return (
    <div className={['flex flex-col gap-1', className].join(' ')}>
      <span className="text-[10.5px] font-mono uppercase tracking-[0.14em] text-[var(--text-faint)]">
        {label}
      </span>
      <div className="flex items-baseline gap-2">
        <span className="font-display text-[2rem] leading-[1.05] font-semibold text-[var(--text)] tabular-nums tracking-tight">
          {value}
        </span>
        {delta && (
          <span className="text-xs font-mono font-semibold tabular-nums" style={{ color: deltaColor }}>
            {delta}
          </span>
        )}
      </div>
      {sublabel && (
        <span className="text-xs text-[var(--text-faint)]">{sublabel}</span>
      )}
    </div>
  )
}
