/**
 * Billing page — /settings/billing
 * Tabs: Plans · Usage · Invoices
 *
 * Key design decisions reflected in the UI:
 *   A8: USD price shown prominently; ZAR charge noted (exchange rate at charge time).
 *   P6: Per-builder pricing; stakeholders/clients/viewers are always free.
 *   P4: Invoice lines backed by git evidence; gaps flagged "needs confirmation".
 *
 * Billing disabled (OSS builds / cfg.Billing.Enabled=false) → graceful empty state.
 */
import { useState, useEffect } from 'react'
import { useSearchParams, useNavigate } from 'react-router-dom'
import { usePlans, useSubscription, useUsage, useInvoices, useInvoiceDetail } from '../lib/useBilling.js'
import * as api from '../lib/api.js'

// ── Constants ─────────────────────────────────────────────────────────────────

const CHARGE_CURRENCY = import.meta.env.VITE_BILLING_CHARGE_CURRENCY ?? 'ZAR'

// ── Shared primitives ─────────────────────────────────────────────────────────

function Spinner({ size = 20 }) {
  return (
    <svg className="animate-spin" width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="var(--brand-teal)" strokeWidth="2">
      <path d="M21 12a9 9 0 1 1-6.219-8.56" strokeLinecap="round" />
    </svg>
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
    <div
      className="rounded-[var(--radius-card)] px-6 py-10 text-center"
      style={{ background: 'var(--bg-surface)', border: '1px dashed var(--border)' }}
    >
      <div className="w-10 h-10 rounded-full flex items-center justify-center mx-auto mb-4" style={{ background: 'var(--bg-surface2)' }}>
        <svg width="18" height="18" fill="none" viewBox="0 0 24 24" stroke="var(--text-faint)" strokeWidth="1.8">
          <path strokeLinecap="round" strokeLinejoin="round" d="M2.25 8.25h19.5M2.25 9h19.5m-16.5 5.25h6m-6 2.25h3m-3.75 3h15a2.25 2.25 0 0 0 2.25-2.25V6.75A2.25 2.25 0 0 0 19.5 4.5h-15a2.25 2.25 0 0 0-2.25 2.25v10.5A2.25 2.25 0 0 0 4.5 19.5Z" />
        </svg>
      </div>
      <p className="text-sm font-semibold text-[var(--text-muted)] mb-1">Billing not enabled on this instance</p>
      <p className="text-xs text-[var(--text-faint)]">This is an OSS build or billing is not configured. Upgrade to the cloud edition to manage plans and invoices.</p>
    </div>
  )
}

function LoadingCenter() {
  return (
    <div className="flex items-center justify-center py-16 gap-3">
      <Spinner />
      <span className="text-sm text-[var(--text-faint)]">Loading…</span>
    </div>
  )
}

// ── Tab bar ───────────────────────────────────────────────────────────────────

const TABS = [
  { id: 'plans',    label: 'Plans' },
  { id: 'usage',    label: 'Usage' },
  { id: 'invoices', label: 'Invoices' },
]

function TabBar({ active, onChange }) {
  return (
    <div
      className="flex gap-1 p-1 rounded-[var(--radius-btn)] mb-8"
      style={{ background: 'var(--bg)', border: '1px solid var(--border)' }}
    >
      {TABS.map(t => (
        <button
          key={t.id}
          onClick={() => onChange(t.id)}
          className={[
            'flex-1 text-xs font-semibold py-2 px-4 rounded-[var(--radius-badge)] transition-all duration-150',
            active === t.id
              ? 'bg-[var(--bg-surface2)] text-[var(--brand-teal)]'
              : 'text-[var(--text-muted)] hover:text-[var(--text)]',
          ].join(' ')}
        >
          {t.label}
        </button>
      ))}
    </div>
  )
}

// ── Currency helpers ──────────────────────────────────────────────────────────

function fmtUsd(cents) {
  if (cents == null) return '—'
  return new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', minimumFractionDigits: 0 }).format(cents / 100)
}

function fmtZar(cents) {
  if (cents == null) return '—'
  return new Intl.NumberFormat('en-ZA', { style: 'currency', currency: 'ZAR', minimumFractionDigits: 2 }).format(cents / 100)
}

function fmtRate(rate) {
  if (rate == null) return '—'
  return `1 USD = ${CHARGE_CURRENCY} ${Number(rate).toFixed(4)}`
}

// ── Plan ladder ───────────────────────────────────────────────────────────────

const PLAN_ORDER = ['free', 'hobby', 'pro', 'team', 'scale', 'ent']

const PLAN_ACCENTS = {
  free:  { border: 'var(--border2)', badge: '#334155',  text: 'var(--text-muted)' },
  hobby: { border: '#6366F1', badge: '#4f46e5',  text: '#a5b4fc' },
  pro:   { border: '#2DD4BF', badge: '#0d9488',  text: '#2DD4BF' },
  team:  { border: '#f59e0b', badge: '#d97706',  text: '#fbbf24' },
  scale: { border: '#ec4899', badge: '#db2777',  text: '#f9a8d4' },
  ent:   { border: '#6366F1', badge: '#4f46e5',  text: '#818cf8' },
}

function PlanBadge({ planKey, isCurrent }) {
  const accent = PLAN_ACCENTS[planKey] ?? PLAN_ACCENTS.free
  if (!isCurrent) return null
  return (
    <span
      className="absolute top-4 right-4 text-[10px] font-mono font-bold px-2 py-0.5 rounded-full uppercase tracking-widest"
      style={{ background: `${accent.badge}22`, color: accent.text, border: `1px solid ${accent.border}55` }}
    >
      Current plan
    </span>
  )
}

