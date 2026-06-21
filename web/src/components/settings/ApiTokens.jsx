/**
 * ApiTokens — the "API tokens" Settings section body.
 *
 * Lists the org's API tokens (name, prefix, scope chips, last-used / created /
 * expiry) with a confirm-gated Revoke, and an owner/admin-only create flow that
 * reveals the raw `gsk_…` secret exactly once in a prominent copy panel.
 *
 * Owner/admin gating mirrors the rest of Settings (orgRole === owner|admin); the
 * backend 403s regardless. Imports the useTokens hook + UI primitives only.
 */
import { useState } from 'react'
import {
  KeyRound, Plus, Trash2, Loader2, Check, Copy, AlertTriangle, X, ShieldAlert,
  Terminal, Clock,
} from 'lucide-react'
import { useOrg } from '../../lib/useOrg.js'
import { useTokens } from '../../lib/useTokens.js'
import { Button, Badge } from '../ui/index.js'

// The five valid scopes (backend contract) with short descriptions.
const SCOPES = [
  { key: 'read:issues',       label: 'read:issues',       desc: 'Read issues and their threads' },
  { key: 'read:context',      label: 'read:context',      desc: 'Read repo / issue context bundles for agents' },
  { key: 'read:prs',          label: 'read:prs',          desc: 'Read pull requests and reviews' },
  { key: 'write:agent_runs',  label: 'write:agent_runs',  desc: 'Record agent runs and their outcomes' },
  { key: 'write:issues',      label: 'write:issues',      desc: 'Create and update issues' },
]
const DEFAULT_SCOPES = ['read:issues', 'read:context']

const EXPIRY_OPTIONS = [
  { label: '30 days', value: 30 },
  { label: '90 days', value: 90 },
  { label: 'Never',   value: 'never' },
]

// ── relative-time helpers ──────────────────────────────────────────────────────

