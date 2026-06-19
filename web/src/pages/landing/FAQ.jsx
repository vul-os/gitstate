/**
 * FAQ — honest answers to the questions a skeptical developer actually asks.
 * Accessible accordion (native disclosure semantics via button + aria-expanded).
 */
import { useState } from 'react'
import { Plus } from 'lucide-react'
import { Reveal } from '../../components/Reveal.jsx'
import { GradientText, Section, Container, Glow } from '../../components/ui/index.js'

function SectionLabel({ children }) {
  return (
    <span className="inline-flex items-center gap-2 text-[11px] font-mono uppercase tracking-[0.15em] text-[var(--brand-teal)] mb-4">
      <span className="w-3 h-px bg-[var(--brand-teal)]" aria-hidden="true" />
      {children}
      <span className="w-3 h-px bg-[var(--brand-teal)]" aria-hidden="true" />
    </span>
  )
}

const FAQS = [
  {
    q: 'How does it derive state without me updating tickets?',
    a: 'gitstate reads commits, branches, and pull-request lifecycle directly from your repos. An open PR is in progress, a merged PR is done, a stale branch is flagged. The board is a projection of git — there is nothing to drag between columns.',
  },
  {
    q: 'Is the effort sizing just an LLM guessing?',
    a: 'The model reads the actual diff — files touched, complexity, surface area — and is calibrated against your team\'s observed cycle time, not arbitrary story points. Every estimate links back to the commits it was derived from, so you can audit it. AI is included; there is no separate API key or per-token bill.',
  },
  {
    q: 'What about work that never touches git?',
    a: 'It is surfaced honestly. Native work items live beside git-derived ones, clearly marked. When an invoice line has no commit evidence, gitstate flags the gap for a human to fill — it never silently invents work to bill for.',
  },
  {
    q: 'Why is pricing per builder and not per seat?',
    a: 'You pay for the people who commit code. Clients, executives, and read-only reviewers are always free — invite as many stakeholders as you want. The seat tax that makes incumbents expensive simply does not exist here.',
  },
  {
    q: 'Can I self-host?',
    a: 'Yes. gitstate ships as a single binary plus a Postgres database — no Kubernetes, no fleet of services. The core is open source under AGPL-3.0, and a commercial enterprise license is available if AGPL does not fit.',
  },
  {
    q: 'Does it work with GitHub and GitLab at the same time?',
    a: 'Yes. Connect both via OAuth in under a minute. Issues sync two-way and are de-duplicated across platforms, so a multi-repo, multi-host team sees one coherent picture.',
  },
]

function FaqItem({ item, open, onToggle, id }) {
  return (
    <div
      className="rounded-[var(--radius-card)] overflow-hidden transition-colors duration-200"
      style={{
        background: open ? 'var(--bg-surface)' : 'transparent',
        border: `1px solid ${open ? 'var(--border2)' : 'var(--border)'}`,
        boxShadow: open ? 'var(--shadow-card)' : 'none',
      }}
    >
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={open}
        aria-controls={`${id}-panel`}
        id={`${id}-button`}
        className="w-full flex items-center justify-between gap-4 px-5 py-4 text-left group cursor-pointer"
      >
        <span
          className="font-display text-[15px] md:text-base font-semibold tracking-[-0.01em] transition-colors"
          style={{ color: open ? 'var(--text)' : 'var(--text-dim)' }}
        >
          {item.q}
        </span>
        <span
          className="shrink-0 w-7 h-7 rounded-lg flex items-center justify-center transition-all duration-300"
          style={{
            background: open ? 'rgba(45,212,191,0.12)' : 'var(--bg-surface2)',
            color: open ? '#2DD4BF' : 'var(--text-muted)',
            transform: open ? 'rotate(45deg)' : 'rotate(0deg)',
          }}
          aria-hidden="true"
        >
          <Plus size={15} strokeWidth={2} />
        </span>
      </button>
      <div
        id={`${id}-panel`}
        role="region"
        aria-labelledby={`${id}-button`}
        className="grid transition-all duration-300 ease-out"
        style={{ gridTemplateRows: open ? '1fr' : '0fr' }}
      >
        <div className="overflow-hidden">
          <p className="px-5 pb-5 text-sm leading-relaxed text-[var(--text-muted)] max-w-prose">
            {item.a}
          </p>
        </div>
      </div>
    </div>
  )
}

export default function FAQ() {
  const [openIdx, setOpenIdx] = useState(0)

  return (
    <Section py="2xl" className="relative overflow-hidden border-t border-[var(--border)]">
      <Glow variant="indigo" size={560} className="top-[10%] left-[10%] opacity-50" />
      <Glow variant="teal" size={520} className="bottom-[5%] right-[8%] opacity-40" />

      <Container size="md" className="relative z-10">
        <Reveal inView>
          <div className="text-center mb-12">
            <div className="flex justify-center">
              <SectionLabel>Questions</SectionLabel>
            </div>
            <h2 className="font-display text-3xl md:text-4xl font-semibold text-[var(--text)] tracking-[-0.025em] mb-3">
              The honest{' '}
              <GradientText as="span" className="font-display text-3xl md:text-4xl font-semibold">
                answers.
              </GradientText>
            </h2>
            <p className="text-base text-[var(--text-muted)] max-w-md mx-auto">
              No asterisks. Here is exactly how gitstate behaves.
            </p>
          </div>
        </Reveal>

        <Reveal inView delay={0.08}>
          <div className="flex flex-col gap-3">
            {FAQS.map((item, i) => (
              <FaqItem
                key={i}
                id={`faq-${i}`}
                item={item}
                open={openIdx === i}
                onToggle={() => setOpenIdx(openIdx === i ? -1 : i)}
              />
            ))}
          </div>
        </Reveal>
      </Container>
    </Section>
  )
}
