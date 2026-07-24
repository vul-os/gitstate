/**
 * Board — a derived, read-only kanban.
 *
 * There is no ticket store behind this screen: every card is a PR, issue or
 * review the daemon already parsed out of git + the connected forge. Columns
 * are a pure function of `state` — nothing here is dragged, assigned or
 * created, because doing so would imply gitstate writes back to the forge,
 * which it deliberately never does. "No tickets to maintain" is the point,
 * so there is intentionally no dnd library wired up.
 */
import { useMemo, useState } from 'react'
import { KanbanSquare, GitPullRequest, CircleDot, Eye, User } from 'lucide-react'
import { Card } from '../components/ui/Card.jsx'
import { Badge } from '../components/ui/Badge.jsx'
import { PageHeader, Spinner, ErrorState, EmptyState } from '../components/common.jsx'
import { useAsync } from '../lib/hooks.js'
import { listRepos, workItems } from '../lib/api.js'

// A guardrail against rendering thousands of cards in one column, not a real
// data limit — truncation is always announced in the UI, never silent.
const MAX_PER_COLUMN = 200

// State → column. Mirrors the state palette RepoDetail already uses
// (open/draft/in_progress/merged/done/closed) so a work item never falls
// between two columns.
const COLUMNS = [
  { key: 'open', title: 'Open', states: ['open', 'draft'] },
  { key: 'in_progress', title: 'In progress', states: ['in_progress'] },
  { key: 'merged', title: 'Merged', states: ['merged'] },
  { key: 'done', title: 'Done / Closed', states: ['done', 'closed'] },
]

const KIND_ICON = { pr: GitPullRequest, issue: CircleDot, review: Eye }
const KIND_COLOR = { pr: 'indigo', issue: 'teal', review: 'yellow' }

async function loadItems(repoId) {
  if (!repoId) return []
  return workItems(repoId, {})
}

function KindBadge({ kind }) {
  const Icon = KIND_ICON[kind] || CircleDot
  return (
    <Badge color={KIND_COLOR[kind] || 'default'}>
      <Icon size={11} aria-hidden="true" /> {kind || '—'}
    </Badge>
  )
}

function WorkCard({ item }) {
  return (
    <Card padding="sm" className="flex flex-col gap-2">
      <div className="flex items-center justify-between gap-2">
        <span className="truncate font-mono text-[11px] text-[var(--text-faint)]">
          {item.external_ref || '—'}
        </span>
        <KindBadge kind={item.kind} />
      </div>
      <p className="text-sm leading-snug text-[var(--text)]">{item.title || '(untitled)'}</p>
      {item.labels?.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {item.labels.map((l) => <Badge key={l}>{l}</Badge>)}
        </div>
      )}
      <div className="flex items-center gap-1.5 text-xs text-[var(--text-faint)]">
        <User size={11} aria-hidden="true" />
        <span className="truncate">{item.author_login || '—'}</span>
      </div>
    </Card>
  )
}

function Column({ title, items }) {
  const shown = items.slice(0, MAX_PER_COLUMN)
  return (
    <div className="flex min-w-[260px] flex-1 flex-col gap-3">
      <div className="flex items-center justify-between rounded-[var(--radius-btn)] border border-[var(--border)] bg-[var(--bg-surface2)] px-3 py-2">
        <span className="text-xs font-semibold uppercase tracking-[0.08em] text-[var(--text-muted)]">
          {title}
        </span>
        <span className="font-mono text-[11px] tabular-nums text-[var(--text-faint)]">
          {items.length}
        </span>
      </div>
      <div className="flex flex-col gap-2">
        {shown.length ? (
          shown.map((it) => <WorkCard key={it.id} item={it} />)
        ) : (
          <p className="py-6 text-center text-xs text-[var(--text-faint)]">Nothing here</p>
        )}
      </div>
      {items.length > MAX_PER_COLUMN && (
        <p className="text-center text-[11px] text-[var(--text-faint)]">
          Showing the first {MAX_PER_COLUMN} of {items.length}
        </p>
      )}
    </div>
  )
}

export default function Board() {
  const { data: repos, loading: reposLoading, error: reposError, reload: reloadRepos } = useAsync(listRepos, [])
  const [repoId, setRepoId] = useState('')

  // Default to the first repo once repos have loaded, without stomping an
  // explicit user selection on every re-render.
  const effectiveRepoId = repoId || repos?.[0]?.id || ''

  const { data: items, loading: itemsLoading, error: itemsError, reload: reloadItems } = useAsync(
    () => loadItems(effectiveRepoId),
    [effectiveRepoId],
  )

  const columns = useMemo(() => {
    const list = items ?? []
    return COLUMNS.map((col) => ({ ...col, items: list.filter((it) => col.states.includes(it.state)) }))
  }, [items])

  if (reposLoading) return <div><PageHeader title="Board" /><Spinner /></div>
  if (reposError) return <div><PageHeader title="Board" /><ErrorState error={reposError} onRetry={reloadRepos} /></div>

  if (!repos?.length) {
    return (
      <div>
        <PageHeader title="Board" />
        <EmptyState
          icon={<KanbanSquare size={22} />}
          title="No repos yet"
          description="Add and scan a repo — the board derives its columns straight from git and forge state; there's nothing to configure here."
        />
      </div>
    )
  }

  return (
    <div>
      <PageHeader
        title="Board"
        subtitle="Read-only and derived from git + the forge — no tickets to create, assign or drag. Every card here is a PR, issue or review gitstate already parsed."
        actions={
          <label className="flex items-center gap-2">
            <span className="sr-only">Repository</span>
            <select
              value={effectiveRepoId}
              onChange={(e) => setRepoId(e.target.value)}
              className="rounded-[var(--radius-btn)] border border-[var(--border)] bg-[var(--bg-surface2)] px-2.5 py-1.5 font-mono text-[11px] text-[var(--text)] outline-none focus-visible:ring-2 focus-visible:ring-[var(--brand-teal)]"
            >
              {repos.map((r) => <option key={r.id} value={r.id}>{r.slug}</option>)}
            </select>
          </label>
        }
      />

      {itemsLoading && <Spinner />}
      {itemsError && <ErrorState error={itemsError} onRetry={reloadItems} />}
      {!itemsLoading && !itemsError && (
        (items?.length ?? 0) === 0 ? (
          <EmptyState
            title="No work items"
            description="Scan this repo with a connected forge to pull PRs, issues and reviews."
          />
        ) : (
          <div className="flex flex-col gap-4 overflow-x-auto pb-2 lg:flex-row">
            {columns.map((col) => <Column key={col.key} title={col.title} items={col.items} />)}
          </div>
        )
      )}
    </div>
  )
}
