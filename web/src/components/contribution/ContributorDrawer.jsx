/**
 * ContributorDrawer — slide-in evidence panel. The trust mechanism.
 *
 * Fetches /api/contribution/{userId} and lays out, per dimension: the live score,
 * the raw numbers behind it, and the actual evidence list (merged PRs, reviews,
 * revert commits) so every figure is traceable back to git.
 */
import { useEffect } from 'react'
import { useContributionMember, DIMENSIONS, dimColor } from '../../lib/useContribution.js'
import { Avatar, CompositeRing, Radar, AuthorshipBar } from './parts.jsx'
import { fmtNum, relTime, clamp01to100 } from './helpers.js'
import { Badge, Button } from '../ui/index.js'
import { KudosBadge } from './Kudos.jsx'
import { X, Bot, GitMerge, GitPullRequest, Undo2, Gauge, ExternalLink, Sprout, Bug, Heart } from 'lucide-react'

function Spinner({ size = 16 }) {
  return (
    <svg className="animate-spin" width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="var(--brand-teal)" strokeWidth="2">
      <path d="M21 12a9 9 0 1 1-6.219-8.56" strokeLinecap="round" />
    </svg>
  )
}

// raw-number rows per dimension (label/value pairs from member.dimensions[*].raw)
const RAW_FIELDS = {
  shipped: [
    ['mergedPRs', 'Merged PRs'],
    ['issuesClosed', 'Issues closed'],
    ['featuresShipped', 'Features shipped'],
  ],
  review: [['reviewsDone', 'Reviews given']],
  effort: [['effortPoints', 'Effort points']],
  quality: [
    ['reverts', 'Reverts'],
    ['bugsIntroduced', 'Bugs (SZZ)'],
    ['testCoupling', 'Tested %', (v) => (v == null ? '—' : `${Math.round(Number(v) * 100)}%`)],
    ['avgCycleHours', 'Avg cycle (h)', (v) => (v == null ? '—' : Number(v).toFixed(1))],
  ],
  ownership: [['areasOwned', 'Areas owned']],
  durability: [
    ['survivingLines', 'Surviving lines'],
    ['authoredLines', 'Authored lines'],
    ['survivalPct', 'Still live', (v) => (v == null ? '—' : `${Math.round(Number(v) * 100)}%`)],
  ],
}

const EVIDENCE_ICON = {
  shipped: GitMerge,
  review: GitPullRequest,
  quality: Undo2,
  effort: Gauge,
  durability: Sprout,
}

function RawStat({ label, value }) {
  return (
    <div className="rounded-[var(--radius-badge)] border border-[var(--border)] bg-[var(--bg)] px-3 py-2">
      <div className="text-[10px] font-mono uppercase tracking-wide text-[var(--text-faint)] truncate">{label}</div>
      <div className="text-base font-display font-semibold text-[var(--text)] tabular-nums mt-0.5">{value}</div>
    </div>
  )
}

function EvidenceList({ dimKey, items }) {
  const Icon = EVIDENCE_ICON[dimKey] ?? GitMerge
  if (!items || items.length === 0) {
    return <p className="text-[11px] text-[var(--text-faint)] italic px-1">No traceable items in this period.</p>
  }
  return (
    <ul className="space-y-1">
      {items.slice(0, 12).map((ev, i) => (
        <li
          key={i}
          className="flex items-start gap-2.5 rounded-[var(--radius-badge)] px-2.5 py-2 hover:bg-[var(--bg-surface2)] transition-colors"
        >
          <Icon size={13} className="mt-0.5 shrink-0" style={{ color: dimColor(dimKey, 62) }} />
          <div className="min-w-0 flex-1">
            <p className="text-[12px] text-[var(--text-dim)] leading-snug break-words">
              {ev.title || ev.message || 'Untitled'}
            </p>
            <p className="text-[10px] font-mono text-[var(--text-faint)] mt-0.5 truncate">
              {[ev.repo, relTime(ev.at)].filter(Boolean).join(' · ')}
            </p>
          </div>
        </li>
      ))}
      {items.length > 12 && (
        <li className="text-[10px] font-mono text-[var(--text-faint)] px-2.5 pt-1">+{items.length - 12} more</li>
      )}
    </ul>
  )
}

