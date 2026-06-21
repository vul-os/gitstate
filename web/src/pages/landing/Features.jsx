/**
 * Features — "Everything derived from git"
 * Six capabilities rendered as a uniform grid of premium cards with distinct
 * Lucide icons, gradient accents, depth + hover lift. Product screenshots live
 * in the dedicated ShowcaseGallery, so this grid stays clean in both themes.
 */
import {
  GitFork,
  Layers,
  Sparkles,
  ReceiptText,
  UserRoundCheck,
  BotMessageSquare,
} from 'lucide-react'
import { Reveal, RevealList } from '../../components/Reveal.jsx'
import {
  Badge,
  GradientText,
  Glow,
  Section,
  Container,
} from '../../components/ui/index.js'

function SectionLabel({ children }) {
  return (
    <span className="inline-flex items-center gap-2 text-[11px] font-mono uppercase tracking-[0.15em] text-[var(--brand-teal)] mb-4">
      <span className="w-4 h-px bg-[var(--brand-teal)] opacity-60" aria-hidden="true" />
      {children}
      <span className="w-4 h-px bg-[var(--brand-teal)] opacity-60" aria-hidden="true" />
    </span>
  )
}

/* ── Feature definitions ────────────────────────────────────────────────────── */

const FEATURES = [
  {
    icon: GitFork,
    accent: '#2DD4BF',
    accentRgb: '45,212,191',
    badgeColor: 'teal',
    label: 'Core engine',
    title: 'Git engine, not a wrapper',
    body: 'Deep git reading: walk history, diff, blame, cycle time, DORA metrics. Derived from the repo itself — no webhook magic required.',
    shot: null,
  },
  {
    icon: Layers,
    accent: '#6366F1',
    accentRgb: '99,102,241',
    badgeColor: 'indigo',
    label: 'Integrations',
    title: 'GitHub + GitLab, unified',
    body: 'Connect both platforms. Issues sync two-way. Your board derives state from real git activity — not sprint ceremonies.',
    shot: null,
  },
  {
    icon: Sparkles,
    accent: '#f59e0b',
    accentRgb: '245,158,11',
    badgeColor: 'yellow',
    label: 'AI sizing',
    title: 'LLM diff-difficulty sizing',
    body: 'Effort sizing from an LLM reading the actual diff — not story-point poker. Calibrated from your observed cycle time, not vibes.',
    shot: null,
  },
  {
    icon: ReceiptText,
    accent: '#22c55e',
    accentRgb: '34,197,94',
    badgeColor: 'green',
    label: 'Provenance',
    title: 'Every item shows its source',
    body: "Git-derived work and manual work live on one board, each tagged with where it came from. Evidence-backed billing builds on the same honest provenance.",
    shot: null,
  },
  {
    icon: UserRoundCheck,
    accent: '#2DD4BF',
    accentRgb: '45,212,191',
    badgeColor: 'teal',
    label: 'Access model',
    title: 'Free stakeholder seats',
    body: 'Pricing is per builder — devs and PMs. Clients, stakeholders, and read-only viewers are always free. The seat-tax killer incumbents can\'t match.',
    shot: null,
  },
  {
    icon: BotMessageSquare,
    accent: '#6366F1',
    accentRgb: '99,102,241',
    badgeColor: 'indigo',
    label: 'Platform',
    title: 'Agent-native from day one',
    body: 'Built for a world where AI writes the code and humans supervise. Agent runs are tracked, attributed, and billed just like human commits.',
    shot: null,
  },
]

/* ── Plain card (no product shot) ───────────────────────────────────────────── */

