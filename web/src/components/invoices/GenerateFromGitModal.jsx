/**
 * GenerateFromGitModal — the "Generate from git" flow (contract: /from-git).
 *
 * Pick a client + date range (+ optional project scope) + rate & basis
 * (effort | hours) → POST /api/invoices/from-git → a draft invoice whose lines
 * are git-derived (each carrying evidence). The user then lands in the editor to
 * add manual lines, set discount/tax/notes, and push to accounting.
 */
import { useState, useMemo, useRef, useId } from 'react'
import { X, Sparkles, GitMerge, ChevronDown, FileText, Clock, Gauge } from 'lucide-react'
import { useCurrency } from '../../lib/currency.jsx'
import { generateInvoiceFromGit } from '../../lib/useInvoices.js'
import { useFocusTrap } from '../../lib/useFocusTrap.js'
import { EvidenceList, GitBadge, Spinner } from './shared.jsx'

function isoDaysAgo(days) {
  const d = new Date()
  d.setDate(d.getDate() - days)
  return d.toISOString().slice(0, 10)
}
function isoToday() {
  return new Date().toISOString().slice(0, 10)
}

function Field({ label, htmlFor, children }) {
  return (
    <div>
      <label htmlFor={htmlFor} className="block text-[10px] font-semibold text-[var(--text-faint)] uppercase tracking-widest mb-1.5">
        {label}
      </label>
      {children}
    </div>
  )
}

const inputCls =
  'w-full bg-[var(--bg)] text-[var(--text)] text-sm rounded-[var(--radius-btn)] px-3 py-2.5 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/50 transition-colors'

