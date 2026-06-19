/**
 * Compare page — gitstate vs Linear, Jira, ClickUp, ZenHub, GitHub Projects.
 *
 * "The Ledger" aesthetic: dark-first, teal→indigo, monospace accents, grain.
 * Wrapped by MarketingLayout from the orchestrator (no nav/footer here).
 *
 * Sections: hero → grounded cost calculator (CostCompare) → honest feature
 * matrix (FeatureMatrix) → structural "why incumbents can't copy it" narrative
 * → CTAs to /signup + /pricing. All competitor numbers are the researched 2026
 * figures, kept exactly as given.
 */
import { Link } from 'react-router-dom'
import {
  ArrowRight, Layers, GitBranch, Lock, ShieldOff, Calculator, Scale,
} from 'lucide-react'
import { Card, Badge, Pill, GradientText, Section, Container, Glow } from '../components/ui'
import { Reveal, RevealList } from '../components/Reveal.jsx'
import CostCompare from '../components/compare/CostCompare.jsx'
import FeatureMatrix from '../components/compare/FeatureMatrix.jsx'

// ── Narrative blocks ──────────────────────────────────────────────────────────

const NARRATIVES = [
  {
    heading: 'The problem is structural',
    body: `Jira, Linear, ClickUp, ZenHub, and GitHub Projects are parallel, hand-maintained records of work that already happened in git. Every sprint ceremony, every story-point estimate, every "please update your ticket" Slack message exists because those tools chose to store a copy of reality rather than read the original.

That copy is unreliable by construction. Estimates have been ~30% wrong for 40 years. Velocity becomes a vanity metric the moment it's a target. Billable hours are reconstructed from memory on Friday afternoon, leaking 15–25% of agency revenue.

These aren't bugs in the tools. They're what happens when you ask a human to invent a number.`,
  },
  {
    heading: 'Why incumbents can\'t copy it',
    body: `These tools aren't ignoring git because they haven't thought of it. They're structurally blocked.

Their entire data model is the hand-entered ticket. Their revenue model is per total seat — every stakeholder who views progress costs you money. Replacing tickets with git-derived state would invalidate years of customer data and destroy the metric their pricing depends on.

gitstate charges per builder. Stakeholders — clients, PMs, executives — are free. The data model IS the git object graph. There's no parallel record to maintain, no incentive to make tickets the source of truth.`,
  },
  {
    heading: 'What "derived" actually means',
    body: `When a PR merges, gitstate marks it done — automatically, immediately, without anyone touching a board. Cycle time is first-commit-to-merge, the exact DORA definition. Effort is an LLM reading the actual diff and judging semantic difficulty: a 3-line change that restructures an auth flow isn't the same weight as 300 lines of generated test fixtures.

For billing teams: every invoice line links to a commit SHA or pull request. Work git can't see — client calls, architecture sessions — is flagged for you to fill in. It is never silently fabricated.`,
  },
]

