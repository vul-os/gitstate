/**
 * DocsHome — the landing experience at /docs (no slug).
 *
 * A hero (the wedge one-liner + a framed product shot), a "Start here" rail of
 * popular pages, a refined client-side search (focus with `/`, highlighted
 * matches, keyboard nav), category sections of cards, and a closing help band.
 *
 * Receives the already-fetched doc list (slug, title, category, summary, order)
 * from the Docs page so there's a single source of truth for the index.
 */
import { useMemo, useState, useRef, useEffect, createElement } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import {
  Search,
  ArrowRight,
  Code2,
  BookOpen,
  X,
  CornerDownLeft,
  GitMerge,
  Sparkles,
  Terminal,
  Compass,
} from 'lucide-react'
import {
  CATEGORY_META,
  groupByTier,
  iconForSlug,
} from './docMeta.jsx'

// Pages worth surfacing on the "Start here" rail (in display order).
const POPULAR = [
  { slug: 'quickstart', tag: 'Set up', icon: Terminal },
  { slug: 'overview', tag: 'Orient', icon: Compass },
  { slug: 'derived-state', tag: 'Core idea', icon: GitMerge },
  { slug: 'effort-and-estimation', tag: 'LLM effort', icon: Sparkles },
  { slug: 'api-reference', tag: 'Build', icon: Code2 },
]

// ── match highlighter ─────────────────────────────────────────────────────────

function Highlight({ text, query }) {
  const q = query.trim()
  if (!q || !text) return text
  const lower = text.toLowerCase()
  const ql = q.toLowerCase()
  const out = []
  let i = 0
  let key = 0
  while (i < text.length) {
    const at = lower.indexOf(ql, i)
    if (at === -1) {
      out.push(text.slice(i))
      break
    }
    if (at > i) out.push(text.slice(i, at))
    out.push(
      <mark
        key={key++}
        className="rounded-[3px] bg-[var(--brand-teal)]/20 px-0.5 text-[var(--text)]"
        style={{ color: 'inherit' }}
      >
        {text.slice(at, at + q.length)}
      </mark>
    )
    i = at + q.length
  }
  return out
}

// ── Doc card ──────────────────────────────────────────────────────────────────

function DocCard({ doc, query }) {
  const icon = iconForSlug(doc.slug, doc.title)
  return (
    <Link
      to={`/docs/${doc.slug}`}
      className="group relative flex flex-col gap-2 overflow-hidden rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-4 transition-all duration-200 hover:-translate-y-0.5 hover:border-[var(--border2)] hover:shadow-lg"
      style={{ boxShadow: 'var(--shadow-card)' }}
    >
      {/* hover sheen */}
      <span
        aria-hidden="true"
        className="pointer-events-none absolute inset-0 rounded-xl opacity-0 transition-opacity duration-200 group-hover:opacity-100"
        style={{ background: 'linear-gradient(135deg, rgba(45,212,191,0.07), rgba(99,102,241,0.045))' }}
      />
      <div className="relative flex items-center gap-2.5">
        <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg border border-[var(--border)] bg-[var(--bg-surface2)] text-[var(--brand-teal)] transition-colors group-hover:border-[var(--brand-teal)]/40">
          {createElement(icon, { size: 15, strokeWidth: 1.75 })}
        </span>
        <span className="font-display text-[0.95rem] font-semibold leading-tight text-[var(--text)]">
          <Highlight text={doc.title} query={query} />
        </span>
      </div>
      {doc.summary && (
        <p className="relative text-[0.8125rem] leading-relaxed text-[var(--text-muted)]">
          <Highlight text={doc.summary} query={query} />
        </p>
      )}
      <span className="relative mt-auto flex items-center gap-1 pt-1 font-mono text-[11px] uppercase tracking-widest text-[var(--text-faint)] transition-colors group-hover:text-[var(--brand-teal)]">
        Read
        <ArrowRight size={11} className="transition-transform duration-200 group-hover:translate-x-0.5" />
      </span>
    </Link>
  )
}

// ── Category section ──────────────────────────────────────────────────────────

function CategorySection({ category, docs, query }) {
  const meta = CATEGORY_META[category] ?? {}
  const icon = meta.icon ?? BookOpen
  return (
    <section className="mb-12 scroll-mt-24">
      <div className="mb-4 flex items-center gap-3">
        <span className="flex h-8 w-8 items-center justify-center rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] text-[var(--brand-teal)]">
          {createElement(icon, { size: 16, strokeWidth: 1.75 })}
        </span>
        <div className="min-w-0">
          <h2 className="font-display text-[1.15rem] font-semibold leading-tight text-[var(--text)]">
            {category}
          </h2>
          {meta.blurb && <p className="text-xs leading-snug text-[var(--text-faint)]">{meta.blurb}</p>}
        </div>
        <span className="ml-auto rounded-full border border-[var(--border)] bg-[var(--bg-surface)] px-2 py-0.5 font-mono text-[11px] tabular-nums text-[var(--text-faint)]">
          {docs.length}
        </span>
      </div>
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {docs.map((d) => (
          <DocCard key={d.slug} doc={d} query={query} />
        ))}
      </div>
    </section>
  )
}

