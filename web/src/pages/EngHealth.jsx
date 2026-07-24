/**
 * Eng Health — DORA-flavored delivery metrics, bus factor, review coverage
 * and quality signals, all derived from git (and forge, where connected).
 */
import { useState } from 'react'
import {
  HeartPulse, Timer, ShieldAlert, GitMerge, Rocket,
  FlaskConical, GitCommitHorizontal, TrendingDown, Bug,
} from 'lucide-react'
import { Card } from '../components/ui/Card.jsx'
import { StatCard } from '../components/ui/StatCard.jsx'
import { BarList } from '../components/ui/BarList.jsx'
import { SegmentedControl } from '../components/ui/SegmentedControl.jsx'
import { PageHeader, Spinner, ErrorState, EmptyState } from '../components/common.jsx'
import { useAsync } from '../lib/hooks.js'
import { listRepos, healthMetrics } from '../lib/api.js'
import { RANGES, formatHours, compact } from '../lib/analyticsView.js'

// A single owner (or two, near-evenly split) holding most of a repo's commits
// means delivery stalls the moment they're unavailable. This threshold is a
// well-known bus-factor rule of thumb, not a value from the daemon — the
// daemon only reports the raw shares, the risk read is a UI concern.
const BUS_FACTOR_RISK_THRESHOLD = 2

