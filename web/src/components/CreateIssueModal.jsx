/**
 * CreateIssueModal — form to create a native (manual) issue.
 * Makes the "manually tracked, not derived from git" distinction visible and tasteful.
 */
import { useState, useCallback, useEffect } from 'react'
import { PencilLine, X, Info, Loader2, AlertCircle } from 'lucide-react'
import { Badge, Button } from './ui/index.js'

export function CreateIssueModal({ projects, onClose, onCreate }) {
  const [title, setTitle] = useState('')
  const [body, setBody] = useState('')
  const [projectId, setProjectId] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState(null)

  useEffect(() => {
    const handler = (e) => { if (e.key === 'Escape') onClose() }
    document.addEventListener('keydown', handler)
    return () => document.removeEventListener('keydown', handler)
  }, [onClose])

  const handleSubmit = useCallback(async (e) => {
    e.preventDefault()
    if (!title.trim()) return
    setSaving(true)
    setError(null)
    try {
      await onCreate({ title: title.trim(), body: body.trim(), projectId: projectId || undefined })
      onClose()
    } catch (err) {
      setError(err.message ?? 'Failed to create issue')
    } finally {
      setSaving(false)
    }
  }, [title, body, projectId, onCreate, onClose])

  const inputCls = "w-full bg-[var(--bg)] text-[var(--text)] text-sm rounded-[var(--radius-btn)] px-3 py-2.5 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/50 placeholder-[var(--text-faint)] transition-colors"

  return (
    <>
      <div
        className="fixed inset-0 z-40"
        style={{ background: 'rgba(11,17,32,0.7)', backdropFilter: 'blur(3px)' }}
        onClick={onClose}
      />
      <div
        className="fixed left-1/2 top-1/2 z-50 w-full max-w-lg -translate-x-1/2 -translate-y-1/2 rounded-[var(--radius-card)] bg-[var(--bg-surface)] border border-[var(--border)] shadow-[var(--shadow-float)]"
      >
        {/* Header */}
        <div className="px-6 pt-6 pb-4 border-b border-[var(--border)]">
          <div className="flex items-start gap-3">
            <div className="w-9 h-9 rounded-[var(--radius-btn)] flex items-center justify-center shrink-0 bg-[var(--bg-surface3)] border border-[var(--border)]">
              <PencilLine size={16} className="text-[var(--text-muted)]" />
            </div>
            <div>
              <h2 className="text-base font-semibold text-[var(--text)] font-display">New manual task</h2>
              <p className="text-xs text-[var(--text-faint)] mt-0.5">
                Tracked here · not derived from git · for non-dev work
              </p>
            </div>
            <button
              onClick={onClose}
              className="ml-auto -mr-1.5 -mt-1 p-1.5 rounded-[var(--radius-badge)] text-[var(--text-faint)] hover:text-[var(--text)] hover:bg-[var(--bg-surface2)] transition-colors"
              aria-label="Close"
            >
              <X size={18} />
            </button>
          </div>
        </div>

        {/* Callout — two-truth-modes context */}
        <div className="mx-6 mt-4 rounded-[var(--radius-btn)] px-4 py-3 flex items-start gap-2.5 bg-[var(--bg-surface3)]/60 border border-[var(--border)]">
          <Info size={14} className="shrink-0 mt-0.5 text-[var(--text-muted)]" />
          <p className="text-xs text-[var(--text-faint)] leading-relaxed">
            Dev work (code / PRs) is <strong className="text-[var(--text-muted)]">automatically derived from git</strong> —
            you don&apos;t create those here. This form is for non-dev work that lives outside a repo:
            meetings, research, design, client calls, ops tasks.
          </p>
        </div>

        {/* Form */}
        <form onSubmit={handleSubmit} className="px-6 py-5 space-y-4">
          <div>
            <label className="block text-xs font-semibold text-[var(--text-faint)] uppercase tracking-widest mb-1.5">
              Title <span className="text-red-400">*</span>
            </label>
            <input
              autoFocus required type="text"
              placeholder="e.g. Client kick-off call, Design review, Q3 planning"
              className={inputCls}
              value={title}
              onChange={e => setTitle(e.target.value)}
            />
          </div>

          <div>
            <label className="block text-xs font-semibold text-[var(--text-faint)] uppercase tracking-widest mb-1.5">
              Description
            </label>
            <textarea
              rows={3}
              placeholder="What needs to be done? Context, deliverables, links…"
              className={inputCls + ' resize-y'}
              value={body}
              onChange={e => setBody(e.target.value)}
            />
          </div>

          {projects?.length > 0 && (
            <div>
              <label className="block text-xs font-semibold text-[var(--text-faint)] uppercase tracking-widest mb-1.5">
                Project
              </label>
              <select
                className={inputCls}
                value={projectId}
                onChange={e => setProjectId(e.target.value)}
              >
                <option value="">— no project —</option>
                {projects.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
              </select>
            </div>
          )}

          {error && (
            <p className="flex items-center gap-2 text-xs text-red-400 bg-red-500/[0.08] border border-red-500/20 rounded-[var(--radius-btn)] px-3 py-2">
              <AlertCircle size={13} className="shrink-0" /> {error}
            </p>
          )}

          <div className="flex items-center gap-3 pt-1">
            <Button
              type="submit"
              disabled={saving || !title.trim()}
              leftIcon={saving ? <Loader2 size={12} className="animate-spin" /> : null}
            >
              Create task
            </Button>
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
            <Badge className="ml-auto">source: manual</Badge>
          </div>
        </form>
      </div>
    </>
  )
}