// ── durability evidence — per-repo line survival, with a survival % bar ───────

function shortSha(s) {
  return s ? String(s).slice(0, 7) : '—'
}

function DurabilityEvidence({ items }) {
  if (!items || items.length === 0) {
    return <p className="text-[11px] text-[var(--text-faint)] italic px-1">No blame-survival data in this period.</p>
  }
  return (
    <ul className="space-y-2">
      {items.slice(0, 12).map((it, i) => {
        const surviving = Math.max(0, it.survivingLines ?? 0)
        const authored = Math.max(0, it.authoredLines ?? 0)
        const pct = authored > 0 ? Math.round((surviving / authored) * 100) : 0
        return (
          <li key={i} className="rounded-[var(--radius-badge)] px-2.5 py-2 hover:bg-[var(--bg-surface2)] transition-colors">
            <div className="flex items-center justify-between gap-2">
              <div className="flex items-center gap-2 min-w-0">
                <Sprout size={13} className="shrink-0" style={{ color: dimColor('durability', 62) }} />
                <span className="text-[12px] text-[var(--text-dim)] font-mono truncate">{it.repo || 'repo'}</span>
              </div>
              <span className="text-[11px] font-mono tabular-nums shrink-0" style={{ color: dimColor('durability', 62) }}>{pct}%</span>
            </div>
            <div className="mt-1.5 h-1.5 w-full rounded-full bg-[var(--bg-surface3)] overflow-hidden">
              <div
                className="h-full rounded-full"
                style={{
                  width: `${pct}%`,
                  background: `linear-gradient(90deg, ${dimColor('durability', 52)}, ${dimColor('durability', 66)})`,
                  transition: 'width 0.45s cubic-bezier(0.22,1,0.36,1)',
                }}
              />
            </div>
            <p className="text-[10px] font-mono text-[var(--text-faint)] mt-1 tabular-nums">
              {fmtNum(surviving)} of {fmtNum(authored)} authored lines still live
            </p>
          </li>
        )
      })}
      {items.length > 12 && (
        <li className="text-[10px] font-mono text-[var(--text-faint)] px-2.5 pt-1">+{items.length - 12} more</li>
      )}
    </ul>
  )
}

// ── SZZ bug-introductions — fix↔introduced sha pairs the member authored ───────

function BugIntroEvidence({ items }) {
  if (!items || items.length === 0) {
    return <p className="text-[11px] text-[var(--text-faint)] italic px-1">No bug-introducing changes traced (SZZ) — clean record.</p>
  }
  return (
    <ul className="space-y-1">
      {items.slice(0, 12).map((it, i) => (
        <li
          key={i}
          className="flex items-start gap-2.5 rounded-[var(--radius-badge)] px-2.5 py-2 hover:bg-[var(--bg-surface2)] transition-colors"
        >
          <Bug size={13} className="mt-0.5 shrink-0 text-red-400/80" />
          <div className="min-w-0 flex-1">
            <p className="text-[12px] text-[var(--text-dim)] leading-snug font-mono">
              <span className="text-[var(--text-faint)]">introduced</span> {shortSha(it.introducedSha)}
              <span className="text-[var(--text-faint)]"> → fixed</span> {shortSha(it.fixSha)}
            </p>
            <p className="text-[10px] font-mono text-[var(--text-faint)] mt-0.5 tabular-nums">
              {fmtNum(it.lines ?? 0)} line{(it.lines ?? 0) === 1 ? '' : 's'} later corrected
            </p>
          </div>
        </li>
      ))}
      {items.length > 12 && (
        <li className="text-[10px] font-mono text-[var(--text-faint)] px-2.5 pt-1">+{items.length - 12} more</li>
      )}
    </ul>
  )
}

