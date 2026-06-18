/**
 * Board page — the main work view.
 * Toggles between Board (Kanban), List, and Table views.
 * Filters by source (git/native), state, and project.
 * Shows issue detail drawer on click.
 * Two-truth-modes prominently surfaced.
 *
 * Board view: real drag-and-drop via @dnd-kit.
 * Dragging a card to another column optimistically updates state
 * and PATCHes /api/issues/{id} {state:<newColumn>}. Reverts on error.
 */
import { useState, useMemo, useCallback } from 'react'
import { useIssues } from '../lib/useIssues.js'
import { useProjects } from '../lib/useProjects.js'
import { KanbanBoard } from '../components/board/KanbanBoard.jsx'
import { ListView } from '../components/ListView.jsx'
import { TableView } from '../components/TableView.jsx'
import { IssueDrawer } from '../components/IssueDrawer.jsx'
import { CreateIssueModal } from '../components/CreateIssueModal.jsx'
import { Card, Badge, Button } from '../components/ui/index.js'

const VIEWS = [
  {
    id: 'board',
    label: 'Board',
    icon: (
      <svg width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
        <path strokeLinecap="round" strokeLinejoin="round" d="M9 17V7m0 10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2h2a2 2 0 0 1 2 2m0 10a2 2 0 0 0 2 2h2a2 2 0 0 0 2-2M9 7a2 2 0 0 1 2-2h2a2 2 0 0 1 2 2m0 10V7m0 10a2 2 0 0 0 2 2h2a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2a2 2 0 0 0-2 2" />
      </svg>
    ),
  },
  {
    id: 'list',
    label: 'List',
    icon: (
      <svg width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
        <path strokeLinecap="round" strokeLinejoin="round" d="M8.25 6.75h12M8.25 12h12m-12 5.25h12M3.75 6.75h.007v.008H3.75V6.75Zm.375 0a.375.375 0 1 1-.75 0 .375.375 0 0 1 .75 0ZM3.75 12h.007v.008H3.75V12Zm.375 0a.375.375 0 1 1-.75 0 .375.375 0 0 1 .75 0Zm-.375 5.25h.007v.008H3.75v-.008Zm.375 0a.375.375 0 1 1-.75 0 .375.375 0 0 1 .75 0Z" />
      </svg>
    ),
  },
  {
    id: 'table',
    label: 'Table',
    icon: (
      <svg width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
        <path strokeLinecap="round" strokeLinejoin="round" d="M3.375 19.5h17.25m-17.25 0a1.125 1.125 0 0 1-1.125-1.125M3.375 19.5h1.5C5.496 19.5 6 18.996 6 18.375m-3.75 0V5.625m0 12.75v-1.5c0-.621.504-1.125 1.125-1.125m18.375 2.625V5.625m0 12.75c0 .621-.504 1.125-1.125 1.125m1.125-1.125v-1.5c0-.621-.504-1.125-1.125-1.125m0 3.75h-1.5A1.125 1.125 0 0 1 18 18.375M20.625 4.5H3.375m17.25 0c.621 0 1.125.504 1.125 1.125M20.625 4.5h-1.5C18.504 4.5 18 5.004 18 5.625m3.75 0v1.5c0 .621-.504 1.125-1.125 1.125M3.375 4.5c-.621 0-1.125.504-1.125 1.125M3.375 4.5h1.5C5.496 4.5 6 5.004 6 5.625m-3.75 0v1.5c0 .621.504 1.125 1.125 1.125m0 0h1.5m-1.5 0c-.621 0-1.125.504-1.125 1.125v1.5c0 .621.504 1.125 1.125 1.125m1.5-3.75C5.496 8.25 6 8.754 6 9.375v1.5m0-5.25v5.25m0-5.25C6 5.004 6.504 4.5 7.125 4.5h9.75c.621 0 1.125.504 1.125 1.125m1.125 2.625h1.5m-1.5 0A1.125 1.125 0 0 1 18 7.125v1.5m1.125-2.625c.621 0 1.125.504 1.125 1.125v1.5m-2.625-.375c0 .621-.504 1.125-1.125 1.125H8.625c-.621 0-1.125-.504-1.125-1.125" />
      </svg>
    ),
  },
]

const SOURCE_FILTERS = [
  { id: '', label: 'All' },
  { id: 'git', label: 'Git-derived' },
  { id: 'native', label: 'Manual' },
]

const STATE_FILTERS = [
  { id: '', label: 'All states' },
  { id: 'open', label: 'Open' },
  { id: 'in_progress', label: 'In progress' },
  { id: 'done', label: 'Done' },
  { id: 'closed', label: 'Closed' },
]

