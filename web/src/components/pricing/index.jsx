/**
 * Pricing page helpers — "The Ledger" aesthetic.
 *
 * Per-builder plan cards and a plan comparison matrix. (The interactive cost
 * calculator now lives in components/compare/CompetitorCalculator.jsx — the
 * single shared head-to-head calculator used on both /pricing and /compare.)
 * Backend GET /api/plans returns:
 *   [{ key, name, perBuilderUsd, includedLlmUsd, overageMarkup, builders }]
 * where builders=0 means unlimited; free tier caps at 2.
 *
 * Note on `overageMarkup`: INTERNAL bulk/committed-use field only — it sizes the
 * estimate math but is never surfaced. Managed AI is presented to clients as
 * metered at the model's standard rate (no per-seat AI fee, no visible markup).
 *
 * Only consumed by pages/Pricing.jsx.
 */
import {
  Check, Minus, Users, Eye, KeyRound, Sparkles, ArrowRight,
  Infinity as InfinityIcon, Cpu, Gauge, Server, Zap, ShieldCheck,
  DollarSign, CreditCard, Info,
} from 'lucide-react'
import { Card, Button, Pill, Glow } from '../ui'

// ── Enterprise detection ──────────────────────────────────────────────────────
// The live API may key enterprise as 'enterprise' (fallback uses 'ent') and a
// backend agent is making it return perBuilderUsd as null (it currently returns
// 0). Treat the enterprise key OR a null/0 per-builder price on a non-free plan
// as a "Custom" tier — defensively handling BOTH null and 0.
function isEnterprisePlan(plan) {
  if (!plan) return false
  if (plan.key === 'enterprise' || plan.key === 'ent') return true
  // null price is always custom; a 0 price is custom unless it's the free tier.
  if (plan.perBuilderUsd == null) return true
  if (plan.perBuilderUsd === 0 && plan.key !== 'free') return true
  return false
}

// ── Plan metadata (icon + tagline per tier) ─────────────────────────────────
const PLAN_META = {
  free:       { icon: Zap,         tagline: 'Solo builders & side projects — forever free' },
  team:       { icon: Users,       tagline: 'Small teams shipping daily, included LLM credits' },
  business:   { icon: ShieldCheck, tagline: 'Growing orgs — more credits, SSO & audit logs' },
  ent:        { icon: Cpu,         tagline: 'Self-host, BYOK, custom SLA & compliance' },
  enterprise: { icon: Cpu,         tagline: 'Self-host, BYOK, custom SLA & compliance' },
}

const PLAN_FEATURES = {
  free: [
    '≤ 2 builder seats',
    'Unlimited stakeholders — free',
    'BYOK LLM (bring your own key)',
    'Community support',
    'Unlimited repos',
  ],
  team: [
    'Unlimited builder seats',
    'Unlimited stakeholders — free',
    '$3 / builder managed-AI credit included',
    'Any model at its standard rate · or BYOK',
    'Priority email support',
    'Unlimited repos',
  ],
  business: [
    'Unlimited builder seats',
    'Unlimited stakeholders — free',
    '$6 / builder managed-AI credit included',
    'Any model at its standard rate · or BYOK',
    'Google / Microsoft SSO + audit logs',
    'Dedicated Slack support',
    'Unlimited repos',
  ],
  ent: [
    'Unlimited builder seats',
    'Unlimited stakeholders — free',
    'BYOK — bring your own LLM key',
    'Self-host on your infra',
    'Custom SLA + compliance',
    'Dedicated CSM',
    'Unlimited repos',
  ],
}
// Live API keys enterprise as 'enterprise'; mirror the 'ent' feature/tagline set.
PLAN_FEATURES.enterprise = PLAN_FEATURES.ent

