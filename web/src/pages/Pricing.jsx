/**
 * Pricing — the public marketing pricing page ("The Ledger" aesthetic).
 *
 * Story: pay per builder, stakeholders free forever, managed AI at standard
 * provider rate (no per-seat AI tax) — or BYOK. Five tiers (Free / Starter /
 * Pro / Scale / Enterprise) read LIVE from GET /api/plans via usePlans(); every
 * price is formatted through useCurrency() so USD + the converted local amount
 * track the currency selector. A monthly/annual toggle and a managed↔BYOK toggle
 * drive the cards; an interactive cost estimator turns the ladder into real
 * numbers; a full feature matrix and an FAQ + CTA band close it out.
 *
 * No nav / no footer — MarketingLayout provides the shell (with the shared
 * ThemeToggle + CurrencySelector). The hero re-surfaces the CurrencySelector so
 * buyers can switch currency without scrolling back up.
 */
import { useMemo, useState } from 'react'
import {
  Sparkles, ArrowRight, Plus, HelpCircle, Mail, KeyRound,
  GitBranch, ShieldCheck, Scale, Infinity as InfinityIcon, Calculator,
} from 'lucide-react'
import {
  Card, Button, Badge, GradientText, Section, Container, Glow,
} from '../components/ui'
import { Reveal } from '../components/Reveal.jsx'
import { CurrencySelector } from '../components/CurrencySelector.jsx'
import { useCurrency } from '../lib/currency.jsx'
import { usePlans } from '../lib/usePlans.js'
import { LADDER, LADDER_FALLBACK, RECOMMENDED_KEY } from '../components/pricing/planData.js'
import PlanCards from '../components/pricing/PlanCards.jsx'
import CostEstimator from '../components/pricing/CostEstimator.jsx'
import PricingMatrix from '../components/pricing/PricingMatrix.jsx'
import AIModels from '../components/pricing/AIModels.jsx'
import CompetitorCalculator from '../components/compare/CompetitorCalculator.jsx'

