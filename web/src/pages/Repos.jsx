/**
 * Repos page — connected integrations + connect form + trigger sync.
 * Premium integrations surface: platform identity, sync health, derived-state legend.
 */
import { useState, useCallback, useMemo, useEffect } from 'react'
import {
  GitBranch, Plus, RefreshCw, Loader2, X, Check,
  CircleDot, GitPullRequest, AlertCircle, Clock, ArrowRight,
  Link2, Unlink, KeyRound, Download, Building2, Settings, Info, ExternalLink,
  ChevronRight, FolderGit2,
} from 'lucide-react'
import { useRepos } from '../lib/useRepos.js'
import {
  connectStartUrl, githubAppInstallUrl, fetchConnectStatus, fetchConnectRepos, disconnectPlatform, syncAllRepos, importRepos,
} from '../lib/api.js'
import { Card, Badge, Button, StatCard } from '../components/ui/index.js'
import { Reveal, RevealList } from '../components/Reveal.jsx'

/** Brand glyphs — lucide-react has no provider logos. */
function GithubMark({ size = 16, className = '', style }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" className={className} style={style} aria-hidden>
      <path d="M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12" />
    </svg>
  )
}

function GitlabMark({ size = 16, className = '', style }) {
  return (
    <svg width={size} height={size} viewBox="0 0 380 380" fill="none" className={className} style={style} aria-hidden>
      <path d="M282.8 170.3L195.5 7.7C193.3 3 189 0 184.2 0s-9.1 3-11.3 7.7L97 156.2l187.8-.6-2 14.7z" fill="#e24329" />
      <path d="M97 156.2L9.7 318.8c-2.2 4.7-.8 10.3 3.4 13.4 2 1.5 4.4 2.3 6.8 2.3 2.6 0 5.2-.9 7.2-2.7l157.1-131.9L97 156.2z" fill="#fc6d26" />
      <path d="M282.8 170.3l-98.6-.9 15.1 35.2 81.8 51.2L282.8 170.3z" fill="#e24329" />
      <path d="M280.1 319.8l-96.4-120.1-86.4 100.3 90.4 75.9c4.1 3.4 9.9 3.4 14 0l78.4-56.1z" fill="#fc6d26" />
    </svg>
  )
}

const PLATFORMS = [
  { id: 'github', label: 'GitHub', icon: GithubMark, accent: 'var(--text)' },
  { id: 'gitlab', label: 'GitLab', icon: GitlabMark, accent: '#fc6d26' },
]

function platformMeta(id) {
  return PLATFORMS.find(p => p.id === id) ?? { id, label: id, icon: GitBranch, accent: 'var(--text-muted)' }
}

function relativeTime(iso) {
  if (!iso) return null
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return null
  const diff = Date.now() - then
  const min = Math.round(diff / 60000)
  if (min < 1) return 'just now'
  if (min < 60) return `${min}m ago`
  const hr = Math.round(min / 60)
  if (hr < 24) return `${hr}h ago`
  const d = Math.round(hr / 24)
  if (d < 30) return `${d}d ago`
  return new Date(iso).toLocaleDateString()
}

