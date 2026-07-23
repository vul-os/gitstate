/**
 * SegmentedControl — the chart range filter (30d / 90d / 6mo / all).
 *
 * Filters belong in one row above the charts they drive, so this is a compact
 * inline control rather than a dropdown. Rendered as a radiogroup so the whole
 * set is one tab stop with arrow-key navigation.
 */
export function SegmentedControl({ options = [], value, onChange, label = 'Range' }) {
  return (
    <div
      role="radiogroup"
      aria-label={label}
      className="inline-flex items-center gap-0.5 rounded-[var(--radius-btn)] border border-[var(--border)] bg-[var(--bg-surface2)] p-0.5"
    >
      {options.map((opt) => {
        const active = opt.value === value
        return (
          <button
            key={opt.value}
            type="button"
            role="radio"
            aria-checked={active}
            onClick={() => onChange(opt.value)}
            className={[
              'rounded-[6px] px-2.5 py-1 font-mono text-[11px] transition-colors',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--brand-teal)]',
              active
                ? 'bg-[var(--bg-surface)] text-[var(--brand-teal)] shadow-[var(--shadow-card)]'
                : 'text-[var(--text-faint)] hover:text-[var(--text)]',
            ].join(' ')}
          >
            {opt.label}
          </button>
        )
      })}
    </div>
  )
}
