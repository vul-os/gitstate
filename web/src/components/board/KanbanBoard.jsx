/**
 * KanbanBoard — the full DnD board.
 *
 * Drag model:
 *   - DndContext wraps all columns.
 *   - Each column is a Droppable (id = state key).
 *   - Each card is a Sortable (id = issue.id).
 *   - onDragEnd: if the card moved to a different column,
 *     optimistically update local state + PATCH /api/issues/{id} {state}.
 *     On error, revert.
 *
 * Sensors: PointerSensor with 8px activation distance (distinguishes click from drag)
 *          + KeyboardSensor for accessibility.
 */
import { useState, useMemo, useCallback } from 'react'
import {
  DndContext,
  DragOverlay,
  PointerSensor,
  KeyboardSensor,
  useSensor,
  useSensors,
  closestCorners,
  defaultDropAnimationSideEffects,
} from '@dnd-kit/core'
import { sortableKeyboardCoordinates, arrayMove } from '@dnd-kit/sortable'
import { KanbanColumn } from './KanbanColumn.jsx'
import { KanbanCardOverlay } from './KanbanCard.jsx'
import * as api from '../../lib/api.js'

const COLUMNS = [
  { id: 'open',        label: 'Open',        color: '#f59e0b' },
  { id: 'in_progress', label: 'In Progress', color: '#6366F1' },
  { id: 'done',        label: 'Done',        color: '#2DD4BF' },
  { id: 'closed',      label: 'Closed',      color: '#64748b' },
]

const dropAnimation = {
  sideEffects: defaultDropAnimationSideEffects({
    styles: {
      active: { opacity: '0.5' },
    },
  }),
}

/** Returns the effective state for an issue (manual override > derivedState > state). */
function effectiveState(issue) {
  return issue.manualStateOverride ?? issue.derivedState ?? issue.state ?? 'open'
}

/**
 * Merge optimistic overrides onto the base issues array.
 * overrides = Map<id, partialIssue>
 */
function applyOverrides(issues, overrides) {
  if (overrides.size === 0) return issues
  return issues.map(i => {
    const o = overrides.get(i.id)
    return o ? { ...i, ...o } : i
  })
}

export function KanbanBoard({ issues, onCardClick, onIssueStateChange }) {
  // Track optimistic state overrides: id → patch. Cleared on revert or confirmed.
  const [overrides, setOverrides] = useState(new Map())
  // Track in-column sort order overrides: columnId → [issueId, ...]
  const [colOrder, setColOrder] = useState(new Map())

  const [activeIssue, setActiveIssue] = useState(null)
  const [overColumnId, setOverColumnId] = useState(null)

  const sensors = useSensors(
    useSensor(PointerSensor, {
      activationConstraint: { distance: 8 },
    }),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
    }),
  )

  // Merge overrides and group into columns
  const grouped = useMemo(() => {
    const merged = applyOverrides(issues, overrides)
    const map = {}
    for (const col of COLUMNS) map[col.id] = []
    for (const issue of merged) {
      const state = effectiveState(issue)
      if (map[state] != null) {
        map[state].push(issue)
      } else {
        map['open'].push(issue)
      }
    }
    // Apply in-column sort overrides
    for (const col of COLUMNS) {
      const order = colOrder.get(col.id)
      if (order) {
        const idxMap = new Map(order.map((id, i) => [id, i]))
        map[col.id].sort((a, b) => {
          const ai = idxMap.has(a.id) ? idxMap.get(a.id) : Infinity
          const bi = idxMap.has(b.id) ? idxMap.get(b.id) : Infinity
          return ai - bi
        })
      }
    }
    return map
  }, [issues, overrides, colOrder])

  /** Find which column an issue id belongs to in the grouped state. */
  const findColumn = useCallback((id) => {
    for (const col of COLUMNS) {
      if (grouped[col.id].some(i => i.id === id)) return col.id
    }
    return null
  }, [grouped])

  const handleDragStart = useCallback(({ active }) => {
    const merged = applyOverrides(issues, overrides)
    const issue = merged.find(i => i.id === active.id)
    setActiveIssue(issue ?? null)
  }, [issues, overrides])

  const handleDragOver = useCallback(({ over }) => {
    if (!over) {
      setOverColumnId(null)
      return
    }
    const isColumn = COLUMNS.some(c => c.id === over.id)
    setOverColumnId(isColumn ? over.id : findColumn(over.id))
  }, [findColumn])

  const handleDragEnd = useCallback(async ({ active, over }) => {
    setActiveIssue(null)
    setOverColumnId(null)

    if (!over) return

    const activeId = active.id
    const overId = over.id

    const sourceColId = findColumn(activeId)
    const isDestColumn = COLUMNS.some(c => c.id === overId)
    const destColId = isDestColumn ? overId : findColumn(overId)

    if (!sourceColId || !destColId) return

    if (sourceColId === destColId) {
      // Same column: reorder within
      const colIssues = grouped[sourceColId]
      const oldIdx = colIssues.findIndex(i => i.id === activeId)
      const newIdx = colIssues.findIndex(i => i.id === overId)
      if (oldIdx !== -1 && newIdx !== -1 && oldIdx !== newIdx) {
        const reordered = arrayMove(colIssues.map(i => i.id), oldIdx, newIdx)
        setColOrder(prev => new Map(prev).set(sourceColId, reordered))
      }
      return
    }

    // Moving to a different column — optimistic update
    const patch = { state: destColId, manualStateOverride: destColId, derivedState: destColId }
    setOverrides(prev => new Map(prev).set(activeId, patch))

    // PATCH to backend
    try {
      await api.patch(`/api/issues/${activeId}`, { state: destColId })
      if (onIssueStateChange) onIssueStateChange(activeId, destColId)
    } catch (err) {
      console.error('Failed to update issue state:', err)
      // Revert optimistic override
      setOverrides(prev => {
        const next = new Map(prev)
        next.delete(activeId)
        return next
      })
    }
  }, [findColumn, grouped, onIssueStateChange])

  return (
    <DndContext
      sensors={sensors}
      collisionDetection={closestCorners}
      onDragStart={handleDragStart}
      onDragOver={handleDragOver}
      onDragEnd={handleDragEnd}
    >
      <div className="flex gap-4 pb-4 overflow-x-auto" style={{ minHeight: 0 }}>
        {COLUMNS.map(col => (
          <KanbanColumn
            key={col.id}
            col={col}
            issues={grouped[col.id]}
            onCardClick={onCardClick}
            isOver={overColumnId === col.id}
          />
        ))}
      </div>

      <DragOverlay dropAnimation={dropAnimation}>
        {activeIssue ? <KanbanCardOverlay issue={activeIssue} /> : null}
      </DragOverlay>
    </DndContext>
  )
}
