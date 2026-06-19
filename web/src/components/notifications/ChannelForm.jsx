import { useState } from 'react'
import { Loader2, Check, MessageSquare, Webhook, Mail } from 'lucide-react'
import { Button } from '../ui/index.js'

const KINDS = [
  { kind: 'slack', label: 'Slack', icon: MessageSquare, placeholder: 'https://hooks.slack.com/services/…', hint: 'Incoming-webhook URL' },
  { kind: 'webhook', label: 'Webhook', icon: Webhook, placeholder: 'https://example.com/hooks/gitstate', hint: 'Receives Slack-format JSON' },
  { kind: 'email', label: 'Email', icon: Mail, placeholder: 'team@yourco.com', hint: 'Plain-text digest' },
]

const DIGEST_KEYS = [
  { key: 'weeklyStatus', label: 'Weekly status' },
  { key: 'stalePRs', label: 'Stale PRs' },
  { key: 'ooo', label: "Who's OOO" },
]

// DigestToggle — a small checkbox chip for a digest type.
function DigestToggle({ active, disabled, onClick, label }) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className={[
        'flex items-center gap-1.5 rounded-full border px-3 py-1.5 text-xs transition-all duration-150',
        disabled ? 'opacity-50 cursor-not-allowed' : 'cursor-pointer',
        active
          ? 'border-[var(--brand-teal)] bg-[var(--brand-teal)]/10 text-[var(--brand-teal)]'
          : 'border-[var(--border)] text-[var(--text-faint)] hover:border-[var(--border2)]',
      ].join(' ')}
    >
      {label}
      {active && <Check size={12} />}
    </button>
  )
}

