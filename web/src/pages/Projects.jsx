/**
 * Projects page — list projects, create new ones, link to filtered board.
 */
import { useState, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { useProjects } from '../lib/useProjects.js'

function CreateProjectModal({ onClose, onCreate }) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState(null)

  const handleSubmit = useCallback(async (e) => {
    e.preventDefault()
    if (!name.trim()) return
    setSaving(true)
    setError(null)
    try {
      await onCreate({ name: name.trim(), description: description.trim() })
      onClose()
    } catch (err) {
      setError(err.message ?? 'Failed to create project')
    } finally {
      setSaving(false)
    }
  }, [name, description, onCreate, onClose])

  return (
    <>
      <div
        className="fixed inset-0 z-40"
        style={{ background: 'rgba(11,17,32,0.7)', backdropFilter: 'blur(3px)' }}
        onClick={onClose}
      />
      <div
        className="fixed left-1/2 top-1/2 z-50 w-full max-w-md -translate-x-1/2 -translate-y-1/2 rounded-2xl"
        style={{ background: '#111827', border: '1px solid #1e2d45', boxShadow: '0 24px 80px rgba(0,0,0,0.6)' }}
      >
        <div className="px-6 pt-6 pb-4 border-b border-[#1e2d45] flex items-center justify-between">
          <h2 className="text-base font-semibold text-[#e2e8f0]">New project</h2>
          <button onClick={onClose} className="text-[#64748b] hover:text-[#e2e8f0] transition-colors">
            <svg width="18" height="18" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
              <path strokeLinecap="round" strokeLinejoin="round" d="M6 18 18 6M6 6l12 12" />
            </svg>
          </button>
        </div>

        <form onSubmit={handleSubmit} className="px-6 py-5 space-y-4">
          <div>
            <label className="block text-xs font-semibold text-[#64748b] uppercase tracking-widest mb-1.5">
              Name <span className="text-[#ef4444]">*</span>
            </label>
            <input
              autoFocus
              required
              type="text"
              placeholder="e.g. Q3 Launch, API v2, Mobile App"
              className="w-full bg-[#0d1628] text-[#e2e8f0] text-sm rounded-lg px-3 py-2.5 border border-[#1e2d45] outline-none focus:border-[#2DD4BF]/50 placeholder-[#334155] transition-colors"
              value={name}
              onChange={e => setName(e.target.value)}
            />
          </div>
          <div>
            <label className="block text-xs font-semibold text-[#64748b] uppercase tracking-widest mb-1.5">
              Description
            </label>
            <textarea
              rows={2}
              placeholder="What is this project for?"
              className="w-full bg-[#0d1628] text-[#e2e8f0] text-sm rounded-lg px-3 py-2.5 border border-[#1e2d45] outline-none focus:border-[#2DD4BF]/50 placeholder-[#334155] resize-none transition-colors"
              value={description}
              onChange={e => setDescription(e.target.value)}
            />
          </div>
          {error && <p className="text-xs text-[#ef4444] bg-[#ef444410] rounded px-3 py-2">{error}</p>}
          <div className="flex items-center gap-3 pt-1">
            <button
              type="submit"
              disabled={saving || !name.trim()}
              className="px-5 py-2 rounded-lg text-sm font-semibold text-[#0B1120] disabled:opacity-40 transition-all flex items-center gap-2"
              style={{ background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' }}
            >
              {saving && (
                <svg className="animate-spin" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                  <path d="M21 12a9 9 0 1 1-6.219-8.56" strokeLinecap="round" />
                </svg>
              )}
              Create project
            </button>
            <button
              type="button"
              onClick={onClose}
              className="px-4 py-2 rounded-lg text-sm font-medium text-[#64748b] hover:text-[#e2e8f0] transition-colors"
            >
              Cancel
            </button>
          </div>
        </form>
      </div>
    </>
  )
}

function ProjectCard({ project, onClick }) {
  const statusColors = {
    active: '#2DD4BF',
    stale: '#f59e0b',
    done: '#6366F1',
  }
  const color = statusColors[project.status ?? 'active'] ?? '#2DD4BF'

  return (
    <div
      onClick={() => onClick(project)}
      className="bg-[#111827] border border-[#1e2d45] rounded-xl p-5 cursor-pointer hover:border-[#2DD4BF]/25 transition-all duration-150 hover:translate-y-[-1px] group"
      style={{ boxShadow: '0 1px 6px rgba(0,0,0,0.25)' }}
    >
      {/* Header */}
      <div className="flex items-start gap-3 mb-3">
        <div
          className="w-8 h-8 rounded-lg flex items-center justify-center shrink-0"
          style={{ background: `${color}15`, border: `1px solid ${color}30` }}
        >
          <svg width="14" height="14" fill="none" viewBox="0 0 24 24" stroke={color} strokeWidth="2">
            <path strokeLinecap="round" strokeLinejoin="round" d="M2.25 12.75V12A2.25 2.25 0 0 1 4.5 9.75h15A2.25 2.25 0 0 1 21.75 12v.75m-8.69-6.44-2.12-2.12a1.5 1.5 0 0 0-1.061-.44H4.5A2.25 2.25 0 0 0 2.25 6v12a2.25 2.25 0 0 0 2.25 2.25h15A2.25 2.25 0 0 0 21.75 18V9a2.25 2.25 0 0 0-2.25-2.25h-5.379a1.5 1.5 0 0 1-1.06-.44Z" />
          </svg>
        </div>
        <div className="flex-1 min-w-0">
          <h3 className="text-sm font-semibold text-[#e2e8f0] group-hover:text-white transition-colors truncate">
            {project.name}
          </h3>
          {project.description && (
            <p className="text-xs text-[#64748b] mt-0.5 line-clamp-2">{project.description}</p>
          )}
        </div>
      </div>

      {/* Stats */}
      <div className="flex items-center gap-4 text-xs font-mono text-[#64748b]">
        {project.issueCount != null && (
          <span>{project.issueCount} issues</span>
        )}
        {project.repoCount != null && (
          <span>{project.repoCount} repos</span>
        )}
        {project.updatedAt && (
          <span className="ml-auto">{new Date(project.updatedAt).toLocaleDateString()}</span>
        )}
      </div>
    </div>
  )
}

export default function Projects() {
  const navigate = useNavigate()
  const { projects, loading, error, createProject } = useProjects()
  const [showCreate, setShowCreate] = useState(false)

  const handleProjectClick = useCallback((project) => {
    // Navigate to board filtered by this project
    navigate(`/board?project=${project.id}`)
  }, [navigate])

  return (
    <div className="max-w-4xl">
      {/* Header */}
      <div className="flex items-center justify-between mb-8">
        <div>
          <h1 className="text-2xl font-bold text-[#e2e8f0] tracking-tight">Projects</h1>
          <p className="text-sm text-[#64748b] mt-1">
            Group issues and repos · filter the board by project.
          </p>
        </div>
        <button
          onClick={() => setShowCreate(true)}
          className="px-4 py-2 rounded-lg text-sm font-semibold text-[#0B1120] transition-all duration-150 flex items-center gap-1.5"
          style={{ background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' }}
        >
          <svg width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2.5">
            <path strokeLinecap="round" strokeLinejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
          </svg>
          New project
        </button>
      </div>

      {/* Loading */}
      {loading && (
        <div className="flex items-center justify-center py-20">
          <svg className="animate-spin" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="#2DD4BF" strokeWidth="2">
            <path d="M21 12a9 9 0 1 1-6.219-8.56" strokeLinecap="round" />
          </svg>
        </div>
      )}

      {/* Error */}
      {!loading && error && (
        <div
          className="rounded-xl px-5 py-4 text-sm text-[#ef4444] mb-4"
          style={{ background: 'rgba(239,68,68,0.06)', border: '1px solid rgba(239,68,68,0.2)' }}
        >
          {error}
        </div>
      )}

      {/* Empty state */}
      {!loading && !error && projects.length === 0 && (
        <div
          className="rounded-xl p-14 text-center"
          style={{ background: 'rgba(13,22,40,0.4)', border: '1px dashed #1e2d45' }}
        >
          <div
            className="w-12 h-12 rounded-xl flex items-center justify-center mx-auto mb-4"
            style={{ background: 'rgba(99,102,241,0.06)', border: '1px solid rgba(99,102,241,0.2)' }}
          >
            <svg width="22" height="22" fill="none" viewBox="0 0 24 24" stroke="#6366F1" strokeWidth="1.5">
              <path strokeLinecap="round" strokeLinejoin="round" d="M2.25 12.75V12A2.25 2.25 0 0 1 4.5 9.75h15A2.25 2.25 0 0 1 21.75 12v.75m-8.69-6.44-2.12-2.12a1.5 1.5 0 0 0-1.061-.44H4.5A2.25 2.25 0 0 0 2.25 6v12a2.25 2.25 0 0 0 2.25 2.25h15A2.25 2.25 0 0 0 21.75 18V9a2.25 2.25 0 0 0-2.25-2.25h-5.379a1.5 1.5 0 0 1-1.06-.44Z" />
            </svg>
          </div>
          <h3 className="text-sm font-semibold text-[#e2e8f0] mb-1">No projects yet</h3>
          <p className="text-xs text-[#64748b] max-w-xs mx-auto mb-4">
            Create a project to group issues and repos, then filter the work board by project.
          </p>
          <button
            onClick={() => setShowCreate(true)}
            className="px-4 py-2 rounded-lg text-sm font-semibold text-[#0B1120]"
            style={{ background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' }}
          >
            Create first project
          </button>
        </div>
      )}

      {/* Project grid */}
      {!loading && projects.length > 0 && (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          {projects.map(p => (
            <ProjectCard key={p.id} project={p} onClick={handleProjectClick} />
          ))}
        </div>
      )}

      {/* Create modal */}
      {showCreate && (
        <CreateProjectModal
          onClose={() => setShowCreate(false)}
          onCreate={createProject}
        />
      )}
    </div>
  )
}
