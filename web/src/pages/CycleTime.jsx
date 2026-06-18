/**
 * CycleTime page — /cycle-time
 * Chart of lead-time over time from GET /api/metrics/cycle-time?repo=&from=&to=
 * Hand-rolled SVG via <LineChart>. Includes repo/date filters.
 */
import { useState } from 'react'
import { useCycleTime } from '../lib/useCycleTime.js'
import { useRepos } from '../lib/useRepos.js'
import { LineChart } from '../components/LineChart.jsx'
import { Card, Stat, Button } from '../components/ui/index.js'

function Spinner() {
  return (
    <svg className="animate-spin shrink-0" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="var(--brand-teal)" strokeWidth="2">
      <path d="M21 12a9 9 0 1 1-6.219-8.56" strokeLinecap="round" />
    </svg>
  )
}

function computeStats(points) {
  const ys = points.map(p => p.days).filter(n => typeof n === 'number' && !Number.isNaN(n))
  if (!ys.length) return null
  const avg = ys.reduce((a, b) => a + b, 0) / ys.length
  const sorted = [...ys].sort((a, b) => a - b)
  const p50 = sorted[Math.floor(sorted.length * 0.5)]
  const p90 = sorted[Math.floor(sorted.length * 0.9)]
  const min = sorted[0]
  const max = sorted[sorted.length - 1]
  return { avg, p50, p90, min, max }
}

const filterInputCls = "bg-[var(--bg)] text-xs text-[var(--text-muted)] rounded-[var(--radius-btn)] px-3 py-2 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/40 transition-colors"

