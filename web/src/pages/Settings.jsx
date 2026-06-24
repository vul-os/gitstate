import { useState, useEffect } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import {
  User, Building2, Plug, AlertTriangle, LogOut, Users, CreditCard,
  ChevronRight, Pencil, Check, Loader2,
  Settings as SettingsIcon,
} from 'lucide-react'
import { useAuth } from '../lib/useAuth.js'
import { useOrg } from '../lib/useOrg.js'
import { Badge, Button } from '../components/ui/index.js'
import { Reveal } from '../components/Reveal.jsx'
import { SectionCard } from '../components/SectionCard.jsx'
import { fetchProfile, patchProfile } from '../lib/api.js'

function FieldRow({ label, value, hint, action }) {
  return (
    <div className="flex items-center gap-4 py-3 border-b border-[var(--border)] last:border-0">
      <div className="flex-1 min-w-0">
        <p className="text-xs font-medium text-[var(--text-muted)]">{label}</p>
        {hint && <p className="text-xs text-[var(--text-faint)] mt-0.5">{hint}</p>}
      </div>
      <div className="text-sm font-mono text-[var(--text-dim)] truncate max-w-[200px]">{value ?? '—'}</div>
      {action ?? (
        <button className="flex items-center gap-1 text-xs text-[var(--brand-teal)] hover:text-[color-mix(in_srgb,var(--brand-teal)_75%,white)] transition-colors duration-150 shrink-0">
          <Pencil size={11} /> Edit
        </button>
      )}
    </div>
  )
}

function Avatar({ user }) {
  const initials = user?.name
    ? user.name.split(' ').map(w => w[0]).join('').slice(0, 2).toUpperCase()
    : user?.email?.slice(0, 2).toUpperCase() ?? '?'
  return (
    <div className="w-12 h-12 rounded-full bg-gradient-to-br from-[var(--brand-teal)] to-[var(--brand-indigo)] flex items-center justify-center text-sm font-bold text-[#0B1120] select-none shrink-0">
      {initials}
    </div>
  )
}

// AccountBody — the editable account block. Loads the authoritative profile from
// the API (the JWT email can be a `@users.noreply.*` placeholder when a user signed
// in with GitHub/GitLab and kept their email private) and, in that case, prompts
// for a real contact email used for notifications + billing receipts.
function AccountBody({ user, onSignOut }) {
  const [profile, setProfile] = useState(null)
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [saving, setSaving] = useState(false)
  const [msg, setMsg] = useState(null) // { ok: bool, text }

  useEffect(() => {
    fetchProfile()
      .then((p) => {
        setProfile(p)
        setName(p.name ?? '')
        setEmail(p.emailIsPlaceholder ? '' : (p.email ?? ''))
      })
      .catch(() => { /* fall back to JWT user below */ })
  }, [])

  const placeholder = profile?.emailIsPlaceholder
  const shownEmail = profile?.email ?? user?.email ?? ''

  async function save(e) {
    e.preventDefault()
    setMsg(null)
    const body = {}
    if (name.trim() && name.trim() !== (profile?.name ?? '')) body.name = name.trim()
    if (email.trim() && email.trim() !== (profile?.email ?? '')) body.email = email.trim()
    if (Object.keys(body).length === 0) { setMsg({ ok: true, text: 'Nothing changed.' }); return }
    setSaving(true)
    try {
      const updated = await patchProfile(body)
      setProfile(updated)
      setName(updated.name ?? '')
      setEmail(updated.emailIsPlaceholder ? '' : (updated.email ?? ''))
      setMsg({ ok: true, text: 'Saved.' })
    } catch (err) {
      setMsg({ ok: false, text: err.message ?? 'Could not save.' })
    } finally {
      setSaving(false)
    }
  }

  const inputCls = "w-full px-3 py-2 rounded-[var(--radius-btn)] bg-[var(--bg)] border border-[var(--border)] text-sm text-[var(--text)] placeholder-[var(--text-faint)] outline-none focus:border-[var(--brand-teal)] focus:ring-1 focus:ring-[var(--brand-teal)]/30 transition-all"

  return (
    <>
      <div className="flex items-center gap-4 pb-4 mb-4 border-b border-[var(--border)]">
        <Avatar user={user} />
        <div className="flex-1 min-w-0">
          <p className="text-sm font-semibold text-[var(--text)] truncate">{profile?.name || user?.name || 'Unknown'}</p>
          <p className="text-xs text-[var(--text-faint)] truncate mt-0.5">
            {placeholder ? 'No contact email yet' : shownEmail}
          </p>
          {user?.role && <Badge color="teal" className="mt-1.5">{user.role}</Badge>}
        </div>
        <Button
          variant="outline"
          size="sm"
          onClick={onSignOut}
          leftIcon={<LogOut size={13} />}
          className="hover:border-[color-mix(in_srgb,var(--bad)_30%,transparent)] hover:text-[var(--bad)] shrink-0"
        >
          Sign out
        </Button>
      </div>

      {placeholder && (
        <div className="flex items-start gap-2.5 mb-4 rounded-[var(--radius-btn)] px-3.5 py-3 bg-[color-mix(in_srgb,var(--warn)_10%,transparent)] border border-[color-mix(in_srgb,var(--warn)_30%,transparent)]">
          <AlertTriangle size={15} className="mt-0.5 shrink-0" style={{ color: 'var(--warn)' }} aria-hidden />
          <p className="text-xs text-[var(--text-muted)] leading-relaxed">
            Your git provider kept your email private, so we couldn't get one. Add a
            real contact email below so we can send notifications and billing receipts.
          </p>
        </div>
      )}

      <form onSubmit={save} className="space-y-3">
        <div>
          <label htmlFor="acct-name" className="block text-xs font-medium text-[var(--text-muted)] mb-1">Display name</label>
          <input id="acct-name" className={inputCls} value={name} onChange={(e) => setName(e.target.value)} placeholder="Your name" />
        </div>
        <div>
          <label htmlFor="acct-email" className="block text-xs font-medium text-[var(--text-muted)] mb-1">
            Contact email{placeholder ? '' : ''}
          </label>
          <input
            id="acct-email"
            type="email"
            className={inputCls}
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder={placeholder ? 'you@example.com' : (shownEmail || 'you@example.com')}
          />
          <p className="text-[11px] text-[var(--text-faint)] mt-1">Used for auth, notifications, and receipts.</p>
          <p className="text-[11px] text-[var(--text-faint)] mt-1">
            Changing your email keeps your linked git contributor — the link is by account, not by email,
            so your commit history stays attributed to you.
          </p>
        </div>
        <div className="flex items-center gap-3 pt-1">
          <Button type="submit" variant="primary" size="sm" disabled={saving}
            leftIcon={saving ? <Loader2 size={13} className="animate-spin" /> : <Check size={13} />}>
            {saving ? 'Saving…' : 'Save'}
          </Button>
          {msg && (
            <span className={`text-xs ${msg.ok ? 'text-[var(--ok)]' : 'text-[var(--bad)]'}`} role="status" aria-live="polite">
              {msg.text}
            </span>
          )}
        </div>
      </form>
    </>
  )
}

