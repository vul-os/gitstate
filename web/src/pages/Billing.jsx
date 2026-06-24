/**
 * Billing — /settings/billing
 *
 * A premium SaaS billing console: plan & status header, animated usage meters,
 * the plan ladder, an invoices table with PDF download, and payment-method state.
 *
 * Wired to the existing read endpoints (see internal/api/billing.go):
 *   GET /api/billing/plans         → [{ key, name, usdCents, builders, maxConns, features }]
 *                                     (perBuilderCents / includedLLMCents / overageMarkup are
 *                                      consumed defensively if the response grows to include them)
 *   GET /api/billing/subscription  → { id, planKey, status, currentPeriodEnd, paystackSubCode }
 *   GET /api/billing/usage         → [{ kind, totalQty, totalCostUSD }]  (builder_seat, llm_tokens, sync)
 *   GET /api/billing/invoices      → [{ id, status, usdCents, zarCents, fxRate, periodStart, periodEnd, ... }]
 *   GET /api/billing/invoices/{id} → invoice + lines (evidence-backed, P4)
 * Checkout / payment-method updates POST /api/billing/checkout (Paystack), verified via
 *   GET /api/billing/verify/{ref} on redirect-back.
 *
 * Key design decisions reflected here:
 *   A8: USD price shown prominently; ZAR charge noted (FX captured at charge time).
 *   P6: Per-builder pricing; stakeholders/clients/viewers are always free.
 *   P4: Invoice lines backed by git evidence; gaps flagged "needs confirmation".
 *
 * Billing disabled (OSS builds / cfg.Billing.Enabled=false) → graceful empty state.
 */
import { useState, useEffect, useMemo } from 'react'
import { useSearchParams, useNavigate } from 'react-router-dom'
import { usePlans, useSubscription, useUsage, useInvoices, useInvoiceDetail } from '../lib/useBilling.js'
import * as api from '../lib/api.js'
import { Reveal, RevealList } from '../components/Reveal.jsx'
import { UsageMeter } from '../components/billing/UsageMeter.jsx'
import { UsageBreakdown } from '../components/billing/UsageBreakdown.jsx'
import { StatusPill, DunningBanner } from '../components/billing/BillingStatus.jsx'
import { MetersSkeleton, BreakdownSkeleton, PlansSkeleton, InvoicesSkeleton } from '../components/billing/Skeletons.jsx'

// ── Constants ─────────────────────────────────────────────────────────────────

const CHARGE_CURRENCY = import.meta.env.VITE_BILLING_CHARGE_CURRENCY ?? 'ZAR'
const PLAN_ORDER = ['free', 'starter', 'pro', 'scale', 'enterprise']

// ── Currency / format helpers ───────────────────────────────────────────────────

const usdFmt0 = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', minimumFractionDigits: 0 })
const usdFmt2 = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', minimumFractionDigits: 2 })

function fmtUsd(cents, decimals = 0) {
  if (cents == null) return '—'
  return (decimals === 2 ? usdFmt2 : usdFmt0).format(cents / 100)
}

function fmtUsdAmount(usd) {
  if (usd == null) return '—'
  return usdFmt2.format(usd)
}

function fmtZar(cents) {
  if (cents == null) return null
  return new Intl.NumberFormat('en-ZA', { style: 'currency', currency: 'ZAR', minimumFractionDigits: 2 }).format(cents / 100)
}

function fmtDate(d, opts = { month: 'short', day: 'numeric', year: 'numeric' }) {
  if (!d) return '—'
  const t = new Date(d)
  if (Number.isNaN(t.getTime())) return '—'
  return t.toLocaleDateString('en-US', opts)
}

// ── Shared primitives ─────────────────────────────────────────────────────────

function Spinner({ size = 18 }) {
  return (
    <svg className="animate-spin" width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <path d="M21 12a9 9 0 1 1-6.219-8.56" strokeLinecap="round" />
    </svg>
  )
}

function Panel({ children, className = '', glow = false, style }) {
  return (
    <div
      className={`rounded-[var(--radius-card)] ${className}`}
      style={{
        background: 'var(--bg-surface)',
        border: '1px solid var(--border)',
        boxShadow: glow ? '0 0 40px rgba(45,212,191,0.05),0 0 80px rgba(99,102,241,0.04)' : undefined,
        ...style,
      }}
    >
      {children}
    </div>
  )
}

function ErrorBanner({ msg }) {
  return (
    <div
      className="rounded-[var(--radius-card)] px-5 py-4 text-sm text-red-400"
      style={{ background: 'rgba(239,68,68,0.06)', border: '1px solid rgba(239,68,68,0.2)' }}
    >
      {msg}
    </div>
  )
}

function BillingDisabled() {
  return (
    <Panel className="px-6 py-12 text-center" style={{ borderStyle: 'dashed' }}>
      <div className="w-11 h-11 rounded-full flex items-center justify-center mx-auto mb-4" style={{ background: 'var(--bg-surface2)' }}>
        <svg width="20" height="20" fill="none" viewBox="0 0 24 24" stroke="var(--text-faint)" strokeWidth="1.6">
          <path strokeLinecap="round" strokeLinejoin="round" d="M2.25 8.25h19.5M2.25 9h19.5m-16.5 5.25h6m-6 2.25h3m-3.75 3h15a2.25 2.25 0 0 0 2.25-2.25V6.75A2.25 2.25 0 0 0 19.5 4.5h-15a2.25 2.25 0 0 0-2.25 2.25v10.5A2.25 2.25 0 0 0 4.5 19.5Z" />
        </svg>
      </div>
      <p className="text-sm font-semibold text-[var(--text-muted)] mb-1">Billing isn’t enabled on this instance</p>
      <p className="text-xs text-[var(--text-faint)] max-w-sm mx-auto">
        This is an OSS build, or billing isn’t configured. Switch to the cloud edition to manage plans, usage, and invoices.
      </p>
    </Panel>
  )
}

// ── Tab bar ───────────────────────────────────────────────────────────────────

const TABS = [
  { id: 'overview', label: 'Overview' },
  { id: 'plans',    label: 'Plans' },
  { id: 'invoices', label: 'Invoices' },
]

function TabBar({ active, onChange }) {
  return (
    <div
      className="flex gap-1 p-1 rounded-[var(--radius-btn)] mb-8 w-full sm:w-fit"
      style={{ background: 'var(--bg)', border: '1px solid var(--border)' }}
    >
      {TABS.map(t => (
        <button
          key={t.id}
          onClick={() => onChange(t.id)}
          className={[
            'flex-1 sm:flex-none text-xs font-semibold py-2 px-5 rounded-[var(--radius-badge)] transition-all duration-150',
            active === t.id
              ? 'bg-[var(--bg-surface2)] text-[var(--text)] shadow-sm'
              : 'text-[var(--text-muted)] hover:text-[var(--text)]',
          ].join(' ')}
        >
          {t.label}
        </button>
      ))}
    </div>
  )
}

// ── Plan helpers ────────────────────────────────────────────────────────────────

const PLAN_ACCENT = {
  free:       { text: 'var(--text-muted)', grad: ['#475569', '#334155'] },
  starter:    { text: '#a5b4fc',           grad: ['#6366F1', '#4f46e5'] },
  pro:        { text: '#2DD4BF',           grad: ['#2DD4BF', '#0d9488'] },
  scale:      { text: '#f9a8d4',           grad: ['#ec4899', '#db2777'] },
  enterprise: { text: '#818cf8',           grad: ['#6366F1', '#4338ca'] },
}

function planAccent(key) {
  return PLAN_ACCENT[key] ?? PLAN_ACCENT.free
}

/** Resolve a plan's monthly per-builder price (cents). Prefers per_builder_cents,
 *  falls back to flat usd_cents. Returns null when the plan is custom (enterprise). */
function planPriceCents(plan) {
  if (!plan) return null
  const key = plan.key ?? plan.planKey
  if (key === 'enterprise') return null
  const perBuilder = plan.perBuilderCents ?? plan.per_builder_cents
  if (perBuilder != null && perBuilder > 0) return perBuilder
  const flat = plan.usdCents ?? plan.usd_cents
  if (flat != null && flat > 0) return flat
  return 0 // genuinely free
}

