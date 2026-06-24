import { useState } from 'react'
import {
  Bell, Plus, Loader2, MessageSquare, Webhook, Mail, Send, Trash2, Pencil,
  Power, AlertTriangle,
} from 'lucide-react'
import { Badge, Button } from '../ui/index.js'
import {
  useChannels, createChannel, updateChannel, deleteChannel, testSendChannel,
} from '../../lib/useNotifications.js'
import { ChannelForm } from './ChannelForm.jsx'
import { DigestPreview } from './DigestPreview.jsx'
import { DiscordIcon, GoogleChatIcon, TeamsIcon } from './channelIcons.jsx'

const KIND_META = {
  slack: { icon: MessageSquare, label: 'Slack' },
  discord: { icon: DiscordIcon, label: 'Discord' },
  google_chat: { icon: GoogleChatIcon, label: 'Google Chat' },
  teams: { icon: TeamsIcon, label: 'Microsoft Teams' },
  webhook: { icon: Webhook, label: 'Webhook' },
  email: { icon: Mail, label: 'Email' },
}

const DIGEST_LABEL = { weeklyStatus: 'Weekly', stalePRs: 'Stale PRs', ooo: 'OOO' }

// TestResultBadge — renders the per-digest outcome of a test send.
function TestResults({ results }) {
  if (!results || results.length === 0) return null
  return (
    <div className="mt-2 flex flex-wrap gap-1.5">
      {results.map((res, i) => {
        const color = res.status === 'sent' ? 'green' : res.status === 'skipped' ? 'yellow' : 'red'
        return (
          <Badge key={i} color={color} title={res.detail || ''}>
            {DIGEST_LABEL[res.kind] ?? res.kind}: {res.status}
          </Badge>
        )
      })}
    </div>
  )
}

