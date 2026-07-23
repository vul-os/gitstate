/**
 * Insights — the full analytics surface.
 *
 * Everything the dashboard previews, at depth: a filter row (range + repo), the
 * headline scalar grid, the contribution heatmap, commit volume, cycle time,
 * weekly throughput, the contributor table, and the label/kind mix.
 */
import { useMemo, useState } from 'react'
import {
  GitCommitHorizontal, FolderGit2, Users, CalendarDays, Plus, Minus, Sigma,
  Activity, Timer, FlaskConical, BarChart3, Bot,
} from 'lucide-react'
import { Card } from '../components/ui/Card.jsx'
import { Badge } from '../components/ui/Badge.jsx'
import { StatCard } from '../components/ui/StatCard.jsx'
import { Heatmap } from '../components/ui/Heatmap.jsx'
import { TrendChart } from '../components/ui/TrendChart.jsx'
import { BarList } from '../components/ui/BarList.jsx'
import { SegmentedControl } from '../components/ui/SegmentedControl.jsx'
import { PageHeader, Spinner, ErrorState, EmptyState } from '../components/common.jsx'
import { useAsync } from '../lib/hooks.js'
import { listRepos, analytics } from '../lib/api.js'
import {
  RANGES, commitSeries, churnSeries, cycleSeries, throughputSeries,
  sparkOf, trendDelta, formatHours, compact, signed,
} from '../lib/analyticsView.js'

async function load(days, repoId) {
  const [repos, stats] = await Promise.all([
    listRepos(),
    analytics({ days, repo_id: repoId || undefined }),
  ])
  return { repos, stats }
}

function Panel({ title, subtitle, action, children, className = '' }) {
  return (
    <Card padding="lg" className={className}>
      <div className="mb-4 flex items-start justify-between gap-3">
        <div>
          <h2 className="text-sm font-semibold text-[var(--text)]">{title}</h2>
          {subtitle && <p className="mt-0.5 text-xs text-[var(--text-faint)]">{subtitle}</p>}
        </div>
        {action}
      </div>
      {children}
    </Card>
  )
}

