/**
 * Dashboard — the local-first home screen.
 *
 * One `GET /api/analytics` round-trip plus the repo roll-up drives everything:
 * headline stat cards with sparklines, the cycle-time trend, the contribution
 * heatmap, the contributor leaderboard, and the per-repo list.
 */
import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  FolderGit2, GitPullRequest, CircleDot, GitMerge, Timer, ArrowRight,
  GitCommitHorizontal, Users, ArrowUpRight, Bot,
} from 'lucide-react'
import { Card } from '../components/ui/Card.jsx'
import { Button } from '../components/ui/Button.jsx'
import { Badge } from '../components/ui/Badge.jsx'
import { StatCard } from '../components/ui/StatCard.jsx'
import { Heatmap } from '../components/ui/Heatmap.jsx'
import { TrendChart } from '../components/ui/TrendChart.jsx'
import { BarList } from '../components/ui/BarList.jsx'
import { SegmentedControl } from '../components/ui/SegmentedControl.jsx'
import { PageHeader, Spinner, ErrorState, EmptyState } from '../components/common.jsx'
import { useAsync } from '../lib/hooks.js'
import { listRepos, projectState, analytics } from '../lib/api.js'
import {
  RANGES, commitSeries, cycleSeries, sparkOf, trendDelta, formatHours, compact,
} from '../lib/analyticsView.js'

// Repo roll-up + analytics in parallel. A repo with no derived state yet simply
// contributes zeros rather than failing the whole page.
async function loadOverview(days) {
  const [repos, stats] = await Promise.all([listRepos(), analytics({ days })])
  const states = await Promise.all(
    repos.map((r) =>
      projectState(r.id)
        .then((s) => ({ repo: r, state: s }))
        .catch(() => ({ repo: r, state: null })),
    ),
  )
  return { rows: states, stats }
}

/** A titled panel with an optional "see more" affordance. */
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

function RepoRow({ repo, state, onOpen }) {
  const done = state?.done ?? 0
  const open = state?.open_prs ?? 0
  return (
    <Card
      padding="md"
      hoverable
      className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between"
    >
      <button
        type="button"
        onClick={onOpen}
        className="flex min-w-0 flex-1 items-center gap-3 text-left"
      >
        <span className="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-[var(--bg-surface2)] text-[var(--brand-teal)]">
          <FolderGit2 size={17} />
        </span>
        <span className="min-w-0">
          <span className="block truncate font-semibold text-[var(--text)]">{repo.slug}</span>
          <span className="mt-0.5 block font-mono text-[11px] text-[var(--text-faint)]">
            {state
              ? `${open} open · ${state.merged_prs} merged · ${done} done`
              : 'not scanned yet'}
          </span>
        </span>
      </button>

      <div className="flex items-center gap-4">
        {state?.cycle_time_p50_hours != null && (
          <span className="hidden font-mono text-[11px] text-[var(--text-faint)] sm:block">
            p50 {formatHours(state.cycle_time_p50_hours)}
          </span>
        )}
        {state?.open_issues > 0 && <Badge color="yellow">{state.open_issues} issues</Badge>}
        <Button variant="ghost" size="sm" onClick={onOpen} rightIcon={<ArrowRight size={14} />}>
          Open
        </Button>
      </div>
    </Card>
  )
}

