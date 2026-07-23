/**
 * Heatmap — the contribution calendar.
 *
 * Magnitude encoded on a SEQUENTIAL single-hue ramp (`--heat-1`…`--heat-5`),
 * never the categorical palette: the cells mean "more vs less", not "which".
 * Empty days get `--heat-0`, a well rather than a ramp step, so "no commits"
 * reads as absence instead of as the lowest bucket.
 *
 * Expects the dense, zero-filled day array the daemon returns
 * (`GET /api/analytics` → `heatmap`), each entry `{ date, weekday, commits,
 * additions, deletions }` — every day in range is present, so the grid needs no
 * client-side gap filling.
 *
 * Props:
 *   days      DayBucket[]  dense, oldest first
 *   metric    'commits' | 'additions'   which field drives the ramp
 *   onSelect  (day) => void   optional click handler
 */
import { useMemo, useState, useId } from 'react'
import { useMeasure } from '../../lib/useMeasure.js'

const WEEKDAY_LABELS = ['Mon', '', 'Wed', '', 'Fri', '', '']
const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

const GAP = 3
const LEFT_GUTTER = 28
const TOP_GUTTER = 16
// A 30-day range must not blow up into giant tiles, and a 1-year range must
// stay legible; the cell size is fitted to the container between these bounds.
const MIN_CELL = 9
const MAX_CELL = 17

/**
 * Bucket values onto the 5-step ramp. Thresholds come from the distribution of
 * NON-EMPTY days (quartiles), not from the max: one 29-commit release day
 * would otherwise flatten every ordinary day into step 1.
 */
function makeScale(values) {
  const nonZero = values.filter((v) => v > 0).sort((a, b) => a - b)
  if (!nonZero.length) return () => 0
  const at = (p) => nonZero[Math.min(nonZero.length - 1, Math.floor(p * nonZero.length))]
  const cuts = [at(0.25), at(0.5), at(0.75), at(0.9)]
  return (v) => {
    if (v <= 0) return 0
    if (v <= cuts[0]) return 1
    if (v <= cuts[1]) return 2
    if (v <= cuts[2]) return 3
    if (v <= cuts[3]) return 4
    return 5
  }
}

function formatDate(iso) {
  const [y, m, d] = iso.split('-')
  return `${MONTHS[Number(m) - 1]} ${Number(d)}, ${y}`
}

export function Heatmap({ days = [], metric = 'commits', onSelect }) {
  const titleId = useId()
  const [hover, setHover] = useState(null)
  const [ref, containerWidth] = useMeasure()

  const { columns, scale, monthTicks, total } = useMemo(() => {
    if (!days.length) return { columns: [], scale: () => 0, monthTicks: [], total: 0 }

    // Chunk into week columns. The first column is padded so its cells sit on
    // the correct weekday row even when the range starts mid-week.
    const cols = []
    let current = new Array(days[0].weekday).fill(null)
    for (const day of days) {
      current.push(day)
      if (day.weekday === 6) {
        cols.push(current)
        current = []
      }
    }
    if (current.length) cols.push(current)

    const values = days.map((d) => d[metric] ?? 0)

    // One label per month, at the column where that month first appears.
    const ticks = []
    let lastMonth = null
    cols.forEach((col, ci) => {
      const first = col.find(Boolean)
      if (!first) return
      const month = first.date.slice(5, 7)
      if (month !== lastMonth) {
        lastMonth = month
        // Skip a label that would collide with the previous one.
        if (!ticks.length || ci - ticks[ticks.length - 1].col >= 3) {
          ticks.push({ col: ci, label: MONTHS[Number(month) - 1] })
        }
      }
    })

    return {
      columns: cols,
      scale: makeScale(values),
      monthTicks: ticks,
      total: values.reduce((a, b) => a + b, 0),
    }
  }, [days, metric])

  if (!days.length) return null

  // Fit the grid to the panel rather than leaving a ragged gutter of dead space
  // on the right (or forcing a scrollbar on a narrow one).
  const available = (containerWidth || 720) - LEFT_GUTTER
  const cell = Math.round(
    Math.min(MAX_CELL, Math.max(MIN_CELL, available / Math.max(1, columns.length) - GAP)),
  )
  const step = cell + GAP
  const width = LEFT_GUTTER + columns.length * step
  const height = TOP_GUTTER + 7 * step

  return (
    <div ref={ref} className="flex flex-col gap-3">
      <div className="overflow-x-auto pb-1">
        <svg
          width={width}
          height={height}
          role="img"
          aria-labelledby={titleId}
          data-chart="heatmap"
          data-days={days.length}
          style={{ display: 'block', minWidth: width }}
        >
          <title id={titleId}>
            {`${total.toLocaleString()} ${metric} across ${days.length} days`}
          </title>

          {monthTicks.map((t) => (
            <text
              key={`${t.col}-${t.label}`}
              x={LEFT_GUTTER + t.col * step}
              y={9}
              className="font-mono"
              fontSize="9.5"
              fill="var(--chart-axis)"
            >
              {t.label}
            </text>
          ))}

          {WEEKDAY_LABELS.map((label, row) =>
            label ? (
              <text
                key={label}
                x={0}
                y={TOP_GUTTER + row * step + cell - 1}
                className="font-mono"
                fontSize="9.5"
                fill="var(--chart-axis)"
              >
                {label}
              </text>
            ) : null,
          )}

          {columns.map((col, ci) =>
            col.map((day, row) => {
              if (!day) return null
              const value = day[metric] ?? 0
              // Ramp bucket — distinct from `step`, the grid pitch.
              const level = scale(value)
              const active = hover?.date === day.date
              return (
                <rect
                  key={day.date}
                  data-day={day.date}
                  data-level={level}
                  x={LEFT_GUTTER + ci * step}
                  y={TOP_GUTTER + row * step}
                  width={cell}
                  height={cell}
                  rx={2.5}
                  fill={`var(--heat-${level})`}
                  stroke={active ? 'var(--text)' : 'transparent'}
                  strokeWidth={active ? 1.5 : 0}
                  style={{ cursor: onSelect ? 'pointer' : 'default' }}
                  onMouseEnter={() => setHover(day)}
                  onMouseLeave={() => setHover((h) => (h?.date === day.date ? null : h))}
                  onClick={onSelect ? () => onSelect(day) : undefined}
                />
              )
            }),
          )}
        </svg>
      </div>

      <div className="flex flex-wrap items-center justify-between gap-3">
        <span className="font-mono text-[11px] text-[var(--text-faint)]" aria-live="polite">
          {hover
            ? `${formatDate(hover.date)} · ${hover.commits.toLocaleString()} commit${hover.commits === 1 ? '' : 's'}${
                hover.commits ? ` · +${hover.additions.toLocaleString()} −${hover.deletions.toLocaleString()}` : ''
              }`
            : `${total.toLocaleString()} ${metric} · ${days.length} days`}
        </span>
        <span
          className="flex items-center gap-1.5 font-mono text-[10px] text-[var(--text-faint)]"
          data-heatmap-legend
        >
          <span>less</span>
          {[0, 1, 2, 3, 4, 5].map((s) => (
            <span
              key={s}
              className="inline-block h-[10px] w-[10px] rounded-[2px]"
              style={{ background: `var(--heat-${s})` }}
            />
          ))}
          <span>more</span>
        </span>
      </div>
    </div>
  )
}
