/**
 * Invoices — client invoicing derived from git.
 *
 * List of invoices (number, client, period, total, status) · a "Generate from
 * git" flow (merged-PR effort → line items with evidence → draft) · an invoice
 * detail view (expandable line evidence, totals, status actions, share link).
 *
 * This is the "…and the invoice" half of the wedge: a defensible invoice
 * straight from git. Currency-aware, both themes, lucide icons.
 */
import { useState, useCallback, useMemo, useRef, useEffect } from 'react'
import {
  Sparkles, Users, FileText, Plus, ChevronRight, ArrowLeft,
  Link2, Check, Send, CircleDollarSign, Ban, Trash2, X, GitMerge, Receipt, Hourglass, Wallet,
  ExternalLink, Loader2, Download, Plug,
} from 'lucide-react'
import { useCurrency } from '../lib/currency.jsx'
import { StatCard } from '../components/ui/index.js'
import { useProjects } from '../lib/useProjects.js'
import { useAccounting } from '../lib/useAccounting.js'
import { pushInvoice, accountingStartUrl, downloadInvoicePdf } from '../lib/api.js'
import {
  useClients, useInvoiceList, useInvoiceDetail,
  patchInvoice, deleteInvoice,
} from '../lib/useInvoices.js'
import GenerateFromGitModal from '../components/invoices/GenerateFromGitModal.jsx'
import InvoiceEditor from '../components/invoices/InvoiceEditor.jsx'
import {
  InvoiceStatusBadge, LoadingCenter, ErrorBanner, Spinner, providerMeta,
} from '../components/invoices/shared.jsx'
import { fmtDate, periodLabel } from '../components/invoices/format.js'
import { useFocusTrap } from '../lib/useFocusTrap.js'

// ── List row ────────────────────────────────────────────────────────────────────

function InvoiceRow({ inv, onClick }) {
  const { format } = useCurrency()
  return (
    <button
      onClick={onClick}
      className="w-full flex items-center gap-4 px-4 py-3.5 hover:bg-[var(--bg-surface2)] transition-colors text-left group"
    >
      <div className="w-9 h-9 rounded-lg flex items-center justify-center shrink-0" style={{ background: 'var(--bg-surface2)' }}>
        <FileText size={16} className="text-[var(--text-faint)] group-hover:text-[var(--brand-teal)] transition-colors" />
      </div>
      <div className="flex-1 min-w-0">
        <p className="text-sm font-semibold text-[var(--text)] truncate group-hover:text-[var(--brand-teal)] transition-colors font-mono">
          {inv.number}
        </p>
        <p className="text-xs text-[var(--text-faint)] mt-0.5 truncate">
          {inv.clientName || 'No client'} · {periodLabel(inv.periodStart, inv.periodEnd)}
        </p>
      </div>
      <div className="text-right shrink-0">
        <p className="text-sm font-bold text-[var(--text)]">{format((inv.totalCents ?? 0) / 100)}</p>
        <p className="text-[10px] text-[var(--text-faint)] mt-0.5">{fmtDate(inv.issuedAt ?? inv.createdAt)}</p>
      </div>
      <InvoiceStatusBadge status={inv.status} />
      <ChevronRight size={15} className="text-[var(--text-faint)] shrink-0" />
    </button>
  )
}

// ── Detail view ─────────────────────────────────────────────────────────────────

const SHARE_BASE = typeof window !== 'undefined' ? window.location.origin : ''

function StatusAction({ icon: Icon, label, onClick, busy, tone = 'default' }) {
  const tones = {
    default: { border: 'var(--border2)', text: 'var(--text)' },
    teal: { border: 'color-mix(in srgb, var(--brand-teal) 40%, transparent)', text: 'var(--brand-teal)' },
    green: { border: 'color-mix(in srgb, var(--ok) 40%, transparent)', text: 'var(--ok)' },
    red: { border: 'color-mix(in srgb, var(--bad) 35%, transparent)', text: 'var(--bad)' },
  }
  const t = tones[tone]
  return (
    <button
      onClick={onClick}
      disabled={busy}
      className="px-3 py-1.5 rounded-[var(--radius-btn)] text-xs font-semibold flex items-center gap-1.5 transition-colors disabled:opacity-50 hover:brightness-110"
      style={{ border: `1px solid ${t.border}`, color: t.text, background: 'var(--bg)' }}
    >
      {busy ? <Spinner size={12} /> : <Icon size={13} />} {label}
    </button>
  )
}

