/**
 * EquityLedger — the advisory equity ledger section.
 *
 * A table of members with the contribution-weighted SUGGESTED % (from the engine)
 * vs an admin-editable ACTUAL %, the pool label, and a clear caveat that the model
 * INFORMS, never decides. A donut of the suggested split sits alongside.
 *
 * All SVG hand-rolled. Both themes, reduced-motion safe.
 */
import { useState } from 'react'
import { Card, Badge } from '../ui/index.js'
import { Avatar } from './parts.jsx'
import { hueFromStr } from './helpers.js'
import { saveEquity } from '../../lib/useContribution.js'
import { Scale, Info, Check, Pencil, X } from 'lucide-react'

const fmtPct = (v) => (v == null ? '—' : `${Number(v).toFixed(1)}%`)

// ── donut of the suggested split ───────────────────────────────────────────

function SuggestedDonut({ rows, size = 160 }) {
  const slices = rows.filter((r) => (r.suggestedPct ?? 0) > 0)
  const total = slices.reduce((s, r) => s + r.suggestedPct, 0)
  if (total <= 0) {
    return (
      <div className="flex items-center justify-center text-xs text-[var(--text-faint)]" style={{ width: size, height: size }}>
        No suggested split yet
      </div>
    )
  }
  const r = size / 2 - 8
  const cx = size / 2
  const cy = size / 2
  const circ = 2 * Math.PI * r
  // Precompute each slice's dash length + cumulative offset purely (no mutation):
  // offset = sum of all earlier slices' fractions × circumference.
  const segs = slices.map((row, i) => {
    const dash = (row.suggestedPct / total) * circ
    const offset = slices.slice(0, i).reduce((acc, s) => acc + (s.suggestedPct / total) * circ, 0)
    return { row, i, dash, offset, col: `hsl(${hueFromStr(row.name || row.userId || String(i))} 70% 56%)` }
  })
  return (
    <div className="relative shrink-0" style={{ width: size, height: size }}>
      <svg width={size} height={size} className="rotate-[-90deg]" aria-hidden>
        <circle cx={cx} cy={cy} r={r} fill="none" stroke="var(--bg-surface3)" strokeWidth="14" />
        {segs.map(({ row, dash, offset, col }) => (
          <circle
            key={row.userId}
            cx={cx} cy={cy} r={r} fill="none"
            stroke={col} strokeWidth="14"
            strokeDasharray={`${dash} ${circ - dash}`}
            strokeDashoffset={-offset}
            style={{ transition: 'stroke-dasharray 0.5s cubic-bezier(0.22,1,0.36,1)' }}
          />
        ))}
      </svg>
      <div className="absolute inset-0 flex flex-col items-center justify-center">
        <span className="text-[10px] font-mono uppercase tracking-wide text-[var(--text-faint)]">Suggested</span>
        <span className="font-display text-lg font-semibold text-[var(--text)] tabular-nums">{slices.length}</span>
        <span className="text-[10px] text-[var(--text-faint)]">members</span>
      </div>
    </div>
  )
}

// ── editable actual cell ───────────────────────────────────────────────────

function ActualCell({ row, period, canEdit, onSaved }) {
  const [editing, setEditing] = useState(false)
  const [val, setVal] = useState(row.actualPct == null ? '' : String(row.actualPct))
  const [label, setLabel] = useState(row.poolLabel || 'Contribution pool')
  const [note, setNote] = useState(row.note || '')
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState(null)

  async function save() {
    setSaving(true); setErr(null)
    try {
      const actualPct = val.trim() === '' ? null : Number(val)
      if (actualPct != null && (Number.isNaN(actualPct) || actualPct < 0 || actualPct > 100)) {
        throw new Error('0–100 only')
      }
      await saveEquity({ userId: row.userId, period, actualPct, poolLabel: label, note })
      setEditing(false)
      onSaved?.()
    } catch (e) {
      setErr(e.message ?? 'Could not save')
    } finally {
      setSaving(false)
    }
  }

  if (!editing) {
    return (
      <div className="flex items-center justify-end gap-2">
        <span className="font-mono tabular-nums text-[13px] text-[var(--text)]">{fmtPct(row.actualPct)}</span>
        {canEdit && (
          <button
            onClick={() => setEditing(true)}
            className="p-1 rounded text-[var(--text-faint)] hover:text-[var(--brand-teal)] hover:bg-[var(--bg-surface2)] transition-colors cursor-pointer"
            aria-label="Edit actual allocation"
          >
            <Pencil size={12} />
          </button>
        )}
      </div>
    )
  }

  return (
    <div className="flex flex-col items-end gap-1.5 min-w-[200px]">
      <div className="flex items-center gap-1.5">
        <input
          type="number" min="0" max="100" step="0.1" autoFocus
          value={val} onChange={(e) => setVal(e.target.value)}
          placeholder="—"
          className="w-16 bg-[var(--bg)] text-right text-[13px] font-mono rounded-[var(--radius-btn)] px-2 py-1 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/50"
        />
        <span className="text-[var(--text-faint)] text-xs">%</span>
        <button onClick={save} disabled={saving}
          className="p-1 rounded text-[var(--brand-teal)] hover:bg-[var(--brand-teal)]/10 transition-colors cursor-pointer disabled:opacity-50" aria-label="Save">
          <Check size={14} />
        </button>
        <button onClick={() => { setEditing(false); setErr(null) }}
          className="p-1 rounded text-[var(--text-faint)] hover:bg-[var(--bg-surface2)] transition-colors cursor-pointer" aria-label="Cancel">
          <X size={14} />
        </button>
      </div>
      <input
        value={label} onChange={(e) => setLabel(e.target.value)} placeholder="Pool label"
        className="w-full bg-[var(--bg)] text-right text-[11px] rounded-[var(--radius-btn)] px-2 py-1 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/50 text-[var(--text-dim)]"
      />
      <input
        value={note} onChange={(e) => setNote(e.target.value)} placeholder="Note (optional)"
        className="w-full bg-[var(--bg)] text-right text-[11px] rounded-[var(--radius-btn)] px-2 py-1 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/50 text-[var(--text-dim)]"
      />
      {err && <span className="text-[10px] text-red-400">{err}</span>}
    </div>
  )
}

