/**
 * Leave & Capacity — /capacity
 *
 * A real leave-management experience plus the capacity summary:
 *   • My leave     — balances per type (ring) + a request-leave form
 *   • Team calendar — month grid of who's off, coloured by leave type
 *   • Approvals     — pending requests with approve/reject (owner/admin)
 *   • Capacity      — effective capacity (availability − approved leave) + editor
 *
 * Data:
 *   GET /api/leave-types · GET /api/leave-balances?user=&year= · GET /api/leave
 *   POST /api/leave · PATCH /api/leave/{id} · GET /api/capacity · PUT /api/availability
 *   Member identities from GET /api/orgs/:id/members.
 */
import { useState, useEffect, useMemo } from 'react'
import {
  Loader2, X, CalendarDays, Clock, Plane, Pencil, AlertCircle, Users,
  CalendarHeart, CalendarCheck, Gauge, Link2, CircleCheck,
} from 'lucide-react'
import { useCapacity } from '../lib/useCapacity.js'
import { useLeave } from '../lib/useLeave.js'
import { useOrg } from '../lib/useOrg.js'
import { useAuth } from '../lib/useAuth.js'
import * as api from '../lib/api.js'
import { Card, Badge, Button } from '../components/ui/index.js'
import { Reveal, RevealList } from '../components/Reveal.jsx'
import {
  BalanceRing, RequestLeaveForm, TeamCalendar, ApprovalQueue,
} from '../components/leave/index.js'
import { typeIndex, shortDate, leaveDays, statusColor } from '../components/leave/leaveUtils.js'

const PERIODS = [
  { id: '7d', label: '7 days', days: 7 },
  { id: '30d', label: '30 days', days: 30 },
  { id: '90d', label: '90 days', days: 90 },
]

/** Convert a period id into an ISO date interval the API understands. */
function periodToInterval(id) {
  const def = PERIODS.find(p => p.id === id) ?? PERIODS[1]
  const end = new Date()
  const start = new Date()
  start.setDate(start.getDate() - def.days)
  const fmt = d => d.toISOString().slice(0, 10)
  return `${fmt(start)}/${fmt(end)}`
}

function initials(name, email) {
  return name
    ? name.split(' ').map(w => w[0]).join('').slice(0, 2).toUpperCase()
    : (email ?? '?').slice(0, 2).toUpperCase()
}

function Avatar({ name, email, size = 36 }) {
  return (
    <div
      className="rounded-full bg-gradient-to-br from-[var(--brand-teal)] to-[var(--brand-indigo)] flex items-center justify-center font-bold text-[#0B1120] select-none shrink-0"
      style={{ width: size, height: size, fontSize: size * 0.32 }}
    >
      {initials(name, email)}
    </div>
  )
}

/** Stacked bar: effective (filled) + leave (hatched) against the team max. */
function CapacityBar({ effective, leave, max }) {
  const effPct = max > 0 ? Math.min(100, (effective / max) * 100) : 0
  const leavePct = max > 0 ? Math.min(100 - effPct, (leave / max) * 100) : 0
  const color = effPct > 75 ? '#22c55e' : effPct > 40 ? 'var(--brand-teal)' : '#f59e0b'
  return (
    <div className="flex h-2 rounded-full bg-[var(--border)] overflow-hidden">
      <div className="h-full transition-all duration-500" style={{ width: `${effPct}%`, background: color }} />
      <div
        className="h-full transition-all duration-500"
        style={{
          width: `${leavePct}%`,
          background: 'repeating-linear-gradient(45deg, var(--text-faint) 0 2px, transparent 2px 5px)',
          opacity: 0.5,
        }}
      />
    </div>
  )
}