// ── Plan card ────────────────────────────────────────────────────────────────
export function PlanCard({ plan, recommended, format }) {
  const isEnt = isEnterprisePlan(plan)
  const isFree = !isEnt && (plan.key === 'free' || plan.perBuilderUsd === 0)
  const meta = PLAN_META[plan.key] ?? PLAN_META.team
  const Icon = meta.icon
  const tagline = meta.tagline
  const features = PLAN_FEATURES[plan.key] ?? []

  return (
    <Card
      padding="lg"
      glow={recommended}
      className={[
        'group relative flex flex-col gap-5 transition-all duration-300',
        recommended
          ? 'border-[#2DD4BF]/45 shadow-[0_0_0_1px_rgba(45,212,191,0.25),0_8px_40px_rgba(45,212,191,0.12),0_24px_64px_rgba(0,0,0,0.35)] lg:-translate-y-2'
          : 'hover:border-[var(--border2)] hover:-translate-y-1 hover:shadow-[var(--shadow-card-hover)]',
      ].join(' ')}
    >
      {recommended && (
        <>
          {/* top accent rail */}
          <span
            aria-hidden
            className="absolute inset-x-0 top-0 h-px"
            style={{ background: 'linear-gradient(90deg, transparent, #2DD4BF, #6366F1, transparent)' }}
          />
          <Glow variant="teal" size={260} className="-top-8 right-0 opacity-50 group-hover:opacity-70 transition-opacity" />
          <div className="absolute -top-3 left-1/2 -translate-x-1/2 z-10">
            <span
              className="inline-flex items-center gap-1 px-2.5 py-0.5 rounded-full text-[10px] font-mono font-semibold uppercase tracking-wider text-[#0B1120] shadow-[0_4px_14px_rgba(45,212,191,0.4)]"
              style={{ background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' }}
            >
              <Sparkles size={11} strokeWidth={2.5} /> Recommended
            </span>
          </div>
        </>
      )}

      {/* Header */}
      <div className="flex items-start justify-between gap-2 relative z-[1]">
        <div className="flex items-start gap-3">
          <div
            className="mt-0.5 w-9 h-9 shrink-0 rounded-[var(--radius-badge)] flex items-center justify-center border"
            style={{
              background: recommended ? 'rgba(45,212,191,0.12)' : 'var(--bg-surface3)',
              borderColor: recommended ? 'rgba(45,212,191,0.3)' : 'var(--border)',
            }}
          >
            <Icon size={17} className={recommended ? 'text-[#2DD4BF]' : 'text-[var(--text-muted)]'} strokeWidth={1.8} />
          </div>
          <div>
            <h3 className="font-display text-lg font-semibold text-[var(--text)] leading-none">{plan.name}</h3>
            <p className="text-[11px] text-[var(--text-faint)] mt-1.5">{tagline}</p>
          </div>
        </div>
        {plan.key !== 'free' && !isEnt && (
          <Pill color={recommended ? 'teal' : 'default'}>{plan.key}</Pill>
        )}
      </div>

      {/* Price */}
      <div className="relative z-[1]">
        {isEnt ? (
          <div className="flex flex-col gap-1">
            <span className="font-display text-[2rem] font-semibold text-[var(--text)] leading-none">Custom</span>
            <span className="text-xs text-[var(--text-faint)] mt-1.5">Self-host or negotiated cloud SLA</span>
          </div>
        ) : (
          <div className="flex flex-col gap-1.5">
            <div className="flex items-baseline gap-1.5">
              <span className="font-display text-[2.4rem] font-semibold text-[var(--text)] leading-none tabular-nums">
                {format(plan.perBuilderUsd)}
              </span>
              <span className="text-sm text-[var(--text-muted)]">/ builder / mo</span>
            </div>
            {isFree ? (
              <span className="text-xs text-[var(--text-faint)]">Free forever — no card required · ≤ 2 builders</span>
            ) : (
              <div className="flex flex-col gap-1.5 mt-1">
                <span className="text-xs text-[#2DD4BF] flex items-center gap-1 font-medium">
                  <Sparkles size={11} className="shrink-0" strokeWidth={2.2} />
                  Managed — AI included, no per-seat AI tax
                </span>
                {/* Managed vs BYOK */}
                {typeof plan.byokPerBuilderUsd === 'number' && plan.byokPerBuilderUsd > 0 && (
                  <div className="inline-flex items-center gap-1.5 rounded-[var(--radius-badge)] border border-[#818cf8]/25 bg-[#6366F1]/[0.07] px-2 py-1 self-start">
                    <KeyRound size={11} className="text-[#818cf8] shrink-0" strokeWidth={2.2} />
                    <span className="text-[11px] text-[var(--text-dim)]">
                      or <span className="font-mono font-semibold text-[#818cf8] tabular-nums">{format(plan.byokPerBuilderUsd)}</span>/builder with your own AI key (BYOK)
                    </span>
                  </div>
                )}
                <span className="text-xs text-[var(--text-faint)] flex items-center gap-1">
                  <DollarSign size={11} className="text-[#2DD4BF] shrink-0" />
                  Managed: {format(plan.includedLlmUsd)} AI credit / builder included
                </span>
                <span className="text-xs text-[var(--text-faint)] flex items-center gap-1">
                  <CreditCard size={11} className="text-[var(--text-faint)] shrink-0" />
                  Beyond it, any model at its standard rate · BYOK = pay your provider direct
                </span>
                <span className="text-[10px] text-[var(--text-faint)]/80 leading-snug flex items-start gap-1 mt-0.5">
                  <Info size={10} className="text-[var(--text-faint)] shrink-0 mt-0.5" />
                  <span>BYOK = bring your own provider key; we drop the included-AI value and route calls direct to your provider.</span>
                </span>
              </div>
            )}
          </div>
        )}
      </div>

      {/* Stakeholders always free badge */}
      {!isEnt && (
        <div className="relative z-[1] flex items-center gap-2 rounded-[var(--radius-badge)] border border-[#6366F1]/20 bg-[#6366F1]/[0.06] px-3 py-2">
          <Eye size={13} className="text-[#818cf8] shrink-0" />
          <span className="text-xs text-[var(--text-dim)]">
            <span className="font-semibold text-[#818cf8]">Unlimited stakeholders</span> — always free
          </span>
        </div>
      )}

      {/* CTA */}
      <Button
        variant={recommended ? 'primary' : 'outline'}
        size="md"
        className="w-full relative z-[1]"
        rightIcon={!isEnt ? <ArrowRight size={15} /> : undefined}
      >
        {isEnt ? 'Talk to sales' : isFree ? 'Get started free' : 'Start free trial'}
      </Button>

      {/* Feature list */}
      <ul className="flex flex-col gap-2.5 pt-4 mt-auto border-t border-[var(--border)] relative z-[1]">
        {features.map(feat => {
          const isStakeholder = feat.startsWith('Unlimited stakeholders')
          const isByok = feat.toLowerCase().includes('byok') || feat.toLowerCase().includes('bring your own')
          return (
            <li key={feat} className="flex items-start gap-2.5 text-[13px] text-[var(--text-dim)]">
              <span
                className="mt-px shrink-0 w-4 h-4 rounded-full flex items-center justify-center"
                style={{
                  background: isStakeholder
                    ? 'rgba(99,102,241,0.14)'
                    : isByok
                    ? 'rgba(99,102,241,0.10)'
                    : 'rgba(45,212,191,0.12)',
                }}
              >
                {isStakeholder
                  ? <InfinityIcon size={11} className="text-[#818cf8]" strokeWidth={2.5} />
                  : isByok
                  ? <KeyRound size={10} className="text-[#818cf8]" strokeWidth={2.5} />
                  : <Check size={11} className="text-[#2DD4BF]" strokeWidth={3} />}
              </span>
              <span className={isStakeholder ? 'text-[var(--text)]' : ''}>{feat}</span>
            </li>
          )
        })}
      </ul>
    </Card>
  )
}

// ── Comparison matrix ────────────────────────────────────────────────────────
const COMPARE_ROWS = [
  { label: 'Per-builder price (managed · AI included)', icon: DollarSign, vals: ['$0', '$6', '$14', 'Custom'] },
  { label: 'Per-builder price (BYOK)',      icon: KeyRound,     vals: ['BYOK', '$3', '$8', 'Custom'] },
  { label: 'Builder cap',                   icon: Users,        vals: ['≤ 2', '∞', '∞', '∞'] },
  { label: 'Stakeholders',                  icon: Eye,          vals: ['∞', '∞', '∞', '∞'], accent: true },
  { label: 'Managed AI credit / builder',   icon: Cpu,          vals: ['—', '$3', '$6', 'BYOK'] },
  { label: 'Managed AI beyond credit',      icon: Gauge,        vals: ['BYOK', 'At model cost', 'At model cost', 'BYOK'], accent: true },
  { label: 'Per-seat AI fee',               icon: DollarSign,   vals: ['None', 'None', 'None', 'None'], accent: true },
  { label: 'BYOK (own LLM key)',            icon: KeyRound,     vals: [true, true, true, true] },
  { label: 'Unlimited repos',               icon: Server,       vals: [true, true, true, true] },
  { label: 'Priority support',              icon: Zap,          vals: [false, true, true, true] },
  { label: 'Org SSO + audit logs',          icon: ShieldCheck,  vals: [false, false, true, true] },
  { label: 'Self-host / on-premise',        icon: Server,       vals: [false, false, false, true] },
  { label: 'Custom SLA',                    icon: ShieldCheck,  vals: [false, false, false, true] },
]

export function CompareTable({ columns, recommendedKey }) {
  return (
    <Card padding="none" className="overflow-hidden border-[var(--border2)]">
      <div className="overflow-x-auto">
        <table className="w-full border-collapse text-sm min-w-[560px]">
          <thead>
            <tr className="border-b border-[var(--border)]">
              <th className="text-left font-normal px-5 py-4 text-[11px] font-mono uppercase tracking-widest text-[var(--text-faint)]">
                Feature
              </th>
              {columns.map(col => {
                const rec = col.key === recommendedKey
                return (
                  <th
                    key={col.key}
                    className="px-4 py-4 text-center"
                    style={rec ? { background: 'rgba(45,212,191,0.05)' } : undefined}
                  >
                    <span className={['font-display text-sm font-semibold', rec ? 'text-[#2DD4BF]' : 'text-[var(--text)]'].join(' ')}>
                      {col.name}
                    </span>
                  </th>
                )
              })}
            </tr>
          </thead>
          <tbody>
            {COMPARE_ROWS.map(row => {
              const Icon = row.icon
              return (
                <tr key={row.label} className="border-b border-[var(--border)] last:border-b-0 hover:bg-[var(--bg-surface2)]/40 transition-colors">
                  <td className="px-5 py-3.5">
                    <span className="flex items-center gap-2.5 text-[var(--text-dim)]">
                      <Icon size={14} className="text-[var(--text-faint)] shrink-0" strokeWidth={1.8} />
                      {row.label}
                    </span>
                  </td>
                  {row.vals.map((v, ci) => {
                    const rec = columns[ci]?.key === recommendedKey
                    return (
                      <td
                        key={ci}
                        className="px-4 py-3.5 text-center"
                        style={rec ? { background: 'rgba(45,212,191,0.04)' } : undefined}
                      >
                        {typeof v === 'boolean'
                          ? (v
                            ? <Check size={16} className="inline text-[#2DD4BF]" strokeWidth={2.5} />
                            : <Minus size={14} className="inline text-[var(--text-faint)]/50" />)
                          : (
                            <span className={[
                              'font-mono text-sm tabular-nums',
                              row.accent ? 'text-[#818cf8]' : 'text-[var(--text-dim)]',
                            ].join(' ')}>{v}</span>
                          )}
                      </td>
                    )
                  })}
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </Card>
  )
}
