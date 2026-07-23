/**
 * BarList — ranked horizontal bars (contributor leaderboard, label mix).
 *
 * Magnitude by identity, so every row is DIRECTLY labelled and the bars share
 * one hue: colour here would imply a category that doesn't exist. Bars are
 * anchored to the left baseline with a 4px rounded data-end.
 *
 * Props:
 *   items  [{ key, label, value, meta?, badge? }]
 *   max    number  optional shared ceiling (defaults to the largest value)
 *   format (n) => string
 */
export function BarList({ items = [], max, format = (n) => n.toLocaleString(), emptyLabel = 'Nothing to show' }) {
  if (!items.length) {
    return <p className="py-6 text-center text-sm text-[var(--text-faint)]">{emptyLabel}</p>
  }
  const ceiling = max ?? Math.max(...items.map((i) => i.value || 0), 1)

  return (
    <ol className="flex flex-col gap-2.5">
      {items.map((item, i) => {
        const pct = ceiling > 0 ? Math.max(2, (item.value / ceiling) * 100) : 0
        return (
          <li key={item.key} className="flex items-center gap-3">
            <span className="w-4 shrink-0 text-right font-mono text-[11px] tabular-nums text-[var(--text-faint)]">
              {i + 1}
            </span>
            <span className="w-32 shrink-0 truncate text-[13px] text-[var(--text)]" title={item.label}>
              {item.label}
            </span>
            {item.badge}
            <span className="h-2 min-w-0 flex-1 overflow-hidden rounded-full bg-[var(--bg-surface2)]">
              <span
                className="block h-full rounded-full"
                style={{
                  width: `${pct}%`,
                  background: 'linear-gradient(90deg, var(--chart-1), var(--chart-2))',
                }}
              />
            </span>
            <span className="w-14 shrink-0 text-right font-mono text-xs tabular-nums text-[var(--text-muted)]">
              {format(item.value)}
            </span>
          </li>
        )
      })}
    </ol>
  )
}
