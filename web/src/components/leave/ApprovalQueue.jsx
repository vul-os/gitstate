/**
 * ApprovalQueue — pending leave requests with approve / reject actions.
 * Visible to owners/admins. Each row shows requester, type, dates, and days.
 */
import { useState } from 'react'
import { Check, X, Loader2, Inbox } from 'lucide-react'
import { Card, Badge } from '../ui/index.js'
import { typeIndex, shortDate, leaveDays } from './leaveUtils.js'
import { MiniAvatar } from './TeamCalendar.jsx'

function PendingRow({ entry, type, nameFor, onDecide }) {
  const [busy, setBusy] = useState(null) // 'approved' | 'rejected' | null
  const display = nameFor?.(entry.userId) ?? entry.userId
  const days = leaveDays(entry)

  async function decide(status) {
    setBusy(status)
    try {
      await onDecide(entry.id, status)
    } finally {
      setBusy(null)
    }
  }

  return (
    <div className="flex items-center gap-3 px-4 py-3 border-b border-[var(--border)] last:border-0">
      <MiniAvatar name={nameFor?.(entry.userId)} email={entry.userId} size={28} />
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-sm text-[var(--text)] truncate">{display}</span>
          {type && (
            <span className="inline-flex items-center gap-1 text-[11px] font-mono px-1.5 py-0.5 rounded-[var(--radius-badge)] border" style={{ borderColor: type.color + '55', color: type.color }}>
              <span className="w-1.5 h-1.5 rounded-full" style={{ background: type.color }} /> {type.name}
            </span>
          )}
          {entry.halfDay && <Badge color="default">½ {entry.portion}</Badge>}
        </div>
        <p className="text-xs text-[var(--text-faint)] font-mono mt-0.5">
          {shortDate(entry.startDate)} → {shortDate(entry.endDate)} · {days}{days === 1 ? ' day' : ' days'}
          {entry.note ? ` · ${entry.note}` : ''}
        </p>
      </div>
      <div className="flex items-center gap-1.5 shrink-0">
        <button
          onClick={() => decide('approved')}
          disabled={busy}
          className="inline-flex items-center gap-1 px-2.5 py-1.5 rounded-[var(--radius-badge)] text-xs font-medium text-green-400 bg-green-500/10 border border-green-500/25 hover:bg-green-500/20 transition-colors disabled:opacity-50"
        >
          {busy === 'approved' ? <Loader2 size={12} className="animate-spin" /> : <Check size={12} />} Approve
        </button>
        <button
          onClick={() => decide('rejected')}
          disabled={busy}
          className="inline-flex items-center gap-1 px-2.5 py-1.5 rounded-[var(--radius-badge)] text-xs font-medium text-red-400 bg-red-500/10 border border-red-500/25 hover:bg-red-500/20 transition-colors disabled:opacity-50"
        >
          {busy === 'rejected' ? <Loader2 size={12} className="animate-spin" /> : <X size={12} />} Reject
        </button>
      </div>
    </div>
  )
}

export function ApprovalQueue({ pending = [], types = [], nameFor, onDecide }) {
  const tIdx = typeIndex(types)

  if (pending.length === 0) {
    return (
      <Card padding="xl" className="border-dashed text-center">
        <Inbox size={22} className="mx-auto text-[var(--text-faint)] mb-2" />
        <p className="text-sm text-[var(--text)] mb-1">No pending requests</p>
        <p className="text-xs text-[var(--text-faint)]">Leave requests awaiting approval will appear here.</p>
      </Card>
    )
  }

  return (
    <Card padding="none" className="overflow-hidden">
      {pending.map((e, i) => (
        <PendingRow key={e.id ?? i} entry={e} type={tIdx[e.leaveTypeId]} nameFor={nameFor} onDecide={onDecide} />
      ))}
    </Card>
  )
}
