/**
 * InvoiceEditor — the comprehensive client-invoice editor.
 *
 * Mixes git-derived lines (carry evidence, a "git" badge, an expandable
 * evidence affordance) with free-form manual lines (editable description / qty /
 * unit rate → amount auto-computed). Inline-edit qty & rate; delete any line;
 * add manual lines. A totals panel handles discount, tax (rate or amount) and a
 * notes textarea — all persisted via PATCH /api/invoices/{id}.
 *
 * Currency-aware, both themes, lucide icons, the gitstate aesthetic.
 */
import { useState, useEffect, useMemo, useCallback, useRef } from 'react'
import {
  Plus, Trash2, ChevronDown, GitMerge, Save, Check, Percent, Tag,
  StickyNote, Pencil, RotateCcw,
} from 'lucide-react'
import { useCurrency } from '../../lib/currency.jsx'
import { patchInvoice } from '../../lib/useInvoices.js'
import { GitBadge, EvidenceList, Spinner } from './shared.jsx'

let manualSeq = 0
function newManualLine() {
  return {
    id: `new-${++manualSeq}`,
    _new: true,
    source: 'manual',
    description: '',
    quantity: 1,
    unitRateCents: 0,
    amountCents: 0,
    evidence: [],
  }
}

/** Normalise an incoming line into the editor's working shape. */
function toEditable(line, i) {
  const source = line.source ?? (line.evidence?.length ? 'git' : 'manual')
  const quantity = line.quantity ?? line.effortPoints ?? 1
  const unitRateCents = line.unitRateCents ?? 0
  const amountCents = line.amountCents ?? Math.round((quantity || 0) * (unitRateCents || 0))
  return {
    id: line.id ?? `line-${i}`,
    source,
    description: line.description ?? '',
    quantity,
    unitRateCents,
    amountCents,
    evidence: line.evidence ?? [],
  }
}

function centsFromInput(v) {
  const n = Number(v)
  return Number.isFinite(n) ? Math.round(n * 100) : 0
}

// ── A single line row (git or manual) ────────────────────────────────────────────

