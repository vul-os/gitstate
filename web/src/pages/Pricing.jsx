/**
 * Pricing page — "The Ledger" aesthetic, premium tier.
 *
 * Per-builder / stakeholders-free model + an interactive cost calculator,
 * a comparison matrix and FAQ. Prices displayed via useCurrency().format(usd);
 * plans are billed in USD and charged in the user's currency at checkout.
 *
 * No nav / no footer — MarketingLayout provides the shell.
 */

import { useState } from 'react'
import {
  Users, Eye, Sparkles, ArrowRight, Plus, HelpCircle, Mail, KeyRound,
  GitBranch, ShieldCheck, Scale, Infinity as InfinityIcon,
} from 'lucide-react'
import {
  Card, Button, Badge, GradientText, Section, Container, Glow, Stat,
} from '../components/ui'
import { Reveal, RevealList } from '../components/Reveal.jsx'
import { useCurrency } from '../lib/currency.jsx'
import { usePlans, FALLBACK_PLANS } from '../lib/usePlans.js'
import { PlanCard, CostCalculator, CompareTable } from '../components/pricing/index.jsx'
import CompetitorCalculator from '../components/compare/CompetitorCalculator.jsx'

const RECOMMENDED_KEY = 'team'

// ── FAQ ──────────────────────────────────────────────────────────────────────
const FAQ_ITEMS = [
  {
    q: 'What exactly is a "builder"?',
    a: 'A builder is any team member who writes code, runs AI agents, manages repositories, or configures integrations — they consume a paid seat. Product managers, designers, executives, and clients who only read dashboards, cycle-time reports, and PR timelines are stakeholders — always free and unlimited on every plan.',
  },
  {
    q: 'How do the included LLM credits work?',
    a: 'Team and Business plans include a monthly managed-LLM credit per builder ($4 and $12 respectively). These credits are pooled across your team and cover AI code insights, automated summaries, and agent runs. If your team exceeds the included credit pool, overage is billed at cost × 1.30. Alternatively, enable BYOK (bring your own key) and route LLM calls directly to your provider at their rate — no markup.',
  },
  {
    q: 'Why billed in USD but charged in my local currency?',
    a: 'gitstate prices all plans in USD so pricing is consistent and predictable wherever you are. At checkout your card is charged in your local currency (ZAR, GBP, EUR, …) at the live exchange rate from your payment processor. The prices shown here are indicative; your bank statement reflects the local-currency amount.',
  },
  {
    q: 'Can I self-host gitstate?',
    a: 'Yes. gitstate is open-source and self-hosting is free forever. You provide the infrastructure; there are no seat limits or feature gates on self-hosted deployments. The cloud plans fund ongoing development and add managed infra, support, and AI features on top.',
  },
  {
    q: 'When should I use BYOK instead of managed LLM credits?',
    a: 'If your team\'s aggregate LLM spend would exceed the included credits and you have existing API agreements with Anthropic, OpenAI, or another provider, BYOK lets you bypass the managed-LLM markup entirely. The cost calculator above shows your BYOK savings in real time.',
  },
  {
    q: 'Can I change plans mid-cycle?',
    a: 'Yes. Upgrades take effect immediately and are prorated. Downgrades take effect at the next billing cycle, so you keep full access until the end of the period you already paid for.',
  },
  {
    q: 'Is there a free trial for paid plans?',
    a: 'Every paid plan starts with a 14-day free trial — no credit card required. If you exceed the Free plan limits during the trial you will be prompted to confirm a payment method; otherwise you auto-revert to Free.',
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
      <div
        className="grid transition-all duration-300 ease-out"
        style={{ gridTemplateRows: open ? '1fr' : '0fr' }}
      >
        <div className="overflow-hidden">
          <p className="text-sm text-[var(--text-muted)] leading-relaxed pb-4 pr-6">{a}</p>
        </div>
      </div>
    </div>
  )
}

// ── Trust signals row ──────────────────────────────────────────────────────────
const SIGNALS = [
  { icon: GitBranch, label: 'Open source · self-host free' },
  { icon: ShieldCheck, label: 'SSO + audit logs on Business+' },
  { icon: KeyRound, label: 'BYOK — bring your own LLM key' },
  { icon: InfinityIcon, label: 'Unlimited free stakeholders' },
]

