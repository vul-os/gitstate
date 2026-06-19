/**
 * Contribution — /contribution
 *
 * Multi-dimensional, evidence-backed contribution view used to *inform* (never
 * automate) share-allocation conversations in an employee-owned company.
 *
 * Sections:
 *   1. Header + period selector + honest advisory caveat banner.
 *   2. Live weight tuning — six sliders re-rank everyone client-side; owners/admins
 *      can persist via PUT /api/contribution/weights.
 *   3. People roster (sorted by the live composite) with composite ring, radar,
 *      dimension bars, and human/agent authorship split.
 *   4. Segregated "Automated agents" section so bots never sit silently among people.
 *   5. Drill-down drawer — raw numbers + the actual PRs / reviews / revert commits.
 *
 * All charts hand-rolled SVG. Both themes. Loading / empty / error states.
 */
import { useState, useMemo } from 'react'
import {
  useContribution, saveWeights, DIMENSION_KEYS,
  useContributionTrends, useEquity, useKudos,
} from '../lib/useContribution.js'
import { useOrg } from '../lib/useOrg.js'
import { useAuth } from '../lib/useAuth.js'
import { Button, Card, Badge } from '../components/ui/index.js'
import { Reveal, RevealList } from '../components/Reveal.jsx'
import { WeightTuner } from '../components/contribution/WeightTuner.jsx'
import { ContributorCard } from '../components/contribution/ContributorCard.jsx'
import { ContributorDrawer } from '../components/contribution/ContributorDrawer.jsx'
import { TrendsChart } from '../components/contribution/TrendsChart.jsx'
import { EquityLedger } from '../components/contribution/EquityLedger.jsx'
import { KudosModal, KudosFeed } from '../components/contribution/Kudos.jsx'
import { computeComposite } from '../components/contribution/helpers.js'
import { ShieldCheck, Info, Scale, Bot, Users, Sparkles, TrendingUp, Heart } from 'lucide-react'

// ── period presets ────────────────────────────────────────────────────────────

const todayISO = () => new Date().toISOString().slice(0, 10)
function isoDaysAgo(days) {
  const d = new Date()
  d.setDate(d.getDate() - days)
  return d.toISOString().slice(0, 10)
}
const PRESETS = [
  { key: '30d', label: '30 days', days: 30 },
  { key: '90d', label: '90 days', days: 90 },
  { key: '365d', label: '12 months', days: 365 },
  { key: 'custom', label: 'Custom', days: null },
]

const DEFAULT_WEIGHTS = { shipped: 5, review: 4, effort: 3, quality: 4, ownership: 3, durability: 3 }

function weightsEqual(a, b) {
  return DIMENSION_KEYS.every((k) => Number(a?.[k] ?? 0) === Number(b?.[k] ?? 0))
}

// ── period selector ───────────────────────────────────────────────────────────

const dateInputCls =
  'bg-[var(--bg)] text-xs text-[var(--text-dim)] rounded-[var(--radius-btn)] px-3 py-1.5 ' +
  'border border-[var(--border)] outline-none focus:border-[var(--brand-teal)]/45 transition-colors'

function PeriodSelector({ preset, setPreset, range, setRange }) {
  function applyPreset(p) {
    setPreset(p.key)
    if (p.days != null) setRange({ from: isoDaysAgo(p.days), to: todayISO() })
  }
  return (
    <div className="flex flex-wrap items-center gap-3">
      <div className="inline-flex items-center rounded-[var(--radius-btn)] border border-[var(--border)] bg-[var(--bg-surface)] p-0.5">
        {PRESETS.map((p) => (
          <button
            key={p.key}
            onClick={() => applyPreset(p)}
            className={[
              'px-3 py-1.5 text-[12px] font-medium rounded-[6px] transition-colors cursor-pointer',
              preset === p.key
                ? 'bg-[#2DD4BF]/15 text-[#2DD4BF]'
                : 'text-[var(--text-faint)] hover:text-[var(--text-dim)]',
            ].join(' ')}
          >
            {p.label}
          </button>
        ))}
      </div>
      {preset === 'custom' && (
        <div className="flex items-center gap-2">
          <input type="date" className={dateInputCls} value={range.from} max={range.to || todayISO()}
            onChange={(e) => setRange((r) => ({ ...r, from: e.target.value }))} />
          <span className="text-[var(--text-faint)] text-xs">→</span>
          <input type="date" className={dateInputCls} value={range.to} max={todayISO()}
            onChange={(e) => setRange((r) => ({ ...r, to: e.target.value }))} />
        </div>
      )}
    </div>
  )
}

