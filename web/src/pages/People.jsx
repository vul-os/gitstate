/**
 * People — the merged-identity roster.
 *
 * gitstate never asks anyone to register — the daemon merges contributor
 * identities automatically wherever it sees the same email in git commit
 * authorship (and forge logins where linked). This page is a straight read of
 * that merge: primary email, every linked alias, and (if applicable) the
 * forge login and agent classification, alongside a commit count pulled from
 * `/api/analytics` and matched back onto each identity's email set.
 */
import { useMemo, useState } from 'react'
import { Contact, Bot, Search, Link2 } from 'lucide-react'
import { Card } from '../components/ui/Card.jsx'
import { Badge } from '../components/ui/Badge.jsx'
import { StatCard } from '../components/ui/StatCard.jsx'
import { PageHeader, Spinner, ErrorState, EmptyState } from '../components/common.jsx'
import { useAsync } from '../lib/hooks.js'
import { contributors, analytics } from '../lib/api.js'
import { compact } from '../lib/analyticsView.js'

async function load() {
  // A 1y window is generous enough that "commits" reads as "recent activity",
  // not a lifetime total — matches the range Insights defaults its stats to.
  const [people, stats] = await Promise.all([contributors(), analytics({ days: 365 })])
  return { people, stats }
}

/** Every email this identity answers to — primary first, deduped against aliases. */
function emailsOf(person) {
  const set = new Set()
  if (person.primary_email) set.add(person.primary_email)
  for (const e of person.emails ?? []) set.add(e)
  return [...set]
}

function PersonCard({ person, commits }) {
  const aliases = emailsOf(person).filter((e) => e !== person.primary_email)
  return (
    <Card padding="md" className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-semibold text-[var(--text)]">
            {person.display_name || person.primary_email || '—'}
          </span>
          {person.is_agent && (
            <Badge color="indigo">
              <Bot size={11} /> {person.agent_kind || 'agent'}
            </Badge>
          )}
          {person.login && (
            <span className="font-mono text-xs text-[var(--text-faint)]">@{person.login}</span>
          )}
        </div>
        <span className="mt-0.5 block font-mono text-xs text-[var(--text-faint)]">
          {person.primary_email || '—'}
        </span>
        {aliases.length > 0 && (
          <div className="mt-2 flex flex-wrap items-center gap-1.5">
            <Link2 size={11} className="shrink-0 text-[var(--text-faint)]" aria-hidden="true" />
            {aliases.map((e) => <Badge key={e}>{e}</Badge>)}
          </div>
        )}
      </div>
      <div className="shrink-0 text-left sm:text-right">
        <span className="block font-display text-lg font-semibold tabular-nums text-[var(--text)]">
          {compact(commits)}
        </span>
        <span className="block text-[10px] font-mono uppercase tracking-[0.1em] text-[var(--text-faint)]">
          commits · 1y
        </span>
      </div>
    </Card>
  )
}

export default function People() {
  const { data, loading, error, reload } = useAsync(load, [])
  const [query, setQuery] = useState('')

  // Memoized so the `?? []` fallback doesn't create a fresh array identity on
  // every render — `filtered` below depends on `people`.
  const people = useMemo(() => data?.people ?? [], [data])
  const stats = data?.stats

  const commitsByEmail = useMemo(() => {
    const m = new Map()
    for (const c of stats?.contributors ?? []) m.set(c.email, c.commits ?? 0)
    return m
  }, [stats])

  function commitsFor(person) {
    return emailsOf(person).reduce((sum, e) => sum + (commitsByEmail.get(e) ?? 0), 0)
  }

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return people
    return people.filter((p) => {
      const hay = [p.display_name, p.primary_email, p.login, ...(p.emails ?? [])]
        .filter(Boolean)
        .join(' ')
        .toLowerCase()
      return hay.includes(q)
    })
  }, [people, query])

  const agentCount = people.filter((p) => p.is_agent).length
  const aliasedCount = people.filter((p) => emailsOf(p).length > 1).length

  if (loading) return <div><PageHeader title="People" /><Spinner /></div>
  if (error) return <div><PageHeader title="People" /><ErrorState error={error} onRetry={reload} /></div>

  if (!people.length) {
    return (
      <div>
        <PageHeader title="People" />
        <EmptyState
          icon={<Contact size={22} />}
          title="No identities yet"
          description="Scan a repo to walk its commit history — identities are merged automatically by shared email, never entered by hand."
        />
      </div>
    )
  }

  return (
    <div>
      <PageHeader
        title="People"
        subtitle="Every contributor identity, merged automatically by shared email seen across git history — not a directory anyone maintains by hand."
      />

      <div className="mb-5 grid grid-cols-2 gap-3 md:grid-cols-3">
        <StatCard label="Identities" value={people.length} accent="var(--chart-2)" icon={<Contact size={14} />} />
        <StatCard
          label="Agent identities" value={agentCount}
          accent="var(--brand-indigo)" icon={<Bot size={14} />}
        />
        <StatCard
          label="Aliased identities" value={aliasedCount} sublabel="more than one linked email"
          accent="var(--chart-5)" icon={<Link2 size={14} />}
        />
      </div>

      <Card padding="md" className="mb-5">
        <label className="flex items-center gap-2">
          <Search size={15} className="shrink-0 text-[var(--text-faint)]" aria-hidden="true" />
          <span className="sr-only">Search people by name, email or login</span>
          <input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search by name, email or login…"
            className="w-full bg-transparent text-sm text-[var(--text)] placeholder:text-[var(--text-faint)] outline-none"
          />
        </label>
      </Card>

      {filtered.length ? (
        <div className="flex flex-col gap-3">
          {filtered.map((p) => <PersonCard key={p.id} person={p} commits={commitsFor(p)} />)}
        </div>
      ) : (
        <EmptyState title="No matches" description="Try a different name, email or login." />
      )}
    </div>
  )
}
