/**
 * useInvoices — client-invoicing data hooks (clients, invoices, generate-from-git).
 *
 * Endpoints (all auth + org-scoped via api.js header injection):
 *   GET/POST  /api/clients              · PATCH /api/clients/{id}
 *   GET       /api/invoices             · GET   /api/invoices/{id}
 *   POST      /api/invoices             · POST  /api/invoices/generate
 *   PATCH     /api/invoices/{id}        · DELETE /api/invoices/{id}
 *
 * The public share view fetches /api/public/invoices/{token} with NO auth — that
 * lives in usePublicInvoice (raw fetch, bypassing the token/org header injection).
 */
import { useReducer, useEffect, useCallback, useRef } from 'react'
import { useOrg } from './useOrg.js'
import { get, post, patch, del, generateInvoiceFromGit } from './api.js'

export { generateInvoiceFromGit }

const BASE = import.meta.env.VITE_API_BASE_URL ?? ''

// ── Generic fetch reducer ───────────────────────────────────────────────────────

const init = { data: null, loading: false, error: null }

function reducer(state, action) {
  switch (action.type) {
    case 'START': return { ...state, loading: true, error: null }
    case 'DONE':  return { ...state, loading: false, data: action.data }
    case 'ERROR': return { ...state, loading: false, error: action.error }
    default: return state
  }
}

// ── Clients ─────────────────────────────────────────────────────────────────────

export function useClients() {
  const { activeOrgId } = useOrg()
  const [state, dispatch] = useReducer(reducer, { ...init, data: [] })
  const genRef = useRef(0)

  const doFetch = useCallback(async () => {
    if (!activeOrgId) return
    const gen = ++genRef.current
    dispatch({ type: 'START' })
    try {
      const data = await get('/api/clients')
      if (genRef.current !== gen) return
      dispatch({ type: 'DONE', data: Array.isArray(data) ? data : [] })
    } catch (e) {
      if (genRef.current !== gen) return
      dispatch({ type: 'ERROR', error: e.message ?? 'Failed to load clients' })
    }
  }, [activeOrgId])

  useEffect(() => { doFetch().catch(() => {}) }, [doFetch])

  const createClient = useCallback(async (body) => {
    const c = await post('/api/clients', body)
    await doFetch()
    return c
  }, [doFetch])

  const updateClient = useCallback(async (id, body) => {
    const c = await patch(`/api/clients/${id}`, body)
    await doFetch()
    return c
  }, [doFetch])

  return {
    clients: state.data ?? [],
    loading: state.loading,
    error: state.error,
    refetch: doFetch,
    createClient,
    updateClient,
  }
}

// ── Invoice list ────────────────────────────────────────────────────────────────

export function useInvoiceList() {
  const { activeOrgId } = useOrg()
  const [state, dispatch] = useReducer(reducer, { ...init, data: [] })
  const genRef = useRef(0)

  const doFetch = useCallback(async () => {
    if (!activeOrgId) return
    const gen = ++genRef.current
    dispatch({ type: 'START' })
    try {
      const data = await get('/api/invoices')
      if (genRef.current !== gen) return
      dispatch({ type: 'DONE', data: Array.isArray(data) ? data : [] })
    } catch (e) {
      if (genRef.current !== gen) return
      dispatch({ type: 'ERROR', error: e.message ?? 'Failed to load invoices' })
    }
  }, [activeOrgId])

  useEffect(() => { doFetch().catch(() => {}) }, [doFetch])

  return {
    invoices: state.data ?? [],
    loading: state.loading,
    error: state.error,
    refetch: doFetch,
  }
}

// ── Invoice detail (header + lines) ─────────────────────────────────────────────

export function useInvoiceDetail(id) {
  const { activeOrgId } = useOrg()
  const [state, dispatch] = useReducer(reducer, init)
  const genRef = useRef(0)

  const doFetch = useCallback(async () => {
    if (!activeOrgId || !id) return
    const gen = ++genRef.current
    dispatch({ type: 'START' })
    try {
      const data = await get(`/api/invoices/${id}`)
      if (genRef.current !== gen) return
      dispatch({ type: 'DONE', data })
    } catch (e) {
      if (genRef.current !== gen) return
      dispatch({ type: 'ERROR', error: e.message ?? 'Failed to load invoice' })
    }
  }, [activeOrgId, id])

  useEffect(() => { doFetch().catch(() => {}) }, [doFetch])

  return { invoice: state.data, loading: state.loading, error: state.error, refetch: doFetch }
}

// ── Mutations (imperative) ──────────────────────────────────────────────────────

/** Generate an invoice draft (or preview) from merged-PR git effort. */
export function generateInvoice(body) {
  return post('/api/invoices/generate', body)
}

/**
 * Update an invoice. Accepts any of:
 *   { status, clientName, notes, discountCents, taxCents|taxRate, lines:[…] }
 * `lines` fully replaces the line set (each: { id?, source, description,
 * quantity, unitRateCents, amountCents }). status='sent' mints a share token.
 */
export function patchInvoice(id, body) {
  return patch(`/api/invoices/${id}`, body)
}

/** Delete an invoice. */
export function deleteInvoice(id) {
  return del(`/api/invoices/${id}`)
}

/** Create a draft from explicit lines (rarely used directly; generate is the path). */
export function createInvoice(body) {
  return post('/api/invoices', body)
}

// ── Public share (no auth) ──────────────────────────────────────────────────────

/**
 * usePublicInvoice — fetch a shared invoice by token with NO authentication.
 * Uses a raw fetch (not api.js) so no Authorization / X-Org-ID headers are sent.
 */
export function usePublicInvoice(token) {
  const [state, dispatch] = useReducer(reducer, init)

  useEffect(() => {
    if (!token) return
    let cancelled = false
    dispatch({ type: 'START' })
    fetch(`${BASE}/api/public/invoices/${encodeURIComponent(token)}`)
      .then(async (res) => {
        if (!res.ok) {
          const body = await res.json().catch(() => null)
          throw new Error(body?.error ?? (res.status === 404 ? 'Invoice not found' : `HTTP ${res.status}`))
        }
        return res.json()
      })
      .then((data) => { if (!cancelled) dispatch({ type: 'DONE', data }) })
      .catch((e) => { if (!cancelled) dispatch({ type: 'ERROR', error: e.message ?? 'Failed to load invoice' }) })
    return () => { cancelled = true }
  }, [token])

  return { invoice: state.data, loading: state.loading, error: state.error }
}