function FormInput({ label, hint, children }) {
  return (
    <div>
      <label className="block text-[11px] font-semibold text-[var(--text-faint)] uppercase tracking-widest mb-1.5">
        {label}
      </label>
      {children}
      {hint && <p className="text-[10px] text-[var(--text-faint)] mt-1 font-mono">{hint}</p>}
    </div>
  )
}

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

  const inputCls = 'w-full bg-[var(--bg)] text-[var(--text)] text-sm rounded-[var(--radius-btn)] px-3 py-2.5 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/50 focus:ring-2 focus:ring-[var(--brand-teal)]/15 placeholder-[var(--text-faint)] font-mono transition-all'

  return (
    <Reveal>
      <Card padding="lg" className="mb-8 border-glow-teal">
        <div className="flex items-center justify-between mb-5">
          <div>
            <h3 className="text-sm font-semibold text-[var(--text)] font-display">Connect a repository</h3>
            <p className="text-xs text-[var(--text-faint)] mt-0.5">
              gitstate reads commits, PRs, and issues to derive project state — no ticket maintenance.
            </p>
          </div>
          <button
            onClick={onClose}
            className="p-1.5 -mr-1.5 rounded-[var(--radius-badge)] text-[var(--text-faint)] hover:text-[var(--text)] hover:bg-[var(--bg-surface2)] transition-colors"
            aria-label="Close"
          >
            <X size={18} />
          </button>
        </div>

        <form onSubmit={handleSubmit} className="space-y-4">
          <FormInput label="Platform">
            <div className="grid grid-cols-2 gap-2">
              {PLATFORMS.map(p => {
                const Icon = p.icon
                const active = platform === p.id
                return (
                  <button
                    key={p.id}
                    type="button"
                    onClick={() => setPlatform(p.id)}
                    className={[
                      'flex items-center gap-2.5 px-4 py-2.5 rounded-[var(--radius-btn)] text-sm font-medium transition-all duration-150 border',
                      active
                        ? 'bg-[var(--brand-teal)]/10 border-[var(--brand-teal)]/40 text-[var(--text)] shadow-[0_0_0_1px_rgba(45,212,191,0.15)]'
                        : 'bg-[var(--bg)] border-[var(--border)] text-[var(--text-muted)] hover:border-[var(--border2)] hover:text-[var(--text)]',
                    ].join(' ')}
                  >
                    <Icon size={16} style={{ color: active ? p.accent : undefined }} />
                    {p.label}
                    {active && <Check size={14} className="ml-auto text-[var(--brand-teal)]" />}
                  </button>
                )
              })}
            </div>
          </FormInput>

          <FormInput label={<>Repository <span className="text-[var(--bad)]">*</span></>} hint="e.g. exo/gitstate">
            <input
              autoFocus required type="text" placeholder="owner/repo-name"
              className={inputCls}
              value={fullName}
              onChange={e => setFullName(e.target.value)}
            />
          </FormInput>

          <FormInput label={<>Access token <span className="text-[var(--text-faint)] font-normal normal-case">(optional for public repos)</span></>}>
            <input
              type="password" placeholder="ghp_… or glpat-…"
              className={inputCls}
              value={token}
              onChange={e => setToken(e.target.value)}
            />
          </FormInput>

          {error && (
            <p className="flex items-center gap-2 text-xs text-[var(--bad)] bg-[color-mix(in_srgb,var(--bad)_8%,transparent)] border border-[color-mix(in_srgb,var(--bad)_25%,transparent)] rounded-[var(--radius-btn)] px-3 py-2">
              <AlertCircle size={13} className="shrink-0" /> {error}
            </p>
          )}

          <div className="flex items-center gap-3 pt-1">
            <Button type="submit" disabled={saving || !fullName.trim()} leftIcon={saving ? <Loader2 size={14} className="animate-spin" /> : null}>
              Connect repository
            </Button>
            <Button type="button" variant="ghost" onClick={onClose}>Cancel</Button>
          </div>
        </form>
      </Card>
    </Reveal>
  )
}

function StatPip({ icon: Icon, value, label, color }) {
  if (value == null) return null
  return (
    <div className="flex items-center gap-1.5 text-xs font-mono text-[var(--text-muted)]" title={label}>
      <Icon size={13} style={{ color }} className="shrink-0" />
      <span className="tabular-nums text-[var(--text-dim)]">{value}</span>
    </div>
  )
}

function RepoRow({ repo, onSync }) {
  const meta = platformMeta(repo.platform)
  const Icon = meta.icon
  const synced = relativeTime(repo.lastSyncedAt)

  return (
    <div className="group flex items-center gap-4 px-5 py-4 border-b border-[var(--border)] last:border-0 hover:bg-[var(--bg-surface2)]/60 transition-colors">
      {/* Platform avatar */}
      <div
        className="w-9 h-9 rounded-[var(--radius-btn)] flex items-center justify-center shrink-0 border border-[var(--border)] bg-[var(--bg)]"
        style={{ boxShadow: `inset 0 0 0 1px ${meta.accent}22` }}
      >
        <Icon size={17} style={{ color: meta.accent }} />
      </div>

      {/* Repo identity */}
      <div className="flex-1 min-w-0">
        <p className="text-sm font-semibold text-[var(--text)] font-mono truncate">{repo.fullName}</p>
        <div className="flex items-center gap-2.5 mt-1 flex-wrap">
          <span className="text-[11px] text-[var(--text-faint)] capitalize">{meta.label}</span>
          {repo.defaultBranch && (
            <span className="flex items-center gap-1 text-[11px] text-[var(--text-faint)] font-mono">
              <GitBranch size={11} /> {repo.defaultBranch}
            </span>
          )}
          {synced ? (
            <span className="flex items-center gap-1 text-[11px] text-[var(--text-faint)]">
              <Clock size={11} /> synced {synced}
            </span>
          ) : (
            <Badge color="yellow">never synced</Badge>
          )}
        </div>
      </div>

      {/* Stats (rendered when the API provides them) */}
      <div className="hidden md:flex items-center gap-4 shrink-0">
        <StatPip icon={CircleDot} value={repo.issueCount} label="open issues" color="var(--brand-teal)" />
        <StatPip icon={GitPullRequest} value={repo.prCount ?? repo.openPrs} label="open PRs" color="var(--brand-indigo)" />
      </div>

      {/* Sync */}
      <button
        onClick={(e) => { e.stopPropagation(); onSync(repo.id) }}
        disabled={repo.syncing}
        className="flex items-center gap-1.5 text-xs font-medium px-2.5 py-1.5 rounded-[var(--radius-badge)] text-[var(--text-faint)] hover:text-[var(--brand-teal)] hover:bg-[var(--brand-teal)]/[0.08] transition-colors disabled:opacity-60 shrink-0"
        title="Trigger sync"
      >
        <RefreshCw size={13} className={repo.syncing ? 'animate-spin' : 'group-hover:rotate-45 transition-transform duration-300'} />
        {repo.syncing ? 'Syncing…' : 'Sync'}
      </button>
    </div>
  )
}