function NarrativeSection({ heading, body, index }) {
  return (
    <Reveal inView>
      <div className="group relative">
        <span
          className="absolute -left-[1.6rem] top-1 w-3 h-3 rounded-full border-2 border-[var(--bg)] z-10"
          style={{ background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' }}
          aria-hidden="true"
        />
        <div className="flex items-baseline gap-3 mb-3">
          <span className="font-mono text-xs text-[var(--brand-teal)] tabular-nums">
            {String(index + 1).padStart(2, '0')}
          </span>
          <h3 className="font-display text-xl font-semibold text-[var(--text)] tracking-tight">
            {heading}
          </h3>
        </div>
        <div className="space-y-3">
          {body.split('\n\n').map((para) => (
            <p key={para.slice(0, 40)} className="text-[var(--text-muted)] leading-relaxed text-[15px]">
              {para}
            </p>
          ))}
        </div>
      </div>
    </Reveal>
  )
}

// ── Structural blockers ───────────────────────────────────────────────────────

const BLOCKERS = [
  {
    tool: 'Jira / ClickUp',
    reason: 'Per-seat pricing on every viewer. Free stakeholder access destroys their revenue model.',
    icon: Lock,
  },
  {
    tool: 'Linear',
    reason: 'The ticket is the atom of truth. Replacing it with a git event invalidates their entire data model.',
    icon: Layers,
  },
  {
    tool: 'ZenHub / GitHub',
    reason: 'GitHub-centric; no unified GitLab. Derived state is additive, not foundational — tickets still own status.',
    icon: GitBranch,
  },
]

function BlockersSection() {
  return (
    <RevealList className="grid grid-cols-1 md:grid-cols-3 gap-4" staggerDelay={0.07} inView>
      {BLOCKERS.map((b) => {
        const Icon = b.icon
        return (
          <Card key={b.tool} padding="lg" hoverable>
            <div className="flex flex-col gap-3 h-full">
              <div className="flex items-center gap-2.5">
                <span className="flex items-center justify-center w-8 h-8 rounded-lg bg-red-500/10 text-red-400/80 shrink-0">
                  <Icon size={16} strokeWidth={2} />
                </span>
                <span className="font-mono text-xs font-medium text-[var(--text-faint)] uppercase tracking-widest">
                  {b.tool}
                </span>
              </div>
              <p className="text-sm text-[var(--text-muted)] leading-relaxed flex-1">{b.reason}</p>
              <Badge color="red" className="self-start inline-flex items-center gap-1">
                <ShieldOff size={11} strokeWidth={2.5} /> won&apos;t fix
              </Badge>
            </div>
          </Card>
        )
      })}
    </RevealList>
  )
}

// ── CTA block ────────────────────────────────────────────────────────────────

function CtaBlock() {
  return (
    <div
      className="relative overflow-hidden rounded-2xl border border-[var(--border2)] grain"
      style={{ background: 'var(--bg-surface)' }}
    >
      <Glow variant="brand" size={500} className="top-1/2 left-1/2" />
      <div className="relative z-10 px-8 py-12 text-center">
        <Reveal>
          <div className="inline-flex flex-wrap items-center justify-center gap-2 mb-5">
            <Badge color="teal">open source · AGPL-3.0</Badge>
            <Badge color="indigo">free stakeholder seats</Badge>
          </div>
        </Reveal>
        <Reveal delay={0.08}>
          <GradientText as="h2" className="font-display text-3xl md:text-4xl font-bold mb-3 tracking-tight">
            Stop maintaining the fiction.
          </GradientText>
        </Reveal>
        <Reveal delay={0.14}>
          <p className="text-[var(--text-muted)] max-w-md mx-auto mb-8 text-[15px] leading-relaxed">
            Connect your repos and let git tell the truth. No ticket migrations, no sprint ceremonies, no
            reconstructed timesheets — and a smaller bill.
          </p>
        </Reveal>
        <Reveal delay={0.2}>
          <div className="flex flex-col sm:flex-row gap-3 justify-center">
            <Link
              to="/signup"
              className="inline-flex items-center justify-center gap-2 px-6 py-3 rounded-lg font-semibold text-sm text-[#0B1120] transition-all duration-150 hover:opacity-90 hover:scale-[1.02]"
              style={{ background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' }}
            >
              Start for free
              <ArrowRight size={15} strokeWidth={2.5} />
            </Link>
            <Link
              to="/pricing"
              className="inline-flex items-center justify-center gap-2 px-6 py-3 rounded-lg font-medium text-sm text-[var(--text-dim)] border border-[var(--border)] hover:border-[var(--border2)] hover:text-[var(--text)] transition-all duration-150"
            >
              See pricing
            </Link>
          </div>
        </Reveal>
      </div>
    </div>
  )
}

// ── Main export ───────────────────────────────────────────────────────────────

export default function Compare() {
  return (
    <div className="min-h-screen bg-[var(--bg)] text-[var(--text)]">

      {/* ── Hero ── */}
      <Section py="xl">
        <Container size="lg">
          <div className="relative overflow-hidden rounded-2xl border border-[var(--border)] px-6 md:px-12 py-16 text-center grain">
            <Glow variant="teal" size={700} className="top-0 left-1/3" />
            <Glow variant="indigo" size={500} className="bottom-0 right-1/4" />

            <div className="relative z-10">
              <Reveal>
                <div className="inline-flex items-center gap-2 mb-6">
                  <Pill color="teal">honest comparison</Pill>
                </div>
              </Reveal>

              <Reveal delay={0.08}>
                <h1 className="font-display text-4xl md:text-5xl lg:text-6xl font-bold tracking-tight mb-4 leading-[1.08]">
                  <span className="text-[var(--text)]">Pay for builders.</span>{' '}
                  <GradientText>Not for everyone who looks.</GradientText>
                </h1>
              </Reveal>

              <Reveal delay={0.15}>
                <p className="text-[var(--text-muted)] text-lg md:text-xl max-w-2xl mx-auto leading-relaxed mt-4">
                  Linear, Jira, ClickUp, ZenHub, and GitHub bill every seat and tax AI per head. gitstate charges
                  per builder, keeps stakeholders free, and bundles managed AI. See exactly what you&apos;d save —
                  with the math shown.
                </p>
              </Reveal>

              <Reveal delay={0.22}>
                <div className="flex flex-col sm:flex-row gap-3 justify-center mt-8">
                  <a
                    href="#calculator"
                    className="inline-flex items-center justify-center gap-2 px-6 py-3 rounded-lg font-semibold text-sm text-[#0B1120] transition-all duration-150 hover:opacity-90"
                    style={{ background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' }}
                  >
                    <Calculator size={15} strokeWidth={2.5} />
                    Calculate my savings
                  </a>
                  <Link
                    to="/pricing"
                    className="inline-flex items-center justify-center gap-2 px-6 py-3 rounded-lg font-medium text-sm text-[var(--text-muted)] border border-[var(--border)] hover:border-[var(--border2)] hover:text-[var(--text)] transition-all duration-150"
                  >
                    View pricing
                  </Link>
                </div>
              </Reveal>
            </div>
          </div>
        </Container>
      </Section>

      {/* ── Cost calculator ── */}
      <Section py="md" id="calculator" className="scroll-mt-8">
        <Container size="lg">
          <Reveal inView>
            <div className="flex items-end justify-between mb-6 gap-4 flex-wrap">
              <div>
                <Badge color="teal" className="mb-3">interactive · grounded in 2026 list prices</Badge>
                <h2 className="font-display text-2xl md:text-3xl font-bold text-[var(--text)] tracking-tight">
                  What you&apos;d actually pay
                </h2>
                <p className="text-[var(--text-muted)] text-sm mt-1.5 max-w-xl">
                  Set your team shape. We compute every tool&apos;s real monthly bill — seats, AI add-ons and all —
                  and show the savings, transparently.
                </p>
              </div>
            </div>
          </Reveal>
          <Reveal inView delay={0.06}>
            <CostCompare />
          </Reveal>
        </Container>
      </Section>

      {/* ── Feature matrix ── */}
      <Section py="lg">
        <Container size="lg">
          <Reveal inView>
            <div className="flex items-end justify-between mb-6 gap-4 flex-wrap">
              <div>
                <Badge color="indigo" className="mb-3 inline-flex items-center gap-1">
                  <Scale size={11} /> fair &amp; honest
                </Badge>
                <h2 className="font-display text-2xl md:text-3xl font-bold text-[var(--text)] tracking-tight">
                  Feature comparison
                </h2>
                <p className="text-[var(--text-muted)] text-sm mt-1.5 max-w-xl">
                  gitstate wins its structural categories — and we mark, honestly, where these competitors lead or
                  match us.
                </p>
              </div>
              <Badge color="teal" className="shrink-0">6 tools</Badge>
            </div>
          </Reveal>
          <Reveal inView delay={0.06}>
            <FeatureMatrix />
          </Reveal>
        </Container>
      </Section>

      {/* ── Narrative: the structural case ── */}
      <Section py="lg">
        <Container size="md">
          <Reveal inView>
            <div className="mb-3">
              <Badge color="default" className="mb-4">the structural case</Badge>
              <h2 className="font-display text-2xl md:text-3xl font-bold text-[var(--text)] tracking-tight">
                Why the comparison isn&apos;t close
              </h2>
            </div>
          </Reveal>

          <div className="mt-10 space-y-12 pl-6 border-l border-[var(--border)]">
            {NARRATIVES.map((n, i) => (
              <NarrativeSection key={i} heading={n.heading} body={n.body} index={i} />
            ))}
          </div>
        </Container>
      </Section>

      {/* ── Structural blockers ── */}
      <Section py="lg">
        <Container size="lg">
          <Reveal inView>
            <div className="mb-8 text-center">
              <Badge color="red" className="mb-4">structurally blocked</Badge>
              <h2 className="font-display text-2xl md:text-3xl font-bold text-[var(--text)] tracking-tight">
                They can&apos;t copy it — and here&apos;s why
              </h2>
              <p className="text-[var(--text-muted)] text-sm mt-2 max-w-lg mx-auto leading-relaxed">
                Each incumbent is blocked by its own business model, not by engineering ambition.
              </p>
            </div>
          </Reveal>
          <BlockersSection />
        </Container>
      </Section>

      {/* ── CTA ── */}
      <Section py="xl">
        <Container size="md">
          <CtaBlock />
        </Container>
      </Section>
    </div>
  )
}
