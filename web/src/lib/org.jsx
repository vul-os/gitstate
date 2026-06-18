/**
 * Org context — active org selection + org list management.
 * Active org id is persisted in localStorage under `gs_active_org`.
 * api.js reads localStorage directly to inject X-Org-ID on /api/* requests.
 */
import { useReducer, useEffect, useCallback, useRef } from 'react'
import { OrgCtx } from './useOrg.js'
import { getActiveOrgId, setActiveOrgId } from './orgStorage.js'
import * as api from './api.js'

// ── Reducer ───────────────────────────────────────────────────────────────────

const initialState = {
  orgs: [],
  orgsLoading: false,
  activeOrgId: getActiveOrgId(),
}

function orgReducer(state, action) {
  switch (action.type) {
    case 'FETCH_START':
      return { ...state, orgsLoading: true }
    case 'FETCH_DONE':
      return { ...state, orgsLoading: false, orgs: action.orgs, activeOrgId: action.activeOrgId }
    case 'FETCH_ERROR':
      return { ...state, orgsLoading: false, orgs: [] }
    case 'SWITCH_ORG':
      return { ...state, activeOrgId: action.orgId }
    case 'ADD_ORG':
      return { ...state, orgs: [...state.orgs, action.org], activeOrgId: action.org.id }
    case 'RESET':
      return { orgs: [], orgsLoading: false, activeOrgId: null }
    default:
      return state
  }
}

// ── Provider ──────────────────────────────────────────────────────────────────

export function OrgProvider({ children, isAuthed }) {
  const [state, dispatch] = useReducer(orgReducer, initialState)
  const fetchGenRef = useRef(0)

  const { orgs, orgsLoading, activeOrgId } = state
  const activeOrg = orgs.find(o => o.id === activeOrgId) ?? orgs[0] ?? null
  const orgRole = activeOrg?.role ?? null

  const switchOrg = useCallback((orgId) => {
    setActiveOrgId(orgId)
    dispatch({ type: 'SWITCH_ORG', orgId })
  }, [])

  const fetchOrgs = useCallback(async () => {
    if (!isAuthed) return
    const gen = ++fetchGenRef.current
    dispatch({ type: 'FETCH_START' })
    try {
      const data = await api.get('/api/orgs')
      if (fetchGenRef.current !== gen) return
      const list = Array.isArray(data) ? data : []
      const stored = getActiveOrgId()
      let nextActive = stored
      if (list.length > 0 && (!stored || !list.find(o => o.id === stored))) {
        nextActive = list[0].id
        setActiveOrgId(nextActive)
      }
      dispatch({ type: 'FETCH_DONE', orgs: list, activeOrgId: nextActive })
    } catch {
      if (fetchGenRef.current !== gen) return
      dispatch({ type: 'FETCH_ERROR' })
    }
  }, [isAuthed])

  useEffect(() => {
    if (!isAuthed) {
      dispatch({ type: 'RESET' })
      return
    }
    fetchOrgs().catch(() => {})
  }, [isAuthed, fetchOrgs])

  const createOrg = useCallback(async (name) => {
    const org = await api.post('/api/orgs', { name })
    setActiveOrgId(org.id)
    dispatch({ type: 'ADD_ORG', org })
    return org
  }, [])

  return (
    <OrgCtx.Provider
      value={{
        orgs,
        orgsLoading,
        activeOrg: activeOrg ?? null,
        activeOrgId: activeOrg?.id ?? activeOrgId,
        orgRole,
        switchOrg,
        createOrg,
        refetchOrgs: fetchOrgs,
      }}
    >
      {children}
    </OrgCtx.Provider>
  )
}