/**
 * ConnectSection — per-org GitHub/GitLab OAuth-app connections.
 * Authorize once; sync uses a stored encrypted token (no per-sync PAT).
 * Shows status, connect/disconnect buttons, an import picker for stored-token
 * repos, and a PAT-fallback escape hatch (the existing ConnectForm).
 */
function ConnectSection({ onImport, onImportAll, onUsePat, justConnected }) {
  const [status, setStatus] = useState(null) // [{platform, connected, login, configured}]
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [picker, setPicker] = useState(null) // platform currently picking repos for
  const [pickerRepos, setPickerRepos] = useState([])
  const [pickerLoading, setPickerLoading] = useState(false)
  const [importing, setImporting] = useState(null) // fullName being imported
  const [bulk, setBulk] = useState(null) // { owner, done, total } during a per-org import-all
  const [query, setQuery] = useState('') // filter the picker list

  const refresh = useCallback(() => {
    return fetchConnectStatus()
      .then(s => {
        setStatus(Array.isArray(s) ? s : [])
        setError(null)
        setLoading(false)
      })
      .catch(e => {
        setError(e.message ?? 'Failed to load connection status')
        setLoading(false)
      })
  }, [])

  useEffect(() => {
    let cancelled = false
    fetchConnectStatus()
      .then(s => { if (!cancelled) { setStatus(Array.isArray(s) ? s : []); setLoading(false) } })
      .catch(e => { if (!cancelled) { setError(e.message ?? 'Failed to load connection status'); setLoading(false) } })
    return () => { cancelled = true }
  }, [])

  // Just returned from a GitHub App install/callback (?connected=<platform>):
  // refresh the connection status and auto-open the import picker so the newly
  // granted repos are visible without a manual click. Self-contained (does not
  // call the memoized openPicker) so it runs exactly once on return.
  useEffect(() => {
    if (!justConnected) return
    let cancelled = false
    Promise.resolve().then(() => {
      if (cancelled) return
      setPicker(justConnected)
      setPickerLoading(true)
      setPickerRepos([])
    })
    Promise.all([fetchConnectStatus(), fetchConnectRepos(justConnected)])
      .then(([s, repos]) => {
        if (cancelled) return
        setStatus(Array.isArray(s) ? s : [])
        setPickerRepos(Array.isArray(repos) ? repos : [])
      })
      .catch(() => {})
      .finally(() => { if (!cancelled) setPickerLoading(false) })
    return () => { cancelled = true }
  }, [justConnected])

  const handleConnect = useCallback((s) => {
    const platform = typeof s === 'string' ? s : s.platform
    const appEnabled = typeof s === 'object' && s.appEnabled
    // GitHub App is the production-grade data path: when the server advertises it
    // (status.appEnabled), "Connect" goes to the App install URL. Otherwise fall
    // back to the OAuth connect/start flow.
    const url = (platform === 'github' && appEnabled) ? githubAppInstallUrl() : connectStartUrl(platform)
    if (!url) {
      setError('Sign in and select an org before connecting.')
      return
    }
    window.location.href = url // top-level nav → provider consent/install → callback → /repos
  }, [])

  const handleDisconnect = useCallback(async (platform) => {
    try {
      await disconnectPlatform(platform)
      if (picker === platform) { setPicker(null); setPickerRepos([]) }
      await refresh()
    } catch (e) {
      setError(e.message ?? 'Failed to disconnect')
    }
  }, [picker, refresh])

  const openPicker = useCallback(async (platform) => {
    if (picker === platform) { setPicker(null); return }
    setPicker(platform)
    setPickerLoading(true)
    setPickerRepos([])
    try {
      const repos = await fetchConnectRepos(platform)
      setPickerRepos(Array.isArray(repos) ? repos : [])
    } catch (e) {
      setError(e.message ?? 'Failed to list repos for import')
    } finally {
      setPickerLoading(false)
    }
  }, [picker])

  const importRepo = useCallback(async (platform, fullName) => {
    setImporting(fullName)
    try {
      // No token in body → server uses the stored connection token.
      await onImport({ platform, fullName })
    } catch (e) {
      setError(e.message ?? 'Failed to import repo')
    } finally {
      setImporting(null)
    }
  }, [onImport])

  // Import a whole owner (org/user) as ONE backend job — it imports + syncs every
  // repo server-side, so it keeps running even if you close the tab. We just queue
  // it and let the parent poll the repo list; rows appear as they import.
  const importGroup = useCallback(async (platform, owner, repos) => {
    setBulk({ owner, queued: false, total: repos.length })
    try {
      await onImportAll(platform, repos.map((r) => r.fullName))
      setBulk({ owner, queued: true, total: repos.length })
      setTimeout(() => setBulk(null), 6000)
    } catch (e) {
      setError(e.message ?? 'Import failed')
      setBulk(null)
    }
  }, [onImportAll])

  return (
    <Reveal delay={0.04}>
      <Card padding="lg" className="mb-6">
        <div className="flex items-center justify-between mb-4">
          <div>
            <h3 className="text-sm font-semibold text-[var(--text)] font-display flex items-center gap-2">
              <Link2 size={15} className="text-[var(--brand-teal)]" /> Connect a platform
            </h3>
            <p className="text-xs text-[var(--text-faint)] mt-0.5">
              Authorize once — gitstate stores an encrypted token and syncs without re-entering a PAT.
            </p>
          </div>
          <button
            onClick={onUsePat}
            className="flex items-center gap-1.5 text-[11px] font-medium text-[var(--text-faint)] hover:text-[var(--text)] transition-colors"
            title="Use a personal access token instead"
          >
            <KeyRound size={13} /> Use a token instead
          </button>
        </div>

        {error && (
          <p className="flex items-center gap-2 text-xs text-[var(--bad)] bg-[color-mix(in_srgb,var(--bad)_8%,transparent)] border border-[color-mix(in_srgb,var(--bad)_25%,transparent)] rounded-[var(--radius-btn)] px-3 py-2 mb-3">
            <AlertCircle size={13} className="shrink-0" /> {error}
          </p>
        )}

        <div className="space-y-2.5">
          {loading && !status && (
            <div className="h-14 rounded-[var(--radius-btn)] bg-[var(--bg-surface3)] animate-pulse" />
          )}
          {(status ?? []).map(s => {
            const meta = platformMeta(s.platform)
            const Icon = meta.icon
            const isPicking = picker === s.platform
            return (
              <div key={s.platform} className="rounded-[var(--radius-btn)] border border-[var(--border)] overflow-hidden">
                <div className="flex items-center gap-3 px-4 py-3 bg-[var(--bg)]">
                  <div
                    className="w-8 h-8 rounded-[var(--radius-badge)] flex items-center justify-center shrink-0 border border-[var(--border)]"
                    style={{ boxShadow: `inset 0 0 0 1px ${meta.accent}22` }}
                  >
                    <Icon size={15} style={{ color: meta.accent }} />
                  </div>
                  <div className="flex-1 min-w-0">
                    <p className="text-sm font-semibold text-[var(--text)]">{meta.label}</p>
                    {!s.configured ? (
                      <p className="text-[11px] text-[var(--text-faint)]">OAuth app not configured on this server</p>
                    ) : s.connected ? (
                      <p className="text-[11px] text-[var(--text-faint)] flex items-center gap-1">
                        <Check size={11} className="text-[var(--brand-teal)]" />
                        Connected{s.login ? <> as <span className="font-mono text-[var(--text-dim)]">{s.login}</span></> : null}
                      </p>
                    ) : (
                      <p className="text-[11px] text-[var(--text-faint)]">Not connected</p>
                    )}
                  </div>

                  {!s.configured ? (
                    <Badge color="gray">unavailable</Badge>
                  ) : s.connected ? (
                    <div className="flex items-center gap-2 shrink-0">
                      <button
                        onClick={() => openPicker(s.platform)}
                        className="flex items-center gap-1.5 text-xs font-medium px-2.5 py-1.5 rounded-[var(--radius-badge)] text-[var(--text-faint)] hover:text-[var(--brand-teal)] hover:bg-[var(--brand-teal)]/[0.08] transition-colors"
                      >
                        <Download size={13} /> {isPicking ? 'Hide' : 'Import repos'}
                      </button>
                      {/* GitHub App: always offer installing on ANOTHER org / changing the
                          repo selection, even while connected — it links to the App install
                          page (302 → github.com/apps/<slug>/installations/new). */}
                      {s.appEnabled && (
                        <button
                          onClick={() => handleConnect(s)}
                          className="flex items-center gap-1.5 text-xs font-medium px-2.5 py-1.5 rounded-[var(--radius-badge)] text-[var(--text-faint)] hover:text-[var(--brand-teal)] hover:bg-[var(--brand-teal)]/[0.08] transition-colors"
                          title="Install on another org or change which repos gitstate can see"
                        >
                          <Settings size={13} /> Install on another org / manage
                        </button>
                      )}
                      <button
                        onClick={() => handleDisconnect(s.platform)}
                        className="flex items-center gap-1.5 text-xs font-medium px-2.5 py-1.5 rounded-[var(--radius-badge)] text-[var(--text-faint)] hover:text-[var(--bad)] hover:bg-[color-mix(in_srgb,var(--bad)_8%,transparent)] transition-colors"
                      >
                        <Unlink size={13} /> Disconnect
                      </button>
                    </div>
                  ) : (
                    <Button variant="outline" onClick={() => handleConnect(s)} leftIcon={<Link2 size={13} />}>
                      Connect {meta.label}
                    </Button>
                  )}
                </div>

                {/* Honest single-connection note: gitstate stores ONE connection per org,
                    so installing the App on a second GitHub account/org REPLACES the
                    current one. Surfaced only for a connected GitHub App. */}
                {s.connected && s.appEnabled && (
                  <div className="flex items-start gap-2 px-4 py-2.5 border-t border-[var(--border)] bg-[var(--bg-surface2)]/40 text-[11px] text-[var(--text-faint)]">
                    <Info size={12} className="text-[var(--text-faint)] shrink-0 mt-0.5" />
                    <span>
                      gitstate stores one GitHub connection per org. Use{' '}
                      <span className="text-[var(--text-dim)] font-medium">Install on another org / manage</span>{' '}
                      to add repos or switch the App to a different account — connecting a
                      new GitHub account <span className="text-[var(--text-dim)]">replaces</span> the current one.
                      {s.appSlug && (
                        <>
                          {' '}
                          <a
                            href={`https://github.com/apps/${s.appSlug}/installations/new`}
                            target="_blank"
                            rel="noreferrer"
                            className="inline-flex items-center gap-1 text-[var(--brand-teal)] hover:underline"
                          >
                            Manage on GitHub <ExternalLink size={10} />
                          </a>
                        </>
                      )}
                    </span>
                  </div>
                )}

                {/* Import picker */}
                {isPicking && (
                  <div className="border-t border-[var(--border)] bg-[var(--bg-surface2)]/40">
                    {pickerLoading && (
                      <div className="px-4 py-6 flex items-center justify-center text-xs text-[var(--text-faint)]">
                        <Loader2 size={14} className="animate-spin mr-2" /> Loading repositories…
                      </div>
                    )}
                    {!pickerLoading && pickerRepos.length === 0 && (
                      <p className="px-4 py-6 text-center text-xs text-[var(--text-faint)]">No repositories available to this token.</p>
                    )}
                    {!pickerLoading && pickerRepos.length > 0 && (() => {
                      // Group by owner (org/user) so you can import a whole org at once
                      // and ignore your personal repos. GitHub lists every repo the token
                      // can see across all granted owners — grouping makes that navigable.
                      const q = query.trim().toLowerCase()
                      const filtered = q ? pickerRepos.filter(r => r.fullName.toLowerCase().includes(q)) : pickerRepos
                      const groups = {}
                      for (const r of filtered) {
                        const owner = r.fullName.includes('/') ? r.fullName.split('/')[0] : '(personal)'
                        ;(groups[owner] ||= []).push(r)
                      }
                      const owners = Object.keys(groups).sort((a, b) => a.localeCompare(b))
                      return (
                        <>
                          <div className="px-4 py-2 bg-[var(--bg-surface2)] border-b border-[var(--border)]">
                            <input
                              value={query}
                              onChange={(e) => setQuery(e.target.value)}
                              placeholder={`Filter ${pickerRepos.length} repositories by name or org…`}
                              className="w-full px-3 py-1.5 rounded-[var(--radius-btn)] bg-[var(--bg)] border border-[var(--border)] text-xs text-[var(--text)] placeholder-[var(--text-faint)] outline-none focus:border-[var(--brand-teal)]"
                            />
                          </div>
                          <div className="max-h-72 overflow-auto">
                            {owners.map(owner => {
                              const list = groups[owner]
                              const busy = bulk?.owner === owner
                              return (
                                <div key={owner}>
                                  <div className="flex items-center gap-2 px-4 py-2 bg-[var(--bg-surface2)]/70 border-b border-[var(--border)]">
                                    <Building2 size={12} className="text-[var(--text-faint)] shrink-0" />
                                    <span className="text-[11px] font-semibold text-[var(--text-muted)] truncate">{owner}</span>
                                    <span className="text-[10px] font-mono text-[var(--text-faint)]">{list.length}</span>
                                    <button
                                      onClick={() => importGroup(s.platform, owner, list)}
                                      disabled={!!bulk}
                                      className="ml-auto flex items-center gap-1.5 text-[11px] font-semibold px-2 py-1 rounded-[var(--radius-badge)] text-[var(--brand-teal)] hover:bg-[var(--brand-teal)]/[0.1] transition-colors disabled:opacity-60 shrink-0"
                                    >
                                      {busy
                                        ? (bulk.queued
                                            ? <><Check size={12} strokeWidth={2.5} /> Queued — importing</>
                                            : <><Loader2 size={12} className="animate-spin" /> Queuing…</>)
                                        : <><Plus size={12} strokeWidth={2.5} /> Import all</>}
                                    </button>
                                  </div>
                                  {list.map(rp => (
                                    <div key={rp.externalId || rp.fullName} className="flex items-center gap-3 px-4 py-2 pl-8 border-b border-[var(--border)] last:border-0">
                                      <GitBranch size={13} className="text-[var(--text-faint)] shrink-0" />
                                      <span className="flex-1 min-w-0 text-xs font-mono text-[var(--text-dim)] truncate">{rp.fullName}</span>
                                      <button
                                        onClick={() => importRepo(s.platform, rp.fullName)}
                                        disabled={importing === rp.fullName || !!bulk}
                                        className="flex items-center gap-1.5 text-[11px] font-medium px-2 py-1 rounded-[var(--radius-badge)] text-[var(--brand-teal)] hover:bg-[var(--brand-teal)]/[0.1] transition-colors disabled:opacity-60 shrink-0"
                                      >
                                        {importing === rp.fullName ? <Loader2 size={12} className="animate-spin" /> : <Plus size={12} strokeWidth={2.5} />}
                                        Import
                                      </button>
                                    </div>
                                  ))}
                                </div>
                              )
                            })}
                          </div>
                        </>
                      )
                    })()}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      </Card>
    </Reveal>
  )
}

export default function Repos() {
  const { repos, loading, error, connectRepo, syncRepo, refetch } = useRepos()
  const [syncingAll, setSyncingAll] = useState(false)
  // Search within the grouped repo list, and per-project collapse state.
  const [query, setQuery] = useState('')
  const [openGroups, setOpenGroups] = useState({}) // { [owner]: bool }
  const toggleGroup = useCallback((owner) => {
    setOpenGroups(prev => ({ ...prev, [owner]: !(prev[owner] ?? true) }))
  }, [])

  const handleSyncAll = useCallback(async () => {
    setSyncingAll(true)
    try {
      await syncAllRepos()
      // The sync runs sequentially in the background (clone + git-analysis per repo);
      // poll a few times so the synced count + activity update as it progresses.
      let ticks = 0
      const id = setInterval(async () => {
        ticks += 1
        await refetch()
        if (ticks >= 20) { clearInterval(id); setSyncingAll(false) }
      }, 6000)
    } catch {
      setSyncingAll(false)
    }
  }, [refetch])
  const [showForm, setShowForm] = useState(false)

  // Surface the ?connected=<platform> / ?error= redirect outcome from the OAuth
  // callback. Computed once from the URL via a lazy initializer (not an effect).
  const [banner, setBanner] = useState(() => {
    const params = new URLSearchParams(window.location.search)
    const connected = params.get('connected')
    const err = params.get('error')
    if (connected) return { kind: 'ok', text: `Connected ${connected}. The newly-granted repos are listed below — pick repos to import.` }
    if (err) return { kind: 'err', text: `Connection failed: ${err}` }
    return null
  })
  // The platform we just returned from connecting (drives a status refresh +
  // auto-opening the import picker in ConnectSection). Read once from the URL.
  const [justConnected] = useState(() => new URLSearchParams(window.location.search).get('connected'))

  // Clean the query string so a refresh doesn't re-show the banner (no setState).
  useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    if (params.get('connected') || params.get('error')) {
      window.history.replaceState({}, '', window.location.pathname)
    }
  }, [])

  // Bulk "Import all" → a single backend job (survives the browser closing).
  // Returns immediately; we poll the repo list so rows appear as they import+sync.
  const importAll = useCallback(async (platform, fullNames) => {
    await importRepos(platform, fullNames)
    let ticks = 0
    const id = setInterval(async () => {
      ticks += 1
      await refetch()
      if (ticks >= 40) clearInterval(id) // ~4 min of polling, then stop
    }, 6000)
  }, [refetch])

  const importRepo = useCallback(async ({ platform, fullName }) => {
    await connectRepo({ platform, fullName }) // no token → server uses stored connection token
    refetch?.().catch(() => {})
  }, [connectRepo, refetch])

  // A "project" = the repo's owner-org (the part before "/" in full_name). Group
  // the connected repos by that owner — same derivation as the import picker — so
  // each project is a collapsible header with its repos nested + counted.
  const repoGroups = useMemo(() => {
    const q = query.trim().toLowerCase()
    const filtered = q
      ? repos.filter(r => (r.fullName || '').toLowerCase().includes(q))
      : repos
    const groups = {}
    for (const r of filtered) {
      const owner = r.fullName?.includes('/') ? r.fullName.split('/')[0] : '(personal)'
      ;(groups[owner] ||= []).push(r)
    }
    for (const owner of Object.keys(groups)) {
      groups[owner].sort((a, b) => (a.fullName || '').localeCompare(b.fullName || ''))
    }
    return Object.keys(groups)
      .sort((a, b) => a.localeCompare(b))
      .map(owner => ({ owner, list: groups[owner] }))
  }, [repos, query])

  const stats = useMemo(() => {
    const total = repos.length
    const github = repos.filter(r => r.platform === 'github').length
    const gitlab = repos.filter(r => r.platform === 'gitlab').length
    const synced = repos.filter(r => r.lastSyncedAt).length
    const issues = repos.reduce((sum, r) => sum + (Number(r.issueCount) || 0), 0)
    const hasIssueData = repos.some(r => r.issueCount != null)
    // A "project" = a distinct owner-org across the connected repos.
    const projects = new Set(
      repos.map(r => (r.fullName?.includes('/') ? r.fullName.split('/')[0] : '(personal)'))
    ).size
    return { total, github, gitlab, synced, issues, hasIssueData, projects }
  }, [repos])

  return (
    <div className="w-full">
      {/* Header */}
      <Reveal>
        <div className="flex items-start justify-between mb-6 gap-4">
          <div className="flex items-start gap-3">
            <span className="mt-0.5 grid place-items-center w-9 h-9 rounded-[var(--radius-btn)] bg-[var(--brand-teal)]/10 border border-[var(--brand-teal)]/20 shrink-0">
              <GitBranch size={17} className="text-[var(--brand-teal)]" />
            </span>
            <div>
              <h1 className="font-display text-2xl font-semibold text-[var(--text)] tracking-tight">Projects</h1>
              <p className="text-sm text-[var(--text-faint)] mt-1">
                Repositories grouped by project — your connected repos are the source of truth for dev work.
              </p>
            </div>
          </div>
          {!showForm && (
            <Button variant="primary" onClick={() => setShowForm(true)} leftIcon={<Plus size={15} strokeWidth={2.5} />}>
              Connect repo
            </Button>
          )}
        </div>
      </Reveal>

      {/* Redirect outcome banner */}
      {banner && (
        <Reveal>
          <div className={[
            'flex items-center gap-2 text-xs rounded-[var(--radius-btn)] px-3 py-2.5 mb-4 border',
            banner.kind === 'ok'
              ? 'text-[var(--brand-teal)] bg-[var(--brand-teal)]/[0.08] border-[var(--brand-teal)]/25'
              : 'text-[var(--bad)] bg-[color-mix(in_srgb,var(--bad)_8%,transparent)] border-[color-mix(in_srgb,var(--bad)_25%,transparent)]',
          ].join(' ')}>
            {banner.kind === 'ok' ? <Check size={14} className="shrink-0" /> : <AlertCircle size={14} className="shrink-0" />}
            {banner.text}
            <button onClick={() => setBanner(null)} className="ml-auto opacity-60 hover:opacity-100" aria-label="Dismiss">
              <X size={14} />
            </button>
          </div>
        </Reveal>
      )}

      {/* Platform connections (OAuth-app) */}
      <ConnectSection onImport={importRepo} onImportAll={importAll} onUsePat={() => setShowForm(true)} justConnected={justConnected} />

      {/* Summary strip */}
      {!loading && repos.length > 0 && (
        <Reveal delay={0.05}>
          <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-6">
            <StatCard
              label="Projects"
              value={stats.projects.toLocaleString()}
              sublabel={stats.projects === 1 ? 'owner-org' : 'owner-orgs'}
              accent="var(--chart-2)"
              icon={<FolderGit2 size={14} />}
            />
            <StatCard
              label="Connected"
              value={stats.total.toLocaleString()}
              sublabel="repositories tracked"
              accent="var(--chart-1)"
              icon={<GitBranch size={14} />}
            />
            <StatCard
              label="Synced"
              value={`${stats.synced}/${stats.total}`}
              sublabel={stats.synced === stats.total ? 'all repos up to date' : `${stats.total - stats.synced} awaiting first sync`}
              accent={stats.synced === stats.total ? 'var(--ok)' : 'var(--chart-3)'}
              icon={<RefreshCw size={14} />}
            />
            {stats.hasIssueData ? (
              <StatCard
                label="Open issues"
                value={stats.issues.toLocaleString()}
                sublabel="tracked across repos"
                accent="var(--chart-6)"
                icon={<CircleDot size={14} />}
              />
            ) : (
              <StatCard
                label="Platforms"
                value={(stats.github > 0 ? 1 : 0) + (stats.gitlab > 0 ? 1 : 0) || '—'}
                sublabel={[
                  stats.github > 0 ? `${stats.github} GitHub` : null,
                  stats.gitlab > 0 ? `${stats.gitlab} GitLab` : null,
                ].filter(Boolean).join(' · ') || 'none connected'}
                accent="var(--chart-2)"
                icon={<Link2 size={14} />}
              />
            )}
          </div>
        </Reveal>
      )}

      {/* Connect form */}
      {showForm && (
        <ConnectForm onConnect={connectRepo} onClose={() => setShowForm(false)} />
      )}

      {/* Sync-all toolbar */}
      {!loading && !error && repos.length > 0 && (
        <div className="flex items-center justify-between mb-3">
          <p className="text-xs text-[var(--text-faint)]">
            {syncingAll
              ? 'Cloning + analyzing each repo in the background — Contribution & Analytics fill in as they finish.'
              : 'Sync pulls issues/PRs and runs git-history analysis (commits, contribution, cycle time).'}
          </p>
          <Button variant="outline" size="sm" onClick={handleSyncAll} disabled={syncingAll}
            leftIcon={<RefreshCw size={13} className={syncingAll ? 'animate-spin' : ''} />}>
            {syncingAll ? 'Syncing all…' : 'Sync all'}
          </Button>
        </div>
      )}

      {/* Repo list — grouped by project (owner-org) */}
      <Reveal delay={0.08}>
        <Card padding="none" className="overflow-hidden">
          {!loading && !error && repos.length > 0 && (
            <div className="px-4 py-2.5 border-b border-[var(--border)] bg-[var(--bg-surface2)]/40">
              <input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder={`Search ${repos.length} repositories by name or project…`}
                className="w-full px-3 py-1.5 rounded-[var(--radius-btn)] bg-[var(--bg)] border border-[var(--border)] text-xs text-[var(--text)] placeholder-[var(--text-faint)] outline-none focus:border-[var(--brand-teal)]"
              />
            </div>
          )}
          <div className="flex items-center gap-4 px-5 py-3 border-b border-[var(--border)] bg-[var(--bg-surface2)]/40">
            <span className="text-[10px] font-semibold text-[var(--text-faint)] uppercase tracking-widest flex-1">Project / Repository</span>
            <span className="text-[10px] font-semibold text-[var(--text-faint)] uppercase tracking-widest hidden md:block">Activity</span>
            <span className="text-[10px] font-semibold text-[var(--text-faint)] uppercase tracking-widest w-14 text-right">Sync</span>
          </div>

          {loading && (
            <div className="divide-y divide-[var(--border)]">
              {Array.from({ length: 3 }).map((_, i) => (
                <div key={i} className="flex items-center gap-4 px-5 py-4 animate-pulse">
                  <div className="w-9 h-9 rounded-[var(--radius-btn)] bg-[var(--bg-surface3)]" />
                  <div className="flex-1 space-y-2">
                    <div className="h-3 w-44 rounded bg-[var(--bg-surface3)]" />
                    <div className="h-2 w-28 rounded bg-[var(--bg-surface2)]" />
                  </div>
                </div>
              ))}
            </div>
          )}

          {!loading && error && (
            <div className="py-10 px-6 text-center">
              <AlertCircle size={20} className="mx-auto text-[var(--bad)] mb-2" />
              <p className="text-sm text-[var(--bad)]">{error}</p>
              <p className="text-xs text-[var(--text-faint)] mt-1">Connect a repo above to get started.</p>
            </div>
          )}

          {!loading && !error && repos.length === 0 && (
            <div className="py-16 text-center px-6">
              <div className="w-12 h-12 rounded-[var(--radius-card)] flex items-center justify-center mx-auto mb-4 bg-[var(--brand-teal)]/[0.06] border border-[var(--brand-teal)]/20">
                <GitBranch size={22} className="text-[var(--brand-teal)]" />
              </div>
              <h3 className="text-sm font-semibold text-[var(--text)] mb-1">No repositories yet</h3>
              <p className="text-xs text-[var(--text-faint)] max-w-xs mx-auto mb-4">
                Connect a GitHub or GitLab repo and gitstate will derive project state from git — no ticket maintenance.
              </p>
              <Button variant="primary" onClick={() => setShowForm(true)} leftIcon={<Plus size={14} strokeWidth={2.5} />}>
                Connect first repo
              </Button>
            </div>
          )}

          {!loading && !error && repos.length > 0 && repoGroups.length === 0 && (
            <p className="px-5 py-10 text-center text-xs text-[var(--text-faint)]">
              No repositories match “{query}”.
            </p>
          )}

          {!loading && !error && repoGroups.length > 0 && (
            <RevealList staggerDelay={0.04}>
              {repoGroups.map(({ owner, list }) => {
                // Default-open; a project header toggles its repos. Avatar = owner's
                // first initial in the brand gradient (no org logos available client-side).
                const open = openGroups[owner] ?? true
                const synced = list.filter(r => r.lastSyncedAt).length
                return (
                  <div key={owner}>
                    <button
                      type="button"
                      onClick={() => toggleGroup(owner)}
                      aria-expanded={open}
                      className="w-full flex items-center gap-2.5 px-5 py-2.5 bg-[var(--bg-surface2)]/70 hover:bg-[var(--bg-surface2)] border-b border-[var(--border)] transition-colors text-left"
                    >
                      <ChevronRight
                        size={13}
                        className={`text-[var(--text-faint)] shrink-0 transition-transform duration-150 ${open ? 'rotate-90' : ''}`}
                      />
                      <span
                        className="w-5 h-5 rounded-[5px] flex items-center justify-center shrink-0 text-[9px] font-bold text-[#0B1120] bg-gradient-to-br from-[#2DD4BF] to-[#6366F1] select-none uppercase"
                        aria-hidden="true"
                      >
                        {owner === '(personal)' ? '@' : owner.slice(0, 2)}
                      </span>
                      <span className="text-[12px] font-semibold text-[var(--text)] truncate">{owner}</span>
                      <span className="text-[10px] font-mono text-[var(--text-faint)] rounded-full px-2 py-0.5 bg-[var(--bg)] border border-[var(--border)] tabular-nums shrink-0">
                        {list.length} {list.length === 1 ? 'repo' : 'repos'}
                      </span>
                      <span className="ml-auto text-[10px] font-mono text-[var(--text-faint)] shrink-0">
                        {synced === list.length ? 'all synced' : `${synced}/${list.length} synced`}
                      </span>
                    </button>
                    {open && list.map(repo => (
                      <RepoRow key={repo.id} repo={repo} onSync={syncRepo} />
                    ))}
                  </div>
                )
              })}
            </RevealList>
          )}
        </Card>
      </Reveal>

      {/* Derived-state legend */}
      <Reveal delay={0.12}>
        <div className="flex items-center gap-2 text-[11px] text-[var(--text-faint)] font-mono mt-4">
          <ArrowRight size={12} className="text-[var(--brand-teal)] shrink-0" />
          Derived from git · merged = done · PR open = in progress · no manual status updates
        </div>
      </Reveal>
    </div>
  )
}