// ── Billing toggle ─────────────────────────────────────────────────────────────
function BillingToggle({ value, onChange }) {
  return (
    <div className="inline-flex items-center p-1 rounded-full border border-[var(--border2)] bg-[var(--bg-surface)]/70 backdrop-blur-sm">
      {[
        { key: 'monthly', label: 'Monthly' },
        { key: 'annual', label: 'Annual' },
      ].map(({ key, label }) => {
        const active = value === key
        return (
          <button
            key={key}
            onClick={() => onChange(key)}
            className={[
              'relative inline-flex items-center gap-2 px-4 py-1.5 rounded-full text-sm font-medium transition-all duration-200 cursor-pointer',
              active ? 'text-[#0B1120]' : 'text-[var(--text-muted)] hover:text-[var(--text)]',
            ].join(' ')}
            style={active ? { background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' } : undefined}
          >
            {label}
            {key === 'annual' && (
              <span
                className={[
                  'text-[10px] font-mono font-semibold px-1.5 py-0.5 rounded-full',
                  active ? 'bg-[#0B1120]/15 text-[#0B1120]' : 'bg-[#2DD4BF]/12 text-[#2DD4BF]',
                ].join(' ')}
              >
                2 mo free
              </span>
            )}
          </button>
        )
      })}
    </div>
  )
}

// ── Mode toggle (managed AI / BYOK) ────────────────────────────────────────────
function ModeToggle({ value, onChange }) {
  const opts = [
    { key: 'managed', label: 'Managed AI', Icon: Sparkles },
    { key: 'byok', label: 'BYOK', Icon: KeyRound },
  ]
  return (
    <div className="inline-flex p-1 rounded-full border border-[var(--border)] bg-[var(--bg-surface)]/70 backdrop-blur-sm">
      {opts.map(({ key, label, Icon }) => {
        const active = value === key
        return (
          <button
            key={key}
            onClick={() => onChange(key)}
            className={[
              'inline-flex items-center gap-1.5 px-3.5 py-1.5 rounded-full text-xs font-medium transition-all duration-200 cursor-pointer',
              active ? 'text-[var(--text)] bg-[var(--bg-surface3)] border border-[var(--border2)]' : 'text-[var(--text-muted)] hover:text-[var(--text)] border border-transparent',
            ].join(' ')}
          >
            <Icon size={12} strokeWidth={2.2} className={active ? 'text-[#2DD4BF]' : ''} /> {label}
          </button>
        )
      })}
    </div>
  )
}

// ── FAQ ────────────────────────────────────────────────────────────────────────
const FAQ_ITEMS = [
  {
    q: 'What exactly is a "builder"?',
    a: 'A builder is anyone who writes code, runs AI agents, manages repositories, or configures integrations — they consume a paid seat. Product managers, designers, executives, and clients who only read dashboards, cycle-time reports, and PR timelines are stakeholders — always free and unlimited on every plan, including Free.',
  },
  {
    q: 'How does managed-AI pricing work? (AI at cost +5%)',
    a: 'There is no per-seat AI fee. Each paid plan includes a monthly managed-AI credit per builder (Starter $1, Pro $5, Scale $20), pooled across your team. Past the credit you pay each model at its provider rate plus a small 5% handling margin — nothing added per seat. Prefer to pay your provider directly? Enable BYOK and route AI calls straight to Anthropic, OpenAI, or Gemini for a lower per-builder price.',
  },
  {
    q: 'Can I be billed in ZAR (or another currency)?',
    a: 'Yes. gitstate anchors prices in USD so they stay consistent worldwide, but at checkout your card is charged in your local currency — ZAR, GBP, EUR, and more — at the live exchange rate from your payment processor. Use the currency selector to preview indicative local prices anywhere on this page.',
  },
  {
    q: 'Can I cancel anytime?',
    a: 'Always. There are no contracts or seat minimums on self-serve plans. Cancel whenever you like and you keep access until the end of the period you have already paid for. Upgrades are immediate and prorated; downgrades take effect at the next cycle.',
  },
  {
    q: 'Who owns my data?',
    a: 'You do. gitstate reads your git history — it never becomes the source of truth. Export everything at any time, and on Enterprise self-host the whole thing on your own infrastructure so your data never leaves your perimeter. Cancelling never holds your data hostage.',
  },
  {
    q: 'Is there a free trial for paid plans?',
    a: 'Every paid plan starts with a 14-day free trial — no credit card required. Exceed the Free limits during the trial and we will prompt you to add a payment method; otherwise you auto-revert to the Free plan.',
  },
]

function FaqItem({ q, a, defaultOpen = false }) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <div className="border-b border-[var(--border)] last:border-b-0">
      <button
        className="w-full flex items-center justify-between gap-4 py-4 text-left group cursor-pointer"
        onClick={() => setOpen(o => !o)}
        aria-expanded={open}
      >
        <span className="text-sm font-medium text-[var(--text)] group-hover:text-[#2DD4BF] transition-colors">
          {q}
        </span>
        <Plus
          size={16}
          className={`shrink-0 text-[var(--text-faint)] group-hover:text-[#2DD4BF] transition-all duration-300 ${open ? 'rotate-[135deg]' : ''}`}
        />
      </button>
      <div className="grid transition-all duration-300 ease-out" style={{ gridTemplateRows: open ? '1fr' : '0fr' }}>
        <div className="overflow-hidden">
          <p className="text-sm text-[var(--text-muted)] leading-relaxed pb-4 pr-6">{a}</p>
        </div>
      </div>
    </div>
  )
}

// ── Trust signals ──────────────────────────────────────────────────────────────
const SIGNALS = [
  { icon: GitBranch, label: 'Open core · self-host free' },
  { icon: ShieldCheck, label: 'SSO + audit on Scale' },
  { icon: KeyRound, label: 'BYOK — bring your own LLM key' },
  { icon: InfinityIcon, label: 'Unlimited free stakeholders' },
]

