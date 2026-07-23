import { useState } from 'react'
import { Layers, Plus, Trash2, Save, X } from 'lucide-react'
import { Card } from '../components/ui/Card.jsx'
import { Button } from '../components/ui/Button.jsx'
import { Badge } from '../components/ui/Badge.jsx'
import { PageHeader, Spinner, ErrorState, EmptyState } from '../components/common.jsx'
import { useAsync, useAction } from '../lib/hooks.js'
import {
  listContexts, createContext, patchContext, deleteContext, listRepos,
} from '../lib/api.js'

async function loadAll() {
  const [contexts, repos] = await Promise.all([listContexts(), listRepos().catch(() => [])])
  return { contexts, repos }
}

function CreateForm({ onCreated }) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [create, { pending, error }] = useAction(createContext)

  async function submit(e) {
    e.preventDefault()
    if (!name.trim()) return
    await create({ name: name.trim(), description: description.trim(), repo_ids: [], pr_refs: [], notes: '', tags: [] })
    setName(''); setDescription(''); setOpen(false)
    onCreated?.()
  }

  if (!open) {
    return (
      <Button className="mb-6" onClick={() => setOpen(true)} leftIcon={<Plus size={15} />}>New context</Button>
    )
  }
  return (
    <Card padding="md" className="mb-6">
      <form onSubmit={submit} className="flex flex-col gap-3">
        <input
          autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="Context name (e.g. Q3 refactor)"
          className="rounded-[var(--radius-btn)] border border-[var(--border2)] bg-[var(--bg)] px-3 py-2 text-sm text-[var(--text)] placeholder:text-[var(--text-faint)] focus:border-[var(--brand-teal)] focus:outline-none"
        />
        <textarea
          value={description} onChange={(e) => setDescription(e.target.value)} placeholder="What this working set is for…" rows={2}
          className="rounded-[var(--radius-btn)] border border-[var(--border2)] bg-[var(--bg)] px-3 py-2 text-sm text-[var(--text)] placeholder:text-[var(--text-faint)] focus:border-[var(--brand-teal)] focus:outline-none resize-y"
        />
        {error && <p className="text-xs text-[var(--bad)]">{error.message}</p>}
        <div className="flex gap-2">
          <Button type="submit" disabled={pending || !name.trim()}>{pending ? 'Creating…' : 'Create'}</Button>
          <Button type="button" variant="ghost" onClick={() => setOpen(false)}>Cancel</Button>
        </div>
      </form>
    </Card>
  )
}

function TagEditor({ ctx, onChanged }) {
  const [value, setValue] = useState('')
  const [patch] = useAction(patchContext)

  async function addTag(e) {
    e.preventDefault()
    const t = value.trim()
    if (!t || ctx.tags?.includes(t)) { setValue(''); return }
    await patch(ctx.id, { tags: [...(ctx.tags || []), t] })
    setValue(''); onChanged?.()
  }
  async function removeTag(t) {
    await patch(ctx.id, { tags: (ctx.tags || []).filter((x) => x !== t) })
    onChanged?.()
  }

  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {(ctx.tags || []).map((t) => (
        <button key={t} type="button" onClick={() => removeTag(t)} title="Remove tag" className="group">
          <Badge color="teal">{t}<X size={11} className="opacity-60 group-hover:opacity-100" /></Badge>
        </button>
      ))}
      <form onSubmit={addTag} className="inline-flex">
        <input
          value={value} onChange={(e) => setValue(e.target.value)} placeholder="+ tag"
          className="w-20 rounded-[var(--radius-badge)] border border-[var(--border2)] bg-[var(--bg)] px-2 py-0.5 text-xs text-[var(--text)] placeholder:text-[var(--text-faint)] focus:border-[var(--brand-teal)] focus:outline-none"
        />
      </form>
    </div>
  )
}

function RepoAttacher({ ctx, repos, onChanged }) {
  const [patch] = useAction(patchContext)
  async function toggle(repoId) {
    const cur = ctx.repo_ids || []
    const next = cur.includes(repoId) ? cur.filter((r) => r !== repoId) : [...cur, repoId]
    await patch(ctx.id, { repo_ids: next })
    onChanged?.()
  }
  if (!repos?.length) return null
  return (
    <div className="flex flex-wrap gap-1.5">
      {repos.map((r) => {
        const on = (ctx.repo_ids || []).includes(r.id)
        return (
          <button
            key={r.id} type="button" onClick={() => toggle(r.id)}
            className={[
              'rounded-[var(--radius-badge)] border px-2 py-0.5 text-xs font-mono transition-colors',
              on
                ? 'border-[#2DD4BF]/40 bg-[#2DD4BF]/10 text-[#2DD4BF]'
                : 'border-[var(--border)] text-[var(--text-faint)] hover:text-[var(--text)]',
            ].join(' ')}
          >
            {r.slug}
          </button>
        )
      })}
    </div>
  )
}

