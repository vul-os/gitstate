/**
 * Projects page — list projects, create new ones, link to filtered board.
 * Shows burndown chart when a project card is selected.
 */
import { useState, useCallback, useMemo, useRef, useId } from 'react'
import { useNavigate } from 'react-router-dom'
import { FolderGit2, Boxes, Activity, Archive } from 'lucide-react'
import { useProjects } from '../lib/useProjects.js'
import { BurndownChart } from '../components/BurndownChart.jsx'
import { Card, Badge, Button, StatCard } from '../components/ui/index.js'
import { Reveal, RevealList } from '../components/Reveal.jsx'
import { useFocusTrap } from '../lib/useFocusTrap.js'

function Spinner() {
  return (
    <svg className="animate-spin shrink-0" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <path d="M21 12a9 9 0 1 1-6.219-8.56" strokeLinecap="round" />
    </svg>
  )
}

function InputField({ label, required, id, ...props }) {
  return (
    <div>
      <label htmlFor={id} className="block text-xs font-semibold text-[var(--text-faint)] uppercase tracking-widest mb-1.5">
        {label} {required && <span className="text-red-400" aria-hidden="true">*</span>}
      </label>
      <input
        id={id}
        {...props}
        required={required}
        aria-required={required || undefined}
        className="w-full bg-[var(--bg)] text-[var(--text)] text-sm rounded-[var(--radius-btn)] px-3 py-2.5 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/50 placeholder-[var(--text-faint)] transition-colors"
      />
    </div>
  )
}

function CreateProjectModal({ onClose, onCreate }) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState(null)
  const dialogRef = useRef(null)
  const uid = useId().replace(/:/g, '')
  useFocusTrap(dialogRef, true, onClose)

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
        aria-hidden="true"
      />
      <div ref={dialogRef} role="dialog" aria-modal="true" aria-labelledby={`${uid}-title`} className="fixed left-1/2 top-1/2 z-50 w-full max-w-md -translate-x-1/2 -translate-y-1/2 rounded-[var(--radius-card)] bg-[var(--bg-surface)] border border-[var(--border)] shadow-2xl">
        <div className="px-6 pt-6 pb-4 border-b border-[var(--border)] flex items-center justify-between">
          <h2 id={`${uid}-title`} className="text-base font-semibold text-[var(--text)] font-display">New project</h2>
          <button type="button" onClick={onClose} aria-label="Close dialog" className="rounded text-[var(--text-faint)] hover:text-[var(--text)] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--brand-teal)]">
            <svg width="18" height="18" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2" aria-hidden="true">
              <path strokeLinecap="round" strokeLinejoin="round" d="M6 18 18 6M6 6l12 12" />
            </svg>
          </button>
        </div>

        <form onSubmit={handleSubmit} className="px-6 py-5 space-y-4">
          <InputField
            label="Name"
            required
            id={`${uid}-name`}
            data-autofocus
            type="text"
            placeholder="e.g. Q3 Launch, API v2, Mobile App"
            value={name}
            onChange={e => setName(e.target.value)}
          />
          <div>
            <label htmlFor={`${uid}-desc`} className="block text-xs font-semibold text-[var(--text-faint)] uppercase tracking-widest mb-1.5">
              Description
            </label>
            <textarea
              id={`${uid}-desc`}
              rows={2}
              placeholder="What is this project for?"
              className="w-full bg-[var(--bg)] text-[var(--text)] text-sm rounded-[var(--radius-btn)] px-3 py-2.5 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/50 placeholder-[var(--text-faint)] resize-none transition-colors"
              value={description}
              onChange={e => setDescription(e.target.value)}
            />
          </div>
          <div aria-live="polite">
            {error && (
              <p role="alert" className="text-xs text-red-400 bg-red-500/[0.08] rounded px-3 py-2">{error}</p>
            )}
          </div>
          <div className="flex items-center gap-3 pt-1">
            <Button type="submit" disabled={saving || !name.trim()} leftIcon={saving ? <Spinner /> : null}>
              Create project
            </Button>
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
          </div>
        </form>
      </div>
    </>
  )
}

const STATUS_COLORS = {
  active: 'teal',
  stale: 'yellow',
  done: 'indigo',
}

// Map a project status to a palette accent token + Badge color name.
const STATUS_ACCENT = {
  teal: 'var(--chart-1)',
  yellow: 'var(--chart-3)',
  indigo: 'var(--chart-2)',
}

