/**
 * CreateIssueModal — form to create a native (manual) issue.
 * Makes the "manually tracked, not derived from git" distinction visible and tasteful.
 */
import { useState, useCallback, useEffect } from 'react'

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

  return (
    <>
      <div
        className="fixed inset-0 z-40"
        style={{ background: 'rgba(11,17,32,0.7)', backdropFilter: 'blur(3px)' }}
        onClick={onClose}
      />
      <div
        className="fixed left-1/2 top-1/2 z-50 w-full max-w-lg -translate-x-1/2 -translate-y-1/2"
        style={{
          background: '#111827',
          border: '1px solid #1e2d45',
          borderRadius: '16px',
          boxShadow: '0 24px 80px rgba(0,0,0,0.6)',
        }}
      >
        {/* Header */}
        <div className="px-6 pt-6 pb-4 border-b border-[#1e2d45]">
          <div className="flex items-start gap-3">
            <div
              className="w-8 h-8 rounded-lg flex items-center justify-center shrink-0"
              style={{ background: 'rgba(148,163,184,0.12)', border: '1px solid rgba(148,163,184,0.2)' }}
            >
              <svg width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="#94a3b8" strokeWidth="2">
                <path strokeLinecap="round" strokeLinejoin="round" d="M16.862 4.487 18.5 2.85a1.5 1.5 0 0 1 2.12 2.12l-9.56 9.56a4.5 4.5 0 0 1-1.897 1.13L6 16.5l.719-3.263a4.5 4.5 0 0 1 1.13-1.897l8.01-8.01-.994-.994Z" />
              </svg>
            </div>
            <div>
              <h2 className="text-base font-semibold text-[#e2e8f0]">New manual task</h2>
              <p className="text-xs text-[#64748b] mt-0.5">
                Tracked here · not derived from git · for non-dev work
              </p>
            </div>
            <button onClick={onClose} className="ml-auto text-[#64748b] hover:text-[#e2e8f0] transition-colors">
              <svg width="18" height="18" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
                <path strokeLinecap="round" strokeLinejoin="round" d="M6 18 18 6M6 6l12 12" />
              </svg>
            </button>
          </div>
        </div>

        {/* Callout — two-truth-modes context */}
        <div
          className="mx-6 mt-4 rounded-lg px-4 py-3 flex items-start gap-2.5"
          style={{ background: 'rgba(148,163,184,0.06)', border: '1px solid rgba(148,163,184,0.14)' }}
        >
          <svg width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="#94a3b8" strokeWidth="2" className="shrink-0 mt-0.5">
            <path strokeLinecap="round" strokeLinejoin="round" d="m11.25 11.25.041-.02a.75.75 0 0 1 1.063.852l-.708 2.836a.75.75 0 0 0 1.063.853l.041-.021M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Zm-9-3.75h.008v.008H12V8.25Z" />
          </svg>
          <p className="text-xs text-[#64748b] leading-relaxed">
            Dev work (code / PRs) is <strong className="text-[#94a3b8]">automatically derived from git</strong> —
            you don't create those here. This form is for non-dev work that lives outside a repo:
            meetings, research, design, client calls, ops tasks.
          </p>
        </div>

        {/* Form */}
        <form onSubmit={handleSubmit} className="px-6 py-5 space-y-4">
          <div>
            <label className="block text-xs font-semibold text-[#64748b] uppercase tracking-widest mb-1.5">
              Title <span className="text-[#ef4444]">*</span>
            </label>
            <input
              autoFocus
              required
              type="text"
              placeholder="e.g. Client kick-off call, Design review, Q3 planning"
              className="w-full bg-[#0d1628] text-[#e2e8f0] text-sm rounded-lg px-3 py-2.5 border border-[#1e2d45] outline-none focus:border-[#2DD4BF]/50 placeholder-[#334155] transition-colors"
              value={title}
              onChange={e => setTitle(e.target.value)}
            />
          </div>

          <div>
            <label className="block text-xs font-semibold text-[#64748b] uppercase tracking-widest mb-1.5">
              Description
            </label>
            <textarea
              rows={3}
              placeholder="What needs to be done? Context, deliverables, links…"
              className="w-full bg-[#0d1628] text-[#e2e8f0] text-sm rounded-lg px-3 py-2.5 border border-[#1e2d45] outline-none focus:border-[#2DD4BF]/50 placeholder-[#334155] resize-y transition-colors"
              value={body}
              onChange={e => setBody(e.target.value)}
            />
          </div>

          {projects?.length > 0 && (
            <div>
              <label className="block text-xs font-semibold text-[#64748b] uppercase tracking-widest mb-1.5">
                Project
              </label>
              <select
                className="w-full bg-[#0d1628] text-[#e2e8f0] text-sm rounded-lg px-3 py-2.5 border border-[#1e2d45] outline-none focus:border-[#2DD4BF]/50 transition-colors"
                value={projectId}
                onChange={e => setProjectId(e.target.value)}
              >
                <option value="">— no project —</option>
                {projects.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
              </select>
            </div>
          )}

          {error && (
            <p className="text-xs text-[#ef4444] bg-[#ef444410] rounded px-3 py-2">{error}</p>
          )}

          <div className="flex items-center gap-3 pt-1">
            <button
              type="submit"
              disabled={saving || !title.trim()}
              className="px-5 py-2 rounded-lg text-sm font-semibold text-[#0B1120] disabled:opacity-40 transition-all flex items-center gap-2"
              style={{ background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' }}
            >
              {saving && (
                <svg className="animate-spin" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                  <path d="M21 12a9 9 0 1 1-6.219-8.56" strokeLinecap="round" />
                </svg>
              )}
              Create task
            </button>
            <button
              type="button"
              onClick={onClose}
              className="px-4 py-2 rounded-lg text-sm font-medium text-[#64748b] hover:text-[#e2e8f0] transition-colors"
            >
              Cancel
            </button>
            <span className="ml-auto text-[10px] font-mono text-[#334155]">source: manual</span>
          </div>
        </form>
      </div>
    </>
  )
}