function TwoTruthsBanner() {
  return (
    <Card className="border-[var(--brand-teal)]/15 bg-gradient-to-r from-[var(--brand-teal)]/[0.03] to-[var(--brand-indigo)]/[0.03] mb-5" padding="sm">
      <div className="flex items-center gap-4 flex-wrap">
        <div className="flex items-center gap-2 shrink-0">
          <Badge color="teal">git</Badge>
          <span className="text-[10px] text-[var(--text-faint)]">derived from git</span>
        </div>
        <span className="text-[var(--border2)] hidden sm:block">·</span>
        <div className="flex items-center gap-2 shrink-0">
          <Badge>manual</Badge>
          <span className="text-[10px] text-[var(--text-faint)]">tracked here · not from git</span>
        </div>
        <p className="text-[10px] text-[var(--text-faint)] font-mono ml-auto hidden md:block">
          two truth-modes · shown honestly
        </p>
      </div>
    </Card>
  )
}

export default function Board() {
  const [view, setView] = useState('board')
  const [sourceFilter, setSourceFilter] = useState('')
  const [stateFilter, setStateFilter] = useState('')
  const [projectFilter, setProjectFilter] = useState('')
  const [selectedIssue, setSelectedIssue] = useState(null)
  const [showCreate, setShowCreate] = useState(false)

  const { projects } = useProjects()

  const { issues, loading, error, createIssue, updateIssue } = useIssues({
    source: sourceFilter || undefined,
    // Don't pass state filter to the API when in board view — the board shows all columns.
    // In list/table view, pass it through.
    state: view !== 'board' ? (stateFilter || undefined) : undefined,
    project: projectFilter || undefined,
  })

  // For list/table views, apply state filter client-side too (mirrors API filter)
  const filteredIssues = useMemo(() => {
    return issues.filter(issue => {
      if (sourceFilter && issue.source !== sourceFilter) return false
      if (stateFilter && view !== 'board') {
        const eff = issue.manualStateOverride ?? issue.derivedState ?? issue.state ?? 'open'
        if (eff !== stateFilter) return false
      }
      if (projectFilter && issue.projectId !== projectFilter) return false
      return true
    })
  }, [issues, sourceFilter, stateFilter, projectFilter, view])

  const gitCount = issues.filter(i => i.source === 'git').length
  const nativeCount = issues.filter(i => i.source === 'native').length

  // Called by KanbanBoard when a card is dropped to a new column
  const handleIssueStateChange = useCallback((issueId, newState) => {
    // Sync the drawer if the moved issue is currently open
    setSelectedIssue(prev => {
      if (prev?.id === issueId) {
        return { ...prev, state: newState, manualStateOverride: newState, derivedState: newState }
      }
      return prev
    })
  }, [])

  return (
    <div className="min-h-full flex flex-col">
      {/* Page header */}
      <div className="flex items-start justify-between mb-5">
        <div>
          <h1 className="font-display text-2xl font-semibold text-[var(--text)] tracking-tight">Work</h1>
          <div className="flex items-center gap-3 mt-1">
            <p className="text-sm text-[var(--text-faint)]">Board · List · Table</p>
            {!loading && (
              <div className="flex items-center gap-2">
                {gitCount > 0 && <Badge color="teal">{gitCount} git</Badge>}
                {nativeCount > 0 && <Badge>{nativeCount} manual</Badge>}
              </div>
            )}
          </div>
        </div>

        <Button
          variant="primary"
          onClick={() => setShowCreate(true)}
          leftIcon={
            <svg width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2.5">
              <path strokeLinecap="round" strokeLinejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
            </svg>
          }
        >
          New task
        </Button>
      </div>

      {/* Two-truth-modes banner */}
      <TwoTruthsBanner />

      {/* Controls bar */}
      <div className="flex items-center gap-2 flex-wrap mb-5">
        {/* View toggle */}
        <div className="flex items-center rounded-[var(--radius-btn)] p-0.5 gap-0.5 shrink-0 bg-[var(--bg)] border border-[var(--border)]">
          {VIEWS.map(v => (
            <button
              key={v.id}
              onClick={() => setView(v.id)}
              className={[
                'flex items-center gap-1.5 px-3 py-1.5 rounded-[6px] text-xs font-medium transition-all duration-150',
                view === v.id
                  ? 'bg-[var(--bg-surface2)] text-[var(--brand-teal)]'
                  : 'text-[var(--text-faint)] hover:text-[var(--text-muted)]',
              ].join(' ')}
            >
              {v.icon}
              {v.label}
            </button>
          ))}
        </div>

        <div className="w-px h-5 bg-[var(--border)] hidden sm:block" />

        {/* Source filter */}
        <div className="flex gap-1">
          {SOURCE_FILTERS.map(f => (
            <button
              key={f.id}
              onClick={() => setSourceFilter(f.id)}
              className={[
                'px-2.5 py-1.5 rounded-[var(--radius-btn)] text-xs font-medium transition-all duration-150 border',
                sourceFilter === f.id
                  ? 'bg-[var(--brand-teal)]/10 text-[var(--brand-teal)] border-[var(--brand-teal)]/25'
                  : 'text-[var(--text-faint)] border-transparent hover:text-[var(--text-muted)]',
              ].join(' ')}
            >
              {f.label}
            </button>
          ))}
        </div>

        <div className="w-px h-5 bg-[var(--border)] hidden sm:block" />

        {/* State filter (only visible + meaningful in list/table view) */}
        <select
          className={[
            'bg-[var(--bg)] text-xs text-[var(--text-muted)] rounded-[var(--radius-btn)] px-2.5 py-1.5 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/40 transition-colors',
            view === 'board' ? 'opacity-40 pointer-events-none' : '',
          ].join(' ')}
          value={stateFilter}
          onChange={e => setStateFilter(e.target.value)}
          title={view === 'board' ? 'State filter applies in List / Table view' : undefined}
        >
          {STATE_FILTERS.map(f => <option key={f.id} value={f.id}>{f.label}</option>)}
        </select>

        {/* Project filter */}
        {projects.length > 0 && (
          <select
            className="bg-[var(--bg)] text-xs text-[var(--text-muted)] rounded-[var(--radius-btn)] px-2.5 py-1.5 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/40 transition-colors"
            value={projectFilter}
            onChange={e => setProjectFilter(e.target.value)}
          >
            <option value="">All projects</option>
            {projects.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
          </select>
        )}

        <span className="ml-auto text-xs text-[var(--text-faint)] font-mono hidden md:block">
          {view === 'board' ? `${issues.length} issues` : `${filteredIssues.length} issue${filteredIssues.length !== 1 ? 's' : ''}`}
        </span>
      </div>

      {/* Loading */}
      {loading && (
        <div className="flex items-center justify-center py-20">
          <svg className="animate-spin" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="var(--brand-teal)" strokeWidth="2">
            <path d="M21 12a9 9 0 1 1-6.219-8.56" strokeLinecap="round" />
          </svg>
        </div>
      )}

      {/* Error */}
      {!loading && error && (
        <Card className="border-red-500/20 bg-red-500/[0.04]">
          <p className="text-sm text-red-400">{error} — the backend may not be running yet.</p>
        </Card>
      )}

      {/* Views */}
      {!loading && !error && (
        <>
          {view === 'board' && (
            <KanbanBoard
              issues={issues}
              onCardClick={setSelectedIssue}
              onIssueStateChange={handleIssueStateChange}
            />
          )}
          {view === 'list' && <ListView issues={filteredIssues} onCardClick={setSelectedIssue} />}
          {view === 'table' && <TableView issues={filteredIssues} onCardClick={setSelectedIssue} />}
        </>
      )}

      {/* Empty state (no issues at all) */}
      {!loading && !error && issues.length === 0 && (
        <Card padding="xl" className="border-dashed mt-4 text-center">
          <div className="w-12 h-12 rounded-[var(--radius-card)] flex items-center justify-center mx-auto mb-4 bg-[var(--brand-teal)]/[0.06] border border-[var(--brand-teal)]/15">
            <svg width="22" height="22" fill="none" viewBox="0 0 24 24" stroke="var(--brand-teal)" strokeWidth="1.5">
              <path strokeLinecap="round" strokeLinejoin="round" d="M9 12h3.75M9 15h3.75M9 18h3.75m3 .75H18a2.25 2.25 0 0 0 2.25-2.25V6.108c0-1.135-.845-2.098-1.976-2.192a48.424 48.424 0 0 0-1.123-.08m-5.801 0c-.065.21-.1.433-.1.664 0 .414.336.75.75.75h4.5a.75.75 0 0 0 .75-.75 2.25 2.25 0 0 0-.1-.664m-5.8 0A2.251 2.251 0 0 1 13.5 2.25H15c1.012 0 1.867.668 2.15 1.586m-5.8 0c-.376.023-.75.05-1.124.08C9.095 4.01 8.25 4.973 8.25 6.108V8.25m0 0H4.875c-.621 0-1.125.504-1.125 1.125v11.25c0 .621.504 1.125 1.125 1.125h9.75c.621 0 1.125-.504 1.125-1.125V9.375c0-.621-.504-1.125-1.125-1.125H8.25ZM6.75 12h.008v.008H6.75V12Zm0 3h.008v.008H6.75V15Zm0 3h.008v.008H6.75V18Z" />
            </svg>
          </div>
          <h3 className="text-sm font-semibold text-[var(--text)] mb-1">No work items yet</h3>
          <p className="text-xs text-[var(--text-faint)] max-w-xs mx-auto mb-4">
            Connect a repo in Repos to pull in git-derived issues, or create a manual task for non-dev work.
          </p>
          <Button variant="primary" onClick={() => setShowCreate(true)}>Create first task</Button>
        </Card>
      )}

      {/* Issue drawer */}
      {selectedIssue && (
        <IssueDrawer
          key={selectedIssue.id}
          issue={selectedIssue}
          onClose={() => setSelectedIssue(null)}
          onSave={async (id, patch) => {
            const updated = await updateIssue(id, patch)
            setSelectedIssue(prev => prev?.id === id ? { ...prev, ...updated } : prev)
          }}
        />
      )}

      {/* Create native issue modal */}
      {showCreate && (
        <CreateIssueModal
          projects={projects}
          onClose={() => setShowCreate(false)}
          onCreate={createIssue}
        />
      )}
    </div>
  )
}