function planIsPerBuilder(plan) {
  const perBuilder = plan?.perBuilderCents ?? plan?.per_builder_cents
  return perBuilder != null && perBuilder > 0
}

function planIncludedLlmCents(plan) {
  return plan?.includedLLMCents ?? plan?.included_llm_cents ?? null
}

// Human labels for known feature flags (features jsonb).
const FEATURE_LABELS = {
  pdf_invoices: 'Branded PDF invoices',
  priority_sync: 'Priority repo sync',
  advanced_analytics: 'Advanced analytics',
  sso: 'SAML / SSO',
  audit: 'Audit log',
  sla: 'Uptime SLA',
  scale_to_zero: 'Scale-to-zero compute',
  byok_only: 'Bring-your-own LLM key',
  byok: 'Bring-your-own LLM key',
  self_host: 'Self-host option',
  unlimited: 'Unlimited everything',
  custom: 'Custom contract',
}

/** Turn the features jsonb into a list of {label, on} feature lines for a plan. */
function planFeatureLines(plan) {
  const features = plan?.features ?? {}
  const lines = []
  for (const [k, v] of Object.entries(features)) {
    if (k === 'max_repos' || k === 'history_days') continue // shown as quotas, not checks
    const label = FEATURE_LABELS[k] ?? k.replace(/_/g, ' ')
    lines.push({ label, on: Boolean(v) })
  }
  return lines
}

const CheckIcon = (
  <svg width="13" height="13" fill="none" viewBox="0 0 24 24" stroke="#2DD4BF" strokeWidth="2.5" className="shrink-0 mt-0.5">
    <path strokeLinecap="round" strokeLinejoin="round" d="m4.5 12.75 6 6 9-13.5" />
  </svg>
)
const DashIcon = (
  <svg width="13" height="13" fill="none" viewBox="0 0 24 24" stroke="var(--text-faint)" strokeWidth="2" className="shrink-0 mt-0.5">
    <path strokeLinecap="round" strokeLinejoin="round" d="M5 12h14" />
  </svg>
)

// ── Usage parsing ───────────────────────────────────────────────────────────────

/** Index a usage rollup array by kind → {qty, costUSD}. Tolerates either array
 *  or {rollups:[...]} / object shapes so it degrades if the contract shifts. */
function indexUsage(usage) {
  const arr = Array.isArray(usage) ? usage : (usage?.rollups ?? usage?.usage ?? [])
  const by = {}
  for (const r of arr) {
    const kind = r.kind ?? r.Kind
    if (!kind) continue
    by[kind] = {
      qty: r.totalQty ?? r.total_qty ?? r.TotalQty ?? 0,
      costUSD: r.totalCostUSD ?? r.total_cost_usd ?? r.TotalCostUSD ?? 0,
    }
  }
  return by
}

// ── Plan & status header ────────────────────────────────────────────────────────

function PlanHeader({ plan, planKey, status, periodEnd, hasPaymentMethod, onUpgrade, onManagePayment, busy }) {
  const accent = planAccent(planKey)
  const priceCents = planPriceCents(plan)
  const perBuilder = planIsPerBuilder(plan)
  const name = plan?.name ?? planKey ?? 'Free'

  return (
    <Panel glow className="overflow-hidden">
      <div
        className="px-6 py-6 sm:px-7"
        style={{ background: `linear-gradient(135deg, ${accent.grad[0]}0d, transparent 60%)` }}
      >
        <div className="flex flex-col sm:flex-row sm:items-start sm:justify-between gap-5">
          <div>
            <div className="flex items-center gap-3 mb-2">
              <p className="text-[11px] uppercase tracking-widest font-semibold text-[var(--text-faint)]">Current plan</p>
              <StatusPill status={status} />
            </div>
            <div className="flex items-end gap-3 flex-wrap">
              <h2 className="text-2xl font-bold text-[var(--text)] font-display" style={{ color: accent.text }}>
                {name}
              </h2>
              {priceCents != null ? (
                priceCents === 0 ? (
                  <span className="text-sm text-[var(--text-faint)] mb-0.5">Free forever</span>
                ) : (
                  <span className="text-sm text-[var(--text-faint)] mb-0.5">
                    {fmtUsd(priceCents)}{perBuilder ? ' / builder' : ''} / mo
                  </span>
                )
              ) : (
                <span className="text-sm text-[var(--text-faint)] mb-0.5">Custom pricing</span>
              )}
            </div>
            <p className="text-xs text-[var(--text-faint)] mt-2">
              {periodEnd
                ? <>Renews <strong className="text-[var(--text-muted)]">{fmtDate(periodEnd)}</strong> · billed in {CHARGE_CURRENCY} at the FX rate captured at charge time</>
                : <>No active subscription · stakeholders &amp; clients are always free</>}
            </p>
          </div>

          <div className="flex flex-col items-stretch sm:items-end gap-2 shrink-0">
            <button
              onClick={onUpgrade}
              className="inline-flex items-center justify-center gap-2 px-4 py-2 rounded-[var(--radius-btn)] text-xs font-semibold text-[#0B1120] transition-all hover:opacity-90 active:scale-[0.98]"
              style={{ background: 'linear-gradient(135deg, var(--brand-teal), var(--brand-indigo))' }}
            >
              {planKey === 'enterprise' ? 'Manage plan' : 'Change plan'}
            </button>
            <button
              onClick={onManagePayment}
              disabled={busy}
              className="inline-flex items-center justify-center gap-1.5 px-4 py-2 rounded-[var(--radius-btn)] text-xs font-medium text-[var(--text-muted)] transition-colors hover:text-[var(--text)] disabled:opacity-50"
              style={{ border: '1px solid var(--border2)' }}
            >
              {busy ? <Spinner size={13} /> : (
                <svg width="13" height="13" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
                  <path strokeLinecap="round" strokeLinejoin="round" d="M2.25 8.25h19.5M2.25 9h19.5m-16.5 5.25h6m-6 2.25h3m-3.75 3h15a2.25 2.25 0 0 0 2.25-2.25V6.75A2.25 2.25 0 0 0 19.5 4.5h-15a2.25 2.25 0 0 0-2.25 2.25v10.5A2.25 2.25 0 0 0 4.5 19.5Z" />
                </svg>
              )}
              {hasPaymentMethod ? 'Update card' : 'Add payment method'}
            </button>
          </div>
        </div>
      </div>
    </Panel>
  )
}

// ── Hero headline meter tile ─────────────────────────────────────────────────────

/** A compact, crafted stat tile with a slim animated meter — the headline numbers
 *  that sit directly under the plan header. Over → red, ≥85% → amber, else brand. */
function HeroMeter({ label, value, sub, ratio, over, accent = 'var(--brand-teal)' }) {
  const metered = ratio != null && Number.isFinite(ratio)
  const clamped = metered ? Math.max(0, Math.min(1, ratio)) : 0
  const pct = Math.round(clamped * 100)
  const fill = over
    ? 'linear-gradient(90deg,#f87171,#ef4444)'
    : clamped >= 0.85
      ? 'linear-gradient(90deg,#fbbf24,#f59e0b)'
      : 'linear-gradient(90deg,#2DD4BF,#6366F1)'
  const glow = over ? 'rgba(239,68,68,0.32)' : clamped >= 0.85 ? 'rgba(245,158,11,0.28)' : 'rgba(99,102,241,0.26)'

  const [w, setW] = useState(0)
  useEffect(() => {
    const id = requestAnimationFrame(() => setW(metered ? pct : 100))
    return () => cancelAnimationFrame(id)
  }, [pct, metered])

  return (
    <div
      className="relative rounded-[var(--radius-card)] p-4 overflow-hidden"
      style={{ background: 'var(--bg-surface)', border: '1px solid var(--border)' }}
    >
      <span aria-hidden className="absolute inset-y-0 left-0 w-[2px] opacity-70" style={{ background: accent }} />
      <p className="text-[10.5px] font-mono uppercase tracking-[0.14em] text-[var(--text-faint)] mb-2">{label}</p>
      <div className="flex items-end justify-between gap-2 mb-3">
        <span className="font-display text-[1.7rem] leading-none font-semibold text-[var(--text)] tabular-nums tracking-tight">
          {value}
        </span>
        {sub && <span className="text-[11px] text-[var(--text-faint)] mb-0.5 tabular-nums">{sub}</span>}
      </div>
      <div
        className="relative h-2 rounded-full overflow-hidden"
        style={{ background: 'var(--bg-surface3)', border: '1px solid var(--border)', boxShadow: 'inset 0 1px 2px rgba(0,0,0,0.25)' }}
      >
        <div
          className="h-full rounded-full"
          style={{
            width: `${w}%`,
            background: fill,
            boxShadow: `0 0 10px ${glow}`,
            transition: 'width 900ms cubic-bezier(0.22,1,0.36,1)',
            opacity: metered ? 1 : 0.4,
          }}
        />
      </div>
    </div>
  )
}

