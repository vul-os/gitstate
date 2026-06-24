/**
 * People / Contributors — /people
 *
 * Manage contributor identities. Git history names one human by many raw
 * emails/logins; gitstate auto-clusters those into canonical contributors.
 * Here you can: re-run detection, rename a contributor inline, see + split its
 * git identities, link it to an org member, invite the un-invited, exclude
 * people from analytics, and merge duplicates together.
 */
import { useState, useMemo, useRef, useEffect, useCallback } from 'react'
import {
  Users, Loader2, Sparkles, Search, Link2, Mail, EyeOff, Eye, GitMerge,
  X, Pencil, Bot, GitCommitHorizontal, GitPullRequest, MessageSquare,
  Scissors, Star, AtSign, AlertCircle, ChevronDown,
} from 'lucide-react'
import { useOrg } from '../lib/useOrg.js'
import { useContributors, useOrgMembers } from '../lib/useContributors.js'
import { Card, Badge, Button, StatCard } from '../components/ui/index.js'
import { Reveal } from '../components/Reveal.jsx'

// ── Small helpers ───────────────────────────────────────────────────────────────

function initials(name, email) {
  if (name) return name.split(/\s+/).map(w => w[0]).join('').slice(0, 2).toUpperCase()
  return (email ?? '?').slice(0, 2).toUpperCase()
}

function Avatar({ name, email, muted }) {
  return (
    <div
      className={[
        'w-9 h-9 rounded-full flex items-center justify-center text-[12px] font-bold shrink-0 select-none',
        muted
          ? 'bg-[var(--bg-surface3)] text-[var(--text-faint)]'
          : 'bg-gradient-to-br from-[var(--brand-teal)] to-[var(--brand-indigo)] text-[#0B1120]',
      ].join(' ')}
    >
      {initials(name, email)}
    </div>
  )
}

const STATUS_FILTERS = [
  { key: 'all', label: 'All' },
  { key: 'linked', label: 'Linked' },
  { key: 'invited', label: 'Invited' },
  { key: 'uninvited', label: 'Not invited' },
]

// ── Status chip ─────────────────────────────────────────────────────────────────

function StatusChip({ c }) {
  if (c.status === 'linked') {
    return (
      <Badge color="teal" title={c.memberEmail ?? c.memberName ?? 'Linked member'}>
        <Link2 size={10} />
        Linked{c.memberName ? ` · ${c.memberName}` : ''}
      </Badge>
    )
  }
  if (c.status === 'invited') {
    return (
      <Badge color="blue" title={c.invitedAt ? `Invited ${c.invitedAt}` : 'Invited'}>
        <Mail size={10} />
        Invited
      </Badge>
    )
  }
  return (
    <Badge color="yellow" title="Not yet linked to a member or invited">
      <AlertCircle size={10} />
      Not invited
    </Badge>
  )
}

// ── Identity chip (with split affordance) ────────────────────────────────────────

function IdentityChip({ identity, canSplit, onSplit, isDefault, onSetDefault, settingDefault, busy }) {
  const isLogin = identity.kind === 'login'
  return (
    <span
      className={[
        'group/idn inline-flex items-center gap-1 max-w-full rounded-[var(--radius-badge)] border pl-1.5 pr-1 py-0.5 text-[11px] font-mono',
        isDefault
          ? 'border-[var(--brand-teal)]/40 bg-[var(--brand-teal)]/10 text-[var(--text-dim)]'
          : 'border-[var(--border)] bg-[var(--bg-surface3)] text-[var(--text-muted)]',
      ].join(' ')}
      title={`${identity.nameSeen ? `Seen as “${identity.nameSeen}” · ` : ''}${isLogin ? 'login' : 'email'}${isDefault ? ' · default identity' : ''}`}
    >
      {/* Default-identity star: filled when this is the canonical name/email used everywhere. */}
      <button
        type="button"
        onClick={() => onSetDefault(identity)}
        disabled={settingDefault || isDefault}
        aria-label={isDefault ? `${identity.value} is the default identity` : `Make ${identity.value} the default identity`}
        title={isDefault ? 'Default identity (used everywhere)' : 'Set as default identity'}
        className={[
          'grid place-items-center w-4 h-4 rounded shrink-0 transition-all focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-[var(--brand-teal)]',
          isDefault ? 'text-[var(--brand-teal)] cursor-default' : 'text-[var(--text-faint)] hover:text-[var(--brand-teal)] hover:bg-[var(--bg-surface)] disabled:opacity-40',
        ].join(' ')}
      >
        {settingDefault ? <Loader2 size={9} className="animate-spin" /> : <Star size={9} className={isDefault ? 'fill-current' : ''} />}
      </button>
      {isLogin ? <AtSign size={10} className="shrink-0 text-[var(--text-faint)]" /> : <Mail size={10} className="shrink-0 text-[var(--text-faint)]" />}
      <span className="truncate">{identity.value}</span>
      {canSplit && (
        <button
          type="button"
          onClick={() => onSplit(identity.value)}
          disabled={busy}
          aria-label={`Split ${identity.value} into its own contributor`}
          title="Split out as its own contributor"
          className="ml-0.5 grid place-items-center w-4 h-4 rounded text-[var(--text-faint)] opacity-0 group-hover/idn:opacity-100 focus:opacity-100 hover:text-[var(--brand-teal)] hover:bg-[var(--bg-surface)] transition-all disabled:opacity-40 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-[var(--brand-teal)]"
        >
          {busy ? <Loader2 size={9} className="animate-spin" /> : <Scissors size={9} />}
        </button>
      )}
    </span>
  )
}