function CapacityCard({ member, maxHours }) {
  const { updateAvailability } = useCapacity()
  const [editing, setEditing] = useState(false)
  const [weeklyHours, setWeeklyHours] = useState(member.weeklyHours ?? Math.round((member.availableHours ?? 40)))
  const [daysPerWeek, setDaysPerWeek] = useState(member.daysPerWeek ?? 5)
  const [saving, setSaving] = useState(false)

  const effective = member.effectiveHours ?? 0
  const available = member.availableHours ?? 0
  const leave = member.approvedLeaveHours ?? 0
  const loggedHours = member.loggedMinutes != null ? Math.round(member.loggedMinutes / 60) : null
  const utilPct = available > 0 ? Math.round((effective / available) * 100) : null

  async function handleSave() {
    setSaving(true)
    try {
      const days = Math.max(1, Math.min(7, Number(daysPerWeek)))
      await updateAvailability(member.userId, {
        weeklyHours: Number(weeklyHours),
        workingDays: Array.from({ length: days }, (_, i) => i + 1),
      })
      setEditing(false)
    } catch {
      /* surfaced elsewhere */
    } finally {
      setSaving(false)
    }
  }

  return (
    <Card padding="md" hoverable className="flex flex-col gap-3.5">
      <div className="flex items-center gap-3">
        <Avatar name={member.name} email={member.email} />
        <div className="flex-1 min-w-0">
          <span className="text-sm font-semibold text-[var(--text)] block truncate">{member.name ?? member.email ?? member.userId}</span>
          {member.name && member.email && (
            <span className="text-xs text-[var(--text-faint)] truncate block">{member.email}</span>
          )}
        </div>
        {member.onLeave && <Badge color="yellow" className="shrink-0"><Plane size={10} /> on leave</Badge>}
        <button
          onClick={() => setEditing(v => !v)}
          className="p-1.5 rounded-[var(--radius-badge)] text-[var(--text-faint)] hover:text-[var(--brand-teal)] hover:bg-[var(--bg-surface2)] transition-colors shrink-0"
          title={editing ? 'Cancel' : 'Edit availability'}
        >
          {editing ? <X size={14} /> : <Pencil size={13} />}
        </button>
      </div>

      {/* Effective capacity headline */}
      <div className="flex items-end justify-between">
        <div className="flex items-baseline gap-1.5">
          <span className="text-2xl font-display font-semibold text-[var(--text)] tabular-nums leading-none">{Math.round(effective)}</span>
          <span className="text-xs text-[var(--text-faint)] font-mono">h effective</span>
        </div>
        {utilPct != null && (
          <span className="text-[11px] font-mono text-[var(--text-muted)] tabular-nums">{utilPct}% of available</span>
        )}
      </div>

      <CapacityBar effective={effective} leave={leave} max={maxHours} />

      {/* Detail chips */}
      <div className="flex flex-wrap gap-1.5 text-[11px]">
        <Badge><Clock size={10} /> {Math.round(available)}h available</Badge>
        {leave > 0 && <Badge color="yellow"><Plane size={10} /> −{Math.round(leave)}h leave</Badge>}
        {loggedHours != null && loggedHours > 0 && <Badge color="teal">{loggedHours}h logged</Badge>}
      </div>

      {/* Availability editor */}
      {editing && (
        <div className="rounded-[var(--radius-btn)] px-4 py-3.5 flex flex-col gap-3 bg-[var(--bg)] border border-[var(--border)]">
          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1">
              <label className="text-[10px] text-[var(--text-faint)] uppercase tracking-widest">Weekly hours</label>
              <input
                type="number" min="0" max="80"
                className="bg-[var(--bg-surface)] text-sm text-[var(--text)] tabular-nums rounded-[var(--radius-badge)] px-2.5 py-1.5 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/50 focus:ring-2 focus:ring-[var(--brand-teal)]/15 transition-all"
                value={weeklyHours}
                onChange={e => setWeeklyHours(e.target.value)}
              />
            </div>
            <div className="flex flex-col gap-1">
              <label className="text-[10px] text-[var(--text-faint)] uppercase tracking-widest">Days / week</label>
              <input
                type="number" min="1" max="7"
                className="bg-[var(--bg-surface)] text-sm text-[var(--text)] tabular-nums rounded-[var(--radius-badge)] px-2.5 py-1.5 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/50 focus:ring-2 focus:ring-[var(--brand-teal)]/15 transition-all"
                value={daysPerWeek}
                onChange={e => setDaysPerWeek(e.target.value)}
              />
            </div>
          </div>
          <Button size="sm" onClick={handleSave} disabled={saving} leftIcon={saving ? <Loader2 size={12} className="animate-spin" /> : null} className="self-start">
            Save availability
          </Button>
        </div>
      )}
    </Card>
  )
}

