/**
 * Contract checks against the daemon's `/api/analytics` payload.
 *
 * These run without a browser page (the runner still gives each one a context,
 * which is cheap). They pin the invariants the charts are built on — a dense
 * grid, internally consistent totals, honest scoping — so a backend regression
 * fails here with a precise message rather than as a mysteriously flat chart.
 */
import { test, api, assert } from '../runner.mjs'

test('analytics: the heatmap is dense and zero-filled', async () => {
  const a = await api('/api/analytics?days=90')

  assert(a.range.days === 90, `analytics: expected a 90-day range, got ${a.range.days}`)
  assert(
    a.heatmap.length === 90,
    `analytics: heatmap must cover every day in range — got ${a.heatmap.length} for 90`,
  )

  // Consecutive, gapless, ascending dates.
  for (let i = 1; i < a.heatmap.length; i++) {
    assert(
      a.heatmap[i].date > a.heatmap[i - 1].date,
      `analytics: heatmap dates out of order at ${i} (${a.heatmap[i - 1].date} → ${a.heatmap[i].date})`,
    )
  }

  // Weekday is precomputed so the client never parses dates, and it cycles 0–6.
  for (let i = 1; i < a.heatmap.length; i++) {
    const expected = (a.heatmap[i - 1].weekday + 1) % 7
    assert(
      a.heatmap[i].weekday === expected,
      `analytics: weekday should advance at ${a.heatmap[i].date}`,
    )
  }

  assert(
    a.heatmap.some((d) => d.commits === 0),
    'analytics: a real calendar has quiet days — none were zero-filled',
  )
  assert(a.heatmap.some((d) => d.commits > 0), 'analytics: no active days in the seeded window')
})

test('analytics: totals are internally consistent', async () => {
  const a = await api('/api/analytics?days=365')
  const t = a.totals

  const summed = a.heatmap.reduce((n, d) => n + d.commits, 0)
  assert(summed === t.commits, `analytics: heatmap sums to ${summed} but totals say ${t.commits}`)

  const active = a.heatmap.filter((d) => d.commits > 0).length
  assert(active === t.active_days, `analytics: ${active} lit days vs active_days ${t.active_days}`)

  assert(
    t.net_lines === t.additions - t.deletions,
    `analytics: net_lines ${t.net_lines} != ${t.additions} - ${t.deletions}`,
  )
  assert(
    t.contributors === a.contributors.length,
    `analytics: contributors total ${t.contributors} != ${a.contributors.length} rows`,
  )
  assert(
    t.cycle_p90_hours >= t.cycle_p50_hours,
    `analytics: p90 ${t.cycle_p90_hours} should be >= p50 ${t.cycle_p50_hours}`,
  )
})

test('analytics: series are ordered and shaped for plotting', async () => {
  const a = await api('/api/analytics?days=365')

  assert(a.weekly.length > 40, `analytics: expected a full year of weeks, got ${a.weekly.length}`)
  for (let i = 1; i < a.weekly.length; i++) {
    assert(
      a.weekly[i].week_start > a.weekly[i - 1].week_start,
      'analytics: weekly buckets must be chronological',
    )
  }

  // Boundary weeks are clipped by the range; the flag is what lets the client
  // drop them from a "per week" line instead of drawing a cliff.
  assert(
    a.weekly.some((w) => w.complete),
    'analytics: a year-long range must contain complete weeks',
  )

  assert(a.cycle_time.length > 0, 'analytics: no merged PRs to plot')
  for (let i = 1; i < a.cycle_time.length; i++) {
    assert(
      a.cycle_time[i].merged_at >= a.cycle_time[i - 1].merged_at,
      'analytics: cycle-time points must be chronological',
    )
  }
  assert(
    a.cycle_time.every((p) => p.hours >= 0),
    'analytics: a negative lead time means merged_at predates created_at',
  )

  // Contributors are ranked by commits desc — the leaderboard renders as-is.
  for (let i = 1; i < a.contributors.length; i++) {
    assert(
      a.contributors[i].commits <= a.contributors[i - 1].commits,
      'analytics: contributors must be ordered by commits desc',
    )
  }
})

test('analytics: scoping to a repo is honest', async () => {
  const repos = await api('/api/repos')
  const all = await api('/api/analytics?days=365')

  let summed = 0
  for (const repo of repos) {
    const scoped = await api(`/api/analytics?days=365&repo_id=${encodeURIComponent(repo.id)}`)
    assert(scoped.totals.repos === 1, `analytics: ${repo.slug} scoped view should report 1 repo`)
    assert(
      scoped.totals.commits <= all.totals.commits,
      `analytics: ${repo.slug} cannot have more commits than the whole ledger`,
    )
    summed += scoped.totals.commits
  }
  assert(
    summed === all.totals.commits,
    `analytics: per-repo commits sum to ${summed}, unscoped reports ${all.totals.commits}`,
  )
})

test('analytics: the window anchors on the data, not on wall-clock now', async () => {
  // A database last scanned months ago must still render a populated view —
  // anchoring on today would show an empty grid for any stale repo.
  const a = await api('/api/analytics?days=30')
  const lastLit = [...a.heatmap].reverse().find((d) => d.commits > 0)
  assert(lastLit, 'analytics: the trailing 30-day window should contain activity')

  const daysFromEnd = a.heatmap.length - 1 - a.heatmap.findIndex((d) => d.date === lastLit.date)
  assert(
    daysFromEnd < 7,
    `analytics: newest activity sits ${daysFromEnd} days before the range end — the window is not anchored on the data`,
  )
})

test('analytics: bad input degrades instead of exploding', async () => {
  // An absurd window is clamped rather than materializing a million-day grid.
  const huge = await api('/api/analytics?days=99999999')
  assert(huge.range.days <= 3653, `analytics: window not clamped (${huge.range.days} days)`)

  // An unknown repo id is an empty result, not a 500.
  const missing = await api('/api/analytics?days=30&repo_id=does-not-exist')
  assert(missing.totals.commits === 0, 'analytics: unknown repo should yield zero commits')
  assert(missing.heatmap.length === 30, 'analytics: unknown repo should still return a grid')
})
