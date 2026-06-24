/**
 * ModelPriceTable — one provider group of model rows showing BOTH the provider's
 * base rate (muted / struck) and OUR managed rate (+5%, prominent) for input and
 * output tokens. Responsive: a real table on desktop, stacked cards on mobile.
 *
 * Props:
 *   provider  — "anthropic" | "openai" | "google"
 *   models    — rows for this provider (already sorted by the parent)
 *   format    — useCurrency().format (USD→selected currency)
 *   cheapestId — id of the cheapest row across ALL providers (gets a highlight)
 */
import { ArrowDownToLine, ArrowUpFromLine } from 'lucide-react'
import { Card, Badge } from '../ui'
import { PROVIDER_META } from './ProviderLogos.jsx'

/** Compact context-window label: 1_000_000 → "1M", 200_000 → "200K". */
function contextLabel(tokens) {
  if (!tokens) return '—'
  if (tokens >= 1_000_000) return `${+(tokens / 1_000_000).toFixed(2)}M`
  if (tokens >= 1_000) return `${Math.round(tokens / 1_000)}K`
  return String(tokens)
}

/** Price figure: base (muted, struck) over our price (prominent) + a +5% chip. */
function PriceCell({ base, ours, format, align = 'right' }) {
  return (
    <div className={`flex flex-col gap-0.5 ${align === 'right' ? 'items-end' : 'items-start'}`}>
      <div className="flex items-center gap-1.5">
        <span className="font-mono text-sm font-semibold text-[var(--text)] tabular-nums leading-none">
          {format(ours, { maximumFractionDigits: 4 })}
        </span>
        <span className="text-[9px] font-mono font-semibold text-[#2DD4BF] bg-[#2DD4BF]/10 border border-[#2DD4BF]/25 rounded px-1 py-px leading-none">
          +5%
        </span>
      </div>
      <span className="font-mono text-[11px] text-[var(--text-faint)] tabular-nums leading-none line-through decoration-[var(--text-faint)]/50">
        {format(base, { maximumFractionDigits: 4 })}
      </span>
    </div>
  )
}

export default function ModelPriceTable({ provider, models, format, cheapestId }) {
  const meta = PROVIDER_META[provider]
  if (!meta || models.length === 0) return null
  const { label, Mark, accent, tint, border, brandColor } = meta

  return (
    <Card padding="none" className="overflow-hidden border-[var(--border2)]">
      {/* Provider header */}
      <div
        className="flex items-center gap-3 px-5 md:px-6 py-4 border-b border-[var(--border)]"
        style={{ background: tint }}
      >
        <span
          className="w-9 h-9 shrink-0 rounded-[var(--radius-badge)] flex items-center justify-center border"
          style={{ background: 'var(--bg-surface)', borderColor: border }}
        >
          <span style={brandColor ? { color: accent } : undefined} className="inline-flex">
            <Mark size={20} />
          </span>
        </span>
        <div>
          <h3 className="font-display text-base font-semibold text-[var(--text)] leading-none">{label}</h3>
          <p className="text-[11px] text-[var(--text-faint)] mt-1">
            {models.length} model{models.length === 1 ? '' : 's'} · metered at list price + 5%
          </p>
        </div>
      </div>

      {/* Desktop table header */}
      <div className="hidden sm:grid grid-cols-[minmax(0,1fr)_auto_auto_auto] gap-x-6 px-6 py-2.5 border-b border-[var(--border)] bg-[var(--bg-surface2)]/30">
        <span className="text-[10px] font-mono uppercase tracking-wider text-[var(--text-faint)]">Model</span>
        <span className="text-[10px] font-mono uppercase tracking-wider text-[var(--text-faint)] text-right">Context</span>
        <span className="text-[10px] font-mono uppercase tracking-wider text-[var(--text-faint)] text-right flex items-center gap-1 justify-end">
          <ArrowDownToLine size={11} /> in / M
        </span>
        <span className="text-[10px] font-mono uppercase tracking-wider text-[var(--text-faint)] text-right flex items-center gap-1 justify-end">
          <ArrowUpFromLine size={11} /> out / M
        </span>
      </div>

      {/* Rows */}
      <div className="flex flex-col divide-y divide-[var(--border)]">
        {models.map((m) => {
          const cheapest = m.id === cheapestId
          return (
            <div
              key={m.id}
              className="px-5 md:px-6 py-4 transition-colors hover:bg-[var(--bg-surface2)]/40"
              style={cheapest ? { background: 'rgba(45,212,191,0.05)' } : undefined}
            >
              {/* Desktop layout */}
              <div className="hidden sm:grid grid-cols-[minmax(0,1fr)_auto_auto_auto] gap-x-6 items-center">
                <div className="min-w-0 flex items-center gap-2">
                  <span className="font-display text-sm font-semibold text-[var(--text)] truncate">{m.displayName}</span>
                  {cheapest && <Badge color="teal">Cheapest</Badge>}
                  {m.tier === 'flagship' && <Badge color="indigo">Flagship</Badge>}
                </div>
                <span className="font-mono text-xs text-[var(--text-muted)] tabular-nums text-right self-center">
                  {contextLabel(m.contextTokens)}
                </span>
                <PriceCell base={m.inputUsdPerMTok} ours={m.ourInputUsdPerMTok} format={format} />
                <PriceCell base={m.outputUsdPerMTok} ours={m.ourOutputUsdPerMTok} format={format} />
              </div>

              {/* Mobile layout */}
              <div className="sm:hidden flex flex-col gap-3">
                <div className="flex items-center gap-2 flex-wrap">
                  <span className="font-display text-sm font-semibold text-[var(--text)]">{m.displayName}</span>
                  {cheapest && <Badge color="teal">Cheapest</Badge>}
                  {m.tier === 'flagship' && <Badge color="indigo">Flagship</Badge>}
                  <span className="ml-auto font-mono text-[11px] text-[var(--text-faint)]">
                    {contextLabel(m.contextTokens)} ctx
                  </span>
                </div>
                <div className="grid grid-cols-2 gap-3">
                  <div className="rounded-[var(--radius-badge)] border border-[var(--border)] bg-[var(--bg-surface2)]/40 px-3 py-2">
                    <span className="flex items-center gap-1 text-[10px] font-mono uppercase tracking-wider text-[var(--text-faint)] mb-1.5">
                      <ArrowDownToLine size={11} /> in / M
                    </span>
                    <PriceCell base={m.inputUsdPerMTok} ours={m.ourInputUsdPerMTok} format={format} align="left" />
                  </div>
                  <div className="rounded-[var(--radius-badge)] border border-[var(--border)] bg-[var(--bg-surface2)]/40 px-3 py-2">
                    <span className="flex items-center gap-1 text-[10px] font-mono uppercase tracking-wider text-[var(--text-faint)] mb-1.5">
                      <ArrowUpFromLine size={11} /> out / M
                    </span>
                    <PriceCell base={m.outputUsdPerMTok} ours={m.ourOutputUsdPerMTok} format={format} align="left" />
                  </div>
                </div>
              </div>
            </div>
          )
        })}
      </div>
    </Card>
  )
}