// ── Payment-method card ─────────────────────────────────────────────────────────

function PaymentMethodCard({ hasPaymentMethod, onManage, busy }) {
  return (
    <Panel className="px-6 py-5">
      <div className="flex items-center justify-between gap-4">
        <div className="flex items-center gap-3">
          <div
            className="w-10 h-10 rounded-lg flex items-center justify-center shrink-0"
            style={{ background: hasPaymentMethod ? 'rgba(45,212,191,0.1)' : 'var(--bg-surface3)' }}
          >
            <svg width="18" height="18" fill="none" viewBox="0 0 24 24" stroke={hasPaymentMethod ? 'var(--brand-teal)' : 'var(--text-faint)'} strokeWidth="1.8">
              <path strokeLinecap="round" strokeLinejoin="round" d="M2.25 8.25h19.5M2.25 9h19.5m-16.5 5.25h6m-6 2.25h3m-3.75 3h15a2.25 2.25 0 0 0 2.25-2.25V6.75A2.25 2.25 0 0 0 19.5 4.5h-15a2.25 2.25 0 0 0-2.25 2.25v10.5A2.25 2.25 0 0 0 4.5 19.5Z" />
            </svg>
          </div>
          <div>
            <p className="text-sm font-semibold text-[var(--text)]">Payment method</p>
            <p className="text-xs text-[var(--text-faint)] mt-0.5">
              {hasPaymentMethod
                ? <>Card on file · charged in {CHARGE_CURRENCY} via Paystack</>
                : <>No card on file — add one to upgrade or enable managed LLM</>}
            </p>
          </div>
        </div>
        <button
          onClick={onManage}
          disabled={busy}
          className="shrink-0 inline-flex items-center gap-1.5 px-3.5 py-2 rounded-[var(--radius-btn)] text-xs font-semibold text-[var(--brand-teal)] transition-colors hover:brightness-110 disabled:opacity-50"
          style={{ border: '1px solid var(--border2)', background: 'var(--bg)' }}
        >
          {busy ? <Spinner size={13} /> : null}
          {hasPaymentMethod ? 'Update' : 'Add card'}
        </button>
      </div>
    </Panel>
  )
}

// ── Overview tab (status + usage meters) ────────────────────────────────────────

