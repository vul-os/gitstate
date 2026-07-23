import { useState } from 'react'
import { Tags, Plus, Trash2 } from 'lucide-react'
import { Card } from '../components/ui/Card.jsx'
import { Button } from '../components/ui/Button.jsx'
import { Badge } from '../components/ui/Badge.jsx'
import { PageHeader, Spinner, ErrorState, EmptyState } from '../components/common.jsx'
import { useAsync, useAction } from '../lib/hooks.js'
import { listCategories, createCategory, deleteCategory } from '../lib/api.js'

const SOURCE_COLOR = { taxonomy: 'indigo', local: 'teal', peer: 'blue' }

function AddForm({ onAdded }) {
  const [key, setKey] = useState('')
  const [label, setLabel] = useState('')
  const [parent, setParent] = useState('')
  const [color, setColor] = useState('#2DD4BF')
  const [create, { pending, error }] = useAction(createCategory)

  async function submit(e) {
    e.preventDefault()
    if (!key.trim() || !label.trim()) return
    await create({ key: key.trim(), label: label.trim(), parent_key: parent.trim() || undefined, color })
    setKey(''); setLabel(''); setParent('')
    onAdded?.()
  }
  return (
    <Card padding="md" className="mb-6">
      <form onSubmit={submit} className="flex flex-col gap-3 sm:flex-row sm:items-end sm:flex-wrap">
        <label className="flex flex-col gap-1">
          <span className="text-[10px] font-mono uppercase tracking-[0.14em] text-[var(--text-faint)]">Key</span>
          <input value={key} onChange={(e) => setKey(e.target.value)} placeholder="feature.api"
            className="w-40 rounded-[var(--radius-btn)] border border-[var(--border2)] bg-[var(--bg)] px-3 py-2 text-sm font-mono text-[var(--text)] placeholder:text-[var(--text-faint)] focus:border-[var(--brand-teal)] focus:outline-none" />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-[10px] font-mono uppercase tracking-[0.14em] text-[var(--text-faint)]">Label</span>
          <input value={label} onChange={(e) => setLabel(e.target.value)} placeholder="API feature"
            className="w-44 rounded-[var(--radius-btn)] border border-[var(--border2)] bg-[var(--bg)] px-3 py-2 text-sm text-[var(--text)] placeholder:text-[var(--text-faint)] focus:border-[var(--brand-teal)] focus:outline-none" />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-[10px] font-mono uppercase tracking-[0.14em] text-[var(--text-faint)]">Parent (optional)</span>
          <input value={parent} onChange={(e) => setParent(e.target.value)} placeholder="feature"
            className="w-36 rounded-[var(--radius-btn)] border border-[var(--border2)] bg-[var(--bg)] px-3 py-2 text-sm font-mono text-[var(--text)] placeholder:text-[var(--text-faint)] focus:border-[var(--brand-teal)] focus:outline-none" />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-[10px] font-mono uppercase tracking-[0.14em] text-[var(--text-faint)]">Color</span>
          <input type="color" value={color} onChange={(e) => setColor(e.target.value)}
            className="h-9 w-12 cursor-pointer rounded-[var(--radius-btn)] border border-[var(--border2)] bg-[var(--bg)]" />
        </label>
        <Button type="submit" disabled={pending || !key.trim() || !label.trim()} leftIcon={<Plus size={15} />}>
          {pending ? 'Adding…' : 'Add local category'}
        </Button>
      </form>
      {error && <p className="mt-2 text-xs text-[var(--bad)]">{error.message}</p>}
    </Card>
  )
}

function CategoryRow({ cat, onChanged }) {
  const [remove, { pending }] = useAction(deleteCategory)
  const isLocal = cat.source === 'local'
  async function doDelete() {
    if (!window.confirm(`Delete category "${cat.key}"?`)) return
    await remove(cat.id)
    onChanged?.()
  }
  return (
    <div className="flex items-center justify-between gap-3 border-b border-[var(--border)] px-4 py-3 last:border-0">
      <div className="flex min-w-0 items-center gap-3">
        <span className="h-3 w-3 shrink-0 rounded-full" style={{ background: cat.color || 'var(--text-faint)' }} />
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="truncate font-medium text-[var(--text)]">{cat.label}</span>
            <Badge color={SOURCE_COLOR[cat.source] || 'default'}>{cat.source}</Badge>
          </div>
          <span className="font-mono text-xs text-[var(--text-faint)]">
            {cat.key}{cat.parent_key ? ` · ↳ ${cat.parent_key}` : ''}
            {cat.taxonomy_version ? ` · v${cat.taxonomy_version}` : ''}
          </span>
        </div>
      </div>
      {isLocal && (
        <Button variant="ghost" size="sm" onClick={doDelete} disabled={pending} aria-label="Delete category">
          <Trash2 size={14} />
        </Button>
      )}
    </div>
  )
}

export default function Categories() {
  const { data: cats, loading, error, reload } = useAsync(listCategories, [])

  return (
    <div>
      <PageHeader
        title="Categories"
        subtitle="The signed taxonomy gives everyone a shared vocabulary; local categories capture your own conventions. Both merge peer-to-peer via CRDT."
      />

      <AddForm onAdded={reload} />

      {loading && <Spinner />}
      {error && <ErrorState error={error} onRetry={reload} />}
      {!loading && !error && (
        cats?.length ? (
          <Card padding="none">
            {cats.map((c) => <CategoryRow key={c.id} cat={c} onChanged={reload} />)}
          </Card>
        ) : (
          <EmptyState icon={<Tags size={22} />} title="No categories" description="Load the taxonomy or add your own local category above." />
        )
      )}
    </div>
  )
}
