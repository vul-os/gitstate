/**
 * Pure view-model helpers over the `GET /api/analytics` payload.
 *
 * Kept out of the components so the shaping (series building, downsampling,
 * unit formatting) is trivially testable and shared between the Dashboard and
 * Insights screens.
 */

/** The range filter's options. `days` maps straight onto `?days=`. */
export const RANGES = [
  { value: 30, label: '30d' },
  { value: 90, label: '90d' },
  { value: 180, label: '6mo' },
  { value: 365, label: '1y' },
]

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

/** "2026-06-15" → "Jun 15". */
export function shortDate(iso) {
  if (typeof iso !== 'string' || iso.length < 10) return ''
  const m = Number(iso.slice(5, 7))
  return `${MONTHS[m - 1] ?? ''} ${Number(iso.slice(8, 10))}`
}

/** Hours → the largest sensible unit: "45m", "18h", "2.1d". */
export function formatHours(h) {
  if (h == null || !isFinite(h)) return '—'
  if (h === 0) return '0' // an axis baseline, not "0m"
  if (h < 1) return `${Math.round(h * 60)}m`
  if (h < 48) return `${h.toFixed(h < 10 ? 1 : 0)}h`
  return `${(h / 24).toFixed(1)}d`
}

/** 1234567 → "1.2M", 12345 → "12.3k", 421 → "421". */
export function compact(n) {
  if (n == null || !isFinite(n)) return '—'
  const abs = Math.abs(n)
  if (abs >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (abs >= 10_000) return `${Math.round(n / 1000)}k`
  if (abs >= 1_000) return `${(n / 1000).toFixed(1)}k`
  return n.toLocaleString()
}

/** A signed count, for net-lines style figures. */
export function signed(n) {
  if (n == null || !isFinite(n)) return '—'
  return `${n > 0 ? '+' : ''}${compact(n)}`
}

/**
 * Whole weeks only.
 *
 * The first and last bucket are normally clipped by the range bounds; plotting
 * a 2-day week beside 7-day weeks draws a cliff at each end that looks like a
 * collapse in activity. They still count toward the totals — they just don't
 * belong on a "per week" line.
 */
function wholeWeeks(a) {
  const weeks = a?.weekly ?? []
  const full = weeks.filter((w) => w.complete)
  // If the range is shorter than a week there are no complete buckets; showing
  // the partial ones beats showing nothing.
  return full.length ? full : weeks
}

/** Weekly commit volume as chart points. */
export function commitSeries(a) {
  return wholeWeeks(a).map((w) => ({ x: shortDate(w.week_start), y: w.commits }))
}

/** Weekly line churn (additions, for the "lines added" trend). */
export function churnSeries(a) {
  return wholeWeeks(a).map((w) => ({ x: shortDate(w.week_start), y: w.additions }))
}

/**
 * Cycle-time points, downsampled to at most `limit` marks.
 *
 * A 300-day window can hold hundreds of merged PRs; drawing every one produces
 * a solid band rather than a trend. Takes evenly-spaced samples so the shape —
 * including the spikes — survives.
 */
export function cycleSeries(a, limit = 120) {
  const pts = a?.cycle_time ?? []
  if (!pts.length) return []
  const stride = Math.max(1, Math.ceil(pts.length / limit))
  const out = []
  for (let i = 0; i < pts.length; i += stride) {
    out.push({ x: shortDate(pts[i].merged_at.slice(0, 10)), y: pts[i].hours })
  }
  // Always keep the most recent point so the line ends where the data does.
  const last = pts[pts.length - 1]
  if (out[out.length - 1]?.x !== shortDate(last.merged_at.slice(0, 10))) {
    out.push({ x: shortDate(last.merged_at.slice(0, 10)), y: last.hours })
  }
  return out
}

/** Merged PRs and closed issues per week — two series, one shared unit. */
export function throughputSeries(a) {
  // Same partial-week clipping as the commit series: the daemon buckets these
  // by the event's own date, so the boundary weeks are short.
  const complete = new Set((a?.weekly ?? []).filter((w) => w.complete).map((w) => w.week_start))
  const all = a?.throughput ?? []
  const pts = complete.size ? all.filter((p) => complete.has(p.week_start)) : all
  return [
    {
      key: 'merged',
      label: 'Merged PRs',
      color: 'var(--chart-1)',
      points: pts.map((p) => ({ x: shortDate(p.week_start), y: p.merged_prs })),
    },
    {
      key: 'closed',
      label: 'Closed issues',
      color: 'var(--chart-2)',
      points: pts.map((p) => ({ x: shortDate(p.week_start), y: p.closed_issues })),
    },
  ]
}

/**
 * The last `n` weekly values, for a StatCard sparkline. Sparklines want shape,
 * not precision, so a short tail beats the full series.
 */
export function sparkOf(a, field = 'commits', n = 14) {
  return wholeWeeks(a).slice(-n).map((w) => w[field] ?? 0)
}

/**
 * Percentage change between the first and second half of the weekly series —
 * the delta chip on the stat cards. Returns null when there isn't enough
 * history to compare honestly.
 */
export function trendDelta(a, field = 'commits') {
  const weeks = wholeWeeks(a)
  if (weeks.length < 4) return null
  const mid = Math.floor(weeks.length / 2)
  const sum = (arr) => arr.reduce((t, w) => t + (w[field] ?? 0), 0)
  const before = sum(weeks.slice(0, mid))
  const after = sum(weeks.slice(mid))
  if (before === 0) return null
  return Math.round(((after - before) / before) * 100)
}
