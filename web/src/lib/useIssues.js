/**
 * useIssues — fetch issues for the active org with optional filters.
 * Params: { source, state, project } — all optional.
 */
import { useReducer, useEffect, useCallback, useRef } from 'react'
import { useOrg } from './useOrg.js'
import * as api from './api.js'

const init = { issues: [], loading: false, error: null }

function reducer(state, action) {
  switch (action.type) {
    case 'FETCH_START':  return { ...state, loading: true, error: null }
    case 'FETCH_DONE':   return { ...state, loading: false, issues: action.issues }
    case 'FETCH_ERROR':  return { ...state, loading: false, error: action.error }
    case 'PREPEND':      return { ...state, issues: [action.issue, ...state.issues] }
    case 'PATCH_ISSUE':  return { ...state, issues: state.issues.map(i => i.id === action.id ? { ...i, ...action.patch } : i) }
    default: return state
  }
}

export function useIssues(filters = {}) {
  const { activeOrgId } = useOrg()
  const [state, dispatch] = useReducer(reducer, init)
  const genRef = useRef(0)

  const source  = filters.source
  const stateF  = filters.state
  const project = filters.project

  const doFetch = useCallback(async () => {
    if (!activeOrgId) return
    const gen = ++genRef.current
    dispatch({ type: 'FETCH_START' })
    try {
      const params = new URLSearchParams()
      if (source)  params.set('source', source)
      if (stateF)  params.set('state', stateF)
      if (project) params.set('project', project)
      const qs = params.toString()
      const data = await api.get(`/api/issues${qs ? `?${qs}` : ''}`)
      if (genRef.current !== gen) return
      dispatch({ type: 'FETCH_DONE', issues: Array.isArray(data) ? data : [] })
    } catch (e) {
      if (genRef.current !== gen) return
      dispatch({ type: 'FETCH_ERROR', error: e.message ?? 'Failed to load issues' })
    }
  }, [activeOrgId, source, stateF, project])

  useEffect(() => { doFetch().catch(() => {}) }, [doFetch])

  const createIssue = useCallback(async ({ title, body, projectId }) => {
    const issue = await api.post('/api/issues', { title, body, projectId, source: 'native' })
    dispatch({ type: 'PREPEND', issue })
    return issue
  }, [])

  const updateIssue = useCallback(async (id, patch) => {
    const updated = await api.patch(`/api/issues/${id}`, patch)
    dispatch({ type: 'PATCH_ISSUE', id, patch: updated })
    return updated
  }, [])

  return {
    issues: state.issues,
    loading: state.loading,
    error: state.error,
    refetch: doFetch,
    createIssue,
    updateIssue,
  }
}