function OverviewTab({ onGoToPlans }) {
  const { data: usage, loading: usageLoading, error: usageError, disabled } = useUsage()
  const { data: sub } = useSubscription()
  const { data: plansData } = usePlans()
  const { data: invoicesData } = useInvoices()

  const [busy, setBusy] = useState(false)
  const [actionError, setActionError] = useState(null)

  const planList = Array.isArray(plansData) ? plansData : (plansData?.plans ?? [])
  const planKey = sub?.planKey ?? sub?.plan_key ?? sub?.plan ?? 'free'
  const status = sub?.status ?? (sub ? 'active' : 'active')
  const periodEnd = sub?.currentPeriodEnd ?? sub?.current_period_end
  const plan = planList.find(p => (p.key ?? p.planKey) === planKey) ?? null
  // paystackSubCode present ⇒ a Paystack subscription/card exists. Best available
  // signal — payment_method_on_file isn't exposed by the subscription endpoint.
  const hasPaymentMethod = Boolean(sub?.paystackSubCode ?? sub?.paystack_sub_code)

  async function startPaystack() {
    setBusy(true); setActionError(null)
    try {
      const target = planKey && planKey !== 'free' ? planKey : 'pro'
      const result = await api.post('/api/billing/checkout', { plan: target })
      const url = result?.authorization_url ?? result?.url
      if (url) { window.location.href = url; return }
      setActionError('Could not start the payment flow. Please try again.')
    } catch (e) {
      setActionError(e?.message ?? 'Could not start the payment flow.')
    } finally {
      setBusy(false)
    }
  }

  if (disabled) return <BillingDisabled />

  const by = indexUsage(usage)
  const builderCap = plan?.builders ?? plan?.builder_limit ?? 0 // 0 = unlimited
  const builderUsed = Math.round(by.builder_seat?.qty ?? 0)
  const llmSpentUSD = by.llm_tokens?.costUSD ?? 0
  const includedLlmCents = planIncludedLlmCents(plan)
  const includedLlmUSD = includedLlmCents != null ? includedLlmCents / 100 : null
  const llmOver = includedLlmUSD != null && llmSpentUSD > includedLlmUSD
  const overageMarkup = plan?.overageMarkup ?? plan?.overage_markup ?? 1.05
  const overagePct = Math.round((overageMarkup - 1) * 100)
  const syncCount = Math.round(by.sync?.qty ?? 0)

  const maxRepos = plan?.features?.max_repos ?? null
  const historyDays = plan?.features?.history_days ?? null

  // ZAR estimate for usage costs: the usage endpoint is USD-only, so we reuse the
  // FX rate captured on the most-recent invoice (1 USD = N ZAR) to *estimate* the
  // charge-currency amount. Falls back to USD-only when no invoice FX is available.
  const invoiceList = Array.isArray(invoicesData) ? invoicesData : (invoicesData?.invoices ?? [])
  const fxRate = (() => {
    for (const inv of invoiceList) {
      const r = inv.fxRate ?? inv.fx_rate
      if (r != null && r > 0) return Number(r)
    }
    return null
  })()
  const toZar = (usd) => (fxRate != null ? usd * fxRate : null)

  const llmHasBudget = includedLlmUSD != null
  const llmBillable = llmHasBudget ? Math.max(0, llmSpentUSD - (includedLlmUSD ?? 0)) : 0
  const breakdownTiles = [
    {
      key: 'builders',
      icon: (
        <svg width="16" height="16" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="1.8">
          <path strokeLinecap="round" strokeLinejoin="round" d="M15 19.128a9.38 9.38 0 0 0 2.625.372 9.337 9.337 0 0 0 4.121-.952 4.125 4.125 0 0 0-7.533-2.493M15 19.128v-.003c0-1.113-.285-2.16-.786-3.07M15 19.128v.106A12.318 12.318 0 0 1 8.624 21c-2.331 0-4.512-.645-6.374-1.766l-.001-.109a6.375 6.375 0 0 1 11.964-3.07M12 6.375a3.375 3.375 0 1 1-6.75 0 3.375 3.375 0 0 1 6.75 0Z" />
        </svg>
      ),
      accent: 'var(--brand-indigo)',
      label: 'Builder seats',
      qty: String(builderUsed),
      qtyUnit: builderUsed === 1 ? 'seat' : 'seats',
      costUSD: by.builder_seat?.costUSD ?? 0,
      costZAR: toZar(by.builder_seat?.costUSD ?? 0),
      empty: builderUsed === 0 && (by.builder_seat?.costUSD ?? 0) === 0,
      note: 'Devs, PMs & anyone creating work. Stakeholders are free.',
    },
    {
      key: 'llm',
      icon: (
        <svg width="16" height="16" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="1.8">
          <path strokeLinecap="round" strokeLinejoin="round" d="M9.813 15.904 9 18.75l-.813-2.846a4.5 4.5 0 0 0-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 0 0 3.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 0 0 3.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 0 0-3.09 3.09Z" />
        </svg>
      ),
      accent: 'var(--brand-teal)',
      label: 'Managed LLM',
      qty: fmtUsdAmount(llmSpentUSD),
      qtyUnit: null,
      costUSD: llmBillable,
      costZAR: llmHasBudget ? toZar(llmBillable) : null,
      empty: llmSpentUSD === 0,
      free: !llmHasBudget || llmBillable === 0,
      note: !llmHasBudget
        ? 'Bring-your-own key — not billed.'
        : llmBillable === 0
          ? `Within the ${fmtUsdAmount(includedLlmUSD)} included allowance.`
          : `Overage above allowance, billed at +${overagePct}%.`,
    },
    {
      key: 'repos',
      icon: (
        <svg width="16" height="16" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="1.8">
          <path strokeLinecap="round" strokeLinejoin="round" d="M2.25 12.75V12A2.25 2.25 0 0 1 4.5 9.75h15A2.25 2.25 0 0 1 21.75 12v.75m-8.69-6.44-2.12-2.12a1.5 1.5 0 0 0-1.061-.44H4.5A2.25 2.25 0 0 0 2.25 6v12a2.25 2.25 0 0 0 2.25 2.25h15A2.25 2.25 0 0 0 21.75 18V9a2.25 2.25 0 0 0-2.25-2.25h-5.379a1.5 1.5 0 0 1-1.06-.44Z" />
        </svg>
      ),
      accent: 'var(--chart-5)',
      label: 'Repo sync',
      qty: String(syncCount),
      qtyUnit: maxRepos != null ? `/ ${maxRepos}` : 'events',
      costUSD: by.sync?.costUSD ?? 0,
      costZAR: toZar(by.sync?.costUSD ?? 0),
      empty: syncCount === 0 && (by.sync?.costUSD ?? 0) === 0,
      free: (by.sync?.costUSD ?? 0) === 0,
      note: historyDays ? `${historyDays} days of history retained.` : 'Connected repositories synced this period.',
    },
  ]
  const fxNote = fxRate != null
    ? `ZAR amounts are estimates at 1 USD = R${fxRate.toFixed(2)} (last invoice FX); each invoice locks its own rate at charge time.`
    : null

  return (
    <div className="space-y-6">
      {actionError && <ErrorBanner msg={actionError} />}

      <DunningBanner
        status={status}
        deadline={periodEnd ? fmtDate(periodEnd) : null}
        onUpdate={startPaystack}
        busy={busy}
      />

      <Reveal>
        <PlanHeader
          plan={plan}
          planKey={planKey}
          status={status}
          periodEnd={periodEnd}
          hasPaymentMethod={hasPaymentMethod}
          onUpgrade={onGoToPlans}
          onManagePayment={startPaystack}
          busy={busy}
        />
      </Reveal>

      {/* Headline meters — the hero numbers at a glance */}
      {!usageLoading && !usageError && (
        <RevealList className="grid grid-cols-1 sm:grid-cols-3 gap-3" staggerDelay={0.06} baseDelay={0.04}>
          <HeroMeter
            label="Billable builders"
            value={String(builderUsed)}
            sub={builderCap > 0 ? `of ${builderCap}` : 'unlimited'}
            ratio={builderCap > 0 ? builderUsed / builderCap : null}
            over={builderCap > 0 && builderUsed > builderCap}
            accent="var(--brand-indigo)"
          />
          <HeroMeter
            label="Managed-LLM spend"
            value={fmtUsdAmount(llmSpentUSD)}
            sub={includedLlmUSD != null ? `of ${fmtUsdAmount(includedLlmUSD)}` : 'BYOK'}
            ratio={includedLlmUSD != null && includedLlmUSD > 0 ? llmSpentUSD / includedLlmUSD : (includedLlmUSD === 0 ? (llmSpentUSD > 0 ? 1 : 0) : null)}
            over={llmOver}
            accent="var(--brand-teal)"
          />
          <HeroMeter
            label={maxRepos != null ? 'Connected repos' : 'Repo activity'}
            value={String(syncCount)}
            sub={maxRepos != null ? `of ${maxRepos}` : 'events'}
            ratio={maxRepos != null && maxRepos > 0 ? Math.min(syncCount, maxRepos) / maxRepos : null}
            over={maxRepos != null && syncCount > maxRepos}
            accent="var(--chart-5)"
          />
        </RevealList>
      )}

      {/* Usage meters */}
      {usageLoading ? (
        <>
          <MetersSkeleton />
          <BreakdownSkeleton />
        </>
      ) : usageError ? (
        <ErrorBanner msg={usageError} />
      ) : (
        <>
        <Reveal delay={0.05}>
          <Panel className="p-6">
            <div className="flex items-center justify-between mb-6">
              <h3 className="text-sm font-semibold text-[var(--text)]">Usage this period</h3>
              {periodEnd && (
                <span className="text-[11px] text-[var(--text-faint)]">resets {fmtDate(periodEnd)}</span>
              )}
            </div>

            <div className="space-y-7">
              <UsageMeter
                label="Billable builders"
                valueText={String(builderUsed)}
                limitText={builderCap > 0 ? String(builderCap) : 'Unlimited'}
                ratio={builderCap > 0 ? builderUsed / builderCap : null}
                over={builderCap > 0 && builderUsed > builderCap}
                overText="Over seat cap — upgrade to add builders"
                hint="Stakeholders, clients & viewers are always free and never counted."
                icon={(
                  <svg width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="1.8">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M15 19.128a9.38 9.38 0 0 0 2.625.372 9.337 9.337 0 0 0 4.121-.952 4.125 4.125 0 0 0-7.533-2.493M15 19.128v-.003c0-1.113-.285-2.16-.786-3.07M15 19.128v.106A12.318 12.318 0 0 1 8.624 21c-2.331 0-4.512-.645-6.374-1.766l-.001-.109a6.375 6.375 0 0 1 11.964-3.07M12 6.375a3.375 3.375 0 1 1-6.75 0 3.375 3.375 0 0 1 6.75 0Z" />
                  </svg>
                )}
              />

              <UsageMeter
                label="Managed-LLM spend"
                valueText={fmtUsdAmount(llmSpentUSD)}
                limitText={includedLlmUSD != null ? `${fmtUsdAmount(includedLlmUSD)} included` : null}
                ratio={includedLlmUSD != null && includedLlmUSD > 0 ? llmSpentUSD / includedLlmUSD : (includedLlmUSD === 0 ? (llmSpentUSD > 0 ? 1 : 0) : null)}
                over={llmOver}
                overText={`Over allowance — overage billed at +${overagePct}%`}
                hint={includedLlmUSD == null
                  ? 'Bring-your-own key on this plan — managed-LLM usage isn’t billed.'
                  : `Beyond the included allowance, usage bills at provider cost +${overagePct}%.`}
                icon={(
                  <svg width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="1.8">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M9.813 15.904 9 18.75l-.813-2.846a4.5 4.5 0 0 0-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 0 0 3.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 0 0 3.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 0 0-3.09 3.09Z" />
                  </svg>
                )}
              />

              {maxRepos != null && (
                <UsageMeter
                  label="Connected repositories"
                  valueText={String(syncCount)}
                  limitText={String(maxRepos)}
                  ratio={maxRepos > 0 ? Math.min(syncCount, maxRepos) / maxRepos : null}
                  over={syncCount > maxRepos}
                  overText="Repo limit reached — upgrade for more"
                  hint={historyDays ? `Free tier retains ${historyDays} days of history.` : undefined}
                  icon={(
                    <svg width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="1.8">
                      <path strokeLinecap="round" strokeLinejoin="round" d="M2.25 12.75V12A2.25 2.25 0 0 1 4.5 9.75h15A2.25 2.25 0 0 1 21.75 12v.75m-8.69-6.44-2.12-2.12a1.5 1.5 0 0 0-1.061-.44H4.5A2.25 2.25 0 0 0 2.25 6v12a2.25 2.25 0 0 0 2.25 2.25h15A2.25 2.25 0 0 0 21.75 18V9a2.25 2.25 0 0 0-2.25-2.25h-5.379a1.5 1.5 0 0 1-1.06-.44Z" />
                    </svg>
                  )}
                />
              )}
            </div>
          </Panel>
        </Reveal>

        <Reveal delay={0.08}>
          <div>
            <div className="flex items-center justify-between mb-3">
              <h3 className="text-sm font-semibold text-[var(--text)]">Cost breakdown</h3>
              <span className="text-[11px] text-[var(--text-faint)]">USD billed{fxRate != null ? ` · ZAR est.` : ''}</span>
            </div>
            <UsageBreakdown tiles={breakdownTiles} fxNote={fxNote} />
          </div>
        </Reveal>
        </>
      )}

      <Reveal delay={0.12}>
        <PaymentMethodCard hasPaymentMethod={hasPaymentMethod} onManage={startPaystack} busy={busy} />
      </Reveal>
    </div>
  )
}

