/**
 * KanbanCard — draggable issue card for the Kanban board.
 * Handles both the "normal render" and the "drag overlay" render.
 * Click (not drag) opens the IssueDrawer.
 */
import { useSortable } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import { GitBadge, NativeBadge, LabelPills, StateChip } from '../IssueBadge.jsx'

function CardContent({ issue, isDragging, isOverlay }) {
  const isGit = issue.source === 'git'

  return (
    <div
      className={[
        'rounded-[var(--radius-card)] p-3.5 select-none transition-all duration-150',
        isOverlay
          ? 'shadow-[0_12px_40px_rgba(0,0,0,0.5)] rotate-[1.5deg] scale-[1.02]'
          : isDragging
          ? 'opacity-0'
          : 'cursor-grab hover:translate-y-[-1px] hover:border-[var(--border2)] hover:shadow-[0_4px_16px_rgba(0,0,0,0.25)] active:cursor-grabbing',
      ].join(' ')}
      style={{
        background: isOverlay ? 'var(--bg-surface2)' : 'var(--bg-surface)',
        border: `1px solid ${isGit ? 'rgba(45,212,191,0.18)' : 'var(--border)'}`,
        boxShadow: isOverlay
          ? '0 12px 40px rgba(0,0,0,0.5), 0 0 0 1px rgba(45,212,191,0.25)'
          : '0 1px 3px rgba(0,0,0,0.12), 0 1px 2px rgba(0,0,0,0.08)',
      }}
    >
      {/* Source badge row */}
      <div className="flex items-center gap-1.5 mb-2">
        {isGit ? <GitBadge /> : <NativeBadge />}
        {issue.platform && (
          <span className="text-[10px] font-mono text-[var(--text-faint)]">{issue.platform}</span>
        )}
        <StateChip state={issue.state} derivedState={issue.derivedState} />
        {issue.manualStateOverride && (
          <span
            className="text-[10px] font-mono ml-auto px-1.5 py-0.5 rounded"
            style={{ color: '#f59e0b', background: 'rgba(245,158,11,0.10)' }}
          >
            overridden
          </span>
        )}
      </div>

      {/* Title */}
      <p className="text-sm font-medium text-[var(--text)] leading-snug line-clamp-2 mb-2">
        {issue.title}
      </p>

      {/* Labels */}
      {issue.labels?.length > 0 && (
        <div className="mb-2">
          <LabelPills labels={issue.labels.slice(0, 3)} />
        </div>
      )}

      {/* Footer */}
      <div className="flex items-center gap-2 mt-2">
        {issue.assigneeId && (
          <div
            className="w-5 h-5 rounded-full flex items-center justify-center text-[9px] font-bold shrink-0"
            style={{
              background: 'linear-gradient(135deg, var(--brand-teal), var(--brand-indigo))',
              color: '#0B1120',
            }}
            title={issue.assigneeId}
          >
            {issue.assigneeId.slice(0, 2).toUpperCase()}
          </div>
        )}
        {issue.projectId && (
          <span className="text-[10px] font-mono text-[var(--text-faint)] truncate max-w-[80px]">
            {issue.projectId}
          </span>
        )}
        {issue.effortEstimate?.score != null && (
          <span className="text-[10px] font-mono text-[var(--text-faint)]">
            D{issue.effortEstimate.score}
          </span>
        )}
        {issue.pullRequest && (
          <span className="text-[10px] font-mono text-[var(--brand-teal)] ml-auto">
            PR #{issue.pullRequest.number}
          </span>
        )}
      </div>
    </div>
  )
}

/**
 * KanbanCard — the sortable version (used inside a column).
 * onClick fires only if the drag distance was small (click, not drag).
 */
export function KanbanCard({ issue, onClick }) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({
    id: issue.id,
    data: { issue },
  })

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
  }

  return (
    <div
      ref={setNodeRef}
      style={style}
      {...attributes}
      {...listeners}
      onClick={() => {
        // Only fire click if not mid-drag
        if (!isDragging) onClick(issue)
      }}
    >
      <CardContent issue={issue} isDragging={isDragging} />
    </div>
  )
}

/**
 * KanbanCardOverlay — rendered in the DragOverlay portal (no sortable wiring).
 */
export function KanbanCardOverlay({ issue }) {
  return <CardContent issue={issue} isOverlay />
}
