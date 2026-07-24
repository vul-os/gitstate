/**
 * Import — pull Jira / Linear issues into the local ledger.
 *
 * The whole point of this screen is that it is NOT a hosted integration.
 * gitstate calls Jira's and Linear's public APIs directly from this machine
 * using the user's own personal token, exactly as it already shells out to
 * `gh`/`glab`. There is no gitstate server, no OAuth broker, and no third
 * party in the path — so the UI says so plainly, because that is the reason
 * to use it over a SaaS importer.
 *
 * The offline mode exists for the cases a token can't cover: an air-gapped
 * machine, a locked-down Jira Server/DC, or simply someone who would rather
 * not store a credential at all.
 */
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  DownloadCloud, ShieldCheck, Plug, FileText, CheckCircle2, XCircle,
  Trash2, FlaskConical, ArrowRight,
} from 'lucide-react'
import { Card } from '../components/ui/Card.jsx'
import { Button } from '../components/ui/Button.jsx'
import { Badge } from '../components/ui/Badge.jsx'
import { SegmentedControl } from '../components/ui/SegmentedControl.jsx'
import { PageHeader, Spinner, ErrorState, EmptyState } from '../components/common.jsx'
import { useAsync, useAction } from '../lib/hooks.js'
import {
  listRepos, trackers, saveTracker, deleteTracker, testTracker,
  importPreview, importRun, importFile,
} from '../lib/api.js'

const MODES = [
  { value: 'connect', label: 'Connect' },
  { value: 'file', label: 'Offline file' },
]

const TRACKERS = [
  {
    kind: 'jira',
    label: 'Jira',
    tokenLabel: 'API token',
    tokenHelp: 'id.atlassian.com → Security → Create and manage API tokens',
    projectLabel: 'Project key (optional)',
    projectHint: 'e.g. ENG — leave blank to import everything the token can see',
    needsSite: true,
  },
  {
    kind: 'linear',
    label: 'Linear',
    tokenLabel: 'Personal API key',
    tokenHelp: 'Linear → Settings → API → Personal API keys',
    projectLabel: 'Team key (optional)',
    projectHint: 'e.g. ENG — leave blank to import every team you can see',
    needsSite: false,
  },
]

async function loadAll() {
  const [repos, configured] = await Promise.all([listRepos(), trackers()])
  return { repos, trackers: configured }
}

function Field({ label, hint, children }) {
  return (
    <label className="flex flex-col gap-1.5">
      <span className="text-[11px] font-medium text-[var(--text-dim)]">{label}</span>
      {children}
      {hint && <span className="text-[11px] text-[var(--text-faint)]">{hint}</span>}
    </label>
  )
}

const inputCls =
  'w-full rounded-[var(--radius-btn)] border border-[var(--border)] bg-[var(--bg-surface2)] ' +
  'px-3 py-2 text-sm text-[var(--text)] outline-none placeholder-[var(--text-faint)] ' +
  'focus-visible:ring-2 focus-visible:ring-[var(--brand-teal)]'

/** The privacy claim, stated once and prominently — it is the product thesis. */
function PrivacyNote() {
  return (
    <div className="flex items-start gap-2.5 rounded-[var(--radius-card)] border border-[color-mix(in_srgb,var(--brand-teal)_25%,transparent)] bg-[color-mix(in_srgb,var(--brand-teal)_6%,transparent)] p-3.5">
      <span className="mt-0.5 shrink-0 text-[var(--brand-teal)]">
        <ShieldCheck size={16} />
      </span>
      <p className="text-xs leading-relaxed text-[var(--text-muted)]">
        Credentials are stored in your local database on this machine and are sent
        only to the vendor they belong to. gitstate has no server in the path —
        this app talks to Jira and Linear directly, the same way it uses your{' '}
        <code className="font-mono text-[var(--text-dim)]">gh</code> login.
      </p>
    </div>
  )
}

function StatusLine({ status }) {
  if (!status) return null
  const good = status.ok
  return (
    <div
      role="status"
      className="flex items-center gap-2 text-xs"
      style={{ color: good ? 'var(--ok)' : 'var(--bad)' }}
    >
      {good ? <CheckCircle2 size={14} /> : <XCircle size={14} />}
      <span>{status.message}</span>
    </div>
  )
}