function FeatureLine({ icon, text, dim }) {
  return (
    <div className={`flex items-start gap-2 text-xs ${dim ? 'text-[var(--text-faint)]' : 'text-[var(--text-muted)]'}`}>
      <span className="mt-0.5 shrink-0">{icon}</span>
      <span>{text}</span>
    </div>
  )
}

const CheckIcon = (
  <svg width="13" height="13" fill="none" viewBox="0 0 24 24" stroke="#2DD4BF" strokeWidth="2.5">
    <path strokeLinecap="round" strokeLinejoin="round" d="m4.5 12.75 6 6 9-13.5" />
  </svg>
)

const DashIcon = (
  <svg width="13" height="13" fill="none" viewBox="0 0 24 24" stroke="var(--text-faint)" strokeWidth="2">
    <path strokeLinecap="round" strokeLinejoin="round" d="M5 12h14" />
  </svg>
)

function PlanCard({ plan, isCurrent, onUpgrade, upgrading }) {
  const key = plan.key ?? plan.planKey ?? ''
  const accent = PLAN_ACCENTS[key] ?? PLAN_ACCENTS.free
  const priceCents = plan.priceCents ?? plan.price_cents ?? 0
  const isFree = priceCents === 0
  const builderLimit = plan.builderLimit ?? plan.builder_limit ?? null
  const storageGb = plan.storageGb ?? plan.storage_gb ?? null
  const features = plan.features ?? []
  const popular = key === 'pro'

  return (
    <div
      className="relative flex flex-col rounded-[var(--radius-card)] p-6 transition-all duration-200"
      style={{
        background: isCurrent ? 'rgba(45,212,191,0.03)' : 'var(--bg-surface)',
        border: `1px solid ${isCurrent ? accent.border : (popular ? '#6366F180' : 'var(--border)')}`,
        boxShadow: popular ? '0 0 0 1px #6366F130' : undefined,
      }}
    >
      {popular && !isCurrent && (
        <div className="absolute -top-3 left-1/2 -translate-x-1/2">
          <span
            className="text-[10px] font-bold px-3 py-1 rounded-full uppercase tracking-widest"
            style={{ background: 'linear-gradient(90deg,#6366F1,#2DD4BF)', color: '#fff' }}
          >
            Most popular
          </span>
        </div>
      )}

      <PlanBadge planKey={key} isCurrent={isCurrent} />

      {/* Plan name + price */}
      <div className="mb-5">
        <h3
          className="text-sm font-bold uppercase tracking-widest mb-3"
          style={{ color: accent.text }}
        >
          {plan.name ?? key}
        </h3>

        <div className="flex items-end gap-2">
          {isFree ? (
            <span className="text-3xl font-extrabold text-[var(--text)]">Free</span>
          ) : (
            <>
              <span className="text-3xl font-extrabold text-[var(--text)]">
                {fmtUsd(priceCents)}
              </span>
              <span className="text-sm text-[var(--text-faint)] mb-1">/mo</span>
            </>
          )}
        </div>

        {/* USD billed / ZAR charged note */}
        {!isFree && (
          <div
            className="mt-2 flex items-center gap-1.5 text-[10px] rounded-md px-2 py-1 w-fit"
            style={{ background: 'rgba(45,212,191,0.06)', border: '1px solid rgba(45,212,191,0.15)', color: '#5eead4' }}
          >
            <svg width="10" height="10" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
              <path strokeLinecap="round" strokeLinejoin="round" d="M12 6v6h4.5m4.5 0a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z" />
            </svg>
            Billed in USD · charged in {CHARGE_CURRENCY} at current rate
          </div>
        )}
      </div>

      {/* Builder seats + stakeholders free */}
      <div
        className="rounded-[var(--radius-badge)] p-3 mb-5 space-y-2"
        style={{ background: 'var(--bg)', border: '1px solid var(--border)' }}
      >
        <div className="flex items-center justify-between text-xs">
          <span className="text-[var(--text-muted)] font-medium">Builder seats</span>
          <span className="font-bold text-[var(--text)]">
            {builderLimit == null || builderLimit === 0 ? 'Unlimited' : builderLimit}
          </span>
        </div>
        <div className="flex items-center justify-between text-xs">
          <span className="text-[var(--text-muted)] font-medium">Stakeholder seats</span>
          <span className="font-bold text-[var(--brand-teal)]">Always free</span>
        </div>
        {storageGb && (
          <div className="flex items-center justify-between text-xs">
            <span className="text-[var(--text-muted)] font-medium">Storage</span>
            <span className="font-bold text-[var(--text)]">{storageGb} GB</span>
          </div>
        )}
      </div>

      {/* Per-builder pricing callout */}
      <div
        className="text-[10px] font-semibold mb-5 px-2 py-1.5 rounded-md text-center"
        style={{ background: 'rgba(99,102,241,0.07)', color: '#818cf8', border: '1px solid rgba(99,102,241,0.15)' }}
      >
        Per-builder pricing — clients &amp; viewers never billed
      </div>

      {/* Feature list */}
      <div className="flex-1 space-y-2 mb-6">
        {features.map((f, i) => (
          <FeatureLine key={i} icon={CheckIcon} text={f} />
        ))}
        {features.length === 0 && (
          <>
            <FeatureLine icon={CheckIcon} text="Git-derived project state" />
            <FeatureLine icon={CheckIcon} text="Evidence-backed invoicing" />
            {isFree && <FeatureLine icon={DashIcon} text="Paystack billing" dim />}
          </>
        )}
      </div>

      {/* CTA */}
      {isCurrent ? (
        <div
          className="w-full py-2.5 rounded-[var(--radius-btn)] text-xs font-semibold text-center"
          style={{ background: `${accent.border}18`, color: accent.text, border: `1px solid ${accent.border}40` }}
        >
          Current plan
        </div>
      ) : isFree ? (
        <div
          className="w-full py-2.5 rounded-[var(--radius-btn)] text-xs font-semibold text-center text-[var(--text-faint)]"
          style={{ border: '1px solid var(--border)' }}
        >
          Downgrade
        </div>
      ) : (
        <button
          onClick={() => onUpgrade(key)}
          disabled={upgrading}
          className="w-full py-2.5 rounded-[var(--radius-btn)] text-xs font-semibold text-white transition-all duration-150 disabled:opacity-50 flex items-center justify-center gap-2"
          style={{ background: `linear-gradient(135deg, ${accent.badge}, ${accent.border})` }}
        >
          {upgrading ? <Spinner size={14} /> : null}
          Upgrade to {plan.name ?? key}
        </button>
      )}
    </div>
  )
}

