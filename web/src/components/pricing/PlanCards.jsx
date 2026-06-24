/**
 * PlanCards — premium 5-tier plan grid for the marketing Pricing page.
 *
 * Reads live numbers from /api/plans (passed in as `plans`); formats every
 * price through useCurrency() so USD + the converted local amount stay in sync
 * with the currency selector. Driven by two presentational controls owned by
 * the parent Pricing page: `billing` ('monthly' | 'annual') and `mode`
 * ('managed' | 'byok'). Annual applies a presentational ANNUAL_DISCOUNT.
 */
import { ArrowRight, Sparkles, KeyRound, Eye } from 'lucide-react'
import { Card, Button, Glow } from '../ui'
import {
  PLAN_META, PLAN_FEATURES, FEATURE_GLYPH, RECOMMENDED_KEY, isEnterprise,
} from './planData.js'

// Annual = ~2 months free → 16.7% off. Presentational only.
export const ANNUAL_DISCOUNT = 1 / 6

function FeatureRow({ feat }) {
  const g = FEATURE_GLYPH[feat.kind] ?? FEATURE_GLYPH.default
  const { Icon } = g
  return (
    <li className="flex items-start gap-2.5 text-[13px] text-[var(--text-dim)]">
      <span
        className="mt-px shrink-0 w-4 h-4 rounded-full flex items-center justify-center"
        style={{ background: g.bg }}
      >
        <Icon size={feat.kind === 'default' ? 11 : 10} style={{ color: g.color }} strokeWidth={feat.kind === 'default' ? 3 : 2.5} />
      </span>
      <span className={feat.kind === 'stakeholder' ? 'text-[var(--text)]' : ''}>{feat.label}</span>
    </li>
  )
}

function PriceBlock({ plan, billing, mode, format }) {
  const ent = isEnterprise(plan)
  if (ent) {
    return (
      <div className="flex flex-col gap-1.5">
        <span className="font-display text-[2.4rem] font-semibold text-[var(--text)] leading-none">Custom</span>
        <span className="text-xs text-[var(--text-faint)]">Self-host or negotiated cloud SLA</span>
      </div>
    )
  }

  const byok = mode === 'byok' && typeof plan.byokPerBuilderUsd === 'number'
  const baseUsd = byok ? plan.byokPerBuilderUsd : (plan.perBuilderUsd ?? 0)
  const isFree = baseUsd === 0 && plan.key === 'free'
  const annual = billing === 'annual'
  // Annual shows the effective per-month price (~2 months free).
  const shownUsd = annual ? baseUsd * (1 - ANNUAL_DISCOUNT) : baseUsd

  if (isFree) {
    return (
      <div className="flex flex-col gap-1.5">
        <div className="flex items-baseline gap-1.5">
          <span className="font-display text-[2.6rem] font-semibold text-[var(--text)] leading-none tabular-nums">
            {format(0)}
          </span>
          <span className="text-sm text-[var(--text-muted)]">forever</span>
        </div>
        <span className="text-xs text-[var(--text-faint)]">No card required · up to 2 builders</span>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-baseline gap-1.5">
        <span className="font-display text-[2.6rem] font-semibold text-[var(--text)] leading-none tabular-nums">
          {format(shownUsd, { minimumFractionDigits: 0, maximumFractionDigits: annual ? 2 : 0 })}
        </span>
        <span className="text-sm text-[var(--text-muted)]">/ builder / mo</span>
      </div>
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px]">
        {annual && (
          <span className="font-mono text-[var(--text-faint)] line-through tabular-nums">
            {format(baseUsd, { minimumFractionDigits: 0, maximumFractionDigits: 0 })}
          </span>
        )}
        {annual && (
          <span className="font-mono text-[#2DD4BF]">billed yearly · 2 months free</span>
        )}
        {!annual && (
          <span className="text-[var(--text-faint)]">billed monthly · cancel anytime</span>
        )}
      </div>
      {byok ? (
        <span className="inline-flex items-center gap-1.5 text-[11px] text-[#818cf8] font-medium">
          <KeyRound size={11} strokeWidth={2.4} /> BYOK — route AI to your own provider key
        </span>
      ) : (
        <span className="inline-flex items-center gap-1.5 text-[11px] text-[#2DD4BF] font-medium">
          <Sparkles size={11} strokeWidth={2.4} /> Managed AI · {format(plan.includedLlmUsd)} / builder credit included
        </span>
      )}
    </div>
  )
}