// ── Main export ────────────────────────────────────────────────────────────────
export default function Pricing() {
  const { format, currency } = useCurrency()
  const { plans, loading, error } = usePlans()

  // Columns for the comparison matrix — all 4 tiers
  const compareCols = (plans.length ? plans : FALLBACK_PLANS)
    .map(p => ({ key: p.key, name: p.name }))

  return (
    <div className="min-h-screen bg-[var(--bg)]">
      {/* ── Hero ── */}
      <Section py="2xl" className="relative overflow-hidden grain">
        <div className="absolute inset-0 ambient-brand pointer-events-none" />
        <Glow variant="brand" size={760} className="-top-20 left-1/2 opacity-70" />
        <Glow variant="indigo" size={420} className="top-1/2 right-0 opacity-30" />
        <Container size="lg" className="relative z-10 text-center">
          <Reveal>
            <div className="inline-flex items-center gap-2 mb-6 px-3 py-1 rounded-full border border-[var(--border2)] bg-[var(--bg-surface)]/60 backdrop-blur-sm">
              <span className="w-1.5 h-1.5 rounded-full bg-[#2DD4BF] shadow-[0_0_8px_#2DD4BF]" />
              <span className="text-xs font-mono text-[var(--text-muted)]">Pay per builder · stakeholders free forever</span>
            </div>
          </Reveal>
          <Reveal delay={0.08}>
            <GradientText as="h1" className="font-display text-5xl md:text-6xl font-semibold leading-[1.05] mb-5">
              Simple, honest pricing
            </GradientText>
          </Reveal>
          <Reveal delay={0.16}>
            <p className="text-[var(--text-muted)] text-lg max-w-xl mx-auto mb-3">
              Pay for the people who build. Everyone else reads for free.
            </p>
          </Reveal>
          <Reveal delay={0.22}>
            <p className="text-xs font-mono text-[var(--text-faint)]">
              Billed in USD · charged in {currency.code} at checkout · 14-day trial on paid plans
            </p>
          </Reveal>

          {/* trust signals */}
          <Reveal delay={0.3}>
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
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-5">
              {Array.from({ length: 4 }).map((_, i) => (
                <Card key={i} padding="lg" className="animate-pulse">
                  <div className="h-9 w-9 rounded-[var(--radius-badge)] bg-[var(--bg-surface2)] mb-4" />
                  <div className="h-5 w-24 rounded bg-[var(--bg-surface2)] mb-3" />
                  <div className="h-9 w-20 rounded bg-[var(--bg-surface2)] mb-6" />
                  <div className="h-9 w-full rounded-[var(--radius-btn)] bg-[var(--bg-surface2)] mb-5" />
                  {Array.from({ length: 4 }).map((_, j) => (
                    <div key={j} className="h-3 w-full rounded bg-[var(--bg-surface2)] mb-2.5" />
                  ))}
                </Card>
              ))}
            </div>
          ) : (
            <RevealList
              className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-5 items-stretch"
              staggerDelay={0.06}
              inView
            >
              {plans.map(plan => (
                <PlanCard
                  key={plan.key}
                  plan={plan}
                  recommended={plan.key === RECOMMENDED_KEY}
                  format={format}
                />
              ))}
            </RevealList>
          )}

          <Reveal inView delay={0.1}>
            <p className="text-center text-[11px] font-mono text-[var(--text-faint)] mt-8">
              Prices shown in {currency.code} at indicative display rates · billed in USD · charged in {currency.code} at the live rate at checkout.
            </p>
          </Reveal>
        </Container>
      </Section>

      {/* ── Builder / stakeholder wedge explainer ── */}
      <Section py="md">
        <Container size="lg">
          <Reveal inView>
            <div
              className="relative overflow-hidden rounded-[var(--radius-card)] border border-[#2DD4BF]/20 p-7 md:p-9"
              style={{ background: 'linear-gradient(135deg, rgba(45,212,191,0.05) 0%, rgba(99,102,241,0.05) 100%)' }}
            >
              <Glow variant="indigo" size={360} className="bottom-0 right-0 opacity-30" />
              <div className="relative z-10 grid grid-cols-1 md:grid-cols-3 gap-6 md:gap-9">
                <div className="md:col-span-2">
                  <h2 className="font-display text-xl md:text-2xl font-semibold text-[var(--text)] mb-3">
                    The builder / stakeholder model
                  </h2>
                  <p className="text-sm text-[var(--text-muted)] leading-relaxed mb-5">
                    gitstate is priced on the people who <em className="text-[var(--text-dim)] not-italic font-medium">create</em>,
                    not the people who <em className="text-[var(--text-dim)] not-italic font-medium">observe</em>.
                    Builders push code, run AI agents, and configure integrations — they consume a seat.
                    Stakeholders (PMs, execs, designers, clients) read dashboards, cycle-time reports, and
                    PR timelines — always free, always unlimited, on every plan.
                  </p>
                  <div className="flex flex-col sm:flex-row gap-4">
                    <div className="flex-1 rounded-[var(--radius-badge)] border border-[#2DD4BF]/25 bg-[#2DD4BF]/[0.06] px-4 py-3">
                      <div className="flex items-center gap-2 mb-1.5">
                        <Users size={14} className="text-[#2DD4BF]" />
                        <span className="text-xs font-mono uppercase tracking-wider text-[#2DD4BF]">Builders → paid seats</span>
                      </div>
                      <p className="text-xs text-[var(--text-muted)]">Engineers · DevOps · platform · anyone who ships.</p>
                    </div>
                    <div className="flex-1 rounded-[var(--radius-badge)] border border-[#6366F1]/25 bg-[#6366F1]/[0.06] px-4 py-3">
                      <div className="flex items-center gap-2 mb-1.5">
                        <Eye size={14} className="text-[#818cf8]" />
                        <span className="text-xs font-mono uppercase tracking-wider text-[#818cf8]">Stakeholders → free</span>
                      </div>
                      <p className="text-xs text-[var(--text-muted)]">PMs · execs · designers · clients. Read-only, unlimited.</p>
                    </div>
                  </div>
                </div>
                <div className="flex flex-col gap-5 justify-center md:border-l md:border-[var(--border)] md:pl-9">
                  <Stat label="Avg builders / team" value="4–8" sublabel="rest are free stakeholders" />
                  <Stat label="Stakeholder cost" value="$0" sublabel="on every plan, forever" />
                </div>
              </div>
            </div>
          </Reveal>
        </Container>
      </Section>

      {/* ── Cost calculator ── */}
      <Section py="lg">
        <Container size="lg">
          <Reveal inView>
            <div className="mb-8 text-center">
              <Badge color="teal" className="mb-3">Interactive</Badge>
              <h2 className="font-display text-2xl md:text-3xl font-semibold text-[var(--text)] mb-2">
                Estimate your cost
              </h2>
              <p className="text-sm text-[var(--text-muted)] max-w-md mx-auto">
                Set your builder count, expected LLM usage, and BYOK preference — we pick the best plan and show your real per-builder cost.
              </p>
            </div>
          </Reveal>
          <Reveal inView delay={0.1}>
            {!loading && (
              <CostCalculator
                plans={plans}
                format={format}
                currency={currency}
                recommendedKey={RECOMMENDED_KEY}
              />
            )}
          </Reveal>
        </Container>
      </Section>

      {/* ── Competitor cost calculator (honest) ── */}
      <Section py="lg">
        <Container size="lg">
          <Reveal inView>
            <div className="mb-8 text-center">
              <Badge color="indigo" className="mb-3 inline-flex items-center gap-1">
                <Scale size={11} /> honest comparison
              </Badge>
              <h2 className="font-display text-2xl md:text-3xl font-semibold text-[var(--text)] mb-2">
                The cheapest at every team size
              </h2>
              <p className="text-sm text-[var(--text-muted)] max-w-lg mx-auto">
                Drag the sliders. We compute every tool&apos;s real monthly bill and rank by actual cost — no thumb
                on the scale. gitstate lands cheapest at every team shape: builders bundle AI at {format(6)}, BYOK
                drops to {format(3)}, and stakeholders are always free.
              </p>
            </div>
          </Reveal>
          <Reveal inView delay={0.1}>
            {!loading && <CompetitorCalculator plans={plans} planKey={RECOMMENDED_KEY} />}
          </Reveal>
        </Container>
      </Section>

      {/* ── Comparison matrix ── */}
      <Section py="lg">
        <Container size="xl">
          <Reveal inView>
            <div className="mb-8 text-center">
              <h2 className="font-display text-2xl md:text-3xl font-semibold text-[var(--text)] mb-2">
                Compare every plan
              </h2>
              <p className="text-sm text-[var(--text-muted)]">
                Full feature matrix — per-builder pricing, included LLM credits, and enterprise options.
              </p>
            </div>
          </Reveal>
          <Reveal inView delay={0.08}>
            <CompareTable columns={compareCols} recommendedKey={RECOMMENDED_KEY} />
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
                  <Sparkles size={12} /> Free forever plan · ≤ 2 builders
                </span>
                <GradientText as="h2" className="font-display text-3xl md:text-4xl font-semibold mb-4 leading-tight">
                  Start for free today
                </GradientText>
                <p className="text-[var(--text-muted)] mb-8 max-w-md mx-auto">
                  No credit card. No seat minimum. Git is already your ledger — gitstate just reads it.
                  Stakeholders always free, builders pay per seat.
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