// AccountingPush — a "Send to…" dropdown over the org's connected accounting
// providers (Xero, QuickBooks, Sage, Zoho Books, FreshBooks). Connected
// providers push the invoice (POST /api/invoices/{id}/push/{provider}); the
// returned deep link surfaces as an "in <Provider> ↗" affordance plus the
// external id. Unconnected-but-configured providers offer a "Connect" link to
// the OAuth start flow.
function AccountingPush({ invoice, onChanged }) {
  const { providers, anyConfigured } = useAccounting()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState(null)
  const [link, setLink] = useState(null) // { provider, externalUrl, externalId } after a fresh push
  const menuRef = useRef(null)

  const connected = providers.filter((p) => p.connected)
  const connectable = providers.filter((p) => p.configured && !p.connected)
  const externalUrl = link?.externalUrl ?? invoice.externalUrl
  const externalId = link?.externalId ?? invoice.externalId
  const externalProvider = link?.provider ?? invoice.externalProvider

  // Close the dropdown on outside-click / Escape.
  useEffect(() => {
    if (!open) return
    function onDoc(e) { if (menuRef.current && !menuRef.current.contains(e.target)) setOpen(false) }
    function onKey(e) { if (e.key === 'Escape') setOpen(false) }
    document.addEventListener('mousedown', onDoc)
    document.addEventListener('keydown', onKey)
    return () => { document.removeEventListener('mousedown', onDoc); document.removeEventListener('keydown', onKey) }
  }, [open])

  async function send(provider) {
    setBusy(provider); setError(null); setOpen(false)
    try {
      const res = await pushInvoice(invoice.id, provider)
      setLink({ provider, externalUrl: res?.externalUrl, externalId: res?.externalId })
      onChanged?.()
    } catch (e) {
      setError(e.message ?? 'Could not send to accounting')
    } finally {
      setBusy('')
    }
  }

  // Nothing configured server-side and never pushed → don't clutter the UI.
  if (!anyConfigured && !externalUrl) return null

  const sentMeta = externalProvider ? providerMeta(externalProvider) : null

  return (
    <div className="mt-4 rounded-[var(--radius-badge)] px-4 py-3.5" style={{ background: 'var(--bg)', border: '1px solid var(--border)' }}>
      <div className="flex items-center justify-between gap-3 flex-wrap">
        <div className="flex items-center gap-2">
          <Receipt size={13} style={{ color: 'var(--brand-teal)' }} />
          <span className="text-xs font-semibold text-[var(--text-muted)]">Accounting</span>
        </div>

        {/* Send-to dropdown */}
        <div className="relative" ref={menuRef}>
          <button
            onClick={() => setOpen((v) => !v)}
            disabled={!!busy}
            aria-haspopup="menu"
            aria-expanded={open}
            className="inline-flex items-center gap-1.5 rounded-[var(--radius-btn)] px-3 py-1.5 text-xs font-bold text-[#04121a] transition-all disabled:opacity-50 hover:brightness-110"
            style={{ background: 'linear-gradient(135deg, var(--brand-teal), var(--brand-indigo))' }}
          >
            {busy ? <Loader2 size={13} className="animate-spin" /> : <Send size={13} />}
            {busy ? 'Sending…' : 'Send to…'}
          </button>

          {open && (
            <div
              role="menu"
              className="absolute right-0 mt-1.5 w-60 rounded-[var(--radius-badge)] overflow-hidden z-20 shadow-xl"
              style={{ background: 'var(--bg-surface)', border: '1px solid var(--border2)' }}
            >
              {connected.length > 0 && (
                <div className="py-1">
                  {connected.map((p) => {
                    const m = providerMeta(p.provider)
                    const sent = externalProvider === p.provider && externalUrl
                    return (
                      <button
                        key={p.provider}
                        role="menuitem"
                        onClick={() => send(p.provider)}
                        className="w-full flex items-center gap-2.5 px-3 py-2 text-left text-xs font-semibold text-[var(--text)] hover:bg-[var(--bg-surface2)] transition-colors"
                      >
                        <m.Mark size={15} />
                        <span className="flex-1 truncate">{sent ? `Re-send to ${m.label}` : m.label}</span>
                        {p.externalName && <span className="text-[10px] text-[var(--text-faint)] truncate max-w-[6rem]">{p.externalName}</span>}
                      </button>
                    )
                  })}
                </div>
              )}
              {connectable.length > 0 && (
                <div className="py-1 border-t border-[var(--border)]">
                  <p className="px-3 py-1 text-[9px] font-bold uppercase tracking-widest text-[var(--text-faint)]">Connect</p>
                  {connectable.map((p) => {
                    const m = providerMeta(p.provider)
                    const url = accountingStartUrl(p.provider)
                    return (
                      <a
                        key={p.provider}
                        role="menuitem"
                        href={url ?? '/settings'}
                        className="w-full flex items-center gap-2.5 px-3 py-2 text-left text-xs font-semibold text-[var(--text-muted)] hover:bg-[var(--bg-surface2)] hover:text-[var(--text)] transition-colors"
                      >
                        <m.Mark size={15} />
                        <span className="flex-1 truncate">{m.label}</span>
                        <Plug size={12} className="text-[var(--text-faint)]" />
                      </a>
                    )
                  })}
                </div>
              )}
              {connected.length === 0 && connectable.length === 0 && (
                <p className="px-3 py-3 text-[11px] text-[var(--text-faint)]">
                  No accounting providers configured. Set one up in <a href="/settings" className="text-[var(--brand-teal)] hover:underline">Settings</a>.
                </p>
              )}
            </div>
          )}
        </div>
      </div>

      {/* Already pushed → show the deep link + external id. */}
      {externalUrl && sentMeta && (
        <div className="flex items-center gap-2 flex-wrap mt-2.5">
          <a
            href={externalUrl} target="_blank" rel="noopener noreferrer"
            className="inline-flex items-center gap-2 rounded-[var(--radius-btn)] px-3 py-1.5 text-xs font-semibold transition-colors hover:brightness-110"
            style={{ border: `1px solid ${sentMeta.accent}55`, color: sentMeta.accent, background: `${sentMeta.accent}14` }}
          >
            <sentMeta.Mark size={14} />
            View in {sentMeta.label}
            <ExternalLink size={12} />
          </a>
          {externalId && (
            <span className="text-[10px] font-mono text-[var(--text-faint)]">#{externalId}</span>
          )}
        </div>
      )}

      {/* Hint when nothing is connected yet. */}
      {connected.length === 0 && !externalUrl && (
        <p className="text-[11px] text-[var(--text-faint)] mt-2">
          Connect an accounting provider in <a href="/settings" className="text-[var(--brand-teal)] hover:underline">Settings</a> to push invoices to your books.
        </p>
      )}

      {error && <p className="text-[11px] mt-2" style={{ color: 'var(--bad)' }}>{error}</p>}
    </div>
  )
}

