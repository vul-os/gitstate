/**
 * ModelPricing — public, unauthenticated "Transparent AI pricing" page (/models).
 *
 * Story: there is no per-seat AI fee. You pay each model at its provider's
 * published rate plus a flat 5% handling margin (payments + FX), with a monthly
 * AI credit per plan and a BYOK escape hatch. Every model's BASE provider rate
 * and OUR rate (+5%) are shown side by side, grouped by provider with real brand
 * marks, sortable / filterable, currency-aware.
 *
 * Data: GET /api/models (lib/api.js → fetchModels). Degrades to a curated
 * fallback (components/pricing/modelData.js) when the endpoint 404s/errors, so
 * the page always renders. our* = base × 1.05 (recomputed defensively).
 *
 * Wrapped by MarketingLayout (shared nav + footer + ThemeToggle + CurrencySelector).
 */
import { useEffect, useMemo, useState } from 'react'
import {
  Sparkles, ArrowRight, KeyRound, Wallet, Percent, Gauge, ArrowUpDown, ShieldCheck,
} from 'lucide-react'
import { Link } from 'react-router-dom'
import MarketingLayout from '../components/marketing/MarketingLayout.jsx'
import { Card, Button, Badge, GradientText, Section, Container, Glow } from '../components/ui'
import { Reveal } from '../components/Reveal.jsx'
import { CurrencySelector } from '../components/CurrencySelector.jsx'
import { useCurrency } from '../lib/currency.jsx'
import { fetchModels } from '../lib/api.js'
import {
  FALLBACK_MODELS, normalizeModel, PROVIDER_ORDER,
} from '../components/pricing/modelData.js'
import { PROVIDER_META, ProviderMark } from '../components/pricing/ProviderLogos.jsx'
import ModelPriceTable from '../components/pricing/ModelPriceTable.jsx'

const SORTS = [
  { key: 'provider', label: 'Provider' },
  { key: 'input', label: 'Input price' },
  { key: 'output', label: 'Output price' },
]

// ── How-billing-works steps ──────────────────────────────────────────────────
const BILLING_STEPS = [
  {
    icon: Sparkles,
    title: 'Monthly AI credit',
    body: 'Every paid plan includes a pooled managed-AI credit per builder. Usage draws this down first — most teams never go past it.',
  },
  {
    icon: Wallet,
    title: 'Wallet & overage',
    body: 'Past the credit, usage is metered against your wallet at the rate shown here — the model’s list price plus a flat 5%. Top up anytime; never a per-seat AI fee.',
  },
  {
    icon: KeyRound,
    title: 'BYOK = $0 managed',
    body: 'Bring your own Anthropic, OpenAI, or Google key and route calls straight to the provider. You pay them directly; we add nothing.',
  },
]

