/**
 * Involvement page — /involvement
 * Renders each person as a multi-dimension card.
 * Explicitly NOT a ranked leaderboard. NOT a single score.
 * Caption: "involvement across dimensions — not a productivity score."
 *
 * Data from GET /api/metrics/involvement?project=&period=
 */
import { useState } from 'react'
import { RefreshCw, Info, Users, GitMerge, Eye, FolderGit2 } from 'lucide-react'
import { useInvolvement } from '../lib/useInvolvement.js'
import { useProjects } from '../lib/useProjects.js'
import { Card, Badge, Button } from '../components/ui/index.js'
import { Reveal, RevealList } from '../components/Reveal.jsx'

function Initials({ name, email }) {
  const text = name
    ? name.split(' ').map(w => w[0]).join('').slice(0, 2).toUpperCase()
    : (email ?? '?').slice(0, 2).toUpperCase()
  return (
    <div className="w-10 h-10 rounded-full bg-gradient-to-br from-[var(--brand-teal)] to-[var(--brand-indigo)] flex items-center justify-center text-[12px] font-bold text-[#0B1120] select-none shrink-0">
      {text}
    </div>
  )
}

function DimBar({ label, value, max, color, icon: Icon }) {
  const pct = max > 0 ? Math.min(100, (value / max) * 100) : 0
  return (
    <div className="flex items-center gap-3">
      <span className="text-[10px] text-[var(--text-faint)] w-28 shrink-0 flex items-center gap-1.5 font-mono">
        {Icon && <Icon size={11} className="opacity-70" style={{ color }} />}
        {label}
      </span>
      <div className="flex-1 h-1.5 rounded-full bg-[var(--border)] overflow-hidden">
        <div
          className="h-full rounded-full transition-all duration-500"
          style={{ width: `${pct}%`, background: color }}
        />
      </div>
      <span className="text-[11px] font-mono text-[var(--text-dim)] w-8 text-right shrink-0 tabular-nums">{value}</span>
    </div>
  )
}

function ActivityDot({ active, lastActive }) {
  return (
    <div className="flex items-center gap-1.5" title={active ? 'Active recently' : 'Dormant'}>
      <span className="relative flex w-2 h-2 shrink-0">
        {active && <span className="absolute inline-flex h-full w-full rounded-full bg-green-400 opacity-50 animate-ping" />}
        <span className="relative inline-flex w-2 h-2 rounded-full" style={{ background: active ? '#22c55e' : 'var(--border2)' }} />
      </span>
      <span className="text-[10px] text-[var(--text-faint)]">
        {active ? 'active' : 'dormant'}
        {lastActive ? ` · ${new Date(lastActive).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}` : ''}
      </span>
    </div>
  )
}

function InvolvementCard({ member, maxes }) {
  const {
    name, email,
    featuresShipped = 0,
    reviewsDone = 0,
    areasOwned = [],
    activeRecently = false,
    lastActive,
  } = member

  return (
    <Card padding="md" hoverable className="flex flex-col gap-4">
      {/* Identity row */}
      <div className="flex items-start gap-3">
        <Initials name={name} email={email} />
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-sm font-semibold text-[var(--text)] truncate">{name ?? email}</span>
            <ActivityDot active={activeRecently} lastActive={lastActive} />
          </div>
          {name && email && (
            <span className="text-xs text-[var(--text-faint)] truncate block">{email}</span>
          )}
        </div>
      </div>

      {/* Multi-dimension bars — the "texture" view, never a single score */}
      <div className="space-y-2.5">
        <DimBar label="Features shipped" value={featuresShipped} max={maxes.featuresShipped} color="var(--brand-teal)" icon={GitMerge} />
        <DimBar label="Reviews done" value={reviewsDone} max={maxes.reviewsDone} color="var(--brand-indigo)" icon={Eye} />
      </div>

      {/* Areas owned */}
      {areasOwned.length > 0 ? (
        <div>
          <span className="text-[10px] text-[var(--text-faint)] uppercase tracking-widest mb-1.5 flex items-center gap-1 font-mono">
            <FolderGit2 size={10} /> Areas owned
          </span>
          <div className="flex flex-wrap gap-1.5">
            {areasOwned.map(area => (
              <Badge key={area} color="indigo">{area}</Badge>
            ))}
          </div>
        </div>
      ) : (
        <div className="text-[10px] text-[var(--text-faint)] font-mono">No owned areas in this window</div>
      )}
    </Card>
  )
}

const PERIODS = [
  { id: '7d',  label: '7 days' },
  { id: '30d', label: '30 days' },
  { id: '90d', label: '90 days' },
]

