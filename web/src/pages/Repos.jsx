import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { FolderGit2, Plus, RefreshCw, Trash2, ArrowRight, HardDrive, Cloud } from 'lucide-react'
import { Card } from '../components/ui/Card.jsx'
import { Button } from '../components/ui/Button.jsx'
import { Badge } from '../components/ui/Badge.jsx'
import { PageHeader, Spinner, ErrorState, EmptyState } from '../components/common.jsx'
import { useAsync, useAction } from '../lib/hooks.js'
import { listRepos, addRepo, scanRepo, deleteRepo } from '../lib/api.js'

function ForgeBadge({ forge }) {
  const map = {
    github: { icon: <Cloud size={12} />, label: 'GitHub' },
    gitlab: { icon: <Cloud size={12} />, label: 'GitLab' },
    local: { icon: <HardDrive size={12} />, label: 'Local' },
  }
  const f = map[forge] ?? map.local
  return <Badge color="default">{f.icon}{f.label}</Badge>
}

function AddRepoForm({ onAdded }) {
  const [mode, setMode] = useState('path') // 'path' | 'remote'
  const [value, setValue] = useState('')
  const [add, { pending, error }] = useAction(addRepo)

  async function submit(e) {
    e.preventDefault()
    const v = value.trim()
    if (!v) return
    const body = mode === 'path' ? { path: v } : { remote_url: v }
    await add(body)
    setValue('')
    onAdded?.()
  }

  return (
    <Card padding="md" className="mb-6">
      <form onSubmit={submit} className="flex flex-col gap-3">
        <div className="flex items-center gap-1 rounded-[var(--radius-btn)] bg-[var(--bg-surface2)] p-1 w-fit">
          {['path', 'remote'].map((m) => (
            <button
              key={m}
              type="button"
              onClick={() => setMode(m)}
              className={[
                'px-3 py-1 rounded-[6px] text-xs font-medium transition-colors',
                mode === m
                  ? 'bg-[var(--bg-surface)] text-[var(--text)] shadow-sm'
                  : 'text-[var(--text-faint)] hover:text-[var(--text)]',
              ].join(' ')}
            >
              {m === 'path' ? 'Local path' : 'Remote URL'}
            </button>
          ))}
        </div>
        <div className="flex flex-col gap-2 sm:flex-row">
          <input
            value={value}
            onChange={(e) => setValue(e.target.value)}
            placeholder={mode === 'path' ? '/Users/you/code/my-project' : 'https://github.com/owner/name'}
            className="flex-1 rounded-[var(--radius-btn)] border border-[var(--border2)] bg-[var(--bg)] px-3 py-2 text-sm text-[var(--text)] placeholder:text-[var(--text-faint)] focus:border-[var(--brand-teal)] focus:outline-none font-mono"
          />
          <Button type="submit" disabled={pending || !value.trim()} leftIcon={<Plus size={15} />}>
            {pending ? 'Adding…' : 'Add repo'}
          </Button>
        </div>
        {error && <p className="text-xs text-[var(--bad)]">{error.message}</p>}
        <p className="text-xs text-[var(--text-faint)]">
          A local path opens the worktree directly; a remote URL is resolved to a forge slug via your{' '}
          <code className="font-mono">gh</code> / <code className="font-mono">glab</code> CLI.
        </p>
      </form>
    </Card>
  )
}

function RepoRow({ repo, onChanged }) {
  const navigate = useNavigate()
  const [scan, { pending: scanning, error: scanErr }] = useAction(scanRepo)
  const [remove, { pending: removing }] = useAction(deleteRepo)

  async function doScan() {
    await scan(repo.id, { with_forge: repo.forge !== 'local' })
    onChanged?.()
  }
  async function doDelete() {
    if (!window.confirm(`Remove ${repo.slug}? Derived data is cleared; your git repo is untouched.`)) return
    await remove(repo.id)
    onChanged?.()
  }

  return (
    <Card padding="md" hoverable className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <button
        type="button"
        onClick={() => navigate(`/repos/${repo.id}`)}
        className="flex min-w-0 items-center gap-3 text-left"
      >
        <span className="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-[var(--bg-surface2)] text-[var(--brand-teal)]">
          <FolderGit2 size={17} />
        </span>
        <span className="min-w-0">
          <span className="flex items-center gap-2">
            <span className="truncate font-semibold text-[var(--text)]">{repo.slug}</span>
            <ForgeBadge forge={repo.forge} />
          </span>
          <span className="mt-0.5 block truncate font-mono text-xs text-[var(--text-faint)]">
            {repo.path}
          </span>
        </span>
      </button>

      <div className="flex shrink-0 flex-wrap items-center gap-2">
        <span className="hidden text-xs text-[var(--text-faint)] sm:inline">
          {repo.last_scanned_at ? `scanned ${new Date(repo.last_scanned_at).toLocaleDateString()}` : 'never scanned'}
        </span>
        <Button variant="outline" size="sm" onClick={doScan} disabled={scanning} leftIcon={<RefreshCw size={14} className={scanning ? 'animate-spin' : ''} />}>
          {scanning ? 'Scanning…' : 'Scan'}
        </Button>
        <Button variant="ghost" size="sm" onClick={() => navigate(`/repos/${repo.id}`)} rightIcon={<ArrowRight size={14} />}>
          Open
        </Button>
        <Button variant="danger" size="sm" onClick={doDelete} disabled={removing} aria-label="Remove repo">
          <Trash2 size={14} />
        </Button>
      </div>
      {scanErr && <p className="w-full text-xs text-[var(--bad)]">{scanErr.message}</p>}
    </Card>
  )
}

export default function Repos() {
  const { data: repos, loading, error, reload } = useAsync(listRepos, [])

  return (
    <div>
      <PageHeader
        title="Repos"
        subtitle="Register git worktrees and forge remotes. gitstate derives project state, effort and contribution locally — nothing leaves your machine."
      />

      <AddRepoForm onAdded={reload} />

      {loading && <Spinner />}
      {error && <ErrorState error={error} onRetry={reload} />}
      {!loading && !error && (
        repos?.length ? (
          <div className="flex flex-col gap-3">
            {repos.map((r) => <RepoRow key={r.id} repo={r} onChanged={reload} />)}
          </div>
        ) : (
          <EmptyState
            icon={<FolderGit2 size={22} />}
            title="No repos yet"
            description="Add a local path or a GitHub / GitLab remote above to start deriving state."
          />
        )
      )}
    </div>
  )
}
