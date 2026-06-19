/**
 * BalanceRing — a compact SVG donut for one leave-type balance.
 * Shows remaining / total days with a coloured arc for the used portion.
 */
import { tint } from './leaveUtils.js'

export function BalanceRing({ type, balance, size = 72 }) {
  const entitled = balance?.entitledDays ?? type?.defaultDays ?? 0
  const carried = balance?.carriedDays ?? 0
  const used = balance?.usedDays ?? 0
  const total = entitled + carried
  const remaining = balance ? balance.remainingDays ?? Math.max(0, total - used) : total
  const pctUsed = total > 0 ? Math.min(1, used / total) : 0

  const color = type?.color ?? '#2DD4BF'
  const stroke = 7
  const r = (size - stroke) / 2
  const c = 2 * Math.PI * r
  const dash = c * pctUsed

  return (
    <div className="flex items-center gap-3.5">
      <div className="relative shrink-0" style={{ width: size, height: size }}>
        <svg width={size} height={size} className="-rotate-90">
          <circle
            cx={size / 2} cy={size / 2} r={r}
            fill="none" stroke="var(--border)" strokeWidth={stroke}
          />
          <circle
            cx={size / 2} cy={size / 2} r={r}
            fill="none" stroke={color} strokeWidth={stroke} strokeLinecap="round"
            strokeDasharray={`${dash} ${c - dash}`}
            className="transition-all duration-700"
          />
        </svg>
        <div className="absolute inset-0 flex flex-col items-center justify-center">
          <span className="text-base font-display font-semibold text-[var(--text)] tabular-nums leading-none">
            {Number.isInteger(remaining) ? remaining : remaining.toFixed(1)}
          </span>
          <span className="text-[9px] text-[var(--text-faint)] font-mono uppercase tracking-wide">left</span>
        </div>
      </div>
      <div className="min-w-0">
        <div className="flex items-center gap-1.5">
          <span
            className="w-2.5 h-2.5 rounded-full shrink-0"
            style={{ background: color, boxShadow: `0 0 0 3px ${tint(color, 0.18)}` }}
          />
          <span className="text-sm font-semibold text-[var(--text)] truncate">{type?.name ?? 'Leave'}</span>
        </div>
        <p className="text-xs text-[var(--text-faint)] mt-1 tabular-nums">
          {used % 1 === 0 ? used : used.toFixed(1)} used · {total % 1 === 0 ? total : total.toFixed(1)} total
          {carried > 0 && <span className="text-[var(--text-muted)]"> · +{carried % 1 === 0 ? carried : carried.toFixed(1)} carried</span>}
        </p>
      </div>
    </div>
  )
}
