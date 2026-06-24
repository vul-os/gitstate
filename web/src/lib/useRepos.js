/**
 * useRepos — fetch + mutate repos for the active org.
 */
import { useReducer, useEffect, useCallback, useRef } from 'react'
import { useOrg } from './useOrg.js'
import * as api from './api.js'

const init = { repos: [], loading: false, error: null }

function reducer(state, action) {
  switch (action.type) {
    case 'FETCH_START': return { ...state, loading: true, error: null }
    case 'FETCH_DONE':  return { ...state, loading: false, repos: action.repos }
    case 'FETCH_ERROR': return { ...state, loading: false, error: action.error }
    case 'ADD_REPO':    return { ...state, repos: [...state.repos, action.repo] }
    case 'PATCH_REPO':  return { ...state, repos: state.repos.map(r => r.id === action.id ? { ...r, ...action.patch } : r) }
    default: return state
  }
}

export function useRepos() {
  const { activeOrgId } = useOrg()
  const [state, dispatch] = useReducer(reducer, init)
  const genRef = useRef(0)

  const doFetch = useCallback(async () => {
    if (!activeOrgId) return
    const gen = ++genRef.current
    dispatch({ type: 'FETCH_START' })
    try {
      const data = await api.get('/api/repos')
      if (genRef.current !== gen) return
      dispatch({ type: 'FETCH_DONE', repos: Array.isArray(data) ? data : [] })
    } catch (e) {
      if (genRef.current !== gen) return
      dispatch({ type: 'FETCH_ERROR', error: e.message ?? 'Failed to load repos' })
    }
  }, [activeOrgId])

  useEffect(() => { doFetch().catch(() => {}) }, [doFetch])

  const connectRepo = useCallback(async ({ platform, fullName, token }) => {
    const repo = await api.post('/api/repos', { platform, fullName, token })
    dispatch({ type: 'ADD_REPO', repo })
    return repo
  }, [])

  const syncRepo = useCallback(async (id) => {
    await api.post(`/api/repos/${id}/sync`, {})
    dispatch({ type: 'PATCH_REPO', id, patch: { syncing: true } })
    setTimeout(() => doFetch().catch(() => {}), 4000)
  }, [doFetch])

  // Move a repo into a project (projectId null/"" → unassigned). Optimistically
  // patches projectId so the row jumps groups immediately; reverts + rethrows on
  // failure so the caller can surface an error and re-settle from the server.
  const moveRepo = useCallback(async (id, projectId) => {
    const prev = state.repos.find(r => r.id === id)?.projectId ?? null
    const next = projectId || null
    dispatch({ type: 'PATCH_REPO', id, patch: { projectId: next } })
    try {
      await api.moveRepoToProject(id, next)
    } catch (e) {
      dispatch({ type: 'PATCH_REPO', id, patch: { projectId: prev } })
      throw e
    }
  }, [state.repos])

  return {
    repos: state.repos,
    loading: state.loading,
    error: state.error,
    refetch: doFetch,
    connectRepo,
    syncRepo,
    moveRepo,
  }
}