// ── Tier divider ────────────────────────────────────────────────────────────────

function TierHeader({ tier, blurb, icon, count }) {
  const Icon = icon ?? BookOpen
  return (
    <div className="mb-8 mt-2">
      <div className="flex items-center gap-3">
        <span className="flex h-9 w-9 items-center justify-center rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] text-[var(--brand-teal)]">
          {createElement(Icon, { size: 18, strokeWidth: 1.75 })}
        </span>
        <div className="min-w-0">
          <h2 className="font-display text-[1.35rem] font-semibold leading-tight tracking-tight text-[var(--text)]">
            {tier}
          </h2>
          {blurb && <p className="text-[0.8125rem] leading-snug text-[var(--text-muted)]">{blurb}</p>}
        </div>
        {typeof count === 'number' && (
          <span className="ml-auto rounded-full border border-[var(--border)] bg-[var(--bg-surface)] px-2.5 py-0.5 font-mono text-[11px] tabular-nums text-[var(--text-faint)]">
            {count}
          </span>
        )}
      </div>
      <div
        className="mt-4 h-px w-full"
        style={{ background: 'linear-gradient(90deg, var(--brand-teal), transparent 60%)', opacity: 0.5 }}
      />
    </div>
  )
}

// ── Home ──────────────────────────────────────────────────────────────────────

