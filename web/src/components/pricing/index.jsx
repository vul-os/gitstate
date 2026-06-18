/**
 * Pricing page helpers — "The Ledger" aesthetic.
 *
 * Per-builder plan cards, a per-builder cost calculator, and a comparison
 * matrix. Backend GET /api/plans returns:
 *   [{ key, name, perBuilderUsd, includedLlmUsd, overageMarkup, builders }]
 * where builders=0 means unlimited; free tier caps at 2.
 *
 * Only consumed by pages/Pricing.jsx.
 */
import { useState } from 'react'
import {
  Check, Minus, Plus, Users, Eye, KeyRound, Sparkles, ArrowRight,
  Infinity as InfinityIcon, Cpu, Calculator, Gauge, Server, Zap, ShieldCheck,
  DollarSign, CreditCard, Info,
} from 'lucide-react'
import { Card, Button, Badge, Pill, Glow } from '../ui'

// ── Plan metadata (icon + tagline per tier) ─────────────────────────────────
const PLAN_META = {
  free:     { icon: Zap,         tagline: 'Solo builders & side projects — forever free' },
  team:     { icon: Users,       tagline: 'Small teams shipping daily, included LLM credits' },
  business: { icon: ShieldCheck, tagline: 'Growing orgs — more credits, SSO & audit logs' },
  ent:      { icon: Cpu,         tagline: 'Self-host, BYOK, custom SLA & compliance' },
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
    '$4 / builder managed-LLM credits included',
    'Overage at cost × 1.30 or BYOK',
    'Priority email support',
    'Unlimited repos',
  ],
  business: [
    'Unlimited builder seats',
    'Unlimited stakeholders — free',
    '$12 / builder managed-LLM credits included',
    'Overage at cost × 1.30 or BYOK',
    'Org SSO + audit logs',
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

// ── Plan card ────────────────────────────────────────────────────────────────
export function PlanCard({ plan, recommended, format }) {
  const isEnt = plan.perBuilderUsd === null
  const isFree = plan.perBuilderUsd === 0
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
        {plan.key !== 'free' && plan.key !== 'ent' && (
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
              <div className="flex flex-col gap-1 mt-1">
                <span className="text-xs text-[var(--text-faint)] flex items-center gap-1">
                  <DollarSign size={11} className="text-[#2DD4BF] shrink-0" />
                  {format(plan.includedLlmUsd)} managed-LLM credit / builder included
                </span>
                <span className="text-xs text-[var(--text-faint)] flex items-center gap-1">
                  <CreditCard size={11} className="text-[var(--text-faint)] shrink-0" />
                  Overage at cost × {plan.overageMarkup} or BYOK (free)
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

// ── Stepper input ─────────────────────────────────────────────────────────────
function Stepper({ value, min, max, step = 1, onChange }) {
  const dec = () => onChange(Math.max(min, value - step))
  const inc = () => onChange(Math.min(max, value + step))
  return (
    <div className="inline-flex items-center rounded-[var(--radius-btn)] border border-[var(--border2)] bg-[var(--bg-surface)] overflow-hidden">
      <button
        type="button"
        onClick={dec}
        disabled={value <= min}
        aria-label="Decrease"
        className="w-9 h-9 flex items-center justify-center text-[var(--text-muted)] hover:text-[var(--text)] hover:bg-[var(--bg-surface2)] transition-colors disabled:opacity-30 disabled:pointer-events-none cursor-pointer"
      >
        <Minus size={15} strokeWidth={2.2} />
      </button>
      <span className="w-12 text-center font-mono text-sm font-semibold text-[var(--text)] tabular-nums border-x border-[var(--border)]">
        {value}
      </span>
      <button
        type="button"
        onClick={inc}
        disabled={value >= max}
        aria-label="Increase"
        className="w-9 h-9 flex items-center justify-center text-[var(--text-muted)] hover:text-[var(--text)] hover:bg-[var(--bg-surface2)] transition-colors disabled:opacity-30 disabled:pointer-events-none cursor-pointer"
      >
        <Plus size={15} strokeWidth={2.2} />
      </button>
    </div>
  )
}

// ── Styled range slider ───────────────────────────────────────────────────────
function FillSlider({ value, min, max, step = 1, onChange, accent = '#2DD4BF' }) {
  const pct = ((value - min) / (max - min)) * 100
  return (
    <input
      type="range"
      min={min}
      max={max}
      step={step}
      value={value}
      onChange={e => onChange(Number(e.target.value))}
      className="w-full h-1.5 rounded-full appearance-none cursor-pointer outline-none
        [&::-webkit-slider-thumb]:appearance-none [&::-webkit-slider-thumb]:w-4 [&::-webkit-slider-thumb]:h-4
        [&::-webkit-slider-thumb]:rounded-full [&::-webkit-slider-thumb]:bg-white
        [&::-webkit-slider-thumb]:border-2 [&::-webkit-slider-thumb]:border-[var(--slider-accent)]
        [&::-webkit-slider-thumb]:shadow-[0_2px_8px_rgba(0,0,0,0.4)] [&::-webkit-slider-thumb]:transition-transform
        [&::-webkit-slider-thumb]:hover:scale-110
        [&::-moz-range-thumb]:w-4 [&::-moz-range-thumb]:h-4 [&::-moz-range-thumb]:rounded-full
        [&::-moz-range-thumb]:bg-white [&::-moz-range-thumb]:border-2 [&::-moz-range-thumb]:border-solid
        [&::-moz-range-thumb]:border-[var(--slider-accent)]"
      style={{
        background: `linear-gradient(90deg, ${accent} ${pct}%, var(--bg-surface3) ${pct}%)`,
        '--slider-accent': accent,
      }}
    />
  )
}

/**
 * Best-fit plan by builder count.
 * - free: ≤2 builders (builders=2)
 * - team/business: unlimited (builders=0) → sorted ascending by perBuilderUsd
 * - ent (perBuilderUsd=null): excluded from auto-match; shown when no fit found
 */
function pickPlan(builderCount, plans) {
  const pricedPlans = plans.filter(p => p.perBuilderUsd !== null)
  const sorted = [...pricedPlans].sort((a, b) => a.perBuilderUsd - b.perBuilderUsd)
  const fit = sorted.find(p => p.builders === 0 || p.builders >= builderCount)
  if (!fit) return plans.find(p => p.perBuilderUsd === null) ?? null
  return fit
}

// ── Cost calculator ──────────────────────────────────────────────────────────
export function CostCalculator({ plans, format, currency, recommendedKey }) {
  const [builders, setBuilders] = useState(4)
  const [llmPerBuilder, setLlmPerBuilder] = useState(8)
  const [byok, setByok] = useState(false)

  const matched = pickPlan(builders, plans)
  const isEnt = matched?.perBuilderUsd === null
  const isFree = matched?.perBuilderUsd === 0

  // Plan seat cost
  const planCost = isEnt ? null : (matched?.perBuilderUsd ?? 0) * builders

  // Included LLM credits per builder
  const includedLlmPerBuilder = matched?.includedLlmUsd ?? 0
  const totalIncluded = includedLlmPerBuilder * builders
  const totalLlmDesired = llmPerBuilder * builders

  // Overage computation
  const markup = matched?.overageMarkup ?? 1.3
  const overageBase = Math.max(0, totalLlmDesired - totalIncluded)
  // Free plan has no included credits so entire LLM is a cost; others pay overage × markup
  const llmCost = byok ? 0 : (isFree ? totalLlmDesired : overageBase * markup)

  const totalUsd = planCost === null ? null : planCost + llmCost
  const effectivePerBuilder = totalUsd !== null && builders > 0 ? totalUsd / builders : null

  const isRec = matched?.key === recommendedKey
  // How much switching to BYOK would save (overage markup savings)
  const byokSaving = !byok && overageBase > 0 ? overageBase * (markup - 1) : 0

  return (
    <Card padding="none" glow className="relative overflow-hidden border-[var(--border2)]">
      <Glow variant="brand" size={620} className="-top-20 left-1/3 opacity-50" />

      <div className="relative z-10 grid grid-cols-1 lg:grid-cols-[1.1fr_0.9fr]">
        {/* ── Inputs ── */}
        <div className="p-6 md:p-8 flex flex-col gap-7 border-b lg:border-b-0 lg:border-r border-[var(--border)]">
          <div className="flex items-center gap-3">
            <div className="w-9 h-9 rounded-[var(--radius-badge)] bg-[#2DD4BF]/10 border border-[#2DD4BF]/25 flex items-center justify-center">
              <Calculator size={17} className="text-[#2DD4BF]" strokeWidth={1.8} />
            </div>
            <div>
              <h3 className="font-display text-base font-semibold text-[var(--text)] leading-none">Cost calculator</h3>
              <p className="text-[11px] text-[var(--text-faint)] mt-1">Math runs client-side — nothing leaves your browser</p>
            </div>
          </div>

          {/* Builders */}
          <Field
            icon={<Users size={13} />}
            label="Builders"
            hint={
              <span>
                People who push code, run agents & manage repos.{' '}
                <span className="text-[#818cf8]">Only builders cost — stakeholders are always free.</span>
              </span>
            }
            control={<Stepper value={builders} min={1} max={100} onChange={setBuilders} />}
          >
            <FillSlider value={builders} min={1} max={100} onChange={setBuilders} accent="#2DD4BF" />
          </Field>

          {/* Stakeholders callout */}
          <div className="flex items-center gap-2.5 rounded-[var(--radius-badge)] border border-[#6366F1]/20 bg-[#6366F1]/[0.05] px-3.5 py-2.5">
            <Eye size={14} className="text-[#818cf8] shrink-0" />
            <p className="text-xs text-[var(--text-muted)]">
              <span className="font-semibold text-[#818cf8]">Stakeholders are free</span> — PMs, execs, clients, designers.
              Only count the people who actually push code.
            </p>
          </div>

          {/* LLM usage per builder */}
          <Field
            icon={<Cpu size={13} />}
            label="Est. managed LLM / builder / mo"
            hint={
              <span>
                Token spend on AI features. Your plan includes{' '}
                <span className="text-[#2DD4BF] font-semibold">
                  {format(matched?.includedLlmUsd ?? 0)} / builder
                </span>{' '}
                — overage at cost × {markup} or use BYOK.
              </span>
            }
            control={
              <span className="font-mono text-sm font-semibold text-[var(--text)] tabular-nums">
                {format(llmPerBuilder)}<span className="text-[var(--text-faint)] font-normal text-xs"> /builder</span>
              </span>
            }
          >
            <FillSlider value={llmPerBuilder} min={0} max={50} step={1} onChange={setLlmPerBuilder} accent="#6366F1" />
          </Field>

          {/* BYOK toggle */}
          <div className="flex items-center justify-between pt-1">
            <div className="flex items-center gap-2">
              <KeyRound size={13} className={byok ? 'text-[#818cf8]' : 'text-[var(--text-faint)]'} />
              <span className="text-xs font-mono uppercase tracking-widest text-[var(--text-faint)]">
                BYOK (bring your own LLM key)
              </span>
            </div>
            <ByokToggle byok={byok} setByok={setByok} />
          </div>
        </div>

        {/* ── Output ── */}
        <div className="p-6 md:p-8 flex flex-col gap-4 bg-[var(--bg-surface)]/40">
          <div className="flex items-center justify-between">
            <span className="text-[11px] font-mono uppercase tracking-widest text-[var(--text-faint)]">Your estimate</span>
            {matched && (
              <Badge color={isRec ? 'teal' : 'default'}>
                {matched.name} plan
              </Badge>
            )}
          </div>

          {/* Big total */}
          <div
            className="rounded-[var(--radius-card)] border p-5 relative overflow-hidden"
            style={{
              borderColor: isRec ? 'rgba(45,212,191,0.3)' : 'var(--border2)',
              background: 'var(--bg-surface2)',
            }}
          >
            {isRec && <Glow variant="teal" size={200} className="top-0 right-0 opacity-40" />}
            {totalUsd !== null ? (
              <div className="relative z-[1] flex flex-col gap-1">
                <div className="flex items-baseline gap-1.5">
                  <span className="font-display text-4xl font-semibold text-[var(--text)] tabular-nums leading-none">
                    {format(totalUsd)}
                  </span>
                  <span className="text-sm text-[var(--text-muted)]">/ mo</span>
                </div>
                {effectivePerBuilder !== null && (
                  <p className="text-xs text-[var(--text-faint)] mt-1">
                    {format(effectivePerBuilder)} effective per builder
                  </p>
                )}
              </div>
            ) : (
              <div className="relative z-[1] flex flex-col gap-1">
                <span className="font-display text-3xl font-semibold text-[var(--text)] leading-none">Custom</span>
                <p className="text-xs text-[var(--text-faint)]">Contact sales for volume & compliance pricing.</p>
              </div>
            )}
          </div>

          {/* Breakdown rows */}
          <div className="flex flex-col divide-y divide-[var(--border)] rounded-[var(--radius-card)] border border-[var(--border)] bg-[var(--bg-surface)] text-[13px]">
            <Row
              label={<><Users size={13} className="text-[#2DD4BF]" /> {builders} builder{builders !== 1 ? 's' : ''} × {format(matched?.perBuilderUsd ?? 0)}</>}
              value={planCost === null ? 'Custom' : format(planCost)}
            />
            <Row
              label={<><Eye size={13} className="text-[#818cf8]" /> Stakeholders (unlimited)</>}
              value={<span className="text-[#818cf8]">Always free</span>}
            />
            <Row
              label={<><DollarSign size={13} className="text-[#2DD4BF]" /> Included LLM credits</>}
              value={<span className="text-[#2DD4BF]">{format(totalIncluded)} / mo</span>}
            />
            <Row
              label={<><Cpu size={13} className="text-[var(--text-muted)]" /> LLM overage / BYOK</>}
              value={
                byok
                  ? <span className="text-[#818cf8]">BYOK · direct</span>
                  : llmCost > 0
                  ? format(llmCost)
                  : <span className="text-[#2DD4BF]">Covered by credits</span>
              }
            />
          </div>

          {/* BYOK / overage hint */}
          {llmPerBuilder > 0 && !byok && (
            <div
              className="flex items-start gap-2.5 rounded-[var(--radius-badge)] border px-3.5 py-3 text-xs leading-relaxed"
              style={{
                borderColor: byokSaving > 0 ? 'rgba(99,102,241,0.3)' : 'rgba(45,212,191,0.2)',
                background: byokSaving > 0 ? 'rgba(99,102,241,0.07)' : 'rgba(45,212,191,0.05)',
              }}
            >
              {byokSaving > 0 ? (
                <>
                  <KeyRound size={14} className="text-[#818cf8] shrink-0 mt-0.5" />
                  <span className="text-[var(--text-dim)]">
                    Enable <strong className="text-[#818cf8]">BYOK</strong> to skip the ×{markup} markup — save ~{format(byokSaving)}/mo
                    by routing calls direct to your provider.
                  </span>
                </>
              ) : (
                <>
                  <Info size={14} className="text-[#2DD4BF] shrink-0 mt-0.5" />
                  <span className="text-[var(--text-faint)]">
                    Your LLM usage fits within the{' '}
                    <span className="text-[#2DD4BF]">{format(totalIncluded)}/mo</span> included credits — no overage charge.
                  </span>
                </>
              )}
            </div>
          )}

          {byok && (
            <div className="flex items-start gap-2.5 rounded-[var(--radius-badge)] border border-[#818cf8]/25 bg-[#6366F1]/[0.07] px-3.5 py-3 text-xs leading-relaxed">
              <KeyRound size={14} className="text-[#818cf8] shrink-0 mt-0.5" />
              <span className="text-[var(--text-dim)]">
                <strong className="text-[#818cf8]">BYOK active</strong> — LLM calls go direct to your provider.
                No managed-LLM markup. You pay your provider at their rate.
              </span>
            </div>
          )}

          <p className="text-[11px] text-[var(--text-faint)] leading-relaxed mt-auto">
            Billed in <span className="font-mono text-[var(--text-muted)]">USD</span> · charged in{' '}
            <span className="font-mono text-[var(--text-muted)]">{currency.code}</span> at the live rate at checkout.
          </p>
        </div>
      </div>
    </Card>
  )
}

function Field({ icon, label, hint, control, children }) {
  return (
    <div className="flex flex-col gap-2.5">
      <div className="flex items-center justify-between gap-3">
        <label className="flex items-center gap-2 text-xs font-mono uppercase tracking-widest text-[var(--text-muted)]">
          <span className="text-[var(--text-faint)]">{icon}</span>
          {label}
        </label>
        {control}
      </div>
      {children}
      <p className="text-[11px] text-[var(--text-faint)]">{hint}</p>
    </div>
  )
}

function Row({ label, value }) {
  return (
    <div className="flex items-center justify-between px-4 py-2.5">
      <span className="flex items-center gap-2 text-[var(--text-muted)]">{label}</span>
      <span className="font-mono tabular-nums text-[var(--text-dim)]">{value}</span>
    </div>
  )
}

function ByokToggle({ byok, setByok }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={byok}
      onClick={() => setByok(b => !b)}
      className={[
        'relative w-11 h-6 rounded-full border transition-all duration-200 cursor-pointer focus-visible:outline-2 focus-visible:outline-[#2DD4BF]',
        byok
          ? 'border-[#818cf8]/50 bg-[#6366F1]/20'
          : 'border-[var(--border2)] bg-[var(--bg-surface3)]',
      ].join(' ')}
    >
      <span
        className={[
          'absolute top-0.5 left-0.5 w-5 h-5 rounded-full transition-all duration-200 shadow-sm',
          byok ? 'translate-x-5 bg-[#818cf8]' : 'translate-x-0 bg-[var(--text-faint)]',
        ].join(' ')}
      />
    </button>
  )
}

// ── Comparison matrix ────────────────────────────────────────────────────────
const COMPARE_ROWS = [
  { label: 'Per-builder price',             icon: DollarSign,   vals: ['$0', '$12', '$25', 'Custom'] },
  { label: 'Builder cap',                   icon: Users,        vals: ['≤ 2', '∞', '∞', '∞'] },
  { label: 'Stakeholders',                  icon: Eye,          vals: ['∞', '∞', '∞', '∞'], accent: true },
  { label: 'Managed LLM credits / builder', icon: Cpu,          vals: ['—', '$4', '$12', 'BYOK'] },
  { label: 'LLM overage markup',            icon: Gauge,        vals: ['—', '×1.30', '×1.30', 'None'] },
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
