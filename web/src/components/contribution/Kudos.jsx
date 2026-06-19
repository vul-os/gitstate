/**
 * Kudos — peer recognition (the SPACE "satisfaction" axis; a human signal that
 * deliberately does NOT feed the score, a partial counter to reviewer collusion).
 *
 *   - KudosBadge  : a small "★ N" pill shown on a contributor card/drawer.
 *   - KudosModal  : give kudos — pick teammate + optional dimension + message.
 *   - KudosFeed   : recent recognition messages.
 */
import { useState } from 'react'
import { Button, Badge } from '../ui/index.js'
import { Avatar } from './parts.jsx'
import { DIMENSIONS, giveKudos } from '../../lib/useContribution.js'
import { relTime } from './helpers.js'
import { Heart, X, Sparkles } from 'lucide-react'

// ── badge ──────────────────────────────────────────────────────────────────

export function KudosBadge({ count = 0, className = '' }) {
  if (!count) return null
  return (
    <span
      className={`inline-flex items-center gap-1 text-[10px] font-mono text-[var(--brand-indigo)] ${className}`}
      title={`${count} kudos received from teammates`}
    >
      <Heart size={11} className="fill-[var(--brand-indigo)]/30" />
      <span className="tabular-nums">{count}</span>
    </span>
  )
}

// ── modal ──────────────────────────────────────────────────────────────────

/**
 * Give-kudos modal. `members` is the roster (each {userId, name, email});
 * `selfId` is excluded (can't kudos yourself). onDone() refetches.
 */
export function KudosModal({ members = [], selfId, defaultToUser, onClose, onDone }) {
  const candidates = members.filter((m) => m.userId && m.userId !== selfId && !m.isAgentBot)
  const [toUser, setToUser] = useState(defaultToUser && defaultToUser !== selfId ? defaultToUser : '')
  const [dimension, setDimension] = useState('')
  const [message, setMessage] = useState('')
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState(null)

  async function submit(e) {
    e.preventDefault()
    if (!toUser || !message.trim()) {
      setErr('Pick a teammate and write a message.')
      return
    }
    setSaving(true); setErr(null)
    try {
      await giveKudos({ toUser, dimension, message: message.trim() })
      onDone?.()
      onClose()
    } catch (e2) {
      setErr(e2.message ?? 'Could not send kudos')
    } finally {
      setSaving(false)
    }
  }

  return (
    <>
      <div className="fixed inset-0 z-40 animate-[fadeIn_0.2s_ease]" style={{ background: 'rgba(11,17,32,0.6)', backdropFilter: 'blur(2px)' }} onClick={onClose} aria-hidden />
      <div className="fixed inset-0 z-50 flex items-center justify-center p-4" role="dialog" aria-modal="true" aria-label="Give kudos">
        <form
          onSubmit={submit}
          className="w-full max-w-md rounded-[var(--radius-card)] border border-[var(--border)] bg-[var(--bg)] shadow-[var(--shadow-float)] overflow-hidden"
          style={{ animation: 'kudosIn 0.24s cubic-bezier(0.22,1,0.36,1)' }}
        >
          <style>{`@keyframes kudosIn{from{transform:translateY(8px) scale(0.98);opacity:0}to{transform:none;opacity:1}}@keyframes fadeIn{from{opacity:0}to{opacity:1}}`}</style>

          <div className="flex items-center justify-between gap-3 px-5 py-4 border-b border-[var(--border)]">
            <div className="flex items-center gap-2">
              <Heart size={16} className="text-[var(--brand-indigo)]" />
              <h3 className="text-base font-semibold text-[var(--text)]">Give kudos</h3>
            </div>
            <button type="button" onClick={onClose} className="p-1.5 rounded-lg text-[var(--text-faint)] hover:text-[var(--text)] hover:bg-[var(--bg-surface2)] transition-colors cursor-pointer" aria-label="Close">
              <X size={18} />
            </button>
          </div>

          <div className="p-5 space-y-4">
            <p className="text-[12px] text-[var(--text-faint)] leading-relaxed">
              Recognition that doesn’t feed the score — a human signal of who made the work better.
            </p>

            {/* teammate */}
            <label className="block">
              <span className="text-[11px] font-mono uppercase tracking-wide text-[var(--text-faint)]">Teammate</span>
              <select
                value={toUser} onChange={(e) => setToUser(e.target.value)}
                className="mt-1.5 w-full bg-[var(--bg-surface)] text-sm text-[var(--text)] rounded-[var(--radius-btn)] px-3 py-2 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/50"
              >
                <option value="">Select someone…</option>
                {candidates.map((m) => (
                  <option key={m.userId} value={m.userId}>{m.name || m.email}</option>
                ))}
              </select>
            </label>

            {/* dimension (optional) */}
            <div>
              <span className="text-[11px] font-mono uppercase tracking-wide text-[var(--text-faint)]">For (optional)</span>
              <div className="mt-1.5 flex flex-wrap gap-1.5">
                {DIMENSIONS.map((d) => {
                  const active = dimension === d.key
                  return (
                    <button
                      key={d.key} type="button"
                      onClick={() => setDimension(active ? '' : d.key)}
                      className={[
                        'px-2.5 py-1 rounded-full text-[11px] border transition-colors cursor-pointer',
                        active
                          ? 'border-[var(--brand-teal)]/50 bg-[var(--brand-teal)]/12 text-[var(--brand-teal)]'
                          : 'border-[var(--border)] text-[var(--text-faint)] hover:text-[var(--text-dim)]',
                      ].join(' ')}
                    >
                      {d.label}
                    </button>
                  )
                })}
              </div>
            </div>

            {/* message */}
            <label className="block">
              <span className="text-[11px] font-mono uppercase tracking-wide text-[var(--text-faint)]">Message</span>
              <textarea
                value={message} onChange={(e) => setMessage(e.target.value.slice(0, 500))}
                rows={3} placeholder="What did they do that mattered?"
                className="mt-1.5 w-full resize-none bg-[var(--bg-surface)] text-sm text-[var(--text)] rounded-[var(--radius-btn)] px-3 py-2 border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/50"
              />
              <span className="text-[10px] font-mono text-[var(--text-faint)]">{message.length}/500</span>
            </label>

            {err && <p className="text-[12px] text-red-400">{err}</p>}
          </div>

          <div className="flex items-center justify-end gap-2 px-5 py-4 border-t border-[var(--border)]">
            <Button type="button" variant="ghost" size="sm" onClick={onClose}>Cancel</Button>
            <Button type="submit" variant="primary" size="sm" disabled={saving} leftIcon={<Heart size={14} />}>
              {saving ? 'Sending…' : 'Send kudos'}
            </Button>
          </div>
        </form>
      </div>
    </>
  )
}

