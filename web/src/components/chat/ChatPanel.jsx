/**
 * ChatPanel — the agentic AI assistant rail for the app shell.
 *
 * A streaming, tool-using assistant over POST /api/chat (SSE). The header carries
 * a provider-grouped model selector; assistant turns stream token-by-token with
 * markdown, interleaved tool-use cards, and confirmable action buttons (e.g.
 * "Upgrade to Pro", "Sync repo"). Theme-aware; uses design tokens. Rendered inside
 * AppShell's right rail.
 */
import { useEffect, useRef, useState } from 'react'
import { Sparkles, X, ArrowUp, Square, RotateCcw } from 'lucide-react'
import { useChat } from '../../lib/useChat.js'
import { ModelSelector } from './ModelSelector.jsx'
import { ToolCard } from './ToolCard.jsx'
import { ActionButton } from './ActionButton.jsx'
import { ChatMarkdown } from './ChatMarkdown.jsx'

const SUGGESTIONS = [
  'How’s our cycle time trending?',
  'Who are the top contributors?',
  'Upgrade me to Pro',
  'Invoice Acme for last month from git',
]

function Avatar() {
  return (
    <div className="mt-0.5 shrink-0 w-6 h-6 rounded-full bg-gradient-to-br from-[var(--brand-teal)] to-[var(--brand-indigo)] flex items-center justify-center">
      <Sparkles size={12} strokeWidth={2.5} className="text-white" />
    </div>
  )
}

function UserBubble({ text }) {
  return (
    <div className="flex justify-end">
      <div className="max-w-[85%] rounded-[var(--radius-card)] rounded-br-sm px-3.5 py-2 bg-[var(--brand-indigo)]/15 border border-[var(--brand-indigo)]/25 text-sm text-[var(--text)] leading-relaxed whitespace-pre-wrap">
        {text}
      </div>
    </div>
  )
}

function StreamingDots() {
  return (
    <span className="inline-flex items-center gap-1 align-middle ml-0.5">
      {[0, 1, 2].map(i => (
        <span key={i} className="w-1 h-1 rounded-full bg-[var(--text-faint)] animate-bounce" style={{ animationDelay: `${i * 0.15}s` }} />
      ))}
    </span>
  )
}

function AssistantBubble({ msg, onRunAction }) {
  const hasContent = msg.parts.some(p => (p.kind === 'text' && p.text) || p.kind === 'tool' || p.kind === 'action')
  return (
    <div className="flex gap-2.5">
      <Avatar />
      <div className="min-w-0 flex-1">
        {msg.parts.map((part, i) => {
          if (part.kind === 'tool') return <ToolCard key={part.id ?? i} part={part} />
          if (part.kind === 'action') return <ActionButton key={part.id ?? i} part={part} onConfirm={() => onRunAction(msg.id, part.id)} />
          if (part.kind === 'text' && part.text) return <ChatMarkdown key={i}>{part.text}</ChatMarkdown>
          return null
        })}

        {/* Thinking indicator before the first token arrives. */}
        {msg.streaming && !hasContent && !msg.error && (
          <div className="flex items-center h-6"><StreamingDots /></div>
        )}
        {/* Trailing caret while text is still streaming. */}
        {msg.streaming && hasContent && !msg.error && (
          <span className="inline-block w-1.5 h-3.5 align-text-bottom bg-[var(--brand-teal)] animate-pulse rounded-sm" aria-hidden="true" />
        )}

        {msg.error && (
          <div className="mt-1 rounded-[var(--radius-badge)] px-3 py-2 text-sm text-red-400 bg-red-500/[0.06] border border-red-500/20">
            {msg.error}
          </div>
        )}
      </div>
    </div>
  )
}

function EmptyState({ onPick, disabled }) {
  return (
    <div className="h-full flex flex-col items-center justify-center text-center px-6">
      <div className="w-11 h-11 rounded-full bg-gradient-to-br from-[var(--brand-teal)] to-[var(--brand-indigo)] flex items-center justify-center mb-4">
        <Sparkles size={20} strokeWidth={2} className="text-white" />
      </div>
      <p className="text-sm text-[var(--text-dim)] leading-relaxed max-w-[260px]">
        Your repo-aware assistant. Ask about metrics, contributors, billing — it can act, with your confirm.
      </p>
      <div className="mt-5 w-full flex flex-col gap-2">
        {SUGGESTIONS.map(s => (
          <button
            key={s}
            type="button"
            disabled={disabled}
            onClick={() => onPick(s)}
            className="text-left text-[12px] text-[var(--text-muted)] hover:text-[var(--text)] rounded-[var(--radius-btn)] border border-[var(--border)] bg-[var(--bg-surface2)] hover:border-[var(--border2)] px-3 py-2 transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {s}
          </button>
        ))}
      </div>
    </div>
  )
}