// ── ledger ──────────────────────────────────────────────────────────────────

export function EquityLedger({ data, loading, error, period, canEdit, onRefetch }) {
  const rows = data?.rows ?? []
  const totalActual = rows.reduce((s, r) => s + (r.actualPct ?? 0), 0)

  return (
    <div className="space-y-4">
      {/* caveat */}
      <div className="relative overflow-hidden rounded-[var(--radius-card)] border border-[var(--brand-indigo)]/25 bg-[var(--brand-indigo)]/[0.05]">
        <div className="relative flex items-start gap-3 p-4">
          <Scale size={18} className="text-[var(--brand-indigo)] mt-0.5 shrink-0" />
          <p className="text-[13px] text-[var(--text-dim)] leading-relaxed">
            <span className="font-semibold text-[var(--text)]">This ledger is advisory.</span>{' '}
            The <em>suggested %</em> is each member’s contribution-weighted share of the pool
            (their composite ÷ the team’s total). It exists to <span className="font-medium text-[var(--text-dim)]">inform</span> an
            allocation conversation — the <em>actual %</em> a human records is what really happens.
            Automated agents are excluded from the pool.
          </p>
        </div>
      </div>

      <Card padding="none" className="overflow-hidden">
        {error ? (
          <div className="p-6 text-sm text-red-400">{error}</div>
        ) : loading && !data ? (
          <div className="p-6 space-y-3 animate-pulse">
            {[0, 1, 2, 3].map((i) => <div key={i} className="h-8 rounded bg-[var(--bg-surface3)]" />)}
          </div>
        ) : rows.length === 0 ? (
          <div className="p-8 text-center text-sm text-[var(--text-faint)]">
            No contributors in this period — the suggested split is derived from contribution.
          </div>
        ) : (
          <div className="flex flex-col lg:flex-row">
            {/* table */}
            <div className="flex-1 min-w-0 overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="text-[10px] font-mono uppercase tracking-wide text-[var(--text-faint)] border-b border-[var(--border)]">
                    <th className="text-left font-medium px-4 py-2.5">Member</th>
                    <th className="text-right font-medium px-3 py-2.5">Composite</th>
                    <th className="text-right font-medium px-3 py-2.5">Suggested</th>
                    <th className="text-right font-medium px-4 py-2.5">Actual</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((row) => (
                    <tr key={row.userId} className="border-b border-[var(--border)]/60 hover:bg-[var(--bg-surface2)]/40 transition-colors">
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-2.5 min-w-0">
                          <Avatar name={row.name || row.email} size={28} />
                          <div className="min-w-0">
                            <p className="text-[13px] font-medium text-[var(--text)] truncate">{row.name || row.email}</p>
                            <p className="text-[10px] font-mono text-[var(--text-faint)] truncate">{row.poolLabel}{row.note ? ` · ${row.note}` : ''}</p>
                          </div>
                        </div>
                      </td>
                      <td className="px-3 py-3 text-right font-mono tabular-nums text-[13px] text-[var(--text-dim)]">
                        {Math.round(row.composite)}
                      </td>
                      <td className="px-3 py-3 text-right">
                        <div className="inline-flex items-center gap-2 justify-end">
                          <div className="hidden sm:block h-1.5 w-14 rounded-full bg-[var(--bg-surface3)] overflow-hidden">
                            <div className="h-full rounded-full" style={{ width: `${Math.min(100, row.suggestedPct)}%`, background: 'linear-gradient(90deg,var(--brand-teal),var(--brand-indigo))' }} />
                          </div>
                          <span className="font-mono tabular-nums text-[13px] text-[var(--brand-teal)] w-12 text-right">{fmtPct(row.suggestedPct)}</span>
                        </div>
                      </td>
                      <td className="px-4 py-3 text-right">
                        <ActualCell row={row} period={period} canEdit={canEdit} onSaved={onRefetch} />
                      </td>
                    </tr>
                  ))}
                </tbody>
                <tfoot>
                  <tr className="text-[11px] font-mono text-[var(--text-faint)]">
                    <td className="px-4 py-2.5">Total</td>
                    <td />
                    <td className="px-3 py-2.5 text-right text-[var(--text-dim)]">100.0%</td>
                    <td className="px-4 py-2.5 text-right text-[var(--text-dim)]">{totalActual > 0 ? `${totalActual.toFixed(1)}%` : '—'}</td>
                  </tr>
                </tfoot>
              </table>
            </div>

            {/* donut */}
            <div className="flex items-center justify-center p-6 border-t lg:border-t-0 lg:border-l border-[var(--border)] shrink-0">
              <SuggestedDonut rows={rows} />
            </div>
          </div>
        )}
      </Card>

      <div className="flex items-start gap-2 px-1">
        <Info size={13} className="text-[var(--text-faint)] mt-0.5 shrink-0" />
        <p className="text-[11px] text-[var(--text-faint)] leading-relaxed">
          {canEdit
            ? 'Click the pencil to record an actual grant, pool label and note. The suggested split recomputes from contribution; the actual is yours to set.'
            : 'Only owners and admins can record actual allocations. The suggested split is computed from each member’s contribution.'}
          {data?.advisory && <Badge color="indigo" className="ml-2 align-middle">Advisory</Badge>}
        </p>
      </div>
    </div>
  )
}