// ── Plan ladder ───────────────────────────────────────────────────────────────

function PlanCard({ plan, isCurrent, onUpgrade, upgrading, builderCount = 0 }) {
  const key = plan.key ?? plan.planKey ?? ''
  const accent = planAccent(key)
  const priceCents = planPriceCents(plan)
  const perBuilder = planIsPerBuilder(plan)
  const includedLlm = planIncludedLlmCents(plan)
  const builderCap = plan.builders ?? plan.builder_limit ?? 0
  const popular = key === 'pro'
  const isEnterprise = key === 'enterprise'
  const features = planFeatureLines(plan)

  // Monthly cost preview at the org's current billable-builder count.
  const seats = Math.max(1, builderCount)
  const monthlyCents = perBuilder && priceCents > 0 ? priceCents * seats : (priceCents > 0 ? priceCents : 0)
  const showPreview = !isEnterprise && priceCents > 0 && perBuilder

  return (
    <div
      className="relative flex flex-col rounded-[var(--radius-card)] p-6 transition-all duration-200 h-full"
      style={{
        background: isCurrent ? `linear-gradient(160deg, ${accent.grad[0]}10, var(--bg-surface) 55%)` : 'var(--bg-surface)',
        border: `1px solid ${isCurrent ? accent.grad[0] + '99' : (popular ? '#6366F166' : 'var(--border)')}`,
        boxShadow: isCurrent ? `0 0 0 1px ${accent.grad[0]}40` : undefined,
      }}
    >
      {popular && !isCurrent && (
        <div className="absolute -top-3 left-1/2 -translate-x-1/2">
          <span className="text-[10px] font-bold px-3 py-1 rounded-full uppercase tracking-widest text-white"
            style={{ background: 'linear-gradient(90deg,#6366F1,#2DD4BF)' }}>
            Most popular
          </span>
        </div>
      )}
      {isCurrent && (
        <span
          className="absolute top-4 right-4 text-[10px] font-mono font-bold px-2 py-0.5 rounded-full uppercase tracking-widest"
          style={{ background: `${accent.grad[0]}22`, color: accent.text, border: `1px solid ${accent.grad[0]}55` }}
        >
          Current
        </span>
      )}

      <h3 className="text-sm font-bold uppercase tracking-widest mb-3" style={{ color: accent.text }}>
        {plan.name ?? key}
      </h3>

      {/* Price */}
      <div className="mb-1 flex items-end gap-1.5">
        {isEnterprise ? (
          <span className="text-3xl font-extrabold text-[var(--text)]">Custom</span>
        ) : priceCents === 0 ? (
          <span className="text-3xl font-extrabold text-[var(--text)]">Free</span>
        ) : (
          <>
            <span className="text-3xl font-extrabold text-[var(--text)] tabular-nums">{fmtUsd(priceCents)}</span>
            <span className="text-xs text-[var(--text-faint)] mb-1.5">{perBuilder ? '/builder/mo' : '/mo'}</span>
          </>
        )}
      </div>
      <p className="text-[11px] text-[var(--text-faint)] mb-5 min-h-[14px]">
        {isEnterprise
          ? 'Self-host, BYOK, unlimited seats'
          : priceCents === 0
            ? 'Bring-your-own LLM key'
            : <>charged in {CHARGE_CURRENCY} at current FX</>}
      </p>

      {showPreview && (
        <div
          className="rounded-[var(--radius-badge)] px-3 py-2 mb-4 flex items-center justify-between gap-2"
          style={{ background: `color-mix(in srgb, ${accent.grad[0]} 8%, transparent)`, border: `1px solid ${accent.grad[0]}33` }}
        >
          <span className="text-[11px] text-[var(--text-muted)]">
            At your <strong className="text-[var(--text)]">{seats}</strong> builder{seats !== 1 ? 's' : ''}
          </span>
          <span className="text-xs font-bold tabular-nums" style={{ color: accent.text }}>
            {fmtUsd(monthlyCents)}/mo
          </span>
        </div>
      )}

      {/* Quotas */}
      <div className="rounded-[var(--radius-badge)] p-3 mb-4 space-y-1.5" style={{ background: 'var(--bg)', border: '1px solid var(--border)' }}>
        <div className="flex items-center justify-between text-xs">
          <span className="text-[var(--text-muted)]">Builder seats</span>
          <span className="font-bold text-[var(--text)]">{builderCap > 0 ? `Up to ${builderCap}` : 'Unlimited'}</span>
        </div>
        <div className="flex items-center justify-between text-xs">
          <span className="text-[var(--text-muted)]">Stakeholders</span>
          <span className="font-bold text-[var(--brand-teal)]">Always free</span>
        </div>
        {includedLlm != null && includedLlm > 0 && (
          <div className="flex items-center justify-between text-xs">
            <span className="text-[var(--text-muted)]">Included LLM</span>
            <span className="font-bold text-[var(--text)]">{fmtUsd(includedLlm)}/builder</span>
          </div>
        )}
        {plan.features?.max_repos != null && (
          <div className="flex items-center justify-between text-xs">
            <span className="text-[var(--text-muted)]">Repositories</span>
            <span className="font-bold text-[var(--text)]">{plan.features.max_repos}</span>
          </div>
        )}
      </div>

      {/* Features */}
      <div className="flex-1 space-y-2 mb-6">
        {features.length === 0 ? (
          <>
            <div className="flex items-start gap-2 text-xs text-[var(--text-muted)]">{CheckIcon}<span>Git-derived project state</span></div>
            <div className="flex items-start gap-2 text-xs text-[var(--text-muted)]">{CheckIcon}<span>Evidence-backed invoicing</span></div>
          </>
        ) : features.map((f, i) => (
          <div key={i} className={`flex items-start gap-2 text-xs ${f.on ? 'text-[var(--text-muted)]' : 'text-[var(--text-faint)]'}`}>
            {f.on ? CheckIcon : DashIcon}
            <span>{f.label}</span>
          </div>
        ))}
      </div>

      {/* CTA */}
      {isCurrent ? (
        <div className="w-full py-2.5 rounded-[var(--radius-btn)] text-xs font-semibold text-center"
          style={{ background: `${accent.grad[0]}18`, color: accent.text, border: `1px solid ${accent.grad[0]}40` }}>
          Current plan
        </div>
      ) : isEnterprise ? (
        <a
          href="mailto:sales@gitstate.dev?subject=Enterprise%20plan"
          className="w-full py-2.5 rounded-[var(--radius-btn)] text-xs font-semibold text-center text-[var(--text-muted)] transition-colors hover:text-[var(--text)] hover:border-[var(--brand-teal)]"
          style={{ border: '1px solid var(--border2)' }}
        >
          Contact sales
        </a>
      ) : priceCents === 0 ? (
        <button
          onClick={() => onUpgrade(key)}
          className="w-full py-2.5 rounded-[var(--radius-btn)] text-xs font-semibold text-center text-[var(--text-faint)] transition-colors hover:text-[var(--text-muted)]"
          style={{ border: '1px solid var(--border)' }}
        >
          Downgrade to Free
        </button>
      ) : (
        <button
          onClick={() => onUpgrade(key)}
          disabled={upgrading}
          className="w-full py-2.5 rounded-[var(--radius-btn)] text-xs font-semibold text-white transition-all duration-150 disabled:opacity-50 flex items-center justify-center gap-2 active:scale-[0.98]"
          style={{ background: `linear-gradient(135deg, ${accent.grad[0]}, ${accent.grad[1]})` }}
        >
          {upgrading && <Spinner size={14} />}
          Choose {plan.name ?? key}
        </button>
      )}
    </div>
  )
}