function relTime(iso) {
  if (!iso) return null
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return null
  const secs = Math.floor((Date.now() - d.getTime()) / 1000)
  if (secs < 60) return 'just now'
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m ago`
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return `${hrs}h ago`
  const days = Math.floor(hrs / 24)
  if (days < 30) return `${days}d ago`
  const months = Math.floor(days / 30)
  if (months < 12) return `${months}mo ago`
  return `${Math.floor(months / 12)}y ago`
}

function expiryLabel(iso) {
  if (!iso) return { text: 'No expiry', tone: 'muted' }
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return { text: 'No expiry', tone: 'muted' }
  const days = Math.floor((d.getTime() - Date.now()) / 86400000)
  if (days < 0) return { text: 'Expired', tone: 'bad' }
  if (days === 0) return { text: 'Expires today', tone: 'warn' }
  if (days <= 7) return { text: `Expires in ${days}d`, tone: 'warn' }
  return { text: `Expires ${d.toLocaleDateString()}`, tone: 'muted' }
}

// ── copy-to-clipboard hook ─────────────────────────────────────────────────────

function useCopy() {
  const [copied, setCopied] = useState(false)
  const [failed, setFailed] = useState(false)
  async function copy(value) {
    if (!value) return
    setFailed(false)
    try {
      if (!navigator.clipboard) throw new Error('no clipboard')
      await navigator.clipboard.writeText(value)
      setCopied(true)
      setTimeout(() => setCopied(false), 1800)
    } catch {
      // Clipboard blocked (no permission / insecure context) — surface a hint.
      setFailed(true)
      setTimeout(() => setFailed(false), 2600)
    }
  }
  return { copied, failed, copy }
}

// ── revealed raw token panel (shown ONCE) ───────────────────────────────────────

function RevealedToken({ token, onDismiss }) {
  const { copied, failed, copy } = useCopy()
  return (
    <div className="rounded-[var(--radius-btn)] border border-[var(--ok)]/40 bg-[color-mix(in_srgb,var(--ok)_8%,transparent)] p-4 space-y-3">
      <div className="flex items-start gap-2">
        <AlertTriangle size={15} className="mt-0.5 text-[var(--warn)] shrink-0" />
        <div className="flex-1 min-w-0">
          <p className="text-sm font-semibold text-[var(--text)]">Copy your token now — you won&apos;t see it again</p>
          <p className="text-xs text-[var(--text-faint)] mt-0.5">
            This is the only time the full secret is shown. Store it somewhere safe.
          </p>
        </div>
        <button
          type="button" onClick={onDismiss} aria-label="Dismiss"
          className="p-1 text-[var(--text-faint)] hover:text-[var(--text)] transition-colors shrink-0"
        >
          <X size={15} />
        </button>
      </div>

      <div className="flex items-center gap-2">
        <code className="flex-1 min-w-0 truncate rounded-[var(--radius-btn)] border border-[var(--border)] bg-[var(--bg)] px-3 py-2 text-xs text-[var(--text)] font-mono">
          {token}
        </code>
        <Button
          variant="outline" size="sm" onClick={() => copy(token)}
          leftIcon={copied ? <Check size={13} className="text-[var(--ok)]" /> : <Copy size={13} />}
          className="shrink-0"
        >
          {copied ? 'Copied' : 'Copy'}
        </Button>
      </div>
      {failed && <p className="text-xs text-[var(--bad)]">Couldn&apos;t access the clipboard — select the token above and copy it manually.</p>}

      <div className="rounded-[var(--radius-btn)] border border-[var(--border)] bg-[var(--bg)] p-3 space-y-1.5">
        <div className="flex items-center gap-1.5 text-[11px] font-medium text-[var(--text-muted)]">
          <Terminal size={12} className="text-[var(--brand-teal)]" /> Use it with gittrack / the MCP server
        </div>
        <code className="block truncate text-[11px] text-[var(--text-dim)] font-mono">
          export GITSTATE_TOKEN={token}
        </code>
      </div>

      <Button variant="primary" size="sm" onClick={onDismiss}>Done — I&apos;ve stored it</Button>
    </div>
  )
}

// ── create form ─────────────────────────────────────────────────────────────────

function CreateForm({ onCreate }) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [scopes, setScopes] = useState(DEFAULT_SCOPES)
  const [expiry, setExpiry] = useState(90)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState(null)

  function reset() {
    setName('')
    setScopes(DEFAULT_SCOPES)
    setExpiry(90)
    setError(null)
  }

  function toggleScope(key) {
    setScopes((s) => s.includes(key) ? s.filter((k) => k !== key) : [...s, key])
  }

  async function submit(e) {
    e.preventDefault()
    if (!name.trim() || scopes.length === 0 || submitting) return
    setSubmitting(true)
    setError(null)
    try {
      await onCreate({
        name: name.trim(),
        scopes,
        expiresInDays: expiry === 'never' ? undefined : expiry,
      })
      setOpen(false)
      reset()
    } catch (err) {
      setError(err?.message ?? 'Failed to create token')
    } finally {
      setSubmitting(false)
    }
  }

  if (!open) {
    return (
      <Button variant="outline" size="sm" onClick={() => setOpen(true)} leftIcon={<Plus size={13} />}>
        New token
      </Button>
    )
  }

  return (
    <form
      onSubmit={submit}
      className="rounded-[var(--radius-btn)] border border-[var(--border)] bg-[var(--bg)] p-4 space-y-4"
    >
      <div className="flex items-center justify-between">
        <p className="text-sm font-semibold text-[var(--text)]">New API token</p>
        <button
          type="button" onClick={() => { setOpen(false); reset() }} aria-label="Cancel"
          className="p-1 text-[var(--text-faint)] hover:text-[var(--text)] transition-colors"
        >
          <X size={15} />
        </button>
      </div>

      <div>
        <label className="block text-xs font-medium text-[var(--text-muted)] mb-1.5">Name</label>
        <input
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. CI agent, local gittrack"
          autoFocus
          className="w-full rounded-[var(--radius-btn)] border border-[var(--border2)] bg-[var(--bg-surface2)] px-3 py-2 text-sm text-[var(--text)] placeholder:text-[var(--text-faint)] focus:border-[var(--brand-teal)] focus:outline-none"
        />
      </div>

      <div>
        <label className="block text-xs font-medium text-[var(--text-muted)] mb-1.5">Scopes</label>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
          {SCOPES.map((s) => {
            const active = scopes.includes(s.key)
            return (
              <button
                type="button"
                key={s.key}
                onClick={() => toggleScope(s.key)}
                className={[
                  'flex items-start gap-2 text-left rounded-[var(--radius-btn)] border p-2.5 transition-all duration-150 cursor-pointer',
                  active
                    ? 'border-[var(--brand-teal)] bg-[var(--brand-teal)]/5'
                    : 'border-[var(--border)] hover:border-[var(--border2)]',
                ].join(' ')}
              >
                <span
                  className={[
                    'mt-0.5 grid place-items-center w-4 h-4 rounded-[4px] border shrink-0 transition-colors',
                    active ? 'border-[var(--brand-teal)] bg-[var(--brand-teal)] text-[#0B1120]' : 'border-[var(--border2)] text-transparent',
                  ].join(' ')}
                >
                  <Check size={11} />
                </span>
                <span className="min-w-0">
                  <span className="block text-xs font-mono text-[var(--text)]">{s.label}</span>
                  <span className="block text-[11px] text-[var(--text-faint)] mt-0.5 leading-snug">{s.desc}</span>
                </span>
              </button>
            )
          })}
        </div>
      </div>

      <div>
        <label className="block text-xs font-medium text-[var(--text-muted)] mb-1.5">Expiry</label>
        <div className="flex gap-2">
          {EXPIRY_OPTIONS.map((o) => (
            <button
              type="button"
              key={String(o.value)}
              onClick={() => setExpiry(o.value)}
              className={[
                'flex-1 rounded-[var(--radius-btn)] border px-3 py-2 text-xs font-medium transition-all duration-150 cursor-pointer',
                expiry === o.value
                  ? 'border-[var(--brand-teal)] bg-[var(--brand-teal)]/10 text-[var(--brand-teal)]'
                  : 'border-[var(--border)] text-[var(--text-faint)] hover:border-[var(--border2)]',
              ].join(' ')}
            >
              {o.label}
            </button>
          ))}
        </div>
      </div>

      {error && <p className="text-xs text-[var(--bad)]">{error}</p>}

      <div className="flex items-center gap-2">
        <Button
          type="submit"
          variant="primary"
          size="sm"
          disabled={submitting || !name.trim() || scopes.length === 0}
          leftIcon={submitting ? <Loader2 size={13} className="animate-spin" /> : <KeyRound size={13} />}
        >
          {submitting ? 'Creating…' : 'Create token'}
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={() => { setOpen(false); reset() }}>
          Cancel
        </Button>
      </div>
    </form>
  )
}

// ── token list row ──────────────────────────────────────────────────────────────

function TokenRow({ token, onRevoke, canManage }) {
  const [confirming, setConfirming] = useState(false)
  const [revoking, setRevoking] = useState(false)
  const [error, setError] = useState(null)

  const revoked = Boolean(token.revokedAt)
  const created = relTime(token.createdAt)
  const lastUsed = token.lastUsedAt ? relTime(token.lastUsedAt) : null
  const exp = expiryLabel(token.expiresAt)
  const expColor = exp.tone === 'bad' ? 'var(--bad)' : exp.tone === 'warn' ? 'var(--warn)' : 'var(--text-faint)'

  async function revoke() {
    setRevoking(true)
    setError(null)
    try {
      await onRevoke(token.id)
    } catch (e) {
      setError(e?.message ?? 'Failed to revoke')
      setRevoking(false)
      setConfirming(false)
    }
  }

  return (
    <div className={`py-3.5 border-t border-[var(--border)] first:border-t-0 first:pt-0 ${revoked ? 'opacity-55' : ''}`}>
      <div className="flex items-start gap-3">
        <span className="mt-0.5 grid place-items-center w-8 h-8 rounded-[var(--radius-btn)] bg-[var(--bg)] border border-[var(--border)] shrink-0">
          <KeyRound size={14} className="text-[var(--text-faint)]" />
        </span>

        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            <p className={`text-sm font-medium truncate ${revoked ? 'text-[var(--text-muted)] line-through' : 'text-[var(--text)]'}`}>{token.name}</p>
            <code className="text-[11px] font-mono text-[var(--text-faint)] bg-[var(--bg)] border border-[var(--border)] rounded px-1.5 py-0.5">
              {token.prefix}…
            </code>
            {revoked && <Badge color="red">revoked</Badge>}
          </div>

          {Array.isArray(token.scopes) && token.scopes.length > 0 && (
            <div className="flex flex-wrap gap-1 mt-1.5">
              {token.scopes.map((s) => (
                <Badge key={s} color={s.startsWith('write') ? 'indigo' : 'teal'}>{s}</Badge>
              ))}
            </div>
          )}

          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 mt-2 text-[11px] text-[var(--text-faint)]">
            {created && <span>Created {created}</span>}
            <span className="inline-flex items-center gap-1">
              <Clock size={10} /> {lastUsed ? `Last used ${lastUsed}` : 'Never used'}
            </span>
            <span style={{ color: expColor }}>{exp.text}</span>
          </div>
          {error && <p className="text-xs text-[var(--bad)] mt-1.5">{error}</p>}
        </div>

        {!revoked && canManage && (
          <div className="shrink-0">
            {confirming ? (
              <div className="flex items-center gap-1.5">
                <Button
                  variant="danger" size="xs" onClick={revoke} disabled={revoking}
                  leftIcon={revoking ? <Loader2 size={12} className="animate-spin" /> : <Trash2 size={12} />}
                >
                  {revoking ? 'Revoking…' : 'Confirm'}
                </Button>
                <Button variant="ghost" size="xs" onClick={() => setConfirming(false)} disabled={revoking}>
                  Cancel
                </Button>
              </div>
            ) : (
              <Button
                variant="outline" size="xs" onClick={() => setConfirming(true)}
                leftIcon={<Trash2 size={12} />}
                className="hover:border-[color-mix(in_srgb,var(--bad)_30%,transparent)] hover:text-[var(--bad)]"
              >
                Revoke
              </Button>
            )}
          </div>
        )}
      </div>
    </div>
  )
}

// ── section body ────────────────────────────────────────────────────────────────

export function ApiTokensBody() {
  const { orgRole } = useOrg()
  const canManage = orgRole === 'owner' || orgRole === 'admin'
  const { data, loading, error, create, revoke } = useTokens()
  const [revealed, setRevealed] = useState(null) // the raw token, shown ONCE

  async function handleCreate(payload) {
    const res = await create(payload)
    if (res?.token) setRevealed(res.token)
    return res
  }

  const tokens = Array.isArray(data) ? data : []

  return (
    <div className="space-y-4">
      <div className="flex items-start gap-2 rounded-[var(--radius-btn)] border border-[var(--border)] bg-[var(--bg)] p-3 text-[11px] text-[var(--text-faint)] leading-relaxed">
        <Terminal size={13} className="mt-0.5 text-[var(--brand-teal)] shrink-0" />
        <span>
          Personal access tokens for agents, the <span className="font-mono text-[var(--text-dim)]">gittrack</span> CLI
          and MCP integrations. Scope them tightly and rotate often — the full secret is shown only once at creation.
        </span>
      </div>

      {loading && !data ? (
        <div className="flex items-center gap-2 py-4 text-xs text-[var(--text-faint)]">
          <Loader2 size={14} className="animate-spin" /> Loading…
        </div>
      ) : error ? (
        <p className="text-xs text-[var(--bad)] py-2">{error}</p>
      ) : (
        <>
          {tokens.length === 0 ? (
            <div className="rounded-[var(--radius-btn)] border border-dashed border-[var(--border2)] bg-[var(--bg)] py-8 px-4 text-center">
              <KeyRound size={20} className="mx-auto text-[var(--text-faint)]" />
              <p className="text-sm text-[var(--text-muted)] mt-2">No API tokens yet</p>
              <p className="text-xs text-[var(--text-faint)] mt-1">
                Create one to let an agent, the CLI or an MCP server authenticate.
              </p>
            </div>
          ) : (
            <div>
              {tokens.map((t) => (
                <TokenRow key={t.id} token={t} onRevoke={revoke} canManage={canManage} />
              ))}
            </div>
          )}

          {revealed && (
            <RevealedToken token={revealed} onDismiss={() => setRevealed(null)} />
          )}

          {canManage ? (
            !revealed && <CreateForm onCreate={handleCreate} />
          ) : (
            <div className="flex items-start gap-2 rounded-[var(--radius-btn)] border border-[var(--border)] bg-[var(--bg)] p-3 text-xs text-[var(--text-faint)]">
              <ShieldAlert size={14} className="mt-0.5 text-[var(--warn)] shrink-0" />
              <span>Only owners and admins can create API tokens. Ask an admin in your workspace to mint one for you.</span>
            </div>
          )}
        </>
      )}
    </div>
  )
}
