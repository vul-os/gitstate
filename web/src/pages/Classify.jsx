import { useState } from 'react'
import { Sparkles, Scale, Check } from 'lucide-react'
import { Card } from '../components/ui/Card.jsx'
import { Button } from '../components/ui/Button.jsx'
import { Badge } from '../components/ui/Badge.jsx'
import { PageHeader, Spinner, ErrorState, EmptyState } from '../components/common.jsx'
import { useAsync, useAction } from '../lib/hooks.js'
import {
  listRepos, workItems, listCategories, classify, effort, classifyFeedback,
} from '../lib/api.js'

function methodBadge(method) {
  return <Badge color={method === 'llm_judged' ? 'indigo' : 'default'}>{method === 'llm_judged' ? 'LLM' : 'heuristic'}</Badge>
}

function ResultRow({ item, result, effortRow, categories, onFeedback }) {
  const [chosen, setChosen] = useState(result?.category_key || '')
  const [feedback, { pending }] = useAction(classifyFeedback)

  async function pick(key) {
    setChosen(key)
    await feedback({ item_id: item.id, category_key: key })
    onFeedback?.()
  }

  return (
    <Card padding="md" className="flex flex-col gap-3">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="font-mono text-xs text-[var(--text-faint)]">{item.external_ref}</span>
            <Badge color="default">{item.kind}</Badge>
          </div>
          <p className="truncate text-sm text-[var(--text)]">{item.title}</p>
        </div>
        {effortRow && (
          <div className="shrink-0 text-right">
            <span className="font-display text-lg font-semibold tabular-nums text-[var(--brand-teal)]">{effortRow.difficulty}</span>
            <span className="block text-[10px] font-mono uppercase tracking-wide text-[var(--text-faint)]">difficulty</span>
          </div>
        )}
      </div>

      {result && (
        <div className="flex flex-wrap items-center gap-2 text-xs text-[var(--text-faint)]">
          <span>classified</span>
          <Badge color="teal">{result.category_key}</Badge>
          <span>· {(result.confidence * 100).toFixed(0)}% conf</span>
          {methodBadge(result.method)}
          {result.rationale && <span className="italic">— {result.rationale}</span>}
        </div>
      )}

      <div className="flex flex-wrap items-center gap-1.5">
        <span className="text-[10px] font-mono uppercase tracking-[0.14em] text-[var(--text-faint)]">Correct label:</span>
        <select
          value={chosen}
          onChange={(e) => pick(e.target.value)}
          disabled={pending}
          className="rounded-[var(--radius-btn)] border border-[var(--border2)] bg-[var(--bg)] px-2 py-1 text-xs text-[var(--text)] focus:border-[var(--brand-teal)] focus:outline-none"
        >
          <option value="">—</option>
          {categories.map((c) => <option key={c.id} value={c.key}>{c.key}</option>)}
        </select>
        {chosen && <Check size={14} className="text-[var(--ok)]" />}
      </div>
    </Card>
  )
}

export default function Classify() {
  const { data: repos, loading, error } = useAsync(listRepos, [])
  const [repoId, setRepoId] = useState('')
  const [items, setItems] = useState([])
  const [results, setResults] = useState({}) // item_id -> classification
  const [efforts, setEfforts] = useState({}) // item_id -> effort estimate
  const [categories, setCategories] = useState([])

  const [runClassify, { pending: classifying, error: clErr }] = useAction(classify)
  const [runEffort, { pending: judging, error: efErr }] = useAction(effort)

  async function selectRepo(id) {
    setRepoId(id)
    setResults({}); setEfforts({})
    if (!id) { setItems([]); return }
    const [its, cats] = await Promise.all([
      workItems(id, {}).catch(() => []),
      listCategories().catch(() => []),
    ])
    setItems(its)
    setCategories(cats)
  }

  async function doClassify() {
    const rows = await runClassify({ repo_id: repoId })
    setResults(Object.fromEntries((rows || []).map((r) => [r.item_id, r])))
  }
  async function doEffort() {
    const rows = await runEffort({ repo_id: repoId })
    setEfforts(Object.fromEntries((rows || []).map((r) => [r.item_id, r])))
  }

  if (loading) return <div><PageHeader title="Classify" /><Spinner /></div>
  if (error) return <div><PageHeader title="Classify" /><ErrorState error={error} /></div>

  return (
    <div>
      <PageHeader
        title="Classify"
        subtitle="Label work items and judge diff-difficulty locally — via your llmux / OpenAI-compatible endpoint, or a deterministic heuristic fallback. Corrections train this box only."
      />

      <Card padding="md" className="mb-6 flex flex-col gap-3 sm:flex-row sm:items-center">
        <select
          value={repoId}
          onChange={(e) => selectRepo(e.target.value)}
          className="flex-1 rounded-[var(--radius-btn)] border border-[var(--border2)] bg-[var(--bg)] px-3 py-2 text-sm text-[var(--text)] focus:border-[var(--brand-teal)] focus:outline-none"
        >
          <option value="">Select a repo…</option>
          {(repos || []).map((r) => <option key={r.id} value={r.id}>{r.slug}</option>)}
        </select>
        <Button onClick={doClassify} disabled={!repoId || classifying} leftIcon={<Sparkles size={15} />}>
          {classifying ? 'Classifying…' : 'Classify items'}
        </Button>
        <Button variant="outline" onClick={doEffort} disabled={!repoId || judging} leftIcon={<Scale size={15} />}>
          {judging ? 'Judging…' : 'Judge effort'}
        </Button>
      </Card>

      {(clErr || efErr) && <p className="mb-4 text-sm text-[var(--bad)]">{(clErr || efErr).message}</p>}

      {!repoId ? (
        <EmptyState icon={<Sparkles size={22} />} title="Pick a repo" description="Choose a repo to classify its work items and estimate effort." />
      ) : items.length ? (
        <div className="flex flex-col gap-3">
          {items.map((it) => (
            <ResultRow
              key={it.id}
              item={it}
              result={results[it.id]}
              effortRow={efforts[it.id]}
              categories={categories}
              onFeedback={() => {}}
            />
          ))}
        </div>
      ) : (
        <EmptyState title="No work items" description="Scan this repo with a connected forge first to pull PRs and issues." />
      )}
    </div>
  )
}
