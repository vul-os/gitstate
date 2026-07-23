import { useMemo } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { RefreshCw, ArrowLeft, Sparkles, Scale, GitCommitHorizontal, Users, Timer, Plus } from 'lucide-react'
import { Card } from '../components/ui/Card.jsx'
import { Button } from '../components/ui/Button.jsx'
import { Badge } from '../components/ui/Badge.jsx'
import { StatCard } from '../components/ui/StatCard.jsx'
import { Heatmap } from '../components/ui/Heatmap.jsx'
import { TrendChart } from '../components/ui/TrendChart.jsx'
import { PageHeader, Spinner, ErrorState, EmptyState, MetricPill } from '../components/common.jsx'
import { useAsync, useAction } from '../lib/hooks.js'
import {
  listRepos, projectState, contributions, workItems, contributors,
  scanRepo, classify, effort, analytics,
} from '../lib/api.js'
import { commitSeries, cycleSeries, sparkOf, formatHours, compact } from '../lib/analyticsView.js'

const DIMS = [
  ['shipped', 'Shipped'],
  ['review', 'Review'],
  ['effort', 'Effort'],
  ['quality', 'Quality'],
  ['ownership', 'Ownership'],
  ['durability', 'Durability'],
]

async function loadRepo(id) {
  const [repos, state, contribs, items, people, stats] = await Promise.all([
    listRepos(),
    projectState(id).catch(() => null),
    contributions(id, {}).catch(() => []),
    workItems(id, {}).catch(() => []),
    contributors().catch(() => []),
    analytics({ repo_id: id, days: 365 }).catch(() => null),
  ])
  const repo = repos.find((r) => r.id === id) || null
  return { repo, state, contribs, items, people, stats }
}

/** Per-repo activity: headline scalars, the heatmap, and the two trends. */
function ActivityPanel({ stats }) {
  const t = stats?.totals
  if (!t || !t.commits) {
    return (
      <EmptyState
        title="No commit history cached"
        description="Scan this repo to walk its git history — the heatmap and trends fill in from the derived commit cache."
      />
    )
  }
  return (
    <div className="flex flex-col gap-4">
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <StatCard
          label="Commits" value={compact(t.commits)} sublabel={`${t.active_days} active days`}
          accent="var(--chart-1)" icon={<GitCommitHorizontal size={14} />} spark={sparkOf(stats, 'commits')}
        />
        <StatCard
          label="Contributors" value={t.contributors} accent="var(--chart-5)" icon={<Users size={14} />}
        />
        <StatCard
          label="Cycle p50" value={formatHours(t.cycle_p50_hours)}
          sublabel={`p90 ${formatHours(t.cycle_p90_hours)}`} accent="var(--chart-3)" icon={<Timer size={14} />}
        />
        <StatCard
          label="Net lines" value={compact(t.net_lines)}
          sublabel={`+${compact(t.additions)} / −${compact(t.deletions)}`}
          accent="var(--ok)" icon={<Plus size={14} />}
        />
      </div>

      <Card padding="lg">
        <h3 className="mb-4 text-sm font-semibold text-[var(--text)]">Contribution heatmap</h3>
        <Heatmap days={stats.heatmap ?? []} />
      </Card>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <Card padding="lg">
          <h3 className="mb-4 text-sm font-semibold text-[var(--text)]">Commits per week</h3>
          <TrendChart
            series={[{ key: 'c', label: 'Commits', color: 'var(--chart-1)', points: commitSeries(stats) }]}
            area height={180}
          />
        </Card>
        <Card padding="lg">
          <h3 className="mb-4 text-sm font-semibold text-[var(--text)]">Cycle time per merged PR</h3>
          <TrendChart
            series={[{ key: 'x', label: 'Lead time', color: 'var(--chart-3)', points: cycleSeries(stats) }]}
            area height={180} yFormat={formatHours}
            emptyLabel="No merged pull requests yet"
          />
        </Card>
      </div>
    </div>
  )
}

