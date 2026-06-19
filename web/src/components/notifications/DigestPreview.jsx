import { useState } from 'react'
import { Loader2, FileText, GitPullRequestArrow, CalendarOff, Inbox } from 'lucide-react'
import { usePreview } from '../../lib/useNotifications.js'

const TABS = [
  { kind: 'weeklyStatus', label: 'Weekly status', icon: FileText },
  { kind: 'stalePRs', label: 'Stale PRs', icon: GitPullRequestArrow },
  { kind: 'ooo', label: "Who's OOO", icon: CalendarOff },
]

// MetricChip — one headline stat from the digest.
function MetricChip({ label, value }) {
  return (
    <div className="rounded-[var(--radius-btn)] border border-[var(--border)] bg-[var(--bg)] px-3 py-2 min-w-[88px]">
      <div className="text-lg font-semibold text-[var(--text)] leading-none tabular-nums">{value}</div>
      <div className="text-[10px] uppercase tracking-wide text-[var(--text-faint)] mt-1">{label}</div>
    </div>
  )
}

// DigestRender — renders the structured digest the way it'd appear in a message.
function DigestRender({ digest }) {
  if (!digest) return null

  if (digest.empty) {
    return (
      <div className="flex flex-col items-center justify-center text-center py-8 px-4">
        <Inbox size={22} className="text-[var(--text-faint)] mb-2" />
        <p className="text-sm font-medium text-[var(--text-muted)]">{digest.title}</p>
        <p className="text-xs text-[var(--text-faint)] mt-1 max-w-sm">
          {digest.emptyReason || 'Nothing to report right now.'}
        </p>
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <div>
        <h4 className="text-sm font-semibold text-[var(--text)]">{digest.title}</h4>
        {digest.subtitle && <p className="text-xs text-[var(--text-faint)] mt-0.5">{digest.subtitle}</p>}
      </div>

      {Array.isArray(digest.metrics) && digest.metrics.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {digest.metrics.map((m, i) => <MetricChip key={i} label={m.label} value={m.value} />)}
        </div>
      )}

      {Array.isArray(digest.sections) && digest.sections.map((s, i) => (
        s.lines && s.lines.length > 0 ? (
          <div key={i}>
            <p className="text-[11px] font-semibold uppercase tracking-wide text-[var(--text-faint)] mb-1.5">{s.heading}</p>
            <ul className="space-y-1.5">
              {s.lines.map((ln, j) => (
                <li key={j} className="flex items-start gap-2 text-sm">
                  <span className="mt-1.5 h-1 w-1 rounded-full bg-[var(--brand-teal)] shrink-0" />
                  <span className="flex-1 min-w-0">
                    <span className="text-[var(--text-dim)]">{ln.text}</span>
                    {ln.meta && <span className="text-[var(--text-faint)] text-xs ml-2">{ln.meta}</span>}
                  </span>
                </li>
              ))}
            </ul>
          </div>
        ) : null
      ))}
    </div>
  )
}

// DigestPreview — tabbed live preview of the three digest kinds, rendered from
// /api/notifications/preview. This is the source of truth for what a channel
// would deliver.
export function DigestPreview() {
  const [kind, setKind] = useState('weeklyStatus')
  const { preview, loading, error } = usePreview(kind)

  return (
    <div className="rounded-[var(--radius-btn)] border border-[var(--border)] bg-[var(--bg-surface2)] overflow-hidden">
      {/* Tabs */}
      <div className="flex items-center gap-1 border-b border-[var(--border)] p-1.5 bg-[var(--bg)]">
        {TABS.map(({ kind: k, label, icon: Icon }) => (
          <button
            key={k}
            type="button"
            onClick={() => setKind(k)}
            className={[
              'flex items-center gap-1.5 rounded-[var(--radius-btn)] px-3 py-1.5 text-xs font-medium transition-all duration-150',
              kind === k
                ? 'bg-[var(--bg-surface2)] text-[var(--text)] shadow-sm'
                : 'text-[var(--text-faint)] hover:text-[var(--text-muted)]',
            ].join(' ')}
          >
            <Icon size={13} />
            {label}
          </button>
        ))}
      </div>

      {/* Body */}
      <div className="p-4 min-h-[180px]">
        {loading ? (
          <div className="flex items-center gap-2 py-8 justify-center text-xs text-[var(--text-faint)]">
            <Loader2 size={14} className="animate-spin" /> Building preview…
          </div>
        ) : error ? (
          <p className="text-xs text-red-400 py-8 text-center">{error}</p>
        ) : (
          <DigestRender digest={preview?.digest} />
        )}
      </div>
    </div>
  )
}
