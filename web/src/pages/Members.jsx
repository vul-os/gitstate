/**
 * Members page — /settings/members
 * List members of the active org, invite by email+role, change role, remove.
 * Invite/remove controls only shown to owner/admin.
 */
import { useReducer, useEffect, useCallback } from 'react'
import { useOrg } from '../lib/useOrg.js'
import * as api from '../lib/api.js'

const ROLES = ['owner', 'admin', 'member', 'stakeholder', 'billing']

const ROLE_META = {
  owner: { label: 'Owner', color: 'text-[#f59e0b] bg-[#f59e0b]/10 border-[#f59e0b]/20' },
  admin: { label: 'Admin', color: 'text-[#6366F1] bg-[#6366F1]/10 border-[#6366F1]/20' },
  member: { label: 'Member', color: 'text-[#94a3b8] bg-[#94a3b8]/10 border-[#94a3b8]/20' },
  stakeholder: { label: 'Stakeholder', color: 'text-[#2DD4BF] bg-[#2DD4BF]/10 border-[#2DD4BF]/20' },
  billing: { label: 'Billing', color: 'text-[#a78bfa] bg-[#a78bfa]/10 border-[#a78bfa]/20' },
}

function RoleBadge({ role }) {
  const meta = ROLE_META[role] ?? { label: role, color: 'text-[#64748b] bg-[#64748b]/10 border-[#64748b]/20' }
  return (
    <span className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-mono font-semibold uppercase tracking-wide border ${meta.color}`}>
      {meta.label}
      {role === 'stakeholder' && (
        <span className="ml-1 text-[#2DD4BF] opacity-70">· free</span>
      )}
    </span>
  )
}

function Avatar({ name, email }) {
  const initials = name
    ? name.split(' ').map(w => w[0]).join('').slice(0, 2).toUpperCase()
    : (email ?? '?').slice(0, 2).toUpperCase()
  return (
    <div className="w-8 h-8 rounded-full bg-gradient-to-br from-[#2DD4BF] to-[#6366F1] flex items-center justify-center text-[11px] font-bold text-[#0B1120] shrink-0 select-none">
      {initials}
    </div>
  )
}

function Spinner() {
  return (
    <svg className="animate-spin" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5">
      <path strokeLinecap="round" d="M12 2v4M12 18v4M4.93 4.93l2.83 2.83M16.24 16.24l2.83 2.83M2 12h4M18 12h4M4.93 19.07l2.83-2.83M16.24 7.76l2.83-2.83" />
    </svg>
  )
}

// ── Reducers ──────────────────────────────────────────────────────────────────

function membersReducer(state, action) {
  switch (action.type) {
    case 'LOADING': return { ...state, loading: true, error: null }
    case 'LOADED': return { ...state, loading: false, members: action.members }
    case 'ERROR': return { ...state, loading: false, error: action.error }
    case 'UPDATE_ROLE':
      return {
        ...state,
        members: state.members.map(m =>
          m.userId === action.userId ? { ...m, role: action.role } : m
        ),
      }
    case 'REMOVE':
      return { ...state, members: state.members.filter(m => m.userId !== action.userId) }
    case 'SET_ROLE_CHANGING':
      return { ...state, roleChanging: { ...state.roleChanging, [action.userId]: action.value } }
    case 'SET_REMOVING':
      return { ...state, removing: { ...state.removing, [action.userId]: action.value } }
    default:
      return state
  }
}

function inviteReducer(state, action) {
  switch (action.type) {
    case 'SENDING': return { ...state, inviting: true, inviteError: null, inviteSuccess: null }
    case 'SUCCESS': return { ...state, inviting: false, inviteEmail: '', inviteSuccess: action.msg }
    case 'ERROR': return { ...state, inviting: false, inviteError: action.error }
    case 'SET_EMAIL': return { ...state, inviteEmail: action.value }
    case 'SET_ROLE': return { ...state, inviteRole: action.value }
    default:
      return state
  }
}

// ── Component ─────────────────────────────────────────────────────────────────

export default function Members() {
  const { activeOrg, orgRole } = useOrg()
  const canManage = orgRole === 'owner' || orgRole === 'admin'

  const [membersState, membersDispatch] = useReducer(membersReducer, {
    members: [],
    loading: false,
    error: null,
    roleChanging: {},
    removing: {},
  })

  const [inviteState, inviteDispatch] = useReducer(inviteReducer, {
    inviteEmail: '',
    inviteRole: 'member',
    inviting: false,
    inviteError: null,
    inviteSuccess: null,
  })

  const orgId = activeOrg?.id

  const fetchMembers = useCallback(async (id) => {
    if (!id) return
    membersDispatch({ type: 'LOADING' })
    try {
      const data = await api.get(`/api/orgs/${id}/members`)
      membersDispatch({ type: 'LOADED', members: Array.isArray(data) ? data : [] })
    } catch (err) {
      membersDispatch({ type: 'ERROR', error: err?.message ?? 'Failed to load members' })
    }
  }, [])

  useEffect(() => {
    fetchMembers(orgId).catch(() => {})
  }, [orgId, fetchMembers])

  async function handleInvite(e) {
    e.preventDefault()
    if (!orgId || !inviteState.inviteEmail.trim()) return
    inviteDispatch({ type: 'SENDING' })
    try {
      await api.post(`/api/orgs/${orgId}/members`, {
        email: inviteState.inviteEmail.trim(),
        role: inviteState.inviteRole,
      })
      inviteDispatch({ type: 'SUCCESS', msg: `Invite sent to ${inviteState.inviteEmail.trim()}` })
      await fetchMembers(orgId)
    } catch (err) {
      inviteDispatch({ type: 'ERROR', error: err?.message ?? 'Failed to send invite' })
    }
  }

  async function handleRoleChange(userId, newRole) {
    if (!orgId) return
    membersDispatch({ type: 'SET_ROLE_CHANGING', userId, value: true })
    try {
      await api.patch(`/api/orgs/${orgId}/members/${userId}`, { role: newRole })
      membersDispatch({ type: 'UPDATE_ROLE', userId, role: newRole })
    } catch {
      // silently revert on error — future: toast
    } finally {
      membersDispatch({ type: 'SET_ROLE_CHANGING', userId, value: false })
    }
  }

  async function handleRemove(userId, memberEmail) {
    if (!orgId) return
    if (!window.confirm(`Remove ${memberEmail ?? userId} from the organization?`)) return
    membersDispatch({ type: 'SET_REMOVING', userId, value: true })
    try {
      await api.del(`/api/orgs/${orgId}/members/${userId}`)
      membersDispatch({ type: 'REMOVE', userId })
    } catch {
      // future: toast
    } finally {
      membersDispatch({ type: 'SET_REMOVING', userId, value: false })
    }
  }

  const { members, loading, error, roleChanging, removing } = membersState
  const { inviteEmail, inviteRole, inviting, inviteError, inviteSuccess } = inviteState

  if (!activeOrg) {
    return (
      <div className="max-w-2xl">
        <div className="mb-8">
          <h1 className="text-2xl font-bold text-[#e2e8f0] tracking-tight">Members</h1>
        </div>
        <div className="bg-[#111827] border border-[#1e2d45] rounded-xl p-8 text-center text-sm text-[#64748b]">
          No active organization. Create or select one from the sidebar.
        </div>
      </div>
    )
  }

  return (
    <div className="max-w-2xl">
      <div className="mb-8">
        <h1 className="text-2xl font-bold text-[#e2e8f0] tracking-tight">Members</h1>
        <p className="text-sm text-[#64748b] mt-1">
          Manage who has access to <span className="text-[#94a3b8] font-medium">{activeOrg.name}</span>.
          {' '}Stakeholders are always <span className="text-[#2DD4BF] font-medium">free</span> — no seat cost.
        </p>
      </div>

      {/* Invite form — only for owner/admin */}
      {canManage && (
        <section className="bg-[#111827] border border-[#1e2d45] rounded-xl p-6 mb-4">
          <h2 className="text-sm font-semibold text-[#e2e8f0] mb-1">Invite a member</h2>
          <p className="text-xs text-[#64748b] mb-4">
            Stakeholder seats are <span className="text-[#2DD4BF] font-medium">free</span> — perfect for clients and external viewers.
          </p>
          <form onSubmit={handleInvite} className="flex gap-2 flex-wrap">
            <input
              type="email"
              required
              value={inviteEmail}
              onChange={e => inviteDispatch({ type: 'SET_EMAIL', value: e.target.value })}
              placeholder="colleague@example.com"
              className="flex-1 min-w-[200px] px-3 py-2 rounded-lg bg-[#0d1628] border border-[#1e2d45] text-sm text-[#e2e8f0] placeholder-[#334155] outline-none focus:border-[#2DD4BF] focus:ring-1 focus:ring-[#2DD4BF]/30 transition-all"
            />
            <select
              value={inviteRole}
              onChange={e => inviteDispatch({ type: 'SET_ROLE', value: e.target.value })}
              className="px-3 py-2 rounded-lg bg-[#0d1628] border border-[#1e2d45] text-sm text-[#e2e8f0] outline-none focus:border-[#2DD4BF] focus:ring-1 focus:ring-[#2DD4BF]/30 transition-all cursor-pointer"
            >
              {ROLES.map(r => (
                <option key={r} value={r}>
                  {r === 'stakeholder' ? 'Stakeholder (free)' : r.charAt(0).toUpperCase() + r.slice(1)}
                </option>
              ))}
            </select>
            <button
              type="submit"
              disabled={inviting || !inviteEmail.trim()}
              className="flex items-center gap-1.5 px-4 py-2 rounded-lg text-sm font-semibold text-[#0B1120] disabled:opacity-50 transition-all shrink-0"
              style={{ background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' }}
            >
              {inviting ? <Spinner /> : null}
              {inviting ? 'Sending…' : 'Invite'}
            </button>
          </form>
          {inviteError && (
            <p className="mt-2 text-xs text-red-400">{inviteError}</p>
          )}
          {inviteSuccess && (
            <p className="mt-2 text-xs text-[#2DD4BF]">{inviteSuccess}</p>
          )}
        </section>
      )}

      {/* Members list */}
      <section className="bg-[#111827] border border-[#1e2d45] rounded-xl overflow-hidden">
        <div className="px-6 py-4 border-b border-[#1e2d45] flex items-center justify-between">
          <h2 className="text-sm font-semibold text-[#e2e8f0]">
            Members
            {!loading && members.length > 0 && (
              <span className="ml-2 text-xs font-mono text-[#64748b]">({members.length})</span>
            )}
          </h2>
          {loading && <Spinner />}
        </div>

        {error && (
          <div className="px-6 py-4 text-sm text-red-400">{error}</div>
        )}

        {!loading && !error && members.length === 0 && (
          <div className="px-6 py-8 text-center text-sm text-[#64748b]">
            No members yet. Invite someone above.
          </div>
        )}

        {members.map((member, idx) => (
          <div
            key={member.userId}
            className={`flex items-center gap-3 px-6 py-4 ${idx < members.length - 1 ? 'border-b border-[#1e2d45]' : ''}`}
          >
            <Avatar name={member.name} email={member.email} />
            <div className="flex-1 min-w-0">
              <p className="text-sm font-medium text-[#e2e8f0] truncate">
                {member.name ?? member.email ?? member.userId}
              </p>
              {member.name && member.email && (
                <p className="text-xs text-[#64748b] truncate">{member.email}</p>
              )}
            </div>

            {/* Role badge / role selector for admin/owner */}
            {canManage ? (
              <select
                value={member.role}
                disabled={!!roleChanging[member.userId]}
                onChange={e => handleRoleChange(member.userId, e.target.value)}
                className="px-2 py-1 rounded-lg bg-[#0d1628] border border-[#1e2d45] text-xs font-mono text-[#94a3b8] outline-none focus:border-[#2DD4BF] transition-all cursor-pointer disabled:opacity-50"
              >
                {ROLES.map(r => (
                  <option key={r} value={r}>
                    {r === 'stakeholder' ? 'stakeholder (free)' : r}
                  </option>
                ))}
              </select>
            ) : (
              <RoleBadge role={member.role} />
            )}

            {/* Remove button — owner/admin only */}
            {canManage && (
              <button
                onClick={() => handleRemove(member.userId, member.email)}
                disabled={!!removing[member.userId]}
                className="ml-1 p-1.5 rounded-lg text-[#64748b] hover:text-red-400 hover:bg-red-500/10 transition-all disabled:opacity-40"
                title="Remove member"
              >
                {removing[member.userId] ? (
                  <Spinner />
                ) : (
                  <svg width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M6 18 18 6M6 6l12 12" />
                  </svg>
                )}
              </button>
            )}
          </div>
        ))}
      </section>
    </div>
  )
}