/** A single leave-history row for the My-leave list. */
function LeaveHistoryRow({ entry, type, nameFor, showMember }) {
  const days = leaveDays(entry)
  return (
    <tr className="border-b border-[var(--border)] hover:bg-[var(--bg-surface2)]/60 transition-colors last:border-0">
      {showMember && (
        <td className="px-4 py-3">
          <div className="flex items-center gap-2.5">
            <Avatar name={nameFor?.(entry.userId)} email={entry.userId} size={26} />
            <span className="text-sm text-[var(--text)] truncate">{nameFor?.(entry.userId) ?? entry.userId}</span>
          </div>
        </td>
      )}
      <td className="px-4 py-3">
        {type ? (
          <span className="inline-flex items-center gap-1.5 text-[11px] font-mono px-2 py-0.5 rounded-[var(--radius-badge)] border" style={{ borderColor: type.color + '55', color: type.color }}>
            <span className="w-1.5 h-1.5 rounded-full" style={{ background: type.color }} /> {type.name}
          </span>
        ) : (
          <Badge>{entry.kind ?? '—'}</Badge>
        )}
      </td>
      <td className="px-4 py-3 text-xs text-[var(--text-muted)] font-mono whitespace-nowrap">
        {shortDate(entry.startDate)} → {shortDate(entry.endDate)}
        {entry.halfDay && <span className="text-[var(--text-faint)]"> · ½ {entry.portion}</span>}
      </td>
      <td className="px-4 py-3 text-xs text-[var(--text-muted)] font-mono tabular-nums whitespace-nowrap">{days}{days === 1 ? ' day' : ' days'}</td>
      <td className="px-4 py-3"><Badge color={statusColor(entry.status)}>{entry.status}</Badge></td>
      <td className="px-4 py-3 text-xs text-[var(--text-faint)] truncate max-w-[180px]">{entry.note || '—'}</td>
    </tr>
  )
}

const TABS = [
  { id: 'mine', label: 'My leave', icon: CalendarHeart },
  { id: 'calendar', label: 'Team calendar', icon: CalendarDays },
  { id: 'approvals', label: 'Approvals', icon: CalendarCheck, manage: true },
  { id: 'capacity', label: 'Capacity', icon: Gauge },
]

