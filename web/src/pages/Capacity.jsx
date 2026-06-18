/**
 * Capacity page — /capacity
 * Effective capacity per member = available hours − approved leave.
 * Data: GET /api/capacity?period=YYYY-MM-DD/YYYY-MM-DD (userId-keyed),
 * GET /api/leave, POST /api/leave, PUT /api/availability.
 * Member names are resolved from GET /api/orgs/:id/members.
 */
import { useState, useEffect, useMemo } from 'react'
import {
  Loader2, Plus, X, CalendarDays, Clock, Plane, Pencil, AlertCircle, Users,
} from 'lucide-react'
import { useCapacity } from '../lib/useCapacity.js'
import { useOrg } from '../lib/useOrg.js'
import * as api from '../lib/api.js'
import { Card, Badge, Button } from '../components/ui/index.js'
import { Reveal, RevealList } from '../components/Reveal.jsx'

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

const LEAVE_KINDS = [
  { id: 'pto', label: 'PTO', color: 'teal' },
  { id: 'sick', label: 'Sick', color: 'yellow' },
  { id: 'holiday', label: 'Holiday', color: 'indigo' },
]

function leaveKindMeta(kind) {
  return LEAVE_KINDS.find(k => k.id === kind) ?? { id: kind, label: kind ?? '—', color: 'default' }
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

function AddLeaveForm({ onAdd, members }) {
  const [open, setOpen] = useState(false)
  const [userId, setUserId] = useState('')
  const [kind, setKind] = useState('pto')
  const [startDate, setStartDate] = useState('')
  const [endDate, setEndDate] = useState('')
  const [note, setNote] = useState('')
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState(null)

  async function handleSubmit(e) {
    e.preventDefault()
    if (!userId || !startDate || !endDate) return
    setSaving(true)
    setErr(null)
    try {
      await onAdd({ userId, kind, startDate, endDate, note })
      setUserId(''); setKind('pto'); setStartDate(''); setEndDate(''); setNote('')
      setOpen(false)
    } catch (e) {
      setErr(e.message ?? 'Failed to add leave')
    } finally {
      setSaving(false)
    }
  }

  const inputCls = 'bg-[var(--bg)] text-sm text-[var(--text)] rounded-[var(--radius-btn)] px-3 py-2 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/50 focus:ring-2 focus:ring-[var(--brand-teal)]/15 transition-all w-full'

  return (
    <div>
      <Button
        variant={open ? 'outline' : 'primary'}
        size="sm"
        onClick={() => setOpen(v => !v)}
        leftIcon={open ? <X size={13} /> : <Plus size={13} strokeWidth={2.5} />}
      >
        {open ? 'Cancel' : 'Add leave'}
      </Button>

      {open && (
        <form onSubmit={handleSubmit} className="mt-4">
          <Card padding="md" className="flex flex-col gap-4">
            <div className="grid sm:grid-cols-2 gap-4">
              <div className="flex flex-col gap-1.5">
                <label className="text-[10px] text-[var(--text-faint)] uppercase tracking-widest">Member</label>
                {members.length > 0 ? (
                  <select required className={inputCls + ' cursor-pointer'} value={userId} onChange={e => setUserId(e.target.value)}>
                    <option value="">Select member…</option>
                    {members.map(m => (
                      <option key={m.userId} value={m.userId}>{m.name ?? m.email ?? m.userId}</option>
                    ))}
                  </select>
                ) : (
                  <input required className={inputCls} placeholder="user@example.com" value={userId} onChange={e => setUserId(e.target.value)} />
                )}
              </div>
              <div className="flex flex-col gap-1.5">
                <label className="text-[10px] text-[var(--text-faint)] uppercase tracking-widest">Kind</label>
                <select className={inputCls + ' cursor-pointer'} value={kind} onChange={e => setKind(e.target.value)}>
                  {LEAVE_KINDS.map(k => <option key={k.id} value={k.id}>{k.label}</option>)}
                </select>
              </div>
              <div className="flex flex-col gap-1.5">
                <label className="text-[10px] text-[var(--text-faint)] uppercase tracking-widest">Start date</label>
                <input type="date" required className={inputCls} value={startDate} onChange={e => setStartDate(e.target.value)} />
              </div>
              <div className="flex flex-col gap-1.5">
                <label className="text-[10px] text-[var(--text-faint)] uppercase tracking-widest">End date</label>
                <input type="date" required className={inputCls} value={endDate} onChange={e => setEndDate(e.target.value)} />
              </div>
              <div className="flex flex-col gap-1.5 sm:col-span-2">
                <label className="text-[10px] text-[var(--text-faint)] uppercase tracking-widest">Note (optional)</label>
                <input className={inputCls} placeholder="e.g. Annual leave" value={note} onChange={e => setNote(e.target.value)} />
              </div>
            </div>
            {err && (
              <p className="flex items-center gap-2 text-xs text-red-400"><AlertCircle size={13} /> {err}</p>
            )}
            <Button type="submit" disabled={saving || !userId || !startDate || !endDate} className="self-start" leftIcon={saving ? <Loader2 size={12} className="animate-spin" /> : null}>
              Add leave
            </Button>
          </Card>
        </form>
      )}
    </div>
  )
}

function LeaveRow({ entry, nameFor }) {
  const meta = leaveKindMeta(entry.kind)
  const fmt = d => (d ? new Date(d).toLocaleDateString(undefined, { month: 'short', day: 'numeric' }) : '—')
  const display = entry.userName ?? nameFor(entry.userId) ?? entry.userId ?? '—'
  return (
    <tr className="border-b border-[var(--border)] hover:bg-[var(--bg-surface2)]/60 transition-colors last:border-0">
      <td className="px-4 py-3">
        <div className="flex items-center gap-2.5">
          <Avatar name={entry.userName ?? nameFor(entry.userId)} email={entry.userId} size={26} />
          <span className="text-sm text-[var(--text)] truncate">{display}</span>
        </div>
      </td>
      <td className="px-4 py-3"><Badge color={meta.color}>{meta.label}</Badge></td>
      <td className="px-4 py-3 text-xs text-[var(--text-muted)] font-mono whitespace-nowrap">
        {fmt(entry.startDate)} → {fmt(entry.endDate)}
      </td>
      <td className="px-4 py-3">
        {entry.status && (
          <Badge color={entry.status === 'approved' ? 'green' : entry.status === 'rejected' ? 'red' : 'default'}>{entry.status}</Badge>
        )}
      </td>
      <td className="px-4 py-3 text-xs text-[var(--text-faint)] truncate max-w-[180px]">{entry.note ?? '—'}</td>
    </tr>
  )
}

export default function Capacity() {
  const [period, setPeriod] = useState('30d')
  const { activeOrg } = useOrg()
  const { capacity, leave, capacityLoading, leaveLoading, error, addLeave } = useCapacity({ period: periodToInterval(period) })

  // Resolve userId → member identity (additive; capacity API is userId-keyed only).
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

  const enriched = useMemo(() => capacity.map(m => ({
    ...m,
    name: m.name ?? memberMap[m.userId]?.name,
    email: m.email ?? memberMap[m.userId]?.email,
  })), [capacity, memberMap])

  const memberList = useMemo(
    () => Object.values(memberMap).map(m => ({ userId: m.userId, name: m.name, email: m.email })),
    [memberMap],
  )

  const maxHours = Math.max(1, ...enriched.map(m => Math.max(m.availableHours ?? 0, m.effectiveHours ?? 0)))

  const totals = useMemo(() => ({
    effective: Math.round(enriched.reduce((s, m) => s + (m.effectiveHours ?? 0), 0)),
    leave: Math.round(enriched.reduce((s, m) => s + (m.approvedLeaveHours ?? 0), 0)),
    members: enriched.length,
  }), [enriched])

  return (
    <div className="max-w-5xl space-y-8">
      {/* Header */}
      <Reveal>
        <div className="flex items-end justify-between gap-4 flex-wrap">
          <div>
            <h1 className="font-display text-2xl font-semibold text-[var(--text)] tracking-tight">Capacity</h1>
            <p className="text-sm text-[var(--text-faint)] mt-1">
              Effective capacity per member — available hours minus approved leave.
            </p>
          </div>
          {/* Period filter */}
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
      </Reveal>

      {/* Totals strip */}
      {!capacityLoading && enriched.length > 0 && (
        <Reveal delay={0.05}>
          <div className="grid grid-cols-3 gap-3">
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
        </Reveal>
      )}

      {/* Error */}
      {error && (
        <Card className="border-red-500/20 bg-red-500/[0.04]">
          <p className="flex items-center gap-2 text-sm text-red-400"><AlertCircle size={15} /> {error} — the backend may not be running yet.</p>
        </Card>
      )}

      {/* Capacity grid */}
      <section>
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-sm font-semibold text-[var(--text)]">Team capacity</h2>
          {capacityLoading && <Loader2 size={15} className="animate-spin text-[var(--brand-teal)]" />}
        </div>

        {capacityLoading && enriched.length === 0 && (
          <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
            {Array.from({ length: 6 }).map((_, i) => (
              <div key={i} className="rounded-[var(--radius-card)] h-44 animate-pulse bg-[var(--bg-surface)] border border-[var(--border)]" />
            ))}
          </div>
        )}

        {!capacityLoading && enriched.length === 0 && !error && (
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

      {/* Leave */}
      <section>
        <div className="flex items-center justify-between mb-4 gap-4 flex-wrap">
          <div>
            <h2 className="text-sm font-semibold text-[var(--text)]">Leave schedule</h2>
            <p className="text-xs text-[var(--text-faint)] mt-0.5">Planned absences that reduce effective capacity.</p>
          </div>
          <AddLeaveForm onAdd={addLeave} members={memberList} />
        </div>

        {leaveLoading && (
          <div className="flex items-center gap-3 py-6">
            <Loader2 size={15} className="animate-spin text-[var(--brand-teal)]" />
            <span className="text-xs text-[var(--text-faint)]">Loading leave schedule…</span>
          </div>
        )}

        {!leaveLoading && leave.length === 0 && (
          <Card padding="xl" className="border-dashed text-center">
            <CalendarDays size={22} className="mx-auto text-[var(--text-faint)] mb-2" />
            <p className="text-sm text-[var(--text)] mb-1">No leave scheduled</p>
            <p className="text-xs text-[var(--text-faint)]">Add leave to show how it affects team capacity.</p>
          </Card>
        )}

        {!leaveLoading && leave.length > 0 && (
          <Card padding="none" className="overflow-hidden">
            <table className="w-full">
              <thead>
                <tr className="bg-[var(--bg-surface2)]/40 border-b border-[var(--border)]">
                  {['Member', 'Kind', 'Dates', 'Status', 'Note'].map(h => (
                    <th key={h} className="text-left px-4 py-2.5 text-[10px] font-semibold text-[var(--text-faint)] uppercase tracking-widest">{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {leave.map((entry, i) => <LeaveRow key={entry.id ?? i} entry={entry} nameFor={nameFor} />)}
              </tbody>
            </table>
          </Card>
        )}
      </section>
    </div>
  )
}
