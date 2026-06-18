/**
 * OrgSwitcher — dropdown to list orgs, switch active org, and create a new one.
 * Rendered inside the Sidebar below the logo.
 */
import { useState, useRef, useEffect } from 'react'
import { useOrg } from '../lib/useOrg.js'

function ChevronIcon() {
  return (
    <svg width="12" height="12" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2.5">
      <path strokeLinecap="round" strokeLinejoin="round" d="m19 9-7 7-7-7" />
    </svg>
  )
}

function CheckIcon() {
  return (
    <svg width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2.5">
      <path strokeLinecap="round" strokeLinejoin="round" d="m5 13 4 4L19 7" />
    </svg>
  )
}

function PlusIcon() {
  return (
    <svg width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2.5">
      <path strokeLinecap="round" strokeLinejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
    </svg>
  )
}

function OrgAvatar({ name, size = 22 }) {
  const letter = name ? name[0].toUpperCase() : '?'
  return (
    <div
      className="rounded-md flex items-center justify-center shrink-0 text-[11px] font-bold text-[#0B1120]"
      style={{
        width: size,
        height: size,
        background: 'linear-gradient(135deg, #2DD4BF, #6366F1)',
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

  // Close dropdown on outside click
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
    <div className="relative px-3 py-2 border-b border-[#1e2d45]" ref={dropdownRef}>
      <button
        onClick={() => { setOpen(v => !v); setCreating(false); setNewName(''); setCreateError(null) }}
        className="w-full flex items-center gap-2.5 px-2 py-1.5 rounded-lg hover:bg-[#162032] transition-colors duration-150 group"
        aria-expanded={open}
      >
        <OrgAvatar name={activeOrg?.name ?? '?'} />
        <div className="flex-1 min-w-0 text-left">
          <p className="text-xs font-semibold text-[#e2e8f0] truncate leading-tight">{displayName}</p>
          {activeOrg?.planKey && (
            <p className="text-[10px] text-[#64748b] font-mono capitalize">{activeOrg.planKey}</p>
          )}
        </div>
        <span className="text-[#64748b] group-hover:text-[#94a3b8] transition-colors">
          <ChevronIcon />
        </span>
      </button>

      {open && (
        <div className="absolute left-3 right-3 top-full mt-1 z-50 bg-[#111827] border border-[#1e2d45] rounded-xl shadow-2xl overflow-hidden">
          {/* Org list */}
          {orgs.length > 0 && (
            <div className="p-1.5 border-b border-[#1e2d45]">
              {orgs.map(org => (
                <button
                  key={org.id}
                  onClick={() => { switchOrg(org.id); setOpen(false) }}
                  className="w-full flex items-center gap-2.5 px-2.5 py-2 rounded-lg hover:bg-[#162032] transition-colors duration-150 text-left"
                >
                  <OrgAvatar name={org.name} />
                  <div className="flex-1 min-w-0">
                    <p className="text-xs font-medium text-[#e2e8f0] truncate">{org.name}</p>
                    {org.role && (
                      <p className="text-[10px] text-[#64748b] font-mono capitalize">{org.role}</p>
                    )}
                  </div>
                  {activeOrg?.id === org.id && (
                    <span className="text-[#2DD4BF] shrink-0">
                      <CheckIcon />
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
                className="w-full flex items-center gap-2 px-2.5 py-2 rounded-lg text-xs text-[#64748b] hover:text-[#e2e8f0] hover:bg-[#162032] transition-colors duration-150"
              >
                <PlusIcon />
                New organization
              </button>
            ) : (
              <form onSubmit={handleCreate} className="px-1 py-1">
                <p className="text-[10px] font-medium text-[#64748b] mb-1.5 uppercase tracking-wide">New organization</p>
                <input
                  autoFocus
                  type="text"
                  value={newName}
                  onChange={e => setNewName(e.target.value)}
                  placeholder="Organization name"
                  className="w-full px-2.5 py-1.5 rounded-lg bg-[#0d1628] border border-[#1e2d45] text-xs text-[#e2e8f0] placeholder-[#334155] outline-none focus:border-[#2DD4BF] focus:ring-1 focus:ring-[#2DD4BF]/30 mb-1.5"
                />
                {createError && (
                  <p className="text-[10px] text-red-400 mb-1.5">{createError}</p>
                )}
                <div className="flex gap-1.5">
                  <button
                    type="submit"
                    disabled={createLoading || !newName.trim()}
                    className="flex-1 py-1.5 rounded-lg text-[11px] font-semibold text-[#0B1120] disabled:opacity-50 transition-all"
                    style={{ background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' }}
                  >
                    {createLoading ? 'Creating…' : 'Create'}
                  </button>
                  <button
                    type="button"
                    onClick={() => { setCreating(false); setNewName(''); setCreateError(null) }}
                    className="px-3 py-1.5 rounded-lg text-[11px] text-[#64748b] hover:text-[#e2e8f0] hover:bg-[#162032] transition-colors"
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