export default function Capacity() {
  const [tab, setTab] = useState('mine')
  const [period, setPeriod] = useState('30d')
  const { activeOrg, orgRole } = useOrg()
  const { user } = useAuth()
  const meId = user?.id ?? null
  const canManage = orgRole === 'owner' || orgRole === 'admin'

  const { capacity, capacityLoading, error: capError } = useCapacity({ period: periodToInterval(period) })
  const {
    types, balances, leave, loading: leaveLoading, error: leaveError,
    requestLeave, decideLeave,
  } = useLeave({})

  // Resolve userId → member identity.
  const [memberMap, setMemberMap] = useState({})
  useEffect(() => {
    const orgId = activeOrg?.id
    if (!orgId) return
    let cancelled = false
    api.get(`/api/orgs/${orgId}/members`)
      .then(data => {
        if (cancelled) return
        const map = {}
        ;(Array.isArray(data) ? data : []).forEach(m => { map[m.userId] = m })
        setMemberMap(map)
      })
      .catch(() => {})
    return () => { cancelled = true }
  }, [activeOrg?.id])

  const nameFor = id => memberMap[id]?.name ?? memberMap[id]?.email ?? null
  const tIdx = useMemo(() => typeIndex(types), [types])

  const memberList = useMemo(
    () => Object.values(memberMap).map(m => ({ userId: m.userId, name: m.name, email: m.email })),
    [memberMap],
  )

  const enriched = useMemo(() => capacity.map(m => ({
    ...m,
    name: m.name ?? memberMap[m.userId]?.name,
    email: m.email ?? memberMap[m.userId]?.email,
  })), [capacity, memberMap])

  const maxHours = Math.max(1, ...enriched.map(m => Math.max(m.availableHours ?? 0, m.effectiveHours ?? 0)))

  // My balances: when we know who "me" is, scope balances; otherwise show all.
  const myBalances = useMemo(() => {
    if (!meId) return balances
    const mine = balances.filter(b => b.userId === meId)
    return mine.length ? mine : balances
  }, [balances, meId])

  const myLeave = useMemo(() => {
    const rows = meId ? leave.filter(e => e.userId === meId) : leave
    return rows.length || !meId ? rows : leave
  }, [leave, meId])

  const pending = useMemo(() => leave.filter(e => e.status === 'pending'), [leave])

  const totals = useMemo(() => ({
    effective: Math.round(enriched.reduce((s, m) => s + (m.effectiveHours ?? 0), 0)),
    leave: Math.round(enriched.reduce((s, m) => s + (m.approvedLeaveHours ?? 0), 0)),
    members: enriched.length,
  }), [enriched])

  const visibleTabs = TABS.filter(t => !t.manage || canManage)

  return (
    <div className="w-full space-y-8">
      {/* Header */}
      <Reveal>
        <div className="flex items-end justify-between gap-4 flex-wrap">
          <div>
            <h1 className="font-display text-2xl font-semibold text-[var(--text)] tracking-tight">Leave &amp; Capacity</h1>
            <p className="text-sm text-[var(--text-faint)] mt-1">
              Configurable leave types, per-person balances, a team calendar, and effective capacity.
            </p>
          </div>
          <CalendarConnectedHint />
        </div>
      </Reveal>

      {/* Tabs */}
      <Reveal delay={0.04}>
        <div className="flex items-center gap-1 border-b border-[var(--border)]">
          {visibleTabs.map(t => {
            const Icon = t.icon
            const active = tab === t.id
            return (
              <button
                key={t.id}
                onClick={() => setTab(t.id)}
                className={[
                  'inline-flex items-center gap-1.5 px-3.5 py-2.5 text-sm font-medium border-b-2 -mb-px transition-colors',
                  active
                    ? 'border-[var(--brand-teal)] text-[var(--text)]'
                    : 'border-transparent text-[var(--text-faint)] hover:text-[var(--text-muted)]',
                ].join(' ')}
              >
                <Icon size={14} className={active ? 'text-[var(--brand-teal)]' : ''} />
                {t.label}
                {t.id === 'approvals' && pending.length > 0 && (
                  <span className="ml-0.5 text-[10px] font-mono px-1.5 py-0.5 rounded-full bg-[var(--brand-teal)]/15 text-[var(--brand-teal)]">{pending.length}</span>
                )}
              </button>
            )
          })}
        </div>
      </Reveal>

      {(leaveError || capError) && (
        <Card className="border-red-500/20 bg-red-500/[0.04]">
          <p className="flex items-center gap-2 text-sm text-red-400"><AlertCircle size={15} /> {leaveError || capError} — the backend may not be running yet.</p>
        </Card>
      )}

      {/* ── My leave ─────────────────────────────────────────────── */}
      {tab === 'mine' && (
        <div className="space-y-8">
          <section>
            <div className="flex flex-col gap-4 mb-4 sm:flex-row sm:items-start sm:justify-between">
              <div>
                <h2 className="text-sm font-semibold text-[var(--text)]">Your balances</h2>
                <p className="text-xs text-[var(--text-faint)] mt-0.5">Days remaining this year, per leave type.</p>
              </div>
              <RequestLeaveForm types={types} members={memberList} canPickMember={canManage} onSubmit={requestLeave} />
            </div>

            {leaveLoading && myBalances.length === 0 && (
              <div className="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-4 gap-4">
                {Array.from({ length: 4 }).map((_, i) => (
                  <div key={i} className="rounded-[var(--radius-card)] h-28 animate-pulse bg-[var(--bg-surface)] border border-[var(--border)]" />
                ))}
              </div>
            )}

            {!leaveLoading && myBalances.length === 0 && (
              <Card padding="xl" className="border-dashed text-center">
                <CalendarHeart size={22} className="mx-auto text-[var(--text-faint)] mb-2" />
                <p className="text-sm text-[var(--text)] mb-1">No balances yet</p>
                <p className="text-xs text-[var(--text-faint)]">Leave-type entitlements will show up here once configured.</p>
              </Card>
            )}

            {myBalances.length > 0 && (
              <RevealList className="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-4 gap-4" staggerDelay={0.04}>
                {myBalances.map(b => (
                  <Card key={b.leaveTypeId + b.userId} padding="md">
                    <BalanceRing type={tIdx[b.leaveTypeId]} balance={b} />
                  </Card>
                ))}
              </RevealList>
            )}
          </section>

          <section>
            <h2 className="text-sm font-semibold text-[var(--text)] mb-4">
              {meId ? 'Your leave history' : 'Recent leave'}
            </h2>
            {myLeave.length === 0 ? (
              <Card padding="xl" className="border-dashed text-center">
                <Plane size={22} className="mx-auto text-[var(--text-faint)] mb-2" />
                <p className="text-sm text-[var(--text)] mb-1">No leave recorded</p>
                <p className="text-xs text-[var(--text-faint)]">Request leave above to get started.</p>
              </Card>
            ) : (
              <Card padding="none" className="overflow-hidden">
                <table className="w-full">
                  <thead>
                    <tr className="bg-[var(--bg-surface2)]/40 border-b border-[var(--border)]">
                      {[...(meId ? [] : ['Member']), 'Type', 'Dates', 'Days', 'Status', 'Note'].map(h => (
                        <th key={h} className="text-left px-4 py-2.5 text-[10px] font-semibold text-[var(--text-faint)] uppercase tracking-widest">{h}</th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {myLeave.map((e, i) => (
                      <LeaveHistoryRow key={e.id ?? i} entry={e} type={tIdx[e.leaveTypeId]} nameFor={nameFor} showMember={!meId} />
                    ))}
                  </tbody>
                </table>
              </Card>
            )}
          </section>
        </div>
      )}

      {/* ── Team calendar ────────────────────────────────────────── */}
      {tab === 'calendar' && (
        <section>
          <TeamCalendar leave={leave} types={types} nameFor={nameFor} />
        </section>
      )}

      {/* ── Approvals ────────────────────────────────────────────── */}
      {tab === 'approvals' && canManage && (
        <section>
          <div className="mb-4">
            <h2 className="text-sm font-semibold text-[var(--text)]">Pending requests</h2>
            <p className="text-xs text-[var(--text-faint)] mt-0.5">Approve or reject. Approving updates the member&apos;s balance.</p>
          </div>
          <ApprovalQueue pending={pending} types={types} nameFor={nameFor} onDecide={decideLeave} />
        </section>
      )}

      {/* ── Capacity ─────────────────────────────────────────────── */}
      {tab === 'capacity' && (
        <div className="space-y-8">
          <section>
            <div className="flex items-center justify-between mb-4 gap-4 flex-wrap">
              <div>
                <h2 className="text-sm font-semibold text-[var(--text)]">Effective capacity</h2>
                <p className="text-xs text-[var(--text-faint)] mt-0.5">Available hours minus approved leave, over the selected window.</p>
              </div>
              <div className="flex items-center rounded-[var(--radius-btn)] p-0.5 gap-0.5 w-fit bg-[var(--bg)] border border-[var(--border)]">
                {PERIODS.map(p => (
                  <button
                    key={p.id}
                    onClick={() => setPeriod(p.id)}
                    className={[
                      'px-3 py-1.5 rounded-[6px] text-xs font-medium transition-all duration-150',
                      period === p.id ? 'bg-[var(--bg-surface2)] text-[var(--brand-teal)]' : 'text-[var(--text-faint)] hover:text-[var(--text-muted)]',
                    ].join(' ')}
                  >
                    {p.label}
                  </button>
                ))}
              </div>
            </div>

            {!capacityLoading && enriched.length > 0 && (
              <div className="grid grid-cols-3 gap-3 mb-4">
                <Card padding="sm" className="flex flex-col gap-1">
                  <span className="text-[10px] font-mono uppercase tracking-widest text-[var(--text-faint)] flex items-center gap-1"><Users size={11} /> Team</span>
                  <span className="text-2xl font-display font-semibold text-[var(--text)] tabular-nums leading-none">{totals.members}</span>
                </Card>
                <Card padding="sm" className="flex flex-col gap-1">
                  <span className="text-[10px] font-mono uppercase tracking-widest text-[var(--text-faint)] flex items-center gap-1"><Clock size={11} /> Effective</span>
                  <span className="text-2xl font-display font-semibold text-[var(--text)] tabular-nums leading-none">{totals.effective}<span className="text-sm text-[var(--text-faint)]">h</span></span>
                </Card>
                <Card padding="sm" className="flex flex-col gap-1">
                  <span className="text-[10px] font-mono uppercase tracking-widest text-[var(--text-faint)] flex items-center gap-1"><Plane size={11} /> On leave</span>
                  <span className="text-2xl font-display font-semibold text-[var(--text)] tabular-nums leading-none">{totals.leave}<span className="text-sm text-[var(--text-faint)]">h</span></span>
                </Card>
              </div>
            )}

            {capacityLoading && enriched.length === 0 && (
              <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
                {Array.from({ length: 6 }).map((_, i) => (
                  <div key={i} className="rounded-[var(--radius-card)] h-44 animate-pulse bg-[var(--bg-surface)] border border-[var(--border)]" />
                ))}
              </div>
            )}

            {!capacityLoading && enriched.length === 0 && !capError && (
              <Card padding="xl" className="border-dashed text-center">
                <Users size={22} className="mx-auto text-[var(--text-faint)] mb-2" />
                <p className="text-sm text-[var(--text)] mb-1">No capacity data yet</p>
                <p className="text-xs text-[var(--text-faint)]">Invite team members to calculate availability and leave.</p>
              </Card>
            )}

            {enriched.length > 0 && (
              <RevealList className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4" staggerDelay={0.04}>
                {enriched.map(m => (
                  <CapacityCard key={m.userId ?? m.email} member={m} maxHours={maxHours} />
                ))}
              </RevealList>
            )}
          </section>
        </div>
      )}
    </div>
  )
}

/** A subtle "calendar connected" hint that links to Settings, shown only when
 *  the (optional, read-only) /api/calendar/status endpoint reports connected. */
function CalendarConnectedHint() {
  const [connected, setConnected] = useState(false)
  useEffect(() => {
    let cancelled = false
    api.get('/api/calendar/status')
      .then(s => { if (!cancelled) setConnected(Boolean(s?.connected)) })
      .catch(() => {}) // endpoint may not exist — stay hidden
    return () => { cancelled = true }
  }, [])
  if (!connected) return null
  return (
    <a
      href="/settings"
      className="inline-flex items-center gap-1.5 text-xs text-[var(--text-muted)] hover:text-[var(--brand-teal)] transition-colors px-2.5 py-1.5 rounded-[var(--radius-badge)] border border-[var(--border)]"
    >
      <CircleCheck size={13} className="text-[var(--brand-teal)]" /> Calendar connected
      <Link2 size={11} className="opacity-60" />
    </a>
  )
}