export default function Involvement() {
  const { projects } = useProjects()
  const [project, setProject] = useState('')
  const [period, setPeriod] = useState('30d')

  const { members, loading, error, refetch } = useInvolvement({ project, period })

  // Compute per-dimension maxes for relative bars — never a composite score
  const maxes = {
    featuresShipped: Math.max(1, ...members.map(m => m.featuresShipped ?? 0)),
    reviewsDone:     Math.max(1, ...members.map(m => m.reviewsDone ?? 0)),
  }

  return (
    <div className="max-w-5xl space-y-6">
      {/* Header */}
      <Reveal>
        <div>
          <h1 className="font-display text-2xl font-semibold text-[var(--text)] tracking-tight">Involvement</h1>
          {/* Honest caption — not a score */}
          <p className="text-sm text-[var(--text-faint)] mt-1">
            Involvement across dimensions — not a productivity score.
          </p>
        </div>
      </Reveal>

      {/* Principle callout — texture, not a number */}
      <Reveal delay={0.05}>
        <Card className="border-[var(--brand-indigo)]/15 bg-gradient-to-r from-[var(--brand-indigo)]/[0.04] to-[var(--brand-teal)]/[0.03]" padding="md">
          <div className="flex items-start gap-3">
            <Info size={15} className="mt-0.5 shrink-0 text-[var(--brand-indigo)]" />
            <p className="text-xs text-[var(--text-muted)] leading-relaxed">
              This view shows <strong className="text-[#c7d2fe]">texture across multiple dimensions</strong> — features shipped, review load, areas owned, and activity — so seniors, mentors, and reviewers are visible alongside feature authors. No single number, no ranking, no formula.
            </p>
          </div>
        </Card>
      </Reveal>

      {/* Filters */}
      <Reveal delay={0.08}>
        <div className="flex flex-wrap gap-4 items-center">
          {/* Period */}
          <div className="flex items-center rounded-[var(--radius-btn)] p-0.5 gap-0.5 bg-[var(--bg)] border border-[var(--border)]">
            {PERIODS.map(p => (
              <button
                key={p.id}
                onClick={() => setPeriod(p.id)}
                className={[
                  'px-3 py-1.5 rounded-[6px] text-xs font-medium transition-all duration-150',
                  period === p.id
                    ? 'bg-[var(--bg-surface2)] text-[var(--brand-teal)]'
                    : 'text-[var(--text-faint)] hover:text-[var(--text-muted)]',
                ].join(' ')}
              >
                {p.label}
              </button>
            ))}
          </div>

          {/* Project filter */}
          {projects.length > 0 && (
            <select
              className="bg-[var(--bg)] text-xs text-[var(--text-muted)] rounded-[var(--radius-btn)] px-3 py-2 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/50 transition-colors cursor-pointer"
              value={project}
              onChange={e => setProject(e.target.value)}
            >
              <option value="">All projects</option>
              {projects.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
            </select>
          )}

          <Button
            variant="ghost"
            size="sm"
            onClick={refetch}
            disabled={loading}
            leftIcon={<RefreshCw size={13} className={loading ? 'animate-spin' : ''} />}
          >
            Refresh
          </Button>

          {!loading && members.length > 0 && (
            <span className="flex items-center gap-1.5 text-xs text-[var(--text-faint)] font-mono ml-auto">
              <Users size={13} /> {members.length} member{members.length !== 1 ? 's' : ''}
            </span>
          )}
        </div>
      </Reveal>

      {/* Error */}
      {error && (
        <Card className="border-red-500/20 bg-red-500/[0.04]">
          <p className="text-sm text-red-400">{error} — the backend may not be running yet.</p>
        </Card>
      )}

      {/* Loading skeleton */}
      {loading && !members.length && (
        <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
          {Array.from({ length: 6 }).map((_, i) => (
            <div
              key={i}
              className="rounded-[var(--radius-card)] p-5 h-44 animate-pulse bg-[var(--bg-surface)] border border-[var(--border)]"
            />
          ))}
        </div>
      )}

      {/* Cards */}
      {!loading && members.length > 0 && (
        <RevealList className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4" staggerDelay={0.04}>
          {members.map(m => (
            <InvolvementCard key={m.userId ?? m.email} member={m} maxes={maxes} />
          ))}
        </RevealList>
      )}

      {/* Empty */}
      {!loading && !error && members.length === 0 && (
        <Card padding="xl" className="border-dashed text-center">
          <div className="w-12 h-12 rounded-[var(--radius-card)] flex items-center justify-center mx-auto mb-4 bg-[var(--brand-indigo)]/[0.06] border border-[var(--brand-indigo)]/15">
            <Users size={22} className="text-[var(--brand-indigo)]" />
          </div>
          <h3 className="text-sm font-semibold text-[var(--text)] mb-1">No involvement data yet</h3>
          <p className="text-xs text-[var(--text-faint)] max-w-xs mx-auto">
            Sync a repository to derive feature shipping and review activity across your team.
          </p>
        </Card>
      )}
    </div>
  )
}
