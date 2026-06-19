/**
 * DocsHome — the landing experience at /docs (no slug).
 *
 * A hero (what the docs cover + the wedge one-liner), a client-side search box
 * that filters doc cards, category sections of cards, and a pair of CTAs.
 *
 * Receives the already-fetched doc list (slug, title, category, summary, order)
 * from the Docs page so there's a single source of truth for the index.
 */
import { useMemo, useState, createElement } from 'react'
import { Link } from 'react-router-dom'
import { Search, ArrowRight, Code2, BookOpen, X } from 'lucide-react'
import {
  CATEGORY_META,
  groupByCategory,
  iconForSlug,
} from './docMeta.jsx'

// ── Doc card ──────────────────────────────────────────────────────────────────

function DocCard({ doc }) {
  const icon = iconForSlug(doc.slug, doc.title)
  return (
    <Link
      to={`/docs/${doc.slug}`}
      className="group relative flex flex-col gap-2 rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-4 transition-all duration-200 hover:border-[var(--border2)] hover:-translate-y-0.5"
      style={{ boxShadow: '0 1px 2px rgba(0,0,0,0.12)' }}
    >
      {/* hover sheen */}
      <span
        aria-hidden="true"
        className="pointer-events-none absolute inset-0 rounded-xl opacity-0 transition-opacity duration-200 group-hover:opacity-100"
        style={{ background: 'linear-gradient(135deg, rgba(45,212,191,0.06), rgba(99,102,241,0.04))' }}
      />
      <div className="relative flex items-center gap-2.5">
        <span
          className="flex items-center justify-center w-8 h-8 shrink-0 rounded-lg border border-[var(--border)] bg-[var(--bg-surface2)] text-[var(--brand-teal)] transition-colors group-hover:border-[var(--brand-teal)]/40"
        >
          {createElement(icon, { size: 15, strokeWidth: 1.75 })}
        </span>
        <span className="font-display text-[0.95rem] font-semibold text-[var(--text)] leading-tight">
          {doc.title}
        </span>
      </div>
      {doc.summary && (
        <p className="relative text-[0.8125rem] leading-relaxed text-[var(--text-muted)]">
          {doc.summary}
        </p>
      )}
      <span className="relative mt-auto flex items-center gap-1 pt-1 text-[11px] font-mono uppercase tracking-widest text-[var(--text-faint)] transition-colors group-hover:text-[var(--brand-teal)]">
        Read
        <ArrowRight size={11} className="transition-transform duration-200 group-hover:translate-x-0.5" />
      </span>
    </Link>
  )
}

// ── Category section ──────────────────────────────────────────────────────────

function CategorySection({ category, docs }) {
  const meta = CATEGORY_META[category] ?? {}
  const icon = meta.icon ?? BookOpen
  return (
    <section className="mb-12">
      <div className="flex items-center gap-3 mb-4">
        <span className="flex items-center justify-center w-7 h-7 rounded-lg text-[var(--brand-teal)]">
          {createElement(icon, { size: 16, strokeWidth: 1.75 })}
        </span>
        <div className="min-w-0">
          <h2 className="font-display text-[1.15rem] font-semibold text-[var(--text)] leading-tight">
            {category}
          </h2>
          {meta.blurb && (
            <p className="text-xs text-[var(--text-faint)] leading-snug">{meta.blurb}</p>
          )}
        </div>
        <span className="ml-auto text-[11px] font-mono text-[var(--text-faint)] tabular-nums">
          {docs.length}
        </span>
      </div>
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {docs.map((d) => (
          <DocCard key={d.slug} doc={d} />
        ))}
      </div>
    </section>
  )
}

// ── Home ──────────────────────────────────────────────────────────────────────

export default function DocsHome({ docs = [] }) {
  const [query, setQuery] = useState('')

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return docs
    return docs.filter((d) =>
      `${d.title} ${d.summary ?? ''} ${d.category ?? ''} ${d.slug}`.toLowerCase().includes(q)
    )
  }, [docs, query])

  const sections = useMemo(() => groupByCategory(filtered), [filtered])
  const hasResults = filtered.length > 0

  return (
    <div className="mx-auto px-4 sm:px-6 pb-24" style={{ maxWidth: '1080px' }}>

      {/* ── Hero ─────────────────────────────────────────────────────────── */}
      <header className="relative pt-14 pb-10 md:pt-20 md:pb-12 text-center">
        {/* ambient glow */}
        <div
          aria-hidden="true"
          className="pointer-events-none absolute inset-x-0 -top-10 h-64"
          style={{
            background:
              'radial-gradient(ellipse 60% 100% at 50% 0%, rgba(45,212,191,0.10), transparent 70%)',
          }}
        />
        <div className="relative">
          <span className="inline-flex items-center gap-2 rounded-full border border-[var(--border)] bg-[var(--bg-surface)] px-3 py-1 text-[10px] font-mono uppercase tracking-[0.18em] text-[var(--text-muted)]">
            <BookOpen size={11} className="text-[var(--brand-teal)]" />
            Documentation
          </span>
          <h1 className="mt-5 font-display text-[2.25rem] md:text-[2.75rem] font-semibold leading-[1.1] tracking-tight text-[var(--text)]">
            The project tracker
            <br className="hidden sm:block" />{' '}
            <span className="gradient-text">nobody updates by hand.</span>
          </h1>
          <p className="mx-auto mt-4 max-w-xl text-[0.95rem] leading-relaxed text-[var(--text-muted)]">
            gitstate derives true project state, effort, and invoices directly from git. These docs
            cover the concepts, the guides for each surface, and the full API &amp; ops reference.
          </p>

          {/* search */}
          <div className="mx-auto mt-7 max-w-md">
            <div className="relative">
              <Search
                size={15}
                className="absolute left-3.5 top-1/2 -translate-y-1/2 text-[var(--text-faint)]"
              />
              <input
                type="text"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Search the docs…"
                aria-label="Search documentation"
                className="w-full rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] py-2.5 pl-10 pr-9 text-sm text-[var(--text)] placeholder:text-[var(--text-faint)] outline-none transition-colors focus:border-[var(--brand-teal)]/60"
              />
              {query && (
                <button
                  onClick={() => setQuery('')}
                  aria-label="Clear search"
                  className="absolute right-2.5 top-1/2 -translate-y-1/2 flex h-6 w-6 items-center justify-center rounded-md text-[var(--text-faint)] hover:text-[var(--text)] hover:bg-[var(--bg-surface2)] transition-colors"
                >
                  <X size={13} />
                </button>
              )}
            </div>
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
              className="inline-flex items-center gap-2 rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-4 py-2 text-sm font-medium text-[var(--text-muted)] transition-colors hover:text-[var(--text)] hover:border-[var(--border2)]"
            >
              <Code2 size={14} className="text-[var(--brand-teal)]" />
              Browse the API reference
            </Link>
          </div>
        </div>
      </header>

      {/* ── Sections ─────────────────────────────────────────────────────── */}
      {hasResults ? (
        sections.map((s) => (
          <CategorySection key={s.category} category={s.category} docs={s.docs} />
        ))
      ) : (
        <div className="py-16 text-center">
          <p className="text-sm text-[var(--text-muted)]">
            No docs match <span className="font-mono text-[var(--text)]">&ldquo;{query}&rdquo;</span>.
          </p>
          <button
            onClick={() => setQuery('')}
            className="mt-3 text-sm font-medium text-[var(--brand-teal)] hover:underline"
          >
            Clear search
          </button>
        </div>
      )}
    </div>
  )
}
