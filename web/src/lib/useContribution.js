/**
 * useContribution — hooks for the /api/contribution endpoints.
 *
 * Mirrors the useAnalytics pattern: useOrg-scoped, race-safe (genRef), reducer state.
 *
 * Endpoints / shapes (see API CONTRACT):
 *   GET /api/contribution?from=&to=
 *     → { period, weights:{shipped,review,effort,quality,ownership,durability},
 *         members:[{ userId, name, email, isAgentBot, composite,
 *           dimensions:{ shipped:{score,raw}, review:{score,raw},
 *             effort:{score,raw}, quality:{score,raw}, ownership:{score,raw},
 *             durability:{score,raw:{survivingLines,authoredLines,survivalPct}} },
 *           authorship:{humanCommits,agentCommits,agentPct} }] }   (sorted by composite desc)
 *     quality.raw also carries bugsIntroduced (SZZ) + testCoupling (0–1, tested-touch fraction).
 *   GET /api/contribution/{userId}?from=&to=
 *     → member + evidence:{ shipped:[{title,repo,at}], review:[...], quality:[{message,at}], effort:[...],
 *         durability:[{repo,survivingLines,authoredLines}], bugIntroductions:[{fixSha,introducedSha,lines}] }
 *   GET /api/contribution/weights → { shipped,review,effort,quality,ownership,durability }
 *   PUT /api/contribution/weights  (owner/admin)
 *
 * Exports:
 *   DIMENSIONS                              ordered dimension metadata (key/label/...)
 *   useContribution({from,to})             → { data, loading, error, refetch }
 *   useContributionMember(userId,{from,to})→ { data, loading, error }   (userId null → idle)
 *   saveWeights(weights)                   → Promise (PUT)
 */
import { useReducer, useEffect, useCallback, useRef } from 'react'
import { useOrg } from './useOrg.js'
import * as api from './api.js'

// ── Ordered dimension metadata — the single source of truth for the five axes ──
// Kept here (not in the page) so the hook, sliders, radar and bars stay in sync.
export const DIMENSIONS = [
  { key: 'shipped',    label: 'Shipped',    short: 'Ship', hue: 168, blurb: 'Merged PRs, issues closed, features delivered' },
  { key: 'review',     label: 'Review',     short: 'Rev',  hue: 199, blurb: 'Code reviews given — multiplying others’ work' },
  { key: 'effort',     label: 'Effort',     short: 'Eff',  hue: 239, blurb: 'Sustained effort points across the period' },
  { key: 'quality',    label: 'Quality',    short: 'Qual', hue: 268, blurb: 'Low reverts & bug-introductions (SZZ), tested changes, healthy cycle time' },
  { key: 'ownership',  label: 'Ownership',  short: 'Own',  hue: 322, blurb: 'Breadth of areas meaningfully owned' },
  { key: 'durability', label: 'Durability', short: 'Dur',  hue: 128, blurb: 'How much of their code still survives in the codebase — persistence over churn' },
]

export const DIMENSION_KEYS = DIMENSIONS.map((d) => d.key)

/** A colour for a dimension (theme-independent HSL — reads well on both). */
export function dimColor(key, l = 60) {
  const d = DIMENSIONS.find((x) => x.key === key)
  return `hsl(${d ? d.hue : 200} 72% ${l}%)`
}

// ── Shared reducer ─────────────────────────────────────────────────────────────

function makeInit(empty) {
  return { data: empty, loading: false, error: null }
}

function reducer(state, action) {
  switch (action.type) {
    case 'FETCH_START': return { ...state, loading: true, error: null }
    case 'FETCH_DONE':  return { ...state, loading: false, data: action.data }
    case 'FETCH_ERROR': return { ...state, loading: false, error: action.error }
    default: return state
  }
}

function rangeQS({ from, to } = {}) {
  const params = new URLSearchParams()
  if (from) params.set('from', from)
  if (to) params.set('to', to)
  const qs = params.toString()
  return qs ? `?${qs}` : ''
}

// ── Public hooks ────────────────────────────────────────────────────────────────

/** Main roster: period, server weights, and the sorted member list. data: object|null */
export function useContribution({ from, to } = {}) {
  const { activeOrgId } = useOrg()
  const [state, dispatch] = useReducer(reducer, null, makeInit)
  const genRef = useRef(0)

  const doFetch = useCallback(async () => {
    if (!activeOrgId) return
    const gen = ++genRef.current
    dispatch({ type: 'FETCH_START' })
    try {
      const raw = await api.get(`/api/contribution${rangeQS({ from, to })}`)
      if (genRef.current !== gen) return
      dispatch({ type: 'FETCH_DONE', data: raw ?? null })
    } catch (e) {
      if (genRef.current !== gen) return
      dispatch({ type: 'FETCH_ERROR', error: e.message ?? 'Failed to load contribution' })
    }
  }, [activeOrgId, from, to])

  useEffect(() => { doFetch().catch(() => {}) }, [doFetch])

  return { data: state.data, loading: state.loading, error: state.error, refetch: doFetch }
}

