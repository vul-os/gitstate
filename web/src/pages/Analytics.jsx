/**
 * Analytics — the flagship git-analytics dashboard. Route: /analytics
 *
 * Mirrors (and extends) gitrack's dashboard:
 *   • Summary stat tiles (commits, repos, contributors, active days, +/−/net, averages)
 *   • GitHub-style contribution heatmap (53w × 7d) with clickable day → commit drill-down
 *   • Commits-over-time hand-rolled SVG area chart with day/week toggle + hover tooltip
 *   • Contributor leaderboard (gradient avatar, commits bar, +/−, active days, projects, first/last, agent badge)
 *   • Per-repo table
 *   • Filters bar: author dropdown, repo dropdown, date-range presets — all refetch client-side
 *
 * All charts hand-rolled SVG (no chart dependency). Both themes, loading skeletons, empty states.
 */
import { useState, useMemo, useRef } from 'react'
import {
  useSummary, useHeatmap, useCommitsOverTime,
  useContributors, useRepoStats, useDayCommits,
  usePullRequests, useIssueFlow, useAgentShare, useProjects,
} from '../lib/useAnalytics.js'
import { Card, Badge } from '../components/ui/index.js'
import { Reveal } from '../components/Reveal.jsx'
import {
  GitCommitHorizontal, GitBranch, Users, CalendarDays, Plus, Minus,
  Sigma, TrendingUp, Activity, X, Bot, ArrowUpRight, Folder, Hash, ChevronDown,
  GitPullRequest, GitMerge, Timer, CircleDot, CircleCheck, ListChecks, Cpu, User,
  BarChart3, AlertTriangle,
} from 'lucide-react'

// ── small helpers ───────────────────────────────────────────────────────────

const fmtNum = (n) => (n == null ? '—' : Number(n).toLocaleString())
const fmtSigned = (n) => (n == null ? '—' : `${n >= 0 ? '+' : ''}${Number(n).toLocaleString()}`)
const fmtAvg = (n) => (n == null ? '—' : Number(n).toFixed(1))
const fmtPct = (n) => (n == null ? '—' : `${Number(n).toFixed(n < 10 ? 1 : 0)}%`)

// Human-friendly duration from a number of hours (lead time).
function fmtHours(h) {
  if (h == null) return '—'
  const n = Number(h)
  if (!Number.isFinite(n) || n <= 0) return '—'
  if (n < 1) return `${Math.round(n * 60)}m`
  if (n < 48) return `${n.toFixed(1)}h`
  return `${(n / 24).toFixed(1)}d`
}

function fmtDate(s, opts = { month: 'short', day: 'numeric', year: 'numeric' }) {
  if (!s) return '—'
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return s
  return d.toLocaleDateString(undefined, opts)
}

function relTime(s) {
  if (!s) return '—'
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return s
  const diff = Date.now() - d.getTime()
  const days = Math.floor(diff / 86400000)
  if (days <= 0) return 'today'
  if (days === 1) return 'yesterday'
  if (days < 30) return `${days}d ago`
  if (days < 365) return `${Math.floor(days / 30)}mo ago`
  return `${Math.floor(days / 365)}y ago`
}

const todayISO = () => new Date().toISOString().slice(0, 10)
function isoDaysAgo(days) {
  const d = new Date()
  d.setDate(d.getDate() - days)
  return d.toISOString().slice(0, 10)
}

// Avatar hue derived deterministically from a string.
function hueFromStr(str = '') {
  let h = 0
  for (let i = 0; i < str.length; i++) h = (h * 31 + str.charCodeAt(i)) >>> 0
  return h % 360
}

function Avatar({ name, size = 28 }) {
  const initials = (name || '?')
    .split(/[\s@.]+/).filter(Boolean).slice(0, 2)
    .map(w => w[0]).join('').toUpperCase() || '?'
  const hue = hueFromStr(name)
  return (
    <div
      className="rounded-full flex items-center justify-center font-bold text-[var(--bg)] select-none shrink-0"
      style={{
        width: size, height: size, fontSize: size * 0.36,
        background: `linear-gradient(135deg, hsl(${hue} 70% 60%), hsl(${(hue + 50) % 360} 70% 55%))`,
      }}
    >
      {initials}
    </div>
  )
}

// ── filters bar ───────────────────────────────────────────────────────────────

const PRESETS = [
  { key: '30d', label: '30d', days: 30 },
  { key: '90d', label: '90d', days: 90 },
  { key: '9mo', label: '9mo', days: 273 },
  { key: 'all', label: 'All', days: null },
]

const selectCls =
  'appearance-none bg-[var(--bg)] text-xs text-[var(--text-dim)] rounded-[var(--radius-btn)] ' +
  'pl-3 pr-8 py-2 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/45 ' +
  'transition-colors cursor-pointer max-w-[180px] truncate'

function FilterSelect({ icon, value, onChange, children }) {
  return (
    <div className="relative inline-flex items-center">
      <span className="absolute left-2.5 text-[var(--text-faint)] pointer-events-none">{icon}</span>
      <select className={selectCls + ' pl-8'} value={value} onChange={onChange}>{children}</select>
      <ChevronDown size={13} className="absolute right-2.5 text-[var(--text-faint)] pointer-events-none" />
    </div>
  )
}

function FiltersBar({ filters, setFilters, preset, setPreset, contributors, repos }) {
  function applyPreset(p) {
    setPreset(p.key)
    setFilters(f => ({ ...f, from: p.days == null ? '' : isoDaysAgo(p.days), to: p.days == null ? '' : todayISO() }))
  }
  return (
    <Card padding="sm" className="sticky top-2 z-20 backdrop-blur supports-[backdrop-filter]:bg-[var(--bg-surface)]/85">
      <div className="flex flex-wrap items-center gap-3">
        {/* presets */}
        <div className="inline-flex items-center rounded-[var(--radius-btn)] border border-[var(--border)] bg-[var(--bg)] p-0.5">
          {PRESETS.map(p => (
            <button
              key={p.key}
              onClick={() => applyPreset(p)}
              className={[
                'px-2.5 py-1 text-[11px] font-mono font-medium rounded-[6px] transition-colors',
                preset === p.key
                  ? 'bg-[#2DD4BF]/15 text-[#2DD4BF]'
                  : 'text-[var(--text-faint)] hover:text-[var(--text-dim)]',
              ].join(' ')}
            >
              {p.label}
            </button>
          ))}
        </div>

        <div className="h-5 w-px bg-[var(--border)]" />

        <FilterSelect
          icon={<Users size={13} />}
          value={filters.author}
          onChange={e => setFilters(f => ({ ...f, author: e.target.value }))}
        >
          <option value="">All authors</option>
          {contributors.map(c => (
            <option key={c.login || c.email} value={c.login || c.email}>
              {c.name || c.login || c.email}
            </option>
          ))}
        </FilterSelect>

        <FilterSelect
          icon={<Folder size={13} />}
          value={filters.repo}
          onChange={e => setFilters(f => ({ ...f, repo: e.target.value }))}
        >
          <option value="">All repos</option>
          {repos.map(r => (
            <option key={r.repoId || r.fullName} value={r.fullName}>{r.fullName}</option>
          ))}
        </FilterSelect>

        {(filters.author || filters.repo) && (
          <button
            onClick={() => setFilters(f => ({ ...f, author: '', repo: '' }))}
            className="inline-flex items-center gap-1 text-[11px] font-mono text-[var(--text-faint)] hover:text-[var(--text-dim)] transition-colors"
          >
            <X size={12} /> clear
          </button>
        )}

        <span className="ml-auto text-[11px] font-mono text-[var(--text-faint)] hidden sm:block">
          {filters.from ? `${filters.from} → ${filters.to || 'now'}` : 'all time'}
        </span>
      </div>
    </Card>
  )
}