function PlansTab() {
  const { data: plans, loading, error, disabled } = usePlans()
  const { data: sub } = useSubscription()
  const { data: usage } = useUsage()
  const [upgrading, setUpgrading] = useState(null)
  const [upgradeError, setUpgradeError] = useState(null)

  const builderCount = Math.round(indexUsage(usage).builder_seat?.qty ?? 0)

  async function handleUpgrade(planKey) {
    if (planKey === 'free') { setUpgradeError('Contact support to downgrade to the Free plan.'); return }
    setUpgrading(planKey); setUpgradeError(null)
    try {
      const result = await api.post('/api/billing/checkout', { plan: planKey })
      const url = result?.authorization_url ?? result?.url
      if (url) { window.location.href = url; return }
      setUpgradeError('Checkout did not return a payment URL. Please try again.')
      setUpgrading(null)
    } catch (e) {
      setUpgradeError(e?.message ?? 'Checkout failed. Please try again.')
      setUpgrading(null)
    }
  }

  const sorted = useMemo(() => {
    const list = Array.isArray(plans) ? plans : (plans?.plans ?? [])
    return [...list].sort((a, b) => {
      const ai = PLAN_ORDER.indexOf(a.key ?? a.planKey ?? ''); const bi = PLAN_ORDER.indexOf(b.key ?? b.planKey ?? '')
      return (ai < 0 ? 99 : ai) - (bi < 0 ? 99 : bi)
    })
  }, [plans])

  if (disabled) return <BillingDisabled />
  if (loading) return <PlansSkeleton />
  if (error) return <ErrorBanner msg={error} />

  const currentPlanKey = sub?.planKey ?? sub?.plan_key ?? sub?.plan ?? 'free'

  return (
    <div className="space-y-6">
      {/* Per-builder wedge callout */}
      <div
        className="rounded-[var(--radius-card)] px-5 py-4 flex items-start gap-4"
        style={{ background: 'linear-gradient(135deg, rgba(45,212,191,0.05), rgba(99,102,241,0.05))', border: '1px solid rgba(45,212,191,0.15)' }}
      >
        <div className="shrink-0 w-9 h-9 rounded-full flex items-center justify-center" style={{ background: 'rgba(45,212,191,0.1)' }}>
          <svg width="17" height="17" fill="none" viewBox="0 0 24 24" stroke="var(--brand-teal)" strokeWidth="2">
            <path strokeLinecap="round" strokeLinejoin="round" d="M18 18.72a9.094 9.094 0 0 0 3.741-.479 3 3 0 0 0-4.682-2.72m.94 3.198.001.031c0 .225-.012.447-.037.666A11.944 11.944 0 0 1 12 21c-2.17 0-4.207-.576-5.963-1.584A6.062 6.062 0 0 1 6 18.719m12 0a5.971 5.971 0 0 0-.941-3.197m0 0A5.995 5.995 0 0 0 12 12.75a5.995 5.995 0 0 0-5.058 2.772m0 0a3 3 0 0 0-4.681 2.72 8.986 8.986 0 0 0 3.74.477m.94-3.197a5.971 5.971 0 0 0-.94 3.197M15 6.75a3 3 0 1 1-6 0 3 3 0 0 1 6 0Zm6 3a2.25 2.25 0 1 1-4.5 0 2.25 2.25 0 0 1 4.5 0Zm-13.5 0a2.25 2.25 0 1 1-4.5 0 2.25 2.25 0 0 1 4.5 0Z" />
          </svg>
        </div>
        <div>
          <p className="text-sm font-semibold text-[var(--text)] mb-0.5">Per-builder pricing — clients &amp; stakeholders are always free</p>
          <p className="text-xs text-[var(--text-muted)] leading-relaxed">
            You only pay for <strong className="text-[var(--text)]">builder seats</strong> (devs, PMs, anyone who creates work).
            Invite as many stakeholders, clients, and read-only viewers as you like — they’re <strong className="text-[var(--brand-teal)]">never billed</strong>.
            Prices are in USD; your card is charged in {CHARGE_CURRENCY} at the FX rate captured on each invoice.
          </p>
        </div>
      </div>

      {upgradeError && <ErrorBanner msg={upgradeError} />}

      {sorted.length === 0 ? (
        <div className="text-sm text-[var(--text-muted)] text-center py-10">No plan data returned. Check the billing configuration.</div>
      ) : (
        <RevealList className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4 items-stretch" staggerDelay={0.05}>
          {sorted.map(plan => (
            <PlanCard
              key={plan.key ?? plan.planKey ?? plan.id}
              plan={plan}
              isCurrent={(plan.key ?? plan.planKey) === currentPlanKey}
              onUpgrade={handleUpgrade}
              upgrading={upgrading === (plan.key ?? plan.planKey)}
              builderCount={builderCount}
            />
          ))}
        </RevealList>
      )}
    </div>
  )
}

// ── Invoices ────────────────────────────────────────────────────────────────────

const INVOICE_STATUS = {
  paid:     { bg: 'rgba(34,197,94,0.1)',  border: 'rgba(34,197,94,0.28)', text: '#4ade80', label: 'Paid' },
  open:     { bg: 'rgba(245,158,11,0.1)', border: 'rgba(245,158,11,0.3)', text: '#fbbf24', label: 'Due' },
  past_due: { bg: 'rgba(239,68,68,0.1)',  border: 'rgba(239,68,68,0.3)',  text: '#f87171', label: 'Past due' },
  draft:    { bg: 'var(--bg-surface3)',   border: 'var(--border2)',       text: 'var(--text-muted)', label: 'Draft' },
  void:     { bg: 'rgba(239,68,68,0.07)', border: 'rgba(239,68,68,0.2)',  text: '#f87171', label: 'Void' },
}

function InvoiceStatus({ status }) {
  const s = INVOICE_STATUS[status?.toLowerCase()] ?? { bg: 'var(--bg-surface3)', border: 'var(--border)', text: 'var(--text-muted)', label: status ?? 'Unknown' }
  return (
    <span className="text-[10px] font-semibold px-2 py-0.5 rounded-full uppercase tracking-wide whitespace-nowrap"
      style={{ background: s.bg, border: `1px solid ${s.border}`, color: s.text }}>
      {s.label}
    </span>
  )
}

function invoicePeriodLabel(inv) {
  const start = inv.periodStart ?? inv.period_start
  const end = inv.periodEnd ?? inv.period_end
  if (start && end) return `${fmtDate(start, { month: 'short', day: 'numeric' })} – ${fmtDate(end)}`
  if (inv.period) return inv.period
  const created = inv.createdAt ?? inv.created_at
  return created ? fmtDate(created) : '—'
}

function InvoiceRow({ inv, onClick }) {
  const usdCents = inv.usdCents ?? inv.usd_cents ?? null
  const zarCents = inv.zarCents ?? inv.zar_cents ?? null
  const zar = fmtZar(zarCents)
  const status = inv.status ?? 'unknown'

  return (
    <button
      onClick={onClick}
      className="w-full flex items-center gap-4 px-4 py-3.5 hover:bg-[var(--bg-surface2)] transition-colors text-left group"
    >
      <div className="w-9 h-9 rounded-lg flex items-center justify-center shrink-0" style={{ background: 'var(--bg-surface3)' }}>
        <svg width="16" height="16" fill="none" viewBox="0 0 24 24" stroke="var(--text-faint)" strokeWidth="1.7">
          <path strokeLinecap="round" strokeLinejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 0 0-3.375-3.375h-1.5A1.125 1.125 0 0 1 13.5 7.125v-1.5a3.375 3.375 0 0 0-3.375-3.375H8.25m2.25 0H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 0 0-9-9Z" />
        </svg>
      </div>
      <div className="flex-1 min-w-0">
        <p className="text-sm font-semibold text-[var(--text)] truncate group-hover:text-[var(--brand-teal)] transition-colors">
          {invoicePeriodLabel(inv)}
        </p>
        <p className="text-xs text-[var(--text-faint)] mt-0.5 font-mono">#{(inv.id ?? '').slice(0, 8) || '—'}</p>
      </div>
      <div className="text-right shrink-0">
        <p className="text-sm font-bold text-[var(--text)] tabular-nums">{fmtUsd(usdCents, 2)}</p>
        {zar && <p className="text-[10px] text-[var(--text-faint)] mt-0.5 tabular-nums">{zar}</p>}
      </div>
      <InvoiceStatus status={status} />
      <svg width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="var(--text-faint)" strokeWidth="2" className="shrink-0">
        <path strokeLinecap="round" strokeLinejoin="round" d="m8.25 4.5 7.5 7.5-7.5 7.5" />
      </svg>
    </button>
  )
}

