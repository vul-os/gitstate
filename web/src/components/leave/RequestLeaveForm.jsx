/**
 * RequestLeaveForm — submit a leave request: type, date range, optional
 * half-day (AM/PM), and a note. Owners/admins can also pick a member.
 */
import { useState } from 'react'
import { Loader2, Plus, X, AlertCircle, Sun, Moon } from 'lucide-react'
import { Card, Button } from '../ui/index.js'

const inputCls =
  'bg-[var(--bg)] text-sm text-[var(--text)] rounded-[var(--radius-btn)] px-3 py-2 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/50 focus:ring-2 focus:ring-[var(--brand-teal)]/15 transition-all w-full'
const labelCls = 'text-[10px] text-[var(--text-faint)] uppercase tracking-widest'

export function RequestLeaveForm({ types = [], members = [], canPickMember = false, onSubmit }) {
  const [open, setOpen] = useState(false)
  const [userId, setUserId] = useState('')
  const [leaveTypeId, setLeaveTypeId] = useState(types[0]?.id ?? '')
  const [startDate, setStartDate] = useState('')
  const [endDate, setEndDate] = useState('')
  const [halfDay, setHalfDay] = useState(false)
  const [portion, setPortion] = useState('am')
  const [note, setNote] = useState('')
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState(null)

  const activeType = types.find(t => t.id === leaveTypeId)

  function reset() {
    setUserId(''); setLeaveTypeId(types[0]?.id ?? '')
    setStartDate(''); setEndDate(''); setHalfDay(false); setPortion('am'); setNote('')
  }

  async function handleSubmit(e) {
    e.preventDefault()
    if (!leaveTypeId || !startDate) return
    setSaving(true)
    setErr(null)
    try {
      const payload = {
        leaveTypeId,
        startDate,
        endDate: halfDay ? startDate : (endDate || startDate),
        halfDay,
        portion: halfDay ? portion : 'full',
        note,
      }
      if (canPickMember && userId) payload.userId = userId
      await onSubmit(payload)
      reset()
      setOpen(false)
    } catch (e2) {
      setErr(e2.message ?? 'Failed to submit request')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div>
      <Button
        variant={open ? 'outline' : 'primary'}
        size="sm"
        onClick={() => setOpen(v => !v)}
        leftIcon={open ? <X size={13} /> : <Plus size={13} strokeWidth={2.5} />}
      >
        {open ? 'Cancel' : 'Request leave'}
      </Button>

      {open && (
        <form onSubmit={handleSubmit} className="mt-4">
          <Card padding="md" className="flex flex-col gap-4">
            <div className="grid sm:grid-cols-2 gap-4">
              {canPickMember && (
                <div className="flex flex-col gap-1.5 sm:col-span-2">
                  <label className={labelCls}>Member</label>
                  {members.length > 0 ? (
                    <select className={inputCls + ' cursor-pointer'} value={userId} onChange={e => setUserId(e.target.value)}>
                      <option value="">Myself</option>
                      {members.map(m => (
                        <option key={m.userId} value={m.userId}>{m.name ?? m.email ?? m.userId}</option>
                      ))}
                    </select>
                  ) : (
                    <input className={inputCls} placeholder="user id (optional)" value={userId} onChange={e => setUserId(e.target.value)} />
                  )}
                </div>
              )}

              <div className="flex flex-col gap-1.5 sm:col-span-2">
                <label className={labelCls}>Leave type</label>
                <div className="flex flex-wrap gap-2">
                  {types.map(t => (
                    <button
                      key={t.id}
                      type="button"
                      onClick={() => setLeaveTypeId(t.id)}
                      className={[
                        'inline-flex items-center gap-1.5 px-3 py-1.5 rounded-[var(--radius-badge)] text-xs font-medium border transition-all',
                        leaveTypeId === t.id
                          ? 'border-transparent text-[#0B1120]'
                          : 'border-[var(--border)] text-[var(--text-muted)] hover:text-[var(--text)]',
                      ].join(' ')}
                      style={leaveTypeId === t.id ? { background: t.color } : undefined}
                    >
                      <span className="w-2 h-2 rounded-full" style={{ background: leaveTypeId === t.id ? '#0B1120' : t.color }} />
                      {t.name}
                    </button>
                  ))}
                  {types.length === 0 && <span className="text-xs text-[var(--text-faint)]">No leave types configured yet.</span>}
                </div>
              </div>

              <div className="flex flex-col gap-1.5">
                <label className={labelCls}>{halfDay ? 'Date' : 'Start date'}</label>
                <input type="date" required className={inputCls} value={startDate} onChange={e => setStartDate(e.target.value)} />
              </div>
              {!halfDay && (
                <div className="flex flex-col gap-1.5">
                  <label className={labelCls}>End date</label>
                  <input type="date" className={inputCls} value={endDate} min={startDate} onChange={e => setEndDate(e.target.value)} />
                </div>
              )}

              {/* Half-day toggle */}
              <div className="flex flex-col gap-1.5 sm:col-span-2">
                <label className={labelCls}>Duration</label>
                <div className="flex items-center gap-2 flex-wrap">
                  <button
                    type="button"
                    onClick={() => setHalfDay(false)}
                    className={[
                      'px-3 py-1.5 rounded-[var(--radius-badge)] text-xs font-medium border transition-all',
                      !halfDay ? 'border-[var(--brand-teal)]/40 bg-[var(--brand-teal)]/10 text-[var(--brand-teal)]' : 'border-[var(--border)] text-[var(--text-muted)] hover:text-[var(--text)]',
                    ].join(' ')}
                  >
                    Full day(s)
                  </button>
                  <button
                    type="button"
                    onClick={() => setHalfDay(true)}
                    className={[
                      'px-3 py-1.5 rounded-[var(--radius-badge)] text-xs font-medium border transition-all',
                      halfDay ? 'border-[var(--brand-teal)]/40 bg-[var(--brand-teal)]/10 text-[var(--brand-teal)]' : 'border-[var(--border)] text-[var(--text-muted)] hover:text-[var(--text)]',
                    ].join(' ')}
                  >
                    Half day
                  </button>
                  {halfDay && (
                    <div className="flex items-center rounded-[var(--radius-btn)] p-0.5 gap-0.5 bg-[var(--bg)] border border-[var(--border)]">
                      <button
                        type="button"
                        onClick={() => setPortion('am')}
                        className={['flex items-center gap-1 px-2.5 py-1 rounded-[6px] text-xs transition-all', portion === 'am' ? 'bg-[var(--bg-surface2)] text-[var(--brand-teal)]' : 'text-[var(--text-faint)]'].join(' ')}
                      >
                        <Sun size={12} /> AM
                      </button>
                      <button
                        type="button"
                        onClick={() => setPortion('pm')}
                        className={['flex items-center gap-1 px-2.5 py-1 rounded-[6px] text-xs transition-all', portion === 'pm' ? 'bg-[var(--bg-surface2)] text-[var(--brand-teal)]' : 'text-[var(--text-faint)]'].join(' ')}
                      >
                        <Moon size={12} /> PM
                      </button>
                    </div>
                  )}
                </div>
              </div>

              <div className="flex flex-col gap-1.5 sm:col-span-2">
                <label className={labelCls}>Note (optional)</label>
                <input className={inputCls} placeholder="e.g. Family trip" value={note} onChange={e => setNote(e.target.value)} />
              </div>
            </div>

            {activeType && !activeType.requiresApproval && (
              <p className="text-xs text-[var(--text-faint)]">This leave type is auto-approved.</p>
            )}
            {err && (
              <p className="flex items-center gap-2 text-xs text-red-400"><AlertCircle size={13} /> {err}</p>
            )}
            <Button
              type="submit"
              disabled={saving || !leaveTypeId || !startDate}
              className="self-start"
              leftIcon={saving ? <Loader2 size={12} className="animate-spin" /> : null}
            >
              Submit request
            </Button>
          </Card>
        </form>
      )}
    </div>
  )
}