// ── caveat banner ─────────────────────────────────────────────────────────────

function CaveatBanner() {
  return (
    <div className="relative overflow-hidden rounded-[var(--radius-card)] border border-[var(--brand-teal)]/25 bg-[var(--brand-teal)]/[0.05]">
      <div className="absolute inset-0 ambient-brand pointer-events-none opacity-60" aria-hidden />
      <div className="relative flex items-start gap-3 p-4">
        <ShieldCheck size={18} className="text-[var(--brand-teal)] mt-0.5 shrink-0" />
        <p className="text-[13px] text-[var(--text-dim)] leading-relaxed">
          <span className="font-semibold text-[var(--text)]">Advisory, multi-dimensional, evidence-backed.</span>{' '}
          Every number drills down to the underlying merged PRs, reviews and commits.
          Two of the six axes are deliberately gaming-resistant — <span className="text-[var(--text-dim)] font-medium">durability</span> rewards
          code that still survives at HEAD, and <span className="text-[var(--text-dim)] font-medium">quality</span> counts
          bug-introductions (SZZ) — so churn and agent-spam don’t inflate a score.
          This view informs allocation conversations — it is deliberately <em>not</em> an
          automatic ranking, and weighting is a judgement the org makes together.
        </p>
      </div>
    </div>
  )
}

// ── loading / empty ──────────────────────────────────────────────────────────

function RosterSkeleton() {
  return (
    <div className="space-y-3">
      {[0, 1, 2, 3].map((i) => (
        <Card key={i} padding="none" className="overflow-hidden">
          <div className="flex items-center gap-5 p-5 animate-pulse">
            <div className="w-10 h-10 rounded-full bg-[var(--bg-surface3)] shrink-0" />
            <div className="space-y-2 w-44">
              <div className="h-3 rounded bg-[var(--bg-surface3)] w-32" />
              <div className="h-2 rounded bg-[var(--bg-surface3)] w-24" />
            </div>
            <div className="w-16 h-16 rounded-full bg-[var(--bg-surface3)] shrink-0" />
            <div className="flex-1 space-y-2">
              {[0, 1, 2].map((j) => <div key={j} className="h-1.5 rounded-full bg-[var(--bg-surface3)]" />)}
            </div>
          </div>
        </Card>
      ))}
    </div>
  )
}

function EmptyState({ title, body }) {
  return (
    <Card padding="xl" className="text-center">
      <div className="mx-auto mb-3 w-10 h-10 rounded-full bg-[var(--bg-surface3)] flex items-center justify-center">
        <Sparkles size={18} className="text-[var(--text-faint)]" />
      </div>
      <p className="text-sm font-medium text-[var(--text-dim)]">{title}</p>
      <p className="text-xs text-[var(--text-faint)] mt-1 max-w-sm mx-auto">{body}</p>
    </Card>
  )
}

function SectionHeading({ icon, title, count, hint }) {
  return (
    <div className="flex items-center gap-2.5 mb-3">
      {icon}
      <h2 className="text-sm font-semibold text-[var(--text)]">{title}</h2>
      {count != null && (
        <span className="text-[11px] font-mono text-[var(--text-faint)] tabular-nums">{count}</span>
      )}
      {hint && <span className="text-[11px] text-[var(--text-faint)] ml-1">· {hint}</span>}
    </div>
  )
}

// ── ranked-list helper ─────────────────────────────────────────────────────────