function LineRow({ line, editing, onChange, onDelete }) {
  const { format, currency } = useCurrency()
  const [open, setOpen] = useState(false)
  const isGit = line.source === 'git'
  const evCount = line.evidence?.length ?? 0
  // Inline edits operate in *display dollars* for ergonomics; we convert to cents.
  const qtyVal = line.quantity ?? 0
  const rateDollars = (line.unitRateCents ?? 0) / 100

  function setQty(v) {
    const q = Number(v)
    const quantity = Number.isFinite(q) ? q : 0
    onChange({ ...line, quantity, amountCents: Math.round(quantity * (line.unitRateCents ?? 0)) })
  }
  function setRate(v) {
    const unitRateCents = centsFromInput(v)
    onChange({ ...line, unitRateCents, amountCents: Math.round((line.quantity ?? 0) * unitRateCents) })
  }

  return (
    <div className="rounded-[var(--radius-badge)]" style={{ background: 'var(--bg-surface2)', border: '1px solid var(--border)' }}>
      <div className="flex items-center gap-3 px-3 py-2.5">
        {/* Evidence expander (git lines only) */}
        {isGit && evCount > 0 ? (
          <button
            onClick={() => setOpen(!open)}
            aria-label={open ? 'Hide evidence' : 'Show evidence'}
            className="shrink-0 text-[var(--text-faint)] hover:text-[var(--brand-teal)] transition-colors"
          >
            <ChevronDown size={14} className={`transition-transform ${open ? 'rotate-180' : ''}`} />
          </button>
        ) : (
          <span className="w-3.5 shrink-0" />
        )}

        {/* Description + meta */}
        <div className="flex-1 min-w-0">
          {editing && !isGit ? (
            <input
              value={line.description}
              onChange={(e) => onChange({ ...line, description: e.target.value })}
              placeholder="Description"
              aria-label="Line description"
              className="w-full bg-[var(--bg)] text-[var(--text)] text-sm rounded-md px-2.5 py-1.5 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/50"
            />
          ) : (
            <div className="flex items-center gap-2 min-w-0">
              <p className="text-sm font-medium text-[var(--text)] truncate">{line.description || <span className="text-[var(--text-faint)] italic">Untitled line</span>}</p>
              {isGit ? <GitBadge /> : (
                <span className="text-[9px] font-bold uppercase tracking-wider px-1.5 py-0.5 rounded shrink-0" style={{ background: 'var(--bg)', color: 'var(--text-faint)', border: '1px solid var(--border)' }}>
                  manual
                </span>
              )}
            </div>
          )}
          {!editing && (
            <p className="text-[11px] text-[var(--text-faint)] mt-0.5 flex items-center gap-1.5">
              {isGit && <GitMerge size={11} style={{ color: 'var(--brand-teal)' }} />}
              {qtyVal} × {format(rateDollars)}
              {isGit && evCount > 0 && <span className="text-[var(--text-muted)]">· {evCount} item{evCount !== 1 ? 's' : ''}</span>}
            </p>
          )}
        </div>

        {/* Inline qty / rate editors */}
        {editing ? (
          <div className="flex items-center gap-2 shrink-0">
            <label className="sr-only" htmlFor={`qty-${line.id}`}>Quantity</label>
            <input
              id={`qty-${line.id}`}
              type="number" min="0" step="0.1" value={qtyVal}
              onChange={(e) => setQty(e.target.value)}
              className="w-16 bg-[var(--bg)] text-[var(--text)] text-sm text-right rounded-md px-2 py-1.5 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/50 tabular-nums"
            />
            <span className="text-[var(--text-faint)] text-xs">×</span>
            <div className="relative">
              <span className="absolute left-2 top-1/2 -translate-y-1/2 text-[11px] text-[var(--text-faint)] pointer-events-none">{currency.symbol ?? '$'}</span>
              <label className="sr-only" htmlFor={`rate-${line.id}`}>Unit rate</label>
              <input
                id={`rate-${line.id}`}
                type="number" min="0" step="1" value={rateDollars}
                onChange={(e) => setRate(e.target.value)}
                className="w-24 bg-[var(--bg)] text-[var(--text)] text-sm text-right rounded-md pl-5 pr-2 py-1.5 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/50 tabular-nums"
              />
            </div>
          </div>
        ) : null}

        {/* Amount */}
        <span className="text-sm font-bold text-[var(--text)] shrink-0 w-24 text-right tabular-nums">
          {format((line.amountCents ?? 0) / 100)}
        </span>

        {/* Delete */}
        {editing && (
          <button
            onClick={onDelete}
            aria-label="Delete line"
            className="shrink-0 text-[var(--text-faint)] hover:text-[var(--bad)] transition-colors"
          >
            <Trash2 size={15} />
          </button>
        )}
      </div>

      {open && isGit && (
        <div className="px-3 pb-3 pl-10">
          <EvidenceList evidence={line.evidence} />
        </div>
      )}
    </div>
  )
}

// ── Totals panel (subtotal · discount · tax · total) ─────────────────────────────

