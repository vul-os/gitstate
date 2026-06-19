/**
 * KanbanColumn — a droppable column containing SortableContext + KanbanCards.
 * Per-column scroll with fixed header. Shows count badge and empty state.
 */
import { useDroppable } from '@dnd-kit/core'
import { SortableContext, verticalListSortingStrategy } from '@dnd-kit/sortable'
import { KanbanCard } from './KanbanCard.jsx'

function ColumnEmptyState({ label, isOver, color }) {
  return (
    <div className="flex flex-col items-center justify-center py-10 gap-2 transition-opacity duration-150">
      <div
        className="w-8 h-8 rounded-lg flex items-center justify-center mb-1 border border-dashed transition-colors duration-150"
        style={{
          background: isOver ? `${color}14` : 'var(--bg-surface3)',
          borderColor: isOver ? `${color}66` : 'var(--border2)',
        }}
      >
        <svg width="14" height="14" fill="none" viewBox="0 0 24 24" strokeWidth="1.5"
          style={{ color: isOver ? color : 'var(--text-faint)' }} stroke="currentColor">
          <path strokeLinecap="round" strokeLinejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
        </svg>
      </div>
      <p className="text-[11px] font-mono text-[var(--text-faint)] text-center leading-snug">
        {isOver ? 'drop here' : `no ${label.toLowerCase()} issues`}
      </p>
    </div>
  )
}

export function KanbanColumn({ col, issues, onCardClick, isOver }) {
  const { setNodeRef } = useDroppable({ id: col.id })
  const issueIds = issues.map(i => i.id)

  return (
    <div className="flex flex-col w-[276px] shrink-0 min-h-0">
      {/* Column header */}
      <div className="flex items-center gap-2 mb-3 px-0.5 shrink-0">
        <div className="w-2 h-2 rounded-full shrink-0" style={{ background: col.color }} />
        <span className="text-xs font-semibold text-[var(--text-muted)] uppercase tracking-widest">
          {col.label}
        </span>
        <span
          className="text-xs font-mono ml-auto px-1.5 py-0.5 rounded"
          style={{ color: col.color, background: `${col.color}1a` }}
        >
          {issues.length}
        </span>
      </div>

      {/* Drop zone + scrollable card list */}
      <SortableContext items={issueIds} strategy={verticalListSortingStrategy}>
        <div
          ref={setNodeRef}
          className="flex flex-col gap-2.5 rounded-xl p-2 overflow-y-auto flex-1 transition-[background,border-color,box-shadow] duration-150"
          style={{
            backgroundColor: isOver ? `${col.color}10` : 'color-mix(in srgb, var(--bg-surface2) 45%, transparent)',
            border: `1px solid ${isOver ? col.color + '55' : 'var(--border)'}`,
            boxShadow: isOver ? `inset 0 0 0 1px ${col.color}22` : 'none',
            minHeight: 120,
            maxHeight: 'calc(100vh - 280px)',
          }}
        >
          {issues.length === 0 ? (
            <ColumnEmptyState label={col.label} isOver={isOver} color={col.color} />
          ) : (
            issues.map(issue => (
              <KanbanCard key={issue.id} issue={issue} onClick={onCardClick} />
            ))
          )}
        </div>
      </SortableContext>
    </div>
  )
}