function ProjectCard({ project, selected, onClick }) {
  const status = project.archived ? 'done' : (project.status ?? 'active')
  const badgeColor = STATUS_COLORS[status] ?? 'teal'
  const accent = STATUS_ACCENT[badgeColor] ?? 'var(--chart-1)'

  return (
    <Card
      hoverable
      onClick={() => onClick(project)}
      className={[
        'group relative cursor-pointer overflow-hidden',
        selected ? 'ring-2 ring-[var(--brand-teal)]/40' : '',
      ].join(' ')}
    >
      {/* hairline accent edge — matches StatCard */}
      <span
        aria-hidden="true"
        className="absolute inset-y-0 left-0 w-[2px] opacity-70 transition-opacity group-hover:opacity-100"
        style={{ background: accent }}
      />

      <div className="flex items-start gap-3 mb-3">
        <div
          className="w-8 h-8 rounded-[var(--radius-badge)] flex items-center justify-center shrink-0 border"
          style={{
            color: accent,
            background: `color-mix(in srgb, ${accent} 12%, transparent)`,
            borderColor: `color-mix(in srgb, ${accent} 25%, transparent)`,
          }}
        >
          <FolderGit2 size={15} />
        </div>
        <div className="flex-1 min-w-0">
          <h3 className="text-sm font-semibold text-[var(--text)] truncate">{project.name}</h3>
          {project.key && (
            <p className="text-[11px] font-mono text-[var(--text-faint)] mt-0.5 uppercase tracking-wider">{project.key}</p>
          )}
          {project.description && (
            <p className="text-xs text-[var(--text-faint)] mt-0.5 line-clamp-2">{project.description}</p>
          )}
        </div>
        <Badge color={badgeColor}>{status}</Badge>
      </div>

      <div className="flex items-center gap-4 text-xs font-mono text-[var(--text-faint)] pt-3 mt-1 border-t border-[var(--border)]">
        <span className="flex items-center gap-1.5">
          <svg width="12" height="12" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2" className="shrink-0">
            <path strokeLinecap="round" strokeLinejoin="round" d="M9 12h3.75M9 15h3.75M9 18h3.75m3 .75H18a2.25 2.25 0 0 0 2.25-2.25V6.108c0-1.135-.845-2.098-1.976-2.192a48.424 48.424 0 0 0-1.123-.08m-5.801 0c-.065.21-.1.433-.1.664 0 .414.336.75.75.75h4.5a.75.75 0 0 0 .75-.75 2.25 2.25 0 0 0-.1-.664m-5.8 0A2.251 2.251 0 0 1 13.5 2.25H15c1.012 0 1.867.668 2.15 1.586m-5.8 0c-.376.023-.75.05-1.124.08C9.095 4.01 8.25 4.973 8.25 6.108V8.25m0 0H4.875c-.621 0-1.125.504-1.125 1.125v11.25c0 .621.504 1.125 1.125 1.125h9.75c.621 0 1.125-.504 1.125-1.125V9.375c0-.621-.504-1.125-1.125-1.125H8.25Z" />
          </svg>
          {project.issueCount != null ? `${project.issueCount} issues` : 'No issues yet'}
        </span>
        {project.repoCount != null && (
          <span className="flex items-center gap-1.5">
            <svg width="12" height="12" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2" className="shrink-0">
              <path strokeLinecap="round" strokeLinejoin="round" d="M6 3v12m0 0a3 3 0 1 0 0 6 3 3 0 0 0 0-6Zm0-12a3 3 0 1 0 0-.001M18 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6Zm0 0c0 3-3 4.5-6 6" />
            </svg>
            {project.repoCount} repos
          </span>
        )}
        {project.updatedAt && (
          <span className="ml-auto text-[var(--text-faint)]">{new Date(project.updatedAt).toLocaleDateString()}</span>
        )}
      </div>
    </Card>
  )
}