function EvidenceHeader({ children }) {
  return (
    <div className="flex items-center gap-1.5 mb-1.5">
      <ExternalLink size={11} className="text-[var(--text-faint)]" />
      <span className="text-[10px] font-mono uppercase tracking-wide text-[var(--text-faint)]">{children}</span>
    </div>
  )
}

function DimensionBlock({ dim, dimData, evidence }) {
  const score = clamp01to100(dimData?.score)
  const raw = dimData?.raw ?? {}
  const fields = RAW_FIELDS[dim.key] ?? []
  const evItems = evidence?.[dim.key]
  return (
    <section className="rounded-[var(--radius-card)] border border-[var(--border)] bg-[var(--bg-surface)] overflow-hidden">
      <div className="flex items-center justify-between gap-3 px-4 py-3 border-b border-[var(--border)]">
        <div className="flex items-center gap-2.5 min-w-0">
          <span className="h-2.5 w-2.5 rounded-full shrink-0" style={{ background: dimColor(dim.key, 60) }} />
          <div className="min-w-0">
            <h4 className="text-[13px] font-semibold text-[var(--text)]">{dim.label}</h4>
            <p className="text-[10px] text-[var(--text-faint)] truncate">{dim.blurb}</p>
          </div>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          <div className="h-1.5 w-16 rounded-full bg-[var(--bg-surface3)] overflow-hidden">
            <div className="h-full rounded-full" style={{ width: `${score}%`, background: dimColor(dim.key, 60) }} />
          </div>
          <span className="font-mono text-[13px] font-semibold tabular-nums w-7 text-right" style={{ color: dimColor(dim.key, 62) }}>
            {Math.round(score)}
          </span>
        </div>
      </div>
      <div className="p-4 space-y-3">
        {fields.length > 0 && (
          <div className="grid grid-cols-2 sm:grid-cols-3 gap-2">
            {fields.map(([key, label, fmt]) => (
              <RawStat key={key} label={label} value={fmt ? fmt(raw[key]) : fmtNum(raw[key])} />
            ))}
          </div>
        )}
        {dim.key === 'durability' ? (
          <div>
            <EvidenceHeader>Line survival · by repo</EvidenceHeader>
            <DurabilityEvidence items={evidence?.durability} />
          </div>
        ) : dim.key === 'quality' ? (
          <>
            <div>
              <EvidenceHeader>Revert commits</EvidenceHeader>
              <EvidenceList dimKey={dim.key} items={evItems} />
            </div>
            <div>
              <EvidenceHeader>Bug-introductions · SZZ</EvidenceHeader>
              <BugIntroEvidence items={evidence?.bugIntroductions} />
            </div>
          </>
        ) : dim.key !== 'ownership' ? (
          <div>
            <EvidenceHeader>Evidence</EvidenceHeader>
            <EvidenceList dimKey={dim.key} items={evItems} />
          </div>
        ) : null}
      </div>
    </section>
  )
}