export default function DocsHome({ docs = [] }) {
  const [query, setQuery] = useState('')
  const inputRef = useRef(null)
  const navigate = useNavigate()

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return docs
    return docs.filter((d) =>
      `${d.title} ${d.summary ?? ''} ${d.category ?? ''} ${d.slug}`.toLowerCase().includes(q)
    )
  }, [docs, query])

  const tiers = useMemo(() => groupByTier(filtered), [filtered])
  const hasResults = filtered.length > 0
  const searching = query.trim().length > 0

  const bySlug = useMemo(() => {
    const m = new Map()
    for (const d of docs) m.set(d.slug, d)
    return m
  }, [docs])
  const popular = POPULAR.map((p) => ({ ...p, doc: bySlug.get(p.slug) })).filter((p) => p.doc)

  // Global "/" focuses search; Escape clears + blurs.
  useEffect(() => {
    const onKey = (e) => {
      const tag = document.activeElement?.tagName
      const typing = tag === 'INPUT' || tag === 'TEXTAREA' || document.activeElement?.isContentEditable
      if (e.key === '/' && !typing && !e.metaKey && !e.ctrlKey) {
        e.preventDefault()
        inputRef.current?.focus()
      } else if (e.key === 'Escape' && document.activeElement === inputRef.current) {
        setQuery('')
        inputRef.current?.blur()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  // Enter in the search box jumps to the top result.
  const onSearchKeyDown = (e) => {
    if (e.key === 'Enter' && searching && filtered[0]) {
      e.preventDefault()
      navigate(`/docs/${filtered[0].slug}`)
    }
  }

  return (
    <div className="mx-auto px-4 pb-24 sm:px-6" style={{ maxWidth: '1080px' }}>

      {/* ── Hero ─────────────────────────────────────────────────────────── */}
      <header className="relative pt-14 md:pt-20">
        {/* ambient glow */}
        <div
          aria-hidden="true"
          className="pointer-events-none absolute inset-x-0 -top-12 h-72"
          style={{
            background:
              'radial-gradient(ellipse 55% 100% at 50% 0%, rgba(45,212,191,0.11), transparent 70%), radial-gradient(ellipse 40% 80% at 78% 10%, rgba(99,102,241,0.08), transparent 70%)',
          }}
        />
        <div className="relative text-center">
          <span className="inline-flex items-center gap-2 rounded-full border border-[var(--border)] bg-[var(--bg-surface)] px-3 py-1 font-mono text-[10px] uppercase tracking-[0.18em] text-[var(--text-muted)]">
            <BookOpen size={11} className="text-[var(--brand-teal)]" />
            Documentation
          </span>
          <h1 className="mx-auto mt-5 max-w-2xl font-display text-[2.25rem] font-semibold leading-[1.08] tracking-tight text-[var(--text)] md:text-[2.9rem]">
            The project tracker
            <br className="hidden sm:block" />{' '}
            <span className="gradient-text">nobody updates by hand.</span>
          </h1>
          <p className="mx-auto mt-4 max-w-xl text-[0.95rem] leading-relaxed text-[var(--text-muted)]">
            gitstate derives true project state, effort, and invoices directly from git. These docs
            cover the concepts, a guide for each surface, and the full API &amp; ops reference.
          </p>

          {/* search */}
          <div className="mx-auto mt-7 max-w-lg">
            <div className="group relative">
              <Search
                size={16}
                className="pointer-events-none absolute left-4 top-1/2 -translate-y-1/2 text-[var(--text-faint)] transition-colors group-focus-within:text-[var(--brand-teal)]"
              />
              <input
                ref={inputRef}
                type="text"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                onKeyDown={onSearchKeyDown}
                placeholder="Search the docs…"
                aria-label="Search documentation"
                className="w-full rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] py-3 pl-11 pr-16 text-sm text-[var(--text)] outline-none transition-all placeholder:text-[var(--text-faint)] focus:border-[var(--brand-teal)]/60 focus:shadow-[0_0_0_3px_rgba(45,212,191,0.10)]"
              />
              {query ? (
                <button
                  onClick={() => { setQuery(''); inputRef.current?.focus() }}
                  aria-label="Clear search"
                  className="absolute right-3 top-1/2 flex h-6 w-6 -translate-y-1/2 items-center justify-center rounded-md text-[var(--text-faint)] transition-colors hover:bg-[var(--bg-surface2)] hover:text-[var(--text)]"
                >
                  <X size={14} />
                </button>
              ) : (
                <kbd className="absolute right-3 top-1/2 hidden h-6 -translate-y-1/2 select-none items-center rounded-md border border-[var(--border)] bg-[var(--bg-surface2)] px-2 font-mono text-[11px] text-[var(--text-faint)] sm:flex">
                  /
                </kbd>
              )}
            </div>
            {searching && hasResults && (
              <p className="mt-2 flex items-center justify-center gap-1.5 font-mono text-[11px] text-[var(--text-faint)]">
                <span>{filtered.length} result{filtered.length === 1 ? '' : 's'}</span>
                <span className="opacity-40">·</span>
                <CornerDownLeft size={11} />
                <span>to open the first</span>
              </p>
            )}
          </div>

          {/* CTAs */}
          <div className="mt-6 flex flex-wrap items-center justify-center gap-3">
            <Link
              to="/docs/quickstart"
              className="group inline-flex items-center gap-2 rounded-lg px-4 py-2 text-sm font-medium text-white transition-transform hover:-translate-y-0.5"
              style={{ background: 'linear-gradient(135deg, #2DD4BF 0%, #6366F1 100%)' }}
            >
              Start here — Quickstart
              <ArrowRight size={14} className="transition-transform duration-200 group-hover:translate-x-0.5" />
            </Link>
            <Link
              to="/docs/api-reference"
              className="inline-flex items-center gap-2 rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-4 py-2 text-sm font-medium text-[var(--text-muted)] transition-colors hover:border-[var(--border2)] hover:text-[var(--text)]"
            >
              <Code2 size={14} className="text-[var(--brand-teal)]" />
              API reference
            </Link>
          </div>
        </div>

        {/* Framed product shot — only when not actively searching */}
        {!searching && (
          <div className="relative mx-auto mt-12 max-w-3xl">
            <div
              aria-hidden="true"
              className="pointer-events-none absolute -inset-x-8 -bottom-6 top-8 rounded-[28px] opacity-60 blur-2xl"
              style={{ background: 'radial-gradient(ellipse at center, rgba(45,212,191,0.12), rgba(99,102,241,0.08) 55%, transparent 75%)' }}
            />
            <div
              className="relative overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--bg-surface)]"
              style={{ boxShadow: 'var(--shadow-card-hover)' }}
            >
              {/* fake window chrome */}
              <div className="flex items-center gap-1.5 border-b border-[var(--border)] bg-[var(--bg-surface2)] px-4 py-2.5">
                <span className="h-2.5 w-2.5 rounded-full" style={{ background: '#ef4444', opacity: 0.65 }} />
                <span className="h-2.5 w-2.5 rounded-full" style={{ background: '#f59e0b', opacity: 0.65 }} />
                <span className="h-2.5 w-2.5 rounded-full" style={{ background: '#22c55e', opacity: 0.65 }} />
                <span className="ml-3 font-mono text-[11px] text-[var(--text-faint)]">gitstate — dashboard</span>
              </div>
              <img
                src="/shots/dashboard.png"
                alt="The gitstate dashboard: derived project state, cycle time, and effort"
                className="block w-full"
                loading="lazy"
              />
            </div>
          </div>
        )}
      </header>

      {/* ── Start here rail ──────────────────────────────────────────────── */}
      {!searching && popular.length > 0 && (
        <section className="mt-14 mb-14">
          <div className="mb-4 flex items-center gap-2">
            <Sparkles size={13} className="text-[var(--brand-teal)]" />
            <h2 className="font-mono text-[11px] uppercase tracking-[0.18em] text-[var(--text-faint)]">
              Start here
            </h2>
          </div>
          <div className="grid gap-2.5 sm:grid-cols-2 lg:grid-cols-5">
            {popular.map(({ doc, tag, icon }) => (
              <Link
                key={doc.slug}
                to={`/docs/${doc.slug}`}
                className="group relative flex flex-col gap-2 overflow-hidden rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-3.5 transition-all duration-200 hover:-translate-y-0.5 hover:border-[var(--brand-teal)]/40"
                style={{ boxShadow: 'var(--shadow-card)' }}
              >
                <span
                  aria-hidden="true"
                  className="pointer-events-none absolute inset-0 opacity-0 transition-opacity duration-200 group-hover:opacity-100"
                  style={{ background: 'linear-gradient(160deg, rgba(45,212,191,0.08), transparent 70%)' }}
                />
                <span className="relative flex h-7 w-7 items-center justify-center rounded-lg border border-[var(--border)] bg-[var(--bg-surface2)] text-[var(--brand-teal)]">
                  {createElement(icon, { size: 14, strokeWidth: 1.75 })}
                </span>
                <span className="relative font-mono text-[9px] uppercase tracking-[0.14em] text-[var(--text-faint)]">
                  {tag}
                </span>
                <span className="relative font-display text-[0.875rem] font-semibold leading-tight text-[var(--text)]">
                  {doc.title}
                </span>
              </Link>
            ))}
          </div>
        </section>
      )}

      {/* ── Tiers → Sections ─────────────────────────────────────────────── */}
      {hasResults ? (
        tiers.map((t) => (
          <div key={t.tier} className="mb-14">
            <TierHeader
              tier={t.tier}
              blurb={t.blurb}
              icon={t.icon}
              count={t.sections.reduce((n, s) => n + s.docs.length, 0)}
            />
            {t.sections.map((s) => (
              <CategorySection key={s.category} category={s.category} docs={s.docs} query={query} />
            ))}
          </div>
        ))
      ) : (
        <div className="py-16 text-center">
          <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] text-[var(--text-faint)]">
            <Search size={18} />
          </div>
          <p className="text-sm text-[var(--text-muted)]">
            No docs match <span className="font-mono text-[var(--text)]">&ldquo;{query}&rdquo;</span>.
          </p>
          <button
            onClick={() => { setQuery(''); inputRef.current?.focus() }}
            className="mt-3 text-sm font-medium text-[var(--brand-teal)] hover:underline"
          >
            Clear search
          </button>
        </div>
      )}

      {/* ── Closing help band ────────────────────────────────────────────── */}
      {!searching && (
        <section className="mt-4 overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--bg-surface)]">
          <div className="relative flex flex-col items-start gap-4 p-6 sm:flex-row sm:items-center sm:justify-between sm:p-7">
            <div
              aria-hidden="true"
              className="pointer-events-none absolute inset-0"
              style={{ background: 'radial-gradient(ellipse 50% 120% at 0% 0%, rgba(99,102,241,0.07), transparent 60%)' }}
            />
            <div className="relative">
              <h3 className="font-display text-[1.05rem] font-semibold text-[var(--text)]">
                Can&apos;t find what you need?
              </h3>
              <p className="mt-1 text-sm text-[var(--text-muted)]">
                Skim the glossary, check the FAQ, or see what&apos;s shipped vs. on the roadmap.
              </p>
            </div>
            <div className="relative flex shrink-0 flex-wrap gap-2">
              <Link to="/docs/glossary" className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface2)] px-3 py-1.5 text-xs font-medium text-[var(--text-muted)] transition-colors hover:border-[var(--border2)] hover:text-[var(--text)]">
                Glossary
              </Link>
              <Link to="/docs/faq" className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface2)] px-3 py-1.5 text-xs font-medium text-[var(--text-muted)] transition-colors hover:border-[var(--border2)] hover:text-[var(--text)]">
                FAQ
              </Link>
              <Link to="/docs/roadmap-and-status" className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface2)] px-3 py-1.5 text-xs font-medium text-[var(--text-muted)] transition-colors hover:border-[var(--border2)] hover:text-[var(--text)]">
                Roadmap
              </Link>
            </div>
          </div>
        </section>
      )}
    </div>
  )
}