async function load(days, repoId) {
  const [repos, health] = await Promise.all([
    listRepos(),
    healthMetrics({ days, repo_id: repoId || undefined }),
  ])
  return { repos, health }
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

/** `0..1` float → "42%", or "—" when the value is genuinely absent. */
function pct(x) {
  if (x == null || !isFinite(x)) return '—'
  return `${Math.round(x * 100)}%`
}

function BusFactorPanel({ busFactor }) {
  const count = busFactor?.count ?? null
  const topShare = busFactor?.top_share ?? null
  const contributors = busFactor?.contributors ?? []
  const atRisk = count != null && count <= BUS_FACTOR_RISK_THRESHOLD && contributors.length > 0

  const items = contributors.map((c) => ({
    key: c.email,
    label: c.name || c.email,
    value: c.commits ?? 0,
    meta: pct(c.share),
    badge: c.is_agent ? (
      <span title="agent identity" className="shrink-0 text-[var(--brand-indigo)] text-[10px] font-mono">
        agent
      </span>
    ) : null,
  }))

  return (
    <Panel
      title="Bus factor"
      subtitle="How concentrated ownership is — the fewest contributors covering the bulk of commits"
    >
      <div className="mb-5 flex items-end gap-4">
        <span className="font-display text-4xl font-semibold tabular-nums text-[var(--text)]">
          {count ?? '—'}
        </span>
        <span className="pb-1 text-xs text-[var(--text-faint)]">
          contributor{count === 1 ? '' : 's'} cover the majority of commits
          {topShare != null && ` · top contributor holds ${pct(topShare)}`}
        </span>
      </div>
      {atRisk && (
        <div className="mb-4 flex items-start gap-2 rounded-[var(--radius-btn)] border border-[color-mix(in_srgb,var(--bad)_30%,transparent)] bg-[color-mix(in_srgb,var(--bad)_6%,transparent)] px-3 py-2">
          <ShieldAlert size={15} className="mt-0.5 shrink-0 text-[var(--bad)]" />
          <p className="text-xs text-[var(--text)]">
            Ownership is concentrated in {count} contributor{count === 1 ? '' : 's'} — losing them would stall delivery.
          </p>
        </div>
      )}
      <BarList items={items} format={(n) => compact(n)} emptyLabel="No commit history in this range" />
    </Panel>
  )
}

function ReviewPanel({ review }) {
  const merged = review?.merged_prs ?? null
  const reviewsDone = review?.reviews_done ?? null
  const reviewedShare = review?.reviewed_pr_share ?? null
  const unreviewed = review?.unreviewed_merged ?? null

  return (
    <Panel title="Review coverage" subtitle="How much merged work was actually reviewed">
      <div className="grid grid-cols-2 gap-4">
        <div>
          <span className="block font-mono text-[10.5px] uppercase tracking-[0.14em] text-[var(--text-faint)]">
            Reviewed share
          </span>
          <span className="font-display text-2xl font-semibold tabular-nums text-[var(--text)]">
            {pct(reviewedShare)}
          </span>
        </div>
        <div>
          <span className="block font-mono text-[10.5px] uppercase tracking-[0.14em] text-[var(--text-faint)]">
            Unreviewed merged
          </span>
          <span className="font-display text-2xl font-semibold tabular-nums text-[var(--text)]">
            {unreviewed == null ? '—' : compact(unreviewed)}
          </span>
        </div>
        <div>
          <span className="block font-mono text-[10.5px] uppercase tracking-[0.14em] text-[var(--text-faint)]">
            Merged PRs
          </span>
          <span className="font-display text-lg font-medium tabular-nums text-[var(--text-muted)]">
            {merged == null ? '—' : compact(merged)}
          </span>
        </div>
        <div>
          <span className="block font-mono text-[10.5px] uppercase tracking-[0.14em] text-[var(--text-faint)]">
            Reviews done
          </span>
          <span className="font-display text-lg font-medium tabular-nums text-[var(--text-muted)]">
            {reviewsDone == null ? '—' : compact(reviewsDone)}
          </span>
        </div>
      </div>
    </Panel>
  )
}

function QualityPanel({ quality }) {
  const testTouch = quality?.test_touch_rate ?? null
  const avgSize = quality?.avg_commit_size_lines ?? null
  const largeShare = quality?.large_commit_share ?? null
  const reverts = quality?.revert_commits ?? null

  return (
    <Panel title="Quality signals" subtitle="Test discipline, commit shape and reverts">
      <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
        <div>
          <span className="mb-1 flex items-center gap-1.5 font-mono text-[10.5px] uppercase tracking-[0.14em] text-[var(--text-faint)]">
            <FlaskConical size={12} /> Test-touch
          </span>
          <span className="font-display text-xl font-semibold tabular-nums text-[var(--text)]">{pct(testTouch)}</span>
        </div>
        <div>
          <span className="mb-1 flex items-center gap-1.5 font-mono text-[10.5px] uppercase tracking-[0.14em] text-[var(--text-faint)]">
            <GitCommitHorizontal size={12} /> Avg commit size
          </span>
          <span className="font-display text-xl font-semibold tabular-nums text-[var(--text)]">
            {avgSize == null || !isFinite(avgSize) ? '—' : `${compact(Math.round(avgSize))} ln`}
          </span>
        </div>
        <div>
          <span className="mb-1 flex items-center gap-1.5 font-mono text-[10.5px] uppercase tracking-[0.14em] text-[var(--text-faint)]">
            <TrendingDown size={12} /> Large-commit share
          </span>
          <span className="font-display text-xl font-semibold tabular-nums text-[var(--text)]">{pct(largeShare)}</span>
        </div>
        <div>
          <span className="mb-1 flex items-center gap-1.5 font-mono text-[10.5px] uppercase tracking-[0.14em] text-[var(--text-faint)]">
            <Bug size={12} /> Reverts
          </span>
          <span className="font-display text-xl font-semibold tabular-nums text-[var(--text)]">
            {reverts == null ? '—' : compact(reverts)}
          </span>
        </div>
      </div>
    </Panel>
  )
}

export default function EngHealth() {
  const [days, setDays] = useState(90)
  const [repoId, setRepoId] = useState('')
  const { data, loading, error, reload } = useAsync(() => load(days, repoId), [days, repoId])

  if (loading) return <div><PageHeader title="Eng Health" /><Spinner /></div>
  if (error) return <div><PageHeader title="Eng Health" /><ErrorState error={error} onRetry={reload} /></div>

  const repos = data?.repos ?? []
  const health = data?.health

  if (!repos.length) {
    return (
      <div>
        <PageHeader title="Eng Health" />
        <EmptyState
          icon={<HeartPulse size={22} />}
          title="Nothing to measure yet"
          description="Add and scan a repo to derive DORA metrics, bus factor, review coverage and quality signals from its git history."
        />
      </div>
    )
  }

  const dora = health?.dora ?? {}
  const busFactor = health?.bus_factor ?? {}
  const review = health?.review ?? {}
  const quality = health?.quality ?? {}
  // No commits scanned into the window yet — every rollup would be nulls, so
  // treat it as an explicit empty state rather than rendering a field of "—".
  const hasData = (dora.lead_time_samples ?? 0) > 0 || (busFactor.count ?? 0) > 0 || (review.merged_prs ?? 0) > 0

  return (
    <div>
      <PageHeader
        title="Eng Health"
        subtitle="Delivery health, ownership risk, review coverage and quality — derived from git, not self-reported."
      />

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
          {health?.range?.from} → {health?.range?.to}
        </span>
      </Card>

      {!hasData ? (
        <EmptyState
          icon={<HeartPulse size={22} />}
          title="No activity in this range"
          description="Widen the time range or pick a different repo — DORA, bus factor, review and quality all need at least one commit or merged PR to compute."
        />
      ) : (
        <>
          <div className="mb-5 grid grid-cols-2 gap-3 md:grid-cols-4">
            <StatCard
              label="Cycle p50" value={formatHours(dora.cycle_p50_hours)}
              sublabel={`p90 ${formatHours(dora.cycle_p90_hours)}`}
              accent="var(--chart-1)" icon={<Timer size={14} />}
            />
            <StatCard
              label="Change-failure rate" value={pct(dora.change_failure_rate)}
              accent="var(--bad)" icon={<ShieldAlert size={14} />}
            />
            <StatCard
              label="Merge frequency"
              value={dora.merge_frequency_per_week == null ? '—' : dora.merge_frequency_per_week.toFixed(1)}
              sublabel="merged PRs / week" accent="var(--chart-2)" icon={<GitMerge size={14} />}
            />
            <StatCard
              label="Deploy proxy"
              value={dora.deploy_proxy_per_week == null ? '—' : dora.deploy_proxy_per_week.toFixed(1)}
              sublabel="deploy-shaped events / week" accent="var(--chart-6)" icon={<Rocket size={14} />}
            />
          </div>

          <div className="mb-5 grid grid-cols-1 gap-4 lg:grid-cols-2">
            <BusFactorPanel busFactor={busFactor} />
            <ReviewPanel review={review} />
          </div>

          <QualityPanel quality={quality} />
        </>
      )}
    </div>
  )
}