// ── Main export ──────────────────────────────────────────────────────────────
export default function Pricing() {
  const { format, currency } = useCurrency()
  const { plans: livePlans, loading, error } = usePlans()
  const [billing, setBilling] = useState('monthly')
  const [mode, setMode] = useState('managed')

  // Normalize to the canonical ladder order; fall back to the seeded ladder if
  // the API returned the stale/legacy set or nothing.
  const plans = useMemo(() => {
    const byKey = new Map((livePlans ?? []).map(p => [p.key, p]))
    const ordered = LADDER.map(k => byKey.get(k)).filter(Boolean)
    return ordered.length === LADDER.length ? ordered : LADDER_FALLBACK
  }, [livePlans])

  return (
    <div className="min-h-screen bg-[var(--bg)]">
      {/* ── Hero ── */}
      <Section py="2xl" className="relative overflow-hidden grain">
        <div className="absolute inset-0 ambient-brand pointer-events-none" />
        <Glow variant="brand" size={820} className="-top-24 left-1/2 opacity-70" />
        <Glow variant="indigo" size={460} className="top-1/2 right-0 opacity-30" />
        <Container size="lg" className="relative z-10 text-center">
          <Reveal>
            <div className="inline-flex items-center gap-2 mb-6 px-3 py-1 rounded-full border border-[var(--border2)] bg-[var(--bg-surface)]/60 backdrop-blur-sm">
              <span className="w-1.5 h-1.5 rounded-full bg-[#2DD4BF] shadow-[0_0_8px_#2DD4BF]" />
              <span className="text-xs font-mono text-[var(--text-muted)]">Pay per builder · stakeholders free · AI at cost +5%</span>
            </div>
          </Reveal>
          <Reveal delay={0.08}>
            <h1 className="font-display text-5xl md:text-6xl font-semibold leading-[1.05] tracking-[-0.02em] text-[var(--text)] mb-5">
              Pricing that bills the{' '}
              <GradientText as="span" className="font-display text-5xl md:text-6xl font-semibold leading-[1.05] tracking-[-0.02em]">
                people who build
              </GradientText>
            </h1>
          </Reveal>
          <Reveal delay={0.16}>
            <p className="text-[var(--text-muted)] text-lg max-w-xl mx-auto mb-2">
              One price per builder. Everyone who only reads dashboards — PMs, execs, clients — is a free
              stakeholder. AI runs at the model&apos;s standard rate plus 5%, or bring your own key.
            </p>
          </Reveal>
          <Reveal delay={0.22}>
            <p className="text-xs font-mono text-[var(--text-faint)] mb-9">
              Anchored in USD · charged in {currency.code} at checkout · 14-day trial · cancel anytime
            </p>
          </Reveal>

          {/* toggles + currency */}
          <Reveal delay={0.28}>
            <div className="flex flex-wrap items-center justify-center gap-3">
              <BillingToggle value={billing} onChange={setBilling} />
              <ModeToggle value={mode} onChange={setMode} />
              <CurrencySelector />
            </div>
          </Reveal>

          {/* trust signals */}
          <Reveal delay={0.36}>
            <div className="flex flex-wrap items-center justify-center gap-x-6 gap-y-2 mt-9">
              {SIGNALS.map(({ icon: Icon, label }) => (
                <span key={label} className="inline-flex items-center gap-2 text-xs text-[var(--text-muted)]">
                  <Icon size={14} className="text-[#2DD4BF]" strokeWidth={1.8} />
                  {label}
                </span>
              ))}
            </div>
          </Reveal>
        </Container>
      </Section>

      {/* ── Plan cards ── */}
      <Section py="md">
        <Container size="xl">
          {error && (
            <div className="mb-6 px-4 py-3 rounded-[var(--radius-badge)] border border-yellow-500/20 bg-yellow-500/5 text-xs text-yellow-400 font-mono">
              {error}
            </div>
          )}

          {loading ? (
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5 gap-5">
              {Array.from({ length: 5 }).map((_, i) => (
                <Card key={i} padding="lg" className="animate-pulse">
                  <div className="h-10 w-10 rounded-[var(--radius-badge)] bg-[var(--bg-surface2)] mb-4" />
                  <div className="h-5 w-24 rounded bg-[var(--bg-surface2)] mb-3" />
                  <div className="h-10 w-20 rounded bg-[var(--bg-surface2)] mb-6" />
                  <div className="h-9 w-full rounded-[var(--radius-btn)] bg-[var(--bg-surface2)] mb-5" />
                  {Array.from({ length: 4 }).map((_, j) => (
                    <div key={j} className="h-3 w-full rounded bg-[var(--bg-surface2)] mb-2.5" />
                  ))}
                </Card>
              ))}
            </div>
          ) : (
            <Reveal inView>
              <PlanCards plans={plans} billing={billing} mode={mode} format={format} />
            </Reveal>
          )}

          <Reveal inView delay={0.1}>
            <p className="text-center text-[11px] font-mono text-[var(--text-faint)] mt-9">
              Prices shown in {currency.code} at indicative display rates · anchored in USD · charged in {currency.code} at the live rate at checkout.
            </p>
          </Reveal>
        </Container>
      </Section>

      {/* ── Interactive cost estimator ── */}
      <Section py="lg">
        <Container size="lg">
          <Reveal inView>
            <div className="mb-8 text-center">
              <Badge color="teal" className="mb-3 inline-flex items-center gap-1">
                <Calculator size={11} /> interactive · live numbers
              </Badge>
              <h2 className="font-display text-2xl md:text-3xl font-semibold text-[var(--text)] mb-2">
                See your real monthly cost
              </h2>
              <p className="text-sm text-[var(--text-muted)] max-w-lg mx-auto">
                Drag the builder count — every tier&apos;s monthly total updates live in {currency.code},
                straight from the published ladder. Stakeholders stay free at every shape.
              </p>
            </div>
          </Reveal>
          <Reveal inView delay={0.1}>
            {!loading && <CostEstimator plans={plans} format={format} />}
          </Reveal>
        </Container>
      </Section>

      {/* ── Managed AI — models & pass-through pricing ── */}
      <Section py="lg">
        <Container size="lg">
          <Reveal inView>
            <div className="mb-8 text-center">
              <Badge color="teal" className="mb-3 inline-flex items-center gap-1">
                <Sparkles size={11} /> managed AI · at cost +5%
              </Badge>
              <h2 className="font-display text-2xl md:text-3xl font-semibold text-[var(--text)] mb-2">
                Run any model at its standard rate
              </h2>
              <p className="text-sm text-[var(--text-muted)] max-w-lg mx-auto">
                No per-seat AI tax. AI is included up to your monthly credit, then metered at the model&apos;s
                standard rate plus 5% — or bring your own key and pay your provider directly.
              </p>
            </div>
          </Reveal>
          <Reveal inView delay={0.1}>
            <AIModels />
          </Reveal>
        </Container>
      </Section>

      {/* ── Head-to-head competitor calculator ── */}
      <Section py="lg">
        <Container size="lg">
          <Reveal inView>
            <div className="mb-8 text-center">
              <Badge color="indigo" className="mb-3 inline-flex items-center gap-1">
                <Scale size={11} /> honest comparison
              </Badge>
              <h2 className="font-display text-2xl md:text-3xl font-semibold text-[var(--text)] mb-2">
                Compare against the tools you&apos;d replace
              </h2>
              <p className="text-sm text-[var(--text-muted)] max-w-lg mx-auto">
                Drag the sliders. We compute every tool&apos;s real monthly bill and rank by actual cost —
                builders bundle AI, BYOK drops it further, and stakeholders are always free.
              </p>
            </div>
          </Reveal>
          <Reveal inView delay={0.1}>
            {!loading && <CompetitorCalculator plans={livePlans} planKey={RECOMMENDED_KEY} />}
          </Reveal>
        </Container>
      </Section>

      {/* ── Comparison matrix ── */}
      <Section py="lg">
        <Container size="xl">
          <Reveal inView>
            <div className="mb-8 text-center">
              <h2 className="font-display text-2xl md:text-3xl font-semibold text-[var(--text)] mb-2">
                Every feature, every tier
              </h2>
              <p className="text-sm text-[var(--text-muted)]">
                The full matrix — per-builder pricing, included AI credits, platform, security &amp; support.
              </p>
            </div>
          </Reveal>
          <Reveal inView delay={0.08}>
            <PricingMatrix plans={plans} format={format} />
          </Reveal>
        </Container>
      </Section>

      {/* ── FAQ ── */}
      <Section py="lg">
        <Container size="md">
          <Reveal inView>
            <div className="mb-8 flex items-start gap-3">
              <div className="w-9 h-9 rounded-[var(--radius-badge)] bg-[#6366F1]/10 border border-[#6366F1]/25 flex items-center justify-center shrink-0">
                <HelpCircle size={17} className="text-[#818cf8]" strokeWidth={1.8} />
              </div>
              <div>
                <h2 className="font-display text-2xl font-semibold text-[var(--text)] mb-1">
                  Frequently asked questions
                </h2>
                <p className="text-sm text-[var(--text-muted)]">
                  Still unsure?{' '}
                  <a href="mailto:hello@gitstate.dev" className="text-[#2DD4BF] hover:underline inline-flex items-center gap-1">
                    <Mail size={13} /> Email us
                  </a>{' '}
                  — we respond same day.
                </p>
              </div>
            </div>
          </Reveal>

          <Reveal inView delay={0.08}>
            <Card padding="none" className="border-[var(--border2)]">
              <div className="px-6">
                {FAQ_ITEMS.map((item, i) => (
                  <FaqItem key={item.q} q={item.q} a={item.a} defaultOpen={i === 0} />
                ))}
              </div>
            </Card>
          </Reveal>
        </Container>
      </Section>

      {/* ── CTA band ── */}
      <Section py="2xl" className="relative overflow-hidden">
        <Container size="md" className="relative z-10">
          <Reveal inView>
            <div className="relative overflow-hidden rounded-[var(--radius-card)] border border-[var(--border2)] bg-[var(--bg-surface)] px-8 py-12 md:px-14 md:py-16 text-center grain">
              <div className="absolute inset-0 ambient-brand pointer-events-none" />
              <Glow variant="teal" size={480} className="top-0 left-1/4 opacity-50" />
              <Glow variant="indigo" size={420} className="bottom-0 right-1/4 opacity-40" />
              <div className="relative z-10">
                <span className="inline-flex items-center gap-1.5 mb-5 px-3 py-1 rounded-full border border-[#2DD4BF]/25 bg-[#2DD4BF]/[0.06] text-xs font-mono text-[#2DD4BF]">
                  <Sparkles size={12} /> Free forever · up to 2 builders
                </span>
                <GradientText as="h2" className="font-display text-3xl md:text-4xl font-semibold mb-4 leading-tight">
                  Start free today
                </GradientText>
                <p className="text-[var(--text-muted)] mb-8 max-w-md mx-auto">
                  No credit card. No seat minimum. Git is already your ledger — gitstate just reads it.
                  Stakeholders always free, builders pay per seat, cancel anytime.
                </p>
                <div className="flex flex-wrap items-center justify-center gap-3">
                  <Button variant="primary" size="lg" rightIcon={<ArrowRight size={16} />}>
                    Get started — it&apos;s free
                  </Button>
                  <Button variant="outline" size="lg" leftIcon={<GitBranch size={15} />}>
                    View self-host docs
                  </Button>
                </div>
              </div>
            </div>
          </Reveal>
        </Container>
      </Section>
    </div>
  )
}
