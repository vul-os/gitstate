/**
 * Repos page — connect repos + list + trigger sync.
 */
import { useState, useCallback } from 'react'
import { useRepos } from '../lib/useRepos.js'

const PLATFORMS = [
  { id: 'github', label: 'GitHub' },
  { id: 'gitlab', label: 'GitLab' },
]

function ConnectForm({ onConnect, onClose }) {
  const [platform, setPlatform] = useState('github')
  const [fullName, setFullName] = useState('')
  const [token, setToken] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState(null)

  const handleSubmit = useCallback(async (e) => {
    e.preventDefault()
    if (!fullName.trim()) return
    setSaving(true)
    setError(null)
    try {
      await onConnect({ platform, fullName: fullName.trim(), token: token.trim() || undefined })
      onClose()
    } catch (err) {
      setError(err.message ?? 'Failed to connect repo')
    } finally {
      setSaving(false)
    }
  }, [platform, fullName, token, onConnect, onClose])

  return (
    <div
      className="mb-8 rounded-xl p-6"
      style={{ background: '#111827', border: '1px solid #1e2d45' }}
    >
      <div className="flex items-center justify-between mb-5">
        <div>
          <h3 className="text-sm font-semibold text-[#e2e8f0]">Connect a repository</h3>
          <p className="text-xs text-[#64748b] mt-0.5">
            gitstate will read commits, PRs, and issues to derive project state.
          </p>
        </div>
        <button onClick={onClose} className="text-[#64748b] hover:text-[#e2e8f0] transition-colors">
          <svg width="18" height="18" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
            <path strokeLinecap="round" strokeLinejoin="round" d="M6 18 18 6M6 6l12 12" />
          </svg>
        </button>
      </div>

      <form onSubmit={handleSubmit} className="space-y-4">
        {/* Platform */}
        <div>
          <label className="block text-xs font-semibold text-[#64748b] uppercase tracking-widest mb-2">
            Platform
          </label>
          <div className="flex gap-2">
            {PLATFORMS.map(p => (
              <button
                key={p.id}
                type="button"
                onClick={() => setPlatform(p.id)}
                className="flex items-center gap-2 px-4 py-2 rounded-lg text-sm font-medium transition-all duration-150"
                style={{
                  background: platform === p.id ? 'rgba(45,212,191,0.12)' : '#0d1628',
                  border: platform === p.id ? '1px solid rgba(45,212,191,0.4)' : '1px solid #1e2d45',
                  color: platform === p.id ? '#2DD4BF' : '#94a3b8',
                }}
              >
                <svg width="14" height="14" fill="currentColor" viewBox="0 0 24 24">
                  {p.id === 'github' ? (
                    <path d="M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12" />
                  ) : (
                    <path d="M4.845.904C3.891.904 3.034 1.317 2.316 2.2.918 3.838.904 5.84.904 5.908v12.184c0 .069.014 2.07 1.412 3.707.718.882 1.576 1.297 2.529 1.297h16.31c.945 0 1.804-.41 2.524-1.29C25.082 20.166 25.096 18.16 25.096 18.092V5.908c0-.069-.014-2.07-1.412-3.708C22.966 1.317 22.109.904 21.155.904H4.845zm10.31 16.334h-4.31v-7.65h4.31v7.65z" />
                  )}
                </svg>
                {p.label}
              </button>
            ))}
          </div>
        </div>

        {/* Repo name */}
        <div>
          <label className="block text-xs font-semibold text-[#64748b] uppercase tracking-widest mb-1.5">
            Repository <span className="text-[#ef4444]">*</span>
          </label>
          <input
            autoFocus
            required
            type="text"
            placeholder="owner/repo-name"
            className="w-full bg-[#0d1628] text-[#e2e8f0] text-sm rounded-lg px-3 py-2.5 border border-[#1e2d45] outline-none focus:border-[#2DD4BF]/50 placeholder-[#334155] font-mono transition-colors"
            value={fullName}
            onChange={e => setFullName(e.target.value)}
          />
          <p className="text-[10px] text-[#64748b] mt-1 font-mono">e.g. exo/gitstate</p>
        </div>

        {/* Token */}
        <div>
          <label className="block text-xs font-semibold text-[#64748b] uppercase tracking-widest mb-1.5">
            Access token <span className="text-[#64748b] font-normal normal-case">(optional for public repos)</span>
          </label>
          <input
            type="password"
            placeholder="ghp_… or glpat-…"
            className="w-full bg-[#0d1628] text-[#e2e8f0] text-sm rounded-lg px-3 py-2.5 border border-[#1e2d45] outline-none focus:border-[#2DD4BF]/50 placeholder-[#334155] font-mono transition-colors"
            value={token}
            onChange={e => setToken(e.target.value)}
          />
        </div>

        {error && (
          <p className="text-xs text-[#ef4444] bg-[#ef444410] rounded px-3 py-2">{error}</p>
        )}

        <div className="flex items-center gap-3 pt-1">
          <button
            type="submit"
            disabled={saving || !fullName.trim()}
            className="px-5 py-2 rounded-lg text-sm font-semibold text-[#0B1120] disabled:opacity-40 transition-all flex items-center gap-2"
            style={{ background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' }}
          >
            {saving && (
              <svg className="animate-spin" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <path d="M21 12a9 9 0 1 1-6.219-8.56" strokeLinecap="round" />
              </svg>
            )}
            Connect repository
          </button>
          <button
            type="button"
            onClick={onClose}
            className="px-4 py-2 rounded-lg text-sm font-medium text-[#64748b] hover:text-[#e2e8f0] transition-colors"
          >
            Cancel
          </button>
        </div>
      </form>
    </div>
  )
}

function SyncButton({ repoId, syncing, onSync }) {
  return (
    <button
      onClick={(e) => { e.stopPropagation(); onSync(repoId) }}
      disabled={syncing}
      className="flex items-center gap-1.5 text-xs font-medium text-[#64748b] hover:text-[#2DD4BF] transition-colors disabled:opacity-50"
      title="Trigger sync"
    >
      <svg
        width="13" height="13" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2"
        className={syncing ? 'animate-spin' : ''}
      >
        <path strokeLinecap="round" strokeLinejoin="round" d="M16.023 9.348h4.992v-.001M2.985 19.644v-4.992m0 0h4.992m-4.993 0 3.181 3.183a8.25 8.25 0 0 0 13.803-3.7M4.031 9.865a8.25 8.25 0 0 1 13.803-3.7l3.181 3.182m0-4.991v4.99" />
      </svg>
      {syncing ? 'Syncing…' : 'Sync'}
    </button>
  )
}

function RepoRow({ repo, onSync }) {
  const platformColor = repo.platform === 'github' ? '#2DD4BF' : '#f59e0b'

  return (
    <div className="flex items-center gap-4 px-5 py-4 border-b border-[#1e2d45] last:border-0 hover:bg-[#0d1628]/40 transition-colors">
      {/* Platform indicator */}
      <div
        className="w-2 h-2 rounded-full shrink-0"
        style={{ background: platformColor }}
        title={repo.platform}
      />

      {/* Repo name */}
      <div className="flex-1 min-w-0">
        <p className="text-sm font-semibold text-[#e2e8f0] font-mono truncate">{repo.fullName}</p>
        <div className="flex items-center gap-3 mt-0.5">
          <span className="text-xs text-[#64748b] font-mono">{repo.platform}</span>
          {repo.lastSyncedAt ? (
            <span className="text-xs text-[#64748b]">
              synced {new Date(repo.lastSyncedAt).toLocaleDateString()}
            </span>
          ) : (
            <span className="text-xs text-[#f59e0b]">never synced</span>
          )}
        </div>
      </div>

      {/* Stats */}
      {repo.issueCount != null && (
        <div className="hidden sm:flex items-center gap-1 text-xs font-mono text-[#64748b]">
          <svg width="12" height="12" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
            <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126ZM12 15.75h.007v.008H12v-.008Z" />
          </svg>
          {repo.issueCount} issues
        </div>
      )}

      {/* Sync button */}
      <SyncButton repoId={repo.id} syncing={repo.syncing} onSync={onSync} />
    </div>
  )
}

export default function Repos() {
  const { repos, loading, error, connectRepo, syncRepo } = useRepos()
  const [showForm, setShowForm] = useState(false)

  return (
    <div className="max-w-3xl">
      {/* Header */}
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-bold text-[#e2e8f0] tracking-tight">Repositories</h1>
          <p className="text-sm text-[#64748b] mt-1">
            Connected repos are the source of truth for dev work.
          </p>
        </div>
        {!showForm && (
          <button
            onClick={() => setShowForm(true)}
            className="px-4 py-2 rounded-lg text-sm font-semibold text-[#0B1120] transition-all duration-150 flex items-center gap-1.5"
            style={{ background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' }}
          >
            <svg width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2.5">
              <path strokeLinecap="round" strokeLinejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
            </svg>
            Connect repo
          </button>
        )}
      </div>

      {/* Connect form */}
      {showForm && (
        <ConnectForm
          onConnect={connectRepo}
          onClose={() => setShowForm(false)}
        />
      )}

      {/* Repo list */}
      <div className="bg-[#111827] border border-[#1e2d45] rounded-xl overflow-hidden">
        {/* List header */}
        <div className="flex items-center gap-4 px-5 py-3 border-b border-[#1e2d45] bg-[#0d1628]/50">
          <span className="text-[10px] font-semibold text-[#64748b] uppercase tracking-widest flex-1">Repository</span>
          <span className="text-[10px] font-semibold text-[#64748b] uppercase tracking-widest hidden sm:block w-24">Issues</span>
          <span className="text-[10px] font-semibold text-[#64748b] uppercase tracking-widest w-14">Sync</span>
        </div>

        {loading && (
          <div className="py-12 text-center">
            <svg className="animate-spin mx-auto" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="#2DD4BF" strokeWidth="2">
              <path d="M21 12a9 9 0 1 1-6.219-8.56" strokeLinecap="round" />
            </svg>
            <p className="text-xs text-[#64748b] mt-2">Loading repos…</p>
          </div>
        )}

        {!loading && error && (
          <div className="py-10 px-6 text-center">
            <p className="text-sm text-[#ef4444]">{error}</p>
            <p className="text-xs text-[#64748b] mt-1">Connect a repo above to get started.</p>
          </div>
        )}

        {!loading && !error && repos.length === 0 && (
          <div className="py-16 text-center px-6">
            <div
              className="w-12 h-12 rounded-xl flex items-center justify-center mx-auto mb-4"
              style={{ background: 'rgba(45,212,191,0.08)', border: '1px solid rgba(45,212,191,0.2)' }}
            >
              <svg width="22" height="22" fill="none" viewBox="0 0 24 24" stroke="#2DD4BF" strokeWidth="1.5">
                <path strokeLinecap="round" strokeLinejoin="round" d="M17.25 6.75 22.5 12l-5.25 5.25m-10.5 0L1.5 12l5.25-5.25m7.5-3-4.5 16.5" />
              </svg>
            </div>
            <h3 className="text-sm font-semibold text-[#e2e8f0] mb-1">No repositories yet</h3>
            <p className="text-xs text-[#64748b] max-w-xs mx-auto">
              Connect a GitHub or GitLab repo and gitstate will derive project state from git — no ticket maintenance.
            </p>
            <button
              onClick={() => setShowForm(true)}
              className="mt-4 px-4 py-2 rounded-lg text-sm font-semibold text-[#0B1120]"
              style={{ background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' }}
            >
              Connect first repo
            </button>
          </div>
        )}

        {!loading && repos.map(repo => (
          <RepoRow key={repo.id} repo={repo} onSync={syncRepo} />
        ))}
      </div>

      {/* Wedge note */}
      <p className="text-xs text-[#334155] font-mono mt-4">
        Derived from git · merged = done · PR open = in progress · no manual status updates
      </p>
    </div>
  )
}