// ── Identity grouping ─────────────────────────────────────────────────────────────

// groupIdentities lays a contributor's flat identity list out as login → attached
// emails. An email "belongs to" a login when it co-occurred with that login on a
// commit (backend `linkedLogins`). Each email is attached to EXACTLY ONE login
// (the first of its linked logins that belongs to this contributor) so it renders
// ONCE — never duplicated across groups (which also duplicated the default star).
// Emails linked to no in-contributor login are standalone → "Other emails".
function groupIdentities(identities) {
  const list = identities ?? []
  const logins = list.filter(i => i.kind === 'login')
  const emails = list.filter(i => i.kind === 'email')
  const loginValues = new Set(logins.map(l => l.value))

  const byLogin = new Map(logins.map(l => [l.value, []]))
  const otherEmails = []
  for (const e of emails) {
    // The first linked login that's actually one of this contributor's logins.
    const target = (e.linkedLogins ?? []).find(v => loginValues.has(v))
    if (target) byLogin.get(target).push(e)
    else otherEmails.push(e)
  }

  const groups = logins.map(login => ({ login, emails: byLogin.get(login.value) }))
  return { groups, otherEmails }
}

// ── Inline-editable display name ─────────────────────────────────────────────────

function EditableName({ value, onSave }) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(value)
  const [saving, setSaving] = useState(false)
  const inputRef = useRef(null)

  useEffect(() => { if (editing) inputRef.current?.focus() }, [editing])

  function beginEdit() {
    setDraft(value)
    setEditing(true)
  }

  async function commit() {
    const next = draft.trim()
    if (!next || next === value) { setEditing(false); setDraft(value); return }
    setSaving(true)
    try {
      await onSave(next)
      setEditing(false)
    } catch {
      setDraft(value)
      setEditing(false)
    } finally {
      setSaving(false)
    }
  }

  if (editing) {
    return (
      <span className="inline-flex items-center gap-1">
        <input
          ref={inputRef}
          value={draft}
          onChange={e => setDraft(e.target.value)}
          onKeyDown={e => {
            if (e.key === 'Enter') commit()
            if (e.key === 'Escape') { setEditing(false); setDraft(value) }
          }}
          onBlur={commit}
          aria-label="Edit display name"
          className="px-1.5 py-0.5 rounded-[var(--radius-badge)] bg-[var(--bg)] border border-[var(--brand-teal)] text-sm font-medium text-[var(--text)] outline-none w-44 max-w-full"
        />
        {saving && <Loader2 size={12} className="animate-spin text-[var(--brand-teal)]" />}
      </span>
    )
  }

  return (
    <button
      type="button"
      onClick={beginEdit}
      className="group/name inline-flex items-center gap-1.5 text-left rounded px-0.5 -mx-0.5 hover:bg-[var(--bg-surface2)] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--brand-teal)]"
      title="Rename contributor"
    >
      <span className="text-sm font-medium text-[var(--text)] truncate">{value || 'Unnamed'}</span>
      <Pencil size={11} className="text-[var(--text-faint)] opacity-0 group-hover/name:opacity-100 transition-opacity shrink-0" />
    </button>
  )
}

// ── Link-to-member dropdown ──────────────────────────────────────────────────────