function PreviewTable({ items }) {
  if (!items?.length) return null
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[560px] text-sm">
        <thead>
          <tr className="border-b border-[var(--border)] text-left">
            {['Key', 'Title', 'State', 'Labels'].map((h) => (
              <th
                key={h}
                className="py-2 pr-3 font-mono text-[10px] font-medium uppercase tracking-[0.12em] text-[var(--text-faint)]"
              >
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {items.slice(0, 25).map((it) => (
            <tr key={it.key} className="border-b border-[var(--border)] last:border-0">
              <td className="py-2 pr-3 font-mono text-xs text-[var(--text-faint)]">{it.key}</td>
              <td className="max-w-[320px] truncate py-2 pr-3 text-[var(--text)]">{it.title}</td>
              <td className="py-2 pr-3">
                <Badge>{it.state}</Badge>
              </td>
              <td className="py-2 pr-3">
                <span className="flex flex-wrap gap-1">
                  {(it.labels || []).slice(0, 3).map((l) => (
                    <Badge key={l} color="indigo">{l}</Badge>
                  ))}
                </span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {items.length > 25 && (
        <p className="mt-2 text-[11px] text-[var(--text-faint)]">
          Showing the first 25 of {items.length.toLocaleString()} fetched.
        </p>
      )}
    </div>
  )
}

function ConnectMode({ repos, configured, reload }) {
  const [kind, setKind] = useState('jira')
  const meta = TRACKERS.find((t) => t.kind === kind)
  const stored = configured.find((t) => t.kind === kind) || {}

  const [form, setForm] = useState({ base_url: '', email: '', token: '', project: '' })
  const [replacing, setReplacing] = useState(false)
  const [status, setStatus] = useState(null)
  const [preview, setPreview] = useState(null)
  const [result, setResult] = useState(null)
  const [repoId, setRepoId] = useState(repos[0]?.id ?? '')

  const [doSave, saveState] = useAction(saveTracker)
  const [doDelete, delState] = useAction(deleteTracker)
  const [doTest, testState] = useAction(testTracker)
  const [doPreview, previewState] = useAction(importPreview)
  const [doRun, runState] = useAction(importRun)

  // Switching tracker resets the transient panels — a Jira preview must never
  // linger under a Linear form.
  function pick(next) {
    setKind(next)
    setForm({ base_url: '', email: '', token: '', project: '' })
    setReplacing(false)
    setStatus(null)
    setPreview(null)
    setResult(null)
  }

  const set = (k) => (e) => setForm((f) => ({ ...f, [k]: e.target.value }))

  async function save() {
    // An empty token is meaningful: the daemon keeps the stored secret, so the
    // user can edit the site/project without re-pasting it.
    await doSave(kind, {
      base_url: form.base_url || stored.base_url || '',
      email: form.email || stored.email || '',
      token: form.token,
      project: form.project || stored.project || '',
    })
    setForm((f) => ({ ...f, token: '' }))
    setReplacing(false)
    reload()
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        {TRACKERS.map((t) => {
          const cfg = configured.find((c) => c.kind === t.kind)
          const active = t.kind === kind
          return (
            <button
              key={t.kind}
              type="button"
              onClick={() => pick(t.kind)}
              aria-pressed={active}
              className={[
                'flex items-center gap-2 rounded-[var(--radius-btn)] border px-3 py-2 text-sm transition-colors',
                active
                  ? 'border-[var(--brand-teal)] bg-[color-mix(in_srgb,var(--brand-teal)_10%,transparent)] text-[var(--text)]'
                  : 'border-[var(--border)] text-[var(--text-faint)] hover:text-[var(--text)]',
              ].join(' ')}
            >
              <Plug size={14} />
              {t.label}
              {cfg?.configured && <Badge color="teal">connected</Badge>}
            </button>
          )
        })}
      </div>

      <PrivacyNote />

      <Card padding="lg" className="flex flex-col gap-4">
        <div className="flex items-center justify-between gap-3">
          <h2 className="text-sm font-semibold text-[var(--text)]">{meta.label} credentials</h2>
          {stored.configured && (
            <span className="flex items-center gap-2">
              <Badge color="teal">token {stored.token}</Badge>
              <Button
                variant="ghost"
                size="xs"
                onClick={async () => {
                  await doDelete(kind)
                  setStatus(null)
                  reload()
                }}
                disabled={delState.pending}
                leftIcon={<Trash2 size={13} />}
              >
                Forget
              </Button>
            </span>
          )}
        </div>

        <div className="grid gap-4 sm:grid-cols-2">
          {meta.needsSite && (
            <>
              <Field label="Site URL" hint="https://your-site.atlassian.net">
                <input
                  className={inputCls}
                  value={form.base_url || stored.base_url || ''}
                  onChange={set('base_url')}
                  placeholder="https://your-site.atlassian.net"
                />
              </Field>
              <Field label="Account email" hint="the Atlassian account that owns the token">
                <input
                  className={inputCls}
                  type="email"
                  autoComplete="off"
                  value={form.email || stored.email || ''}
                  onChange={set('email')}
                  placeholder="you@example.com"
                />
              </Field>
            </>
          )}

          <Field label={meta.tokenLabel} hint={meta.tokenHelp}>
            {stored.configured && !replacing ? (
              <div className="flex items-center gap-2">
                <span className="font-mono text-sm text-[var(--text-faint)]">{stored.token}</span>
                <Button variant="outline" size="xs" onClick={() => setReplacing(true)}>
                  Replace
                </Button>
              </div>
            ) : (
              <input
                className={inputCls}
                type="password"
                autoComplete="off"
                spellCheck="false"
                value={form.token}
                onChange={set('token')}
                placeholder="paste your token"
              />
            )}
          </Field>

          <Field label={meta.projectLabel} hint={meta.projectHint}>
            <input
              className={inputCls}
              value={form.project || stored.project || ''}
              onChange={set('project')}
              placeholder="ENG"
            />
          </Field>
        </div>

        {saveState.error && (
          <p role="alert" className="text-xs text-[var(--bad)]">{saveState.error.message}</p>
        )}

        <div className="flex flex-wrap items-center gap-2">
          <Button onClick={save} disabled={saveState.pending}>
            {saveState.pending ? 'Saving…' : 'Save credentials'}
          </Button>
          <Button
            variant="outline"
            onClick={async () => setStatus(await doTest(kind))}
            disabled={testState.pending || !stored.configured}
            leftIcon={<FlaskConical size={14} />}
          >
            {testState.pending ? 'Testing…' : 'Test connection'}
          </Button>
          <StatusLine status={status} />
          {testState.error && (
            <span role="alert" className="text-xs text-[var(--bad)]">{testState.error.message}</span>
          )}
        </div>
      </Card>

      <Card padding="lg" className="flex flex-col gap-4">
        <div>
          <h2 className="text-sm font-semibold text-[var(--text)]">Preview and import</h2>
          <p className="mt-0.5 text-xs text-[var(--text-faint)]">
            Preview fetches without writing anything, so nothing lands blind.
          </p>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant="outline"
            onClick={async () => setPreview(await doPreview({ kind, limit: 50 }))}
            disabled={previewState.pending || !stored.configured}
          >
            {previewState.pending ? 'Fetching…' : 'Preview 50'}
          </Button>

          <label className="flex items-center gap-2">
            <span className="sr-only">Import into repository</span>
            <select
              value={repoId}
              onChange={(e) => setRepoId(e.target.value)}
              className="rounded-[var(--radius-btn)] border border-[var(--border)] bg-[var(--bg-surface2)] px-2.5 py-1.5 font-mono text-[11px] text-[var(--text)] outline-none focus-visible:ring-2 focus-visible:ring-[var(--brand-teal)]"
            >
              {repos.map((r) => (
                <option key={r.id} value={r.id}>{r.slug}</option>
              ))}
            </select>
          </label>

          <Button
            onClick={async () => setResult(await doRun({ kind, repo_id: repoId }))}
            disabled={runState.pending || !stored.configured || !repoId}
            rightIcon={<ArrowRight size={14} />}
          >
            {runState.pending ? 'Importing…' : 'Import'}
          </Button>
        </div>

        {(previewState.error || runState.error) && (
          <p role="alert" className="text-xs text-[var(--bad)]">
            {(previewState.error || runState.error).message}
          </p>
        )}
        {result && (
          <p className="text-xs" style={{ color: 'var(--ok)' }}>
            Imported {result.imported.toLocaleString()} issue
            {result.imported === 1 ? '' : 's'}. Re-importing updates them in place.
          </p>
        )}
        {preview && (
          <>
            <p className="text-xs text-[var(--text-faint)]">
              {preview.count.toLocaleString()} issue{preview.count === 1 ? '' : 's'} fetched.
            </p>
            <PreviewTable items={preview.items} />
          </>
        )}
      </Card>
    </div>
  )
}

function FileMode({ repos }) {
  const [content, setContent] = useState('')
  const [source, setSource] = useState('')
  const [repoId, setRepoId] = useState(repos[0]?.id ?? '')
  const [fileName, setFileName] = useState('')
  const [result, setResult] = useState(null)
  const [doImport, importState] = useAction(importFile)

  // Read the file in the browser and drop it into the textarea, so the user
  // can see exactly what is about to be parsed before committing to it.
  async function onFile(e) {
    const file = e.target.files?.[0]
    if (!file) return
    setFileName(file.name)
    setContent(await file.text())
    setResult(null)
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-start gap-2.5 rounded-[var(--radius-card)] border border-[var(--border)] bg-[var(--bg-surface2)] p-3.5">
        <span className="mt-0.5 shrink-0 text-[var(--brand-indigo)]"><FileText size={16} /></span>
        <p className="text-xs leading-relaxed text-[var(--text-muted)]">
          Paste or choose a Jira/Linear CSV or JSON export. This path performs
          <strong className="text-[var(--text-dim)]"> no network requests at all</strong> — useful on an
          air-gapped machine, against a Jira Server instance that won&apos;t issue a
          token, or if you&apos;d simply rather not store a credential.
        </p>
      </div>

      <Card padding="lg" className="flex flex-col gap-4">
        <div className="grid gap-4 sm:grid-cols-3">
          <Field label="Export file" hint={fileName || 'CSV or JSON'}>
            <input
              type="file"
              accept=".csv,.json,text/csv,application/json"
              onChange={onFile}
              className="block w-full text-xs text-[var(--text-muted)] file:mr-3 file:rounded-[var(--radius-btn)] file:border-0 file:bg-[var(--bg-surface3)] file:px-3 file:py-1.5 file:text-xs file:text-[var(--text)]"
            />
          </Field>
          <Field label="Source" hint="auto-detected from the file when left on Auto">
            <select value={source} onChange={(e) => setSource(e.target.value)} className={inputCls}>
              <option value="">Auto</option>
              <option value="jira">Jira</option>
              <option value="linear">Linear</option>
            </select>
          </Field>
          <Field label="Import into">
            <select value={repoId} onChange={(e) => setRepoId(e.target.value)} className={inputCls}>
              {repos.map((r) => (
                <option key={r.id} value={r.id}>{r.slug}</option>
              ))}
            </select>
          </Field>
        </div>

        <Field label="Export contents">
          <textarea
            value={content}
            onChange={(e) => setContent(e.target.value)}
            rows={10}
            spellCheck="false"
            placeholder={'Issue key,Summary,Status,Assignee,Labels,Created,Updated,Resolved\nENG-1,Fix the parser,Done,ada@example.com,bug,2026-05-01,2026-06-01,2026-06-01'}
            className={`${inputCls} font-mono text-xs`}
          />
        </Field>

        {importState.error && (
          <p role="alert" className="text-xs text-[var(--bad)]">{importState.error.message}</p>
        )}
        {result && (
          <p className="text-xs" style={{ color: 'var(--ok)' }}>
            Imported {result.imported.toLocaleString()} issue
            {result.imported === 1 ? '' : 's'} from the export.
          </p>
        )}

        <div>
          <Button
            onClick={async () =>
              setResult(await doImport({ source: source || undefined, repo_id: repoId, content }))
            }
            disabled={importState.pending || !content.trim() || !repoId}
          >
            {importState.pending ? 'Importing…' : 'Import from file'}
          </Button>
        </div>
      </Card>
    </div>
  )
}

export default function Import() {
  const navigate = useNavigate()
  const [mode, setMode] = useState('connect')
  const { data, loading, error, reload } = useAsync(loadAll, [])

  if (loading) return <div><PageHeader title="Import" /><Spinner /></div>
  if (error) return <div><PageHeader title="Import" /><ErrorState error={error} onRetry={reload} /></div>

  const repos = data?.repos ?? []
  if (!repos.length) {
    return (
      <div>
        <PageHeader title="Import" />
        <EmptyState
          icon={<DownloadCloud size={22} />}
          title="Add a repo first"
          description="Imported issues attach to a repo, so gitstate needs at least one before it can pull anything in."
          action={<Button onClick={() => navigate('/repos')} rightIcon={<ArrowRight size={15} />}>Add a repo</Button>}
        />
      </div>
    )
  }

  return (
    <div>
      <PageHeader
        title="Import"
        subtitle="Pull Jira and Linear issues in with your own API token — from this machine, with no service in between."
        actions={<SegmentedControl options={MODES} value={mode} onChange={setMode} label="Import mode" />}
      />
      {mode === 'connect' ? (
        <ConnectMode repos={repos} configured={data.trackers ?? []} reload={reload} />
      ) : (
        <FileMode repos={repos} />
      )}
    </div>
  )
}
