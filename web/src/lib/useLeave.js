/**
 * useLeave — richer leave-management data: configurable leave TYPES,
 * per-user BALANCES (entitled / carried / used / remaining), and the leave
 * request / approval mutations.
 *
 * Endpoints:
 *   GET   /api/leave-types
 *   GET   /api/leave-balances?user=&year=
 *   GET   /api/leave            (all entries — also used for the team calendar)
 *   POST  /api/leave            (type, dates, half-day, portion, note)
 *   PATCH /api/leave/{id}       (approve | reject)
 */
import { useReducer, useEffect, useCallback, useRef } from 'react'
import { useOrg } from './useOrg.js'
import * as api from './api.js'

const init = {
  types: [],
  balances: [],
  leave: [],
  loading: true,
  error: null,
}

function reducer(state, action) {
  switch (action.type) {
    case 'START':       return { ...state, loading: true, error: null }
    case 'DONE':        return { ...state, loading: false, types: action.types, balances: action.balances, leave: action.leave }
    case 'ERROR':       return { ...state, loading: false, error: action.error }
    case 'SET_BAL':     return { ...state, balances: action.balances }
    case 'ADD_LEAVE':   return { ...state, leave: [action.entry, ...state.leave] }
    case 'PATCH_LEAVE': return { ...state, leave: state.leave.map(e => (e.id === action.entry.id ? action.entry : e)) }
    default: return state
  }
}

export function useLeave({ userId, year } = {}) {
  const { activeOrgId } = useOrg()
  const [state, dispatch] = useReducer(reducer, init)
  const genRef = useRef(0)

  const yr = year ?? new Date().getFullYear()

  const balanceQs = useCallback(() => {
    const qs = new URLSearchParams({ year: String(yr) })
    if (userId) qs.set('user', userId)
    return qs.toString()
  }, [userId, yr])

  const refetch = useCallback(async () => {
    if (!activeOrgId) return
    const gen = ++genRef.current
    dispatch({ type: 'START' })
    try {
      const [t, b, l] = await Promise.all([
        api.get('/api/leave-types').catch(() => []),
        api.get(`/api/leave-balances?${balanceQs()}`).catch(() => []),
        api.get('/api/leave').catch(() => []),
      ])
      if (genRef.current !== gen) return
      dispatch({
        type: 'DONE',
        types: Array.isArray(t) ? t : [],
        balances: Array.isArray(b) ? b : [],
        leave: Array.isArray(l) ? l : [],
      })
    } catch (e) {
      if (genRef.current !== gen) return
      dispatch({ type: 'ERROR', error: e.message ?? 'Failed to load leave data' })
    }
  }, [activeOrgId, balanceQs])

  useEffect(() => { refetch().catch(() => {}) }, [refetch])

  const requestLeave = useCallback(async (entry) => {
    const created = await api.post('/api/leave', entry)
    dispatch({ type: 'ADD_LEAVE', entry: created ?? entry })
    return created
  }, [])

  const decideLeave = useCallback(async (id, status) => {
    const updated = await api.patch(`/api/leave/${id}`, { status })
    if (updated) dispatch({ type: 'PATCH_LEAVE', entry: updated })
    // Balances may have shifted (used_days recomputed on approve/reject).
    api.get(`/api/leave-balances?${balanceQs()}`)
      .then(b => dispatch({ type: 'SET_BAL', balances: Array.isArray(b) ? b : [] }))
      .catch(() => {})
    return updated
  }, [balanceQs])

  return {
    types: state.types,
    balances: state.balances,
    leave: state.leave,
    loading: state.loading,
    error: state.error,
    refetch,
    requestLeave,
    decideLeave,
  }
}