// ── stat tiles ────────────────────────────────────────────────────────────────

function StatTile({ icon, label, value, sub, accent, loading }) {
  return (
    <Card padding="md" className="relative overflow-hidden">
      <div className="flex items-start justify-between">
        <span className="text-[10px] font-mono uppercase tracking-widest text-[var(--text-faint)]">{label}</span>
        <span style={{ color: accent }} className="opacity-80">{icon}</span>
      </div>
      {loading ? (
        <div className="mt-2 h-7 w-16 rounded bg-[var(--bg-surface3)] animate-pulse" />
      ) : (
        <div className="mt-1.5 font-display text-2xl font-semibold text-[var(--text)] tabular-nums tracking-tight">
          {value}
        </div>
      )}
      {sub && <div className="text-[11px] text-[var(--text-faint)] mt-0.5">{sub}</div>}
    </Card>
  )
}

function StatTiles({ summary, loading }) {
  const s = summary ?? {}
  const avg = s.averages ?? {}
  // One balanced, evenly-wrapping grid (10 tiles → 2/3/4/5-up) — avoids the
  // ragged "7 then 3" layout at 1440px.
  const tiles = [
    { icon: <GitCommitHorizontal size={15} />, label: 'Commits', value: fmtNum(s.totalCommits), accent: 'var(--brand-teal)' },
    { icon: <GitBranch size={15} />, label: 'Repos', value: fmtNum(s.repos), accent: 'var(--brand-indigo)' },
    { icon: <Users size={15} />, label: 'Contributors', value: fmtNum(s.contributors), accent: 'var(--brand-teal)' },
    { icon: <CalendarDays size={15} />, label: 'Active days', value: fmtNum(s.activeDays), accent: 'var(--brand-indigo)' },
    { icon: <Plus size={15} />, label: 'Additions', value: fmtNum(s.additions), accent: '#22c55e' },
    { icon: <Minus size={15} />, label: 'Deletions', value: fmtNum(s.deletions), accent: '#ef4444' },
    { icon: <Sigma size={15} />, label: 'Net lines', value: fmtSigned(s.netLines), accent: 'var(--brand-teal)' },
    { icon: <Activity size={15} />, label: 'Commits / active day', value: fmtAvg(avg.commitsPerActiveDay), accent: 'var(--brand-indigo)' },
    { icon: <Users size={15} />, label: 'Commits / contributor', value: fmtAvg(avg.commitsPerContributor), accent: 'var(--brand-teal)' },
    { icon: <TrendingUp size={15} />, label: 'Lines / commit', value: fmtAvg(avg.linesPerCommit), accent: 'var(--brand-indigo)' },
  ]
  return (
    <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 gap-3">
      {tiles.map(t => <StatTile key={t.label} {...t} loading={loading} />)}
    </div>
  )
}

// ── contribution heatmap ────────────────────────────────────────────────────

const WEEKS = 53
const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']
const CELL = 12 // px including gap
const GAP = 3

// teal → indigo scale for cell intensity
const HEAT_COLORS = ['#14b8a6', '#22b8bf', '#3aa6d4', '#5a8ee6', '#6366F1']

function colorForCount(count, max) {
  if (!count) return null
  const t = max <= 1 ? 1 : Math.log(count + 1) / Math.log(max + 1)
  const idx = Math.min(HEAT_COLORS.length - 1, Math.floor(t * HEAT_COLORS.length))
  return HEAT_COLORS[idx]
}

/** Build a 53-week grid ending today (or at filter `to`). */
function buildGrid(heatmap, endISO) {
  const map = new Map()
  let max = 0
  for (const d of heatmap) {
    if (d?.date) { map.set(d.date.slice(0, 10), d.count || 0); if ((d.count || 0) > max) max = d.count }
  }
  const end = endISO ? new Date(endISO + 'T00:00:00') : new Date()
  end.setHours(0, 0, 0, 0)
  // align end to Saturday so columns are clean weeks
  const endDow = end.getDay()
  const gridEnd = new Date(end); gridEnd.setDate(end.getDate() + (6 - endDow))
  const start = new Date(gridEnd); start.setDate(gridEnd.getDate() - (WEEKS * 7 - 1))

  const weeks = []
  const cur = new Date(start)
  for (let w = 0; w < WEEKS; w++) {
    const col = []
    for (let dow = 0; dow < 7; dow++) {
      const iso = cur.toISOString().slice(0, 10)
      const isFuture = cur > end
      col.push({ iso, count: map.get(iso) || 0, future: isFuture, date: new Date(cur) })
      cur.setDate(cur.getDate() + 1)
    }
    weeks.push(col)
  }
  return { weeks, max }
}

function Heatmap({ heatmap, loading, endISO, selectedDate, onSelect }) {
  const { weeks, max } = useMemo(() => buildGrid(heatmap, endISO), [heatmap, endISO])
  const [hover, setHover] = useState(null) // {x,y,cell}
  const wrapRef = useRef(null)

  // month labels: place at first week whose first day's month differs from prev
  const monthLabels = useMemo(() => {
    const out = []
    let prev = -1
    weeks.forEach((col, wi) => {
      const m = col[0].date.getMonth()
      if (m !== prev && col[0].date.getDate() <= 14) { out.push({ wi, label: MONTHS[m] }); prev = m }
    })
    return out
  }, [weeks])

  const width = WEEKS * CELL + 30
  const height = 7 * CELL + 22

  if (loading) {
    return <div className="h-[140px] rounded-[var(--radius-card)] bg-[var(--bg-surface2)] animate-pulse" />
  }
  if (!heatmap.length || max === 0) {
    return (
      <div className="flex flex-col items-center justify-center h-[140px] text-center">
        <CalendarDays size={22} className="text-[var(--text-faint)] mb-2" />
        <p className="text-sm text-[var(--text-faint)]">No commit activity in this range.</p>
      </div>
    )
  }

  const total = weeks.reduce((a, col) => a + col.reduce((b, c) => b + c.count, 0), 0)

  return (
    <div className="relative" ref={wrapRef}>
      <div className="overflow-x-auto pb-1">
        <svg width={width} height={height} className="block" role="img" aria-label="Contribution heatmap">
          {/* month labels */}
          {monthLabels.map(m => (
            <text
              key={m.wi}
              x={30 + m.wi * CELL}
              y={10}
              className="font-mono"
              fontSize="9"
              fill="var(--text-faint)"
            >
              {m.label}
            </text>
          ))}
          {/* weekday labels */}
          {['Mon', 'Wed', 'Fri'].map((d, i) => (
            <text key={d} x={0} y={22 + (i * 2 + 1) * CELL + 9} fontSize="9" className="font-mono" fill="var(--text-faint)">{d}</text>
          ))}
          {/* cells */}
          <g transform={`translate(30, 16)`}>
            {weeks.map((col, wi) =>
              col.map((cell, dow) => {
                if (cell.future) return null
                const fill = colorForCount(cell.count, max)
                const selected = selectedDate === cell.iso
                return (
                  <rect
                    key={cell.iso}
                    x={wi * CELL}
                    y={dow * CELL}
                    width={CELL - GAP}
                    height={CELL - GAP}
                    rx={2}
                    fill={fill || 'var(--bg-surface3)'}
                    stroke={selected ? 'var(--text)' : 'transparent'}
                    strokeWidth={selected ? 1.5 : 0}
                    className="cursor-pointer transition-opacity hover:opacity-80"
                    onClick={() => onSelect(cell.count ? cell.iso : null)}
                    onMouseEnter={e => {
                      const r = wrapRef.current?.getBoundingClientRect()
                      const cr = e.target.getBoundingClientRect()
                      setHover({
                        x: cr.left - (r?.left ?? 0) + (CELL - GAP) / 2,
                        y: cr.top - (r?.top ?? 0),
                        cell,
                      })
                    }}
                    onMouseLeave={() => setHover(null)}
                  />
                )
              })
            )}
          </g>
        </svg>
      </div>

      {/* legend + total */}
      <div className="flex items-center justify-between mt-1.5">
        <span className="text-[11px] font-mono text-[var(--text-faint)]">{fmtNum(total)} commits</span>
        <div className="flex items-center gap-1.5 text-[10px] font-mono text-[var(--text-faint)]">
          <span>less</span>
          <div className="w-2.5 h-2.5 rounded-[2px]" style={{ background: 'var(--bg-surface3)' }} />
          {HEAT_COLORS.map(c => <div key={c} className="w-2.5 h-2.5 rounded-[2px]" style={{ background: c }} />)}
          <span>more</span>
        </div>
      </div>

      {/* hover tooltip */}
      {hover && (
        <div
          className="pointer-events-none absolute z-30 -translate-x-1/2 -translate-y-full mb-1 px-2 py-1 rounded-[var(--radius-badge)] bg-[var(--bg)] border border-[var(--border2)] shadow-[var(--shadow-float)] whitespace-nowrap"
          style={{ left: hover.x, top: hover.y - 4 }}
        >
          <div className="text-[11px] font-semibold text-[var(--text)] tabular-nums">
            {hover.cell.count} commit{hover.cell.count === 1 ? '' : 's'}
          </div>
          <div className="text-[10px] font-mono text-[var(--text-faint)]">{fmtDate(hover.cell.iso)}</div>
        </div>
      )}
    </div>
  )
}

