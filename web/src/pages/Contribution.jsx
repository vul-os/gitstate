/**
 * Contribution — the six gaming-resistant dimensions, cross-repo.
 *
 * This is deliberately texture, never a leaderboard: the README is emphatic
 * that contribution is "shown as texture across six dimensions, never a
 * single rank, never a bonus formula." The weight tuner exists so a reader can
 * ask "what if I cared more about review than shipped volume?" — not to let
 * anyone crown a winner. Composite recomputes live, client-side, as the
 * sliders move; nothing is "ranked" by the daemon.
 */
import { useMemo, useState } from 'react'
import {
  Scale, Users, Bot, GitMerge, MessageSquareText, RotateCcw, Save,
} from 'lucide-react'
import { Card } from '../components/ui/Card.jsx'
import { Button } from '../components/ui/Button.jsx'
import { Badge } from '../components/ui/Badge.jsx'
import { StatCard } from '../components/ui/StatCard.jsx'
import { PageHeader, Spinner, ErrorState, EmptyState } from '../components/common.jsx'
import { useAsync, useAction } from '../lib/hooks.js'
import { contributionRollup, weights, saveWeights, resetWeights } from '../lib/api.js'
import { compact } from '../lib/analyticsView.js'

const DIMS = [
  ['shipped', 'Shipped'],
  ['review', 'Review'],
  ['effort', 'Effort'],
  ['quality', 'Quality'],
  ['ownership', 'Ownership'],
  ['durability', 'Durability'],
]