function DimBar({ value }) {
  const v = Math.max(0, Math.min(100, value ?? 0))
  return (
    <div className="flex items-center gap-2">
      <div className="h-1.5 w-16 overflow-hidden rounded-full bg-[var(--bg-surface3)]">
        <div className="h-full rounded-full bg-[var(--brand-teal)]" style={{ width: `${v}%` }} />
      </div>
      <span className="w-8 text-right font-mono text-xs tabular-nums text-[var(--text-muted)]">{v.toFixed(0)}</span>
    </div>
  )
}

function ProjectStatePanel({ state }) {
  if (!state) {
    return (
      <EmptyState
        title="No derived state yet"
        description="Scan this repo to walk its git history (and forge, if connected) and derive project state."
      />
    )
  }
  const cells = [
    ['Head', state.head_sha ? state.head_sha.slice(0, 8) : '—'],
    ['Open PRs', state.open_prs],
    ['Merged PRs', state.merged_prs],
    ['Draft PRs', state.draft_prs],
    ['Open issues', state.open_issues],
    ['Closed issues', state.closed_issues],
    ['In progress', state.in_progress],
    ['Done', state.done],
    ['Cycle p50 (h)', state.cycle_time_p50_hours != null ? state.cycle_time_p50_hours.toFixed(1) : '—'],
    ['Cycle p90 (h)', state.cycle_time_p90_hours != null ? state.cycle_time_p90_hours.toFixed(1) : '—'],
    ['Change-fail rate', state.change_failure_rate != null ? `${(state.change_failure_rate * 100).toFixed(0)}%` : '—'],
  ]
  return (
    <Card padding="lg">
      <div className="grid grid-cols-2 gap-5 sm:grid-cols-3 lg:grid-cols-4">
        {cells.map(([label, value]) => <MetricPill key={label} label={label} value={value} />)}
      </div>
      {state.warnings?.length > 0 && (
        <div className="mt-4 flex flex-wrap gap-2">
          {state.warnings.map((w, i) => <Badge key={i} color="yellow">{w}</Badge>)}
        </div>
      )}
    </Card>
  )
}