function InvoiceDetail({ id, onBack, onChanged }) {
  const { invoice, loading, error, refetch } = useInvoiceDetail(id)
  const { format } = useCurrency()
  const [busy, setBusy] = useState(false)
  const [copied, setCopied] = useState(false)
  const [actionError, setActionError] = useState(null)

  const setStatus = useCallback(async (status) => {
    setBusy(true); setActionError(null)
    try {
      await patchInvoice(id, { status })
      await refetch()
      onChanged?.()
    } catch (e) {
      setActionError(e.message ?? 'Could not update status')
    } finally {
      setBusy(false)
    }
  }, [id, refetch, onChanged])

  const remove = useCallback(async () => {
    if (!window.confirm('Delete this invoice? This cannot be undone.')) return
    setBusy(true); setActionError(null)
    try {
      await deleteInvoice(id)
      onChanged?.()
      onBack()
    } catch (e) {
      setActionError(e.message ?? 'Could not delete')
      setBusy(false)
    }
  }, [id, onBack, onChanged])

  const [pdfBusy, setPdfBusy] = useState(false)
  const downloadPdf = useCallback(async () => {
    setPdfBusy(true); setActionError(null)
    try {
      await downloadInvoicePdf(id)
    } catch (e) {
      setActionError(e.message ?? 'Could not download PDF')
    } finally {
      setPdfBusy(false)
    }
  }, [id])

  if (loading) return <LoadingCenter label="Loading invoice…" />
  if (error) return <ErrorBanner msg={error} />
  if (!invoice) return null

  const inv = invoice
  const shareLink = inv.shareToken ? `${SHARE_BASE}/i/${inv.shareToken}` : null

  async function copyShare() {
    if (!shareLink) return
    try {
      await navigator.clipboard.writeText(shareLink)
      setCopied(true)
      setTimeout(() => setCopied(false), 1800)
    } catch { /* ignore */ }
  }

  return (
    <div className="space-y-6 max-w-3xl mx-auto">
      <button onClick={onBack} className="flex items-center gap-1.5 text-xs text-[var(--text-muted)] hover:text-[var(--text)] transition-colors">
        <ArrowLeft size={14} /> Back to invoices
      </button>

      {/* Header card */}
      <div className="rounded-[var(--radius-card)] px-6 py-5" style={{ background: 'var(--bg-surface)', border: '1px solid var(--border)' }}>
        <div className="flex items-start justify-between gap-4 mb-4">
          <div>
            <p className="text-[10px] text-[var(--text-faint)] uppercase tracking-widest font-semibold mb-1">Invoice</p>
            <h2 className="text-2xl font-bold text-[var(--text)] font-display font-mono">{inv.number}</h2>
            <p className="text-xs text-[var(--text-muted)] mt-1.5">
              {inv.clientName || 'No client'}
              {inv.projectName ? ` · ${inv.projectName}` : ''}
            </p>
            <p className="text-xs text-[var(--text-faint)] mt-0.5">{periodLabel(inv.periodStart, inv.periodEnd)}</p>
          </div>
          <InvoiceStatusBadge status={inv.status} />
        </div>

        <div className="rounded-[var(--radius-badge)] p-4 flex items-end justify-between" style={{ background: 'var(--bg)', border: '1px solid var(--border)' }}>
          <span className="text-sm text-[var(--text-muted)]">Total due</span>
          <span className="text-2xl font-bold gradient-text">{format((inv.totalCents ?? 0) / 100)}</span>
        </div>

        {/* Actions */}
        <div className="flex flex-wrap items-center gap-2 mt-4">
          {inv.status === 'draft' && <StatusAction icon={Send} label="Mark sent & share" tone="teal" busy={busy} onClick={() => setStatus('sent')} />}
          {inv.status === 'sent' && <StatusAction icon={CircleDollarSign} label="Mark paid" tone="green" busy={busy} onClick={() => setStatus('paid')} />}
          {(inv.status === 'sent' || inv.status === 'paid') && <StatusAction icon={FileText} label="Back to draft" busy={busy} onClick={() => setStatus('draft')} />}
          {inv.status !== 'void' && <StatusAction icon={Ban} label="Void" tone="red" busy={busy} onClick={() => setStatus('void')} />}
          <StatusAction icon={Download} label="Download PDF" busy={pdfBusy} onClick={downloadPdf} />
          <StatusAction icon={Trash2} label="Delete" tone="red" busy={busy} onClick={remove} />
        </div>

        {actionError && <p className="text-xs mt-3" style={{ color: 'var(--bad)' }}>{actionError}</p>}

        {/* Share link */}
        {shareLink && (
          <div className="mt-4 flex items-center gap-2 rounded-[var(--radius-badge)] px-3 py-2.5" style={{ background: 'rgba(45,212,191,0.05)', border: '1px solid rgba(45,212,191,0.18)' }}>
            <Link2 size={14} style={{ color: 'var(--brand-teal)' }} className="shrink-0" />
            <span className="flex-1 min-w-0 truncate text-xs font-mono text-[var(--text-dim)]">{shareLink}</span>
            <button
              onClick={copyShare}
              className="shrink-0 px-2.5 py-1 rounded-md text-[11px] font-semibold flex items-center gap-1 transition-colors"
              style={{ background: 'rgba(45,212,191,0.12)', color: 'var(--brand-teal)' }}
            >
              {copied ? <><Check size={12} /> Copied</> : <>Copy link</>}
            </button>
          </div>
        )}

        {/* Push to Xero / QuickBooks (alongside git-evidence + manual creation). */}
        <AccountingPush invoice={inv} onChanged={() => { refetch(); onChanged?.() }} />
      </div>

      {/* Editor — git + manual lines, inline edit, discount/tax/notes */}
      <InvoiceEditor invoice={inv} onSaved={() => { refetch(); onChanged?.() }} />

      <p className="text-[10px] text-[var(--text-faint)] flex items-center gap-1.5 px-1">
        <GitMerge size={11} style={{ color: 'var(--brand-teal)' }} />
        Git-derived lines are backed by merged work — expand a line to see the evidence. Add manual lines, set tax & a discount, then push to your books.
      </p>
    </div>
  )
}

