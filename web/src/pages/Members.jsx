/**
 * Members page — /settings/members
 * List members of the active org, invite by email+role, change role, remove.
 * Invite/remove controls only shown to owner/admin.
 */
import { useReducer, useEffect, useCallback } from 'react'
import { Loader2, UserPlus, X, Crown, ShieldCheck, User, Eye, CreditCard, Users, Hammer } from 'lucide-react'
import { useOrg } from '../lib/useOrg.js'
import * as api from '../lib/api.js'
import { Card, Badge, Button, StatCard } from '../components/ui/index.js'
import { Reveal } from '../components/Reveal.jsx'

const ROLES = ['owner', 'admin', 'member', 'stakeholder', 'billing']

const ROLE_META = {
  owner: { color: 'yellow', icon: Crown },
  admin: { color: 'indigo', icon: ShieldCheck },
  member: { color: 'default', icon: User },
  stakeholder: { color: 'teal', icon: Eye },
  billing: { color: 'blue', icon: CreditCard },
}

function RoleBadge({ role }) {
  const meta = ROLE_META[role] ?? ROLE_META.member
  const Icon = meta.icon
  return (
    <Badge color={meta.color}>
      <Icon size={10} />
      {role}
      {role === 'stakeholder' && <span className="opacity-60"> · free</span>}
    </Badge>
  )
}