// ── day drill-down panel ─────────────────────────────────────────────────────

function DayDrillDown({ date, filters, onClose }) {
  const { data: commits, loading, error } = useDayCommits(date, filters)
  if (!date) return null
  return (
    <Card padding="none" className="border-[#2DD4BF]/25 overflow-hidden">
      <div className="flex items-center justify-between px-5 py-3 border-b border-[var(--border)] bg-[var(--bg-surface2)]">
        <div className="flex items-center gap-2">
          <Hash size={14} className="text-[var(--brand-teal)]" />
          <span className="text-sm font-semibold text-[var(--text)]">{fmtDate(date, { weekday: 'long', month: 'long', day: 'numeric', year: 'numeric' })}</span>
          {!loading && <Badge color="teal">{commits.length} commit{commits.length === 1 ? '' : 's'}</Badge>}
        </div>
        <button onClick={onClose} className="text-[var(--text-faint)] hover:text-[var(--text)] transition-colors p-1 -mr-1 rounded">
          <X size={16} />
        </button>
      </div>

      <div className="max-h-[360px] overflow-y-auto divide-y divide-[var(--border)]">
        {loading && (
          <div className="px-5 py-8 text-center text-sm text-[var(--text-faint)]">Loading commits…</div>
        )}
        {error && !loading && (
          <div className="px-5 py-8 text-center text-sm text-red-400">{error}</div>
        )}
        {!loading && !error && commits.length === 0 && (
          <div className="px-5 py-8 text-center text-sm text-[var(--text-faint)]">No commits matched the current filters on this day.</div>
        )}
        {!loading && commits.map((c, i) => (
          <div key={c.sha || i} className="px-5 py-3 hover:bg-[var(--bg-surface2)] transition-colors">
            <div className="flex items-start justify-between gap-3">
              <div className="min-w-0 flex-1">
                <p className="text-sm text-[var(--text-dim)] leading-snug truncate">{(c.message || '').split('\n')[0] || '(no message)'}</p>
                <div className="flex items-center gap-2 mt-1 text-[11px] font-mono text-[var(--text-faint)]">
                  {c.sha && <span className="text-[var(--brand-teal)]">{c.sha.slice(0, 7)}</span>}
                  {c.authorLogin && <span>· {c.authorLogin}</span>}
                  {c.repoFullName && <span className="truncate">· {c.repoFullName}</span>}
                  {c.committedAt && <span>· {fmtDate(c.committedAt, { hour: '2-digit', minute: '2-digit' })}</span>}
                </div>
              </div>
              <div className="flex items-center gap-1.5 shrink-0 font-mono text-[11px] tabular-nums">
                {c.additions != null && <span className="text-green-400">+{c.additions}</span>}
                {c.deletions != null && <span className="text-red-400">−{c.deletions}</span>}
              </div>
            </div>
          </div>
        ))}
      </div>
    </Card>
  )
}

// ── commits over time (SVG area chart) ───────────────────────────────────────

