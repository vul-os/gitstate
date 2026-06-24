/**
 * useAccounting — accounting connection status for the org.
 *
 * Providers: xero, quickbooks, sage, zoho_books, freshbooks.
 * GET /api/accounting/status → [{provider, configured, connected, externalName}]
 * Connect is a top-level navigation (accountingStartUrl); disconnect + invoice
 * push are imperative helpers in api.js.
 */
import { useReducer, useEffect, useCallback, useRef } from 'react'
import { useOrg } from './useOrg.js'
import { fetchAccountingStatus } from './api.js'

const init = { data: [], loading: false, error: null }

function reducer(state, action) {
  switch (action.type) {
    case 'START': return { ...state, loading: true, error: null }
    case 'DONE':  return { ...state, loading: false, data: action.data }
    case 'ERROR': return { ...state, loading: false, error: action.error }
    default: return state
  }
}

export function useAccounting() {
  const { activeOrgId } = useOrg()
  const [state, dispatch] = useReducer(reducer, init)
  const genRef = useRef(0)

  const refetch = useCallback(async () => {
    if (!activeOrgId) return
    const gen = ++genRef.current
    dispatch({ type: 'START' })
    try {
      const data = await fetchAccountingStatus()
      if (genRef.current !== gen) return
      dispatch({ type: 'DONE', data: Array.isArray(data) ? data : [] })
    } catch (e) {
      if (genRef.current !== gen) return
      dispatch({ type: 'ERROR', error: e.message ?? 'Failed to load accounting status' })
    }
  }, [activeOrgId])

  useEffect(() => { refetch().catch(() => {}) }, [refetch])

  const providers = Array.isArray(state.data) ? state.data : []
  return {
    providers,
    connected: providers.filter((p) => p.connected),
    xero: providers.find((p) => p.provider === 'xero'),
    quickbooks: providers.find((p) => p.provider === 'quickbooks'),
    anyConfigured: providers.some((p) => p.configured),
    anyConnected: providers.some((p) => p.connected),
    loading: state.loading,
    error: state.error,
    refetch,
  }
}