function TotalsPanel({ subtotalCents, discountCents, setDiscount, taxMode, setTaxMode, taxRate, setTaxRate, taxCents, setTaxCents, computedTaxCents, totalCents }) {
  const { format, currency } = useCurrency()
  const sym = currency.symbol ?? '$'
  return (
    <div className="rounded-[var(--radius-badge)] p-4 space-y-3" style={{ background: 'var(--bg)', border: '1px solid var(--border)' }}>
      <Row label="Subtotal" value={format(subtotalCents / 100)} />

      {/* Discount */}
      <div className="flex items-center justify-between gap-3">
        <span className="text-sm text-[var(--text-muted)] flex items-center gap-1.5"><Tag size={13} style={{ color: 'var(--brand-indigo)' }} /> Discount</span>
        <div className="relative w-32">
          <span className="absolute left-2 top-1/2 -translate-y-1/2 text-[11px] text-[var(--text-faint)] pointer-events-none">{sym}</span>
          <input
            type="number" min="0" step="1"
            value={(discountCents ?? 0) / 100}
            onChange={(e) => setDiscount(centsFromInput(e.target.value))}
            aria-label="Discount amount"
            className="w-full bg-[var(--bg-surface)] text-[var(--text)] text-sm text-right rounded-md pl-5 pr-2 py-1.5 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/50 tabular-nums"
          />
        </div>
      </div>

      {/* Tax — rate or amount */}
      <div className="flex items-center justify-between gap-3">
        <span className="text-sm text-[var(--text-muted)] flex items-center gap-1.5"><Percent size={13} style={{ color: 'var(--brand-teal)' }} /> Tax</span>
        <div className="flex items-center gap-1.5">
          <div className="inline-flex rounded-md overflow-hidden border border-[var(--border)] shrink-0" role="group" aria-label="Tax mode">
            <button
              onClick={() => setTaxMode('rate')}
              className="px-2 py-1 text-[10px] font-bold uppercase tracking-wide transition-colors"
              style={taxMode === 'rate' ? { background: 'var(--brand-teal)', color: '#04121a' } : { background: 'var(--bg-surface)', color: 'var(--text-faint)' }}
            >%</button>
            <button
              onClick={() => setTaxMode('amount')}
              className="px-2 py-1 text-[10px] font-bold uppercase tracking-wide transition-colors"
              style={taxMode === 'amount' ? { background: 'var(--brand-teal)', color: '#04121a' } : { background: 'var(--bg-surface)', color: 'var(--text-faint)' }}
            >{sym}</button>
          </div>
          {taxMode === 'rate' ? (
            <div className="relative w-24">
              <input
                type="number" min="0" step="0.1" value={taxRate}
                onChange={(e) => setTaxRate(e.target.value)}
                aria-label="Tax rate percent"
                className="w-full bg-[var(--bg-surface)] text-[var(--text)] text-sm text-right rounded-md pl-2 pr-5 py-1.5 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/50 tabular-nums"
              />
              <span className="absolute right-2 top-1/2 -translate-y-1/2 text-[11px] text-[var(--text-faint)] pointer-events-none">%</span>
            </div>
          ) : (
            <div className="relative w-24">
              <span className="absolute left-2 top-1/2 -translate-y-1/2 text-[11px] text-[var(--text-faint)] pointer-events-none">{sym}</span>
              <input
                type="number" min="0" step="1" value={(taxCents ?? 0) / 100}
                onChange={(e) => setTaxCents(centsFromInput(e.target.value))}
                aria-label="Tax amount"
                className="w-full bg-[var(--bg-surface)] text-[var(--text)] text-sm text-right rounded-md pl-5 pr-2 py-1.5 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/50 tabular-nums"
              />
            </div>
          )}
        </div>
      </div>
      {taxMode === 'rate' && (
        <div className="flex items-center justify-end -mt-1">
          <span className="text-[11px] text-[var(--text-faint)]">= {format(computedTaxCents / 100)}</span>
        </div>
      )}

      <div className="flex items-center justify-between pt-3 border-t border-[var(--border)]">
        <span className="text-sm font-semibold text-[var(--text)]">Total</span>
        <span className="text-xl font-bold gradient-text tabular-nums">{format(totalCents / 100)}</span>
      </div>
    </div>
  )
}

function Row({ label, value }) {
  return (
    <div className="flex items-center justify-between">
      <span className="text-sm text-[var(--text-muted)]">{label}</span>
      <span className="text-sm font-semibold text-[var(--text)] tabular-nums">{value}</span>
    </div>
  )
}

// ── Editor root ──────────────────────────────────────────────────────────────────