function CommitsOverTime({ filters }) {
  const [bucket, setBucket] = useState('day')
  const { data, loading } = useCommitsOverTime(filters, bucket)
  const [hover, setHover] = useState(null)
  const svgRef = useRef(null)

  const W = 760, H = 220, PAD = { t: 16, r: 16, b: 26, l: 36 }
  const innerW = W - PAD.l - PAD.r
  const innerH = H - PAD.t - PAD.b

  const points = useMemo(() => (data || []).filter(d => d?.date), [data])
  const max = useMemo(() => points.reduce((m, p) => Math.max(m, p.count || 0), 0), [points])
  const total = useMemo(() => points.reduce((a, p) => a + (p.count || 0), 0), [points])

  const xFor = (i) => points.length <= 1 ? PAD.l + innerW / 2 : PAD.l + (i / (points.length - 1)) * innerW
  const yFor = (v) => PAD.t + innerH - (max <= 0 ? 0 : (v / max) * innerH)

  const linePath = points.map((p, i) => `${i === 0 ? 'M' : 'L'} ${xFor(i).toFixed(1)} ${yFor(p.count || 0).toFixed(1)}`).join(' ')
  const areaPath = points.length
    ? `${linePath} L ${xFor(points.length - 1).toFixed(1)} ${(PAD.t + innerH).toFixed(1)} L ${xFor(0).toFixed(1)} ${(PAD.t + innerH).toFixed(1)} Z`
    : ''

  // y gridlines
  const ticks = max > 0 ? [0, 0.5, 1].map(t => Math.round(max * t)) : [0]

  function onMove(e) {
    if (!points.length) return
    const rect = svgRef.current.getBoundingClientRect()
    const x = (e.clientX - rect.left) * (W / rect.width)
    let idx = Math.round(((x - PAD.l) / innerW) * (points.length - 1))
    idx = Math.max(0, Math.min(points.length - 1, idx))
    setHover({ idx, p: points[idx] })
  }

  return (
    <Card padding="lg">
      <div className="flex items-center justify-between mb-4">
        <div>
          <h2 className="text-sm font-semibold text-[var(--text)]">Commits over time</h2>
          <p className="text-xs text-[var(--text-faint)] mt-0.5">{fmtNum(total)} commits, bucketed by {bucket}</p>
        </div>
        <div className="inline-flex items-center rounded-[var(--radius-btn)] border border-[var(--border)] bg-[var(--bg)] p-0.5">
          {['day', 'week'].map(b => (
            <button
              key={b}
              onClick={() => setBucket(b)}
              className={[
                'px-3 py-1 text-[11px] font-mono font-medium rounded-[6px] transition-colors capitalize',
                bucket === b ? 'bg-[#2DD4BF]/15 text-[#2DD4BF]' : 'text-[var(--text-faint)] hover:text-[var(--text-dim)]',
              ].join(' ')}
            >
              {b}
            </button>
          ))}
        </div>
      </div>

      {loading ? (
        <div className="h-[220px] rounded-[var(--radius-card)] bg-[var(--bg-surface2)] animate-pulse" />
      ) : points.length === 0 ? (
        <div className="flex flex-col items-center justify-center h-[220px] text-center">
          <Activity size={22} className="text-[var(--text-faint)] mb-2" />
          <p className="text-sm text-[var(--text-faint)]">No commits in this range.</p>
        </div>
      ) : (
        <div className="relative overflow-x-auto">
          <svg
            ref={svgRef} width={W} height={H} className="block max-w-full"
            onMouseMove={onMove} onMouseLeave={() => setHover(null)}
          >
            <defs>
              <linearGradient id="cotArea" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor="#2DD4BF" stopOpacity="0.35" />
                <stop offset="100%" stopColor="#6366F1" stopOpacity="0.02" />
              </linearGradient>
              <linearGradient id="cotLine" x1="0" y1="0" x2="1" y2="0">
                <stop offset="0%" stopColor="#2DD4BF" />
                <stop offset="100%" stopColor="#6366F1" />
              </linearGradient>
            </defs>

            {/* gridlines + y labels */}
            {ticks.map((t, i) => {
              const y = yFor(t)
              return (
                <g key={i}>
                  <line x1={PAD.l} y1={y} x2={W - PAD.r} y2={y} stroke="var(--border)" strokeWidth="1" strokeDasharray="2 3" />
                  <text x={PAD.l - 6} y={y + 3} textAnchor="end" fontSize="9" className="font-mono" fill="var(--text-faint)">{t}</text>
                </g>
              )
            })}

            {areaPath && <path d={areaPath} fill="url(#cotArea)" />}
            {linePath && <path d={linePath} fill="none" stroke="url(#cotLine)" strokeWidth="2" strokeLinejoin="round" strokeLinecap="round" />}

            {/* x labels (first / mid / last) */}
            {points.length > 0 && [0, Math.floor(points.length / 2), points.length - 1]
              .filter((v, i, a) => a.indexOf(v) === i).map(i => (
                <text key={i} x={xFor(i)} y={H - 8} textAnchor="middle" fontSize="9" className="font-mono" fill="var(--text-faint)">
                  {fmtDate(points[i].date, { month: 'short', day: 'numeric' })}
                </text>
              ))}

            {/* hover marker */}
            {hover && (
              <g>
                <line x1={xFor(hover.idx)} y1={PAD.t} x2={xFor(hover.idx)} y2={PAD.t + innerH} stroke="var(--border2)" strokeWidth="1" />
                <circle cx={xFor(hover.idx)} cy={yFor(hover.p.count || 0)} r="3.5" fill="#2DD4BF" stroke="var(--bg)" strokeWidth="1.5" />
              </g>
            )}
          </svg>

          {hover && (
            <div
              className="pointer-events-none absolute z-20 -translate-x-1/2 -translate-y-full px-2.5 py-1.5 rounded-[var(--radius-badge)] bg-[var(--bg)] border border-[var(--border2)] shadow-[var(--shadow-float)] whitespace-nowrap"
              style={{
                left: `${(xFor(hover.idx) / W) * 100}%`,
                top: `${(yFor(hover.p.count || 0) / H) * 100}%`,
              }}
            >
              <div className="text-sm font-semibold text-[var(--text)] tabular-nums">{hover.p.count} commit{hover.p.count === 1 ? '' : 's'}</div>
              <div className="text-[10px] font-mono text-[var(--text-faint)]">{fmtDate(hover.p.date)}</div>
            </div>
          )}
        </div>
      )}
    </Card>
  )
}

// ── contributor leaderboard ──────────────────────────────────────────────────