export function ContributorDrawer({ userId, range, onClose, kudosCount = 0, onGiveKudos }) {
  const { data, loading, error } = useContributionMember(userId, range)

  useEffect(() => {
    const onKey = (e) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  const member = data
  const isBot = member?.isAgentBot

  return (
    <>
      <div
        className="fixed inset-0 z-40 animate-[fadeIn_0.2s_ease]"
        style={{ background: 'rgba(11,17,32,0.6)', backdropFilter: 'blur(2px)' }}
        onClick={onClose}
        aria-hidden
      />
      <aside
        className="fixed right-0 top-0 h-full z-50 flex flex-col bg-[var(--bg)] border-l border-[var(--border)] w-full max-w-[560px] shadow-[var(--shadow-float)]"
        style={{ animation: 'slideInRight 0.28s cubic-bezier(0.22,1,0.36,1)' }}
        role="dialog"
        aria-modal="true"
        aria-label="Contributor evidence"
      >
        <style>{`@keyframes slideInRight{from{transform:translateX(24px);opacity:0}to{transform:translateX(0);opacity:1}}@keyframes fadeIn{from{opacity:0}to{opacity:1}}`}</style>

        {/* header */}
        <div className="flex items-start justify-between gap-3 px-5 py-4 border-b border-[var(--border)] shrink-0">
          {loading && !member ? (
            <div className="flex items-center gap-2 text-sm text-[var(--text-faint)]"><Spinner /> Loading evidence…</div>
          ) : member ? (
            <div className="flex items-center gap-3 min-w-0">
              <Avatar name={member.name || member.email} isAgentBot={isBot} size={44} />
              <div className="min-w-0">
                <div className="flex items-center gap-1.5">
                  <h3 className="text-base font-semibold text-[var(--text)] truncate">{member.name || member.email}</h3>
                  {isBot && <Badge color="indigo"><Bot size={11} /> agent</Badge>}
                  <KudosBadge count={kudosCount} className="shrink-0" />
                </div>
                <p className="text-[11px] font-mono text-[var(--text-faint)] truncate">{member.email}</p>
              </div>
            </div>
          ) : (
            <h3 className="text-base font-semibold text-[var(--text)]">Contributor</h3>
          )}
          <div className="flex items-center gap-1.5 shrink-0">
          {member && !isBot && onGiveKudos && (
            <Button variant="ghost" size="xs" leftIcon={<Heart size={13} />} onClick={() => onGiveKudos(member.userId)}>
              Kudos
            </Button>
          )}
          <button
            onClick={onClose}
            className="shrink-0 p-1.5 rounded-lg text-[var(--text-faint)] hover:text-[var(--text)] hover:bg-[var(--bg-surface2)] transition-colors cursor-pointer"
            aria-label="Close"
          >
            <X size={18} />
          </button>
          </div>
        </div>

        {/* body */}
        <div className="flex-1 overflow-y-auto p-5 space-y-5">
          {error && (
            <div className="rounded-[var(--radius-card)] border border-red-500/20 bg-red-500/[0.04] p-4">
              <p className="text-sm text-red-400">{error}</p>
            </div>
          )}

          {member && (
            <>
              {/* summary: ring + radar + authorship */}
              <div className="flex flex-wrap items-center gap-6 rounded-[var(--radius-card)] border border-[var(--border)] bg-[var(--bg-surface)] p-5">
                <CompositeRing value={member.composite} size={96} stroke={7} />
                <Radar dimensions={member.dimensions} size={140} />
                <div className="flex-1 min-w-[180px] space-y-2">
                  <div className="flex items-baseline gap-2">
                    <span className="font-display text-2xl font-semibold text-[var(--text)] tabular-nums">
                      {Math.round(member.composite)}
                    </span>
                    <span className="text-xs text-[var(--text-faint)]">/ 100 composite</span>
                  </div>
                  <p className="text-[11px] text-[var(--text-faint)] leading-snug">
                    Composite reflects the org’s current weighting. Every score below
                    drills to its underlying git evidence.
                  </p>
                  <div className="pt-2">
                    <p className="text-[10px] font-mono uppercase tracking-wide text-[var(--text-faint)] mb-1">Authorship</p>
                    <AuthorshipBar authorship={member.authorship} />
                  </div>
                </div>
              </div>

              {/* per-dimension evidence */}
              {DIMENSIONS.map((dim) => (
                <DimensionBlock key={dim.key} dim={dim} dimData={member.dimensions?.[dim.key]} evidence={member.evidence} />
              ))}
            </>
          )}

          {loading && member && (
            <div className="flex items-center justify-center gap-2 py-3 text-xs text-[var(--text-faint)]"><Spinner size={13} /> Refreshing…</div>
          )}
        </div>
      </aside>
    </>
  )
}