async function load() {
  const [rows, w] = await Promise.all([
    contributionRollup({}),
    weights(),
  ])
  return { rows, weights: w }
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

/** The same small dimension bar used on the per-repo contribution table. */
function DimBar({ value }) {
  const v = Math.max(0, Math.min(100, value ?? 0))
  return (
    <div className="flex items-center gap-2">
      <div className="h-1.5 w-14 overflow-hidden rounded-full bg-[var(--bg-surface3)]">
        <div className="h-full rounded-full bg-[var(--brand-teal)]" style={{ width: `${v}%` }} />
      </div>
      <span className="w-7 text-right font-mono text-xs tabular-nums text-[var(--text-muted)]">
        {value == null ? '—' : v.toFixed(0)}
      </span>
    </div>
  )
}

/**
 * Recompute the composite from LOCAL weights rather than trusting the
 * server's `composite` field — that's what makes the tuner feel instant, and
 * it lets a reader explore "what if" without writing anything. The formula
 * mirrors the daemon's: a weighted mean, so a dimension left at 0 simply
 * drops out rather than zeroing the whole score.
 */
function recompute(dimensions, w) {
  let num = 0
  let den = 0
  for (const [key] of DIMS) {
    const dv = dimensions?.[key]
    const wv = w?.[key]
    if (dv == null || wv == null) continue
    num += dv * wv
    den += wv
  }
  return den > 0 ? num / den : 0
}

function WeightTuner({ local, persisted, onChange, onSave, onReset, saving, resetting }) {
  const dirty = DIMS.some(([key]) => (local?.[key] ?? 0) !== (persisted?.[key] ?? 0))
  return (
    <Panel
      title="Weight tuner"
      subtitle="Re-weigh the six dimensions to explore the texture — the table below re-ranks live as you drag."
      action={
        <div className="flex items-center gap-2">
          {dirty && (
            <span className="font-mono text-[11px] text-[var(--chart-3)]" role="status">
              unsaved changes
            </span>
          )}
          <Button
            variant="outline" size="xs" onClick={onReset} disabled={resetting}
            leftIcon={<RotateCcw size={13} />}
          >
            {resetting ? 'Resetting…' : 'Reset to defaults'}
          </Button>
          <Button
            variant="primary" size="xs" onClick={onSave} disabled={saving || !dirty}
            leftIcon={<Save size={13} />}
          >
            {saving ? 'Saving…' : 'Save weights'}
          </Button>
        </div>
      }
    >
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {DIMS.map(([key, label]) => {
          const v = local?.[key] ?? 0
          const inputId = `weight-${key}`
          return (
            <div key={key} className="flex flex-col gap-1.5">
              <label htmlFor={inputId} className="flex items-center justify-between text-xs text-[var(--text-muted)]">
                <span>{label}</span>
                <span className="font-mono tabular-nums text-[var(--text)]">{v.toFixed(2)}</span>
              </label>
              <input
                id={inputId}
                type="range"
                min={0}
                max={1}
                step={0.05}
                value={v}
                onChange={(e) => onChange(key, Number(e.target.value))}
                className="w-full accent-[var(--brand-teal)]"
              />
            </div>
          )
        })}
      </div>
    </Panel>
  )
}

/**
 * Repo spread, capped at two chips plus a "+N" overflow.
 *
 * Someone active across five repos would otherwise stack five full slugs into
 * a narrow column and treble the row height, which buries the dimension bars
 * this table exists to show. The full list stays available on hover.
 */
function RepoChips({ repos, max = 2 }) {
  if (!repos?.length) return <span className="text-xs text-[var(--text-faint)]">—</span>
  const shown = repos.slice(0, max)
  const rest = repos.length - shown.length
  const short = (slug) => slug.split('/').pop()
  return (
    <div className="flex flex-wrap items-center gap-1" title={repos.join(', ')}>
      {shown.map((slug) => (
        <Badge key={slug} color="default">{short(slug)}</Badge>
      ))}
      {rest > 0 && (
        <span className="font-mono text-[10px] text-[var(--text-faint)]">+{rest}</span>
      )}
    </div>
  )
}

function ContributorTexture({ rows, localWeights }) {
  // Recompute per row with the live weights, then sort — the "ranking" is
  // purely a reading aid for this exploratory view, not a scored leaderboard.
  const ranked = useMemo(
    () =>
      (rows ?? [])
        .map((r) => ({ ...r, liveComposite: recompute(r.dimensions, localWeights) }))
        .sort((a, b) => b.liveComposite - a.liveComposite),
    [rows, localWeights],
  )

  if (!ranked.length) {
    return <p className="py-6 text-center text-sm text-[var(--text-faint)]">No contribution derived in this range</p>
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[860px] text-sm">
        <thead>
          <tr className="border-b border-[var(--border)] text-left">
            <th className="py-2 pr-3 font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-[var(--text-faint)]">
              Contributor
            </th>
            {DIMS.map(([key, label]) => (
              <th key={key} className="px-3 py-2 font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-[var(--text-faint)]">
                {label}
              </th>
            ))}
            <th className="px-3 py-2 font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-[var(--text-faint)]">
              Repos
            </th>
            <th className="px-3 py-2 text-right font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-[var(--text-faint)]">
              Composite
            </th>
          </tr>
        </thead>
        <tbody>
          {ranked.map((c) => (
            <tr key={c.contributor_id} className="border-b border-[var(--border)] last:border-0">
              <td className="py-2.5 pr-3">
                <div className="flex items-center gap-2">
                  <span className="font-medium text-[var(--text)]">
                    {c.display_name || c.primary_email || c.contributor_id}
                  </span>
                  {c.is_agent && <Badge color="indigo">agent</Badge>}
                </div>
                {c.primary_email && (
                  <span className="font-mono text-[11px] text-[var(--text-faint)]">{c.primary_email}</span>
                )}
              </td>
              {DIMS.map(([key]) => (
                <td key={key} className="px-3 py-2.5"><DimBar value={c.dimensions?.[key]} /></td>
              ))}
              <td className="px-3 py-2.5"><RepoChips repos={c.repos} /></td>
              <td className="px-3 py-2.5 text-right">
                <span className="font-display text-base font-semibold tabular-nums text-[var(--brand-teal)]">
                  {c.liveComposite.toFixed(1)}
                </span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export default function Contribution() {
  const { data, loading, error, reload } = useAsync(load, [])
  const [runSave, { pending: saving }] = useAction(saveWeights)
  const [runReset, { pending: resetting }] = useAction(resetWeights)

  // Local, editable copy of the weights — the slider state. Seeded from the
  // server once data lands; `null` means "not seeded yet".
  const [local, setLocal] = useState(null)
  const persisted = data?.weights ?? null
  const effective = local ?? persisted

  if (loading) return <div><PageHeader title="Contribution" /><Spinner /></div>
  if (error) return <div><PageHeader title="Contribution" /><ErrorState error={error} onRetry={reload} /></div>

  const rows = data?.rows ?? []

  if (!rows.length) {
    return (
      <div>
        <PageHeader
          title="Contribution"
          subtitle="Texture across six gaming-resistant dimensions — never a leaderboard or ranking."
        />
        <EmptyState
          icon={<Scale size={22} />}
          title="No contribution derived yet"
          description="Scan a repo to compute shipped, review, effort, quality, ownership and durability per contributor."
        />
      </div>
    )
  }

  function setWeight(key, value) {
    setLocal({ ...effective, [key]: value })
  }

  async function doSave() {
    const saved = await runSave(effective)
    setLocal(saved)
    reload()
  }

  async function doReset() {
    const def = await runReset()
    setLocal(def)
    reload()
  }

  const contributorCount = rows.length
  const agentCount = rows.filter((r) => r.is_agent).length
  const agentShare = contributorCount ? agentCount / contributorCount : null
  const totalMergedPrs = rows.reduce((sum, r) => sum + (r.raw?.merged_prs ?? 0), 0)
  const totalReviews = rows.reduce((sum, r) => sum + (r.raw?.reviews_done ?? 0), 0)

  return (
    <div>
      <PageHeader
        title="Contribution"
        subtitle="Texture across six gaming-resistant dimensions, merged across every tracked repo — never a leaderboard or ranking."
      />

      <div className="mb-5 grid grid-cols-2 gap-3 md:grid-cols-4">
        <StatCard
          label="Contributors" value={contributorCount}
          accent="var(--chart-5)" icon={<Users size={14} />}
        />
        <StatCard
          label="Agent share"
          value={agentShare == null ? '—' : `${Math.round(agentShare * 100)}%`}
          sublabel={`${agentCount} of ${contributorCount}`}
          accent="var(--brand-indigo)" icon={<Bot size={14} />}
        />
        <StatCard
          label="Merged PRs" value={compact(totalMergedPrs)}
          accent="var(--chart-1)" icon={<GitMerge size={14} />}
        />
        <StatCard
          label="Reviews" value={compact(totalReviews)}
          accent="var(--chart-2)" icon={<MessageSquareText size={14} />}
        />
      </div>

      <div className="mb-5">
        <WeightTuner
          local={effective}
          persisted={persisted}
          onChange={setWeight}
          onSave={doSave}
          onReset={doReset}
          saving={saving}
          resetting={resetting}
        />
      </div>

      <Panel
        title="Contributors"
        subtitle="Six dimensions plus agent identity and repo spread — raw texture, sorted only to make the tuner's effect legible"
      >
        <ContributorTexture rows={rows} localWeights={effective} />
      </Panel>
    </div>
  )
}
