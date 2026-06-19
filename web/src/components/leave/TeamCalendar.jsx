/**
 * TeamCalendar — a month grid showing who's off when, coloured by leave type.
 * Approved leave is solid; pending leave is shown with reduced opacity.
 * Prev/next month navigation; weekends dimmed.
 */
import { useMemo, useState } from 'react'
import { ChevronLeft, ChevronRight, CalendarDays } from 'lucide-react'
import { Card } from '../ui/index.js'
import { parseDate, typeIndex, initials } from './leaveUtils.js'

const WEEKDAYS = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun']

/** ISO day-of-week index (Mon=0 … Sun=6). */
function isoDow(date) {
  return (date.getDay() + 6) % 7
}

function ymd(date) {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

export function TeamCalendar({ leave = [], types = [], nameFor }) {
  const [cursor, setCursor] = useState(() => {
    const d = new Date()
    return new Date(d.getFullYear(), d.getMonth(), 1)
  })
  const tIdx = useMemo(() => typeIndex(types), [types])

  const year = cursor.getFullYear()
  const month = cursor.getMonth()

  // Map "YYYY-MM-DD" → [{entry, type}] for fast per-cell lookup.
  const byDay = useMemo(() => {
    const map = {}
    for (const e of leave) {
      const start = parseDate(e.startDate)
      const end = parseDate(e.endDate)
      if (!start || !end) continue
      for (let d = new Date(start); d <= end; d.setDate(d.getDate() + 1)) {
        const key = ymd(d)
        ;(map[key] ??= []).push(e)
      }
    }
    return map
  }, [leave])

  const cells = useMemo(() => {
    const first = new Date(year, month, 1)
    const lead = isoDow(first)
    const daysInMonth = new Date(year, month + 1, 0).getDate()
    const out = []
    for (let i = 0; i < lead; i++) out.push(null)
    for (let day = 1; day <= daysInMonth; day++) out.push(new Date(year, month, day))
    while (out.length % 7 !== 0) out.push(null)
    return out
  }, [year, month])

  const monthLabel = cursor.toLocaleDateString(undefined, { month: 'long', year: 'numeric' })
  const todayKey = ymd(new Date())

  return (
    <Card padding="md" className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold text-[var(--text)] flex items-center gap-2">
          <CalendarDays size={14} className="text-[var(--brand-teal)]" /> {monthLabel}
        </h3>
        <div className="flex items-center gap-1">
          <button
            onClick={() => setCursor(new Date(year, month - 1, 1))}
            className="p-1.5 rounded-[var(--radius-badge)] text-[var(--text-faint)] hover:text-[var(--text)] hover:bg-[var(--bg-surface2)] transition-colors"
            aria-label="Previous month"
          >
            <ChevronLeft size={16} />
          </button>
          <button
            onClick={() => setCursor(new Date(new Date().getFullYear(), new Date().getMonth(), 1))}
            className="px-2 py-1 rounded-[var(--radius-badge)] text-[11px] text-[var(--text-faint)] hover:text-[var(--text)] hover:bg-[var(--bg-surface2)] transition-colors"
          >
            Today
          </button>
          <button
            onClick={() => setCursor(new Date(year, month + 1, 1))}
            className="p-1.5 rounded-[var(--radius-badge)] text-[var(--text-faint)] hover:text-[var(--text)] hover:bg-[var(--bg-surface2)] transition-colors"
            aria-label="Next month"
          >
            <ChevronRight size={16} />
          </button>
        </div>
      </div>

      <div className="grid grid-cols-7 gap-1">
        {WEEKDAYS.map((d, i) => (
          <div key={d} className={['text-[10px] font-mono uppercase tracking-wide text-center pb-1', i >= 5 ? 'text-[var(--text-faint)]/60' : 'text-[var(--text-faint)]'].join(' ')}>
            {d}
          </div>
        ))}
        {cells.map((date, i) => {
          if (!date) return <div key={`e${i}`} className="aspect-square rounded-[var(--radius-badge)]" />
          const key = ymd(date)
          const entries = byDay[key] ?? []
          const weekend = isoDow(date) >= 5
          const isToday = key === todayKey
          return (
            <div
              key={key}
              className={[
                'aspect-square rounded-[var(--radius-badge)] p-1 flex flex-col border transition-colors',
                weekend ? 'bg-[var(--bg)]/40 border-transparent' : 'bg-[var(--bg)] border-[var(--border)]',
                isToday ? 'ring-1 ring-[var(--brand-teal)]/50' : '',
              ].join(' ')}
            >
              <span className={['text-[10px] font-mono tabular-nums leading-none', isToday ? 'text-[var(--brand-teal)] font-bold' : weekend ? 'text-[var(--text-faint)]/60' : 'text-[var(--text-faint)]'].join(' ')}>
                {date.getDate()}
              </span>
              <div className="flex flex-wrap gap-0.5 mt-auto justify-start content-end overflow-hidden">
                {entries.slice(0, 4).map((e, j) => {
                  const t = tIdx[e.leaveTypeId]
                  const color = t?.color ?? 'var(--text-faint)'
                  const display = nameFor?.(e.userId) ?? e.userId
                  return (
                    <span
                      key={(e.id ?? '') + j}
                      title={`${display} · ${t?.name ?? e.kind ?? 'leave'}${e.halfDay ? ` (½ ${e.portion})` : ''}${e.status === 'pending' ? ' · pending' : ''}`}
                      className="w-[7px] h-[7px] rounded-full shrink-0"
                      style={{ background: color, opacity: e.status === 'pending' ? 0.4 : 1 }}
                    />
                  )
                })}
                {entries.length > 4 && (
                  <span className="text-[8px] text-[var(--text-faint)] leading-none">+{entries.length - 4}</span>
                )}
              </div>
            </div>
          )
        })}
      </div>

      {/* Legend */}
      {types.length > 0 && (
        <div className="flex flex-wrap gap-x-3 gap-y-1.5 pt-1">
          {types.map(t => (
            <span key={t.id} className="inline-flex items-center gap-1.5 text-[11px] text-[var(--text-muted)]">
              <span className="w-2 h-2 rounded-full" style={{ background: t.color }} />
              {t.name}
            </span>
          ))}
        </div>
      )}
    </Card>
  )
}

/** Initials avatar reused by the calendar tooltips / future cells. */
export function MiniAvatar({ name, email, size = 18 }) {
  return (
    <div
      className="rounded-full bg-gradient-to-br from-[var(--brand-teal)] to-[var(--brand-indigo)] flex items-center justify-center font-bold text-[#0B1120] select-none shrink-0"
      style={{ width: size, height: size, fontSize: size * 0.4 }}
    >
      {initials(name, email)}
    </div>
  )
}
