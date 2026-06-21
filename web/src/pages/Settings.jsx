import { useState, useEffect } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import {
  User, Building2, Plug, AlertTriangle, LogOut, Users, CreditCard,
  ChevronRight, Pencil, Sparkles, KeyRound, Server, Check, Loader2,
  CalendarDays, Link2, Unlink, ArrowUpFromLine, ArrowDownToLine, Bell,
  Webhook, Copy, RefreshCw, Rocket, Eye, EyeOff, CircleDot,
  Settings as SettingsIcon,
} from 'lucide-react'
import { useAuth } from '../lib/useAuth.js'
import { useOrg } from '../lib/useOrg.js'
import { useWebhooks } from '../lib/useWebhooks.js'
import { Card, Badge, Button } from '../components/ui/index.js'
import { Reveal } from '../components/Reveal.jsx'
import { NotificationsBody } from '../components/notifications/NotificationsSection.jsx'
import { ApiTokensBody } from '../components/settings/ApiTokens.jsx'
import {
  get, put,
  calendarStartUrl, fetchCalendarStatus, patchCalendar, disconnectCalendar,
} from '../lib/api.js'

function SectionCard({ icon: Icon, title, description, children, delay = 0, tone = 'default', accent = 'var(--brand-teal)' }) {
  const isDanger = tone === 'danger'
  const chipColor = isDanger ? 'var(--bad)' : accent
  return (
    <Reveal delay={delay}>
      <Card
        padding="lg"
        className="mb-4"
        style={isDanger ? { borderColor: 'color-mix(in srgb, var(--bad) 30%, transparent)' } : undefined}
      >
        <div className="mb-4 flex items-start gap-2.5">
          {Icon && (
            <span
              className="grid place-items-center w-7 h-7 rounded-[6px] shrink-0"
              style={{ color: chipColor, background: `color-mix(in srgb, ${chipColor} 14%, transparent)` }}
            >
              <Icon size={15} />
            </span>
          )}
          <div>
            <h2 className="text-sm font-semibold text-[var(--text)]">{title}</h2>
            {description && <p className="text-xs text-[var(--text-faint)] mt-0.5">{description}</p>}
          </div>
        </div>
        {children}
      </Card>
    </Reveal>
  )
}

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

// ModeOption — a selectable BYOK-vs-managed card.
function ModeOption({ icon: Icon, title, blurb, selected, disabled, onSelect }) {
  return (
    <button
      type="button"
      onClick={disabled ? undefined : onSelect}
      disabled={disabled}
      className={[
        'flex-1 text-left rounded-[var(--radius-btn)] border p-4 transition-all duration-150',
        disabled ? 'opacity-40 cursor-not-allowed' : 'cursor-pointer',
        selected
          ? 'border-[var(--brand-teal)] bg-[var(--brand-teal)]/5 shadow-[0_0_18px_rgba(45,212,191,0.12)]'
          : 'border-[var(--border)] hover:border-[var(--border2)]',
      ].join(' ')}
    >
      <div className="flex items-center gap-2">
        <Icon size={15} className={selected ? 'text-[var(--brand-teal)]' : 'text-[var(--text-faint)]'} />
        <span className="text-sm font-semibold text-[var(--text)]">{title}</span>
        {selected && <Check size={14} className="ml-auto text-[var(--brand-teal)]" />}
      </div>
      <p className="text-xs text-[var(--text-faint)] mt-2 leading-relaxed">{blurb}</p>
    </button>
  )
}