// ChannelForm — create (or edit) a notification channel. When `initial` is set
// the form edits that channel; otherwise it creates a new one.
//
// Props:
//   initial         — existing channel object, or null for create
//   emailConfigured — whether the server can deliver email
//   onSave(body)    — async; resolves to the saved channel
//   onCancel()      — dismiss the form
export function ChannelForm({ initial, emailConfigured, onSave, onCancel }) {
  const editing = Boolean(initial)
  const [kind, setKind] = useState(initial?.kind ?? 'slack')
  const [target, setTarget] = useState(initial?.target ?? '')
  const [label, setLabel] = useState(initial?.label ?? '')
  const [schedule, setSchedule] = useState(initial?.schedule ?? 'weekly')
  const [digests, setDigests] = useState({
    weeklyStatus: initial?.digests?.weeklyStatus ?? true,
    stalePRs: initial?.digests?.stalePRs ?? true,
    ooo: initial?.digests?.ooo ?? true,
  })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState(null)

  const activeKind = KINDS.find((k) => k.kind === kind) ?? KINDS[0]
  const noDigests = !digests.weeklyStatus && !digests.stalePRs && !digests.ooo

  function toggleDigest(key) {
    setDigests((d) => ({ ...d, [key]: !d[key] }))
  }

  async function submit() {
    setError(null)
    if (!target.trim()) {
      setError(activeKind.kind === 'email' ? 'Enter an email address' : 'Enter a webhook URL')
      return
    }
    if (noDigests) {
      setError('Select at least one digest')
      return
    }
    setSaving(true)
    try {
      const body = editing
        ? { target: target.trim(), label, schedule, digests }
        : { kind, target: target.trim(), label, schedule, digests, enabled: true }
      await onSave(body)
    } catch (e) {
      setError(e?.message ?? 'Failed to save channel')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="rounded-[var(--radius-btn)] border border-[var(--border2)] bg-[var(--bg)] p-4 space-y-4">
      {/* Kind selector — fixed when editing (kind is immutable post-create). */}
      {!editing && (
        <div>
          <label className="block text-xs font-medium text-[var(--text-muted)] mb-1.5">Channel type</label>
          <div className="flex flex-col sm:flex-row gap-2">
            {KINDS.map(({ kind: k, label: kl, icon: Icon }) => {
              const disabled = k === 'email' && !emailConfigured
              const selected = kind === k
              return (
                <button
                  key={k}
                  type="button"
                  disabled={disabled}
                  onClick={() => setKind(k)}
                  className={[
                    'flex-1 flex items-center gap-2 rounded-[var(--radius-btn)] border px-3 py-2.5 text-left transition-all duration-150',
                    disabled ? 'opacity-40 cursor-not-allowed' : 'cursor-pointer',
                    selected
                      ? 'border-[var(--brand-teal)] bg-[var(--brand-teal)]/5'
                      : 'border-[var(--border)] hover:border-[var(--border2)]',
                  ].join(' ')}
                >
                  <Icon size={15} className={selected ? 'text-[var(--brand-teal)]' : 'text-[var(--text-faint)]'} />
                  <div className="min-w-0">
                    <p className="text-sm text-[var(--text)]">{kl}</p>
                    <p className="text-[10px] text-[var(--text-faint)]">
                      {k === 'email' && !emailConfigured ? 'Configure SMTP' : KINDS.find((x) => x.kind === k).hint}
                    </p>
                  </div>
                </button>
              )
            })}
          </div>
        </div>
      )}

      {/* Target */}
      <div>
        <label className="block text-xs font-medium text-[var(--text-muted)] mb-1.5">
          {activeKind.kind === 'email' ? 'Email address' : 'Webhook URL'}
        </label>
        <input
          type={activeKind.kind === 'email' ? 'email' : 'url'}
          value={target}
          onChange={(e) => setTarget(e.target.value)}
          placeholder={activeKind.placeholder}
          autoComplete="off"
          className="w-full font-mono rounded-[var(--radius-btn)] border border-[var(--border2)] bg-[var(--bg-surface2)] px-3 py-2 text-sm text-[var(--text)] placeholder:text-[var(--text-faint)] focus:border-[var(--brand-teal)] focus:outline-none"
        />
      </div>

      {/* Label */}
      <div>
        <label className="block text-xs font-medium text-[var(--text-muted)] mb-1.5">Label <span className="text-[var(--text-faint)]">(optional)</span></label>
        <input
          type="text"
          value={label}
          onChange={(e) => setLabel(e.target.value)}
          placeholder="e.g. #eng-standup"
          className="w-full rounded-[var(--radius-btn)] border border-[var(--border2)] bg-[var(--bg-surface2)] px-3 py-2 text-sm text-[var(--text)] placeholder:text-[var(--text-faint)] focus:border-[var(--brand-teal)] focus:outline-none"
        />
      </div>

      {/* Digests */}
      <div>
        <label className="block text-xs font-medium text-[var(--text-muted)] mb-1.5">Digests</label>
        <div className="flex flex-wrap gap-2">
          {DIGEST_KEYS.map(({ key, label: dl }) => (
            <DigestToggle key={key} active={digests[key]} onClick={() => toggleDigest(key)} label={dl} />
          ))}
        </div>
      </div>

      {/* Schedule */}
      <div>
        <label className="block text-xs font-medium text-[var(--text-muted)] mb-1.5">Schedule</label>
        <div className="inline-flex rounded-[var(--radius-btn)] border border-[var(--border2)] overflow-hidden">
          {['weekly', 'daily'].map((s) => (
            <button
              key={s}
              type="button"
              onClick={() => setSchedule(s)}
              className={[
                'px-4 py-1.5 text-xs capitalize transition-colors duration-150',
                schedule === s ? 'bg-[var(--brand-teal)]/10 text-[var(--brand-teal)]' : 'text-[var(--text-faint)] hover:text-[var(--text-muted)]',
              ].join(' ')}
            >
              {s}
            </button>
          ))}
        </div>
      </div>

      {error && <p className="text-xs text-red-400">{error}</p>}

      <div className="flex items-center gap-2 pt-1">
        <Button
          variant="primary"
          size="sm"
          onClick={submit}
          disabled={saving}
          leftIcon={saving ? <Loader2 size={13} className="animate-spin" /> : undefined}
        >
          {saving ? 'Saving…' : editing ? 'Save changes' : 'Add channel'}
        </Button>
        <Button variant="ghost" size="sm" onClick={onCancel} disabled={saving}>Cancel</Button>
      </div>
    </div>
  )
}
