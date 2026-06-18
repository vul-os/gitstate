/**
 * IssueBadge — visual differentiator for git-derived vs native (manual) issues.
 * This is the two-truth-modes wedge made visible on every issue card.
 */

/** Small "git" badge for source=git issues. */
export function GitBadge() {
  return (
    <span
      className="inline-flex items-center gap-1 text-[10px] font-mono font-semibold px-1.5 py-0.5 rounded"
      style={{ color: '#2DD4BF', background: 'rgba(45,212,191,0.12)', border: '1px solid rgba(45,212,191,0.25)' }}
      title="State derived from git — merged = done, PR open = in progress"
    >
      <svg width="9" height="9" viewBox="0 0 24 24" fill="currentColor">
        <path d="M2.6 10.59L8.38 4.8l1.69 1.7-3.77 3.77 3.77 3.77-1.69 1.7L2.6 10.59zm18.8 0l-5.78-5.79-1.69 1.7 3.77 3.77-3.77 3.77 1.69 1.7 5.78-5.79zM12.97 3L9.5 21.1l1.96.39L14.93 3.4 12.97 3z" />
      </svg>
      git
    </span>
  )
}

/** Small "manual" badge for source=native issues. */
export function NativeBadge() {
  return (
    <span
      className="inline-flex items-center gap-1 text-[10px] font-mono font-semibold px-1.5 py-0.5 rounded"
      style={{ color: '#94a3b8', background: 'rgba(148,163,184,0.1)', border: '1px solid rgba(148,163,184,0.2)' }}
      title="Tracked manually — not derived from git"
    >
      <svg width="9" height="9" viewBox="0 0 24 24" fill="currentColor">
        <path d="M3 17.25V21h3.75L17.81 9.94l-3.75-3.75L3 17.25zM20.71 7.04c.39-.39.39-1.02 0-1.41l-2.34-2.34a.9959.9959 0 0 0-1.41 0l-1.83 1.83 3.75 3.75 1.83-1.83z" />
      </svg>
      manual
    </span>
  )
}

/** State chip — colour-coded by state string. */
export function StateChip({ state, derivedState }) {
  const display = derivedState ?? state ?? 'open'

  const map = {
    open:        { color: '#f59e0b', bg: 'rgba(245,158,11,0.12)' },
    in_progress: { color: '#6366F1', bg: 'rgba(99,102,241,0.12)' },
    done:        { color: '#2DD4BF', bg: 'rgba(45,212,191,0.12)' },
    closed:      { color: '#64748b', bg: 'rgba(100,116,139,0.12)' },
    merged:      { color: '#2DD4BF', bg: 'rgba(45,212,191,0.12)' },
  }

  const s = map[display] ?? map.open
  const label = display.replace('_', ' ')

  return (
    <span
      className="text-[10px] font-mono font-semibold px-2 py-0.5 rounded-full capitalize"
      style={{ color: s.color, background: s.bg }}
    >
      {label}
    </span>
  )
}

/** Labels list (small pills). */
export function LabelPills({ labels }) {
  if (!labels?.length) return null
  return (
    <div className="flex flex-wrap gap-1">
      {labels.map((l, i) => (
        <span
          key={i}
          className="text-[10px] font-mono px-1.5 py-0.5 rounded"
          style={{ color: '#94a3b8', background: 'rgba(148,163,184,0.1)', border: '1px solid rgba(148,163,184,0.15)' }}
        >
          {l}
        </span>
      ))}
    </div>
  )
}