// ── Plans tab ─────────────────────────────────────────────────────────────────

function PlansTab() {
  const { data: plans, loading, error, disabled } = usePlans()
  const { data: sub } = useSubscription()
  const [upgrading, setUpgrading] = useState(null)
  const [upgradeError, setUpgradeError] = useState(null)

  async function handleUpgrade(planKey) {
    setUpgrading(planKey)
    setUpgradeError(null)
    try {
      const result = await api.post('/api/billing/checkout', { plan: planKey })
      if (result?.authorization_url) {
        window.location.href = result.authorization_url
      } else if (result?.url) {
        window.location.href = result.url
      }
    } catch (e) {
      setUpgradeError(e.message ?? 'Checkout failed. Please try again.')
      setUpgrading(null)
    }
  }

  if (disabled) return <BillingDisabled />
  if (loading) return <LoadingCenter />
  if (error) return <ErrorBanner msg={error} />

  const planList = Array.isArray(plans) ? plans : (plans?.plans ?? [])
  const sorted = [...planList].sort((a, b) => {
    const ai = PLAN_ORDER.indexOf(a.key ?? a.planKey ?? '')
    const bi = PLAN_ORDER.indexOf(b.key ?? b.planKey ?? '')
    return (ai < 0 ? 99 : ai) - (bi < 0 ? 99 : bi)
  })
  const currentPlanKey = sub?.planKey ?? sub?.plan_key ?? sub?.plan ?? 'free'

  return (
    <div>
      {/* Header callout: stakeholders free */}
      <div
        className="rounded-[var(--radius-card)] px-5 py-4 mb-8 flex items-start gap-4"
        style={{ background: 'linear-gradient(135deg, rgba(45,212,191,0.05), rgba(99,102,241,0.05))', border: '1px solid rgba(45,212,191,0.15)' }}
      >
        <div
          className="shrink-0 w-9 h-9 rounded-full flex items-center justify-center"
          style={{ background: 'rgba(45,212,191,0.1)' }}
        >
          <svg width="17" height="17" fill="none" viewBox="0 0 24 24" stroke="var(--brand-teal)" strokeWidth="2">
            <path strokeLinecap="round" strokeLinejoin="round" d="M15 19.128a9.38 9.38 0 0 0 2.625.372 9.337 9.337 0 0 0 4.121-.952 4.125 4.125 0 0 0-7.533-2.493M15 19.128v-.003c0-1.113-.285-2.16-.786-3.07M15 19.128v.106A12.318 12.318 0 0 1 8.624 21c-2.331 0-4.512-.645-6.374-1.766l-.001-.109a6.375 6.375 0 0 1 11.964-3.07M12 6.375a3.375 3.375 0 1 1-6.75 0 3.375 3.375 0 0 1 6.75 0Zm8.25 2.25a2.625 2.625 0 1 1-5.25 0 2.625 2.625 0 0 1 5.25 0Z" />
          </svg>
        </div>
        <div>
          <p className="text-sm font-semibold text-[var(--text)] mb-0.5">Clients and stakeholders are always free</p>
          <p className="text-xs text-[var(--text-muted)] leading-relaxed">
            Pricing is per <strong className="text-[var(--text)]">builder seat</strong> (devs, PMs, anyone who creates or manages work).
            Stakeholders, clients, and read-only viewers are <strong className="text-[var(--brand-teal)]">never counted toward your bill</strong> — invite
            as many as you need. That&apos;s the wedge incumbents can&apos;t match.
          </p>
        </div>
      </div>

      {/* FX notice */}
      <div
        className="flex items-center gap-2 mb-6 text-xs text-[var(--text-faint)]"
        style={{ borderLeft: '2px solid var(--border)', paddingLeft: '12px' }}
      >
        <svg width="12" height="12" fill="none" viewBox="0 0 24 24" stroke="var(--text-faint)" strokeWidth="2">
          <path strokeLinecap="round" strokeLinejoin="round" d="m11.25 11.25.041-.02a.75.75 0 0 1 1.063.852l-.708 2.836a.75.75 0 0 0 1.063.853l.041-.021M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Zm-9-3.75h.008v.008H12V8.25Z" />
        </svg>
        All prices shown in <strong className="text-[var(--text-muted)] ml-1">USD</strong>.
        <span className="mx-1">Your card is charged in</span>
        <strong className="text-[var(--text-muted)]">{CHARGE_CURRENCY}</strong>
        <span className="ml-1">using the exchange rate captured at payment time (stored on your invoice).</span>
      </div>

      {upgradeError && <ErrorBanner msg={upgradeError} />}

      {/* Plan grid */}
      {sorted.length === 0 ? (
        <div className="text-sm text-[var(--text-muted)] text-center py-8">No plan data returned. Check billing configuration.</div>
      ) : (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
          {sorted.map(plan => (
            <PlanCard
              key={plan.key ?? plan.planKey ?? plan.id}
              plan={plan}
              isCurrent={(plan.key ?? plan.planKey) === currentPlanKey}
              onUpgrade={handleUpgrade}
              upgrading={upgrading === (plan.key ?? plan.planKey)}
            />
          ))}
        </div>
      )}
    </div>
  )
}