function ContributorLeaderboard({ contributors, loading }) {
  const sorted = useMemo(
    () => [...(contributors || [])].sort((a, b) => (b.commits || 0) - (a.commits || 0)),
    [contributors]
  )
  const maxCommits = sorted.reduce((m, c) => Math.max(m, c.commits || 0), 0) || 1

  return (
    <Card padding="lg">
      <div className="flex items-center justify-between mb-1">
        <h2 className="text-sm font-semibold text-[var(--text)]">Contributor leaderboard</h2>
        {!loading && <span className="text-xs font-mono text-[var(--text-faint)]">{sorted.length} people</span>}
      </div>
      <p className="text-[11px] text-[var(--text-faint)] mb-4">
        Involvement texture from git history — not a productivity score.
      </p>

      {loading ? (
        <div className="space-y-2">
          {Array.from({ length: 5 }).map((_, i) => <div key={i} className="h-10 rounded bg-[var(--bg-surface2)] animate-pulse" />)}
        </div>
      ) : sorted.length === 0 ? (
        <div className="py-8 text-center text-sm text-[var(--text-faint)]">No contributors in this range.</div>
      ) : (
        <div className="overflow-x-auto -mx-1">
          <table className="w-full text-xs">
            <thead>
              <tr className="border-b border-[var(--border)] text-[var(--text-faint)]">
                <th className="text-left font-mono uppercase tracking-wider font-medium px-2 py-2 w-8">#</th>
                <th className="text-left font-mono uppercase tracking-wider font-medium px-2 py-2">Contributor</th>
                <th className="text-left font-mono uppercase tracking-wider font-medium px-2 py-2 min-w-[160px]">Commits</th>
                <th className="text-right font-mono uppercase tracking-wider font-medium px-2 py-2">Lines</th>
                <th className="text-right font-mono uppercase tracking-wider font-medium px-2 py-2 hidden sm:table-cell">Active</th>
                <th className="text-right font-mono uppercase tracking-wider font-medium px-2 py-2 hidden md:table-cell">Repos</th>
                <th className="text-right font-mono uppercase tracking-wider font-medium px-2 py-2 hidden lg:table-cell">First → Last</th>
              </tr>
            </thead>
            <tbody>
              {sorted.map((c, i) => {
                const name = c.name || c.login || c.email || 'unknown'
                const pct = ((c.commits || 0) / maxCommits) * 100
                return (
                  <tr key={c.login || c.email || i} className="border-b border-[var(--border)] hover:bg-[var(--bg-surface2)] transition-colors">
                    <td className="px-2 py-2.5 font-mono text-[var(--text-faint)] tabular-nums">{i + 1}</td>
                    <td className="px-2 py-2.5">
                      <div className="flex items-center gap-2.5 min-w-0">
                        <Avatar name={name} size={26} />
                        <div className="min-w-0">
                          <div className="flex items-center gap-1.5">
                            <span className="text-[var(--text-dim)] font-medium truncate">{name}</span>
                            {c.isAgent && (
                              <span className="inline-flex items-center gap-0.5 px-1 py-px rounded-[4px] text-[9px] font-mono bg-[#6366F1]/12 text-[#818cf8] border border-[#6366F1]/25">
                                <Bot size={9} /> agent
                              </span>
                            )}
                          </div>
                          {c.login && c.login !== name && (
                            <span className="text-[10px] font-mono text-[var(--text-faint)] truncate block">@{c.login}</span>
                          )}
                        </div>
                      </div>
                    </td>
                    <td className="px-2 py-2.5">
                      <div className="flex items-center gap-2">
                        <div className="flex-1 h-1.5 rounded-full bg-[var(--bg-surface3)] overflow-hidden min-w-[60px]">
                          <div className="h-full rounded-full" style={{ width: `${pct}%`, background: 'linear-gradient(90deg,#2DD4BF,#6366F1)' }} />
                        </div>
                        <span className="font-mono tabular-nums text-[var(--text-dim)] w-9 text-right">{fmtNum(c.commits)}</span>
                      </div>
                    </td>
                    <td className="px-2 py-2.5 text-right font-mono tabular-nums whitespace-nowrap">
                      <span className="text-green-400">+{fmtNum(c.additions)}</span>{' '}
                      <span className="text-red-400">−{fmtNum(c.deletions)}</span>
                    </td>
                    <td className="px-2 py-2.5 text-right font-mono tabular-nums text-[var(--text-muted)] hidden sm:table-cell">{fmtNum(c.activeDays)}d</td>
                    <td className="px-2 py-2.5 text-right font-mono tabular-nums text-[var(--text-muted)] hidden md:table-cell">{fmtNum(c.projects)}</td>
                    <td className="px-2 py-2.5 text-right font-mono text-[var(--text-faint)] whitespace-nowrap hidden lg:table-cell">
                      {fmtDate(c.firstAt, { month: 'short', year: '2-digit' })} → {relTime(c.lastAt)}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  )
}

// ── per-repo table ────────────────────────────────────────────────────────────

function RepoTable({ repos, loading }) {
  const sorted = useMemo(
    () => [...(repos || [])].sort((a, b) => (b.commits || 0) - (a.commits || 0)),
    [repos]
  )
  return (
    <Card padding="lg">
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-sm font-semibold text-[var(--text)]">Repositories</h2>
        {!loading && <span className="text-xs font-mono text-[var(--text-faint)]">{sorted.length} repos</span>}
      </div>
      {loading ? (
        <div className="space-y-2">
          {Array.from({ length: 4 }).map((_, i) => <div key={i} className="h-9 rounded bg-[var(--bg-surface2)] animate-pulse" />)}
        </div>
      ) : sorted.length === 0 ? (
        <div className="py-8 text-center text-sm text-[var(--text-faint)]">No repositories in this range.</div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead>
              <tr className="border-b border-[var(--border)] text-[var(--text-faint)]">
                <th className="text-left font-mono uppercase tracking-wider font-medium px-2 py-2">Repository</th>
                <th className="text-right font-mono uppercase tracking-wider font-medium px-2 py-2">Commits</th>
                <th className="text-right font-mono uppercase tracking-wider font-medium px-2 py-2">Contributors</th>
                <th className="text-right font-mono uppercase tracking-wider font-medium px-2 py-2">Lines</th>
                <th className="text-right font-mono uppercase tracking-wider font-medium px-2 py-2">Last activity</th>
              </tr>
            </thead>
            <tbody>
              {sorted.map((r, i) => (
                <tr key={r.repoId || r.fullName || i} className="border-b border-[var(--border)] hover:bg-[var(--bg-surface2)] transition-colors">
                  <td className="px-2 py-2.5">
                    <div className="flex items-center gap-2 min-w-0">
                      <GitBranch size={13} className="text-[var(--brand-indigo)] shrink-0" />
                      <span className="text-[var(--text-dim)] font-mono truncate">{r.fullName}</span>
                    </div>
                  </td>
                  <td className="px-2 py-2.5 text-right font-mono tabular-nums text-[var(--text-dim)]">{fmtNum(r.commits)}</td>
                  <td className="px-2 py-2.5 text-right font-mono tabular-nums text-[var(--text-muted)]">{fmtNum(r.contributors)}</td>
                  <td className="px-2 py-2.5 text-right font-mono tabular-nums whitespace-nowrap">
                    <span className="text-green-400">+{fmtNum(r.additions)}</span>{' '}
                    <span className="text-red-400">−{fmtNum(r.deletions)}</span>
                  </td>
                  <td className="px-2 py-2.5 text-right font-mono text-[var(--text-faint)] whitespace-nowrap">{relTime(r.lastActivity)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  )
}

// ── shared mini stacked/grouped bar chart (SVG) ───────────────────────────────

/**
 * TwoSeriesBars — a compact SVG showing two daily series as either grouped or
 * stacked bars over a shared x-axis. `series` = [{date, a, b}], with labels +
 * colors for a/b. Hover shows a tooltip. Used by PR throughput, issue flow and
 * agent share.
 */
function TwoSeriesBars({ series, labelA, labelB, colorA, colorB, stacked = false, height = 180 }) {
  const [hover, setHover] = useState(null)
  const svgRef = useRef(null)
  const W = 760, H = height, PAD = { t: 14, r: 12, b: 24, l: 30 }
  const innerW = W - PAD.l - PAD.r
  const innerH = H - PAD.t - PAD.b

  const pts = useMemo(() => (series || []).filter(d => d?.date), [series])
  const max = useMemo(() => pts.reduce((m, p) => {
    const v = stacked ? (p.a || 0) + (p.b || 0) : Math.max(p.a || 0, p.b || 0)
    return Math.max(m, v)
  }, 0) || 1, [pts, stacked])

  if (!pts.length) {
    return (
      <div className="flex flex-col items-center justify-center text-center" style={{ height }}>
        <Activity size={20} className="text-[var(--text-faint)] mb-2" />
        <p className="text-sm text-[var(--text-faint)]">No data in this range.</p>
      </div>
    )
  }

  const slot = innerW / pts.length
  const barW = stacked ? Math.min(14, slot * 0.7) : Math.min(7, slot * 0.38)
  const yFor = (v) => PAD.t + innerH - (v / max) * innerH
  const ticks = [0, 0.5, 1].map(t => Math.round(max * t)).filter((v, i, a) => a.indexOf(v) === i)

  function onMove(e) {
    const rect = svgRef.current.getBoundingClientRect()
    const x = (e.clientX - rect.left) * (W / rect.width)
    let idx = Math.floor((x - PAD.l) / slot)
    idx = Math.max(0, Math.min(pts.length - 1, idx))
    setHover({ idx, p: pts[idx] })
  }

  return (
    <div className="relative overflow-x-auto">
      <svg ref={svgRef} width={W} height={H} className="block max-w-full"
        onMouseMove={onMove} onMouseLeave={() => setHover(null)}>
        {ticks.map((t, i) => {
          const y = yFor(t)
          return (
            <g key={i}>
              <line x1={PAD.l} y1={y} x2={W - PAD.r} y2={y} stroke="var(--border)" strokeWidth="1" strokeDasharray="2 3" />
              <text x={PAD.l - 5} y={y + 3} textAnchor="end" fontSize="9" className="font-mono" fill="var(--text-faint)">{t}</text>
            </g>
          )
        })}
        {pts.map((p, i) => {
          const cx = PAD.l + i * slot + slot / 2
          const a = p.a || 0, b = p.b || 0
          if (stacked) {
            const hA = (a / max) * innerH, hB = (b / max) * innerH
            const yA = PAD.t + innerH - hA
            const yB = yA - hB
            return (
              <g key={i}>
                <rect x={cx - barW / 2} y={yA} width={barW} height={hA} rx={1.5} fill={colorA} opacity={hover && hover.idx === i ? 1 : 0.85} />
                <rect x={cx - barW / 2} y={yB} width={barW} height={hB} rx={1.5} fill={colorB} opacity={hover && hover.idx === i ? 1 : 0.85} />
              </g>
            )
          }
          const hA = (a / max) * innerH, hB = (b / max) * innerH
          return (
            <g key={i}>
              <rect x={cx - barW - 1} y={PAD.t + innerH - hA} width={barW} height={hA} rx={1.5} fill={colorA} opacity={hover && hover.idx === i ? 1 : 0.8} />
              <rect x={cx + 1} y={PAD.t + innerH - hB} width={barW} height={hB} rx={1.5} fill={colorB} opacity={hover && hover.idx === i ? 1 : 0.8} />
            </g>
          )
        })}
        {[0, Math.floor(pts.length / 2), pts.length - 1].filter((v, i, a) => a.indexOf(v) === i).map(i => (
          <text key={i} x={PAD.l + i * slot + slot / 2} y={H - 7} textAnchor="middle" fontSize="9" className="font-mono" fill="var(--text-faint)">
            {fmtDate(pts[i].date, { month: 'short', day: 'numeric' })}
          </text>
        ))}
      </svg>

      <div className="flex items-center gap-4 mt-1.5 text-[10px] font-mono text-[var(--text-faint)]">
        <span className="inline-flex items-center gap-1.5"><span className="w-2.5 h-2.5 rounded-[2px]" style={{ background: colorA }} />{labelA}</span>
        <span className="inline-flex items-center gap-1.5"><span className="w-2.5 h-2.5 rounded-[2px]" style={{ background: colorB }} />{labelB}</span>
      </div>

      {hover && (
        <div className="pointer-events-none absolute z-20 -translate-x-1/2 -translate-y-full px-2.5 py-1.5 rounded-[var(--radius-badge)] bg-[var(--bg)] border border-[var(--border2)] shadow-[var(--shadow-float)] whitespace-nowrap"
          style={{ left: `${((PAD.l + hover.idx * slot + slot / 2) / W) * 100}%`, top: 0 }}>
          <div className="text-[10px] font-mono text-[var(--text-faint)] mb-0.5">{fmtDate(hover.p.date)}</div>
          <div className="text-xs font-semibold tabular-nums" style={{ color: colorA }}>{labelA}: {hover.p.a || 0}</div>
          <div className="text-xs font-semibold tabular-nums" style={{ color: colorB }}>{labelB}: {hover.p.b || 0}</div>
        </div>
      )}
    </div>
  )
}

// Small headline metric used inside panels.
function MiniMetric({ icon, label, value, accent, sub }) {
  return (
    <div className="rounded-[var(--radius-card)] border border-[var(--border)] bg-[var(--bg)] px-3.5 py-3">
      <div className="flex items-center gap-1.5 text-[10px] font-mono uppercase tracking-widest text-[var(--text-faint)]">
        <span style={{ color: accent }}>{icon}</span>{label}
      </div>
      <div className="mt-1 font-display text-xl font-semibold text-[var(--text)] tabular-nums tracking-tight">{value}</div>
      {sub && <div className="text-[10px] text-[var(--text-faint)] mt-0.5">{sub}</div>}
    </div>
  )
}

// ── pull-request analytics panel ──────────────────────────────────────────────

function PullRequestPanel({ filters }) {
  const { data, loading } = usePullRequests(filters)
  const d = data ?? {}
  const series = useMemo(
    () => (d.throughput || []).map(t => ({ date: t.date, a: t.opened || 0, b: t.merged || 0 })),
    [d.throughput]
  )
  const mergePct = d.mergeRate != null ? d.mergeRate * 100 : null

  return (
    <Card padding="lg">
      <div className="flex items-center justify-between mb-4">
        <div>
          <h2 className="text-sm font-semibold text-[var(--text)] flex items-center gap-2">
            <GitPullRequest size={15} className="text-[var(--brand-teal)]" /> Pull requests
          </h2>
          <p className="text-xs text-[var(--text-faint)] mt-0.5">Merge rate, lead time, and opened/merged throughput.</p>
        </div>
        {!loading && <span className="text-xs font-mono text-[var(--text-faint)]">{fmtNum(d.total)} total</span>}
      </div>

      {loading ? (
        <div className="h-[260px] rounded-[var(--radius-card)] bg-[var(--bg-surface2)] animate-pulse" />
      ) : (
        <>
          <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-3 mb-5">
            <MiniMetric icon={<GitMerge size={13} />} label="Merge rate" accent="#22c55e"
              value={fmtPct(mergePct)} sub={`${fmtNum(d.merged)} merged`} />
            <MiniMetric icon={<Timer size={13} />} label="Lead p50" accent="var(--brand-teal)"
              value={fmtHours(d.leadTimeP50Hours)} sub="first commit → merge" />
            <MiniMetric icon={<Timer size={13} />} label="Lead p90" accent="var(--brand-indigo)"
              value={fmtHours(d.leadTimeP90Hours)} sub="slowest 10%" />
            <MiniMetric icon={<CircleDot size={13} />} label="Open" accent="#eab308"
              value={fmtNum(d.open)} sub={`${fmtNum(d.closed)} closed`} />
            <MiniMetric icon={<Sigma size={13} />} label="Avg files" accent="var(--brand-indigo)"
              value={fmtAvg(d.avgChangedFiles)} sub="changed / PR" />
          </div>
          <TwoSeriesBars series={series} labelA="opened" labelB="merged"
            colorA="#2DD4BF" colorB="#22c55e" />
        </>
      )}
    </Card>
  )
}

// ── issue-flow panel ──────────────────────────────────────────────────────────

const ISSUE_STATES = [
  { key: 'open', label: 'Open', color: '#eab308', icon: <CircleDot size={13} /> },
  { key: 'inProgress', label: 'In progress', color: '#2DD4BF', icon: <Activity size={13} /> },
  { key: 'done', label: 'Done', color: '#22c55e', icon: <CircleCheck size={13} /> },
  { key: 'closed', label: 'Closed', color: '#94a3b8', icon: <X size={13} /> },
]

function IssueFlowPanel({ filters }) {
  const { data, loading } = useIssueFlow(filters)
  const d = data ?? {}
  // Merge opened + closedSeries by date for the chart.
  const series = useMemo(() => {
    const m = new Map()
    for (const o of (d.opened || [])) if (o?.date) m.set(o.date.slice(0, 10), { date: o.date, a: o.count || 0, b: 0 })
    for (const c of (d.closedSeries || [])) {
      if (!c?.date) continue
      const k = c.date.slice(0, 10)
      const e = m.get(k) || { date: c.date, a: 0, b: 0 }
      e.b = c.count || 0
      m.set(k, e)
    }
    return [...m.values()].sort((x, y) => new Date(x.date) - new Date(y.date))
  }, [d.opened, d.closedSeries])

  const total = ISSUE_STATES.reduce((a, s) => a + (d[s.key] || 0), 0)
  const byProject = useMemo(
    () => [...(d.byProject || [])].sort((a, b) => (b.open + b.done) - (a.open + a.done)),
    [d.byProject]
  )

  return (
    <Card padding="lg">
      <div className="flex items-center justify-between mb-4">
        <div>
          <h2 className="text-sm font-semibold text-[var(--text)] flex items-center gap-2">
            <ListChecks size={15} className="text-[var(--brand-indigo)]" /> Issue flow
          </h2>
          <p className="text-xs text-[var(--text-faint)] mt-0.5">State breakdown, opened vs closed over time, and per-project split.</p>
        </div>
        {!loading && <span className="text-xs font-mono text-[var(--text-faint)]">{fmtNum(total)} issues</span>}
      </div>

      {loading ? (
        <div className="h-[280px] rounded-[var(--radius-card)] bg-[var(--bg-surface2)] animate-pulse" />
      ) : (
        <>
          {/* state breakdown bar */}
          <div className="mb-5">
            <div className="flex h-2.5 rounded-full overflow-hidden bg-[var(--bg-surface3)]">
              {ISSUE_STATES.map(s => {
                const v = d[s.key] || 0
                const pct = total ? (v / total) * 100 : 0
                return pct > 0 ? <div key={s.key} style={{ width: `${pct}%`, background: s.color }} title={`${s.label}: ${v}`} /> : null
              })}
            </div>
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-2 mt-3">
              {ISSUE_STATES.map(s => (
                <div key={s.key} className="flex items-center gap-2">
                  <span style={{ color: s.color }}>{s.icon}</span>
                  <div>
                    <div className="font-display text-lg font-semibold text-[var(--text)] tabular-nums leading-none">{fmtNum(d[s.key])}</div>
                    <div className="text-[10px] font-mono uppercase tracking-wide text-[var(--text-faint)] mt-0.5">{s.label}</div>
                  </div>
                </div>
              ))}
            </div>
          </div>

          <TwoSeriesBars series={series} labelA="opened" labelB="closed"
            colorA="#6366F1" colorB="#22c55e" height={170} />

          {/* per-project */}
          {byProject.length > 0 && (
            <div className="mt-5 overflow-x-auto">
              <table className="w-full text-xs">
                <thead>
                  <tr className="border-b border-[var(--border)] text-[var(--text-faint)]">
                    <th className="text-left font-mono uppercase tracking-wider font-medium px-2 py-2">Project</th>
                    <th className="text-left font-mono uppercase tracking-wider font-medium px-2 py-2 min-w-[160px]">Open / done</th>
                    <th className="text-right font-mono uppercase tracking-wider font-medium px-2 py-2">Open</th>
                    <th className="text-right font-mono uppercase tracking-wider font-medium px-2 py-2">Done</th>
                  </tr>
                </thead>
                <tbody>
                  {byProject.map((p, i) => {
                    const t = (p.open || 0) + (p.done || 0)
                    const donePct = t ? (p.done / t) * 100 : 0
                    return (
                      <tr key={p.project || i} className="border-b border-[var(--border)] hover:bg-[var(--bg-surface2)] transition-colors">
                        <td className="px-2 py-2.5 text-[var(--text-dim)] font-medium truncate max-w-[200px]">{p.project}</td>
                        <td className="px-2 py-2.5">
                          <div className="h-1.5 rounded-full bg-[#eab308]/30 overflow-hidden min-w-[60px]">
                            <div className="h-full rounded-full bg-[#22c55e]" style={{ width: `${donePct}%` }} />
                          </div>
                        </td>
                        <td className="px-2 py-2.5 text-right font-mono tabular-nums text-[#eab308]">{fmtNum(p.open)}</td>
                        <td className="px-2 py-2.5 text-right font-mono tabular-nums text-[#22c55e]">{fmtNum(p.done)}</td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}
    </Card>
  )
}

// ── agent-share panel ─────────────────────────────────────────────────────────

function AgentSharePanel({ filters }) {
  const { data, loading } = useAgentShare(filters)
  const d = data ?? {}
  const total = (d.agentCommits || 0) + (d.humanCommits || 0)
  const agentPct = d.agentPct != null ? d.agentPct : (total ? (d.agentCommits / total) * 100 : 0)
  const series = useMemo(
    () => (d.overTime || []).map(o => ({ date: o.date, a: o.agent || 0, b: o.human || 0 })),
    [d.overTime]
  )

  return (
    <Card padding="lg">
      <div className="flex items-center justify-between mb-4">
        <div>
          <h2 className="text-sm font-semibold text-[var(--text)] flex items-center gap-2">
            <Bot size={15} className="text-[var(--brand-indigo)]" /> Agent vs human
          </h2>
          <p className="text-xs text-[var(--text-faint)] mt-0.5">Share of commits authored by coding agents over time.</p>
        </div>
        {!loading && <span className="text-xs font-mono text-[var(--text-faint)]">{fmtNum(total)} commits</span>}
      </div>

      {loading ? (
        <div className="h-[240px] rounded-[var(--radius-card)] bg-[var(--bg-surface2)] animate-pulse" />
      ) : (
        <>
          <div className="flex items-end gap-6 mb-4">
            <div>
              <div className="font-display text-4xl font-semibold tracking-tight tabular-nums"
                style={{ background: 'linear-gradient(135deg,#6366F1,#2DD4BF)', WebkitBackgroundClip: 'text', WebkitTextFillColor: 'transparent' }}>
                {fmtPct(agentPct)}
              </div>
              <div className="text-[11px] font-mono text-[var(--text-faint)] mt-1">agent-authored</div>
            </div>
            <div className="flex-1 grid grid-cols-2 gap-3">
              <MiniMetric icon={<Cpu size={13} />} label="Agent" accent="#6366F1" value={fmtNum(d.agentCommits)} />
              <MiniMetric icon={<User size={13} />} label="Human" accent="#2DD4BF" value={fmtNum(d.humanCommits)} />
            </div>
          </div>
          {/* split bar */}
          <div className="flex h-2.5 rounded-full overflow-hidden bg-[var(--bg-surface3)] mb-5">
            <div style={{ width: `${agentPct}%`, background: '#6366F1' }} />
            <div style={{ width: `${100 - agentPct}%`, background: '#2DD4BF' }} />
          </div>
          <TwoSeriesBars series={series} labelA="agent" labelB="human"
            colorA="#6366F1" colorB="#2DD4BF" stacked height={170} />
        </>
      )}
    </Card>
  )
}

// ── per-project table ─────────────────────────────────────────────────────────

function ProjectTable({ filters }) {
  const { data: projects, loading } = useProjects(filters)
  const sorted = useMemo(
    () => [...(projects || [])].sort((a, b) => (b.commits || 0) - (a.commits || 0)),
    [projects]
  )
  return (
    <Card padding="lg">
      <div className="flex items-center justify-between mb-4">
        <div>
          <h2 className="text-sm font-semibold text-[var(--text)] flex items-center gap-2">
            <Folder size={15} className="text-[var(--brand-teal)]" /> Projects
          </h2>
          <p className="text-xs text-[var(--text-faint)] mt-0.5">Commits, contributors, churn, and issue health per project.</p>
        </div>
        {!loading && <span className="text-xs font-mono text-[var(--text-faint)]">{sorted.length} projects</span>}
      </div>
      {loading ? (
        <div className="space-y-2">
          {Array.from({ length: 4 }).map((_, i) => <div key={i} className="h-9 rounded bg-[var(--bg-surface2)] animate-pulse" />)}
        </div>
      ) : sorted.length === 0 ? (
        <div className="py-8 text-center text-sm text-[var(--text-faint)]">No projects in this range.</div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead>
              <tr className="border-b border-[var(--border)] text-[var(--text-faint)]">
                <th className="text-left font-mono uppercase tracking-wider font-medium px-2 py-2">Project</th>
                <th className="text-right font-mono uppercase tracking-wider font-medium px-2 py-2">Commits</th>
                <th className="text-right font-mono uppercase tracking-wider font-medium px-2 py-2 hidden sm:table-cell">Contributors</th>
                <th className="text-right font-mono uppercase tracking-wider font-medium px-2 py-2">Open</th>
                <th className="text-right font-mono uppercase tracking-wider font-medium px-2 py-2">Done</th>
                <th className="text-right font-mono uppercase tracking-wider font-medium px-2 py-2 hidden md:table-cell">Lines</th>
              </tr>
            </thead>
            <tbody>
              {sorted.map((p, i) => (
                <tr key={p.projectId || p.name || i} className="border-b border-[var(--border)] hover:bg-[var(--bg-surface2)] transition-colors">
                  <td className="px-2 py-2.5">
                    <div className="flex items-center gap-2 min-w-0">
                      <Folder size={13} className="text-[var(--brand-teal)] shrink-0" />
                      <span className="text-[var(--text-dim)] font-medium truncate">{p.name}</span>
                    </div>
                  </td>
                  <td className="px-2 py-2.5 text-right font-mono tabular-nums text-[var(--text-dim)]">{fmtNum(p.commits)}</td>
                  <td className="px-2 py-2.5 text-right font-mono tabular-nums text-[var(--text-muted)] hidden sm:table-cell">{fmtNum(p.contributors)}</td>
                  <td className="px-2 py-2.5 text-right font-mono tabular-nums text-[#eab308]">{fmtNum(p.openIssues)}</td>
                  <td className="px-2 py-2.5 text-right font-mono tabular-nums text-[#22c55e]">{fmtNum(p.doneIssues)}</td>
                  <td className="px-2 py-2.5 text-right font-mono tabular-nums whitespace-nowrap hidden md:table-cell">
                    <span className="text-green-400">+{fmtNum(p.additions)}</span>{' '}
                    <span className="text-red-400">−{fmtNum(p.deletions)}</span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  )
}

// ── section heading ───────────────────────────────────────────────────────────

function SectionHeading({ children }) {
  return (
    <div className="flex items-center gap-3 pt-2">
      <h2 className="text-[11px] font-mono uppercase tracking-[0.18em] text-[var(--text-faint)]">{children}</h2>
      <div className="flex-1 h-px bg-[var(--border)]" />
    </div>
  )
}

// ── page ──────────────────────────────────────────────────────────────────────

export default function Analytics() {
  const [filters, setFilters] = useState({ from: isoDaysAgo(273), to: todayISO(), repo: '', author: '' })
  const [preset, setPreset] = useState('9mo')
  const [selectedDate, setSelectedDate] = useState(null)

  const { data: summary, loading: sumLoading, error: sumError } = useSummary(filters)
  const { data: heatmap, loading: heatLoading } = useHeatmap(filters)
  const { data: contributors, loading: contribLoading } = useContributors(filters)
  const { data: repos, loading: repoLoading } = useRepoStats(filters)

  return (
    <div className="w-full space-y-6">
      {/* Header */}
      <Reveal>
        <div className="flex items-start justify-between gap-4">
          <div className="flex items-start gap-3">
            <span className="mt-0.5 grid place-items-center w-9 h-9 rounded-[var(--radius-btn)] bg-[var(--brand-teal)]/10 border border-[var(--brand-teal)]/20 shrink-0">
              <BarChart3 size={17} className="text-[var(--brand-teal)]" />
            </span>
            <div>
              <h1 className="font-display text-2xl font-semibold text-[var(--text)] tracking-tight">Analytics</h1>
              <p className="text-sm text-[var(--text-faint)] mt-1">
                Delivery insight across every connected repo, PR, issue and project — derived from git, not self-reported.
              </p>
            </div>
          </div>
          <a
            href="#leaderboard"
            className="hidden sm:inline-flex items-center gap-1 text-xs font-mono text-[var(--text-faint)] hover:text-[var(--brand-teal)] transition-colors mt-1"
          >
            jump to people <ArrowUpRight size={13} />
          </a>
        </div>
      </Reveal>

      {/* Filters */}
      <Reveal delay={0.05}>
        <FiltersBar
          filters={filters} setFilters={setFilters}
          preset={preset} setPreset={setPreset}
          contributors={contributors} repos={repos}
        />
      </Reveal>

      {sumError && (
        <Card className="border-red-500/25 bg-red-500/[0.04]">
          <div className="flex items-start gap-3">
            <AlertTriangle size={16} className="text-red-400 mt-0.5 shrink-0" />
            <div>
              <p className="text-sm text-red-400">{sumError}</p>
              <p className="text-xs text-[var(--text-faint)] mt-0.5">The backend may not be running yet.</p>
            </div>
          </div>
        </Card>
      )}

      {/* Stat tiles */}
      <Reveal delay={0.08}><StatTiles summary={summary} loading={sumLoading} /></Reveal>

      {/* Heatmap + drill-down */}
      <Reveal delay={0.1} inView>
        <Card padding="lg">
          <div className="flex items-center justify-between mb-4">
            <div>
              <h2 className="text-sm font-semibold text-[var(--text)]">Contribution heatmap</h2>
              <p className="text-xs text-[var(--text-faint)] mt-0.5">Click any day to see its commits</p>
            </div>
          </div>
          <Heatmap
            heatmap={heatmap} loading={heatLoading} endISO={filters.to}
            selectedDate={selectedDate} onSelect={setSelectedDate}
          />
        </Card>
      </Reveal>

      {selectedDate && (
        <Reveal>
          <DayDrillDown date={selectedDate} filters={filters} onClose={() => setSelectedDate(null)} />
        </Reveal>
      )}

      {/* Commits over time */}
      <Reveal delay={0.05} inView><CommitsOverTime filters={filters} /></Reveal>

      {/* ── Delivery: PRs + issues ─────────────────────────────────────────── */}
      <Reveal inView><SectionHeading>Delivery</SectionHeading></Reveal>
      <div className="grid grid-cols-1 xl:grid-cols-2 gap-6 items-start">
        <Reveal delay={0.05} inView><PullRequestPanel filters={filters} /></Reveal>
        <Reveal delay={0.08} inView><IssueFlowPanel filters={filters} /></Reveal>
      </div>

      {/* ── Authorship: agent vs human ─────────────────────────────────────── */}
      <Reveal inView><SectionHeading>Authorship</SectionHeading></Reveal>
      <Reveal delay={0.05} inView><AgentSharePanel filters={filters} /></Reveal>

      {/* ── People & projects ──────────────────────────────────────────────── */}
      <Reveal inView><SectionHeading>People & projects</SectionHeading></Reveal>

      {/* Leaderboard */}
      <div id="leaderboard" />
      <Reveal delay={0.05} inView><ContributorLeaderboard contributors={contributors} loading={contribLoading} /></Reveal>

      <div className="grid grid-cols-1 xl:grid-cols-2 gap-6 items-start">
        {/* Projects */}
        <Reveal delay={0.05} inView><ProjectTable filters={filters} /></Reveal>
        {/* Repos */}
        <Reveal delay={0.08} inView><RepoTable repos={repos} loading={repoLoading} /></Reveal>
      </div>
    </div>
  )
}
