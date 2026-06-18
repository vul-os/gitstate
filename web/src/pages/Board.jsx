/**
 * Board page — the main work view.
 * Toggles between Board (Kanban), List, and Table views.
 * Filters by source (git/native), state, and project.
 * Shows issue detail drawer on click.
 * Two-truth-modes prominently surfaced.
 */
import { useState, useMemo } from 'react'
import { useIssues } from '../lib/useIssues.js'
import { useProjects } from '../lib/useProjects.js'
import { BoardView } from '../components/BoardView.jsx'
import { ListView } from '../components/ListView.jsx'
import { TableView } from '../components/TableView.jsx'
import { IssueDrawer } from '../components/IssueDrawer.jsx'
import { CreateIssueModal } from '../components/CreateIssueModal.jsx'

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
    <div
      className="rounded-xl px-5 py-3.5 flex items-center gap-4 mb-5"
      style={{
        background: 'linear-gradient(135deg, rgba(45,212,191,0.04), rgba(99,102,241,0.04))',
        border: '1px solid rgba(45,212,191,0.12)',
      }}
    >
      <div className="flex items-center gap-2 shrink-0">
        <span
          className="text-[10px] font-mono font-semibold px-1.5 py-0.5 rounded"
          style={{ color: '#2DD4BF', background: 'rgba(45,212,191,0.12)', border: '1px solid rgba(45,212,191,0.25)' }}
        >
          git
        </span>
        <span className="text-[10px] text-[#64748b]">derived from git</span>
      </div>
      <span className="text-[#1e2d45]">·</span>
      <div className="flex items-center gap-2 shrink-0">
        <span
          className="text-[10px] font-mono font-semibold px-1.5 py-0.5 rounded"
          style={{ color: '#94a3b8', background: 'rgba(148,163,184,0.1)', border: '1px solid rgba(148,163,184,0.2)' }}
        >
          manual
        </span>
        <span className="text-[10px] text-[#64748b]">tracked here · not from git</span>
      </div>
      <p className="text-[10px] text-[#334155] font-mono ml-auto hidden md:block">
        two truth-modes · shown honestly
      </p>
    </div>
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
    state: stateFilter || undefined,
    project: projectFilter || undefined,
  })

  // Client-side filter (in case server doesn't support all params yet)
  const filteredIssues = useMemo(() => {
    return issues.filter(issue => {
      if (sourceFilter && issue.source !== sourceFilter) return false
      if (stateFilter) {
        const eff = issue.manualStateOverride ?? issue.derivedState ?? issue.state ?? 'open'
        if (eff !== stateFilter) return false
      }
      if (projectFilter && issue.projectId !== projectFilter) return false
      return true
    })
  }, [issues, sourceFilter, stateFilter, projectFilter])

  const gitCount = issues.filter(i => i.source === 'git').length
  const nativeCount = issues.filter(i => i.source === 'native').length

  return (
    <div className="min-h-full">
      {/* Page header */}
      <div className="flex items-start justify-between mb-5">
        <div>
          <h1 className="text-2xl font-bold text-[#e2e8f0] tracking-tight">Work</h1>
          <div className="flex items-center gap-3 mt-1">
            <p className="text-sm text-[#64748b]">Board · List · Table</p>
            {!loading && (
              <div className="flex items-center gap-2">
                {gitCount > 0 && (
                  <span className="text-[10px] font-mono text-[#2DD4BF] bg-[#2DD4BF12] px-1.5 py-0.5 rounded">
                    {gitCount} git
                  </span>
                )}
                {nativeCount > 0 && (
                  <span className="text-[10px] font-mono text-[#94a3b8] bg-[#94a3b810] px-1.5 py-0.5 rounded">
                    {nativeCount} manual
                  </span>
                )}
              </div>
            )}
          </div>
        </div>

        {/* Create native issue */}
        <button
          onClick={() => setShowCreate(true)}
          className="px-4 py-2 rounded-lg text-sm font-semibold text-[#0B1120] transition-all duration-150 flex items-center gap-1.5 shrink-0"
          style={{ background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' }}
        >
          <svg width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2.5">
            <path strokeLinecap="round" strokeLinejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
          </svg>
          New task
        </button>
      </div>

      {/* Two-truth-modes banner */}
      <TwoTruthsBanner />

      {/* Controls bar */}
      <div className="flex items-center gap-2 flex-wrap mb-5">
        {/* View toggle */}
        <div
          className="flex items-center rounded-lg p-0.5 gap-0.5 shrink-0"
          style={{ background: '#0d1628', border: '1px solid #1e2d45' }}
        >
          {VIEWS.map(v => (
            <button
              key={v.id}
              onClick={() => setView(v.id)}
              className="flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium transition-all duration-150"
              style={{
                background: view === v.id ? '#1a2d4a' : 'transparent',
                color: view === v.id ? '#2DD4BF' : '#64748b',
              }}
            >
              {v.icon}
              {v.label}
            </button>
          ))}
        </div>

        <div className="w-px h-5 bg-[#1e2d45] hidden sm:block" />

        {/* Source filter */}
        <div className="flex gap-1">
          {SOURCE_FILTERS.map(f => (
            <button
              key={f.id}
              onClick={() => setSourceFilter(f.id)}
              className="px-2.5 py-1.5 rounded-lg text-xs font-medium transition-all duration-150"
              style={{
                background: sourceFilter === f.id ? 'rgba(45,212,191,0.1)' : 'transparent',
                color: sourceFilter === f.id ? '#2DD4BF' : '#64748b',
                border: sourceFilter === f.id ? '1px solid rgba(45,212,191,0.25)' : '1px solid transparent',
              }}
            >
              {f.label}
            </button>
          ))}
        </div>

        <div className="w-px h-5 bg-[#1e2d45] hidden sm:block" />

        {/* State filter */}
        <select
          className="bg-[#0d1628] text-xs text-[#94a3b8] rounded-lg px-2.5 py-1.5 border border-[#1e2d45] outline-none focus:border-[#2DD4BF]/40 transition-colors"
          value={stateFilter}
          onChange={e => setStateFilter(e.target.value)}
        >
          {STATE_FILTERS.map(f => <option key={f.id} value={f.id}>{f.label}</option>)}
        </select>

        {/* Project filter */}
        {projects.length > 0 && (
          <select
            className="bg-[#0d1628] text-xs text-[#94a3b8] rounded-lg px-2.5 py-1.5 border border-[#1e2d45] outline-none focus:border-[#2DD4BF]/40 transition-colors"
            value={projectFilter}
            onChange={e => setProjectFilter(e.target.value)}
          >
            <option value="">All projects</option>
            {projects.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
          </select>
        )}

        <span className="ml-auto text-xs text-[#334155] font-mono hidden md:block">
          {filteredIssues.length} issue{filteredIssues.length !== 1 ? 's' : ''}
        </span>
      </div>

      {/* Loading */}
      {loading && (
        <div className="flex items-center justify-center py-20">
          <svg className="animate-spin" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="#2DD4BF" strokeWidth="2">
            <path d="M21 12a9 9 0 1 1-6.219-8.56" strokeLinecap="round" />
          </svg>
        </div>
      )}

      {/* Error */}
      {!loading && error && (
        <div
          className="rounded-xl px-5 py-4 text-sm text-[#ef4444]"
          style={{ background: 'rgba(239,68,68,0.06)', border: '1px solid rgba(239,68,68,0.2)' }}
        >
          {error} — the backend may not be running yet.
        </div>
      )}

      {/* Views */}
      {!loading && !error && (
        <>
          {view === 'board' && <BoardView issues={filteredIssues} onCardClick={setSelectedIssue} />}
          {view === 'list' && <ListView issues={filteredIssues} onCardClick={setSelectedIssue} />}
          {view === 'table' && <TableView issues={filteredIssues} onCardClick={setSelectedIssue} />}
        </>
      )}

      {/* Empty state (no error, no loading, no issues at all) */}
      {!loading && !error && issues.length === 0 && (
        <div
          className="rounded-xl p-12 text-center mt-4"
          style={{ background: 'rgba(13,22,40,0.4)', border: '1px dashed #1e2d45' }}
        >
          <div
            className="w-12 h-12 rounded-xl flex items-center justify-center mx-auto mb-4"
            style={{ background: 'rgba(45,212,191,0.06)', border: '1px solid rgba(45,212,191,0.15)' }}
          >
            <svg width="22" height="22" fill="none" viewBox="0 0 24 24" stroke="#2DD4BF" strokeWidth="1.5">
              <path strokeLinecap="round" strokeLinejoin="round" d="M9 12h3.75M9 15h3.75M9 18h3.75m3 .75H18a2.25 2.25 0 0 0 2.25-2.25V6.108c0-1.135-.845-2.098-1.976-2.192a48.424 48.424 0 0 0-1.123-.08m-5.801 0c-.065.21-.1.433-.1.664 0 .414.336.75.75.75h4.5a.75.75 0 0 0 .75-.75 2.25 2.25 0 0 0-.1-.664m-5.8 0A2.251 2.251 0 0 1 13.5 2.25H15c1.012 0 1.867.668 2.15 1.586m-5.8 0c-.376.023-.75.05-1.124.08C9.095 4.01 8.25 4.973 8.25 6.108V8.25m0 0H4.875c-.621 0-1.125.504-1.125 1.125v11.25c0 .621.504 1.125 1.125 1.125h9.75c.621 0 1.125-.504 1.125-1.125V9.375c0-.621-.504-1.125-1.125-1.125H8.25ZM6.75 12h.008v.008H6.75V12Zm0 3h.008v.008H6.75V15Zm0 3h.008v.008H6.75V18Z" />
            </svg>
          </div>
          <h3 className="text-sm font-semibold text-[#e2e8f0] mb-1">No work items yet</h3>
          <p className="text-xs text-[#64748b] max-w-xs mx-auto mb-4">
            Connect a repo in Repos to pull in git-derived issues, or create a manual task for non-dev work.
          </p>
          <button
            onClick={() => setShowCreate(true)}
            className="px-4 py-2 rounded-lg text-sm font-semibold text-[#0B1120]"
            style={{ background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' }}
          >
            Create first task
          </button>
        </div>
      )}

      {/* Issue drawer — keyed by id so it remounts when switching issues */}
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
