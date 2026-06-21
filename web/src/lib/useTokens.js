/**
 * useTokens — load and manage the org's API tokens (for agents / CLI / MCP).
 *
 *   GET    /api/tokens        → [{ id, name, prefix, scopes, lastUsedAt, expiresAt, createdAt }]
 *   POST   /api/tokens        → { id, name, prefix, scopes, …, token: "gsk_…" }  (raw token ONCE)
 *   DELETE /api/tokens/{id}   → revoke
 *
 * Owner/admin-gated + JWT-auth on the backend (it 403s otherwise). Follows the
 * app's useOrg + race-safe (genRef) pattern. Imports get/post/del from api.js
 * (never edits it). The raw token from `create` is returned to the caller so the
 * UI can reveal it once; it is never persisted in this hook's state.
 *
 * Returns: { data, loading, error, refetch, create, revoke }
 */
import { useReducer, useEffect, useCallback, useRef } from 'react'
import { useOrg } from './useOrg.js'
import { get, post, del } from './api.js'

const init = { data: null, loading: false, error: null }

function reducer(state, action) {
  switch (action.type) {
    case 'FETCH_START': return { ...state, loading: true, error: null }
    case 'FETCH_DONE':  return { ...state, loading: false, data: action.data }
    case 'FETCH_ERROR': return { ...state, loading: false, error: action.error }
    default: return state
  }
}

export function useTokens() {
  const { activeOrgId } = useOrg()
  const [state, dispatch] = useReducer(reducer, init)
  const genRef = useRef(0)

  const doFetch = useCallback(async () => {
    if (!activeOrgId) return
    const gen = ++genRef.current
    dispatch({ type: 'FETCH_START' })
    try {
      const data = await get('/api/tokens')
      if (genRef.current !== gen) return
      dispatch({ type: 'FETCH_DONE', data: Array.isArray(data) ? data : [] })
    } catch (e) {
      if (genRef.current !== gen) return
      dispatch({ type: 'FETCH_ERROR', error: e.message ?? 'Failed to load API tokens' })
    }
  }, [activeOrgId])

  useEffect(() => { doFetch().catch(() => {}) }, [doFetch])

  // create mints a token; the response carries the raw secret ONCE. We return it
  // to the caller (to reveal once) and refetch the list so the new row appears.
  const create = useCallback(async ({ name, scopes, expiresInDays }) => {
    const body = { name, scopes }
    if (expiresInDays != null) body.expiresInDays = expiresInDays
    const res = await post('/api/tokens', body)
    doFetch().catch(() => {})
    return res
  }, [doFetch])

  // revoke deletes a token then refetches the list.
  const revoke = useCallback(async (id) => {
    await del(`/api/tokens/${id}`)
    doFetch().catch(() => {})
  }, [doFetch])

  return { data: state.data, loading: state.loading, error: state.error, refetch: doFetch, create, revoke }
}