// ChannelRow — one channel with enable toggle, test send, edit, delete.
function ChannelRow({ channel, emailConfigured, onChanged, border }) {
  const [busy, setBusy] = useState(false)
  const [editing, setEditing] = useState(false)
  const [error, setError] = useState(null)
  const [testResults, setTestResults] = useState(null)

  const meta = KIND_META[channel.kind] ?? KIND_META.webhook
  const Icon = meta.icon
  const emailBlocked = channel.kind === 'email' && !emailConfigured
  const enabledDigests = Object.entries(channel.digests || {})
    .filter(([, v]) => v)
    .map(([k]) => DIGEST_LABEL[k] ?? k)

  async function toggleEnabled() {
    setBusy(true); setError(null)
    try {
      await updateChannel(channel.id, { enabled: !channel.enabled })
      onChanged()
    } catch (e) {
      setError(e?.message ?? 'Failed to update')
    } finally {
      setBusy(false)
    }
  }

  async function runTest() {
    setBusy(true); setError(null); setTestResults(null)
    try {
      const res = await testSendChannel(channel.id)
      setTestResults(res?.results ?? [])
    } catch (e) {
      setError(e?.message ?? 'Test send failed')
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    setBusy(true); setError(null)
    try {
      await deleteChannel(channel.id)
      onChanged()
    } catch (e) {
      setError(e?.message ?? 'Failed to delete')
      setBusy(false)
    }
  }

  async function save(body) {
    const updated = await updateChannel(channel.id, body)
    setEditing(false)
    onChanged()
    return updated
  }

  if (editing) {
    return (
      <div className={`py-3 ${border ? 'border-t border-[var(--border)]' : ''}`}>
        <ChannelForm
          initial={channel}
          emailConfigured={emailConfigured}
          onSave={save}
          onCancel={() => setEditing(false)}
        />
      </div>
    )
  }

  return (
    <div className={`py-3 ${border ? 'border-t border-[var(--border)]' : ''}`}>
      <div className="flex items-center gap-3">
        <div className="w-9 h-9 rounded-[var(--radius-btn)] bg-[var(--bg)] border border-[var(--border)] flex items-center justify-center shrink-0">
          <Icon size={15} className="text-[var(--text-muted)]" />
        </div>
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <p className="text-sm text-[var(--text)] truncate">{channel.label || meta.label}</p>
            {!channel.enabled && <Badge color="default">paused</Badge>}
            {emailBlocked && <Badge color="yellow">SMTP off</Badge>}
          </div>
          <p className="text-xs text-[var(--text-faint)] truncate font-mono">
            {channel.kind === 'email' ? channel.target : maskUrl(channel.target)}
          </p>
          <div className="flex flex-wrap items-center gap-1.5 mt-1.5">
            <Badge color="teal">{channel.schedule}</Badge>
            {enabledDigests.map((d) => <Badge key={d} color="indigo">{d}</Badge>)}
            {enabledDigests.length === 0 && <span className="text-[10px] text-[var(--text-faint)]">no digests selected</span>}
          </div>
        </div>
        <div className="flex items-center gap-1 shrink-0">
          <IconButton title={channel.enabled ? 'Pause' : 'Enable'} onClick={toggleEnabled} disabled={busy} active={channel.enabled}>
            <Power size={14} />
          </IconButton>
          <IconButton title="Send test" onClick={runTest} disabled={busy || !channel.enabled}>
            {busy ? <Loader2 size={14} className="animate-spin" /> : <Send size={14} />}
          </IconButton>
          <IconButton title="Edit" onClick={() => setEditing(true)} disabled={busy}>
            <Pencil size={14} />
          </IconButton>
          <IconButton title="Delete" onClick={remove} disabled={busy} danger>
            <Trash2 size={14} />
          </IconButton>
        </div>
      </div>

      {emailBlocked && (
        <p className="text-xs text-[var(--text-faint)] mt-2 ml-12 flex items-center gap-1.5">
          <AlertTriangle size={12} className="text-yellow-400" />
          Email won't deliver until SMTP is configured on the server.
        </p>
      )}
      <TestResults results={testResults} />
      {error && <p className="text-xs text-red-400 mt-2 ml-12">{error}</p>}
    </div>
  )
}

function IconButton({ children, title, onClick, disabled, danger, active }) {
  return (
    <button
      type="button"
      title={title}
      onClick={onClick}
      disabled={disabled}
      className={[
        'p-1.5 rounded-[var(--radius-btn)] transition-colors duration-150',
        disabled ? 'opacity-40 cursor-not-allowed' : 'cursor-pointer',
        danger
          ? 'text-[var(--text-faint)] hover:text-red-400 hover:bg-red-500/10'
          : active
            ? 'text-[var(--brand-teal)] hover:bg-[var(--bg-surface3)]'
            : 'text-[var(--text-faint)] hover:text-[var(--text-muted)] hover:bg-[var(--bg-surface3)]',
      ].join(' ')}
    >
      {children}
    </button>
  )
}

// maskUrl shows only the host of a webhook URL so the secret path isn't exposed
// in the UI.
function maskUrl(url) {
  try {
    const u = new URL(url)
    return `${u.protocol}//${u.host}/…`
  } catch {
    return url
  }
}

// NotificationsBody — the inner content of the Notifications SectionCard. Kept
// separate from the SectionCard wrapper (which lives in Settings.jsx) so the
// component owns only its own concern.
export function NotificationsBody() {
  const { channels, emailConfigured, loading, error, reload } = useChannels()
  const [adding, setAdding] = useState(false)

  async function addChannel(body) {
    const created = await createChannel(body)
    setAdding(false)
    reload()
    return created
  }

  return (
    <div className="space-y-5">
      {/* Live preview — the evidence that would be delivered. */}
      <div>
        <div className="flex items-center justify-between mb-2">
          <p className="text-xs font-medium text-[var(--text-muted)]">Live digest preview</p>
          <span className="text-[10px] text-[var(--text-faint)]">Built from your real data, scoped to this org</span>
        </div>
        <DigestPreview />
      </div>

      {/* Channels */}
      <div>
        <div className="flex items-center justify-between mb-1">
          <p className="text-xs font-medium text-[var(--text-muted)]">Delivery channels</p>
          {!adding && (
            <Button variant="outline" size="sm" leftIcon={<Plus size={13} />} onClick={() => setAdding(true)}>
              Add channel
            </Button>
          )}
        </div>

        {!emailConfigured && (
          <p className="text-[11px] text-[var(--text-faint)] mb-2">
            Email delivery is unavailable on this server — set <span className="font-mono text-[var(--text-muted)]">SMTP_HOST</span> to enable it. Slack and webhook channels work out of the box.
          </p>
        )}

        {adding && (
          <div className="mb-3">
            <ChannelForm emailConfigured={emailConfigured} onSave={addChannel} onCancel={() => setAdding(false)} />
          </div>
        )}

        {loading ? (
          <div className="flex items-center gap-2 py-4 text-xs text-[var(--text-faint)]">
            <Loader2 size={14} className="animate-spin" /> Loading channels…
          </div>
        ) : error ? (
          <p className="text-xs text-red-400 py-2">{error}</p>
        ) : channels.length === 0 && !adding ? (
          <div className="rounded-[var(--radius-btn)] border border-dashed border-[var(--border)] py-8 px-4 text-center">
            <Bell size={20} className="mx-auto text-[var(--text-faint)] mb-2" />
            <p className="text-sm text-[var(--text-muted)]">No channels yet</p>
            <p className="text-xs text-[var(--text-faint)] mt-1">Add a Slack, Discord, Google Chat, Teams, webhook, or email channel to push status where your team works.</p>
          </div>
        ) : (
          <div>
            {channels.map((ch, i) => (
              <ChannelRow
                key={ch.id}
                channel={ch}
                emailConfigured={emailConfigured}
                onChanged={reload}
                border={i > 0}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
