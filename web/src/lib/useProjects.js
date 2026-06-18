/**
 * useProjects — fetch projects list for the active org.
 */
import { useReducer, useEffect, useCallback, useRef } from 'react'
import { useOrg } from './useOrg.js'
import * as api from './api.js'

const init = { projects: [], loading: false, error: null }

function reducer(state, action) {
  switch (action.type) {
    case 'FETCH_START': return { ...state, loading: true, error: null }
    case 'FETCH_DONE':  return { ...state, loading: false, projects: action.projects }
    case 'FETCH_ERROR': return { ...state, loading: false, error: action.error }
    case 'ADD_PROJECT': return { ...state, projects: [...state.projects, action.project] }
    default: return state
  }
}

export function useProjects() {
  const { activeOrgId } = useOrg()
  const [state, dispatch] = useReducer(reducer, init)
  const genRef = useRef(0)

  const doFetch = useCallback(async () => {
    if (!activeOrgId) return
    const gen = ++genRef.current
    dispatch({ type: 'FETCH_START' })
    try {
      const data = await api.get('/api/projects')
      if (genRef.current !== gen) return
      dispatch({ type: 'FETCH_DONE', projects: Array.isArray(data) ? data : [] })
    } catch (e) {
      if (genRef.current !== gen) return
      dispatch({ type: 'FETCH_ERROR', error: e.message ?? 'Failed to load projects' })
    }
  }, [activeOrgId])

  useEffect(() => { doFetch().catch(() => {}) }, [doFetch])

  const createProject = useCallback(async ({ name, description }) => {
    const proj = await api.post('/api/projects', { name, description })
    dispatch({ type: 'ADD_PROJECT', project: proj })
    return proj
  }, [])

  return {
    projects: state.projects,
    loading: state.loading,
    error: state.error,
    refetch: doFetch,
    createProject,
  }
}