// LLMSettingsSection — choose BYOK (bring your own provider key → $0 managed
// cost) vs managed (platform key; metered at the model's standard rate beyond
// the included monthly AI credit — no per-seat AI fee, no markup).
function LLMSettingsSection({ delay }) {
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState(null)
  const [saved, setSaved] = useState(false)

  const [mode, setMode] = useState('managed')
  const [provider, setProvider] = useState('anthropic')
  const [model, setModel] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [hasKey, setHasKey] = useState(false)
  const [managedAvailable, setManagedAvailable] = useState(true)

  useEffect(() => {
    let active = true
    get('/api/settings/llm')
      .then((s) => {
        if (!active) return
        setMode(s.mode ?? 'managed')
        setProvider(s.provider ?? 'anthropic')
        setModel(s.model ?? '')
        setHasKey(Boolean(s.hasKey))
        setManagedAvailable(Boolean(s.managedAvailable))
      })
      .catch((e) => active && setError(e.message ?? 'Failed to load LLM settings'))
      .finally(() => active && setLoading(false))
    return () => { active = false }
  }, [])

  async function save() {
    setSaving(true)
    setError(null)
    setSaved(false)
    try {
      const body = { mode, provider, model: model.trim() || undefined }
      if (mode === 'byok' && apiKey.trim()) body.apiKey = apiKey.trim()
      const s = await put('/api/settings/llm', body)
      setMode(s.mode)
      setProvider(s.provider)
      setModel(s.model ?? '')
      setHasKey(Boolean(s.hasKey))
      setApiKey('')
      setSaved(true)
      setTimeout(() => setSaved(false), 2500)
    } catch (e) {
      setError(e.message ?? 'Failed to save')
    } finally {
      setSaving(false)
    }
  }

  return (
    <SectionCard
      icon={Sparkles}
      title="AI & LLM"
      description="How effort estimates and status summaries are powered."
      delay={delay}
      accent="var(--chart-5)"
    >
      {loading ? (
        <div className="flex items-center gap-2 py-4 text-xs text-[var(--text-faint)]">
          <Loader2 size={14} className="animate-spin" /> Loading…
        </div>
      ) : (
        <div className="space-y-4">
          <div className="flex flex-col sm:flex-row gap-3">
            <ModeOption
              icon={Server}
              title="Managed"
              selected={mode === 'managed'}
              disabled={!managedAvailable}
              onSelect={() => setMode('managed')}
              blurb={
                managedAvailable
                  ? 'Use the gitstate platform key. Run any model at its standard rate — no per-seat AI fee. AI is included up to each builder\'s monthly credit, then metered at the model\'s standard provider rate.'
                  : 'Unavailable — this server has no platform LLM key. Bring your own key instead.'
              }
            />
            <ModeOption
              icon={KeyRound}
              title="Bring your own key"
              selected={mode === 'byok'}
              onSelect={() => setMode('byok')}
              blurb="Bring your own provider API key. AI calls go direct to your provider and you pay them at their rate — nothing managed AI on your gitstate invoice."
            />
          </div>

          {mode === 'byok' && (
            <div className="space-y-3 rounded-[var(--radius-btn)] border border-[var(--border)] bg-[var(--bg)] p-4">
              <div>
                <label className="block text-xs font-medium text-[var(--text-muted)] mb-1.5">Provider</label>
                <select
                  value={provider}
                  onChange={(e) => setProvider(e.target.value)}
                  className="w-full rounded-[var(--radius-btn)] border border-[var(--border2)] bg-[var(--bg-surface2)] px-3 py-2 text-sm text-[var(--text)] focus:border-[var(--brand-teal)] focus:outline-none"
                >
                  <option value="anthropic">Anthropic (Claude)</option>
                </select>
              </div>

              <div>
                <label className="block text-xs font-medium text-[var(--text-muted)] mb-1.5">
                  API key {hasKey && <span className="text-[var(--brand-teal)]">•••• saved</span>}
                </label>
                <input
                  type="password"
                  value={apiKey}
                  onChange={(e) => setApiKey(e.target.value)}
                  placeholder={hasKey ? 'Enter a new key to replace the saved one' : 'sk-ant-…'}
                  autoComplete="off"
                  className="w-full font-mono rounded-[var(--radius-btn)] border border-[var(--border2)] bg-[var(--bg-surface2)] px-3 py-2 text-sm text-[var(--text)] placeholder:text-[var(--text-faint)] focus:border-[var(--brand-teal)] focus:outline-none"
                />
                <p className="text-xs text-[var(--text-faint)] mt-1">Write-only. Stored encrypted; never shown again.</p>
              </div>

              <div>
                <label className="block text-xs font-medium text-[var(--text-muted)] mb-1.5">Model <span className="text-[var(--text-faint)]">(optional)</span></label>
                <input
                  type="text"
                  value={model}
                  onChange={(e) => setModel(e.target.value)}
                  placeholder="claude-sonnet-4-6"
                  className="w-full font-mono rounded-[var(--radius-btn)] border border-[var(--border2)] bg-[var(--bg-surface2)] px-3 py-2 text-sm text-[var(--text)] placeholder:text-[var(--text-faint)] focus:border-[var(--brand-teal)] focus:outline-none"
                />
              </div>
            </div>
          )}

          {error && <p className="text-xs text-[var(--bad)]">{error}</p>}

          <div className="flex items-center gap-3">
            <Button
              variant="primary"
              size="sm"
              onClick={save}
              disabled={saving || (mode === 'byok' && !hasKey && !apiKey.trim())}
              leftIcon={saving ? <Loader2 size={13} className="animate-spin" /> : saved ? <Check size={13} /> : undefined}
            >
              {saving ? 'Saving…' : saved ? 'Saved' : 'Save AI settings'}
            </Button>
            {mode === 'managed' && managedAvailable && (
              <span className="text-xs text-[var(--text-faint)]">Metered at the model&apos;s standard rate only beyond your included AI credit.</span>
            )}
          </div>
        </div>
      )}
    </SectionCard>
  )
}