function NotesEditor({ ctx, onChanged }) {
  const [notes, setNotes] = useState(ctx.notes || '')
  const [dirty, setDirty] = useState(false)
  const [patch, { pending }] = useAction(patchContext)
  async function save() {
    await patch(ctx.id, { notes })
    setDirty(false); onChanged?.()
  }
  return (
    <div className="flex flex-col gap-2">
      <textarea
        value={notes} rows={2}
        onChange={(e) => { setNotes(e.target.value); setDirty(true) }}
        placeholder="Notes…"
        className="rounded-[var(--radius-btn)] border border-[var(--border2)] bg-[var(--bg)] px-3 py-2 text-sm text-[var(--text)] placeholder:text-[var(--text-faint)] focus:border-[var(--brand-teal)] focus:outline-none resize-y"
      />
      {dirty && (
        <Button size="sm" variant="outline" onClick={save} disabled={pending} leftIcon={<Save size={13} />}>
          {pending ? 'Saving…' : 'Save notes'}
        </Button>
      )}
    </div>
  )
}

function ContextCard({ ctx, repos, onChanged }) {
  const [remove, { pending: removing }] = useAction(deleteContext)
  async function doDelete() {
    if (!window.confirm(`Delete context "${ctx.name}"?`)) return
    await remove(ctx.id)
    onChanged?.()
  }
  return (
    <Card padding="lg" className="flex flex-col gap-4">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h3 className="flex items-center gap-2 font-semibold text-[var(--text)]">
            <Layers size={16} className="text-[var(--brand-teal)]" />
            {ctx.name || 'Untitled'}
          </h3>
          {ctx.description && <p className="mt-1 text-sm text-[var(--text-faint)]">{ctx.description}</p>}
        </div>
        <Button variant="danger" size="sm" onClick={doDelete} disabled={removing} aria-label="Delete context">
          <Trash2 size={14} />
        </Button>
      </div>

      <div>
        <p className="mb-1.5 text-[10px] font-mono uppercase tracking-[0.14em] text-[var(--text-faint)]">Repos</p>
        <RepoAttacher ctx={ctx} repos={repos} onChanged={onChanged} />
      </div>
      <div>
        <p className="mb-1.5 text-[10px] font-mono uppercase tracking-[0.14em] text-[var(--text-faint)]">Tags</p>
        <TagEditor ctx={ctx} onChanged={onChanged} />
      </div>
      <div>
        <p className="mb-1.5 text-[10px] font-mono uppercase tracking-[0.14em] text-[var(--text-faint)]">Notes</p>
        <NotesEditor key={ctx.updated_at} ctx={ctx} onChanged={onChanged} />
      </div>

      {ctx.pr_refs?.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {ctx.pr_refs.map((p, i) => <Badge key={i} color="indigo">{p.repo_slug}#{p.number}</Badge>)}
        </div>
      )}
    </Card>
  )
}

export default function Contexts() {
  const { data, loading, error, reload } = useAsync(loadAll, [])

  return (
    <div>
      <PageHeader
        title="Contexts"
        subtitle="Saved working sets of repos, PRs, tags and notes — the unit gitstate shares peer-to-peer over CRDT. Everything here lives in your local store."
      />

      <CreateForm onCreated={reload} />

      {loading && <Spinner />}
      {error && <ErrorState error={error} onRetry={reload} />}
      {!loading && !error && (
        data?.contexts?.length ? (
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            {data.contexts.map((c) => (
              <ContextCard key={c.id} ctx={c} repos={data.repos} onChanged={reload} />
            ))}
          </div>
        ) : (
          <EmptyState
            icon={<Layers size={22} />}
            title="No contexts yet"
            description="Create a context to group the repos and PRs you're focused on. Later, sync it to another machine peer-to-peer."
          />
        )
      )}
    </div>
  )
}
