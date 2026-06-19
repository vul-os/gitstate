/**
 * ContributorCard — one row in the roster.
 *
 * Shows avatar + identity, the live composite ring, a compact 6-axis radar,
 * stacked dimension bars, and the human/agent authorship split. Clicking opens
 * the evidence drawer. `rank` and `delta` reflect the *live* (re-weighted) order.
 */
import { Card } from '../ui/index.js'
import { Avatar, CompositeRing, Radar, DimensionBars, AuthorshipBar } from './parts.jsx'
import { Bot, ChevronRight, Bug, FlaskConical } from 'lucide-react'

/**
 * Tiny captions making the multi-signal quality axis legible at a glance:
 * bug-introductions (SZZ) and the share of touches that are tests. Only renders
 * when the backend has supplied the raw numbers.
 */
function QualitySignals({ dimensions }) {
  const raw = dimensions?.quality?.raw
  if (!raw) return null
  const szz = raw.bugsIntroduced
  const tested = raw.testCoupling
  const hasSzz = szz != null
  const hasTested = tested != null
  if (!hasSzz && !hasTested) return null
  return (
    <div className="flex items-center gap-3 text-[10px] font-mono text-[var(--text-faint)]">
      {hasSzz && (
        <span className="inline-flex items-center gap-1" title="Changes later implicated in a bug-fix (SZZ)">
          <Bug size={11} className={szz > 0 ? 'text-red-400/80' : 'text-[var(--text-faint)]'} />
          <span className="tabular-nums">{szz} SZZ</span>
        </span>
      )}
      {hasTested && (
        <span className="inline-flex items-center gap-1" title="Share of file-touches that are tests">
          <FlaskConical size={11} className="text-[var(--brand-teal)]" />
          <span className="tabular-nums">{Math.round(tested * 100)}% tested</span>
        </span>
      )}
    </div>
  )
}

export function ContributorCard({ member, rank, liveComposite, delta, onOpen }) {
  const isBot = member.isAgentBot
  return (
    <Card
      padding="none"
      hoverable
      className="group cursor-pointer"
      onClick={() => onOpen(member.userId)}
      role="button"
      tabIndex={0}
      onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onOpen(member.userId) } }}
    >
      <div className="flex flex-col sm:flex-row sm:items-center gap-5 p-5">
        {/* rank + identity */}
        <div className="flex items-center gap-4 min-w-0 sm:w-[240px] shrink-0">
          <span className="font-mono text-[13px] tabular-nums text-[var(--text-faint)] w-5 text-right shrink-0">
            {rank != null ? rank : '—'}
          </span>
          <Avatar name={member.name || member.email} isAgentBot={isBot} size={40} />
          <div className="min-w-0">
            <div className="flex items-center gap-1.5">
              <p className="text-sm font-semibold text-[var(--text)] truncate">{member.name || member.email}</p>
              {isBot && <Bot size={13} className="text-[var(--brand-indigo)] shrink-0" />}
            </div>
            <p className="text-[11px] text-[var(--text-faint)] truncate font-mono">{member.email}</p>
          </div>
        </div>

        {/* composite */}
        <div className="flex items-center justify-center sm:justify-start shrink-0">
          <CompositeRing value={liveComposite} delta={delta} size={76} stroke={6} />
        </div>

        {/* radar */}
        <div className="hidden lg:flex items-center justify-center shrink-0">
          <Radar dimensions={member.dimensions} size={116} />
        </div>

        {/* bars + authorship */}
        <div className="flex-1 min-w-0 space-y-3">
          <DimensionBars dimensions={member.dimensions} compact />
          <QualitySignals dimensions={member.dimensions} />
          <div className="pt-1">
            <AuthorshipBar authorship={member.authorship} />
          </div>
        </div>

        <ChevronRight size={16} className="hidden sm:block text-[var(--text-faint)] group-hover:text-[var(--brand-teal)] transition-colors shrink-0" />
      </div>
    </Card>
  )
}