export default function Projects() {
  const navigate = useNavigate()
  const { projects, loading, error, createProject } = useProjects()
  const [showCreate, setShowCreate] = useState(false)
  const [selectedProjectId, setSelectedProjectId] = useState(null)

  const handleProjectClick = useCallback((project) => {
    setSelectedProjectId(prev => prev === project.id ? null : project.id)
  }, [])

  const stats = useMemo(() => {
    const total = projects.length
    const archived = projects.filter(p => p.archived).length
    return { total, archived, active: total - archived }
  }, [projects])

  return (
    <div className="w-full">
      {/* Header */}
      <Reveal>
        <div className="flex items-end justify-between mb-6 gap-4">
          <div className="flex items-start gap-3">
            <span className="mt-0.5 grid place-items-center w-9 h-9 rounded-[var(--radius-btn)] bg-[var(--brand-teal)]/10 border border-[var(--brand-teal)]/20 shrink-0">
              <Boxes size={17} className="text-[var(--brand-teal)]" />
            </span>
            <div>
              <h1 className="font-display text-2xl font-semibold text-[var(--text)] tracking-tight">Projects</h1>
              <p className="text-sm text-[var(--text-faint)] mt-1">Group issues and repos · filter the board by project.</p>
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
            New project
          </Button>
        </div>
      </Reveal>

      {/* Loading */}
      {loading && (
        <div className="flex items-center justify-center py-20">
          <svg className="animate-spin" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="var(--brand-teal)" strokeWidth="2">
            <path d="M21 12a9 9 0 1 1-6.219-8.56" strokeLinecap="round" />
          </svg>
        </div>
      )}

      {/* Error */}
      {!loading && error && (
        <Card className="border-red-500/20 bg-red-500/[0.04] mb-4">
          <p className="text-sm text-red-400">{error}</p>
        </Card>
      )}

      {/* Empty state */}
      {!loading && !error && projects.length === 0 && (
        <Card padding="xl" className="border-dashed text-center">
          <div className="w-12 h-12 rounded-[var(--radius-card)] flex items-center justify-center mx-auto mb-4 bg-[var(--brand-indigo)]/[0.06] border border-[var(--brand-indigo)]/20">
            <svg width="22" height="22" fill="none" viewBox="0 0 24 24" stroke="var(--brand-indigo)" strokeWidth="1.5">
              <path strokeLinecap="round" strokeLinejoin="round" d="M2.25 12.75V12A2.25 2.25 0 0 1 4.5 9.75h15A2.25 2.25 0 0 1 21.75 12v.75m-8.69-6.44-2.12-2.12a1.5 1.5 0 0 0-1.061-.44H4.5A2.25 2.25 0 0 0 2.25 6v12a2.25 2.25 0 0 0 2.25 2.25h15A2.25 2.25 0 0 0 21.75 18V9a2.25 2.25 0 0 0-2.25-2.25h-5.379a1.5 1.5 0 0 1-1.06-.44Z" />
            </svg>
          </div>
          <h3 className="text-sm font-semibold text-[var(--text)] mb-1">No projects yet</h3>
          <p className="text-xs text-[var(--text-faint)] max-w-xs mx-auto mb-4">
            Create a project to group issues and repos, then filter the work board by project.
          </p>
          <Button variant="primary" onClick={() => setShowCreate(true)}>Create first project</Button>
        </Card>
      )}

      {/* Project grid */}
      {!loading && projects.length > 0 && (
        <>
          <Reveal>
            <div className="grid grid-cols-1 sm:grid-cols-3 gap-4 mb-6">
              <StatCard label="Projects" value={stats.total.toLocaleString()} sublabel="total" accent="var(--chart-2)" icon={<Boxes size={14} />} />
              <StatCard label="Active" value={stats.active.toLocaleString()} sublabel="in progress" accent="var(--chart-1)" icon={<Activity size={14} />} />
              <StatCard label="Archived" value={stats.archived.toLocaleString()} sublabel="closed out" accent="var(--text-faint)" icon={<Archive size={14} />} />
            </div>
          </Reveal>

          <RevealList className="grid grid-cols-1 md:grid-cols-2 gap-4" staggerDelay={0.05}>
            {projects.map(p => (
              <ProjectCard
                key={p.id}
                project={p}
                selected={selectedProjectId === p.id}
                onClick={handleProjectClick}
              />
            ))}
          </RevealList>

          {/* Burndown panel */}
          {selectedProjectId && (
            <Reveal key={selectedProjectId}>
            <Card padding="lg" className="mt-4">
              <div className="flex items-center justify-between mb-3">
                <div className="flex items-center gap-2.5">
                  <span className="grid place-items-center w-7 h-7 rounded-[6px] shrink-0" style={{ color: 'var(--chart-1)', background: 'color-mix(in srgb, var(--chart-1) 14%, transparent)' }}>
                    <Activity size={15} />
                  </span>
                  <h2 className="text-sm font-semibold text-[var(--text)]">
                    Burndown — {projects.find(p => p.id === selectedProjectId)?.name ?? ''}
                  </h2>
                </div>
                <div className="flex items-center gap-3">
                  <Button
                    variant="ghost"
                    size="xs"
                    onClick={() => navigate(`/board?project=${selectedProjectId}`)}
                  >
                    Open board
                  </Button>
                  <button
                    onClick={() => setSelectedProjectId(null)}
                    className="text-[var(--text-faint)] hover:text-[var(--text-muted)] transition-colors"
                  >
                    <svg width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
                      <path strokeLinecap="round" strokeLinejoin="round" d="M6 18 18 6M6 6l12 12" />
                    </svg>
                  </button>
                </div>
              </div>
              <BurndownChart projectId={selectedProjectId} />
            </Card>
            </Reveal>
          )}
        </>
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