// Inline brand marks (no extra deps).
function GoogleCalendarMark() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" aria-hidden>
      <rect x="4" y="4" width="16" height="16" rx="2" fill="#fff" stroke="#dadce0" />
      <path d="M4 6a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v2H4V6z" fill="#4285F4" />
      <path d="M16 12.6c0 .9-.7 1.6-1.8 1.6-.9 0-1.6-.5-1.8-1.2l.9-.4c.1.4.4.7.9.7.4 0 .8-.2.8-.7s-.4-.7-.9-.7h-.4v-.8h.4c.4 0 .7-.2.7-.6 0-.3-.3-.6-.7-.6-.4 0-.6.2-.8.6l-.9-.4c.3-.7.9-1 1.7-1 1 0 1.7.6 1.7 1.4 0 .5-.2.9-.6 1.1.5.2.8.6.8 1.2z" fill="#4285F4" />
    </svg>
  )
}

function OutlookMark() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" aria-hidden>
      <rect x="9" y="4" width="11" height="16" rx="1.5" fill="#fff" stroke="#dadce0" />
      <rect x="11" y="6" width="7" height="2" fill="#0F6CBD" opacity=".5" />
      <rect x="11" y="9" width="7" height="2" fill="#0F6CBD" opacity=".5" />
      <rect x="2" y="6" width="11" height="12" rx="2.5" fill="#0F6CBD" />
      <path d="M7.5 9.2c-1.5 0-2.5 1.2-2.5 2.8s1 2.8 2.5 2.8S10 13.6 10 12s-1-2.8-2.5-2.8zm0 4.6c-.8 0-1.3-.8-1.3-1.8s.5-1.8 1.3-1.8 1.3.8 1.3 1.8-.5 1.8-1.3 1.8z" fill="#fff" />
    </svg>
  )
}