// ── Invoice detail ──────────────────────────────────────────────────────────────

function GitEvidenceChips({ evidence }) {
  if (!evidence || typeof evidence !== 'object') return null
  const repo = evidence.repo ?? evidence.repoName ?? evidence.repo_name
  const sha = evidence.sha ?? evidence.commitSha ?? evidence.commit_sha
  const pr = evidence.pr ?? evidence.prNumber ?? evidence.pr_number
  const confirmationRequired = evidence.confirmation_required ?? evidence.confirmationRequired
  if (!repo && !sha && !pr && !confirmationRequired) return null
  return (
    <div className="flex flex-wrap gap-1.5 mt-2">
      {repo && (
        <span className="text-[10px] font-mono px-1.5 py-0.5 rounded" style={{ background: 'rgba(99,102,241,0.1)', color: '#a5b4fc', border: '1px solid rgba(99,102,241,0.2)' }}>{repo}</span>
      )}
      {sha && (
        <span className="text-[10px] font-mono px-1.5 py-0.5 rounded" style={{ background: 'rgba(45,212,191,0.1)', color: '#2DD4BF', border: '1px solid rgba(45,212,191,0.25)' }}>{String(sha).slice(0, 7)}</span>
      )}
      {pr && (
        <span className="text-[10px] font-mono px-1.5 py-0.5 rounded" style={{ background: 'rgba(129,140,248,0.1)', color: '#818cf8', border: '1px solid rgba(165,180,252,0.2)' }}>PR #{pr}</span>
      )}
    </div>
  )
}

function InvoiceLineRow({ line }) {
  const desc = line.description ?? line.desc ?? '—'
  const usdCents = line.usdCents ?? line.usd_cents ?? null
  const isEstimated = line.isEstimated ?? line.is_estimated ?? false
  const evidence = line.evidence ?? null

  return (
    <div
      className="rounded-[var(--radius-badge)] px-4 py-3"
      style={{
        background: isEstimated ? 'rgba(245,158,11,0.04)' : 'var(--bg)',
        border: isEstimated ? '1px solid rgba(245,158,11,0.18)' : '1px solid var(--border)',
        borderLeft: isEstimated ? '2px solid #f59e0b' : undefined,
      }}
    >
      <div className="flex items-start justify-between gap-4">
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-sm text-[var(--text)] font-medium">{desc}</span>
            {isEstimated && (
              <span className="text-[10px] font-bold px-2 py-0.5 rounded-full uppercase tracking-wider shrink-0"
                style={{ background: 'rgba(245,158,11,0.12)', color: '#fbbf24', border: '1px solid rgba(245,158,11,0.3)' }}>
                needs confirmation
              </span>
            )}
          </div>
          <GitEvidenceChips evidence={evidence} />
          {isEstimated && (
            <p className="text-[10px] text-[var(--text-faint)] mt-1.5">No git activity found — confirm before sending to a client.</p>
          )}
        </div>
        <span className="text-sm font-bold text-[var(--text)] shrink-0 tabular-nums">{fmtUsd(usdCents, 2)}</span>
      </div>
    </div>
  )
}

function InvoiceDetailPanel({ id, onBack }) {
  const { data: inv, loading, error, disabled } = useInvoiceDetail(id)
  const [pdfBusy, setPdfBusy] = useState(false)
  const [pdfError, setPdfError] = useState(null)

  async function downloadPdf() {
    setPdfBusy(true); setPdfError(null)
    try { await api.downloadInvoicePdf(id) }
    catch (e) { setPdfError(e?.message ?? 'Could not download PDF') }
    finally { setPdfBusy(false) }
  }

  if (disabled) return <BillingDisabled />
  if (loading) return <InvoicesSkeleton />
  if (error) return <ErrorBanner msg={error} />
  if (!inv) return null

  const status = inv.status ?? 'unknown'
  const usdCents = inv.usdCents ?? inv.usd_cents ?? null
  const zarCents = inv.zarCents ?? inv.zar_cents ?? null
  const zar = fmtZar(zarCents)
  const fxRate = inv.fxRate ?? inv.fx_rate ?? null
  const issuedAt = inv.issuedAt ?? inv.issued_at
  const lines = inv.lines ?? inv.lineItems ?? inv.line_items ?? []
  const hasEstimated = lines.some(l => l.isEstimated ?? l.is_estimated)

  return (
    <div className="space-y-6">
      <button onClick={onBack} className="flex items-center gap-1.5 text-xs text-[var(--text-muted)] hover:text-[var(--text)] transition-colors">
        <svg width="13" height="13" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
          <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 19.5 8.25 12l7.5-7.5" />
        </svg>
        All invoices
      </button>

      <Panel className="px-6 py-5">
        <div className="flex items-start justify-between gap-4 mb-5">
          <div>
            <p className="text-[11px] text-[var(--text-faint)] uppercase tracking-widest font-semibold mb-1">Invoice</p>
            <h2 className="text-xl font-bold text-[var(--text)] font-display">{invoicePeriodLabel(inv)}</h2>
            <p className="text-xs text-[var(--text-faint)] mt-1 font-mono">#{(inv.id ?? id).slice(0, 12)}</p>
            {issuedAt && <p className="text-xs text-[var(--text-faint)] mt-0.5">Issued {fmtDate(issuedAt, { month: 'long', day: 'numeric', year: 'numeric' })}</p>}
          </div>
          <div className="flex flex-col items-end gap-2 shrink-0">
            <InvoiceStatus status={status} />
            <button
              onClick={downloadPdf}
              disabled={pdfBusy}
              className="inline-flex items-center gap-1.5 rounded-[var(--radius-btn)] px-3 py-1.5 text-xs font-semibold transition-colors disabled:opacity-50 hover:brightness-110"
              style={{ border: '1px solid var(--border2)', color: 'var(--brand-teal)', background: 'var(--bg)' }}
            >
              {pdfBusy ? <Spinner size={13} /> : (
                <svg width="13" height="13" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
                  <path strokeLinecap="round" strokeLinejoin="round" d="M3 16.5v2.25A2.25 2.25 0 0 0 5.25 21h13.5A2.25 2.25 0 0 0 21 18.75V16.5M16.5 12 12 16.5m0 0L7.5 12m4.5 4.5V3" />
                </svg>
              )}
              Download PDF
            </button>
          </div>
        </div>
        {pdfError && <p className="text-xs mb-3 text-red-400">{pdfError}</p>}

        <div className="rounded-[var(--radius-badge)] p-4 space-y-2" style={{ background: 'var(--bg)', border: '1px solid var(--border)' }}>
          <div className="flex items-center justify-between text-sm">
            <span className="text-[var(--text-muted)]">Billed (USD)</span>
            <span className="font-bold text-[var(--text)] text-lg tabular-nums">{fmtUsd(usdCents, 2)}</span>
          </div>
          {zar && (
            <div className="flex items-center justify-between text-sm border-t border-[var(--border)] pt-2">
              <span className="text-[var(--text-muted)]">Charged in {CHARGE_CURRENCY}</span>
              <span className="font-bold text-[var(--brand-teal)] tabular-nums">{zar}</span>
            </div>
          )}
          {fxRate != null && (
            <div className="flex items-center justify-between text-xs border-t border-[var(--border)] pt-2">
              <span className="text-[var(--text-faint)]">FX rate at charge time</span>
              <span className="font-mono text-[var(--text-faint)]">1 USD = {CHARGE_CURRENCY} {Number(fxRate).toFixed(4)}</span>
            </div>
          )}
        </div>
      </Panel>

      {hasEstimated && (
        <div className="flex items-start gap-3 rounded-[var(--radius-card)] px-5 py-4 text-xs"
          style={{ background: 'rgba(245,158,11,0.06)', border: '1px solid rgba(245,158,11,0.2)' }}>
          <svg width="15" height="15" fill="none" viewBox="0 0 24 24" stroke="#f59e0b" strokeWidth="2" className="shrink-0 mt-0.5">
            <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126ZM12 15.75h.007v.008H12v-.008Z" />
          </svg>
          <div>
            <p className="font-semibold text-[#fbbf24] mb-0.5">Some lines need confirmation</p>
            <p className="leading-relaxed text-[var(--text-muted)]">
              Lines marked <strong className="text-[#fbbf24]">needs confirmation</strong> are estimated — no git activity backs them.
              Review before sending. gitstate only bills what git can prove.
            </p>
          </div>
        </div>
      )}

      <Panel className="p-6">
        <h3 className="text-sm font-semibold text-[var(--text)] mb-4">Line items</h3>
        {lines.length === 0 ? (
          <p className="text-xs text-[var(--text-faint)] text-center py-4">No line items on this invoice yet.</p>
        ) : (
          <div className="space-y-2">
            {lines.map((line, i) => <InvoiceLineRow key={line.id ?? i} line={line} />)}
          </div>
        )}
        {lines.length > 0 && (
          <div className="flex items-center justify-between pt-4 mt-4 border-t border-[var(--border)]">
            <span className="text-sm font-semibold text-[var(--text-muted)]">Total</span>
            <div className="text-right">
              <p className="text-base font-bold text-[var(--text)] tabular-nums">{fmtUsd(usdCents, 2)}</p>
              {zar && <p className="text-xs text-[var(--text-faint)] mt-0.5 tabular-nums">{zar}{fxRate != null ? ` @ ${Number(fxRate).toFixed(4)}` : ''}</p>}
            </div>
          </div>
        )}
      </Panel>
    </div>
  )
}

