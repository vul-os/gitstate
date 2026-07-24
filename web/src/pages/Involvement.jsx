/**
 * Involvement — who touches which repo, in both directions.
 *
 * The same underlying join (commit authorship × repo) read two ways: grouped
 * by repo ("who works on this?") or grouped by person ("what do they touch?").
 * A SegmentedControl swaps the grouping without a second round-trip — both
 * shapes come back in one `/api/involvement` response.
 */
import { useMemo, useState } from 'react'
import { Users, FolderGit2, Bot, Layers } from 'lucide-react'
import { Card } from '../components/ui/Card.jsx'
import { Badge } from '../components/ui/Badge.jsx'
import { StatCard } from '../components/ui/StatCard.jsx'
import { BarList } from '../components/ui/BarList.jsx'
import { SegmentedControl } from '../components/ui/SegmentedControl.jsx'
import { PageHeader, Spinner, ErrorState, EmptyState } from '../components/common.jsx'
import { useAsync } from '../lib/hooks.js'
import { listRepos, involvement } from '../lib/api.js'
import { RANGES } from '../lib/analyticsView.js'

const VIEWS = [
  { value: 'repo', label: 'By repo' },
  { value: 'person', label: 'By person' },
]

// A guardrail, not a real ceiling — a huge org shouldn't render hundreds of
// BarLists at once. The UI says so rather than silently trimming the list.
const MAX_CARDS = 100

async function load(days, repoId) {
  const [repos, inv] = await Promise.all([
    listRepos(),
    involvement({ days, repo_id: repoId || undefined }),
  ])
  return { repos, inv }
}

/** Shares come back as 0..1 floats — always render as a whole-number percent. */
function pct(x) {
  if (x == null || !isFinite(x)) return '—'
  return `${Math.round(x * 100)}%`
}

function Panel({ title, subtitle, action, children }) {
  return (
    <Card padding="lg">
      <div className="mb-4 flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="truncate text-sm font-semibold text-[var(--text)]">{title}</h2>
          {subtitle && <p className="mt-0.5 text-xs text-[var(--text-faint)]">{subtitle}</p>}
        </div>
        {action}
      </div>
      {children}
    </Card>
  )
}

function AgentBadge() {
  return (
    <span title="agent identity" className="shrink-0 text-[var(--brand-indigo)]">
      <Bot size={13} />
    </span>
  )
}

function RepoCard({ repo }) {
  const items = useMemo(
    () => (repo.contributors ?? []).map((c) => ({
      key: c.email,
      label: c.name || c.email || '—',
      value: c.share ?? 0,
      badge: c.is_agent ? <AgentBadge /> : null,
    })),
    [repo],
  )
  return (
    <Panel
      title={repo.slug || '—'}
      subtitle={`${(repo.commits ?? 0).toLocaleString()} commits in range`}
      action={<Badge color="teal">{repo.contributors?.length ?? 0} contributors</Badge>}
    >
      <BarList items={items} format={pct} emptyLabel="No commits in this range" />
    </Panel>
  )
}

function PersonCard({ person }) {
  const items = useMemo(
    () => (person.repos ?? []).map((r) => ({
      key: r.repo_id,
      label: r.slug || '—',
      value: r.share ?? 0,
    })),
    [person],
  )
  return (
    <Panel
      title={
        <span className="flex items-center gap-2">
          <span className="truncate">{person.name || person.email || '—'}</span>
          {person.is_agent && <Badge color="indigo">agent</Badge>}
        </span>
      }
      subtitle={person.email || undefined}
      action={
        <div className="flex shrink-0 flex-col items-end gap-0.5">
          <span className="font-mono text-xs tabular-nums text-[var(--text)]">
            {(person.total_commits ?? 0).toLocaleString()} commits
          </span>
          <span className="font-mono text-[11px] tabular-nums text-[var(--text-faint)]">
            {person.repo_count ?? person.repos?.length ?? 0} repos
          </span>
        </div>
      }
    >
      <BarList items={items} format={pct} emptyLabel="No repo activity in this range" />
    </Panel>
  )
}

export default function Involvement() {
  const [days, setDays] = useState(90)
  const [repoId, setRepoId] = useState('')
  const [view, setView] = useState('repo')
  const { data, loading, error, reload } = useAsync(() => load(days, repoId), [days, repoId])

  const repos = data?.repos ?? []
  // Memoized so the `?? []` fallback doesn't create a fresh array identity on
  // every render — avgReposPerPerson's useMemo below depends on invPeople.
  const invRepos = useMemo(() => data?.inv?.repos ?? [], [data])
  const invPeople = useMemo(() => data?.inv?.people ?? [], [data])

  const avgReposPerPerson = useMemo(() => {
    if (!invPeople.length) return 0
    const total = invPeople.reduce((sum, p) => sum + (p.repo_count ?? p.repos?.length ?? 0), 0)
    return total / invPeople.length
  }, [invPeople])

  if (loading) return <div><PageHeader title="Involvement" /><Spinner /></div>
  if (error) return <div><PageHeader title="Involvement" /><ErrorState error={error} onRetry={reload} /></div>

  const empty = invRepos.length === 0 && invPeople.length === 0
  const activeCount = view === 'repo' ? invRepos.length : invPeople.length

  return (
    <div>
      <PageHeader
        title="Involvement"
        subtitle="Who touches which repo, both directions — derived from commit authorship, not a self-reported org chart."
      />

      {/* Filters live in one row above the cards they drive, matching Insights. */}
      <Card padding="md" className="mb-5 flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap items-center gap-3">
          <SegmentedControl options={RANGES} value={days} onChange={setDays} label="Time range" />
          <label className="flex items-center gap-2">
            <span className="sr-only">Repository</span>
            <select
              value={repoId}
              onChange={(e) => setRepoId(e.target.value)}
              className="rounded-[var(--radius-btn)] border border-[var(--border)] bg-[var(--bg-surface2)] px-2.5 py-1.5 font-mono text-[11px] text-[var(--text)] outline-none focus-visible:ring-2 focus-visible:ring-[var(--brand-teal)]"
            >
              <option value="">All repos</option>
              {repos.map((r) => (
                <option key={r.id} value={r.id}>{r.slug}</option>
              ))}
            </select>
          </label>
        </div>
        <SegmentedControl options={VIEWS} value={view} onChange={setView} label="Group by" />
      </Card>

      {empty ? (
        <EmptyState
          icon={<Users size={22} />}
          title="No involvement in this range"
          description="Scan a repo to derive commit authorship — this view fills in from git history, not from entered roles."
        />
      ) : (
        <>
          <div className="mb-5 grid grid-cols-2 gap-3 md:grid-cols-3">
            <StatCard label="People" value={invPeople.length} accent="var(--chart-5)" icon={<Users size={14} />} />
            <StatCard label="Repos" value={invRepos.length} accent="var(--chart-2)" icon={<FolderGit2 size={14} />} />
            <StatCard
              label="Avg repos / person" value={avgReposPerPerson.toFixed(1)}
              accent="var(--chart-4)" icon={<Layers size={14} />}
            />
          </div>

          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            {view === 'repo'
              ? invRepos.slice(0, MAX_CARDS).map((r) => <RepoCard key={r.repo_id} repo={r} />)
              : invPeople.slice(0, MAX_CARDS).map((p) => <PersonCard key={p.email} person={p} />)}
          </div>

          {activeCount > MAX_CARDS && (
            <p className="mt-3 text-center text-xs text-[var(--text-faint)]">
              Showing the first {MAX_CARDS} of {activeCount} — narrow the filter to see the rest.
            </p>
          )}
        </>
      )}
    </div>
  )
}