export default function InvoiceEditor({ invoice, onSaved }) {
  const editable = invoice.status === 'draft'
  const [editing, setEditing] = useState(false)

  // Working copy
  const [lines, setLines] = useState(() => (invoice.lines ?? []).map(toEditable))
  const [discountCents, setDiscountCents] = useState(invoice.discountCents ?? 0)
  const [notes, setNotes] = useState(invoice.notes ?? '')

  // Tax: backend may give taxRate (fraction or %) or a flat taxCents. Prefer rate
  // when present, else derive amount mode.
  const initialRate = invoice.taxRate != null
    ? (invoice.taxRate <= 1 ? invoice.taxRate * 100 : invoice.taxRate)
    : null
  const [taxMode, setTaxMode] = useState(initialRate != null ? 'rate' : 'amount')
  const [taxRate, setTaxRate] = useState(initialRate != null ? String(initialRate) : '0')
  const [taxCents, setTaxCents] = useState(invoice.taxCents ?? 0)

  const [busy, setBusy] = useState(false)
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState(null)

  // Re-seed when the invoice identity changes (navigating between invoices).
  const seedRef = useRef(invoice.id)
  useEffect(() => {
    if (seedRef.current === invoice.id) return
    seedRef.current = invoice.id
    setLines((invoice.lines ?? []).map(toEditable))
    setDiscountCents(invoice.discountCents ?? 0)
    setNotes(invoice.notes ?? '')
    const r = invoice.taxRate != null ? (invoice.taxRate <= 1 ? invoice.taxRate * 100 : invoice.taxRate) : null
    setTaxMode(r != null ? 'rate' : 'amount')
    setTaxRate(r != null ? String(r) : '0')
    setTaxCents(invoice.taxCents ?? 0)
    setEditing(false)
  }, [invoice])

  const { format } = useCurrency()

  const subtotalCents = useMemo(
    () => lines.reduce((s, l) => s + (l.amountCents ?? 0), 0),
    [lines],
  )
  const afterDiscount = Math.max(0, subtotalCents - (discountCents ?? 0))
  const computedTaxCents = taxMode === 'rate'
    ? Math.round(afterDiscount * (Number(taxRate) || 0) / 100)
    : (taxCents ?? 0)
  const totalCents = afterDiscount + computedTaxCents

  const updateLine = useCallback((idx, next) => {
    setLines((ls) => ls.map((l, i) => (i === idx ? next : l)))
  }, [])
  const deleteLine = useCallback((idx) => {
    setLines((ls) => ls.filter((_, i) => i !== idx))
  }, [])
  const addManual = useCallback(() => {
    setLines((ls) => [...ls, newManualLine()])
    setEditing(true)
  }, [])

  async function save() {
    setBusy(true); setError(null); setSaved(false)
    // Strip the editor-only `_new` flag; send line essentials.
    const payloadLines = lines.map((l) => ({
      id: l._new ? undefined : l.id,
      source: l.source,
      description: l.description,
      quantity: l.quantity,
      unitRateCents: l.unitRateCents,
      amountCents: l.amountCents,
    }))
    const body = {
      lines: payloadLines,
      discountCents: discountCents ?? 0,
      notes,
    }
    if (taxMode === 'rate') body.taxRate = (Number(taxRate) || 0) / 100
    else body.taxCents = taxCents ?? 0

    try {
      await patchInvoice(invoice.id, body)
      setSaved(true)
      setEditing(false)
      setTimeout(() => setSaved(false), 2200)
      onSaved?.()
    } catch (e) {
      setError(e.message ?? 'Could not save invoice')
    } finally {
      setBusy(false)
    }
  }

  function resetEdits() {
    setLines((invoice.lines ?? []).map(toEditable))
    setDiscountCents(invoice.discountCents ?? 0)
    setNotes(invoice.notes ?? '')
    const r = invoice.taxRate != null ? (invoice.taxRate <= 1 ? invoice.taxRate * 100 : invoice.taxRate) : null
    setTaxMode(r != null ? 'rate' : 'amount')
    setTaxRate(r != null ? String(r) : '0')
    setTaxCents(invoice.taxCents ?? 0)
    setEditing(false)
    setError(null)
  }

  return (
    <div className="rounded-[var(--radius-card)] p-6" style={{ background: 'var(--bg-surface)', border: '1px solid var(--border)' }}>
      {/* Header row */}
      <div className="flex items-center justify-between gap-3 mb-4 flex-wrap">
        <div className="flex items-center gap-2">
          <h3 className="text-sm font-semibold text-[var(--text)]">Line items</h3>
          <span className="text-[11px] text-[var(--text-faint)] font-mono">{lines.length} line{lines.length !== 1 ? 's' : ''}</span>
        </div>
        {editable && (
          <div className="flex items-center gap-2">
            {editing && (
              <button
                onClick={resetEdits}
                disabled={busy}
                className="px-2.5 py-1.5 rounded-[var(--radius-btn)] text-xs font-semibold text-[var(--text-muted)] border border-[var(--border2)] hover:text-[var(--text)] transition-colors flex items-center gap-1.5 disabled:opacity-50"
              >
                <RotateCcw size={13} /> Reset
              </button>
            )}
            {!editing ? (
              <button
                onClick={() => setEditing(true)}
                className="px-2.5 py-1.5 rounded-[var(--radius-btn)] text-xs font-semibold text-[var(--text)] border border-[var(--border2)] hover:border-[var(--brand-teal)]/50 transition-colors flex items-center gap-1.5"
              >
                <Pencil size={13} /> Edit
              </button>
            ) : (
              <button
                onClick={save}
                disabled={busy}
                className="px-3 py-1.5 rounded-[var(--radius-btn)] text-xs font-bold text-[#04121a] transition-all disabled:opacity-50 flex items-center gap-1.5"
                style={{ background: 'linear-gradient(135deg, var(--brand-teal), var(--brand-indigo))' }}
              >
                {busy ? <Spinner size={13} /> : saved ? <Check size={13} /> : <Save size={13} />}
                {saved ? 'Saved' : 'Save changes'}
              </button>
            )}
          </div>
        )}
      </div>

      {/* Lines */}
      {lines.length === 0 ? (
        <p className="text-xs text-[var(--text-faint)] text-center py-6">No line items yet. {editable ? 'Add one below or generate from git.' : ''}</p>
      ) : (
        <div className="space-y-2">
          {lines.map((l, i) => (
            <LineRow
              key={l.id}
              line={l}
              editing={editing}
              onChange={(next) => updateLine(i, next)}
              onDelete={() => deleteLine(i)}
            />
          ))}
        </div>
      )}

      {/* Add manual line */}
      {editable && (
        <button
          onClick={addManual}
          className="mt-3 w-full py-2.5 rounded-[var(--radius-badge)] text-xs font-semibold text-[var(--text-muted)] border border-dashed border-[var(--border2)] hover:border-[var(--brand-teal)]/50 hover:text-[var(--brand-teal)] transition-colors flex items-center justify-center gap-1.5"
        >
          <Plus size={14} /> Add line
        </button>
      )}

      {/* Totals + notes */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 mt-5">
        <div>
          <label className="block text-[10px] font-semibold text-[var(--text-faint)] uppercase tracking-widest mb-1.5 flex items-center gap-1.5">
            <StickyNote size={11} /> Notes
          </label>
          {editable && editing ? (
            <textarea
              value={notes}
              onChange={(e) => setNotes(e.target.value)}
              rows={6}
              placeholder="Payment terms, thank-you note, PO number…"
              className="w-full h-full min-h-[8rem] bg-[var(--bg)] text-[var(--text)] text-sm rounded-[var(--radius-badge)] px-3 py-2.5 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/50 resize-none"
            />
          ) : (
            <div className="rounded-[var(--radius-badge)] px-3 py-2.5 text-sm text-[var(--text-dim)] whitespace-pre-wrap min-h-[8rem]" style={{ background: 'var(--bg)', border: '1px solid var(--border)' }}>
              {notes ? notes : <span className="text-[var(--text-faint)] italic">No notes.</span>}
            </div>
          )}
        </div>

        {editable && editing ? (
          <TotalsPanel
            subtotalCents={subtotalCents}
            discountCents={discountCents} setDiscount={setDiscountCents}
            taxMode={taxMode} setTaxMode={setTaxMode}
            taxRate={taxRate} setTaxRate={setTaxRate}
            taxCents={taxCents} setTaxCents={setTaxCents}
            computedTaxCents={computedTaxCents}
            totalCents={totalCents}
          />
        ) : (
          <div className="rounded-[var(--radius-badge)] p-4 space-y-2.5" style={{ background: 'var(--bg)', border: '1px solid var(--border)' }}>
            <Row label="Subtotal" value={format(subtotalCents / 100)} />
            {(discountCents ?? 0) > 0 && <Row label="Discount" value={`− ${format((discountCents ?? 0) / 100)}`} />}
            {computedTaxCents > 0 && <Row label={taxMode === 'rate' ? `Tax (${taxRate}%)` : 'Tax'} value={format(computedTaxCents / 100)} />}
            <div className="flex items-center justify-between pt-2.5 border-t border-[var(--border)]">
              <span className="text-sm font-semibold text-[var(--text)]">Total</span>
              <span className="text-xl font-bold gradient-text tabular-nums">{format(totalCents / 100)}</span>
            </div>
          </div>
        )}
      </div>

      {error && <p className="text-xs mt-3" style={{ color: 'var(--bad)' }}>{error}</p>}
    </div>
  )
}