// ── Clients drawer ──────────────────────────────────────────────────────────────

function ClientsModal({ clients, onClose, createClient }) {
  const { format } = useCurrency()
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [rate, setRate] = useState(150)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(null)
  const dialogRef = useRef(null)
  useFocusTrap(dialogRef, true, onClose)

  async function add() {
    if (!name.trim()) return
    setBusy(true); setError(null)
    try {
      await createClient({ name: name.trim(), contactEmail: email.trim(), rateCents: Math.round(Number(rate) * 100) })
      setName(''); setEmail(''); setRate(150)
    } catch (e) {
      setError(e.message ?? 'Could not add client')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto py-10 px-4" style={{ background: 'rgba(2,6,16,0.72)', backdropFilter: 'blur(3px)' }} onClick={onClose}>
      <div ref={dialogRef} role="dialog" aria-modal="true" aria-labelledby="clients-modal-title" className="w-full max-w-lg rounded-[var(--radius-card)] overflow-hidden" style={{ background: 'var(--bg-surface)', border: '1px solid var(--border2)' }} onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center justify-between px-6 py-4" style={{ borderBottom: '1px solid var(--border)' }}>
          <div className="flex items-center gap-2.5">
            <Users size={16} style={{ color: 'var(--brand-indigo)' }} aria-hidden="true" />
            <h2 id="clients-modal-title" className="text-sm font-semibold text-[var(--text)] font-display">Clients</h2>
          </div>
          <button type="button" onClick={onClose} aria-label="Close dialog" className="rounded text-[var(--text-faint)] hover:text-[var(--text)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--brand-teal)]"><X size={18} aria-hidden="true" /></button>
        </div>

        <div className="px-6 py-5 space-y-4">
          {/* Add new */}
          <div className="rounded-[var(--radius-badge)] p-4 space-y-3" style={{ background: 'var(--bg)', border: '1px solid var(--border)' }}>
            <p className="text-[10px] font-semibold text-[var(--text-faint)] uppercase tracking-widest">Add a client</p>
            <input value={name} onChange={(e) => setName(e.target.value)} placeholder="Client name" aria-label="Client name" className="w-full bg-[var(--bg-surface)] text-[var(--text)] text-sm rounded-[var(--radius-btn)] px-3 py-2 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/50" />
            <div className="grid grid-cols-2 gap-3">
              <input value={email} onChange={(e) => setEmail(e.target.value)} placeholder="Billing email" aria-label="Billing email" className="bg-[var(--bg-surface)] text-[var(--text)] text-sm rounded-[var(--radius-btn)] px-3 py-2 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/50" />
              <input type="number" min="0" value={rate} onChange={(e) => setRate(e.target.value)} placeholder="Rate / pt" aria-label="Rate per effort point" className="bg-[var(--bg-surface)] text-[var(--text)] text-sm rounded-[var(--radius-btn)] px-3 py-2 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/50" />
            </div>
            <div aria-live="polite">
              {error && <p role="alert" className="text-xs" style={{ color: 'var(--bad)' }}>{error}</p>}
            </div>
            <button onClick={add} disabled={busy || !name.trim()} className="w-full py-2 rounded-[var(--radius-btn)] text-xs font-bold text-[#04121a] disabled:opacity-40 flex items-center justify-center gap-1.5" style={{ background: 'linear-gradient(135deg, var(--brand-teal), var(--brand-indigo))' }}>
              {busy ? <Spinner size={13} /> : <Plus size={13} />} Add client
            </button>
          </div>

          {/* Existing */}
          <div className="space-y-1.5">
            {clients.length === 0 ? (
              <p className="text-xs text-[var(--text-faint)] text-center py-3">No clients yet.</p>
            ) : clients.map((c) => (
              <div key={c.id} className="flex items-center gap-3 px-3 py-2.5 rounded-[var(--radius-badge)]" style={{ background: 'var(--bg)', border: '1px solid var(--border)' }}>
                <div className="flex-1 min-w-0">
                  <p className="text-sm font-medium text-[var(--text)] truncate">{c.name}</p>
                  {c.contactEmail && <p className="text-[11px] text-[var(--text-faint)] truncate">{c.contactEmail}</p>}
                </div>
                <span className="text-[11px] font-mono text-[var(--text-muted)] shrink-0">{format(c.rateCents / 100)}/pt</span>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  )
}

// ── Page root ───────────────────────────────────────────────────────────────────

export default function Invoices() {
  const { invoices, loading, error, refetch } = useInvoiceList()
  const { clients, createClient, refetch: refetchClients } = useClients()
  const { projects } = useProjects()
  const { format } = useCurrency()

  const [selectedId, setSelectedId] = useState(null)
  const [showGenerate, setShowGenerate] = useState(false)
  const [showClients, setShowClients] = useState(false)

  // Headline rollup: outstanding (sent) vs paid vs drafted, in cents.
  const totals = useMemo(() => {
    let outstanding = 0, paid = 0, draft = 0, outstandingN = 0, paidN = 0, draftN = 0
    for (const inv of invoices) {
      const c = inv.totalCents ?? 0
      const s = String(inv.status).toLowerCase()
      if (s === 'sent') { outstanding += c; outstandingN += 1 }
      else if (s === 'paid') { paid += c; paidN += 1 }
      else if (s === 'draft') { draft += c; draftN += 1 }
    }
    return { outstanding, paid, draft, outstandingN, paidN, draftN }
  }, [invoices])

  if (selectedId) {
    return (
      <div className="w-full">
        <InvoiceDetail id={selectedId} onBack={() => setSelectedId(null)} onChanged={refetch} />
      </div>
    )
  }

  return (
    <div className="w-full">
      {/* Header */}
      <div className="flex items-start justify-between gap-4 mb-6 flex-wrap">
        <div className="flex items-start gap-3">
          <span className="mt-0.5 grid place-items-center w-9 h-9 rounded-[var(--radius-btn)] bg-[var(--brand-teal)]/10 border border-[var(--brand-teal)]/20 shrink-0">
            <Receipt size={17} className="text-[var(--brand-teal)]" />
          </span>
          <div>
            <h1 className="text-2xl font-semibold text-[var(--text)] tracking-tight font-display">Invoices</h1>
            <p className="text-sm text-[var(--text-faint)] mt-1">Client invoices derived from merged delivery — every line backed by git.</p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => setShowClients(true)}
            className="px-3.5 py-2 rounded-[var(--radius-btn)] text-xs font-semibold text-[var(--text)] border border-[var(--border2)] hover:border-[var(--brand-indigo)]/50 transition-colors flex items-center gap-1.5"
          >
            <Users size={14} /> Clients
          </button>
          <button
            onClick={() => setShowGenerate(true)}
            className="px-4 py-2 rounded-[var(--radius-btn)] text-xs font-bold text-[#04121a] transition-all flex items-center gap-1.5"
            style={{ background: 'linear-gradient(135deg, var(--brand-teal), var(--brand-indigo))' }}
          >
            <Sparkles size={14} /> Generate from git
          </button>
        </div>
      </div>

      {/* Headline rollup */}
      {!loading && !error && invoices.length > 0 && (
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-4 mb-6">
          <StatCard
            label="Outstanding"
            value={format(totals.outstanding / 100)}
            sublabel={`${totals.outstandingN} sent · awaiting payment`}
            accent="var(--warn)"
            icon={<Hourglass size={14} />}
          />
          <StatCard
            label="Paid"
            value={format(totals.paid / 100)}
            sublabel={`${totals.paidN} settled invoice${totals.paidN !== 1 ? 's' : ''}`}
            accent="var(--ok)"
            icon={<CircleDollarSign size={14} />}
          />
          <StatCard
            label="Drafted"
            value={format(totals.draft / 100)}
            sublabel={`${totals.draftN} not yet sent`}
            accent="var(--info)"
            icon={<Wallet size={14} />}
          />
        </div>
      )}

      {loading ? (
        <LoadingCenter label="Loading invoices…" />
      ) : error ? (
        <ErrorBanner msg={error} />
      ) : invoices.length === 0 ? (
        <EmptyState onGenerate={() => setShowGenerate(true)} />
      ) : (
        <div className="rounded-[var(--radius-card)] overflow-hidden" style={{ background: 'var(--bg-surface)', border: '1px solid var(--border)' }}>
          <div className="px-4 py-3" style={{ borderBottom: '1px solid var(--border)' }}>
            <p className="text-[11px] font-semibold text-[var(--text-faint)] uppercase tracking-widest">
              {invoices.length} invoice{invoices.length !== 1 ? 's' : ''}
            </p>
          </div>
          <div className="divide-y divide-[var(--border)]">
            {invoices.map((inv) => (
              <InvoiceRow key={inv.id} inv={inv} onClick={() => setSelectedId(inv.id)} />
            ))}
          </div>
        </div>
      )}

      {showGenerate && (
        <GenerateFromGitModal
          clients={clients}
          projects={projects}
          onClose={() => setShowGenerate(false)}
          onCreated={(inv) => {
            setShowGenerate(false)
            refetch()
            if (inv?.id) setSelectedId(inv.id)
          }}
        />
      )}
      {showClients && (
        <ClientsModal
          clients={clients}
          createClient={async (b) => { await createClient(b); await refetchClients() }}
          onClose={() => setShowClients(false)}
        />
      )}
    </div>
  )
}

function EmptyState({ onGenerate }) {
  return (
    <div className="rounded-[var(--radius-card)] px-6 py-16 text-center" style={{ background: 'var(--bg-surface)', border: '1px dashed var(--border)' }}>
      <div className="w-12 h-12 rounded-xl flex items-center justify-center mx-auto mb-4" style={{ background: 'linear-gradient(135deg, rgba(45,212,191,0.12), rgba(99,102,241,0.12))' }}>
        <Sparkles size={22} style={{ color: 'var(--brand-teal)' }} />
      </div>
      <p className="text-sm font-semibold text-[var(--text)] mb-1">No invoices yet</p>
      <p className="text-xs text-[var(--text-faint)] max-w-sm mx-auto mb-5">
        Generate your first invoice straight from git: pick a client and date range, and gitstate turns merged-PR effort into priced line items with evidence.
      </p>
      <button
        onClick={onGenerate}
        className="px-4 py-2 rounded-[var(--radius-btn)] text-xs font-bold text-[#04121a] inline-flex items-center gap-1.5"
        style={{ background: 'linear-gradient(135deg, var(--brand-teal), var(--brand-indigo))' }}
      >
        <Sparkles size={14} /> Generate from git
      </button>
    </div>
  )
}