export default function Dashboard() {
  const navigate = useNavigate()
  const [days, setDays] = useState(180)
  const { data, loading, error, reload } = useAsync(() => loadOverview(days), [days])

  const stats = data?.stats
  const rows = data?.rows ?? []

  const contributorItems = useMemo(
    () =>
      (stats?.contributors ?? []).slice(0, 6).map((c) => ({
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

  if (loading) return <div><PageHeader title="Dashboard" /><Spinner /></div>
  if (error) return <div><PageHeader title="Dashboard" /><ErrorState error={error} onRetry={reload} /></div>

  if (!rows.length) {
    return (
      <div>
        <PageHeader
          title="Dashboard"
          subtitle="Your local project ledger — derived from git + forge, on your machine."
        />
        <EmptyState
          icon={<FolderGit2 size={22} />}
          title="Add your first repo"
          description="gitstate derives true project state, effort and contribution directly from your git history and forge — no server, no upload."
          action={<Button onClick={() => navigate('/repos')} rightIcon={<ArrowRight size={15} />}>Add a repo</Button>}
        />
      </div>
    )
  }

  const t = stats?.totals ?? {}
  const scanned = rows.filter((d) => d.state)

  return (
    <div>
      <PageHeader
        title="Dashboard"
        subtitle={`${rows.length} repo${rows.length === 1 ? '' : 's'} tracked locally · ${scanned.length} with derived state`}
        actions={
          <>
            <SegmentedControl options={RANGES} value={days} onChange={setDays} label="Time range" />
            <Button variant="outline" size="sm" onClick={() => navigate('/repos')}>
              Manage repos
            </Button>
          </>
        }
      />

      <div className="mb-5 grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard
          label="Commits"
          value={compact(t.commits ?? 0)}
          sublabel={`across ${t.active_days ?? 0} active days`}
          accent="var(--chart-1)"
          icon={<GitCommitHorizontal size={14} />}
          spark={sparkOf(stats, 'commits')}
          delta={trendDelta(stats, 'commits') != null ? { value: trendDelta(stats, 'commits') } : null}
        />
        <StatCard
          label="Merged PRs"
          value={compact(t.merged_prs ?? 0)}
          sublabel={`${t.open_prs ?? 0} still open`}
          accent="var(--chart-2)"
          icon={<GitMerge size={14} />}
          spark={(stats?.throughput ?? []).slice(-14).map((p) => p.merged_prs)}
        />
        <StatCard
          label="Cycle p50"
          value={formatHours(t.cycle_p50_hours)}
          sublabel={`p90 ${formatHours(t.cycle_p90_hours)} · open → merged`}
          accent="var(--chart-3)"
          icon={<Timer size={14} />}
        />
        <StatCard
          label="Contributors"
          value={t.contributors ?? 0}
          sublabel={`${compact(t.additions ?? 0)} added · ${compact(t.deletions ?? 0)} removed`}
          accent="var(--chart-5)"
          icon={<Users size={14} />}
        />
      </div>

      <div className="mb-5">
        <Panel
          title="Cycle time trend"
          subtitle="Lead time from open to merge, per merged pull request"
          action={
            <Badge color="teal">
              {(stats?.cycle_time?.length ?? 0).toLocaleString()} merged PRs
            </Badge>
          }
        >
          <TrendChart
            series={[
              {
                key: 'cycle',
                label: 'Lead time',
                color: 'var(--chart-1)',
                points: cycleSeries(stats),
              },
            ]}
            area
            height={230}
            yFormat={(h) => formatHours(h)}
            valueSuffix=""
            emptyLabel="No merged pull requests in this range"
          />
        </Panel>
      </div>

      <div className="mb-5 grid grid-cols-1 gap-4 lg:grid-cols-5">
        <Panel
          className="lg:col-span-3"
          title="Contribution heatmap"
          subtitle="Commits per day across every tracked repo"
          action={
            <button
              type="button"
              onClick={() => navigate('/insights')}
              className="inline-flex items-center gap-1 font-mono text-[11px] text-[var(--text-faint)] transition-colors hover:text-[var(--brand-teal)]"
            >
              full insights <ArrowUpRight size={12} />
            </button>
          }
        >
          <Heatmap days={stats?.heatmap ?? []} />
        </Panel>

        <Panel
          className="lg:col-span-2"
          title="Top contributors"
          subtitle="By commits in range — texture, not a ranking"
          action={
            <button
              type="button"
              onClick={() => navigate('/insights')}
              className="inline-flex items-center gap-1 font-mono text-[11px] text-[var(--text-faint)] transition-colors hover:text-[var(--brand-teal)]"
            >
              all <ArrowUpRight size={12} />
            </button>
          }
        >
          <BarList items={contributorItems} emptyLabel="No commits in this range" />
        </Panel>
      </div>

      <div className="mb-5">
        <Panel title="Commit volume" subtitle="Commits per week across every tracked repo">
          <TrendChart
            series={[
              { key: 'commits', label: 'Commits', color: 'var(--chart-2)', points: commitSeries(stats) },
            ]}
            area
            height={190}
          />
        </Panel>
      </div>

      <div className="mb-3 flex items-center justify-between">
        <h2 className="text-sm font-semibold text-[var(--text)]">Repositories</h2>
        <span className="font-mono text-[11px] text-[var(--text-faint)]">
          <GitPullRequest size={11} className="mr-1 inline" />
          {t.open_prs ?? 0} open
          <CircleDot size={11} className="mx-1 ml-3 inline" />
          {t.open_issues ?? 0} issues
        </span>
      </div>
      <div className="flex flex-col gap-2.5">
        {rows.map(({ repo, state }) => (
          <RepoRow
            key={repo.id}
            repo={repo}
            state={state}
            onOpen={() => navigate(`/repos/${repo.id}`)}
          />
        ))}
      </div>
    </div>
  )
}