export default function Settings() {
  const { user, logout } = useAuth()
  const { activeOrg, orgRole } = useOrg()
  const navigate = useNavigate()

  async function handleSignOut() {
    await logout()
    navigate('/login', { replace: true })
  }

  return (
    <div className="w-full">
      <Reveal>
        <div className="mb-8 flex items-start gap-3">
          <span className="mt-0.5 grid place-items-center w-9 h-9 rounded-[var(--radius-btn)] bg-[var(--brand-teal)]/10 border border-[var(--brand-teal)]/20 shrink-0">
            <SettingsIcon size={17} className="text-[var(--brand-teal)]" />
          </span>
          <div>
            <h1 className="font-display text-2xl font-semibold text-[var(--text)] tracking-tight">Settings</h1>
            <p className="text-sm text-[var(--text-faint)] mt-1">Workspace and account preferences.</p>
          </div>
        </div>
      </Reveal>

      {/* Account section */}
      <SectionCard icon={User} title="Account" description="Your personal account details." delay={0.05} accent="var(--chart-1)">
        <AccountBody user={user} onSignOut={handleSignOut} />
      </SectionCard>

      <SectionCard
        icon={Building2}
        title="Organization"
        description={activeOrg ? `Active workspace: ${activeOrg.name}` : 'Your workspace settings.'}
        delay={0.1}
        accent="var(--chart-2)"
      >
        <FieldRow label="Name" value={activeOrg?.name ?? '—'} hint="Shown to team members and clients" />
        <FieldRow label="Slug" value={activeOrg?.slug ?? '—'} hint="URL prefix for your workspace" />
        <FieldRow
          label="Plan"
          value={activeOrg?.planKey ? activeOrg.planKey.charAt(0).toUpperCase() + activeOrg.planKey.slice(1) : 'Free'}
          hint="Manage your plan and invoices"
          action={
            <Link to="/settings/billing" className="flex items-center gap-1 text-xs text-[var(--brand-teal)] hover:text-[color-mix(in_srgb,var(--brand-teal)_75%,white)] transition-colors duration-150 shrink-0">
              <CreditCard size={11} /> Billing
            </Link>
          }
        />
        <FieldRow label="Your role" value={orgRole ?? '—'} hint="Your permission level in this org" />
        <div className="flex items-center justify-between py-3">
          <div className="flex items-start gap-2.5">
            <Users size={15} className="mt-0.5 text-[var(--text-faint)] shrink-0" />
            <div>
              <p className="text-xs font-medium text-[var(--text-muted)]">Members</p>
              <p className="text-xs text-[var(--text-faint)] mt-0.5">Invite teammates and clients (stakeholders are free)</p>
            </div>
          </div>
          <Link to="/settings/members">
            <Button variant="outline" size="sm" rightIcon={<ChevronRight size={13} />}>Manage members</Button>
          </Link>
        </div>
      </SectionCard>

      <SectionCard
        icon={Plug}
        title="Integrations"
        description="Connect gitstate to your external tools — accounting, calendars, notifications, webhooks, API tokens and AI."
        delay={0.15}
        accent="var(--chart-3)"
      >
        <div className="flex items-center justify-between py-2">
          <div className="flex items-start gap-2.5">
            <Plug size={15} className="mt-0.5 text-[var(--text-faint)] shrink-0" />
            <div>
              <p className="text-xs font-medium text-[var(--text-muted)]">External services</p>
              <p className="text-xs text-[var(--text-faint)] mt-0.5">Xero / QuickBooks, Google / Outlook calendars, Slack &amp; webhooks, API tokens, and your AI provider key.</p>
            </div>
          </div>
          <Link to="/integrations">
            <Button variant="outline" size="sm" rightIcon={<ChevronRight size={13} />}>Open Integrations</Button>
          </Link>
        </div>
      </SectionCard>

      <SectionCard icon={AlertTriangle} title="Danger zone" description="Irreversible actions." delay={0.25} tone="danger">
        <div className="flex items-center justify-between py-2">
          <div>
            <p className="text-sm text-[var(--text)]">Delete organization</p>
            <p className="text-xs text-[var(--text-faint)] mt-0.5">Permanently deletes the workspace and all data.</p>
          </div>
          <Button variant="danger" size="sm">Delete</Button>
        </div>
      </SectionCard>
    </div>
  )
}
