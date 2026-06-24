/**
 * UsageBreakdown — a per-kind usage card grid for the current billing period.
 *
 * One tile per metered dimension (builder seats, managed LLM, repo sync), each
 * with a tinted icon chip, the quantity, and the accrued USD cost (with an
 * optional ZAR estimate derived from the most-recent invoice FX rate — the usage
 * endpoint itself is USD-only, so ZAR is shown as an estimate, never invented).
 *
 * Renders a tasteful empty state when nothing has accrued yet (Free / $0), so the
 * section never collapses to a blank panel.
 */

const usdFmt2 = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', minimumFractionDigits: 2 })
const zarFmt = new Intl.NumberFormat('en-ZA', { style: 'currency', currency: 'ZAR', minimumFractionDigits: 2 })

function fmtUsd(usd) {
  return usdFmt2.format(usd ?? 0)
}

// ── Icons ──────────────────────────────────────────────────────────────────────

const SparkIcon = (
  <svg width="20" height="20" fill="none" viewBox="0 0 24 24" stroke="var(--text-faint)" strokeWidth="1.6">
    <path strokeLinecap="round" strokeLinejoin="round" d="M3 13.125C3 12.504 3.504 12 4.125 12h2.25c.621 0 1.125.504 1.125 1.125v6.75C7.5 20.496 6.996 21 6.375 21h-2.25A1.125 1.125 0 0 1 3 19.875v-6.75ZM9.75 8.625c0-.621.504-1.125 1.125-1.125h2.25c.621 0 1.125.504 1.125 1.125v11.25c0 .621-.504 1.125-1.125 1.125h-2.25a1.125 1.125 0 0 1-1.125-1.125V8.625ZM16.5 4.125c0-.621.504-1.125 1.125-1.125h2.25C20.496 3 21 3.504 21 4.125v15.75c0 .621-.504 1.125-1.125 1.125h-2.25a1.125 1.125 0 0 1-1.125-1.125V4.125Z" />
  </svg>
)

// ── One usage tile ──────────────────────────────────────────────────────────────

function UsageTile({ icon, accent, label, qty, qtyUnit, costUSD, costZAR, note, free }) {
  return (
    <div
      className="group relative flex flex-col rounded-[var(--radius-card)] p-4 overflow-hidden transition-all duration-200 hover:border-[var(--border2)] hover:shadow-[var(--shadow-card-hover)]"
      style={{ background: 'var(--bg-surface)', border: '1px solid var(--border)' }}
    >
      <span
        aria-hidden="true"
        className="absolute inset-y-0 left-0 w-[2px] opacity-70 transition-opacity group-hover:opacity-100"
        style={{ background: accent }}
      />
      <div className="flex items-center gap-2 mb-3">
        <span
          className="grid place-items-center w-7 h-7 rounded-[7px] shrink-0"
          style={{ color: accent, background: `color-mix(in srgb, ${accent} 14%, transparent)` }}
        >
          {icon}
        </span>
        <span className="text-[10.5px] font-mono uppercase tracking-[0.14em] text-[var(--text-faint)] truncate">
          {label}
        </span>
      </div>

      <div className="flex items-end justify-between gap-3">
        <span className="font-display text-[1.6rem] leading-[1.05] font-semibold text-[var(--text)] tabular-nums tracking-tight">
          {qty}
          {qtyUnit && <span className="text-sm font-medium text-[var(--text-faint)] ml-1">{qtyUnit}</span>}
        </span>
        <div className="text-right shrink-0">
          {free ? (
            <span className="text-xs font-semibold text-[var(--brand-teal)]">Included</span>
          ) : (
            <>
              <p className="text-sm font-bold text-[var(--text)] tabular-nums">{fmtUsd(costUSD)}</p>
              {costZAR != null && (
                <p className="text-[10px] text-[var(--text-faint)] tabular-nums mt-0.5">≈ {zarFmt.format(costZAR)}</p>
              )}
            </>
          )}
        </div>
      </div>

      {note && <p className="mt-2 text-[11px] text-[var(--text-faint)] leading-snug">{note}</p>}
    </div>
  )
}

/**
 * @param tiles  Array<{ key, icon, accent, label, qty, qtyUnit, costUSD, costZAR, note, free }>
 * @param fxNote optional footer line (FX-rate provenance)
 */
export function UsageBreakdown({ tiles, fxNote }) {
  // Empty when no tile carries usage. Callers set `empty: true` on a tile that has
  // neither cost nor quantity (qty is a pre-formatted string, so we can't infer it
  // reliably here). Fall back to a cost-only check if `empty` is unset.
  const allZero = tiles.every(t => (t.empty != null ? t.empty : (t.costUSD ?? 0) === 0))

  if (allZero) {
    return (
      <div
        className="rounded-[var(--radius-card)] px-6 py-10 text-center"
        style={{ background: 'var(--bg-surface)', border: '1px dashed var(--border2)' }}
      >
        <div className="w-11 h-11 rounded-full flex items-center justify-center mx-auto mb-3" style={{ background: 'var(--bg-surface2)' }}>
          {SparkIcon}
        </div>
        <p className="text-sm font-semibold text-[var(--text-muted)] mb-1">No metered usage yet this period</p>
        <p className="text-xs text-[var(--text-faint)] max-w-sm mx-auto">
          Builder seats, managed-LLM spend, and repo activity show up here as they accrue. Stakeholders &amp; clients are always free.
        </p>
      </div>
    )
  }

  return (
    <div>
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
        {tiles.map(t => (
          <UsageTile key={t.key} {...t} />
        ))}
      </div>
      {fxNote && <p className="mt-3 text-[10.5px] text-[var(--text-faint)]">{fxNote}</p>}
    </div>
  )
}