/**
 * Recompute composites from the live weights and re-sort, tracking each member's
 * rank delta vs the server's original order (so we can show ▲/▼ on the ring).
 */
function rankMembers(members, weights, serverOrderById) {
  const scored = members.map((m) => ({ m, live: computeComposite(m.dimensions, weights) }))
  scored.sort((a, b) => b.live - a.live)
  return scored.map((s, i) => {
    const serverRank = serverOrderById.get(s.m.userId)
    const liveRank = i + 1
    return { member: s.m, live: s.live, rank: liveRank, delta: serverRank != null ? serverRank - liveRank : 0 }
  })
}

// ── page ──────────────────────────────────────────────────────────────────────

const TABS = [
  { key: 'people', label: 'People', icon: Users },
  { key: 'trends', label: 'Over time', icon: TrendingUp },
  { key: 'equity', label: 'Equity (advisory)', icon: Scale },
]

export default function Contribution() {
  const { orgRole } = useOrg()
  const { user } = useAuth()
  const selfId = user?.id ?? null
  const canEdit = orgRole === 'owner' || orgRole === 'admin'

  const [tab, setTab] = useState('people')
  const [preset, setPreset] = useState('90d')
  const [range, setRange] = useState({ from: isoDaysAgo(90), to: todayISO() })
  const [openId, setOpenId] = useState(null)
  const [kudosOpen, setKudosOpen] = useState(false)
  const [kudosTo, setKudosTo] = useState(null)

  const { data, loading, error } = useContribution(range)
  const trends = useContributionTrends({ periods: 6, interval: 'month' })
  const equity = useEquity({})
  const kudos = useKudos({})

  const kudosCounts = kudos.data?.counts ?? {}
  // userId → array of composites (oldest→newest) for per-member sparklines.
  const trendByUser = useMemo(() => {
    const map = new Map()
    for (const s of trends.data?.series ?? []) {
      map.set(s.userId, (s.points ?? []).map((p) => p.composite))
    }
    return map
  }, [trends.data])

  function openKudos(toUser) {
    setKudosTo(toUser ?? null)
    setKudosOpen(true)
  }

  // Live weights, seeded from the server once data arrives.
  const [weights, setWeights] = useState(DEFAULT_WEIGHTS)
  const [serverWeights, setServerWeights] = useState(DEFAULT_WEIGHTS)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [saveError, setSaveError] = useState(null)

  // Sync server weights into local editable state when they change — done during
  // render (React's "adjust state when a prop changes" pattern) so there's no
  // effect cascade and dragging stays instant.
  const serverKey = data?.weights ? JSON.stringify(data.weights) : null
  const [appliedKey, setAppliedKey] = useState(null)
  if (serverKey && appliedKey !== serverKey) {
    const sw = { ...DEFAULT_WEIGHTS, ...data.weights }
    setAppliedKey(serverKey)
    setServerWeights(sw)
    setWeights(sw)
  }

  const dirty = !weightsEqual(weights, serverWeights)

  const setWeight = (key, value) => {
    setWeights((w) => ({ ...w, [key]: value }))
    setSaved(false)
    setSaveError(null)
  }
  const resetWeights = () => { setWeights(serverWeights); setSaved(false); setSaveError(null) }

  async function persist() {
    setSaving(true); setSaveError(null)
    try {
      const result = await saveWeights(weights)
      const next = { ...DEFAULT_WEIGHTS, ...(result ?? weights) }
      setServerWeights(next)
      setWeights(next)
      setSaved(true)
    } catch (e) {
      setSaveError(e.message ?? 'Could not save weights')
    } finally {
      setSaving(false)
    }
  }

  const members = data?.members ?? []
  const people = members.filter((m) => !m.isAgentBot)
  const agents = members.filter((m) => m.isAgentBot)

  // Server order (by composite desc, as delivered) for rank-delta tracking.
  const serverOrderById = useMemo(() => {
    const map = new Map()
    people.forEach((m, i) => map.set(m.userId, i + 1))
    return map
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data])

  const rankedPeople = useMemo(
    () => rankMembers(people, weights, serverOrderById),
    [people, weights, serverOrderById],
  )
  const rankedAgents = useMemo(
    () => rankMembers(agents, weights, new Map()),
    [agents, weights],
  )

  return (
    <div className="w-full space-y-6">
      {/* Header */}
      <Reveal>
        <div className="relative overflow-hidden rounded-[var(--radius-card)]">
          <div className="absolute inset-0 ambient-brand pointer-events-none" aria-hidden />
          <div className="relative flex flex-col lg:flex-row lg:items-end lg:justify-between gap-4 py-1">
            <div>
              <div className="flex items-center gap-2 mb-1.5">
                <Badge color="teal"><Scale size={11} /> Advisory</Badge>
              </div>
              <h1 className="font-display text-2xl font-semibold text-[var(--text)] tracking-tight">Contribution</h1>
              <p className="text-sm text-[var(--text-faint)] mt-1 max-w-xl">
                A multi-dimensional, evidence-backed view of how each person moved outcomes —
                built to inform share-allocation conversations, not to crown a leaderboard.
              </p>
            </div>
            <div className="flex flex-wrap items-center gap-3">
              <Button variant="outline" size="sm" leftIcon={<Heart size={14} />} onClick={() => openKudos(null)}>
                Give kudos
              </Button>
              {tab !== 'equity' && (
                <PeriodSelector preset={preset} setPreset={setPreset} range={range} setRange={setRange} />
              )}
            </div>
          </div>
        </div>
      </Reveal>

      {/* tabs */}
      <Reveal delay={0.03}>
        <div className="inline-flex items-center rounded-[var(--radius-btn)] border border-[var(--border)] bg-[var(--bg-surface)] p-0.5">
          {TABS.map((t) => {
            const Icon = t.icon
            return (
              <button
                key={t.key}
                onClick={() => setTab(t.key)}
                className={[
                  'inline-flex items-center gap-1.5 px-3.5 py-1.5 text-[12px] font-medium rounded-[6px] transition-colors cursor-pointer',
                  tab === t.key ? 'bg-[#2DD4BF]/15 text-[#2DD4BF]' : 'text-[var(--text-faint)] hover:text-[var(--text-dim)]',
                ].join(' ')}
              >
                <Icon size={13} /> {t.label}
              </button>
            )
          })}
        </div>
      </Reveal>

      {tab === 'people' && <Reveal delay={0.05}><CaveatBanner /></Reveal>}

      {/* Error */}
      {tab === 'people' && error && (
        <Card className="border-red-500/20 bg-red-500/[0.04]">
          <p className="text-sm text-red-400">{error} — the backend may not be running yet.</p>
        </Card>
      )}

      {/* ── Over time ─────────────────────────────────────────────── */}
      {tab === 'trends' && (
        <div className="grid grid-cols-1 xl:grid-cols-[1fr_340px] gap-6 items-start">
          <Card padding="lg" className="min-w-0">
            <SectionHeading
              icon={<TrendingUp size={15} className="text-[var(--brand-teal)]" />}
              title="Composite over time"
              hint="last 6 months · each line is a member"
            />
            {trends.error ? (
              <p className="text-sm text-red-400 py-6">{trends.error}</p>
            ) : trends.loading && !trends.data ? (
              <div className="h-[240px] rounded-[var(--radius-card)] bg-[var(--bg-surface3)] animate-pulse" />
            ) : (
              <TrendsChart series={(trends.data?.series ?? []).filter((s) => !s.isAgentBot)} />
            )}
            <p className="text-[11px] text-[var(--text-faint)] mt-4 leading-relaxed">
              Trends are computed per period with the same evidence-backed engine, then cached
              as snapshots. Rising lines mean a member’s contribution grew relative to the team.
            </p>
          </Card>

          {/* kudos feed alongside */}
          <Card padding="lg">
            <div className="flex items-center justify-between mb-3">
              <SectionHeading icon={<Heart size={15} className="text-[var(--brand-indigo)]" />} title="Recent kudos" />
            </div>
            <KudosFeed kudos={kudos.data?.kudos ?? []} loading={kudos.loading} />
          </Card>
        </div>
      )}

      {/* ── Equity (advisory) ─────────────────────────────────────── */}
      {tab === 'equity' && (
        <Reveal>
          <EquityLedger
            data={equity.data}
            loading={equity.loading}
            error={equity.error}
            period={undefined}
            canEdit={canEdit}
            onRefetch={equity.refetch}
          />
        </Reveal>
      )}

      {/* Main layout: roster + sticky tuner */}
      {tab === 'people' && (
      <div className="grid grid-cols-1 xl:grid-cols-[1fr_320px] gap-6 items-start">
        {/* Roster */}
        <div className="space-y-8 min-w-0">
          {/* People */}
          <section>
            <SectionHeading
              icon={<Users size={15} className="text-[var(--brand-teal)]" />}
              title="People"
              count={loading ? null : people.length}
              hint="ranked by the live weighting"
            />
            {loading && !data ? (
              <RosterSkeleton />
            ) : people.length === 0 ? (
              <EmptyState
                title="No contributors in this period"
                body="Connect a repository and run a sync, or widen the period. Contribution is derived from git — nothing is entered by hand."
              />
            ) : (
              <RevealList className="space-y-3" staggerDelay={0.05} inView>
                {rankedPeople.map(({ member, live, rank, delta }) => (
                  <ContributorCard
                    key={member.userId}
                    member={member}
                    rank={rank}
                    liveComposite={live}
                    delta={delta}
                    onOpen={setOpenId}
                    kudosCount={kudosCounts[member.userId] ?? 0}
                    trend={trendByUser.get(member.userId)}
                  />
                ))}
              </RevealList>
            )}
          </section>

          {/* Automated agents — segregated */}
          {rankedAgents.length > 0 && (
            <section>
              <SectionHeading
                icon={<Bot size={15} className="text-[var(--brand-indigo)]" />}
                title="Automated agents"
                count={rankedAgents.length}
                hint="shown separately — agent output never inflates a person"
              />
              <RevealList className="space-y-3" staggerDelay={0.05} inView>
                {rankedAgents.map(({ member, live, rank, delta }) => (
                  <ContributorCard
                    key={member.userId}
                    member={member}
                    rank={rank}
                    liveComposite={live}
                    delta={delta}
                    onOpen={setOpenId}
                    kudosCount={kudosCounts[member.userId] ?? 0}
                  />
                ))}
              </RevealList>
            </section>
          )}
        </div>

        {/* Sticky weight tuner */}
        <div className="xl:sticky xl:top-4">
          <Card padding="lg" className="border-glow-teal">
            <WeightTuner
              weights={weights}
              onChange={setWeight}
              onReset={resetWeights}
              onSave={persist}
              dirty={dirty}
              saving={saving}
              saved={saved}
              canEdit={canEdit}
            />
            {saveError && <p className="text-[11px] text-red-400 mt-3">{saveError}</p>}
            <div className="mt-5 pt-4 border-t border-[var(--border)] flex items-start gap-2">
              <Info size={13} className="text-[var(--text-faint)] mt-0.5 shrink-0" />
              <p className="text-[10px] text-[var(--text-faint)] leading-relaxed">
                Dragging recomputes every composite locally for instant feedback.
                Saved weights become the org default for everyone.
              </p>
            </div>
          </Card>
        </div>
      </div>
      )}

      {/* Drill-down drawer */}
      {openId && (
        <ContributorDrawer
          userId={openId}
          range={range}
          onClose={() => setOpenId(null)}
          kudosCount={kudosCounts[openId] ?? 0}
          onGiveKudos={(uid) => openKudos(uid)}
        />
      )}

      {/* Give-kudos modal */}
      {kudosOpen && (
        <KudosModal
          members={members}
          selfId={selfId}
          defaultToUser={kudosTo}
          onClose={() => setKudosOpen(false)}
          onDone={() => kudos.refetch()}
        />
      )}
    </div>
  )
}
