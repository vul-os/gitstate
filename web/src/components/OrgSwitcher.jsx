/**
 * OrgSwitcher — dropdown to list orgs, switch active org, and create a new one.
 * Rendered inside the Sidebar below the logo.
 */
import { useState, useRef, useEffect } from 'react'
import { ChevronsUpDown, Check, Plus, Loader2 } from 'lucide-react'
import { useOrg } from '../lib/useOrg.js'

function OrgAvatar({ name, size = 22 }) {
  const letter = name ? name[0].toUpperCase() : '?'
  return (
    <div
      className="rounded-[var(--radius-badge)] flex items-center justify-center shrink-0 text-[11px] font-bold text-[#0B1120]"
      style={{
        width: size,
        height: size,
        background: 'linear-gradient(135deg, var(--brand-teal), var(--brand-indigo))',
      }}
    >
      {letter}
    </div>
  )
}

export function OrgSwitcher() {
  const { orgs, orgsLoading, activeOrg, switchOrg, createOrg } = useOrg()
  const [open, setOpen] = useState(false)
  const [creating, setCreating] = useState(false)
  const [newName, setNewName] = useState('')
  const [createError, setCreateError] = useState(null)
  const [createLoading, setCreateLoading] = useState(false)
  const dropdownRef = useRef(null)

  useEffect(() => {
    if (!open) return
    function handler(e) {
      if (dropdownRef.current && !dropdownRef.current.contains(e.target)) {
        setOpen(false)
        setCreating(false)
        setNewName('')
        setCreateError(null)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [open])

  async function handleCreate(e) {
    e.preventDefault()
    if (!newName.trim()) return
    setCreateLoading(true)
    setCreateError(null)
    try {
      await createOrg(newName.trim())
      setNewName('')
      setCreating(false)
      setOpen(false)
    } catch (err) {
      setCreateError(err?.message ?? 'Failed to create organization')
    } finally {
      setCreateLoading(false)
    }
  }

  const displayName = activeOrg?.name ?? (orgsLoading ? 'Loading…' : 'Select org')

  return (
    <div className="relative px-3 py-2 border-b border-[var(--border)]" ref={dropdownRef}>
      <button
        onClick={() => { setOpen(v => !v); setCreating(false); setNewName(''); setCreateError(null) }}
        className="w-full flex items-center gap-2.5 px-2 py-1.5 rounded-[var(--radius-btn)] hover:bg-[var(--bg-surface2)] transition-colors duration-150 group"
        aria-expanded={open}
      >
        <OrgAvatar name={activeOrg?.name ?? '?'} />
        <div className="flex-1 min-w-0 text-left">
          <p className="text-xs font-semibold text-[var(--text)] truncate leading-tight">{displayName}</p>
          {activeOrg?.planKey && (
            <p className="text-[10px] text-[var(--text-faint)] font-mono capitalize">{activeOrg.planKey}</p>
          )}
        </div>
        <span className="text-[var(--text-faint)] group-hover:text-[var(--text-muted)] transition-colors">
          <ChevronsUpDown size={13} />
        </span>
      </button>

      {open && (
        <div className="absolute left-3 right-3 top-full mt-1 z-50 bg-[var(--bg-surface)] border border-[var(--border)] rounded-[var(--radius-card)] shadow-[var(--shadow-float)] overflow-hidden">
          {/* Org list */}
          {orgs.length > 0 && (
            <div className="p-1.5 border-b border-[var(--border)]">
              {orgs.map(org => (
                <button
                  key={org.id}
                  onClick={() => { switchOrg(org.id); setOpen(false) }}
                  className="w-full flex items-center gap-2.5 px-2.5 py-2 rounded-[var(--radius-btn)] hover:bg-[var(--bg-surface2)] transition-colors duration-150 text-left"
                >
                  <OrgAvatar name={org.name} />
                  <div className="flex-1 min-w-0">
                    <p className="text-xs font-medium text-[var(--text)] truncate">{org.name}</p>
                    {org.role && (
                      <p className="text-[10px] text-[var(--text-faint)] font-mono capitalize">{org.role}</p>
                    )}
                  </div>
                  {activeOrg?.id === org.id && (
                    <span className="text-[var(--brand-teal)] shrink-0">
                      <Check size={14} strokeWidth={2.5} />
                    </span>
                  )}
                </button>
              ))}
            </div>
          )}

          {/* New org form / button */}
          <div className="p-1.5">
            {!creating ? (
              <button
                onClick={() => { setCreating(true) }}
                className="w-full flex items-center gap-2 px-2.5 py-2 rounded-[var(--radius-btn)] text-xs text-[var(--text-faint)] hover:text-[var(--text)] hover:bg-[var(--bg-surface2)] transition-colors duration-150"
              >
                <Plus size={14} strokeWidth={2.5} />
                New organization
              </button>
            ) : (
              <form onSubmit={handleCreate} className="px-1 py-1">
                <p className="text-[10px] font-medium text-[var(--text-faint)] mb-1.5 uppercase tracking-wide">New organization</p>
                <input
                  autoFocus
                  type="text"
                  value={newName}
                  onChange={e => setNewName(e.target.value)}
                  placeholder="Organization name"
                  className="w-full px-2.5 py-1.5 rounded-[var(--radius-btn)] bg-[var(--bg)] border border-[var(--border)] text-xs text-[var(--text)] placeholder-[var(--text-faint)] outline-none focus:border-[var(--brand-teal)] focus:ring-1 focus:ring-[var(--brand-teal)]/30 mb-1.5 transition-all"
                />
                {createError && (
                  <p className="text-[10px] text-red-400 mb-1.5">{createError}</p>
                )}
                <div className="flex gap-1.5">
                  <button
                    type="submit"
                    disabled={createLoading || !newName.trim()}
                    className="flex-1 flex items-center justify-center gap-1.5 py-1.5 rounded-[var(--radius-btn)] text-[11px] font-semibold text-[#0B1120] disabled:opacity-50 transition-all bg-gradient-to-r from-[var(--brand-teal)] to-[var(--brand-indigo)] hover:opacity-90"
                  >
                    {createLoading && <Loader2 size={11} className="animate-spin" />}
                    {createLoading ? 'Creating…' : 'Create'}
                  </button>
                  <button
                    type="button"
                    onClick={() => { setCreating(false); setNewName(''); setCreateError(null) }}
                    className="px-3 py-1.5 rounded-[var(--radius-btn)] text-[11px] text-[var(--text-faint)] hover:text-[var(--text)] hover:bg-[var(--bg-surface2)] transition-colors"
                  >
                    Cancel
                  </button>
                </div>
              </form>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