// ── feed ───────────────────────────────────────────────────────────────────

function dimLabel(key) {
  return DIMENSIONS.find((d) => d.key === key)?.label
}

export function KudosFeed({ kudos = [], loading, emptyHint }) {
  if (loading && kudos.length === 0) {
    return (
      <div className="space-y-2 animate-pulse">
        {[0, 1, 2].map((i) => <div key={i} className="h-12 rounded-[var(--radius-card)] bg-[var(--bg-surface3)]" />)}
      </div>
    )
  }
  if (kudos.length === 0) {
    return (
      <div className="rounded-[var(--radius-card)] border border-dashed border-[var(--border)] p-6 text-center">
        <Sparkles size={16} className="mx-auto mb-2 text-[var(--text-faint)]" />
        <p className="text-xs text-[var(--text-faint)]">{emptyHint || 'No kudos yet — be the first to recognise a teammate.'}</p>
      </div>
    )
  }
  return (
    <ul className="space-y-2">
      {kudos.map((k) => (
        <li key={k.id} className="flex items-start gap-3 rounded-[var(--radius-card)] border border-[var(--border)] bg-[var(--bg-surface)] p-3">
          <Avatar name={k.fromName || k.fromUser} size={30} />
          <div className="min-w-0 flex-1">
            <p className="text-[12px] text-[var(--text-dim)] leading-snug">
              <span className="font-semibold text-[var(--text)]">{k.fromName || 'Someone'}</span>
              <span className="text-[var(--text-faint)]"> → </span>
              <span className="font-semibold text-[var(--text)]">{k.toName || 'a teammate'}</span>
              {k.dimension && dimLabel(k.dimension) && (
                <Badge color="teal" className="ml-1.5 align-middle">{dimLabel(k.dimension)}</Badge>
              )}
            </p>
            <p className="text-[12px] text-[var(--text-dim)] mt-0.5 leading-snug break-words">{k.message}</p>
            <p className="text-[10px] font-mono text-[var(--text-faint)] mt-1">{relTime(k.createdAt)}</p>
          </div>
        </li>
      ))}
    </ul>
  )
}