function InvoicesTab() {
  const { data: invoicesData, loading, error, disabled } = useInvoices()
  const [selectedId, setSelectedId] = useState(null)

  if (disabled) return <BillingDisabled />
  if (loading) return <InvoicesSkeleton />
  if (error) return <ErrorBanner msg={error} />
  if (selectedId) return <InvoiceDetailPanel id={selectedId} onBack={() => setSelectedId(null)} />

  const invoices = Array.isArray(invoicesData) ? invoicesData : (invoicesData?.invoices ?? [])

  if (invoices.length === 0) {
    return (
      <Panel className="px-6 py-14 text-center" style={{ borderStyle: 'dashed' }}>
        <div className="w-11 h-11 rounded-full flex items-center justify-center mx-auto mb-4" style={{ background: 'var(--bg-surface2)' }}>
          <svg width="20" height="20" fill="none" viewBox="0 0 24 24" stroke="var(--text-faint)" strokeWidth="1.6">
            <path strokeLinecap="round" strokeLinejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 0 0-3.375-3.375h-1.5A1.125 1.125 0 0 1 13.5 7.125v-1.5a3.375 3.375 0 0 0-3.375-3.375H8.25m2.25 0H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 0 0-9-9Z" />
          </svg>
        </div>
        <p className="text-sm font-semibold text-[var(--text-muted)] mb-1">No invoices yet</p>
        <p className="text-xs text-[var(--text-faint)]">Invoices appear here once your first billing period closes.</p>
      </Panel>
    )
  }

  return (
    <Reveal>
      <Panel className="overflow-hidden">
        <div className="px-4 py-3 border-b border-[var(--border)] flex items-center justify-between">
          <p className="text-xs font-semibold text-[var(--text-faint)] uppercase tracking-widest">
            {invoices.length} invoice{invoices.length !== 1 ? 's' : ''}
          </p>
          <p className="text-[10px] text-[var(--text-faint)] hidden sm:block">USD billed · {CHARGE_CURRENCY} charged</p>
        </div>
        <div className="divide-y divide-[var(--border)]">
          {invoices.map(inv => (
            <InvoiceRow key={inv.id} inv={inv} onClick={() => setSelectedId(inv.id)} />
          ))}
        </div>
      </Panel>
    </Reveal>
  )
}

// ── Paystack redirect-back handler ──────────────────────────────────────────────

function PaystackReturn() {
  const [searchParams] = useSearchParams()
  const navigate = useNavigate()
  const ref = searchParams.get('reference') ?? searchParams.get('trxref')
  const [status, setStatus] = useState(() => ref ? 'verifying' : 'error')
  const [message, setMessage] = useState(() => ref ? '' : 'No payment reference found.')

  useEffect(() => {
    if (!ref) return
    let cancelled = false
    api.get(`/api/billing/verify/${ref}`)
      .then(data => {
        if (cancelled) return
        if (data?.status === 'success' || data?.paid) {
          setStatus('success'); setMessage(data?.message ?? 'Payment confirmed — your plan is updated.')
        } else {
          setStatus('failed'); setMessage(data?.message ?? 'Payment verification failed. Please contact support.')
        }
      })
      .catch(e => { if (!cancelled) { setStatus('error'); setMessage(e?.message ?? 'Could not verify payment.') } })
    return () => { cancelled = true }
  }, [ref])

  return (
    <div className="flex flex-col items-center justify-center py-20 gap-4">
      {status === 'verifying' && (
        <><span className="text-[var(--brand-teal)]"><Spinner size={32} /></span><p className="text-sm text-[var(--text-muted)]">Verifying your payment…</p></>
      )}
      {status === 'success' && (
        <>
          <div className="w-14 h-14 rounded-full flex items-center justify-center" style={{ background: 'rgba(34,197,94,0.1)' }}>
            <svg width="28" height="28" fill="none" viewBox="0 0 24 24" stroke="#22c55e" strokeWidth="2.5"><path strokeLinecap="round" strokeLinejoin="round" d="m4.5 12.75 6 6 9-13.5" /></svg>
          </div>
          <p className="text-base font-semibold text-green-400">Payment successful</p>
          <p className="text-sm text-[var(--text-muted)] text-center max-w-sm">{message}</p>
          <button onClick={() => navigate('/settings/billing')} className="mt-2 px-5 py-2.5 rounded-[var(--radius-btn)] text-sm font-semibold text-[#0B1120]" style={{ background: 'linear-gradient(135deg, var(--brand-teal), var(--brand-indigo))' }}>View your plan</button>
        </>
      )}
      {(status === 'failed' || status === 'error') && (
        <>
          <div className="w-14 h-14 rounded-full flex items-center justify-center" style={{ background: 'rgba(239,68,68,0.1)' }}>
            <svg width="28" height="28" fill="none" viewBox="0 0 24 24" stroke="#ef4444" strokeWidth="2.5"><path strokeLinecap="round" strokeLinejoin="round" d="M6 18 18 6M6 6l12 12" /></svg>
          </div>
          <p className="text-base font-semibold text-red-400">Payment not confirmed</p>
          <p className="text-sm text-[var(--text-muted)] text-center max-w-sm">{message}</p>
          <button onClick={() => navigate('/settings/billing')} className="mt-2 px-5 py-2.5 rounded-[var(--radius-btn)] text-sm font-semibold text-[var(--text)] transition-colors" style={{ border: '1px solid var(--border)' }}>Back to billing</button>
        </>
      )}
    </div>
  )
}

// ── Root ────────────────────────────────────────────────────────────────────────

export default function Billing() {
  const [searchParams, setSearchParams] = useSearchParams()
  const hasRef = searchParams.get('reference') || searchParams.get('trxref')

  const tabParam = searchParams.get('tab') ?? 'overview'
  const valid = ['overview', 'plans', 'invoices']
  const [tab, setTab] = useState(valid.includes(tabParam) ? tabParam : 'overview')

  function handleTabChange(t) {
    setTab(t)
    setSearchParams(p => { p.set('tab', t); return p }, { replace: true })
  }

  if (hasRef) return (
    <div className="max-w-2xl mx-auto"><PaystackReturn /></div>
  )

  return (
    <div className="w-full">
      <div className="mb-7">
        <h1 className="text-2xl font-semibold text-[var(--text)] tracking-tight font-display">Billing &amp; usage</h1>
        <p className="text-sm text-[var(--text-faint)] mt-1">Your plan, real-time usage, and evidence-backed invoices.</p>
      </div>

      <TabBar active={tab} onChange={handleTabChange} />

      {tab === 'overview' && <OverviewTab onGoToPlans={() => handleTabChange('plans')} />}
      {tab === 'plans'    && <PlansTab />}
      {tab === 'invoices' && <InvoicesTab />}
    </div>
  )
}