function FeatureCard({ f }) {
  const Icon = f.icon
  return (
    <div
      className="group relative flex flex-col gap-4 rounded-[var(--radius-card)] p-6 overflow-hidden transition-all duration-300 hover:-translate-y-0.5"
      style={{
        background: 'var(--bg-surface)',
        border: '1px solid var(--border)',
        boxShadow: 'var(--shadow-card)',
      }}
      onMouseEnter={e => {
        e.currentTarget.style.border = `1px solid rgba(${f.accentRgb},0.2)`
        e.currentTarget.style.boxShadow = `var(--shadow-card-hover), 0 0 28px rgba(${f.accentRgb},0.06)`
      }}
      onMouseLeave={e => {
        e.currentTarget.style.border = '1px solid var(--border)'
        e.currentTarget.style.boxShadow = 'var(--shadow-card)'
      }}
    >
      {/* Ambient hover glow */}
      <div
        aria-hidden="true"
        className="absolute inset-0 pointer-events-none opacity-0 group-hover:opacity-100 transition-opacity duration-500"
        style={{
          background: `radial-gradient(300px circle at 10% -10%, rgba(${f.accentRgb},0.07) 0%, transparent 70%)`,
        }}
      />

      {/* Icon + badge row */}
      <div className="relative z-10 flex items-start justify-between">
        <div
          className="w-11 h-11 rounded-xl flex items-center justify-center shrink-0 transition-transform duration-200 group-hover:scale-105"
          style={{
            background: `linear-gradient(135deg, rgba(${f.accentRgb},0.15) 0%, rgba(${f.accentRgb},0.06) 100%)`,
            boxShadow: `inset 0 0 0 1px rgba(${f.accentRgb},0.2), 0 1px 4px rgba(0,0,0,0.2)`,
            color: f.accent,
          }}
        >
          <Icon size={20} strokeWidth={1.5} aria-hidden="true" />
        </div>
        <Badge color={f.badgeColor} className="text-[10px] shrink-0">
          {f.label}
        </Badge>
      </div>

      {/* Hairline rule */}
      <div
        aria-hidden="true"
        className="relative z-10 h-px"
        style={{
          background: `linear-gradient(to right, rgba(${f.accentRgb},0.22) 0%, transparent 80%)`,
        }}
      />

      {/* Text */}
      <div className="relative z-10 flex flex-col gap-1.5">
        <h3
          className="font-display text-base font-semibold tracking-[-0.02em] leading-snug"
          style={{ color: 'var(--text)' }}
        >
          {f.title}
        </h3>
        <p className="text-sm leading-relaxed" style={{ color: 'var(--text-muted)' }}>
          {f.body}
        </p>
      </div>

      {/* Corner pip */}
      <div
        aria-hidden="true"
        className="absolute bottom-3.5 right-3.5 w-1 h-1 rounded-full opacity-20 group-hover:opacity-50 transition-opacity duration-300"
        style={{ background: f.accent }}
      />
    </div>
  )
}

/* ── Section ─────────────────────────────────────────────────────────────────── */

export default function Features() {
  return (
    <Section py="xl" className="relative overflow-hidden border-t border-[var(--border)]">
      <Glow variant="indigo" size={700} className="top-0 right-[10%] opacity-50" />
      <Glow variant="teal" size={500} className="bottom-0 left-[5%] opacity-40" />

      <Container size="lg" className="relative z-10">
        {/* Header */}
        <Reveal inView>
          <div className="text-center mb-14">
            <SectionLabel>Capabilities</SectionLabel>
            <h2
              className="font-display text-3xl md:text-4xl font-semibold tracking-[-0.025em] mb-4"
              style={{ color: 'var(--text)' }}
            >
              Everything derived from git.{' '}
              <GradientText
                as="span"
                className="font-display text-3xl md:text-4xl font-semibold"
              >
                Nothing entered by hand.
              </GradientText>
            </h2>
            <p className="text-base max-w-lg mx-auto" style={{ color: 'var(--text-muted)' }}>
              Jira, Linear, ClickUp — manually maintained fictions sitting next to git.
              gitstate eliminates the fiction.
            </p>
          </div>
        </Reveal>

        {/* Uniform capability grid — even card heights, clean in both themes */}
        <RevealList
          className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4"
          staggerDelay={0.07}
          inView
        >
          {FEATURES.map(f => (
            <FeatureCard key={f.title} f={f} />
          ))}
        </RevealList>
      </Container>
    </Section>
  )
}