/** Drill-down: one member + evidence. `userId` null/'' → idle. data: object|null */
export function useContributionMember(userId, { from, to } = {}) {
  const { activeOrgId } = useOrg()
  const [state, dispatch] = useReducer(reducer, null, makeInit)
  const genRef = useRef(0)

  useEffect(() => {
    if (!activeOrgId || !userId) {
      dispatch({ type: 'FETCH_DONE', data: null })
      return
    }
    const gen = ++genRef.current
    dispatch({ type: 'FETCH_START' })
    ;(async () => {
      try {
        const raw = await api.get(`/api/contribution/${encodeURIComponent(userId)}${rangeQS({ from, to })}`)
        if (genRef.current !== gen) return
        dispatch({ type: 'FETCH_DONE', data: raw ?? null })
      } catch (e) {
        if (genRef.current !== gen) return
        dispatch({ type: 'FETCH_ERROR', error: e.message ?? 'Failed to load contributor detail' })
      }
    })()
  }, [activeOrgId, userId, from, to])

  return { data: state.data, loading: state.loading, error: state.error }
}

/** Persist tuned weights (owner/admin). Resolves to the saved weights. */
export function saveWeights(weights) {
  return api.put('/api/contribution/weights', weights)
}

// ── Trends over time ───────────────────────────────────────────────────────────

/**
 * Contribution over time. `data` shape:
 *   { interval, periods, series:[{ userId, name, email, isAgentBot,
 *       points:[{ periodStart, periodEnd, composite, dimensions:{...} }] }] }   (oldest→newest)
 * Members ordered by their latest composite (desc). data: object|null.
 */
export function useContributionTrends({ periods = 6, interval = 'month' } = {}) {
  const { activeOrgId } = useOrg()
  const [state, dispatch] = useReducer(reducer, null, makeInit)
  const genRef = useRef(0)

  const doFetch = useCallback(async () => {
    if (!activeOrgId) return
    const gen = ++genRef.current
    dispatch({ type: 'FETCH_START' })
    try {
      const params = new URLSearchParams({ periods: String(periods), interval })
      const raw = await api.get(`/api/contribution/trends?${params.toString()}`)
      if (genRef.current !== gen) return
      dispatch({ type: 'FETCH_DONE', data: raw ?? null })
    } catch (e) {
      if (genRef.current !== gen) return
      dispatch({ type: 'FETCH_ERROR', error: e.message ?? 'Failed to load trends' })
    }
  }, [activeOrgId, periods, interval])

  useEffect(() => { doFetch().catch(() => {}) }, [doFetch])

  return { data: state.data, loading: state.loading, error: state.error, refetch: doFetch }
}

// ── Equity ledger (advisory) ─────────────────────────────────────────────────

/**
 * Advisory equity ledger for a period. `data` shape:
 *   { period, advisory:true, note, rows:[{ userId, name, email, composite,
 *       suggestedPct, actualPct|null, poolLabel, note }] }
 * `period` is a YYYY-MM-DD inside the target month (omitted ⇒ current month).
 */
export function useEquity({ period } = {}) {
  const { activeOrgId } = useOrg()
  const [state, dispatch] = useReducer(reducer, null, makeInit)
  const genRef = useRef(0)

  const doFetch = useCallback(async () => {
    if (!activeOrgId) return
    const gen = ++genRef.current
    dispatch({ type: 'FETCH_START' })
    try {
      const qs = period ? `?period=${encodeURIComponent(period)}` : ''
      const raw = await api.get(`/api/equity${qs}`)
      if (genRef.current !== gen) return
      dispatch({ type: 'FETCH_DONE', data: raw ?? null })
    } catch (e) {
      if (genRef.current !== gen) return
      dispatch({ type: 'FETCH_ERROR', error: e.message ?? 'Failed to load equity ledger' })
    }
  }, [activeOrgId, period])

  useEffect(() => { doFetch().catch(() => {}) }, [doFetch])

  return { data: state.data, loading: state.loading, error: state.error, refetch: doFetch }
}

/**
 * Record an admin-entered actual grant (owner/admin). `actualPct` null clears it.
 * Resolves to the refreshed ledger.
 */
export function saveEquity({ userId, period, actualPct, poolLabel, note }) {
  return api.put('/api/equity', { userId, period, actualPct, poolLabel, note })
}

// ── Kudos (peer recognition) ──────────────────────────────────────────────────

/**
 * Kudos feed + counts. `data` shape:
 *   { kudos:[{ id, fromUser, fromName, toUser, toName, dimension, message, createdAt }],
 *     counts:{ [userId]: number } }
 * When `user` is set, kudos is filtered to that recipient (counts stay org-wide).
 */
export function useKudos({ user } = {}) {
  const { activeOrgId } = useOrg()
  const [state, dispatch] = useReducer(reducer, null, makeInit)
  const genRef = useRef(0)

  const doFetch = useCallback(async () => {
    if (!activeOrgId) return
    const gen = ++genRef.current
    dispatch({ type: 'FETCH_START' })
    try {
      const qs = user ? `?user=${encodeURIComponent(user)}` : ''
      const raw = await api.get(`/api/kudos${qs}`)
      if (genRef.current !== gen) return
      dispatch({ type: 'FETCH_DONE', data: raw ?? null })
    } catch (e) {
      if (genRef.current !== gen) return
      dispatch({ type: 'FETCH_ERROR', error: e.message ?? 'Failed to load kudos' })
    }
  }, [activeOrgId, user])

  useEffect(() => { doFetch().catch(() => {}) }, [doFetch])

  return { data: state.data, loading: state.loading, error: state.error, refetch: doFetch }
}

/** Give kudos to a teammate. Resolves to the created kudo. */
export function giveKudos({ toUser, dimension, message }) {
  return api.post('/api/kudos', { toUser, dimension: dimension || undefined, message })
}