export default function GenerateFromGitModal({ clients, projects, onClose, onCreated }) {
  const { format } = useCurrency()
  const dialogRef = useRef(null)
  const uid = useId().replace(/:/g, '')
  useFocusTrap(dialogRef, true, onClose)

  const [clientId, setClientId] = useState(clients?.[0]?.id ?? '')
  const [projectId, setProjectId] = useState('')
  const [from, setFrom] = useState(isoDaysAgo(30))
  const [to, setTo] = useState(isoToday())
  const [rateBasis, setRateBasis] = useState('effort') // 'effort' | 'hours'

  const selectedClient = useMemo(() => clients?.find((c) => c.id === clientId), [clients, clientId])
  const [rate, setRate] = useState(selectedClient ? selectedClient.rateCents / 100 : 150)
  const [rateTouched, setRateTouched] = useState(false)

  const [preview, setPreview] = useState(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(null)
  const [expanded, setExpanded] = useState(null)

  function onClientChange(id) {
    setClientId(id)
    setPreview(null)
    if (!rateTouched) {
      const c = clients?.find((x) => x.id === id)
      if (c) setRate(c.rateCents / 100)
    }
  }

  // The contract keys on clientName; we send both clientName and clientId so the
  // server can match by id when available and fall back to the literal name.
  function reqBody(previewFlag) {
    return {
      clientName: selectedClient?.name || undefined,
      clientId: clientId || undefined,
      projectIds: projectId ? [projectId] : undefined,
      from,
      to,
      rateCents: Math.round(Number(rate) * 100),
      rateBasis,
      preview: previewFlag,
    }
  }

  async function runPreview() {
    setBusy(true); setError(null)
    try {
      setPreview(await generateInvoiceFromGit(reqBody(true)))
    } catch (e) {
      setError(e.message ?? 'Could not build preview')
    } finally {
      setBusy(false)
    }
  }

  async function save() {
    setBusy(true); setError(null)
    try {
      const inv = await generateInvoiceFromGit(reqBody(false))
      onCreated?.(inv)
    } catch (e) {
      setError(e.message ?? 'Could not create invoice')
      setBusy(false)
    }
  }

  const lines = preview?.lines ?? []
  const total = preview?.totalCents ?? lines.reduce((s, l) => s + (l.amountCents ?? 0), 0)
  const basisLabel = rateBasis === 'hours' ? 'hr' : 'pt'
  const qtyOf = (l) => l.quantity ?? l.effortPoints ?? l.hours ?? 0

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto py-10 px-4"
      style={{ background: 'rgba(2,6,16,0.72)', backdropFilter: 'blur(3px)' }}
      onClick={onClose}
    >
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={`${uid}-title`}
        className="w-full max-w-2xl rounded-[var(--radius-card)] overflow-hidden"
        style={{ background: 'var(--bg-surface)', border: '1px solid var(--border2)' }}
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div
          className="flex items-center justify-between px-6 py-4"
          style={{ borderBottom: '1px solid var(--border)', background: 'linear-gradient(135deg, rgba(45,212,191,0.05), rgba(99,102,241,0.05))' }}
        >
          <div className="flex items-center gap-2.5">
            <div className="w-8 h-8 rounded-lg flex items-center justify-center" style={{ background: 'rgba(45,212,191,0.12)' }}>
              <Sparkles size={16} style={{ color: 'var(--brand-teal)' }} />
            </div>
            <div>
              <h2 id={`${uid}-title`} className="text-sm font-semibold text-[var(--text)] font-display">Generate from git</h2>
              <p className="text-[11px] text-[var(--text-faint)]">Merged delivery → priced line items with evidence</p>
            </div>
          </div>
          <button type="button" onClick={onClose} aria-label="Close dialog" className="rounded text-[var(--text-faint)] hover:text-[var(--text)] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--brand-teal)]">
            <X size={18} aria-hidden="true" />
          </button>
        </div>

        {/* Inputs */}
        <div className="px-6 py-5 space-y-4">
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <Field label="Client" htmlFor={`${uid}-client`}>
              <div className="relative">
                <select id={`${uid}-client`} value={clientId} onChange={(e) => onClientChange(e.target.value)} className={`${inputCls} appearance-none pr-8`}>
                  <option value="">— No client —</option>
                  {clients?.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
                </select>
                <ChevronDown size={14} className="absolute right-3 top-1/2 -translate-y-1/2 pointer-events-none text-[var(--text-faint)]" aria-hidden="true" />
              </div>
            </Field>
            <Field label="Project scope (optional)" htmlFor={`${uid}-project`}>
              <div className="relative">
                <select id={`${uid}-project`} value={projectId} onChange={(e) => { setProjectId(e.target.value); setPreview(null) }} className={`${inputCls} appearance-none pr-8`}>
                  <option value="">— All repositories —</option>
                  {projects?.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
                </select>
                <ChevronDown size={14} className="absolute right-3 top-1/2 -translate-y-1/2 pointer-events-none text-[var(--text-faint)]" aria-hidden="true" />
              </div>
            </Field>
          </div>

          <div className="grid grid-cols-2 sm:grid-cols-4 gap-4">
            <Field label="From" htmlFor={`${uid}-from`}>
              <input id={`${uid}-from`} type="date" value={from} onChange={(e) => { setFrom(e.target.value); setPreview(null) }} className={inputCls} />
            </Field>
            <Field label="To" htmlFor={`${uid}-to`}>
              <input id={`${uid}-to`} type="date" value={to} onChange={(e) => { setTo(e.target.value); setPreview(null) }} className={inputCls} />
            </Field>
            <Field label={`Rate / ${basisLabel}`} htmlFor={`${uid}-rate`}>
              <input
                id={`${uid}-rate`}
                type="number" min="0" step="1" value={rate}
                onChange={(e) => { setRate(e.target.value); setRateTouched(true); setPreview(null) }}
                className={inputCls}
              />
            </Field>
            <Field label="Basis" htmlFor={`${uid}-basis`}>
              <div className="inline-flex w-full rounded-[var(--radius-btn)] overflow-hidden border border-[var(--border)]" role="group" aria-label="Rate basis">
                <button
                  type="button"
                  onClick={() => { setRateBasis('effort'); setPreview(null) }}
                  className="flex-1 px-2 py-2.5 text-xs font-semibold flex items-center justify-center gap-1 transition-colors"
                  style={rateBasis === 'effort' ? { background: 'var(--brand-teal)', color: '#04121a' } : { background: 'var(--bg)', color: 'var(--text-faint)' }}
                >
                  <Gauge size={12} /> Effort
                </button>
                <button
                  type="button"
                  onClick={() => { setRateBasis('hours'); setPreview(null) }}
                  className="flex-1 px-2 py-2.5 text-xs font-semibold flex items-center justify-center gap-1 transition-colors"
                  style={rateBasis === 'hours' ? { background: 'var(--brand-teal)', color: '#04121a' } : { background: 'var(--bg)', color: 'var(--text-faint)' }}
                >
                  <Clock size={12} /> Hours
                </button>
              </div>
            </Field>
          </div>

          <div aria-live="polite">
            {error && (
              <div role="alert" className="text-xs text-red-400 rounded-md px-3 py-2" style={{ background: 'rgba(239,68,68,0.06)', border: '1px solid rgba(239,68,68,0.2)' }}>
                {error}
              </div>
            )}
          </div>

          {/* Preview */}
          {preview && (
            <div className="rounded-[var(--radius-card)] overflow-hidden" style={{ border: '1px solid var(--border)' }}>
              <div className="px-4 py-2.5 flex items-center justify-between" style={{ background: 'var(--bg)', borderBottom: '1px solid var(--border)' }}>
                <span className="text-[11px] font-semibold text-[var(--text-muted)] uppercase tracking-widest flex items-center gap-1.5">
                  <GitBadge /> Preview · {lines.length} line{lines.length !== 1 ? 's' : ''}
                </span>
              </div>
              {lines.length === 0 ? (
                <div className="px-4 py-8 text-center text-xs text-[var(--text-faint)]">
                  <GitMerge size={20} className="mx-auto mb-2 opacity-50" />
                  No merged work found in this window. Try a wider date range or sync more activity.
                </div>
              ) : (
                <div className="divide-y divide-[var(--border)]">
                  {lines.map((l, i) => {
                    const open = expanded === i
                    return (
                      <div key={i}>
                        <button
                          onClick={() => setExpanded(open ? null : i)}
                          className="w-full flex items-center gap-3 px-4 py-3 text-left hover:bg-[var(--bg-surface2)] transition-colors"
                        >
                          <ChevronDown size={14} className={`shrink-0 text-[var(--text-faint)] transition-transform ${open ? 'rotate-180' : ''}`} />
                          <div className="flex-1 min-w-0">
                            <p className="text-xs font-medium text-[var(--text)] truncate">{l.description}</p>
                            <p className="text-[10px] text-[var(--text-faint)] mt-0.5">{qtyOf(l)} {basisLabel} × {format(Number(rate))}</p>
                          </div>
                          <span className="text-sm font-bold text-[var(--text)] shrink-0">{format((l.amountCents ?? 0) / 100)}</span>
                        </button>
                        {open && (
                          <div className="px-4 pb-3 pl-11">
                            <EvidenceList evidence={l.evidence} />
                          </div>
                        )}
                      </div>
                    )
                  })}
                </div>
              )}
              <div className="px-4 py-3 flex items-center justify-between" style={{ background: 'var(--bg)', borderTop: '1px solid var(--border)' }}>
                <span className="text-xs font-semibold text-[var(--text-muted)]">Estimated subtotal</span>
                <span className="text-base font-bold gradient-text">{format((total ?? 0) / 100)}</span>
              </div>
            </div>
          )}
        </div>

        {/* Footer */}
        <div className="px-6 py-4 flex items-center justify-between gap-3" style={{ borderTop: '1px solid var(--border)' }}>
          <p className="text-[10px] text-[var(--text-faint)] flex items-center gap-1.5">
            <FileText size={12} /> Created as a draft — then add manual lines, tax & notes.
          </p>
          <div className="flex items-center gap-2">
            <button
              onClick={runPreview}
              disabled={busy}
              className="px-4 py-2 rounded-[var(--radius-btn)] text-xs font-semibold text-[var(--text)] border border-[var(--border2)] hover:border-[var(--brand-teal)]/50 transition-colors disabled:opacity-50 flex items-center gap-2"
            >
              {busy && !preview ? <Spinner size={13} /> : null}
              {preview ? 'Refresh preview' : 'Preview lines'}
            </button>
            <button
              onClick={save}
              disabled={busy}
              className="px-4 py-2 rounded-[var(--radius-btn)] text-xs font-bold text-[#04121a] transition-all disabled:opacity-40 flex items-center gap-2"
              style={{ background: 'linear-gradient(135deg, var(--brand-teal), var(--brand-indigo))' }}
            >
              {busy && preview ? <Spinner size={13} /> : <Sparkles size={13} />}
              Create draft
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