export default function CycleTime() {
  const { repos } = useRepos()
  const [repo, setRepo] = useState('')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')

  const { points, loading, error, refetch } = useCycleTime({ repo, from, to })

  const stats = computeStats(points)

  const chartPoints = points.map(pt => ({
    x: pt.date,
    y: typeof pt.days === 'number' ? pt.days : 0,
    label: pt.date,
    title: pt.title,
    repo: pt.repo,
  }))

  return (
    <div className="max-w-5xl space-y-6">
      {/* Header */}
      <div>
        <h1 className="font-display text-2xl font-semibold text-[var(--text)] tracking-tight">Cycle Time</h1>
        <p className="text-sm text-[var(--text-faint)] mt-1">
          Lead time from PR open to merge — derived from git, no estimates entered.
        </p>
      </div>

      {/* Filters */}
      <Card padding="md">
        <div className="flex flex-wrap gap-4 items-end">
          <div className="flex flex-col gap-1.5">
            <label className="text-[10px] font-medium text-[var(--text-faint)] uppercase tracking-widest font-mono">Repository</label>
            <select className={filterInputCls} value={repo} onChange={e => setRepo(e.target.value)}>
              <option value="">All repos</option>
              {repos.map(r => <option key={r.id} value={r.name ?? r.fullName ?? r.id}>{r.name ?? r.fullName}</option>)}
            </select>
          </div>

          <div className="flex flex-col gap-1.5">
            <label className="text-[10px] font-medium text-[var(--text-faint)] uppercase tracking-widest font-mono">From</label>
            <input type="date" className={filterInputCls} value={from} onChange={e => setFrom(e.target.value)} />
          </div>

          <div className="flex flex-col gap-1.5">
            <label className="text-[10px] font-medium text-[var(--text-faint)] uppercase tracking-widest font-mono">To</label>
            <input type="date" className={filterInputCls} value={to} onChange={e => setTo(e.target.value)} />
          </div>

          <Button
            variant="primary"
            size="sm"
            onClick={refetch}
            disabled={loading}
            leftIcon={loading ? <Spinner /> : (
              <svg width="13" height="13" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
                <path strokeLinecap="round" strokeLinejoin="round" d="M16.023 9.348h4.992v-.001M2.985 19.644v-4.992m0 0h4.992m-4.993 0 3.181 3.183a8.25 8.25 0 0 0 13.803-3.7M4.031 9.865a8.25 8.25 0 0 1 13.803-3.7l3.181 3.182m0-4.991v4.99" />
              </svg>
            )}
          >
            Apply
          </Button>
        </div>
      </Card>

      {/* Stats row */}
      {stats && (
        <div className="grid grid-cols-2 sm:grid-cols-5 gap-3">
          <Card padding="md"><Stat label="Avg" value={`${stats.avg.toFixed(1)}d`} /></Card>
          <Card padding="md"><Stat label="Median (p50)" value={`${stats.p50.toFixed(1)}d`} /></Card>
          <Card padding="md"><Stat label="p90" value={`${stats.p90.toFixed(1)}d`} /></Card>
          <Card padding="md"><Stat label="Min" value={`${stats.min.toFixed(1)}d`} /></Card>
          <Card padding="md"><Stat label="Max" value={`${stats.max.toFixed(1)}d`} /></Card>
        </div>
      )}

      {/* Error */}
      {error && (
        <Card className="border-red-500/20 bg-red-500/[0.04]">
          <p className="text-sm text-red-400">{error} — the backend may not be running yet.</p>
        </Card>
      )}

      {/* Chart */}
      <Card padding="lg">
        <div className="flex items-center justify-between mb-5">
          <div>
            <h2 className="text-sm font-semibold text-[var(--text)]">Lead time per merged PR</h2>
            <p className="text-xs text-[var(--text-faint)] mt-0.5">Days from PR open → merge, chronological</p>
          </div>
          {loading && <Spinner />}
          {!loading && chartPoints.length > 0 && (
            <span className="text-xs font-mono text-[var(--text-faint)]">{chartPoints.length} PRs</span>
          )}
        </div>

        <div className="overflow-x-auto">
          <LineChart
            points={chartPoints}
            width={760}
            height={220}
            color="var(--brand-teal)"
            xLabel={pt => {
              const d = new Date(pt.x)
              return isNaN(d) ? pt.x : `${d.getMonth() + 1}/${d.getDate()}`
            }}
            yLabel={v => `${Math.round(v)}d`}
            tooltip={pt => {
              const d = new Date(pt.x)
              const dateStr = isNaN(d) ? pt.x : d.toLocaleDateString()
              return [
                dateStr,
                `${pt.y.toFixed(1)} days`,
                pt.title ? `"${pt.title}"` : '',
                pt.repo ? `@ ${pt.repo}` : '',
              ].filter(Boolean).join(' · ')
            }}
            emptyText={loading ? 'Loading…' : 'No cycle time data — connect a repo and run a sync.'}
          />
        </div>
      </Card>

      {/* Raw data table */}
      {!loading && points.length > 0 && (
        <Card padding="lg">
          <h2 className="text-sm font-semibold text-[var(--text)] mb-4">Raw data</h2>
          <div className="overflow-x-auto">
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b border-[var(--border)]">
                  <th className="text-left px-3 py-2 text-[var(--text-faint)] font-mono uppercase tracking-wider">Date</th>
                  <th className="text-left px-3 py-2 text-[var(--text-faint)] font-mono uppercase tracking-wider">Days</th>
                  <th className="text-left px-3 py-2 text-[var(--text-faint)] font-mono uppercase tracking-wider">PR Title</th>
                  <th className="text-left px-3 py-2 text-[var(--text-faint)] font-mono uppercase tracking-wider">Repo</th>
                </tr>
              </thead>
              <tbody>
                {points.slice().reverse().map((pt, i) => (
                  <tr key={i} className="border-b border-[var(--border)] hover:bg-[var(--bg-surface2)] transition-colors">
                    <td className="px-3 py-2 text-[var(--text-faint)] font-mono">{pt.date}</td>
                    <td className="px-3 py-2 font-mono text-[var(--brand-teal)]">
                      {typeof pt.days === 'number' ? `${pt.days.toFixed(1)}d` : '—'}
                    </td>
                    <td className="px-3 py-2 text-[var(--text-muted)]">{pt.title ?? '—'}</td>
                    <td className="px-3 py-2 text-[var(--text-faint)] font-mono">{pt.repo ?? '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      )}
    </div>
  )
}