// CalendarRow — one provider's connect/disconnect + push/pull toggles.
function CalendarRow({ status, onChanged, brand, label, border }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(null)
  const connected = status?.connected
  const configured = status?.configured

  function connect() {
    const url = calendarStartUrl(status.provider)
    if (url) window.location.href = url
  }

  async function disconnect() {
    setBusy(true); setError(null)
    try {
      await disconnectCalendar(status.provider)
      onChanged()
    } catch (e) {
      setError(e.message ?? 'Failed to disconnect')
    } finally {
      setBusy(false)
    }
  }

  async function toggle(field) {
    setBusy(true); setError(null)
    try {
      await patchCalendar(status.provider, { [field]: !status[field] })
      onChanged()
    } catch (e) {
      setError(e.message ?? 'Failed to update')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className={`py-3 ${border ? 'border-t border-[var(--border)]' : ''}`}>
      <div className="flex items-center gap-3">
        <div className="w-9 h-9 rounded-[var(--radius-btn)] bg-[var(--bg)] border border-[var(--border)] flex items-center justify-center shrink-0">
          {brand}
        </div>
        <div className="flex-1 min-w-0">
          <p className="text-sm text-[var(--text)]">{label}</p>
          {!configured ? (
            <p className="text-xs text-[var(--text-faint)]">Not configured on this server</p>
          ) : connected ? (
            <p className="text-xs text-[var(--text-faint)] truncate">{status.email || 'Connected'}</p>
          ) : (
            <p className="text-xs text-[var(--text-faint)]">Sync leave &amp; availability two ways</p>
          )}
        </div>
        {configured && (connected ? (
          <Button
            variant="outline" size="sm" onClick={disconnect} disabled={busy}
            leftIcon={busy ? <Loader2 size={13} className="animate-spin" /> : <Unlink size={13} />}
            className="hover:border-[color-mix(in_srgb,var(--bad)_30%,transparent)] hover:text-[var(--bad)] shrink-0"
          >
            Disconnect
          </Button>
        ) : (
          <Button
            variant="outline" size="sm" onClick={connect}
            leftIcon={<Link2 size={13} />} className="shrink-0"
          >
            Connect
          </Button>
        ))}
      </div>

      {connected && (
        <div className="mt-3 ml-12 flex flex-col sm:flex-row gap-2">
          <ToggleChip
            active={status.pushLeave} disabled={busy} onClick={() => toggle('pushLeave')}
            icon={ArrowUpFromLine} label="Push approved leave"
          />
          <ToggleChip
            active={status.pullBusy} disabled={busy} onClick={() => toggle('pullBusy')}
            icon={ArrowDownToLine} label="Pull busy into availability"
          />
        </div>
      )}

      {error && <p className="text-xs text-[var(--bad)] mt-2 ml-12">{error}</p>}
    </div>
  )
}

function ToggleChip({ active, disabled, onClick, icon: Icon, label }) {
  return (
    <button
      type="button" onClick={onClick} disabled={disabled}
      className={[
        'flex items-center gap-1.5 rounded-full border px-3 py-1.5 text-xs transition-all duration-150',
        disabled ? 'opacity-50 cursor-not-allowed' : 'cursor-pointer',
        active
          ? 'border-[var(--brand-teal)] bg-[var(--brand-teal)]/10 text-[var(--brand-teal)]'
          : 'border-[var(--border)] text-[var(--text-faint)] hover:border-[var(--border2)]',
      ].join(' ')}
    >
      <Icon size={12} />
      {label}
      {active && <Check size={12} className="ml-0.5" />}
    </button>
  )
}

// CalendarSection — connect/disconnect Google & Microsoft calendars and control
// the two-way (push leave / pull busy) sync per provider.
function CalendarSection({ delay }) {
  const [statuses, setStatuses] = useState(null)
  const [error, setError] = useState(null)

  function reload() {
    fetchCalendarStatus()
      .then(setStatuses)
      .catch((e) => setError(e.message ?? 'Failed to load calendars'))
  }

  useEffect(() => {
    let active = true
    fetchCalendarStatus()
      .then((s) => active && setStatuses(s))
      .catch((e) => active && setError(e.message ?? 'Failed to load calendars'))
    return () => { active = false }
  }, [])

  // Defensively coerce to an array — a stray HTML / non-JSON 200 response must
  // never throw `statuses?.find is not a function` and blank the page.
  const statusList = Array.isArray(statuses) ? statuses : []
  const google = statusList.find((s) => s.provider === 'google')
  const microsoft = statusList.find((s) => s.provider === 'microsoft')
  const anyConfigured = google?.configured || microsoft?.configured

  return (
    <SectionCard
      icon={CalendarDays}
      title="Calendars"
      description="Two-way sync: approved leave becomes an out-of-office event; calendar busy time feeds your availability."
      delay={delay}
      accent="var(--chart-6)"
    >
      {!statuses ? (
        <div className="flex items-center gap-2 py-4 text-xs text-[var(--text-faint)]">
          <Loader2 size={14} className="animate-spin" /> Loading…
        </div>
      ) : !anyConfigured ? (
        <div className="py-2 text-xs text-[var(--text-faint)] space-y-1.5">
          <p>No calendar provider is configured on this server. To enable two-way calendar sync, set OAuth credentials in the server environment and restart:</p>
          <ul className="space-y-1 font-mono text-[11px] text-[var(--text-muted)]">
            <li>· <span className="text-[var(--brand-teal)]">OAUTH_GOOGLE_CLIENT_ID</span> / <span className="text-[var(--brand-teal)]">OAUTH_GOOGLE_CLIENT_SECRET</span></li>
            <li>· <span className="text-[var(--brand-indigo)]">OAUTH_MICROSOFT_CLIENT_ID</span> / <span className="text-[var(--brand-indigo)]">OAUTH_MICROSOFT_CLIENT_SECRET</span></li>
          </ul>
          <p>The calendar flow reuses your existing Google/Microsoft sign-in app — just add the calendar scopes.</p>
        </div>
      ) : (
        <>
          <CalendarRow
            status={google ?? { provider: 'google', configured: false }}
            onChanged={reload} brand={<GoogleCalendarMark />} label="Google Calendar" border={false}
          />
          <CalendarRow
            status={microsoft ?? { provider: 'microsoft', configured: false }}
            onChanged={reload} brand={<OutlookMark />} label="Microsoft / Outlook" border
          />
        </>
      )}
      {error && <p className="text-xs text-[var(--bad)] mt-2">{error}</p>}
    </SectionCard>
  )
}

// ── Webhooks & CI/CD ─────────────────────────────────────────────────────────

function CopyField({ label, value }) {
  const [copied, setCopied] = useState(false)
  async function copy() {
    if (!value) return
    try {
      await navigator.clipboard.writeText(value)
      setCopied(true)
      setTimeout(() => setCopied(false), 1600)
    } catch { /* clipboard blocked — ignore */ }
  }
  return (
    <div>
      {label && <label className="block text-[11px] font-medium text-[var(--text-muted)] mb-1">{label}</label>}
      <div className="flex items-center gap-2">
        <code className="flex-1 min-w-0 truncate rounded-[var(--radius-btn)] border border-[var(--border)] bg-[var(--bg)] px-3 py-2 text-xs text-[var(--text-dim)] font-mono">
          {value || '—'}
        </code>
        <Button variant="outline" size="sm" onClick={copy} disabled={!value}
          leftIcon={copied ? <Check size={13} className="text-[var(--brand-teal)]" /> : <Copy size={13} />}
          className="shrink-0">
          {copied ? 'Copied' : 'Copy'}
        </Button>
      </div>
    </div>
  )
}

function lastEventLabel(iso) {
  if (!iso) return null
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return null
  const mins = Math.floor((Date.now() - d.getTime()) / 60000)
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins}m ago`
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return `${hrs}h ago`
  return `${Math.floor(hrs / 24)}d ago`
}

const GITHUB_SETUP = [
  'In your repo → Settings → Webhooks → Add webhook.',
  'Paste the Payload URL above (it already carries your org id).',
  'Content type: application/json.',
  'Paste the Secret into the “Secret” field (used for HMAC-SHA256 signing).',
  'Select events: Pushes, Pull requests, Issues, Deployment statuses, Workflow runs.',
]
const GITLAB_SETUP = [
  'In your project → Settings → Webhooks.',
  'Paste the URL above.',
  'Paste the Secret into the “Secret token” field.',
  'Enable triggers: Push, Merge request, Issues, Pipeline, Deployment events.',
]

function WebhookProviderRow({ p, brand, label, setup, rotating, onRotate, revealed }) {
  const [showSecret, setShowSecret] = useState(false)
  const last = lastEventLabel(p.lastEventAt)
  return (
    <div className="py-4 border-t border-[var(--border)] first:border-t-0 first:pt-0 space-y-3">
      <div className="flex items-center gap-3">
        <div className="w-9 h-9 rounded-[var(--radius-btn)] bg-[var(--bg)] border border-[var(--border)] flex items-center justify-center shrink-0">
          {brand}
        </div>
        <div className="flex-1 min-w-0">
          <p className="text-sm text-[var(--text)]">{label}</p>
          <div className="flex items-center gap-2 mt-0.5">
            {p.secretSet ? (
              <Badge color="teal">configured</Badge>
            ) : (
              <span className="text-xs text-[var(--text-faint)]">not configured</span>
            )}
            {last ? (
              <span className="inline-flex items-center gap-1 text-[11px] text-[var(--text-faint)]">
                <CircleDot size={10} className="text-[var(--brand-teal)]" /> last event {last}
              </span>
            ) : p.secretSet ? (
              <span className="text-[11px] text-[var(--text-faint)]">no events received yet</span>
            ) : null}
          </div>
        </div>
        <Button variant="outline" size="sm" onClick={() => onRotate(p.provider)} disabled={rotating}
          leftIcon={rotating ? <Loader2 size={13} className="animate-spin" /> : <RefreshCw size={13} />}
          className="shrink-0">
          {p.secretSet ? 'Rotate secret' : 'Generate secret'}
        </Button>
      </div>

      <CopyField label="Payload URL" value={p.payloadUrl} />

      {revealed && (
        <div className="rounded-[var(--radius-btn)] border border-[var(--brand-teal)]/30 bg-[var(--brand-teal)]/[0.05] p-3 space-y-2">
          <div className="flex items-center gap-2 text-[11px] text-[var(--brand-teal)] font-medium">
            <KeyRound size={12} /> New secret — copy it now, it won’t be shown again.
          </div>
          <div className="flex items-center gap-2">
            <code className="flex-1 min-w-0 truncate rounded-[var(--radius-btn)] border border-[var(--border)] bg-[var(--bg)] px-3 py-2 text-xs text-[var(--text)] font-mono">
              {showSecret ? revealed : '•'.repeat(Math.min(revealed.length, 48))}
            </code>
            <button type="button" onClick={() => setShowSecret(v => !v)}
              className="p-2 text-[var(--text-faint)] hover:text-[var(--text)] transition-colors shrink-0">
              {showSecret ? <EyeOff size={14} /> : <Eye size={14} />}
            </button>
          </div>
          <CopyField value={revealed} />
        </div>
      )}

      <details className="group">
        <summary className="cursor-pointer text-[11px] text-[var(--text-faint)] hover:text-[var(--text-dim)] transition-colors list-none flex items-center gap-1">
          <ChevronRight size={12} className="transition-transform group-open:rotate-90" /> Setup instructions
        </summary>
        <ol className="mt-2 ml-1 space-y-1 text-[11px] text-[var(--text-faint)] list-decimal list-inside">
          {setup.map((s, i) => <li key={i}>{s}</li>)}
        </ol>
      </details>
    </div>
  )
}

function WebhooksSection({ delay }) {
  const { data, loading, error, rotate } = useWebhooks()
  const [rotating, setRotating] = useState('')
  const [revealed, setRevealed] = useState({}) // provider → one-time secret
  const [rotateErr, setRotateErr] = useState(null)

  async function onRotate(provider) {
    setRotating(provider); setRotateErr(null)
    try {
      const res = await rotate(provider)
      setRevealed(r => ({ ...r, [provider]: res.secret }))
    } catch (e) {
      setRotateErr(e.message ?? 'Failed to generate secret')
    } finally {
      setRotating('')
    }
  }

  const providers = Array.isArray(data?.providers) ? data.providers : []
  const github = providers.find(p => p.provider === 'github')
  const gitlab = providers.find(p => p.provider === 'gitlab')

  return (
    <SectionCard
      icon={Webhook}
      title="Webhooks & CI/CD"
      description="Real-time sync (push/PR/issue) and CI/CD deploys → real DORA deploy frequency & MTTR on Engineering Health."
      delay={delay}
      accent="var(--chart-4)"
    >
      {loading && !data ? (
        <div className="flex items-center gap-2 py-4 text-xs text-[var(--text-faint)]">
          <Loader2 size={14} className="animate-spin" /> Loading…
        </div>
      ) : error ? (
        <p className="text-xs text-[var(--bad)] py-2">{error}</p>
      ) : (
        <>
          <div className="flex items-start gap-2 rounded-[var(--radius-btn)] border border-[var(--border)] bg-[var(--bg)] p-3 mb-1 text-[11px] text-[var(--text-faint)] leading-relaxed">
            <Rocket size={13} className="mt-0.5 text-[var(--brand-teal)] shrink-0" />
            <span>
              Point your provider at the payload URL and set the secret. Commits, PRs and issues
              sync the moment they happen — no polling. Deployment & pipeline events become real
              deploys (and failures open incidents), powering true DORA deploy frequency and MTTR.
            </span>
          </div>
          {github && (
            <WebhookProviderRow
              p={github} label="GitHub" setup={GITHUB_SETUP}
              rotating={rotating === 'github'} onRotate={onRotate} revealed={revealed.github}
              brand={
                <svg width="17" height="17" viewBox="0 0 24 24" fill="var(--text)" aria-hidden>
                  <path d="M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12" />
                </svg>
              }
            />
          )}
          {gitlab && (
            <WebhookProviderRow
              p={gitlab} label="GitLab" setup={GITLAB_SETUP}
              rotating={rotating === 'gitlab'} onRotate={onRotate} revealed={revealed.gitlab}
              brand={
                <svg width="17" height="17" viewBox="0 0 380 380" fill="none">
                  <path d="M282.8 170.3L195.5 7.7C193.3 3 189 0 184.2 0s-9.1 3-11.3 7.7L97 156.2l187.8-.6-2 14.7z" fill="#e24329" />
                  <path d="M97 156.2L9.7 318.8c-2.2 4.7-.8 10.3 3.4 13.4 2 1.5 4.4 2.3 6.8 2.3 2.6 0 5.2-.9 7.2-2.7l157.1-131.9L97 156.2z" fill="#fc6d26" />
                  <path d="M282.8 170.3l-98.6-.9 15.1 35.2 81.8 51.2L282.8 170.3z" fill="#e24329" />
                  <path d="M280.1 319.8l-96.4-120.1-86.4 100.3 90.4 75.9c4.1 3.4 9.9 3.4 14 0l78.4-56.1z" fill="#fc6d26" />
                </svg>
              }
            />
          )}
          {rotateErr && <p className="text-xs text-[var(--bad)] mt-2">{rotateErr}</p>}
        </>
      )}
    </SectionCard>
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
        <div className="flex items-center gap-4 pb-4 mb-2 border-b border-[var(--border)]">
          <Avatar user={user} />
          <div className="flex-1 min-w-0">
            <p className="text-sm font-semibold text-[var(--text)] truncate">{user?.name ?? 'Unknown'}</p>
            <p className="text-xs text-[var(--text-faint)] truncate mt-0.5">{user?.email ?? ''}</p>
            {user?.role && <Badge color="teal" className="mt-1.5">{user.role}</Badge>}
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={handleSignOut}
            leftIcon={<LogOut size={13} />}
            className="hover:border-[color-mix(in_srgb,var(--bad)_30%,transparent)] hover:text-[var(--bad)] shrink-0"
          >
            Sign out
          </Button>
        </div>

        <FieldRow label="Display name" value={user?.name} hint="Shown on commits and mentions" />
        <FieldRow label="Email" value={user?.email} hint="Used for auth and notifications" />
        <FieldRow label="Password" value="••••••••" hint="Change your password" />
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

      <LLMSettingsSection delay={0.15} />

      <SectionCard icon={Plug} title="Integrations" description="Connected git platforms." delay={0.2} accent="var(--chart-3)">
        <div className="flex items-center gap-3 py-3">
          <div className="w-9 h-9 rounded-[var(--radius-btn)] bg-[var(--bg)] border border-[var(--border)] flex items-center justify-center shrink-0">
            <svg width="17" height="17" viewBox="0 0 24 24" fill="var(--text)" aria-hidden>
              <path d="M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12" />
            </svg>
          </div>
          <div className="flex-1 min-w-0">
            <p className="text-sm text-[var(--text)]">GitHub</p>
            <p className="text-xs text-[var(--text-faint)]">Connect from the Repositories page</p>
          </div>
          <Link to="/repos">
            <Button variant="outline" size="sm">Connect</Button>
          </Link>
        </div>
        <div className="flex items-center gap-3 py-3 border-t border-[var(--border)]">
          <div className="w-9 h-9 rounded-[var(--radius-btn)] bg-[var(--bg)] border border-[var(--border)] flex items-center justify-center shrink-0">
            <svg width="17" height="17" viewBox="0 0 380 380" fill="none">
              <path d="M282.8 170.3L195.5 7.7C193.3 3 189 0 184.2 0s-9.1 3-11.3 7.7L97 156.2l187.8-.6-2 14.7z" fill="#e24329" />
              <path d="M97 156.2L9.7 318.8c-2.2 4.7-.8 10.3 3.4 13.4 2 1.5 4.4 2.3 6.8 2.3 2.6 0 5.2-.9 7.2-2.7l157.1-131.9L97 156.2z" fill="#fc6d26" />
              <path d="M282.8 170.3l-98.6-.9 15.1 35.2 81.8 51.2L282.8 170.3z" fill="#e24329" />
              <path d="M280.1 319.8l-96.4-120.1-86.4 100.3 90.4 75.9c4.1 3.4 9.9 3.4 14 0l78.4-56.1z" fill="#fc6d26" />
            </svg>
          </div>
          <div className="flex-1 min-w-0">
            <p className="text-sm text-[var(--text)]">GitLab</p>
            <p className="text-xs text-[var(--text-faint)]">Connect from the Repositories page</p>
          </div>
          <Link to="/repos">
            <Button variant="outline" size="sm">Connect</Button>
          </Link>
        </div>
      </SectionCard>

      <CalendarSection delay={0.22} />

      <SectionCard
        icon={Bell}
        title="Notifications"
        description="Push evidence-based status to where your team works — Slack, a webhook, or email."
        delay={0.24}
        accent="var(--info)"
      >
        <NotificationsBody />
      </SectionCard>

      <WebhooksSection delay={0.245} />

      <SectionCard
        icon={KeyRound}
        title="API tokens"
        description="Personal access tokens for agents, the gittrack CLI and MCP integrations."
        delay={0.248}
        accent="var(--chart-5)"
      >
        <ApiTokensBody />
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