function LinkMenu({ members, loading, onPick, busy }) {
  const [open, setOpen] = useState(false)
  const [q, setQ] = useState('')
  const ref = useRef(null)

  useEffect(() => {
    if (!open) return
    function onDoc(e) { if (ref.current && !ref.current.contains(e.target)) setOpen(false) }
    document.addEventListener('mousedown', onDoc)
    return () => document.removeEventListener('mousedown', onDoc)
  }, [open])

  const filtered = useMemo(() => {
    const t = q.trim().toLowerCase()
    if (!t) return members
    return members.filter(m =>
      (m.name ?? '').toLowerCase().includes(t) || (m.email ?? '').toLowerCase().includes(t))
  }, [members, q])

  return (
    <div className="relative" ref={ref}>
      <Button
        variant="outline"
        size="xs"
        onClick={() => setOpen(o => !o)}
        disabled={busy}
        leftIcon={busy ? <Loader2 size={12} className="animate-spin" /> : <Link2 size={12} />}
        rightIcon={<ChevronDown size={12} />}
      >
        Link
      </Button>
      {open && (
        <div className="absolute right-0 z-30 mt-1 w-64 rounded-[var(--radius-card)] border border-[var(--border2)] bg-[var(--bg-surface)] shadow-[var(--shadow-card-hover)] p-1.5">
          <div className="flex items-center gap-1.5 px-2 py-1.5 border-b border-[var(--border)] mb-1">
            <Search size={12} className="text-[var(--text-faint)]" />
            <input
              autoFocus
              value={q}
              onChange={e => setQ(e.target.value)}
              placeholder="Search members…"
              aria-label="Search members"
              className="flex-1 bg-transparent text-xs text-[var(--text)] placeholder-[var(--text-faint)] outline-none"
            />
          </div>
          <div className="max-h-56 overflow-y-auto">
            {loading && <p className="px-2 py-3 text-xs text-[var(--text-faint)]">Loading members…</p>}
            {!loading && filtered.length === 0 && (
              <p className="px-2 py-3 text-xs text-[var(--text-faint)]">No members found.</p>
            )}
            {filtered.map(m => (
              <button
                key={m.userId}
                type="button"
                onClick={() => { setOpen(false); onPick(m.userId) }}
                className="w-full flex items-center gap-2 px-2 py-1.5 rounded-[var(--radius-badge)] text-left hover:bg-[var(--bg-surface2)] transition-colors"
              >
                <Avatar name={m.name} email={m.email} />
                <span className="min-w-0">
                  <span className="block text-xs font-medium text-[var(--text)] truncate">{m.name ?? m.email ?? m.userId}</span>
                  {m.name && m.email && <span className="block text-[11px] text-[var(--text-faint)] truncate">{m.email}</span>}
                </span>
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

// ── Invite popover ───────────────────────────────────────────────────────────────

function InviteMenu({ defaultEmail, onInvite, busy }) {
  const [open, setOpen] = useState(false)
  const [email, setEmail] = useState(defaultEmail ?? '')
  const ref = useRef(null)

  useEffect(() => {
    if (!open) return
    function onDoc(e) { if (ref.current && !ref.current.contains(e.target)) setOpen(false) }
    document.addEventListener('mousedown', onDoc)
    return () => document.removeEventListener('mousedown', onDoc)
  }, [open])

  function toggle() {
    setOpen(o => {
      const next = !o
      if (next) setEmail(defaultEmail ?? '') // refresh default each open
      return next
    })
  }

  async function submit(e) {
    e.preventDefault()
    try {
      await onInvite(email.trim() || undefined)
      setOpen(false)
    } catch { /* surfaced upstream */ }
  }

  return (
    <div className="relative" ref={ref}>
      <Button
        variant="outline"
        size="xs"
        onClick={toggle}
        disabled={busy}
        leftIcon={busy ? <Loader2 size={12} className="animate-spin" /> : <Mail size={12} />}
      >
        Invite
      </Button>
      {open && (
        <form
          onSubmit={submit}
          className="absolute right-0 z-30 mt-1 w-72 rounded-[var(--radius-card)] border border-[var(--border2)] bg-[var(--bg-surface)] shadow-[var(--shadow-card-hover)] p-3"
        >
          <label className="block text-[11px] font-medium text-[var(--text-muted)] mb-1.5">
            Invite email
          </label>
          <input
            type="email"
            value={email}
            onChange={e => setEmail(e.target.value)}
            placeholder="person@example.com"
            aria-label="Invite email"
            className="w-full px-2.5 py-1.5 rounded-[var(--radius-btn)] bg-[var(--bg)] border border-[var(--border)] text-xs text-[var(--text)] placeholder-[var(--text-faint)] outline-none focus:border-[var(--brand-teal)] focus:ring-2 focus:ring-[var(--brand-teal)]/15"
          />
          <p className="text-[11px] text-[var(--text-faint)] mt-1.5">
            Defaults to the primary email — change it to invite a different address.
          </p>
          <div className="flex justify-end gap-2 mt-2.5">
            <Button type="button" variant="ghost" size="xs" onClick={() => setOpen(false)}>Cancel</Button>
            <Button type="submit" size="xs" disabled={busy} leftIcon={busy ? <Loader2 size={12} className="animate-spin" /> : <Mail size={12} />}>
              Send invite
            </Button>
          </div>
        </form>
      )}
    </div>
  )
}

// ── Stats inline ─────────────────────────────────────────────────────────────────

function MiniStat({ icon, value, label }) {
  return (
    <span className="inline-flex items-center gap-1 text-[11px] font-mono text-[var(--text-faint)]" title={label}>
      {icon}
      <span className="tabular-nums text-[var(--text-muted)]">{value ?? 0}</span>
    </span>
  )
}

// ── Grouped identities (login → attached emails, + standalone "Other emails") ────

function IdentityGroups({ identities, chipProps }) {
  const { groups, otherEmails } = useMemo(() => groupIdentities(identities), [identities])
  if (groups.length === 0 && otherEmails.length === 0) return null

  return (
    <div className="flex flex-wrap gap-2 mt-2">
      {groups.map(({ login, emails }) => (
        // One unified pill: the @username and its email(s) together, so the
        // link is obvious. Each chip keeps its own star/split actions.
        <div
          key={`g:${login.value}`}
          className="inline-flex flex-wrap items-center gap-1.5 rounded-[var(--radius-badge)] border border-[var(--border)] bg-[var(--bg-surface2)]/40 px-1.5 py-1"
        >
          <IdentityChip identity={login} {...chipProps(login)} />
          {emails.map(email => (
            <span key={`${login.value}>${email.value}`} className="inline-flex items-center gap-1.5">
              <span className="text-[var(--text-faint)] text-[10px] select-none">·</span>
              <IdentityChip identity={email} {...chipProps(email)} />
            </span>
          ))}
        </div>
      ))}

      {otherEmails.length > 0 && (
        <div className="inline-flex flex-wrap items-center gap-1.5 rounded-[var(--radius-badge)] border border-dashed border-[var(--border)] bg-[var(--bg-surface2)]/30 px-1.5 py-1">
          <span className="text-[10px] uppercase tracking-wide font-medium text-[var(--text-faint)] shrink-0 px-1">
            No linked username
          </span>
          {otherEmails.map(email => (
            <IdentityChip key={`other>${email.value}`} identity={email} {...chipProps(email)} />
          ))}
        </div>
      )}
    </div>
  )
}

// ── Contributor row ──────────────────────────────────────────────────────────────

function ContributorRow({
  c, members, membersLoading, selected, onToggleSelect,
  onRename, onLink, onInvite, onToggleExclude, onSplit, onSetDefault, busy,
}) {
  // An identity is the current default when it supplies the canonical name/email.
  // Compared case-insensitively (identity values are lowercased; primary_email may not be).
  const lc = (s) => (s || '').toLowerCase()
  const isDefaultIdentity = (idn) =>
    (idn.kind === 'email' && !!c.primaryEmail && lc(idn.value) === lc(c.primaryEmail)) ||
    (idn.kind === 'login' && !c.primaryEmail && lc(idn.nameSeen || idn.value) === lc(c.displayName))
  const muted = c.excluded
  const canSplit = (c.identities?.length ?? 0) > 1
  const stats = c.stats ?? {}

  return (
    <div
      className={[
        'flex flex-col gap-3 px-4 sm:px-6 py-4 border-b border-[var(--border)] last:border-b-0 transition-colors',
        muted ? 'opacity-55 hover:opacity-80' : 'hover:bg-[var(--bg-surface2)]/50',
      ].join(' ')}
    >
      <div className="flex items-start gap-3">
        {/* Merge selection checkbox */}
        <label className="mt-1 shrink-0 cursor-pointer" title="Select for merge">
          <input
            type="checkbox"
            checked={selected}
            onChange={() => onToggleSelect(c.id)}
            aria-label={`Select ${c.displayName} for merge`}
            className="w-4 h-4 rounded border-[var(--border2)] accent-[var(--brand-teal)] cursor-pointer"
          />
        </label>

        <Avatar name={c.displayName} email={c.primaryEmail} muted={muted} />

        <div className="flex-1 min-w-0">
          {/* Name row */}
          <div className="flex items-center gap-2 flex-wrap">
            <EditableName value={c.displayName} onSave={onRename} />
            {c.isBot && (
              <Badge color="default" title="Automated / bot identity">
                <Bot size={10} /> Bot
              </Badge>
            )}
            {muted && (
              <Badge color="red" title="Excluded from contribution & analytics">
                <EyeOff size={10} /> Excluded
              </Badge>
            )}
            <StatusChip c={c} />
          </div>

          {c.primaryEmail && (
            <p className="text-xs text-[var(--text-faint)] truncate mt-0.5">{c.primaryEmail}</p>
          )}

          {/* Identities — grouped: each @login anchors the emails it co-occurred
              with on commits; emails tied to no login fall under "Other emails". */}
          {(c.identities?.length ?? 0) > 0 && (
            <IdentityGroups
              identities={c.identities}
              chipProps={(idn) => ({
                canSplit,
                onSplit: (v) => onSplit(c.id, v),
                isDefault: isDefaultIdentity(idn),
                onSetDefault: (i) => onSetDefault(c.id, i),
                settingDefault: busy.default === `${c.id}:${idn.value}`,
                busy: busy.split === `${c.id}:${idn.value}`,
              })}
            />
          )}

          {/* Stats */}
          <div className="flex items-center gap-3 mt-2">
            <MiniStat icon={<GitCommitHorizontal size={12} />} value={stats.commits} label="commits" />
            <MiniStat icon={<GitPullRequest size={12} />} value={stats.prs} label="pull requests" />
            <MiniStat icon={<MessageSquare size={12} />} value={stats.reviews} label="reviews" />
          </div>
        </div>

        {/* Actions */}
        <div className="flex items-center gap-1.5 shrink-0 flex-wrap justify-end">
          {c.status !== 'linked' && (
            <LinkMenu
              members={members}
              loading={membersLoading}
              onPick={(userId) => onLink(c.id, userId)}
              busy={busy.link === c.id}
            />
          )}
          {c.status === 'uninvited' && (
            <InviteMenu
              defaultEmail={c.primaryEmail}
              onInvite={(email) => onInvite(c.id, email)}
              busy={busy.invite === c.id}
            />
          )}
          <button
            type="button"
            onClick={() => onToggleExclude(c)}
            disabled={busy.exclude === c.id}
            title={muted ? 'Include in analytics' : 'Exclude from analytics'}
            aria-label={muted ? `Include ${c.displayName}` : `Exclude ${c.displayName}`}
            className="grid place-items-center w-7 h-7 rounded-[var(--radius-badge)] border border-[var(--border2)] text-[var(--text-faint)] hover:text-[var(--text)] hover:border-[var(--brand-teal)] transition-all disabled:opacity-40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--brand-teal)]"
          >
            {busy.exclude === c.id
              ? <Loader2 size={13} className="animate-spin" />
              : muted ? <Eye size={13} /> : <EyeOff size={13} />}
          </button>
        </div>
      </div>
    </div>
  )
}

// ── Merge bar ────────────────────────────────────────────────────────────────────

function MergeBar({ selectedIds, contributors, onCancel, onMerge, merging }) {
  // Track only the user's explicit override; the effective survivor falls back
  // to the first selected id whenever the override is no longer selected. This
  // avoids syncing prop→state in an effect.
  const [override, setOverride] = useState(null)
  const survivor = selectedIds.includes(override) ? override : selectedIds[0]

  const selected = contributors.filter(c => selectedIds.includes(c.id))
  if (selected.length < 2) return null

  return (
    <div className="sticky bottom-4 z-20 mt-4">
      <Card padding="none" className="shadow-[var(--shadow-card-hover)] border-[var(--brand-teal)]/40">
        <div className="flex items-center gap-3 px-4 py-3 flex-wrap">
          <GitMerge size={16} className="text-[var(--brand-teal)] shrink-0" />
          <span className="text-sm text-[var(--text)]">
            Merge <span className="font-semibold">{selected.length}</span> contributors
          </span>
          <div className="flex items-center gap-1.5">
            <label className="text-xs text-[var(--text-faint)]">Keep:</label>
            <select
              value={survivor}
              onChange={e => setOverride(e.target.value)}
              aria-label="Survivor contributor"
              className="px-2 py-1 rounded-[var(--radius-badge)] bg-[var(--bg)] border border-[var(--border)] text-xs text-[var(--text)] outline-none focus:border-[var(--brand-teal)] cursor-pointer max-w-[180px]"
            >
              {selected.map(c => (
                <option key={c.id} value={c.id}>{c.displayName || c.primaryEmail || c.id}</option>
              ))}
            </select>
          </div>
          <div className="flex-1" />
          <Button variant="ghost" size="xs" onClick={onCancel} disabled={merging}>Cancel</Button>
          <Button
            size="xs"
            onClick={() => onMerge(survivor)}
            disabled={merging}
            leftIcon={merging ? <Loader2 size={12} className="animate-spin" /> : <GitMerge size={12} />}
          >
            {merging ? 'Merging…' : 'Merge selected'}
          </Button>
        </div>
      </Card>
    </div>
  )
}

// ── Page ─────────────────────────────────────────────────────────────────────────

export default function People() {
  const { activeOrg } = useOrg()
  const {
    contributors, loading, error, notReady, detecting, detectSummary,
    detect, patch, merge, split, link, invite,
  } = useContributors()
  const { members, loading: membersLoading } = useOrgMembers()

  const [search, setSearch] = useState('')
  const [statusFilter, setStatusFilter] = useState('all')
  const [showExcluded, setShowExcluded] = useState(false)
  const [selectedIds, setSelectedIds] = useState([])
  const [busy, setBusy] = useState({}) // { link, invite, exclude, split }
  const [actionError, setActionError] = useState(null)
  const [merging, setMerging] = useState(false)

  const setBusyKey = useCallback((k, v) => setBusy(prev => ({ ...prev, [k]: v })), [])

  const toggleSelect = useCallback((id) => {
    setSelectedIds(prev => prev.includes(id) ? prev.filter(x => x !== id) : [...prev, id])
  }, [])

  // ── Action handlers (all guard + surface errors) ──
  const onRename = useCallback(async (id, displayName) => {
    setActionError(null)
    try { await patch(id, { displayName }) }
    catch (e) { setActionError(e?.message ?? 'Rename failed'); throw e }
  }, [patch])

  const onToggleExclude = useCallback(async (c) => {
    setActionError(null)
    setBusyKey('exclude', c.id)
    try { await patch(c.id, { excluded: !c.excluded }) }
    catch (e) { setActionError(e?.message ?? 'Update failed') }
    finally { setBusyKey('exclude', null) }
  }, [patch, setBusyKey])

  const onLink = useCallback(async (id, userId) => {
    setActionError(null)
    setBusyKey('link', id)
    try { await link(id, userId) }
    catch (e) { setActionError(e?.message ?? 'Link failed') }
    finally { setBusyKey('link', null) }
  }, [link, setBusyKey])

  const onInvite = useCallback(async (id, email) => {
    setActionError(null)
    setBusyKey('invite', id)
    try { await invite(id, email) }
    catch (e) { setActionError(e?.message ?? 'Invite failed'); throw e }
    finally { setBusyKey('invite', null) }
  }, [invite, setBusyKey])

  const onSplit = useCallback(async (id, value) => {
    setActionError(null)
    setBusyKey('split', `${id}:${value}`)
    try { await split(id, value) }
    catch (e) { setActionError(e?.message ?? 'Split failed') }
    finally { setBusyKey('split', null) }
  }, [split, setBusyKey])

  // onSetDefault makes a chosen identity the canonical one for an unlinked (or any)
  // contributor — its name + (for an email identity) its address become the
  // display_name/primary_email used throughout the leaderboard, contribution, and
  // every people-keyed chart.
  const onSetDefault = useCallback(async (id, idn) => {
    setActionError(null)
    setBusyKey('default', `${id}:${idn.value}`)
    try {
      const body = {}
      if (idn.kind === 'email') {
        // Make this email the canonical/default. Update the display name only when
        // the identity actually carries a name — never clobber it with a raw email.
        body.primaryEmail = idn.value
        if (idn.nameSeen) body.displayName = idn.nameSeen
      } else {
        // A login becomes canonical: use its name + clear the email default so the
        // login's isDefault check (which requires no primary_email) lights up.
        body.displayName = idn.nameSeen || idn.value
        body.primaryEmail = ''
      }
      await patch(id, body)
    } catch (e) { setActionError(e?.message ?? 'Set-default failed') }
    finally { setBusyKey('default', null) }
  }, [patch, setBusyKey])

  const onMerge = useCallback(async (survivorId) => {
    setActionError(null)
    setMerging(true)
    try {
      const losers = selectedIds.filter(id => id !== survivorId && contributors.some(c => c.id === id))
      // Merge each loser into the survivor sequentially.
      for (const id of losers) {
        await merge(id, survivorId)
      }
      setSelectedIds([])
    } catch (e) {
      setActionError(e?.message ?? 'Merge failed')
    } finally {
      setMerging(false)
    }
  }, [selectedIds, contributors, merge])

  const onDetect = useCallback(async () => {
    setActionError(null)
    try { await detect() }
    catch (e) { setActionError(e?.message ?? 'Detection failed') }
  }, [detect])

  // ── Derived ──
  const filtered = useMemo(() => {
    const t = search.trim().toLowerCase()
    return contributors.filter(c => {
      if (!showExcluded && c.excluded) return false
      if (statusFilter !== 'all' && c.status !== statusFilter) return false
      if (!t) return true
      if ((c.displayName ?? '').toLowerCase().includes(t)) return true
      if ((c.primaryEmail ?? '').toLowerCase().includes(t)) return true
      if ((c.memberName ?? '').toLowerCase().includes(t)) return true
      return (c.identities ?? []).some(idn => (idn.value ?? '').toLowerCase().includes(t))
    })
  }, [contributors, search, statusFilter, showExcluded])

  const counts = useMemo(() => {
    const visible = contributors.filter(c => !c.excluded)
    const identityCount = contributors.reduce((n, c) => n + (c.identities?.length ?? 0), 0)
    return {
      people: visible.length,
      total: contributors.length,
      identities: identityCount,
      linked: contributors.filter(c => c.status === 'linked').length,
      uninvited: contributors.filter(c => c.status === 'uninvited' && !c.excluded).length,
      excluded: contributors.filter(c => c.excluded).length,
    }
  }, [contributors])

  // Selections that still point at a live contributor (e.g. survivors after a
  // merge). Derived rather than pruned-in-effect so it can't cascade renders.
  const validSelectedIds = useMemo(
    () => selectedIds.filter(id => contributors.some(c => c.id === id)),
    [selectedIds, contributors],
  )

  if (!activeOrg) {
    return (
      <div className="max-w-2xl">
        <Header />
        <Card padding="xl" className="text-center mt-6">
          <Users size={22} className="mx-auto text-[var(--text-faint)] mb-2" />
          <p className="text-sm text-[var(--text-faint)]">No active organization. Create or select one from the sidebar.</p>
        </Card>
      </div>
    )
  }

  return (
    <div className="w-full">
      <Reveal>
        <Header orgName={activeOrg.name} />
      </Reveal>

      {/* Auto-group / detect banner */}
      <Reveal delay={0.04}>
        <Card padding="lg" className="mb-5">
          <div className="flex items-start justify-between gap-4 flex-wrap">
            <div className="flex items-start gap-3 min-w-0">
              <span className="mt-0.5 grid place-items-center w-9 h-9 rounded-[var(--radius-btn)] bg-[var(--brand-indigo)]/10 border border-[var(--brand-indigo)]/20 shrink-0">
                <Sparkles size={16} className="text-[var(--brand-indigo)]" />
              </span>
              <div className="min-w-0">
                <h2 className="text-sm font-semibold text-[var(--text)]">Auto-grouped contributors</h2>
                <p className="text-xs text-[var(--text-faint)] mt-1 max-w-xl">
                  Identities are grouped automatically by name, email and login — and stay
                  fully editable. Rename, merge duplicates, or split an identity out at any time.
                </p>
                {(detectSummary || counts.identities > 0) && (
                  <p className="text-xs font-mono text-[var(--text-muted)] mt-2">
                    <span className="text-[var(--text)]">{(detectSummary?.identities ?? counts.identities).toLocaleString()}</span> identities
                    {' → '}
                    <span className="text-[var(--brand-teal)]">{(detectSummary?.contributors ?? counts.total).toLocaleString()}</span> people
                    {detectSummary?.merged != null && detectSummary.merged > 0 && (
                      <span className="text-[var(--text-faint)]"> · {detectSummary.merged} auto-merged</span>
                    )}
                  </p>
                )}
              </div>
            </div>
            <Button
              onClick={onDetect}
              disabled={detecting}
              leftIcon={detecting ? <Loader2 size={14} className="animate-spin" /> : <Sparkles size={14} />}
            >
              {detecting ? 'Detecting…' : 'Detect / re-group'}
            </Button>
          </div>
        </Card>
      </Reveal>

      {/* Stat tiles */}
      {!loading && counts.total > 0 && (
        <Reveal delay={0.06}>
          <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-5">
            <StatCard label="People" value={counts.total.toLocaleString()} sublabel="canonical contributors" accent="var(--chart-2)" icon={<Users size={14} />} />
            <StatCard label="Identities" value={counts.identities.toLocaleString()} sublabel="raw emails + logins" accent="var(--chart-3)" icon={<AtSign size={14} />} />
            <StatCard label="Linked" value={counts.linked.toLocaleString()} sublabel="tied to a member" accent="var(--ok)" icon={<Link2 size={14} />} />
            <StatCard label="Not invited" value={counts.uninvited.toLocaleString()} sublabel="awaiting link / invite" accent="var(--chart-1)" icon={<AlertCircle size={14} />} />
          </div>
        </Reveal>
      )}

      {/* Toolbar */}
      <Reveal delay={0.08}>
        <div className="flex items-center gap-2 flex-wrap mb-3">
          <div className="relative flex-1 min-w-[220px]">
            <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--text-faint)]" />
            <input
              value={search}
              onChange={e => setSearch(e.target.value)}
              placeholder="Search name, email, or login…"
              aria-label="Search contributors"
              className="w-full pl-9 pr-3 py-2 rounded-[var(--radius-btn)] bg-[var(--bg-surface)] border border-[var(--border)] text-sm text-[var(--text)] placeholder-[var(--text-faint)] outline-none focus:border-[var(--brand-teal)] focus:ring-2 focus:ring-[var(--brand-teal)]/15 transition-all"
            />
          </div>
          <div className="inline-flex rounded-[var(--radius-btn)] border border-[var(--border)] bg-[var(--bg-surface)] p-0.5">
            {STATUS_FILTERS.map(f => (
              <button
                key={f.key}
                type="button"
                onClick={() => setStatusFilter(f.key)}
                className={[
                  'px-2.5 py-1 rounded-[6px] text-xs font-medium transition-colors',
                  statusFilter === f.key
                    ? 'bg-[var(--brand-teal)]/15 text-[var(--brand-teal)]'
                    : 'text-[var(--text-faint)] hover:text-[var(--text)]',
                ].join(' ')}
              >
                {f.label}
              </button>
            ))}
          </div>
          <button
            type="button"
            onClick={() => setShowExcluded(s => !s)}
            className={[
              'inline-flex items-center gap-1.5 px-2.5 py-2 rounded-[var(--radius-btn)] border text-xs font-medium transition-colors',
              showExcluded
                ? 'border-[var(--brand-teal)] text-[var(--brand-teal)] bg-[var(--brand-teal)]/8'
                : 'border-[var(--border)] text-[var(--text-faint)] hover:text-[var(--text)]',
            ].join(' ')}
            title="Excluded contributors are removed from contribution & analytics"
          >
            {showExcluded ? <Eye size={13} /> : <EyeOff size={13} />}
            {showExcluded ? 'Showing excluded' : 'Hide excluded'}
            {counts.excluded > 0 && <span className="font-mono opacity-70">({counts.excluded})</span>}
          </button>
        </div>
      </Reveal>

      {actionError && (
        <div role="alert" className="mb-3 flex items-center gap-2 px-3 py-2 rounded-[var(--radius-btn)] border border-[var(--bad)]/30 bg-[var(--bad)]/5 text-xs text-[var(--bad)]">
          <AlertCircle size={13} /> {actionError}
          <button type="button" onClick={() => setActionError(null)} className="ml-auto" aria-label="Dismiss"><X size={13} /></button>
        </div>
      )}

      {/* List */}
      <Reveal delay={0.1}>
        <Card padding="none" className="overflow-visible">
          <div className="px-4 sm:px-6 py-3 border-b border-[var(--border)] flex items-center justify-between gap-3">
            <h2 className="text-sm font-semibold text-[var(--text)] flex items-center gap-2">
              <Users size={15} className="text-[var(--text-faint)]" />
              Contributors
              {!loading && counts.total > 0 && (
                <span className="text-xs font-mono text-[var(--text-faint)]">
                  ({filtered.length}{filtered.length !== counts.total ? ` of ${counts.total}` : ''})
                </span>
              )}
            </h2>
            {loading && <Loader2 size={15} className="animate-spin text-[var(--brand-teal)]" />}
          </div>

          {/* Error */}
          {error && (
            <div className="px-6 py-4 text-sm text-[var(--bad)]">{error}</div>
          )}

          {/* Loading skeleton */}
          {loading && contributors.length === 0 && (
            <div>
              {Array.from({ length: 5 }).map((_, i) => (
                <div key={i} className="flex items-center gap-3 px-6 py-5 border-b border-[var(--border)] last:border-0 animate-pulse">
                  <div className="w-4 h-4 rounded bg-[var(--bg-surface3)]" />
                  <div className="w-9 h-9 rounded-full bg-[var(--bg-surface3)]" />
                  <div className="flex-1 space-y-2">
                    <div className="h-3 w-40 rounded bg-[var(--bg-surface3)]" />
                    <div className="h-2 w-56 rounded bg-[var(--bg-surface2)]" />
                  </div>
                  <div className="h-7 w-16 rounded bg-[var(--bg-surface2)]" />
                </div>
              ))}
            </div>
          )}

          {/* Not-ready / empty states */}
          {!loading && !error && contributors.length === 0 && (
            <div className="px-6 py-14 text-center">
              <span className="mx-auto mb-3 grid place-items-center w-11 h-11 rounded-full bg-[var(--brand-indigo)]/10 border border-[var(--brand-indigo)]/20">
                <Sparkles size={18} className="text-[var(--brand-indigo)]" />
              </span>
              <p className="text-sm font-medium text-[var(--text)]">
                {notReady ? 'No contributors grouped yet' : 'No contributors found'}
              </p>
              <p className="text-xs text-[var(--text-faint)] mt-1 max-w-sm mx-auto">
                {notReady
                  ? 'Run detection to cluster the git identities in your repos into people you can manage.'
                  : 'Once your repos are synced, run detection to group their git identities into people.'}
              </p>
              <Button
                className="mt-4 mx-auto"
                onClick={onDetect}
                disabled={detecting}
                leftIcon={detecting ? <Loader2 size={14} className="animate-spin" /> : <Sparkles size={14} />}
              >
                {detecting ? 'Detecting…' : 'Detect contributors'}
              </Button>
            </div>
          )}

          {/* Filtered-empty */}
          {!loading && contributors.length > 0 && filtered.length === 0 && (
            <div className="px-6 py-12 text-center">
              <Search size={18} className="mx-auto text-[var(--text-faint)] mb-2" />
              <p className="text-sm text-[var(--text-faint)]">No contributors match your filters.</p>
            </div>
          )}

          {filtered.map(c => (
            <ContributorRow
              key={c.id}
              c={c}
              members={members}
              membersLoading={membersLoading}
              selected={validSelectedIds.includes(c.id)}
              onToggleSelect={toggleSelect}
              onRename={(name) => onRename(c.id, name)}
              onLink={onLink}
              onInvite={onInvite}
              onToggleExclude={onToggleExclude}
              onSplit={onSplit}
              onSetDefault={onSetDefault}
              busy={busy}
            />
          ))}
        </Card>
      </Reveal>

      {/* Sticky merge bar */}
      <MergeBar
        selectedIds={validSelectedIds}
        contributors={contributors}
        onCancel={() => setSelectedIds([])}
        onMerge={onMerge}
        merging={merging}
      />
    </div>
  )
}

function Header({ orgName }) {
  return (
    <div className="mb-6 flex items-start gap-3">
      <span className="mt-0.5 grid place-items-center w-9 h-9 rounded-[var(--radius-btn)] bg-[var(--brand-teal)]/10 border border-[var(--brand-teal)]/20 shrink-0">
        <Users size={17} className="text-[var(--brand-teal)]" />
      </span>
      <div>
        <h1 className="font-display text-2xl font-semibold text-[var(--text)] tracking-tight">People</h1>
        <p className="text-sm text-[var(--text-faint)] mt-1">
          Manage contributor identities{orgName ? <> in <span className="text-[var(--text-dim)] font-medium">{orgName}</span></> : ''}.
          {' '}Link git authors to members, invite, exclude, merge or split.
        </p>
      </div>
    </div>
  )
}