export function ChatPanel({ onClose }) {
  const {
    messages, sending, send, stop, regenerate, runAction,
    models, modelId, selectedModel, chooseModel,
    gatewayDisabled, canRegenerate,
  } = useChat()
  const [input, setInput] = useState('')
  const scrollRef = useRef(null)
  const endRef = useRef(null)

  useEffect(() => {
    endRef.current?.scrollIntoView({ behavior: 'smooth', block: 'end' })
  }, [messages])

  function submit(e) {
    e?.preventDefault()
    if (!input.trim() || sending) return
    send(input)
    setInput('')
  }

  function pick(text) {
    if (sending) return
    send(text)
  }

  const isEmpty = messages.length === 0

  return (
    <div className="flex flex-col h-full min-h-0">
      {/* Header */}
      <header className="h-12 shrink-0 flex items-center gap-2 px-4 border-b border-[var(--border)]">
        <Sparkles size={15} strokeWidth={2} className="text-[var(--brand-teal)]" aria-hidden="true" />
        <span className="text-[13px] font-semibold text-[var(--text)] tracking-tight">Ask AI</span>
        <div className="ml-auto flex items-center gap-1.5">
          <ModelSelector
            models={models}
            modelId={modelId}
            selectedModel={selectedModel}
            onChoose={chooseModel}
            disabled={sending}
          />
          <button
            type="button"
            onClick={onClose}
            aria-label="Close chat"
            title="Close"
            className="flex items-center justify-center w-7 h-7 rounded-md text-[var(--text-faint)] hover:text-[var(--text)] hover:bg-[var(--bg-surface2)] transition-colors cursor-pointer focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--brand-teal)]"
          >
            <X size={15} strokeWidth={2} aria-hidden="true" />
          </button>
        </div>
      </header>

      {/* Gateway-off banner */}
      {gatewayDisabled && (
        <div className="shrink-0 px-4 py-2 text-[11.5px] text-[var(--text-faint)] bg-[var(--bg-surface)] border-b border-[var(--border)]">
          AI chat isn’t enabled on this server. Set <code className="font-mono text-[var(--text-muted)]">LLM_GATEWAY</code> + a provider key to turn it on.
        </div>
      )}

      {/* Messages */}
      <div ref={scrollRef} className="flex-1 min-h-0 overflow-y-auto" aria-live="polite" aria-busy={sending}>
        {isEmpty ? (
          <EmptyState onPick={pick} disabled={gatewayDisabled} />
        ) : (
          <div className="px-4 py-5 space-y-5">
            {messages.map(msg =>
              msg.role === 'user'
                ? <UserBubble key={msg.id} text={msg.content} />
                : <AssistantBubble key={msg.id} msg={msg} onRunAction={runAction} />
            )}
            {canRegenerate && (
              <div className="flex justify-center">
                <button
                  type="button"
                  onClick={regenerate}
                  className="inline-flex items-center gap-1.5 rounded-[var(--radius-btn)] border border-[var(--border)] bg-[var(--bg-surface2)] px-2.5 py-1 text-[11.5px] text-[var(--text-muted)] hover:text-[var(--text)] hover:border-[var(--border2)] transition-colors cursor-pointer"
                >
                  <RotateCcw size={11} strokeWidth={2} /> Regenerate
                </button>
              </div>
            )}
            <div ref={endRef} />
          </div>
        )}
      </div>

      {/* Composer */}
      <form onSubmit={submit} className="shrink-0 p-3 border-t border-[var(--border)]">
        <div className="flex items-end gap-2 rounded-[var(--radius-card)] border border-[var(--border)] bg-[var(--bg)] px-3 py-2 focus-within:border-[var(--brand-indigo)]/50 transition-colors">
          <textarea
            rows={1}
            value={input}
            onChange={e => setInput(e.target.value)}
            onKeyDown={e => { if (e.key === 'Enter' && !e.shiftKey) submit(e) }}
            placeholder={gatewayDisabled ? 'AI chat is disabled' : 'Ask anything about your repos…'}
            aria-label="Ask AI about your repos"
            disabled={gatewayDisabled}
            className="flex-1 resize-none bg-transparent text-sm text-[var(--text)] outline-none placeholder-[var(--text-faint)] max-h-32 leading-relaxed disabled:cursor-not-allowed"
          />
          {sending ? (
            <button
              type="button"
              onClick={stop}
              aria-label="Stop generating"
              title="Stop"
              className="shrink-0 flex items-center justify-center w-7 h-7 rounded-full bg-[var(--bg-surface3)] text-[var(--text)] hover:bg-[var(--border2)] transition-colors cursor-pointer focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--brand-teal)]"
            >
              <Square size={11} strokeWidth={2.5} fill="currentColor" aria-hidden="true" />
            </button>
          ) : (
            <button
              type="submit"
              disabled={!input.trim() || gatewayDisabled}
              aria-label="Send message"
              className="shrink-0 flex items-center justify-center w-7 h-7 rounded-full bg-gradient-to-br from-[var(--brand-teal)] to-[var(--brand-indigo)] text-white disabled:opacity-40 disabled:cursor-not-allowed hover:opacity-90 transition-opacity cursor-pointer focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--brand-teal)] focus-visible:ring-offset-1 focus-visible:ring-offset-[var(--bg)]"
            >
              <ArrowUp size={15} strokeWidth={2.5} aria-hidden="true" />
            </button>
          )}
        </div>
      </form>
    </div>
  )
}
