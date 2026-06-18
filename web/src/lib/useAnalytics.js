/**
 * useAnalytics — hooks for the /api/analytics/* dashboard endpoints.
 *
 * Every hook follows the useOrg + race-safe (genRef) pattern used across the app.
 * They all take the shared filter object: { from, to, repo, author }.
 *
 * Exports:
 *   useSummary(filters)          → { data, loading, error, refetch }
 *   useHeatmap(filters)          → { data, loading, error, refetch }   data: [{date,count}]
 *   useCommitsOverTime(filters, bucket) → { data, loading, error, refetch }  data: [{date,count}]
 *   useContributors(filters)     → { data, loading, error, refetch }   data: [{login,...}]
 *   useRepoStats(filters)        → { data, loading, error, refetch }   data: [{repoId,...}]
 *   useDayCommits(date, filters) → { data, loading, error }            data: [{sha,...}]  (date null = idle)
 */
import { useReducer, useEffect, useCallback, useRef } from 'react'
import { useOrg } from './useOrg.js'
import * as api from './api.js'

// ── Shared reducer ────────────────────────────────────────────────────────────

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

/** Build a `?from=&to=&repo=&author=` query string from the filter object. */
function filterQS(filters = {}, extra = {}) {
  const params = new URLSearchParams()
  const { from, to, repo, author } = filters
  if (from)   params.set('from', from)
  if (to)     params.set('to', to)
  if (repo)   params.set('repo', repo)
  if (author) params.set('author', author)
  for (const [k, v] of Object.entries(extra)) {
    if (v != null && v !== '') params.set(k, v)
  }
  const qs = params.toString()
  return qs ? `?${qs}` : ''
}

/**
 * Generic analytics fetch hook factory.
 * @param {string} path        endpoint suffix after /api/analytics/
 * @param {object} filters     shared filter object
 * @param {*} empty            empty-state value (array or null)
 * @param {function} normalize maps the raw payload → consumer shape
 * @param {object} extra       extra query params (e.g. { bucket })
 */
function useAnalyticsResource(path, filters, empty, normalize, extra) {
  const { activeOrgId } = useOrg()
  const [state, dispatch] = useReducer(reducer, empty, makeInit)
  const genRef = useRef(0)

  const { from, to, repo, author } = filters
  const extraKey = extra ? JSON.stringify(extra) : ''

  const doFetch = useCallback(async () => {
    if (!activeOrgId) return
    const gen = ++genRef.current
    dispatch({ type: 'FETCH_START' })
    try {
      const qs = filterQS({ from, to, repo, author }, extra)
      const raw = await api.get(`/api/analytics/${path}${qs}`)
      if (genRef.current !== gen) return
      dispatch({ type: 'FETCH_DONE', data: normalize(raw) })
    } catch (e) {
      if (genRef.current !== gen) return
      dispatch({ type: 'FETCH_ERROR', error: e.message ?? 'Failed to load analytics' })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeOrgId, from, to, repo, author, path, extraKey])

  useEffect(() => { doFetch().catch(() => {}) }, [doFetch])

  return { data: state.data, loading: state.loading, error: state.error, refetch: doFetch }
}

const asArray = (raw) => (Array.isArray(raw) ? raw : (raw?.items ?? []))

// ── Public hooks ──────────────────────────────────────────────────────────────

export function useSummary(filters = {}) {
  return useAnalyticsResource('summary', filters, null, (raw) => raw ?? null)
}

export function useHeatmap(filters = {}) {
  return useAnalyticsResource('heatmap', filters, [], asArray)
}

export function useCommitsOverTime(filters = {}, bucket = 'day') {
  return useAnalyticsResource('commits-over-time', filters, [], asArray, { bucket })
}

export function useContributors(filters = {}) {
  return useAnalyticsResource('contributors', filters, [], asArray)
}

export function useRepoStats(filters = {}) {
  return useAnalyticsResource('repos', filters, [], asArray)
}

/**
 * useDayCommits — drill-down for a single day. `date` null/'' → idle (no fetch).
 * Respects the active filters so the drill-down stays consistent with the heatmap.
 */
export function useDayCommits(date, filters = {}) {
  const { activeOrgId } = useOrg()
  const [state, dispatch] = useReducer(reducer, [], makeInit)
  const genRef = useRef(0)

  const { repo, author } = filters

  useEffect(() => {
    if (!activeOrgId || !date) return
    const gen = ++genRef.current
    dispatch({ type: 'FETCH_START' })
    ;(async () => {
      try {
        const qs = filterQS({ repo, author })
        const raw = await api.get(`/api/analytics/day/${date}${qs}`)
        if (genRef.current !== gen) return
        dispatch({ type: 'FETCH_DONE', data: asArray(raw) })
      } catch (e) {
        if (genRef.current !== gen) return
        dispatch({ type: 'FETCH_ERROR', error: e.message ?? 'Failed to load commits' })
      }
    })()
  }, [activeOrgId, date, repo, author])

  return { data: state.data, loading: state.loading, error: state.error }
}
