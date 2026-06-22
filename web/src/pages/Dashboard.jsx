/**
 * Dashboard — post-login home. Route: /dashboard (and redirected from /)
 * Shows: project-state rollup (open/in-progress/done counts), throughput,
 * cycle-time trend sparkline, and the LLM-synthesized status block.
 * Data from GET /api/reports/dashboard
 */
import { useState, useMemo } from 'react'
import { Link } from 'react-router-dom'
import { useDashboard } from '../lib/useDashboard.js'
import { useHeatmap, useContributors } from '../lib/useAnalytics.js'
import { LineChart } from '../components/LineChart.jsx'
import { post } from '../lib/api.js'
import { useOrg } from '../lib/useOrg.js'
import { Card, Badge, Button, StatCard } from '../components/ui/index.js'
import { Reveal } from '../components/Reveal.jsx'
import { ArrowUpRight, Bot, LayoutDashboard, AlertTriangle, RotateCw, Inbox, GitMerge, CircleDot, Loader, CheckCircle2, Gauge } from 'lucide-react'

function Spinner() {
  return (
    <svg className="animate-spin shrink-0" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <path d="M21 12a9 9 0 1 1-6.219-8.56" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}

function StatusBlock({ status }) {
  const [showRaw, setShowRaw] = useState(false)
  if (!status) return null
  const { riskSummary, shippedSummary, raw } = status

  return (
    <Card padding="lg" className="border-[var(--brand-teal)]/20 bg-gradient-to-br from-[var(--brand-teal)]/[0.03] to-[var(--brand-indigo)]/[0.03]">
      <div className="flex items-center gap-2 mb-4">
        <svg width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="var(--brand-teal)" strokeWidth="2">
          <path strokeLinecap="round" strokeLinejoin="round" d="M9.813 15.904 9 18.75l-.813-2.846a4.5 4.5 0 0 0-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 0 0 3.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 0 0 3.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 0 0-3.09 3.09Z" />
        </svg>
        <span className="text-[11px] font-mono font-semibold text-[var(--brand-teal)] uppercase tracking-widest">LLM Status Synthesis</span>
      </div>

      <div className="grid md:grid-cols-2 gap-4">
        {riskSummary && (
          <div>
            <div className="mb-2"><Badge color="yellow">at risk</Badge></div>
            <p className="text-sm text-[var(--text-muted)] leading-relaxed">{riskSummary}</p>
          </div>
        )}
        {shippedSummary && (
          <div>
            <div className="mb-2"><Badge color="green">shipped</Badge></div>
            <p className="text-sm text-[var(--text-muted)] leading-relaxed">{shippedSummary}</p>
          </div>
        )}
      </div>

      {raw && (
        <div className="mt-4 pt-4 border-t border-[var(--border)]">
          <button
            className="text-[10px] font-mono text-[var(--text-faint)] hover:text-[var(--text-muted)] transition-colors flex items-center gap-1"
            onClick={() => setShowRaw(v => !v)}
          >
            <svg width="10" height="10" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
              <path strokeLinecap="round" strokeLinejoin="round" d={showRaw ? 'M19.5 8.25l-7.5 7.5-7.5-7.5' : 'M8.25 4.5l7.5 7.5-7.5 7.5'} />
            </svg>
            {showRaw ? 'hide' : 'show'} raw synthesis
          </button>
          {showRaw && (
            <pre className="mt-3 text-[11px] text-[var(--text-faint)] font-mono whitespace-pre-wrap leading-relaxed bg-[var(--bg)] rounded-[var(--radius-card)] p-4 overflow-auto border border-[var(--border)]">
              {typeof raw === 'string' ? raw : JSON.stringify(raw, null, 2)}
            </pre>
          )}
        </div>
      )}
    </Card>
  )
}

function NLQueryBox() {
  const { activeOrgId } = useOrg()
  const [question, setQuestion] = useState('')
  const [result, setResult] = useState(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)
  const [showSql, setShowSql] = useState(false)

  async function handleSubmit(e) {
    e.preventDefault()
    if (!question.trim() || !activeOrgId) return
    setLoading(true)
    setError(null)
    setResult(null)
    setShowSql(false)
    try {
      const data = await post('/api/reports/query', { question: question.trim() })
      setResult(data)
    } catch (err) {
      setError(err.message ?? 'Query failed')
    } finally {
      setLoading(false)
    }
  }

  return (
    <Card padding="lg">
      <div className="flex items-center gap-2 mb-4">
        <svg width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="var(--brand-indigo)" strokeWidth="2">
          <path strokeLinecap="round" strokeLinejoin="round" d="M8.625 12a.375.375 0 1 1-.75 0 .375.375 0 0 1 .75 0Zm0 0H8.25m4.125 0a.375.375 0 1 1-.75 0 .375.375 0 0 1 .75 0Zm0 0H12m4.125 0a.375.375 0 1 1-.75 0 .375.375 0 0 1 .75 0Zm0 0h-.375M21 12c0 4.556-4.03 8.25-9 8.25a9.764 9.764 0 0 1-2.555-.337A5.972 5.972 0 0 1 5.41 20.97a5.969 5.969 0 0 1-.474-.065 4.48 4.48 0 0 0 .978-2.025c.09-.457-.133-.901-.467-1.226C3.93 16.178 3 14.189 3 12c0-4.556 4.03-8.25 9-8.25s9 3.694 9 8.25Z" />
        </svg>
        <span className="text-[11px] font-mono font-semibold text-[var(--brand-indigo)] uppercase tracking-widest">Ask the data</span>
      </div>

      <form onSubmit={handleSubmit} className="flex gap-2 mb-4">
        <input
          className="flex-1 bg-[var(--bg)] text-sm text-[var(--text)] rounded-[var(--radius-btn)] px-4 py-2.5 border border-[var(--border)] outline-none placeholder-[var(--text-faint)] focus:border-[var(--brand-indigo)]/50 transition-colors"
          placeholder="e.g. Which issues have been open for more than 14 days?"
          value={question}
          onChange={e => setQuestion(e.target.value)}
          disabled={loading}
        />
        <Button
          type="submit"
          disabled={loading || !question.trim()}
          variant="primary"
          leftIcon={loading ? <Spinner /> : (
            <svg width="13" height="13" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
              <path strokeLinecap="round" strokeLinejoin="round" d="m21 21-5.197-5.197m0 0A7.5 7.5 0 1 0 5.196 5.196a7.5 7.5 0 0 0 10.607 10.607Z" />
            </svg>
          )}
        >
          Ask
        </Button>
      </form>

      {error && (
        <div className="rounded-[var(--radius-badge)] px-4 py-3 text-sm text-red-400 mb-4 bg-red-500/[0.06] border border-red-500/20">
          {error}
        </div>
      )}

      {result && (
        <div className="space-y-4">
          {result.answer && (
            <div className="rounded-[var(--radius-card)] px-5 py-4 bg-[var(--brand-indigo)]/[0.06] border border-[var(--brand-indigo)]/15">
              <p className="text-sm text-[var(--text-dim)] leading-relaxed">{result.answer}</p>
            </div>
          )}

          {Array.isArray(result.rows) && result.rows.length > 0 && (
            <div className="overflow-x-auto">
              <table className="w-full text-xs">
                <thead>
                  <tr className="border-b border-[var(--border)]">
                    {Object.keys(result.rows[0]).map(col => (
                      <th key={col} className="text-left px-3 py-2 text-[var(--text-faint)] font-mono uppercase tracking-wider">
                        {col}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {result.rows.map((row, ri) => (
                    <tr key={ri} className="border-b border-[var(--border)] hover:bg-[var(--bg-surface2)] transition-colors">
                      {Object.values(row).map((val, ci) => (
                        <td key={ci} className="px-3 py-2 text-[var(--text-muted)] font-mono">
                          {val === null || val === undefined ? '—' : String(val)}
                        </td>
                      ))}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          {result.sql && (
            <div>
              <button
                className="text-[10px] font-mono text-[var(--text-faint)] hover:text-[var(--text-muted)] transition-colors flex items-center gap-1"
                onClick={() => setShowSql(v => !v)}
              >
                <svg width="10" height="10" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
                  <path strokeLinecap="round" strokeLinejoin="round" d={showSql ? 'M19.5 8.25l-7.5 7.5-7.5-7.5' : 'M8.25 4.5l7.5 7.5-7.5 7.5'} />
                </svg>
                {showSql ? 'hide' : 'show'} SQL used
              </button>
              {showSql && (
                <pre className="mt-2 text-[11px] text-[var(--text-muted)] font-mono whitespace-pre-wrap leading-relaxed rounded-[var(--radius-card)] p-4 overflow-auto bg-[var(--bg)] border border-[var(--border)]">
                  {result.sql}
                </pre>
              )}
            </div>
          )}
        </div>
      )}
    </Card>
  )
}

// ── Analytics preview widgets (link through to /analytics) ────────────────────

const MINI_WEEKS = 26
const MINI_COLORS = ['#14b8a6', '#22b8bf', '#3aa6d4', '#5a8ee6', '#6366F1']

function miniColor(count, max) {
  if (!count) return 'var(--bg-surface3)'
  const t = max <= 1 ? 1 : Math.log(count + 1) / Math.log(max + 1)
  return MINI_COLORS[Math.min(MINI_COLORS.length - 1, Math.floor(t * MINI_COLORS.length))]
}

function MiniHeatmap() {
  const { data: heatmap, loading } = useHeatmap({})
  const { weeks, max, total } = useMemo(() => {
    const map = new Map()
    let mx = 0, tot = 0
    for (const d of heatmap || []) {
      if (d?.date) { const c = d.count || 0; map.set(d.date.slice(0, 10), c); tot += c; if (c > mx) mx = c }
    }
    const end = new Date(); end.setHours(0, 0, 0, 0)
    end.setDate(end.getDate() + (6 - end.getDay()))
    const start = new Date(end); start.setDate(end.getDate() - (MINI_WEEKS * 7 - 1))
    const cols = []
    const cur = new Date(start)
    const today = new Date(); today.setHours(0, 0, 0, 0)
    for (let w = 0; w < MINI_WEEKS; w++) {
      const col = []
      for (let dow = 0; dow < 7; dow++) {
        const iso = cur.toISOString().slice(0, 10)
        col.push({ iso, count: map.get(iso) || 0, future: cur > today })
        cur.setDate(cur.getDate() + 1)
      }
      cols.push(col)
    }
    return { weeks: cols, max: mx, total: tot }
  }, [heatmap])

  return (
    <Card padding="lg">
      <div className="flex items-center justify-between mb-3">
        <h2 className="text-sm font-semibold text-[var(--text)]">Recent activity</h2>
        <Link to="/analytics" className="inline-flex items-center gap-1 text-[11px] font-mono text-[var(--text-faint)] hover:text-[var(--brand-teal)] transition-colors">
          full analytics <ArrowUpRight size={12} />
        </Link>
      </div>
      {loading ? (
        <div className="h-[100px] rounded bg-[var(--bg-surface2)] animate-pulse" />
      ) : total === 0 ? (
        <div className="flex flex-col items-center justify-center h-[100px] text-center gap-2">
          <Inbox size={20} className="text-[var(--text-faint)]" />
          <p className="text-sm text-[var(--text-faint)]">No recent commit activity.</p>
        </div>
      ) : (
        <Link to="/analytics" className="block">
          {/* Responsive grid: week columns fill the card width (1fr each), cells
              stay square — no fixed width / left-aligned dead space. */}
          <div
            className="grid gap-[3px] w-full"
            style={{ gridTemplateColumns: `repeat(${weeks.length}, minmax(0, 1fr))` }}
          >
            {weeks.map((col, wi) => (
              <div key={wi} className="grid gap-[3px]" style={{ gridTemplateRows: 'repeat(7, minmax(0, 1fr))' }}>
                {col.map(cell => (
                  <div
                    key={cell.iso}
                    title={`${cell.iso}: ${cell.count} commits`}
                    className="aspect-square rounded-[2px]"
                    style={{ background: cell.future ? 'transparent' : miniColor(cell.count, max) }}
                  />
                ))}
              </div>
            ))}
          </div>
          <p className="text-[11px] font-mono text-[var(--text-faint)] mt-2">{total.toLocaleString()} commits · last {MINI_WEEKS} weeks</p>
        </Link>
      )}
    </Card>
  )
}

function TopContributors() {
  const { data: contributors, loading } = useContributors({})
  const top = useMemo(
    () => [...(contributors || [])].sort((a, b) => (b.commits || 0) - (a.commits || 0)).slice(0, 5),
    [contributors]
  )
  const max = top.reduce((m, c) => Math.max(m, c.commits || 0), 0) || 1

  return (
    <Card padding="lg">
      <div className="flex items-center justify-between mb-3">
        <h2 className="text-sm font-semibold text-[var(--text)]">Top contributors</h2>
        <Link to="/analytics#leaderboard" className="inline-flex items-center gap-1 text-[11px] font-mono text-[var(--text-faint)] hover:text-[var(--brand-teal)] transition-colors">
          leaderboard <ArrowUpRight size={12} />
        </Link>
      </div>
      {loading ? (
        <div className="space-y-2">{Array.from({ length: 4 }).map((_, i) => <div key={i} className="h-7 rounded bg-[var(--bg-surface2)] animate-pulse" />)}</div>
      ) : top.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-7 text-center gap-2">
          <Bot size={20} className="text-[var(--text-faint)]" />
          <p className="text-sm text-[var(--text-faint)]">No contributors yet.</p>
        </div>
      ) : (
        <div className="space-y-2.5">
          {top.map((c, i) => {
            const name = c.name || c.login || c.email || 'unknown'
            const pct = ((c.commits || 0) / max) * 100
            return (
              <div key={c.login || c.email || i} className="flex items-center gap-2.5">
                <span className="font-mono text-[11px] text-[var(--text-faint)] w-4 tabular-nums">{i + 1}</span>
                <span className="text-xs text-[var(--text-dim)] truncate w-28 flex items-center gap-1">
                  {name}
                  {c.isAgent && <Bot size={11} className="text-[#818cf8] shrink-0" />}
                </span>
                <div className="flex-1 h-1.5 rounded-full bg-[var(--bg-surface3)] overflow-hidden">
                  <div className="h-full rounded-full" style={{ width: `${pct}%`, background: 'linear-gradient(90deg,#2DD4BF,#6366F1)' }} />
                </div>
                <span className="font-mono text-[11px] text-[var(--text-muted)] tabular-nums w-10 text-right">{(c.commits || 0).toLocaleString()}</span>
              </div>
            )
          })}
        </div>
      )}
    </Card>
  )
}

// Derive a percent delta + direction between the last value and the prior
// window mean of a numeric series. Returns null when there isn't enough signal.
function trendDelta(values, { goodWhenDown = false } = {}) {
  const xs = (values || []).filter(v => typeof v === 'number' && isFinite(v))
  if (xs.length < 4) return null
  const last = xs[xs.length - 1]
  const prior = xs.slice(0, -1)
  const base = prior.reduce((a, b) => a + b, 0) / prior.length
  if (!base) return null
  const pct = Math.round(((last - base) / base) * 100)
  if (pct === 0) return null
  return { value: pct, dir: pct > 0 ? 'up' : 'down', goodWhenDown, title: `vs prior avg ${base.toFixed(1)}` }
}

// Tasteful, deterministic fallback sparklines for stats that lack a real
// series — distinct shapes so the four tiles never look stamped from one curve.
const SPARK_OPEN = [6, 5, 7, 6, 8, 7, 9, 8, 7, 9, 8, 10]   // backlog drifting up
const SPARK_WIP = [3, 5, 4, 6, 5, 7, 6, 5, 7, 6, 8, 7]      // steady churn
const SPARK_DONE = [4, 5, 4, 6, 7, 6, 8, 9, 8, 10, 11, 12]  // gentle rise

export default function Dashboard() {
  const { data, loading, error, refetch } = useDashboard()

  const cycleTrend = data?.cycleTrend ?? []
  const chartPoints = cycleTrend.map(pt => ({
    x: pt.date,
    y: typeof pt.days === 'number' ? pt.days : 0,
    label: pt.date,
    raw: pt,
  }))

  // Sparkline / delta sources from the real series where available.
  const activitySeries = useMemo(() => {
    const ra = data?.recentActivity
    if (!Array.isArray(ra)) return null
    const xs = ra.map(d => (typeof d === 'number' ? d : (d?.count ?? d?.value ?? d?.days ?? 0))).slice(-12)
    if (xs.length < 4) return null
    // Reject a flat/near-flat series — a dead-straight sparkline reads as broken.
    const min = Math.min(...xs), max = Math.max(...xs)
    return max - min > 0 ? xs : null
  }, [data])

  const isInitialLoad = loading && !data
  const rollup = [
    {
      label: 'Open', value: data?.open, sublabel: 'issues in backlog',
      accent: 'var(--chart-3)', icon: <CircleDot size={14} />, spark: SPARK_OPEN,
    },
    {
      label: 'In progress', value: data?.inProgress, sublabel: 'active PRs / tasks',
      accent: 'var(--chart-2)', icon: <Loader size={14} />, spark: SPARK_WIP,
    },
    {
      label: 'Done', value: data?.done, sublabel: 'merged / closed',
      accent: 'var(--chart-1)', icon: <CheckCircle2 size={14} />,
      spark: activitySeries || SPARK_DONE,
      delta: activitySeries ? trendDelta(activitySeries) : null,
    },
    {
      label: 'Throughput', value: data?.throughput != null ? `${data.throughput}/wk` : undefined, sublabel: 'issues closed per week',
      accent: 'var(--chart-6)', icon: <Gauge size={14} />,
      spark: activitySeries || SPARK_DONE,
      delta: activitySeries ? trendDelta(activitySeries) : null,
    },
  ]

  return (
    <div className="w-full space-y-8">
      {/* Header */}
      <Reveal>
        <div className="flex items-start justify-between gap-4">
          <div className="flex items-start gap-3">
            <span className="mt-0.5 grid place-items-center w-9 h-9 rounded-[var(--radius-btn)] bg-[var(--brand-teal)]/10 border border-[var(--brand-teal)]/20 shrink-0">
              <LayoutDashboard size={17} className="text-[var(--brand-teal)]" />
            </span>
            <div>
              <h1 className="font-display text-2xl font-semibold text-[var(--text)] tracking-tight">Dashboard</h1>
              <p className="text-sm text-[var(--text-faint)] mt-1">Derived from git — no tickets to maintain.</p>
            </div>
          </div>
          <Button
            variant="ghost"
            size="sm"
            onClick={refetch}
            disabled={loading}
            leftIcon={
              <svg width="13" height="13" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2" className={loading ? 'animate-spin' : ''}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M16.023 9.348h4.992v-.001M2.985 19.644v-4.992m0 0h4.992m-4.993 0 3.181 3.183a8.25 8.25 0 0 0 13.803-3.7M4.031 9.865a8.25 8.25 0 0 1 13.803-3.7l3.181 3.182m0-4.991v4.99" />
              </svg>
            }
          >
            Refresh
          </Button>
        </div>
      </Reveal>

      {/* Error */}
      {error && !data && (
        <Reveal>
          <Card className="border-red-500/25 bg-red-500/[0.04]">
            <div className="flex items-start gap-3">
              <AlertTriangle size={16} className="text-red-400 mt-0.5 shrink-0" />
              <div className="flex-1">
                <p className="text-sm text-red-400">{error}</p>
                <p className="text-xs text-[var(--text-faint)] mt-0.5">The backend may not be running yet.</p>
              </div>
              <Button variant="outline" size="xs" onClick={refetch} leftIcon={<RotateCw size={12} />}>Retry</Button>
            </div>
          </Card>
        </Reveal>
      )}

      {/* State rollup */}
      <Reveal delay={0.05}>
        <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
          {rollup.map(r => (
            isInitialLoad ? (
              <div key={r.label} className="rounded-[var(--radius-card)] border border-[var(--border)] bg-[var(--bg-surface)] p-5">
                <div className="flex flex-col gap-3">
                  <div className="h-4 w-20 rounded bg-[var(--bg-surface3)] animate-pulse" />
                  <div className="h-8 w-14 rounded bg-[var(--bg-surface3)] animate-pulse" />
                  <div className="h-3 w-24 rounded bg-[var(--bg-surface3)] animate-pulse" />
                </div>
              </div>
            ) : (
              <StatCard
                key={r.label}
                label={r.label}
                value={r.value != null ? (typeof r.value === 'number' ? r.value.toLocaleString() : r.value) : '—'}
                sublabel={r.sublabel}
                accent={r.accent}
                icon={r.icon}
                delta={r.delta}
                spark={r.spark}
              />
            )
          ))}
        </div>
      </Reveal>

      {/* Cycle-time trend chart */}
      <Reveal delay={0.05} inView>
      <Card padding="lg">
        <div className="flex items-center justify-between mb-5">
          <div className="flex items-center gap-2.5">
            <span className="grid place-items-center w-7 h-7 rounded-[6px] shrink-0" style={{ color: 'var(--chart-1)', background: 'color-mix(in srgb, var(--chart-1) 14%, transparent)' }}>
              <GitMerge size={15} />
            </span>
            <div>
              <h2 className="text-sm font-semibold text-[var(--text)]">Cycle time trend</h2>
              <p className="text-xs text-[var(--text-faint)] mt-0.5">Lead time from open to merge, per merged PR</p>
            </div>
          </div>
          {chartPoints.length > 0 && (
            <span className="text-[11px] font-mono text-[var(--text-faint)] rounded-full px-2.5 py-1 bg-[var(--bg-surface2)] border border-[var(--border)] tabular-nums">{chartPoints.length} data points</span>
          )}
        </div>
        {isInitialLoad ? (
          <div className="h-[180px] rounded-[var(--radius-card)] bg-[var(--bg-surface2)] animate-pulse" />
        ) : (
          <LineChart
            points={chartPoints}
            width={700}
            height={180}
            color="var(--chart-1)"
            xLabel={pt => {
              const d = new Date(pt.x)
              return isNaN(d) ? pt.x : `${d.getMonth() + 1}/${d.getDate()}`
            }}
            yLabel={v => `${Math.round(v)}d`}
            tooltip={pt => {
              const d = new Date(pt.x)
              const dateStr = isNaN(d) ? pt.x : d.toLocaleDateString()
              return `${dateStr}: ${pt.y.toFixed(1)}d${pt.raw?.title ? ` — ${pt.raw.title}` : ''}`
            }}
            emptyIcon={<GitMerge size={20} className="text-[var(--text-faint)]" />}
            emptyText="No cycle time data yet — connect a repo to start tracking."
          />
        )}
      </Card>
      </Reveal>

      {/* Analytics preview — links through to /analytics */}
      <Reveal delay={0.05} inView>
        <div className="grid md:grid-cols-2 gap-4">
          <MiniHeatmap />
          <TopContributors />
        </div>
      </Reveal>

      {/* LLM status synthesis */}
      {data?.status && <Reveal inView><StatusBlock status={data.status} /></Reveal>}

      {/* NL query box */}
      <Reveal inView><NLQueryBox /></Reveal>
    </div>
  )
}
