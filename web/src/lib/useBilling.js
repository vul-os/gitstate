/**
 * useBilling — billing data hooks.
 * Fetches plans, subscription, usage, and invoices from the billing API.
 * Handles 404/403 gracefully (billing disabled in OSS builds).
 */
import { useReducer, useEffect, useCallback, useRef, useState } from 'react'
import { useOrg } from './useOrg.js'
import * as api from './api.js'

// ── Public billing flag (cached) ──────────────────────────────────────────────
// GET /api/config exposes the public billing config. We resolve whether billing
// is enabled from it FIRST (billing.chargeCurrency) and short-circuit to
// "disabled" WITHOUT touching /api/billing/* when billing is off — mirroring how
// the calendar/notifications sections gate on server-reported config. This keeps
// OSS / billing-disabled builds from spamming 404s on /api/billing/* across
// navigation. The verdict is cached at module scope (a shared in-flight promise
// dedupes concurrent hook mounts).
//
// Belt-and-braces: some instances report a charge currency even while the
// billing endpoints are disabled (they 404 with "billing is not enabled"). If we
// ever observe that 404/403, we persist a sticky "disabled" verdict in
// localStorage so every subsequent visit/reload short-circuits with zero billing
// requests — no repeated console noise on the Billing page.
// NOTE: the key is versioned (-v2). A prior bug let a *free-plan* subscription
// 404 wrongly mark billing "disabled" and persist it stickily, blanking the whole
// Billing page on reload. Bumping the key invalidates those poisoned verdicts so
// the flag is re-derived from /api/config; only a genuine config-level "billing
// off" sticks now.
const BILLING_DISABLED_KEY = 'gs-billing-config-v2'

let _billingFlag = null            // null = unknown, true/false once resolved
let _billingFlagPromise = null

function readDisabledCache() {
  try { return localStorage.getItem(BILLING_DISABLED_KEY) === 'disabled' }
  catch { return false }
}

/** Persist a sticky "billing disabled" verdict so future loads never call /api/billing/*. */
export function markBillingDisabled() {
  _billingFlag = false
  try { localStorage.setItem(BILLING_DISABLED_KEY, 'disabled') } catch { /* ignore */ }
}

function resolveBillingEnabled() {
  if (_billingFlag !== null) return Promise.resolve(_billingFlag)
  // Sticky cache: a prior billing 404/403 → billing is off on this instance.
  if (readDisabledCache()) {
    _billingFlag = false
    return Promise.resolve(false)
  }
  if (!_billingFlagPromise) {
    _billingFlagPromise = api.fetchConfig()
      .then((cfg) => {
        _billingFlag = Boolean(cfg?.billing?.chargeCurrency)
        return _billingFlag
      })
      .catch(() => {
        // Config unavailable → assume billing disabled (degrade gracefully,
        // no billing requests fired).
        _billingFlag = false
        return _billingFlag
      })
  }
  return _billingFlagPromise
}

/** Hook: resolves whether billing is enabled on this instance (cached). */
export function useBillingEnabled() {
  // Seed from the cached flag so a resolved value renders immediately with no
  // effect-driven setState.
  const [enabled, setEnabled] = useState(_billingFlag)
  useEffect(() => {
    if (_billingFlag !== null) return // already resolved → state already seeded
    let active = true
    resolveBillingEnabled().then((v) => active && setEnabled(v))
    return () => { active = false }
  }, [])
  return enabled // null while loading, then true | false
}

// ── Generic data hook factory ─────────────────────────────────────────────────

const initState = { data: null, loading: false, error: null, disabled: false }

function makeReducer() {
  return function reducer(state, action) {
    switch (action.type) {
      case 'FETCH_START':   return { ...state, loading: true, error: null, disabled: false }
      case 'FETCH_DONE':    return { ...state, loading: false, data: action.data }
      case 'FETCH_ERROR':   return { ...state, loading: false, error: action.error }
      case 'FETCH_DISABLED':return { ...state, loading: false, disabled: true, data: null }
      default: return state
    }
  }
}