function PlanCard({ plan, recommended, billing, mode, format }) {
  const ent = isEnterprise(plan)
  const meta = PLAN_META[plan.key] ?? PLAN_META.starter
  const Icon = meta.icon
  const features = PLAN_FEATURES[plan.key] ?? []

  return (
    <div className={['group relative flex flex-col h-full', recommended ? 'xl:-translate-y-3' : ''].join(' ')}>
      {recommended && (
        <div className="absolute -top-3 left-1/2 -translate-x-1/2 z-20">
          <span
            className="inline-flex items-center gap-1 px-3 py-0.5 rounded-full text-[10px] font-mono font-semibold uppercase tracking-wider text-[#0B1120] shadow-[0_6px_18px_rgba(45,212,191,0.45)] whitespace-nowrap"
            style={{ background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' }}
          >
            <Sparkles size={11} strokeWidth={2.5} /> Most popular
          </span>
        </div>
      )}

      {/* Gradient-border wrapper for the recommended card */}
      <div
        className={[
          'relative flex flex-1 rounded-[var(--radius-card)]',
          recommended ? 'p-px' : '',
        ].join(' ')}
        style={recommended ? { background: 'linear-gradient(160deg, rgba(45,212,191,0.7), rgba(99,102,241,0.55) 55%, rgba(45,212,191,0.15))' } : undefined}
      >
        <Card
          padding="lg"
          glow={recommended}
          className={[
            'relative flex flex-1 flex-col gap-5 transition-all duration-300 w-full',
            recommended
              ? 'border-transparent shadow-[0_18px_60px_-12px_rgba(45,212,191,0.25)]'
              : 'hover:border-[var(--border2)] hover:-translate-y-1.5 hover:shadow-[var(--shadow-card-hover)]',
          ].join(' ')}
        >
          {recommended && (
            <Glow variant="teal" size={300} className="-top-10 right-0 opacity-50 group-hover:opacity-75 transition-opacity" />
          )}

          {/* Header */}
          <div className="flex items-center gap-3 relative z-[1]">
            <div
              className="w-10 h-10 shrink-0 rounded-[var(--radius-badge)] flex items-center justify-center border"
              style={{
                background: recommended ? 'rgba(45,212,191,0.12)' : 'var(--bg-surface3)',
                borderColor: recommended ? 'rgba(45,212,191,0.32)' : 'var(--border)',
              }}
            >
              <Icon size={18} className={recommended ? 'text-[#2DD4BF]' : 'text-[var(--text-muted)]'} strokeWidth={1.8} />
            </div>
            <div>
              <h3 className="font-display text-lg font-semibold text-[var(--text)] leading-none">{plan.name}</h3>
              <p className="text-[11px] text-[var(--text-faint)] mt-1.5">{meta.tagline}</p>
            </div>
          </div>

          {/* Price */}
          <div className="relative z-[1] min-h-[88px]">
            <PriceBlock plan={plan} billing={billing} mode={mode} format={format} />
          </div>

          {/* Stakeholders-free chip */}
          {!ent && (
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
            rightIcon={!ent ? <ArrowRight size={15} /> : undefined}
          >
            {ent ? 'Contact sales' : plan.key === 'free' ? 'Start free' : 'Choose ' + plan.name}
          </Button>

          {/* Features */}
          <ul className="flex flex-col gap-2.5 pt-4 mt-auto border-t border-[var(--border)] relative z-[1]">
            {features.map(feat => <FeatureRow key={feat.label} feat={feat} />)}
          </ul>
        </Card>
      </div>
    </div>
  )
}

export default function PlanCards({ plans, billing, mode, format }) {
  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5 gap-5 items-stretch">
      {plans.map(plan => (
        <PlanCard
          key={plan.key}
          plan={plan}
          recommended={plan.key === RECOMMENDED_KEY}
          billing={billing}
          mode={mode}
          format={format}
        />
      ))}
    </div>
  )
}