function ContributorTable({ rows }) {
  if (!rows?.length) {
    return <p className="py-6 text-center text-sm text-[var(--text-faint)]">No commits in this range</p>
  }
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[560px] text-sm">
        <thead>
          <tr className="border-b border-[var(--border)] text-left">
            {['Contributor', 'Commits', 'Active days', 'Added', 'Removed', 'Files'].map((h, i) => (
              <th
                key={h}
                className={[
                  'py-2 font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-[var(--text-faint)]',
                  i === 0 ? 'pr-3' : 'px-3 text-right',
                ].join(' ')}
              >
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((c) => (
            <tr key={c.email} className="border-b border-[var(--border)] last:border-0">
              <td className="py-2.5 pr-3">
                <div className="flex items-center gap-2">
                  <span className="font-medium text-[var(--text)]">{c.name || c.email}</span>
                  {c.is_agent && <Badge color="indigo">agent</Badge>}
                </div>
                <span className="font-mono text-[11px] text-[var(--text-faint)]">{c.email}</span>
              </td>
              <td className="px-3 py-2.5 text-right font-mono tabular-nums text-[var(--text)]">
                {c.commits.toLocaleString()}
              </td>
              <td className="px-3 py-2.5 text-right font-mono tabular-nums text-[var(--text-muted)]">
                {c.active_days}
              </td>
              <td className="px-3 py-2.5 text-right font-mono tabular-nums text-[var(--ok)]">
                +{compact(c.additions)}
              </td>
              <td className="px-3 py-2.5 text-right font-mono tabular-nums text-[var(--bad)]">
                −{compact(c.deletions)}
              </td>
              <td className="px-3 py-2.5 text-right font-mono tabular-nums text-[var(--text-muted)]">
                {compact(c.files_changed)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export default function Insights() {
  const [days, setDays] = useState(365)
  const [repoId, setRepoId] = useState('')
  const { data, loading, error, reload } = useAsync(() => load(days, repoId), [days, repoId])

  const stats = data?.stats
  const repos = data?.repos ?? []
  const t = stats?.totals ?? {}

  const contributorItems = useMemo(
    () =>
      (stats?.contributors ?? []).map((c) => ({
        key: c.email,
        label: c.name || c.email,
        value: c.commits,
        badge: c.is_agent ? (
          <span title="agent identity" className="shrink-0 text-[var(--brand-indigo)]">
            <Bot size={13} />
          </span>
        ) : null,
      })),
    [stats],
  )

  const labelItems = useMemo(
    () => (stats?.labels ?? []).slice(0, 8).map((s) => ({ key: s.key, label: s.key, value: s.count })),
    [stats],
  )

  if (loading) return <div><PageHeader title="Insights" /><Spinner /></div>
  if (error) return <div><PageHeader title="Insights" /><ErrorState error={error} onRetry={reload} /></div>

  if (!repos.length) {
    return (
      <div>
        <PageHeader title="Insights" />
        <EmptyState
          icon={<BarChart3 size={22} />}
          title="Nothing to analyze yet"
          description="Add and scan a repo, and its whole delivery history shows up here — derived from git, not self-reported."
        />
      </div>
    )
  }

  return (
    <div>
      <PageHeader
        title="Insights"
        subtitle="Delivery insight across every tracked repo, PR and issue — derived from git, not self-reported."
      />

      {/* Filters live in one row above the charts they drive. */}
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
        <span className="font-mono text-[11px] text-[var(--text-faint)]">
          {stats?.range?.from} → {stats?.range?.to}
        </span>
      </Card>

      <div className="mb-5 grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-5">
        <StatCard
          label="Commits" value={compact(t.commits ?? 0)} accent="var(--chart-1)"
          icon={<GitCommitHorizontal size={14} />} spark={sparkOf(stats, 'commits')}
          delta={trendDelta(stats, 'commits') != null ? { value: trendDelta(stats, 'commits') } : null}
        />
        <StatCard label="Repos" value={t.repos ?? 0} accent="var(--chart-2)" icon={<FolderGit2 size={14} />} />
        <StatCard label="Contributors" value={t.contributors ?? 0} accent="var(--chart-5)" icon={<Users size={14} />} />
        <StatCard label="Active days" value={t.active_days ?? 0} accent="var(--chart-6)" icon={<CalendarDays size={14} />} />
        <StatCard
          label="Additions" value={compact(t.additions ?? 0)} accent="var(--ok)"
          icon={<Plus size={14} />} spark={sparkOf(stats, 'additions')}
        />
        <StatCard label="Deletions" value={compact(t.deletions ?? 0)} accent="var(--bad)" icon={<Minus size={14} />} />
        <StatCard label="Net lines" value={signed(t.net_lines ?? 0)} accent="var(--chart-3)" icon={<Sigma size={14} />} />
        <StatCard
          label="Commits / day" value={(t.commits_per_active_day ?? 0).toFixed(1)}
          sublabel="per active day" accent="var(--chart-4)" icon={<Activity size={14} />}
        />
        <StatCard
          label="Cycle p50" value={formatHours(t.cycle_p50_hours)}
          sublabel={`p90 ${formatHours(t.cycle_p90_hours)}`} accent="var(--chart-1)" icon={<Timer size={14} />}
        />
        <StatCard
          label="Test-touch" value={`${Math.round((t.test_touch_rate ?? 0) * 100)}%`}
          sublabel={`${compact(t.test_touch_commits ?? 0)} commits touched tests`}
          accent="var(--chart-6)" icon={<FlaskConical size={14} />}
        />
      </div>

      <div className="mb-5">
        <Panel
          title="Contribution heatmap"
          subtitle="Commits per day — hover any cell for that day's detail"
        >
          <Heatmap days={stats?.heatmap ?? []} />
        </Panel>
      </div>

      <div className="mb-5 grid grid-cols-1 gap-4 lg:grid-cols-2">
        <Panel title="Commit volume" subtitle="Commits per week">
          <TrendChart
            series={[{ key: 'commits', label: 'Commits', color: 'var(--chart-1)', points: commitSeries(stats) }]}
            area height={210}
          />
        </Panel>
        <Panel title="Lines added" subtitle="Additions per week">
          <TrendChart
            series={[{ key: 'churn', label: 'Additions', color: 'var(--chart-2)', points: churnSeries(stats) }]}
            area height={210} yFormat={compact}
          />
        </Panel>
      </div>

      <div className="mb-5 grid grid-cols-1 gap-4 lg:grid-cols-2">
        <Panel
          title="Cycle time"
          subtitle="Lead time from open to merge, per merged PR"
          action={<Badge color="teal">{(stats?.cycle_time?.length ?? 0).toLocaleString()} PRs</Badge>}
        >
          <TrendChart
            series={[{ key: 'cycle', label: 'Lead time', color: 'var(--chart-3)', points: cycleSeries(stats) }]}
            area height={210} yFormat={formatHours}
            emptyLabel="No merged pull requests in this range"
          />
        </Panel>
        <Panel title="Throughput" subtitle="Work reaching a terminal state per week">
          <TrendChart series={throughputSeries(stats)} height={210} />
        </Panel>
      </div>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-5">
        <Panel
          className="lg:col-span-3"
          title="Contributors"
          subtitle="Raw activity in range — never a performance ranking"
        >
          <ContributorTable rows={stats?.contributors ?? []} />
        </Panel>

        <div className="flex flex-col gap-4 lg:col-span-2">
          <Panel title="Top by commits" subtitle="Same data, at a glance">
            <BarList items={contributorItems.slice(0, 8)} emptyLabel="No commits in this range" />
          </Panel>
          <Panel title="Label mix" subtitle="Across every PR and issue">
            <BarList items={labelItems} emptyLabel="No labelled work items" />
          </Panel>
        </div>
      </div>
    </div>
  )
}
