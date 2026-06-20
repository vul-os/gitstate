/**
 * AIModels — transparent "Models & pricing" pass-through table.
 *
 * Managed AI is metered at each model's standard provider list price — there is
 * NO per-seat AI fee and NO visible markup. Clients either run a model at its
 * standard rate (covered first by their included monthly AI credit) or BYOK and
 * pay their provider directly.
 *
 * Rates below are INDICATIVE provider list prices (USD per 1M tokens) and are
 * intentionally editable placeholders — see the footnote / invoice for exact
 * metered usage. Reusable: drop <AIModels /> onto any page.
 *
 * Consumed by pages/Pricing.jsx.
 */
import { Cpu, KeyRound, ArrowDownToLine, ArrowUpFromLine, Info, Sparkles, Zap, Gem } from 'lucide-react'
import { Card, Badge, Glow } from '../ui'

// Indicative provider list prices, USD per 1M tokens. Editable placeholders.
const MODELS = [
  {
    name: 'Claude Haiku 4.5',
    icon: Zap,
    inUsd: 1,
    outUsd: 5,
    note: 'fast, cheap drafts',
  },
  {
    name: 'Claude Sonnet 4.6',
    icon: Sparkles,
    inUsd: 3,
    outUsd: 15,
    note: 'default — best value',
    recommended: true,
  },
  {
    name: 'Claude Opus 4.8',
    icon: Gem,
    inUsd: 5,
    outUsd: 25,
    note: 'deep reasoning',
  },
]

// Rates are USD/M token list prices — kept in USD (with a note) since they are
// provider-quoted in USD; the per-seat plan prices elsewhere are currency-aware.
const usdPerM = (n) =>
  new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'USD',
    minimumFractionDigits: 0,
    maximumFractionDigits: 0,
  }).format(n)

function RateCell({ icon: Icon, label, value, accent }) {
  return (
    <div className="flex items-center gap-2">
      <Icon size={13} className={accent ? 'text-[#2DD4BF]' : 'text-[#818cf8]'} strokeWidth={1.9} />
      <span className="font-mono text-sm font-semibold text-[var(--text-dim)] tabular-nums">{value}</span>
      <span className="text-[10px] font-mono uppercase tracking-wider text-[var(--text-faint)]">{label}</span>
    </div>
  )
}

export default function AIModels() {
  return (
    <Card padding="none" glow className="relative overflow-hidden border-[var(--border2)]">
      <Glow variant="brand" size={520} className="-top-16 right-0 opacity-40" />

      {/* Header */}
      <div className="relative z-10 p-6 md:p-8 border-b border-[var(--border)]">
        <div className="flex items-start gap-3 mb-3">
          <div className="w-9 h-9 shrink-0 rounded-[var(--radius-badge)] bg-[#2DD4BF]/10 border border-[#2DD4BF]/25 flex items-center justify-center">
            <Cpu size={17} className="text-[#2DD4BF]" strokeWidth={1.8} />
          </div>
          <div>
            <div className="flex items-center gap-2.5">
              <h3 className="font-display text-lg font-semibold text-[var(--text)] leading-none">Models &amp; pricing</h3>
              <Badge color="teal">At model cost</Badge>
            </div>
            <p className="text-[11px] text-[var(--text-faint)] mt-1.5">Transparent pass-through · no per-seat AI fee</p>
          </div>
        </div>
        <p className="text-sm text-[var(--text-muted)] leading-relaxed max-w-2xl">
          You pay the model&apos;s standard rate — nothing added per seat. Your monthly AI credit covers
          usage first; beyond it, every model is metered at the rate below. Prefer to pay your provider
          directly? Bring your own key (BYOK).
        </p>
      </div>

      {/* Model rows */}
      <div className="relative z-10 flex flex-col divide-y divide-[var(--border)]">
        {MODELS.map((m) => {
          const Icon = m.icon
          return (
            <div
              key={m.name}
              className="flex flex-col sm:flex-row sm:items-center gap-3 sm:gap-6 px-6 md:px-8 py-4 transition-colors hover:bg-[var(--bg-surface2)]/40"
              style={m.recommended ? { background: 'rgba(45,212,191,0.04)' } : undefined}
            >
              {/* Name + note */}
              <div className="flex items-center gap-3 sm:w-[40%] min-w-0">
                <div
                  className="w-8 h-8 shrink-0 rounded-[var(--radius-badge)] flex items-center justify-center border"
                  style={{
                    background: m.recommended ? 'rgba(45,212,191,0.12)' : 'var(--bg-surface3)',
                    borderColor: m.recommended ? 'rgba(45,212,191,0.3)' : 'var(--border)',
                  }}
                >
                  <Icon size={15} className={m.recommended ? 'text-[#2DD4BF]' : 'text-[var(--text-muted)]'} strokeWidth={1.8} />
                </div>
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="font-display text-sm font-semibold text-[var(--text)] truncate">{m.name}</span>
                    {m.recommended && <Badge color="teal">Default</Badge>}
                  </div>
                  <span className="text-[11px] text-[var(--text-faint)]">{m.note}</span>
                </div>
              </div>

              {/* Rates */}
              <div className="flex flex-wrap items-center gap-x-7 gap-y-1.5 sm:ml-auto pl-11 sm:pl-0">
                <RateCell icon={ArrowDownToLine} label="in / M" value={usdPerM(m.inUsd)} accent />
                <RateCell icon={ArrowUpFromLine} label="out / M" value={usdPerM(m.outUsd)} />
              </div>
            </div>
          )
        })}
      </div>

      {/* Other providers + BYOK + footnote */}
      <div className="relative z-10 border-t border-[var(--border)] px-6 md:px-8 py-5 flex flex-col gap-3 bg-[var(--bg-surface)]/40">
        <div className="flex flex-col sm:flex-row sm:items-center gap-3">
          <p className="text-xs text-[var(--text-muted)] flex items-center gap-2">
            <Cpu size={13} className="text-[var(--text-faint)] shrink-0" />
            OpenAI &amp; Gemini models also available at their standard rates.
          </p>
          <span className="inline-flex items-center gap-1.5 rounded-[var(--radius-badge)] border border-[#818cf8]/25 bg-[#6366F1]/[0.07] px-2.5 py-1 self-start sm:ml-auto">
            <KeyRound size={12} className="text-[#818cf8] shrink-0" strokeWidth={2.1} />
            <span className="text-[11px] text-[var(--text-dim)]">BYOK — pay your provider directly</span>
          </span>
        </div>
        <p className="text-[11px] text-[var(--text-faint)]/90 leading-relaxed flex items-start gap-1.5">
          <Info size={12} className="text-[var(--text-faint)] shrink-0 mt-0.5" />
          <span>
            Rates are indicative provider list prices in <span className="font-mono text-[var(--text-muted)]">USD</span> per
            1M tokens; see your invoice for exact metered usage.
          </span>
        </p>
      </div>
    </Card>
  )
}