export default function ModelPricing() {
  const { format, currency } = useCurrency()
  const [models, setModels] = useState(null) // null = loading
  const [usedFallback, setUsedFallback] = useState(false)
  const [providerFilter, setProviderFilter] = useState('all')
  const [sort, setSort] = useState('provider')

  // Load the live catalog; fall back to the curated list on any failure.
  useEffect(() => {
    let cancelled = false
    fetchModels()
      .then((data) => {
        if (cancelled) return
        if (Array.isArray(data) && data.length > 0) {
          setModels(data.map(normalizeModel))
          setUsedFallback(false)
        } else {
          setModels(FALLBACK_MODELS)
          setUsedFallback(true)
        }
      })
      .catch(() => {
        if (cancelled) return
        setModels(FALLBACK_MODELS)
        setUsedFallback(true)
      })
    return () => { cancelled = true }
  }, [])

  const rows = models ?? FALLBACK_MODELS

  // Cheapest by our blended input+output rate, across all providers.
  const cheapestId = useMemo(() => {
    let best = null
    for (const m of rows) {
      const score = m.ourInputUsdPerMTok + m.ourOutputUsdPerMTok
      if (best === null || score < best.score) best = { id: m.id, score }
    }
    return best?.id ?? null
  }, [rows])

  // Group → filter → sort within each provider.
  const grouped = useMemo(() => {
    const order = PROVIDER_ORDER.filter((p) => providerFilter === 'all' || p === providerFilter)
    return order.map((provider) => {
      const list = rows.filter((m) => m.provider === provider)
      const sorted = [...list].sort((a, b) => {
        if (sort === 'input') return a.ourInputUsdPerMTok - b.ourInputUsdPerMTok
        if (sort === 'output') return a.ourOutputUsdPerMTok - b.ourOutputUsdPerMTok
        return 0 // provider sort keeps curated order within group
      })
      return { provider, models: sorted }
    }).filter((g) => g.models.length > 0)
  }, [rows, providerFilter, sort])

  const loading = models === null

  return (
    <MarketingLayout>
      {/* ── Hero ── */}
      <Section py="2xl" className="relative overflow-hidden grain">
        <div className="absolute inset-0 ambient-brand pointer-events-none" />
        <Glow variant="brand" size={820} className="-top-24 left-1/2 opacity-70" />
        <Glow variant="indigo" size={460} className="top-1/2 right-0 opacity-30" />
        <Container size="lg" className="relative z-10 text-center">
          <Reveal>
            <div className="inline-flex items-center gap-2 mb-6 px-3 py-1 rounded-full border border-[var(--border2)] bg-[var(--bg-surface)]/60 backdrop-blur-sm">
              <span className="w-1.5 h-1.5 rounded-full bg-[#2DD4BF] shadow-[0_0_8px_#2DD4BF]" />
              <span className="text-xs font-mono text-[var(--text-muted)]">Provider list price + a flat 5% · no per-seat AI fee</span>
            </div>
          </Reveal>
          <Reveal delay={0.08}>
            <h1 className="font-display text-5xl md:text-6xl font-semibold leading-[1.05] tracking-[-0.02em] text-[var(--text)] mb-5">
              Transparent{' '}
              <GradientText as="span" className="font-display text-5xl md:text-6xl font-semibold leading-[1.05] tracking-[-0.02em]">
                AI pricing
              </GradientText>
            </h1>
          </Reveal>
          <Reveal delay={0.16}>
            <p className="text-[var(--text-muted)] text-lg max-w-2xl mx-auto mb-2">
              You pay each model at the provider&apos;s published rate plus a flat 5% handling margin —
              nothing added per seat. A monthly AI credit is included with every plan, and you can always
              bring your own key.
            </p>
          </Reveal>
          <Reveal delay={0.22}>
            <p className="text-xs font-mono text-[var(--text-faint)] mb-9">
              Rates per 1M tokens · anchored in USD · shown in {currency.code} · the 5% covers payments &amp; FX
            </p>
          </Reveal>

          <Reveal delay={0.28}>
            <div className="flex flex-wrap items-center justify-center gap-3">
              <span className="inline-flex items-center gap-2 text-xs text-[var(--text-muted)] px-3 py-1.5 rounded-full border border-[var(--border)] bg-[var(--bg-surface)]/50">
                <Gauge size={13} className="text-[#2DD4BF]" /> Display currency
              </span>
              <CurrencySelector />
            </div>
          </Reveal>

          {/* Provider chips */}
          <Reveal delay={0.36}>
            <div className="flex flex-wrap items-center justify-center gap-x-6 gap-y-2 mt-9">
              {PROVIDER_ORDER.map((p) => (
                <span key={p} className="inline-flex items-center gap-2 text-xs text-[var(--text-muted)]">
                  <ProviderMark provider={p} size={15} />
                  {PROVIDER_META[p].label}
                </span>
              ))}
            </div>
          </Reveal>
        </Container>
      </Section>

      {/* ── Controls + provider groups ── */}
      <Section py="md">
        <Container size="lg">
          {/* Notice when running on curated fallback data */}
          {usedFallback && !loading && (
            <Reveal inView>
              <div className="mb-6 px-4 py-3 rounded-[var(--radius-badge)] border border-yellow-500/20 bg-yellow-500/5 text-xs text-yellow-400 font-mono flex items-center gap-2">
                <ShieldCheck size={13} className="shrink-0" />
                Showing curated reference prices — live rates were unavailable. The +5% margin still applies exactly.
              </div>
            </Reveal>
          )}

          {/* Filter + sort toolbar */}
          <Reveal inView>
            <div className="flex flex-col sm:flex-row sm:items-center gap-3 mb-7">
              {/* Provider filter */}
              <div className="inline-flex p-1 rounded-full border border-[var(--border)] bg-[var(--bg-surface)]/70 backdrop-blur-sm self-start sm:self-auto">
                {['all', ...PROVIDER_ORDER].map((p) => {
                  const active = providerFilter === p
                  const meta = p === 'all' ? null : PROVIDER_META[p]
                  return (
                    <button
                      key={p}
                      onClick={() => setProviderFilter(p)}
                      className={[
                        'inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full text-xs font-medium transition-all duration-200 cursor-pointer',
                        active
                          ? 'text-[var(--text)] bg-[var(--bg-surface3)] border border-[var(--border2)]'
                          : 'text-[var(--text-muted)] hover:text-[var(--text)] border border-transparent',
                      ].join(' ')}
                    >
                      {meta && <ProviderMark provider={p} size={13} />}
                      {meta ? meta.label : 'All providers'}
                    </button>
                  )
                })}
              </div>

              {/* Sort */}
              <div className="inline-flex items-center gap-2 sm:ml-auto self-start sm:self-auto">
                <ArrowUpDown size={13} className="text-[var(--text-faint)]" />
                <span className="text-[11px] font-mono uppercase tracking-wider text-[var(--text-faint)]">Sort</span>
                <div className="inline-flex p-1 rounded-full border border-[var(--border)] bg-[var(--bg-surface)]/70 backdrop-blur-sm">
                  {SORTS.map(({ key, label }) => {
                    const active = sort === key
                    return (
                      <button
                        key={key}
                        onClick={() => setSort(key)}
                        className={[
                          'px-3 py-1.5 rounded-full text-xs font-medium transition-all duration-200 cursor-pointer',
                          active
                            ? 'text-[#0B1120]'
                            : 'text-[var(--text-muted)] hover:text-[var(--text)]',
                        ].join(' ')}
                        style={active ? { background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' } : undefined}
                      >
                        {label}
                      </button>
                    )
                  })}
                </div>
              </div>
            </div>
          </Reveal>

          {/* Loading skeletons */}
          {loading ? (
            <div className="flex flex-col gap-6">
              {Array.from({ length: 3 }).map((_, i) => (
                <Card key={i} padding="lg" className="animate-pulse">
                  <div className="flex items-center gap-3 mb-5">
                    <div className="h-9 w-9 rounded-[var(--radius-badge)] bg-[var(--bg-surface2)]" />
                    <div className="h-4 w-28 rounded bg-[var(--bg-surface2)]" />
                  </div>
                  {Array.from({ length: 3 }).map((_, j) => (
                    <div key={j} className="h-10 w-full rounded bg-[var(--bg-surface2)] mb-2.5" />
                  ))}
                </Card>
              ))}
            </div>
          ) : (
            <div className="flex flex-col gap-6">
              {grouped.map(({ provider, models: list }, i) => (
                <Reveal key={provider} inView delay={i * 0.06}>
                  <ModelPriceTable
                    provider={provider}
                    models={list}
                    format={format}
                    cheapestId={cheapestId}
                  />
                </Reveal>
              ))}
            </div>
          )}

          <Reveal inView delay={0.1}>
            <p className="text-center text-[11px] font-mono text-[var(--text-faint)] mt-8 flex items-center justify-center gap-1.5 flex-wrap">
              <Percent size={11} className="text-[#2DD4BF]" />
              Strikethrough = provider list price · prominent figure = your managed rate (+5%) · billed in {currency.code} at checkout.
            </p>
          </Reveal>
        </Container>
      </Section>

      {/* ── How billing works ── */}
      <Section py="lg">
        <Container size="lg">
          <Reveal inView>
            <div className="mb-8 text-center">
              <Badge color="teal" className="mb-3 inline-flex items-center gap-1">
                <Wallet size={11} /> how billing works
              </Badge>
              <h2 className="font-display text-2xl md:text-3xl font-semibold text-[var(--text)] mb-2">
                Allowance, then wallet — never a seat tax
              </h2>
              <p className="text-sm text-[var(--text-muted)] max-w-lg mx-auto">
                AI is included up to your monthly credit, then metered at the rate above. The flat 5%
                covers payment processing and FX — nothing more.
              </p>
            </div>
          </Reveal>

          <div className="grid grid-cols-1 md:grid-cols-3 gap-5">
            {BILLING_STEPS.map(({ icon: Icon, title, body }, i) => (
              <Reveal key={title} inView delay={i * 0.08}>
                <Card padding="lg" hoverable className="h-full border-[var(--border2)]">
                  <div className="w-10 h-10 rounded-[var(--radius-badge)] bg-[#2DD4BF]/10 border border-[#2DD4BF]/25 flex items-center justify-center mb-4">
                    <Icon size={18} className="text-[#2DD4BF]" strokeWidth={1.8} />
                  </div>
                  <h3 className="font-display text-base font-semibold text-[var(--text)] mb-1.5">{title}</h3>
                  <p className="text-sm text-[var(--text-muted)] leading-relaxed">{body}</p>
                </Card>
              </Reveal>
            ))}
          </div>
        </Container>
      </Section>

      {/* ── CTA band ── */}
      <Section py="2xl" className="relative overflow-hidden">
        <Container size="md" className="relative z-10">
          <Reveal inView>
            <div className="relative overflow-hidden rounded-[var(--radius-card)] border border-[var(--border2)] bg-[var(--bg-surface)] px-8 py-12 md:px-14 md:py-16 text-center grain">
              <div className="absolute inset-0 ambient-brand pointer-events-none" />
              <Glow variant="teal" size={480} className="top-0 left-1/4 opacity-50" />
              <Glow variant="indigo" size={420} className="bottom-0 right-1/4 opacity-40" />
              <div className="relative z-10">
                <span className="inline-flex items-center gap-1.5 mb-5 px-3 py-1 rounded-full border border-[#2DD4BF]/25 bg-[#2DD4BF]/[0.06] text-xs font-mono text-[#2DD4BF]">
                  <Sparkles size={12} /> AI at cost + 5% · or BYOK for $0 managed
                </span>
                <GradientText as="h2" className="font-display text-3xl md:text-4xl font-semibold mb-4 leading-tight">
                  See the plans these credits ride on
                </GradientText>
                <p className="text-[var(--text-muted)] mb-8 max-w-md mx-auto">
                  Every paid plan bundles a monthly AI credit per builder. Pick a plan, or start free and
                  add a key later — no contracts, cancel anytime.
                </p>
                <div className="flex flex-wrap items-center justify-center gap-3">
                  <Link to="/pricing">
                    <Button variant="primary" size="lg" rightIcon={<ArrowRight size={16} />}>
                      View plan pricing
                    </Button>
                  </Link>
                  <Link to="/signup">
                    <Button variant="outline" size="lg" leftIcon={<Sparkles size={15} />}>
                      Start free
                    </Button>
                  </Link>
                </div>
              </div>
            </div>
          </Reveal>
        </Container>
      </Section>
    </MarketingLayout>
  )
}
