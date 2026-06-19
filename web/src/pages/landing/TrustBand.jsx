/**
 * TrustBand — honest "works with" social-proof strip.
 * No fake customer logos. Neutral, factual trust signals: the platforms it
 * connects to and the stack it runs on, rendered as a quiet monochrome row
 * that subtly brightens on hover.
 */
import { Database, Binary } from 'lucide-react'
import { Reveal } from '../../components/Reveal.jsx'
import { Section, Container } from '../../components/ui/index.js'

/* ── Brand glyphs (inline — lucide has no brand marks) ─────────────────────── */
function GitHubGlyph() {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor" className="h-5 w-5" aria-hidden="true">
      <path d="M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12" />
    </svg>
  )
}
function GitLabGlyph() {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor" className="h-5 w-5" aria-hidden="true">
      <path d="m23.6 9.6-.03-.08-3.27-8.53a.85.85 0 0 0-.84-.54.85.85 0 0 0-.8.6l-2.2 6.76H7.55l-2.2-6.76a.85.85 0 0 0-.81-.6.85.85 0 0 0-.83.54L.42 9.52l-.03.08a6.06 6.06 0 0 0 2.01 7l.01.01.03.02 4.97 3.72 2.46 1.86 1.5 1.13a1 1 0 0 0 1.21 0l1.5-1.13 2.46-1.86 5-3.74.01-.01a6.06 6.06 0 0 0 2.01-7Z" />
    </svg>
  )
}

const TRUST = [
  { glyph: GitHubGlyph,                                       label: 'GitHub' },
  { glyph: GitLabGlyph,                                       label: 'GitLab' },
  { glyph: () => <Database className="h-5 w-5" strokeWidth={1.6} aria-hidden="true" />, label: 'Postgres' },
  { glyph: () => <Binary className="h-5 w-5" strokeWidth={1.6} aria-hidden="true" />,   label: 'Single binary' },
]

export default function TrustBand() {
  return (
    <Section py="md" className="relative overflow-hidden border-t border-[var(--border)]">
      <Container size="lg" className="relative z-10">
        <Reveal inView>
          <div className="flex flex-col items-center gap-7">
            <p className="text-[11px] font-mono uppercase tracking-[0.2em] text-[var(--text-faint)]">
              Connects to your stack — not the other way around
            </p>
            <div className="flex flex-wrap items-center justify-center gap-x-10 gap-y-5 sm:gap-x-14">
              {TRUST.map(({ glyph: Glyph, label }) => (
                <span
                  key={label}
                  className="group inline-flex items-center gap-2.5 text-[var(--text-faint)] hover:text-[var(--text-dim)] transition-colors duration-200"
                >
                  <span className="opacity-70 group-hover:opacity-100 transition-opacity duration-200">
                    <Glyph />
                  </span>
                  <span className="text-sm font-medium tracking-tight">{label}</span>
                </span>
              ))}
            </div>
          </div>
        </Reveal>
      </Container>
    </Section>
  )
}
