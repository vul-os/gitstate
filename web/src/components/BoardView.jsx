/**
 * BoardView — Kanban board with columns: open / in_progress / done / closed.
 * Default work view.
 */
import { IssueCard } from './IssueCard.jsx'

const COLUMNS = [
  { id: 'open',        label: 'Open',        color: '#f59e0b' },
  { id: 'in_progress', label: 'In Progress',  color: '#6366F1' },
  { id: 'done',        label: 'Done',         color: '#2DD4BF' },
  { id: 'closed',      label: 'Closed',       color: '#64748b' },
]

function Column({ col, issues, onCardClick }) {
  return (
    <div className="flex flex-col w-[272px] shrink-0">
      {/* Column header */}
      <div className="flex items-center gap-2 mb-3 px-0.5">
        <div className="w-2 h-2 rounded-full shrink-0" style={{ background: col.color }} />
        <span className="text-xs font-semibold text-[#94a3b8] uppercase tracking-widest">{col.label}</span>
        <span
          className="text-xs font-mono ml-auto px-1.5 py-0.5 rounded"
          style={{ color: col.color, background: `${col.color}18` }}
        >
          {issues.length}
        </span>
      </div>

      {/* Cards */}
      <div
        className="flex flex-col gap-2.5 min-h-[120px] rounded-xl p-2"
        style={{ background: 'rgba(13,22,40,0.4)', border: '1px solid #1e2d45' }}
      >
        {issues.length === 0 && (
          <div className="flex-1 flex items-center justify-center py-8">
            <p className="text-xs text-[#334155] font-mono">empty</p>
          </div>
        )}
        {issues.map(issue => (
          <IssueCard key={issue.id} issue={issue} onClick={onCardClick} />
        ))}
      </div>
    </div>
  )
}

export function BoardView({ issues, onCardClick }) {
  // Group issues by their effective state
  const grouped = {}
  for (const col of COLUMNS) grouped[col.id] = []

  for (const issue of issues) {
    // Git issues may have a manual override; fall back to state
    const effectiveState = issue.manualStateOverride ?? issue.derivedState ?? issue.state ?? 'open'
    const target = grouped[effectiveState] ? effectiveState : 'open'
    grouped[target].push(issue)
  }

  return (
    <div className="flex gap-4 overflow-x-auto pb-4">
      {COLUMNS.map(col => (
        <Column
          key={col.id}
          col={col}
          issues={grouped[col.id]}
          onCardClick={onCardClick}
        />
      ))}
    </div>
  )
}