// ── Usage tab ─────────────────────────────────────────────────────────────────

function UsageBar({ label, used, limit, accent = 'var(--brand-teal)', sub }) {
  const pct = limit ? Math.min(100, Math.round((used / limit) * 100)) : 0
  const isOver = limit && used > limit
  const barColor = isOver ? '#ef4444' : accent

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between text-xs">
        <span className="font-medium text-[var(--text-muted)]">{label}</span>
        <span className="font-mono text-[var(--text-faint)]">
          {used ?? 0}
          {limit ? <span> / {limit}</span> : null}
          {!limit && <span className="ml-1">used</span>}
        </span>
      </div>
      {limit ? (
        <div className="h-2 rounded-full overflow-hidden" style={{ background: 'var(--border)' }}>
          <div
            className="h-full rounded-full transition-all duration-700"
            style={{ width: `${pct}%`, background: barColor }}
          />
        </div>
      ) : null}
      {sub && <p className="text-[10px] text-[var(--text-faint)]">{sub}</p>}
      {isOver && (
        <p className="text-[10px] text-red-400 font-semibold">Over limit — upgrade to continue</p>
      )}
    </div>
  )
}

function UsageTab() {
  const { data: usage, loading, error, disabled } = useUsage()
  const { data: sub } = useSubscription()

  if (disabled) return <BillingDisabled />
  if (loading) return <LoadingCenter />
  if (error) return <ErrorBanner msg={error} />

  const planName = sub?.planName ?? sub?.plan_name ?? sub?.plan ?? 'Free'
  const periodStart = sub?.periodStart ?? sub?.period_start
  const periodEnd   = sub?.periodEnd   ?? sub?.period_end

  const builderUsed  = usage?.buildersUsed  ?? usage?.builders_used  ?? 0
  const builderLimit = usage?.builderLimit  ?? usage?.builder_limit  ?? null
  const storageUsed  = usage?.storageGbUsed ?? usage?.storage_gb_used ?? null
  const storageLimit = usage?.storageGbLimit ?? usage?.storage_gb_limit ?? null
  const apiCalls     = usage?.apiCallsUsed  ?? usage?.api_calls_used  ?? null
  const events       = usage?.usageEvents   ?? usage?.usage_events    ?? []

  function fmtDate(d) {
    if (!d) return '—'
    return new Date(d).toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' })
  }

  return (
    <div className="space-y-8">
      {/* Period header */}
      <div
        className="rounded-[var(--radius-card)] px-6 py-5 flex items-center justify-between"
        style={{ background: 'var(--bg-surface)', border: '1px solid var(--border)' }}
      >
        <div>
          <p className="text-xs text-[var(--text-faint)] uppercase tracking-widest font-semibold mb-1">Current billing period</p>
          <p className="text-sm font-semibold text-[var(--text)]">
            {fmtDate(periodStart)} — {fmtDate(periodEnd)}
          </p>
        </div>
        <div
          className="text-xs font-bold px-3 py-1.5 rounded-full uppercase tracking-wider"
          style={{ background: 'rgba(45,212,191,0.1)', color: 'var(--brand-teal)', border: '1px solid rgba(45,212,191,0.25)' }}
        >
          {planName}
        </div>
      </div>

      {/* Usage meters */}
      <div
        className="rounded-[var(--radius-card)] p-6 space-y-6"
        style={{ background: 'var(--bg-surface)', border: '1px solid var(--border)' }}
      >
        <h3 className="text-sm font-semibold text-[var(--text)]">Seat usage</h3>

        <UsageBar
          label="Builder seats"
          used={builderUsed}
          limit={builderLimit}
          accent="#6366F1"
          sub="Stakeholders, clients, and viewers are always free and not counted here."
        />

        {storageUsed != null && (
          <UsageBar
            label="Storage"
            used={`${storageUsed.toFixed(1)} GB`}
            limit={storageLimit ? `${storageLimit} GB` : null}
            accent="#2DD4BF"
          />
        )}

        {apiCalls != null && (
          <UsageBar
            label="API calls"
            used={apiCalls}
            limit={null}
            accent="#f59e0b"
            sub="Usage events this period"
          />
        )}
      </div>

      {/* Stakeholders-free reminder */}
      <div
        className="flex items-center gap-3 rounded-[var(--radius-card)] px-5 py-4 text-xs text-[var(--text-muted)]"
        style={{ background: 'rgba(45,212,191,0.04)', border: '1px solid rgba(45,212,191,0.12)' }}
      >
        <svg width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="var(--brand-teal)" strokeWidth="2">
          <path strokeLinecap="round" strokeLinejoin="round" d="M9 12.75 11.25 15 15 9.75M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z" />
        </svg>
        <span>
          <strong className="text-[var(--text)]">Stakeholders, clients, and viewers are free</strong> — only builders (devs, PMs, and admins) count toward your seat limit.
        </span>
      </div>

      {/* Recent usage events */}
      {Array.isArray(events) && events.length > 0 && (
        <div
          className="rounded-[var(--radius-card)] p-6"
          style={{ background: 'var(--bg-surface)', border: '1px solid var(--border)' }}
        >
          <h3 className="text-sm font-semibold text-[var(--text)] mb-4">Recent usage events</h3>
          <div className="space-y-2">
            {events.slice(0, 20).map((ev, i) => (
              <div key={i} className="flex items-center justify-between py-2 border-b border-[var(--border)] last:border-0 text-xs">
                <span className="text-[var(--text-muted)]">{ev.type ?? ev.event_type ?? '—'}</span>
                <span className="font-mono text-[var(--text)]">{ev.quantity ?? ev.value ?? 1}</span>
                <span className="text-[var(--text-faint)]">
                  {ev.occurredAt ?? ev.occurred_at ? new Date(ev.occurredAt ?? ev.occurred_at).toLocaleDateString() : '—'}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

// ── Invoices tab ──────────────────────────────────────────────────────────────

function InvoiceStatus({ status }) {
  const map = {
    paid:    { bg: 'rgba(34,197,94,0.08)',   border: 'rgba(34,197,94,0.25)',   text: '#22c55e',  label: 'Paid' },
    open:    { bg: 'rgba(245,158,11,0.08)',  border: 'rgba(245,158,11,0.25)',  text: '#f59e0b',  label: 'Open' },
    draft:   { bg: 'rgba(71,85,105,0.15)',   border: 'rgba(71,85,105,0.3)',    text: '#64748b',  label: 'Draft' },
    void:    { bg: 'rgba(239,68,68,0.08)',   border: 'rgba(239,68,68,0.2)',    text: '#ef4444',  label: 'Void' },
    overdue: { bg: 'rgba(239,68,68,0.1)',    border: 'rgba(239,68,68,0.3)',    text: '#ef4444',  label: 'Overdue' },
  }
  const s = map[status?.toLowerCase()] ?? { bg: 'rgba(71,85,105,0.1)', border: '#1e2d45', text: '#64748b', label: status ?? 'Unknown' }
  return (
    <span
      className="text-[10px] font-semibold px-2 py-0.5 rounded-full uppercase tracking-wide"
      style={{ background: s.bg, border: `1px solid ${s.border}`, color: s.text }}
    >
      {s.label}
    </span>
  )
}

function InvoiceRow({ inv, onClick }) {
  const number   = inv.number ?? inv.invoice_number ?? inv.id ?? '—'
  const period   = inv.periodLabel ?? inv.period_label ?? inv.period ?? '—'
  const usdCents = inv.totalUsdCents ?? inv.total_usd_cents ?? inv.totalCents ?? null
  const zarCents = inv.totalZarCents ?? inv.total_zar_cents ?? null
  const status   = inv.status ?? 'unknown'
  const date     = inv.issuedAt ?? inv.issued_at ?? inv.createdAt ?? inv.created_at

  return (
    <button
      onClick={onClick}
      className="w-full flex items-center gap-4 px-4 py-3.5 rounded-[var(--radius-badge)] hover:bg-[var(--bg-surface2)] transition-colors text-left group"
    >
      <div className="shrink-0">
        <svg width="16" height="16" fill="none" viewBox="0 0 24 24" stroke="var(--text-faint)" strokeWidth="1.8">
          <path strokeLinecap="round" strokeLinejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 0 0-3.375-3.375h-1.5A1.125 1.125 0 0 1 13.5 7.125v-1.5a3.375 3.375 0 0 0-3.375-3.375H8.25m0 12.75h7.5m-7.5 3H12M10.5 2.25H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 0 0-9-9Z" />
        </svg>
      </div>
      <div className="flex-1 min-w-0">
        <p className="text-sm font-semibold text-[var(--text)] truncate group-hover:text-[var(--brand-teal)] transition-colors">
          Invoice #{number}
        </p>
        <p className="text-xs text-[var(--text-faint)] mt-0.5">{period} {date ? `· ${new Date(date).toLocaleDateString()}` : ''}</p>
      </div>
      <div className="text-right shrink-0">
        <p className="text-sm font-bold text-[var(--text)]">{fmtUsd(usdCents)}</p>
        {zarCents != null && (
          <p className="text-[10px] text-[var(--text-faint)] mt-0.5">{fmtZar(zarCents)}</p>
        )}
      </div>
      <InvoiceStatus status={status} />
      <svg width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="var(--text-faint)" strokeWidth="2" className="shrink-0">
        <path strokeLinecap="round" strokeLinejoin="round" d="m8.25 4.5 7.5 7.5-7.5 7.5" />
      </svg>
    </button>
  )
}

// ── Invoice detail view ───────────────────────────────────────────────────────

function GitEvidenceChip({ evidence }) {
  if (!evidence) return null
  const { commitSha, prNumber, repoName } = evidence
  const displaySha = commitSha ? commitSha.slice(0, 7) : null
  return (
    <div className="flex flex-wrap gap-1.5 mt-1.5">
      {repoName && (
        <span
          className="text-[10px] font-mono px-1.5 py-0.5 rounded"
          style={{ background: 'rgba(99,102,241,0.08)', color: '#6366F1', border: '1px solid rgba(99,102,241,0.2)' }}
        >
          {repoName}
        </span>
      )}
      {displaySha && (
        <span
          className="text-[10px] font-mono px-1.5 py-0.5 rounded"
          style={{ background: 'rgba(45,212,191,0.1)', color: '#0d9488', border: '1px solid rgba(45,212,191,0.25)' }}
        >
          {displaySha}
        </span>
      )}
      {prNumber && (
        <span
          className="text-[10px] font-mono px-1.5 py-0.5 rounded"
          style={{ background: 'rgba(129,140,248,0.1)', color: '#6366F1', border: '1px solid rgba(165,180,252,0.2)' }}
        >
          PR #{prNumber}
        </span>
      )}
    </div>
  )
}

function InvoiceLineRow({ line }) {
  const desc        = line.description ?? line.desc ?? '—'
  const unitsCents  = line.unitPriceCents ?? line.unit_price_cents ?? null
  const qty         = line.quantity ?? 1
  const totalCents  = line.totalCents ?? line.total_cents ?? (unitsCents != null ? unitsCents * qty : null)
  const isEstimated = line.estimated ?? line.is_estimated ?? false
  const evidence    = line.evidence ?? line.gitEvidence ?? line.git_evidence ?? null
  const evidenceObj = typeof evidence === 'object' ? evidence : null
  const evidenceStr = typeof evidence === 'string' ? evidence : null

  return (
    <div
      className={[
        'rounded-[var(--radius-badge)] px-4 py-3 space-y-1',
        isEstimated ? 'border-l-2' : '',
      ].join(' ')}
      style={{
        background: isEstimated ? 'rgba(245,158,11,0.04)' : 'var(--bg-surface3)',
        border: isEstimated ? '1px solid rgba(245,158,11,0.18)' : '1px solid var(--border)',
        borderLeftColor: isEstimated ? '#f59e0b' : undefined,
      }}
    >
      <div className="flex items-start justify-between gap-4">
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-sm text-[var(--text)] font-medium">{desc}</span>
            {isEstimated && (
              <span
                className="text-[10px] font-bold px-2 py-0.5 rounded-full uppercase tracking-wider shrink-0"
                style={{ background: 'rgba(245,158,11,0.12)', color: '#f59e0b', border: '1px solid rgba(245,158,11,0.3)' }}
              >
                needs confirmation
              </span>
            )}
          </div>
          {qty !== 1 && unitsCents != null && (
            <p className="text-[11px] text-[var(--text-faint)] mt-0.5">
              {qty} × {fmtUsd(unitsCents)}
            </p>
          )}
        </div>
        <div className="text-right shrink-0">
          <span className="text-sm font-bold text-[var(--text)]">{fmtUsd(totalCents)}</span>
        </div>
      </div>

      {/* Git evidence */}
      {evidenceObj && <GitEvidenceChip evidence={evidenceObj} />}
      {evidenceStr && (
        <p className="text-[10px] font-mono text-[var(--text-faint)] mt-1">{evidenceStr}</p>
      )}

      {isEstimated && !evidence && (
        <p className="text-[10px] text-[var(--text-muted)] mt-1">
          No git activity detected — this line needs manual confirmation before sending to client.
        </p>
      )}
    </div>
  )
}

function InvoiceDetailPanel({ id, onBack }) {
  const { data: inv, loading, error, disabled } = useInvoiceDetail(id)

  if (disabled) return <BillingDisabled />
  if (loading) return <LoadingCenter />
  if (error) return <ErrorBanner msg={error} />
  if (!inv) return null

  const number    = inv.number ?? inv.invoice_number ?? id
  const period    = inv.periodLabel ?? inv.period_label ?? inv.period ?? '—'
  const status    = inv.status ?? 'unknown'
  const usdCents  = inv.totalUsdCents ?? inv.total_usd_cents ?? inv.totalCents ?? null
  const zarCents  = inv.totalZarCents ?? inv.total_zar_cents ?? null
  const fxRate    = inv.fxRate ?? inv.fx_rate ?? inv.exchangeRate ?? inv.exchange_rate ?? null
  const issuedAt  = inv.issuedAt ?? inv.issued_at
  const lines     = inv.lines ?? inv.lineItems ?? inv.line_items ?? []
  const hasEstimated = lines.some(l => l.estimated ?? l.is_estimated)

  return (
    <div className="space-y-6">
      {/* Back */}
      <button
        onClick={onBack}
        className="flex items-center gap-1.5 text-xs text-[var(--text-muted)] hover:text-[var(--text)] transition-colors"
      >
        <svg width="13" height="13" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
          <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 19.5 8.25 12l7.5-7.5" />
        </svg>
        Back to invoices
      </button>

      {/* Invoice header */}
      <div
        className="rounded-[var(--radius-card)] px-6 py-5"
        style={{ background: 'var(--bg-surface)', border: '1px solid var(--border)' }}
      >
        <div className="flex items-start justify-between gap-4 mb-4">
          <div>
            <p className="text-xs text-[var(--text-faint)] uppercase tracking-widest font-semibold mb-1">Invoice</p>
            <h2 className="text-xl font-bold text-[var(--text)] font-display">#{number}</h2>
            <p className="text-xs text-[var(--text-muted)] mt-1">{period}</p>
            {issuedAt && (
              <p className="text-xs text-[var(--text-faint)] mt-0.5">Issued {new Date(issuedAt).toLocaleDateString('en-US', { month: 'long', day: 'numeric', year: 'numeric' })}</p>
            )}
          </div>
          <InvoiceStatus status={status} />
        </div>

        {/* Amounts: USD billed + ZAR charged + FX rate */}
        <div
          className="rounded-[var(--radius-badge)] p-4 space-y-2"
          style={{ background: 'var(--bg)', border: '1px solid var(--border)' }}
        >
          <div className="flex items-center justify-between text-sm">
            <span className="text-[var(--text-muted)]">Billed amount (USD)</span>
            <span className="font-bold text-[var(--text)] text-lg">{fmtUsd(usdCents)}</span>
          </div>
          {zarCents != null && (
            <div className="flex items-center justify-between text-sm border-t border-[var(--border)] pt-2">
              <span className="text-[var(--text-muted)]">Charged in {CHARGE_CURRENCY}</span>
              <span className="font-bold text-[var(--brand-teal)]">{fmtZar(zarCents)}</span>
            </div>
          )}
          {fxRate && (
            <div className="flex items-center justify-between text-xs border-t border-[var(--border)] pt-2">
              <span className="text-[var(--text-faint)]">Exchange rate at charge time</span>
              <span className="font-mono text-[var(--text-faint)]">{fmtRate(fxRate)}</span>
            </div>
          )}
        </div>
      </div>

      {/* Estimated lines notice */}
      {hasEstimated && (
        <div
          className="flex items-start gap-3 rounded-xl px-5 py-4 text-xs"
          style={{ background: 'rgba(245,158,11,0.06)', border: '1px solid rgba(245,158,11,0.2)' }}
        >
          <svg width="15" height="15" fill="none" viewBox="0 0 24 24" stroke="#f59e0b" strokeWidth="2" className="shrink-0 mt-0.5">
            <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126ZM12 15.75h.007v.008H12v-.008Z" />
          </svg>
          <div>
            <p className="font-semibold text-[#fbbf24] mb-0.5">Some lines need confirmation</p>
            <p className="text-[#92400e] leading-relaxed" style={{ color: '#b45309' }}>
              Lines marked <strong style={{ color: '#f59e0b' }}>needs confirmation</strong> are estimated — no git activity was found to back them.
              Review these before sending the invoice to your client. gitstate will only count what git can prove.
            </p>
          </div>
        </div>
      )}

      {/* Line items */}
      <div
        className="rounded-[var(--radius-card)] p-6"
        style={{ background: 'var(--bg-surface)', border: '1px solid var(--border)' }}
      >
        <h3 className="text-sm font-semibold text-[var(--text)] mb-4">Line items</h3>

        {lines.length === 0 ? (
          <p className="text-xs text-[var(--text-faint)] text-center py-4">No line items on this invoice yet.</p>
        ) : (
          <div className="space-y-2">
            {lines.map((line, i) => (
              <InvoiceLineRow key={line.id ?? i} line={line} />
            ))}
          </div>
        )}

        {/* Total row */}
        {lines.length > 0 && (
          <div className="flex items-center justify-between pt-4 mt-4 border-t border-[var(--border)]">
            <span className="text-sm font-semibold text-[var(--text-muted)]">Total</span>
            <div className="text-right">
              <p className="text-base font-bold text-[var(--text)]">{fmtUsd(usdCents)}</p>
              {zarCents != null && (
                <p className="text-xs text-[var(--text-faint)] mt-0.5">
                  {fmtZar(zarCents)}
                  {fxRate ? ` at ${fmtRate(fxRate)}` : ''}
                </p>
              )}
            </div>
          </div>
        )}
      </div>

      {/* Git evidence legend */}
      <div className="text-[10px] text-[var(--text-faint)] flex flex-wrap gap-x-4 gap-y-1">
        <span className="flex items-center gap-1.5">
          <span className="w-2 h-2 rounded-sm" style={{ background: '#2DD4BF' }} />
          Commit SHA (git-verified)
        </span>
        <span className="flex items-center gap-1.5">
          <span className="w-2 h-2 rounded-sm" style={{ background: '#6366F1' }} />
          Repository
        </span>
        <span className="flex items-center gap-1.5">
          <span className="w-2 h-2 rounded-sm" style={{ background: '#a5b4fc' }} />
          Pull request
        </span>
        <span className="flex items-center gap-1.5">
          <span className="w-2 h-2 rounded-full border border-[#f59e0b]" />
          Needs confirmation (no git evidence)
        </span>
      </div>
    </div>
  )
}

function InvoicesTab() {
  const { data: invoicesData, loading, error, disabled } = useInvoices()
  const [selectedId, setSelectedId] = useState(null)

  if (disabled) return <BillingDisabled />
  if (loading) return <LoadingCenter />
  if (error) return <ErrorBanner msg={error} />

  if (selectedId) {
    return <InvoiceDetailPanel id={selectedId} onBack={() => setSelectedId(null)} />
  }

  const invoices = Array.isArray(invoicesData) ? invoicesData : (invoicesData?.invoices ?? [])

  if (invoices.length === 0) {
    return (
      <div
        className="rounded-[var(--radius-card)] px-6 py-12 text-center"
        style={{ background: 'var(--bg-surface)', border: '1px dashed var(--border)' }}
      >
        <div className="w-10 h-10 rounded-full flex items-center justify-center mx-auto mb-4" style={{ background: 'var(--bg-surface2)' }}>
          <svg width="18" height="18" fill="none" viewBox="0 0 24 24" stroke="var(--text-faint)" strokeWidth="1.8">
            <path strokeLinecap="round" strokeLinejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 0 0-3.375-3.375h-1.5A1.125 1.125 0 0 1 13.5 7.125v-1.5a3.375 3.375 0 0 0-3.375-3.375H8.25m0 12.75h7.5m-7.5 3H12M10.5 2.25H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 0 0-9-9Z" />
          </svg>
        </div>
        <p className="text-sm font-semibold text-[var(--text-muted)] mb-1">No invoices yet</p>
        <p className="text-xs text-[var(--text-faint)]">Invoices will appear here once your first billing period closes.</p>
      </div>
    )
  }

  return (
    <div>
      <div
        className="rounded-[var(--radius-card)] overflow-hidden"
        style={{ background: 'var(--bg-surface)', border: '1px solid var(--border)' }}
      >
        <div className="px-4 py-3 border-b border-[var(--border)]">
          <p className="text-xs font-semibold text-[var(--text-faint)] uppercase tracking-widest">
            {invoices.length} invoice{invoices.length !== 1 ? 's' : ''}
          </p>
        </div>
        <div className="divide-y divide-[var(--border)]">
          {invoices.map(inv => (
            <InvoiceRow
              key={inv.id ?? inv.number}
              inv={inv}
              onClick={() => setSelectedId(inv.id)}
            />
          ))}
        </div>
      </div>
      <p className="text-[10px] text-[var(--text-faint)] mt-3 text-center">
        Each invoice shows the USD amount billed and the {CHARGE_CURRENCY} amount charged, with the exchange rate captured at payment time.
      </p>
    </div>
  )
}

// ── Paystack return handler ───────────────────────────────────────────────────

function PaystackReturn() {
  const [searchParams] = useSearchParams()
  const navigate = useNavigate()
  const ref = searchParams.get('reference') ?? searchParams.get('trxref')

  // Derive initial state: if there's no ref we already know the outcome
  const [status, setStatus] = useState(() => ref ? 'verifying' : 'error')
  const [message, setMessage] = useState(() => ref ? '' : 'No payment reference found.')

  useEffect(() => {
    if (!ref) return
    let cancelled = false
    api.get(`/api/billing/verify/${ref}`)
      .then(data => {
        if (cancelled) return
        if (data?.status === 'success' || data?.paid) {
          setStatus('success')
          setMessage(data?.message ?? 'Payment confirmed! Your plan has been upgraded.')
        } else {
          setStatus('failed')
          setMessage(data?.message ?? 'Payment verification failed. Please contact support.')
        }
      })
      .catch(e => {
        if (cancelled) return
        setStatus('error')
        setMessage(e.message ?? 'Could not verify payment.')
      })
    return () => { cancelled = true }
  }, [ref])

  return (
    <div className="flex flex-col items-center justify-center py-20 gap-4">
      {status === 'verifying' && (
        <>
          <Spinner size={32} />
          <p className="text-sm text-[var(--text-muted)]">Verifying your payment…</p>
        </>
      )}
      {status === 'success' && (
        <>
          <div className="w-14 h-14 rounded-full flex items-center justify-center" style={{ background: 'rgba(34,197,94,0.1)' }}>
            <svg width="28" height="28" fill="none" viewBox="0 0 24 24" stroke="#22c55e" strokeWidth="2.5">
              <path strokeLinecap="round" strokeLinejoin="round" d="m4.5 12.75 6 6 9-13.5" />
            </svg>
          </div>
          <p className="text-base font-semibold text-green-400">Payment successful</p>
          <p className="text-sm text-[var(--text-muted)] text-center max-w-sm">{message}</p>
          <button
            onClick={() => navigate('/settings/billing')}
            className="mt-2 px-5 py-2.5 rounded-[var(--radius-btn)] text-sm font-semibold text-[#0B1120]"
            style={{ background: 'linear-gradient(135deg, var(--brand-teal), var(--brand-indigo))' }}
          >
            View your plan
          </button>
        </>
      )}
      {(status === 'failed' || status === 'error') && (
        <>
          <div className="w-14 h-14 rounded-full flex items-center justify-center" style={{ background: 'rgba(239,68,68,0.1)' }}>
            <svg width="28" height="28" fill="none" viewBox="0 0 24 24" stroke="#ef4444" strokeWidth="2.5">
              <path strokeLinecap="round" strokeLinejoin="round" d="M6 18 18 6M6 6l12 12" />
            </svg>
          </div>
          <p className="text-base font-semibold text-red-400">Payment not confirmed</p>
          <p className="text-sm text-[var(--text-muted)] text-center max-w-sm">{message}</p>
          <button
            onClick={() => navigate('/settings/billing')}
            className="mt-2 px-5 py-2.5 rounded-[var(--radius-btn)] text-sm font-semibold text-[var(--text)] border border-[var(--border)] hover:border-[var(--brand-teal)]/40 transition-colors"
          >
            Back to billing
          </button>
        </>
      )}
    </div>
  )
}

// ── Root billing page ─────────────────────────────────────────────────────────

export default function Billing() {
  const [searchParams, setSearchParams] = useSearchParams()

  // Detect Paystack redirect-back
  const hasRef = searchParams.get('reference') || searchParams.get('trxref')

  const tabParam = searchParams.get('tab') ?? 'plans'
  const [tab, setTab] = useState(['plans', 'usage', 'invoices'].includes(tabParam) ? tabParam : 'plans')

  function handleTabChange(t) {
    setTab(t)
    setSearchParams(p => { p.set('tab', t); return p }, { replace: true })
  }

  if (hasRef) return (
    <div className="max-w-2xl mx-auto">
      <PaystackReturn />
    </div>
  )

  return (
    <div className="w-full">
      <div className="mb-8">
        <h1 className="text-2xl font-semibold text-[var(--text)] tracking-tight font-display">Billing</h1>
        <p className="text-sm text-[var(--text-faint)] mt-1">Plan, usage, and evidence-backed invoices.</p>
      </div>

      <TabBar active={tab} onChange={handleTabChange} />

      {tab === 'plans'    && <PlansTab />}
      {tab === 'usage'    && <UsageTab />}
      {tab === 'invoices' && <InvoicesTab />}
    </div>
  )
}