function Avatar({ name, email }) {
  const initials = name
    ? name.split(' ').map(w => w[0]).join('').slice(0, 2).toUpperCase()
    : (email ?? '?').slice(0, 2).toUpperCase()
  return (
    <div className="w-9 h-9 rounded-full bg-gradient-to-br from-[var(--brand-teal)] to-[var(--brand-indigo)] flex items-center justify-center text-[12px] font-bold text-[#0B1120] shrink-0 select-none">
      {initials}
    </div>
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
    case 'SUCCESS': return { ...state, inviting: false, inviteEmail: '', inviteSuccess: action.msg, inviteLink: action.link ?? null }
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
    inviteLink: null,
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
      const res = await api.post(`/api/orgs/${orgId}/members`, {
        email: inviteState.inviteEmail.trim(),
        role: inviteState.inviteRole,
      })
      inviteDispatch({
        type: 'SUCCESS',
        msg: `Invite created for ${inviteState.inviteEmail.trim()}`,
        link: res?.acceptUrl ?? null,
      })
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
  const { inviteEmail, inviteRole, inviting, inviteError, inviteSuccess, inviteLink } = inviteState

  if (!activeOrg) {
    return (
      <div className="max-w-2xl">
        <div className="mb-8 flex items-start gap-3">
          <span className="mt-0.5 grid place-items-center w-9 h-9 rounded-[var(--radius-btn)] bg-[var(--brand-teal)]/10 border border-[var(--brand-teal)]/20 shrink-0">
            <Users size={17} className="text-[var(--brand-teal)]" />
          </span>
          <h1 className="font-display text-2xl font-semibold text-[var(--text)] tracking-tight">Members</h1>
        </div>
        <Card padding="xl" className="text-center">
          <Users size={22} className="mx-auto text-[var(--text-faint)] mb-2" />
          <p className="text-sm text-[var(--text-faint)]">No active organization. Create or select one from the sidebar.</p>
        </Card>
      </div>
    )
  }

  const seatCount = members.filter(m => m.role !== 'stakeholder').length
  const stakeholderCount = members.filter(m => m.role === 'stakeholder').length

  return (
    <div className="w-full">
      <Reveal>
        <div className="mb-6 flex items-start gap-3">
          <span className="mt-0.5 grid place-items-center w-9 h-9 rounded-[var(--radius-btn)] bg-[var(--brand-teal)]/10 border border-[var(--brand-teal)]/20 shrink-0">
            <Users size={17} className="text-[var(--brand-teal)]" />
          </span>
          <div>
            <h1 className="font-display text-2xl font-semibold text-[var(--text)] tracking-tight">Members</h1>
            <p className="text-sm text-[var(--text-faint)] mt-1">
              Manage who has access to <span className="text-[var(--text-dim)] font-medium">{activeOrg.name}</span>.
              {' '}Stakeholders are always <span className="text-[var(--brand-teal)] font-medium">free</span> — no seat cost.
            </p>
          </div>
        </div>
      </Reveal>

      {/* Headline counts */}
      {!loading && members.length > 0 && (
        <Reveal delay={0.04}>
          <div className="grid grid-cols-1 sm:grid-cols-3 gap-4 mb-6">
            <StatCard
              label="Members"
              value={members.length.toLocaleString()}
              sublabel={`${members.length === 1 ? 'person' : 'people'} in this org`}
              accent="var(--chart-2)"
              icon={<Users size={14} />}
            />
            <StatCard
              label="Billable builders"
              value={seatCount.toLocaleString()}
              sublabel="owner / admin / member"
              accent="var(--chart-1)"
              icon={<Hammer size={14} />}
            />
            <StatCard
              label="Free stakeholders"
              value={stakeholderCount.toLocaleString()}
              sublabel="clients & viewers — no seat cost"
              accent="var(--ok)"
              icon={<Eye size={14} />}
            />
          </div>
        </Reveal>
      )}

      {/* Invite form */}
      {canManage && (
        <Reveal delay={0.05}>
          <Card padding="lg" className="mb-4">
            <div className="flex items-center gap-2 mb-1">
              <UserPlus size={15} className="text-[var(--brand-teal)]" />
              <h2 className="text-sm font-semibold text-[var(--text)]">Invite a member</h2>
            </div>
            <p className="text-xs text-[var(--text-faint)] mb-4">
              Stakeholder seats are <span className="text-[var(--brand-teal)] font-medium">free</span> — perfect for clients and external viewers.
            </p>
            <form onSubmit={handleInvite} className="flex gap-2 flex-wrap">
              <input
                type="email"
                required
                aria-required="true"
                aria-label="Invitee email address"
                value={inviteEmail}
                onChange={e => inviteDispatch({ type: 'SET_EMAIL', value: e.target.value })}
                placeholder="colleague@example.com"
                className="flex-1 min-w-[200px] px-3 py-2 rounded-[var(--radius-btn)] bg-[var(--bg)] border border-[var(--border)] text-sm text-[var(--text)] placeholder-[var(--text-faint)] outline-none focus:border-[var(--brand-teal)] focus:ring-2 focus:ring-[var(--brand-teal)]/15 transition-all"
              />
              <select
                value={inviteRole}
                aria-label="Invitee role"
                onChange={e => inviteDispatch({ type: 'SET_ROLE', value: e.target.value })}
                className="px-3 py-2 rounded-[var(--radius-btn)] bg-[var(--bg)] border border-[var(--border)] text-sm text-[var(--text)] outline-none focus:border-[var(--brand-teal)] transition-all cursor-pointer"
              >
                {ROLES.map(r => (
                  <option key={r} value={r}>
                    {r === 'stakeholder' ? 'Stakeholder (free)' : r.charAt(0).toUpperCase() + r.slice(1)}
                  </option>
                ))}
              </select>
              <Button
                type="submit"
                disabled={inviting || !inviteEmail.trim()}
                leftIcon={inviting ? <Loader2 size={14} className="animate-spin" /> : <UserPlus size={14} />}
              >
                {inviting ? 'Sending…' : 'Invite'}
              </Button>
            </form>
            <div aria-live="polite">
              {inviteError && <p role="alert" className="mt-2 text-xs text-[var(--bad)]">{inviteError}</p>}
              {inviteSuccess && <p className="mt-2 text-xs text-[var(--ok)]">{inviteSuccess}</p>}
            </div>
            {inviteLink && (
              <div className="mt-2 flex items-center gap-2 flex-wrap">
                <span className="text-xs text-[var(--text-muted)]">No email configured — share this link:</span>
                <code className="text-xs px-2 py-1 rounded bg-[var(--bg-surface)] border border-[var(--border)] text-[var(--text)] max-w-full truncate">{inviteLink}</code>
                <button
                  type="button"
                  aria-label="Copy invite link"
                  onClick={() => navigator.clipboard?.writeText(inviteLink)}
                  className="text-xs px-2 py-1 rounded border border-[var(--border)] hover:border-[var(--brand-teal)] text-[var(--text-muted)] hover:text-[var(--brand-teal)] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--brand-teal)]"
                >
                  Copy
                </button>
              </div>
            )}
          </Card>
        </Reveal>
      )}

      {/* Members list */}
      <Reveal delay={0.1}>
        <Card padding="none" className="overflow-hidden">
          <div className="px-6 py-4 border-b border-[var(--border)] flex items-center justify-between gap-3 flex-wrap">
            <h2 className="text-sm font-semibold text-[var(--text)] flex items-center gap-2">
              <Users size={15} className="text-[var(--text-faint)]" />
              Members
              {!loading && members.length > 0 && (
                <span className="text-xs font-mono text-[var(--text-faint)]">({members.length})</span>
              )}
            </h2>
            <div className="flex items-center gap-3">
              {!loading && members.length > 0 && (
                <span
                  className="text-[11px] font-mono text-[var(--text-faint)]"
                  title={`${members.length} ${members.length === 1 ? 'person' : 'people'} total — ${seatCount} billable builder${seatCount !== 1 ? 's' : ''}, ${stakeholderCount} free stakeholder${stakeholderCount !== 1 ? 's' : ''}`}
                >
                  <span className="text-[var(--text-muted)]">{members.length} {members.length === 1 ? 'person' : 'people'}</span>
                  {' = '}
                  {seatCount} billable builder{seatCount !== 1 ? 's' : ''}
                  {stakeholderCount > 0 && (
                    <span className="text-[var(--brand-teal)]"> + {stakeholderCount} free stakeholder{stakeholderCount !== 1 ? 's' : ''}</span>
                  )}
                </span>
              )}
              {loading && <Loader2 size={15} className="animate-spin text-[var(--brand-teal)]" />}
            </div>
          </div>

          {error && (
            <div className="px-6 py-4 text-sm text-[var(--bad)]">{error}</div>
          )}

          {loading && members.length === 0 && (
            <div className="divide-y divide-[var(--border)]">
              {Array.from({ length: 4 }).map((_, i) => (
                <div key={i} className="flex items-center gap-3 px-6 py-4 animate-pulse">
                  <div className="w-9 h-9 rounded-full bg-[var(--bg-surface3)]" />
                  <div className="flex-1 space-y-2">
                    <div className="h-3 w-32 rounded bg-[var(--bg-surface3)]" />
                    <div className="h-2 w-44 rounded bg-[var(--bg-surface2)]" />
                  </div>
                </div>
              ))}
            </div>
          )}

          {!loading && !error && members.length === 0 && (
            <div className="px-6 py-10 text-center">
              <Users size={20} className="mx-auto text-[var(--text-faint)] mb-2" />
              <p className="text-sm text-[var(--text-faint)]">No members yet. Invite someone above.</p>
            </div>
          )}

          {members.map((member, idx) => (
            <div
              key={member.userId}
              className={`group flex items-center gap-3 px-6 py-4 hover:bg-[var(--bg-surface2)]/60 transition-colors ${idx < members.length - 1 ? 'border-b border-[var(--border)]' : ''}`}
            >
              <Avatar name={member.name} email={member.email} />
              <div className="flex-1 min-w-0">
                <p className="text-sm font-medium text-[var(--text)] truncate">
                  {member.name ?? member.email ?? member.userId}
                </p>
                {member.name && member.email && (
                  <p className="text-xs text-[var(--text-faint)] truncate">{member.email}</p>
                )}
              </div>

              {/* Role selector / badge */}
              {canManage ? (
                <select
                  value={member.role}
                  disabled={!!roleChanging[member.userId]}
                  aria-label={`Role for ${member.name ?? member.email ?? 'member'}`}
                  onChange={e => handleRoleChange(member.userId, e.target.value)}
                  className="px-2.5 py-1.5 rounded-[var(--radius-badge)] bg-[var(--bg)] border border-[var(--border)] text-xs font-mono text-[var(--text-muted)] outline-none focus:border-[var(--brand-teal)] hover:border-[var(--border2)] transition-all cursor-pointer disabled:opacity-50"
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

              {/* Remove button */}
              {canManage && (
                <button
                  type="button"
                  onClick={() => handleRemove(member.userId, member.email)}
                  disabled={!!removing[member.userId]}
                  className="ml-1 p-1.5 rounded-[var(--radius-badge)] text-[var(--text-faint)] opacity-0 group-hover:opacity-100 focus:opacity-100 hover:text-[var(--bad)] hover:bg-[color-mix(in_srgb,var(--bad)_10%,transparent)] transition-all disabled:opacity-40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--brand-teal)]"
                  aria-label={`Remove ${member.name ?? member.email ?? 'member'}`}
                  title="Remove member"
                >
                  {removing[member.userId] ? <Loader2 size={14} className="animate-spin" aria-hidden="true" /> : <X size={14} aria-hidden="true" />}
                </button>
              )}
            </div>
          ))}
        </Card>
      </Reveal>
    </div>
  )
}