// `notFoundIsEmpty`: some endpoints return 404 as a *legitimate empty result*
// rather than "billing disabled". The subscription endpoint 404s when the org has
// no subscription (i.e. it's on the implicit Free plan) — that is NOT a signal
// that billing is off, so we must NOT mark billing disabled or short-circuit the
// whole page. We can rely on this because every billing fetch first gates on the
// public billing flag (/api/config); if billing were genuinely disabled the
// request would never fire. So any runtime 404 from such an endpoint means
// "free / empty", surfaced as data:null with disabled:false.
function makeFetcher(path, { notFoundIsEmpty = false } = {}) {
  return function useFetcher(orgId) {
    const [state, dispatch] = useReducer(makeReducer(), initState)
    const genRef = useRef(0)

    const doFetch = useCallback(async () => {
      if (!orgId) return
      const gen = ++genRef.current
      dispatch({ type: 'FETCH_START' })
      try {
        // Gate on the public billing flag — don't hit /api/billing/* (and
        // generate 404 noise) when billing isn't configured on this instance.
        const enabled = await resolveBillingEnabled()
        if (genRef.current !== gen) return
        if (!enabled) {
          dispatch({ type: 'FETCH_DISABLED' })
          return
        }
        const data = await api.get(path)
        if (genRef.current !== gen) return
        dispatch({ type: 'FETCH_DONE', data })
      } catch (e) {
        // Free-plan / empty: a 404 here means "no subscription", not "billing
        // off". Surface it as an empty success — keep billing enabled so plans,
        // usage meters and the ladder still render against the Free plan.
        if (notFoundIsEmpty && e.status === 404) {
          if (genRef.current !== gen) return
          dispatch({ type: 'FETCH_DONE', data: null })
          return
        }
        if (e.status === 404 || e.status === 403) {
          // Billing endpoint reports disabled → make it sticky so future loads
          // short-circuit before firing any /api/billing/* request.
          markBillingDisabled()
        }
        if (genRef.current !== gen) return
        if (e.status === 404 || e.status === 403) {
          dispatch({ type: 'FETCH_DISABLED' })
        } else {
          dispatch({ type: 'FETCH_ERROR', error: e.message ?? 'Failed to load' })
        }
      }
    }, [orgId])

    useEffect(() => { doFetch().catch(() => {}) }, [doFetch])

    return { ...state, refetch: doFetch }
  }
}

// ── Plans hook ────────────────────────────────────────────────────────────────

export function usePlans() {
  const { activeOrgId } = useOrg()
  const fetcher = makeFetcher('/api/billing/plans')
  return fetcher(activeOrgId)
}

// ── Subscription hook ─────────────────────────────────────────────────────────

export function useSubscription() {
  const { activeOrgId } = useOrg()
  // A 404 here = no subscription = the org is on the implicit Free plan. Treat it
  // as an empty result (data:null), NOT as "billing disabled" — otherwise a normal
  // free-plan org would poison the sticky billing flag and blank out the whole page.
  const fetcher = makeFetcher('/api/billing/subscription', { notFoundIsEmpty: true })
  return fetcher(activeOrgId)
}

// ── Usage hook ────────────────────────────────────────────────────────────────

export function useUsage() {
  const { activeOrgId } = useOrg()
  const fetcher = makeFetcher('/api/billing/usage')
  return fetcher(activeOrgId)
}

// ── Invoices hook ─────────────────────────────────────────────────────────────

export function useInvoices() {
  const { activeOrgId } = useOrg()
  const fetcher = makeFetcher('/api/billing/invoices')
  return fetcher(activeOrgId)
}

// ── Invoice detail hook ───────────────────────────────────────────────────────

export function useInvoiceDetail(id) {
  const { activeOrgId } = useOrg()
  const [state, dispatch] = useReducer(makeReducer(), initState)
  const genRef = useRef(0)

  const doFetch = useCallback(async () => {
    if (!activeOrgId || !id) return
    const gen = ++genRef.current
    dispatch({ type: 'FETCH_START' })
    try {
      const enabled = await resolveBillingEnabled()
      if (genRef.current !== gen) return
      if (!enabled) {
        dispatch({ type: 'FETCH_DISABLED' })
        return
      }
      const data = await api.get(`/api/billing/invoices/${id}`)
      if (genRef.current !== gen) return
      dispatch({ type: 'FETCH_DONE', data })
    } catch (e) {
      if (e.status === 404 || e.status === 403) {
        markBillingDisabled()
      }
      if (genRef.current !== gen) return
      if (e.status === 404 || e.status === 403) {
        dispatch({ type: 'FETCH_DISABLED' })
      } else {
        dispatch({ type: 'FETCH_ERROR', error: e.message ?? 'Failed to load invoice' })
      }
    }
  }, [activeOrgId, id])

  useEffect(() => { doFetch().catch(() => {}) }, [doFetch])

  return { ...state, refetch: doFetch }
}
