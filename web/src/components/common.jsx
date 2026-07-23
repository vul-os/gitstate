/**
 * Shared local-first page chrome: headers, spinners, empty + error states.
 * Uses the existing design tokens so every screen reads as one system.
 */

export function PageHeader({ title, subtitle, actions }) {
  return (
    <div className="mb-6 flex flex-wrap items-start justify-between gap-3">
      <div>
        <h1 className="font-display text-[1.6rem] font-semibold tracking-tight text-[var(--text)]">
          {title}
        </h1>
        {subtitle && (
          <p className="mt-1 max-w-2xl text-sm text-[var(--text-faint)]">{subtitle}</p>
        )}
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
  )
}

export function Spinner({ label = 'Loading…' }) {
  return (
    <div role="status" aria-label={label} className="flex items-center justify-center py-16">
      <div
        className="h-8 w-8 rounded-full border-2 border-[var(--border2)] border-t-[var(--brand-teal)] animate-spin"
        style={{ animationDuration: '0.7s' }}
      />
      <span className="sr-only">{label}</span>
    </div>
  )
}

export function ErrorState({ error, onRetry }) {
  const msg = error?.message || 'Something went wrong.'
  const unreachable = error?.code === 'daemon_unreachable'
  return (
    <div className="rounded-[var(--radius-card)] border border-[color-mix(in_srgb,var(--bad)_30%,transparent)] bg-[color-mix(in_srgb,var(--bad)_6%,transparent)] p-6 text-center">
      <p className="text-sm font-medium text-[var(--text)]">{msg}</p>
      {unreachable && (
        <p className="mt-1 text-xs text-[var(--text-faint)]">
          Start the daemon with <code className="font-mono">gitstate serve</code> and try again.
        </p>
      )}
      {onRetry && (
        <button
          type="button"
          onClick={onRetry}
          className="mt-4 rounded-[var(--radius-btn)] border border-[var(--border2)] bg-[var(--bg-surface2)] px-4 py-2 text-sm text-[var(--text)] hover:bg-[var(--bg-surface3)]"
        >
          Retry
        </button>
      )}
    </div>
  )
}

export function EmptyState({ icon, title, description, action }) {
  return (
    <div className="rounded-[var(--radius-card)] border border-dashed border-[var(--border2)] bg-[var(--bg-surface)] px-6 py-14 text-center">
      {icon && (
        <div className="mx-auto mb-4 grid h-12 w-12 place-items-center rounded-full bg-[var(--bg-surface2)] text-[var(--brand-teal)]">
          {icon}
        </div>
      )}
      <h3 className="text-base font-semibold text-[var(--text)]">{title}</h3>
      {description && (
        <p className="mx-auto mt-1.5 max-w-md text-sm text-[var(--text-faint)]">{description}</p>
      )}
      {action && <div className="mt-5 flex justify-center">{action}</div>}
    </div>
  )
}

/** Small labelled scalar for stat rows. */
export function MetricPill({ label, value }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-[10px] font-mono uppercase tracking-[0.14em] text-[var(--text-faint)]">
        {label}
      </span>
      <span className="font-display text-lg font-semibold tabular-nums text-[var(--text)]">
        {value}
      </span>
    </div>
  )
}