function ContributionsTable({ contribs, people }) {
  const nameFor = useMemo(() => {
    const m = new Map((people || []).map((p) => [p.id, p]))
    return (id) => m.get(id) || null
  }, [people])

  if (!contribs?.length) {
    return <EmptyState title="No contributions derived" description="Scan the repo to compute the six gaming-resistant dimensions per contributor." />
  }

  return (
    <Card padding="none" className="overflow-x-auto">
      <table className="w-full min-w-[720px] text-sm">
        <thead>
          <tr className="border-b border-[var(--border)] text-left">
            <th className="px-4 py-3 font-medium text-[var(--text-faint)]">Contributor</th>
            {DIMS.map(([, label]) => (
              <th key={label} className="px-3 py-3 font-medium text-[var(--text-faint)]">{label}</th>
            ))}
            <th className="px-3 py-3 font-medium text-[var(--text-faint)]">Agent</th>
            <th className="px-4 py-3 font-medium text-[var(--text-faint)]">Composite</th>
          </tr>
        </thead>
        <tbody>
          {contribs.map((c, i) => {
            const p = nameFor(c.contributor_id)
            return (
              <tr key={i} className="border-b border-[var(--border)] last:border-0">
                <td className="px-4 py-3">
                  <div className="flex items-center gap-2">
                    <span className="font-medium text-[var(--text)]">{p?.display_name || c.contributor_id.slice(0, 8)}</span>
                    {p?.is_agent && <Badge color="indigo">agent</Badge>}
                  </div>
                  {p?.primary_email && <span className="font-mono text-xs text-[var(--text-faint)]">{p.primary_email}</span>}
                </td>
                {DIMS.map(([key]) => (
                  <td key={key} className="px-3 py-3"><DimBar value={c.dimensions?.[key]} /></td>
                ))}
                <td className="px-3 py-3 font-mono text-xs tabular-nums text-[var(--text-muted)]">
                  {Math.round((c.agent_pct ?? 0) * 100)}%
                </td>
                <td className="px-4 py-3">
                  <span className="font-display text-base font-semibold tabular-nums text-[var(--brand-teal)]">
                    {(c.composite ?? 0).toFixed(1)}
                  </span>
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </Card>
  )
}

function WorkItems({ items }) {
  if (!items?.length) {
    return <EmptyState title="No work items" description="Scan with a connected forge to pull PRs, issues and reviews." />
  }
  const stateColor = { open: 'green', merged: 'indigo', closed: 'red', draft: 'default', in_progress: 'yellow', done: 'teal' }
  return (
    <div className="flex flex-col gap-2">
      {items.slice(0, 50).map((it) => (
        <Card key={it.id} padding="sm" className="flex items-center justify-between gap-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <span className="font-mono text-xs text-[var(--text-faint)]">{it.external_ref}</span>
              <Badge color={stateColor[it.state] || 'default'}>{it.kind}</Badge>
            </div>
            <p className="truncate text-sm text-[var(--text)]">{it.title}</p>
          </div>
          <Badge color={stateColor[it.state] || 'default'}>{it.state}</Badge>
        </Card>
      ))}
    </div>
  )
}

export default function RepoDetail() {
  const { id } = useParams()
  const navigate = useNavigate()
  const { data, loading, error, reload } = useAsync(() => loadRepo(id), [id])

  const [runScan, { pending: scanning, error: scanErr }] = useAction(scanRepo)
  const [runClassify, { pending: classifying }] = useAction(classify)
  const [runEffort, { pending: judging }] = useAction(effort)

  if (loading) return <div><PageHeader title="Repo" /><Spinner /></div>
  if (error) return <div><PageHeader title="Repo" /><ErrorState error={error} onRetry={reload} /></div>
  if (!data?.repo) {
    return (
      <div>
        <PageHeader title="Repo" />
        <EmptyState title="Repo not found" description="It may have been removed." action={<Button onClick={() => navigate('/repos')}>Back to repos</Button>} />
      </div>
    )
  }

  const { repo, state, contribs, items, people, stats } = data

  async function doScan() {
    await runScan(repo.id, { with_forge: repo.forge !== 'local' })
    reload()
  }
  async function doClassify() {
    await runClassify({ repo_id: repo.id })
    reload()
  }
  async function doEffort() {
    await runEffort({ repo_id: repo.id })
    reload()
  }

  return (
    <div>
      <button onClick={() => navigate('/repos')} className="mb-3 inline-flex items-center gap-1.5 text-sm text-[var(--text-faint)] hover:text-[var(--text)]">
        <ArrowLeft size={15} /> Repos
      </button>

      <PageHeader
        title={repo.slug}
        subtitle={repo.path}
        actions={
          <>
            <Button variant="outline" size="sm" onClick={doClassify} disabled={classifying} leftIcon={<Sparkles size={14} />}>
              {classifying ? 'Classifying…' : 'Classify'}
            </Button>
            <Button variant="outline" size="sm" onClick={doEffort} disabled={judging} leftIcon={<Scale size={14} />}>
              {judging ? 'Judging…' : 'Judge effort'}
            </Button>
            <Button size="sm" onClick={doScan} disabled={scanning} leftIcon={<RefreshCw size={14} className={scanning ? 'animate-spin' : ''} />}>
              {scanning ? 'Scanning…' : 'Scan'}
            </Button>
          </>
        }
      />
      {scanErr && <p className="mb-4 text-sm text-[var(--bad)]">{scanErr.message}</p>}

      <section className="mb-8">
        <h2 className="mb-3 text-sm font-semibold text-[var(--text)]">Project state</h2>
        <ProjectStatePanel state={state} />
      </section>

      <section className="mb-8">
        <h2 className="mb-3 text-sm font-semibold text-[var(--text)]">
          Activity <span className="font-normal text-[var(--text-faint)]">· last 12 months</span>
        </h2>
        <ActivityPanel stats={stats} />
      </section>

      <section className="mb-8">
        <h2 className="mb-3 text-sm font-semibold text-[var(--text)]">
          Contribution <span className="font-normal text-[var(--text-faint)]">· six dimensions, normalized within this repo</span>
        </h2>
        <ContributionsTable contribs={contribs} people={people} />
      </section>

      <section>
        <h2 className="mb-3 text-sm font-semibold text-[var(--text)]">
          Work items <span className="font-normal text-[var(--text-faint)]">· {items?.length || 0}</span>
        </h2>
        <WorkItems items={items} />
      </section>
    </div>
  )
}
