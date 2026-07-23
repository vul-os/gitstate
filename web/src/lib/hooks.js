/**
 * Small data-fetching helpers shared by the local-first pages.
 * No global cache — each hook owns its own request lifecycle against the daemon.
 */
import { useState, useEffect, useCallback, useRef } from 'react'
import { ApiError } from './api.js'

function asApiError(err) {
  return err instanceof ApiError ? err : new ApiError(0, String(err?.message || err))
}

/**
 * Run an async loader, exposing { data, loading, error, reload }.
 * Re-runs whenever `deps` change, and drops results from superseded runs.
 *
 * `loading` is DERIVED, not stored: each run gets a fresh identity token, and
 * the hook reports loading until the settled result carries the current token.
 * That keeps every state update inside a promise callback — no synchronous
 * setState in the effect body, and so no cascading render on mount.
 *
 * The loader is read through a ref because callers pass a fresh closure every
 * render (`() => load(days)`); re-running is driven by `deps`, never by the
 * function's identity.
 *
 * @template T
 * @param {() => Promise<T>} loader
 * @param {any[]} deps
 */
export function useAsync(loader, deps = []) {
  const [nonce, setNonce] = useState(0)
  const reload = useCallback(() => setNonce((n) => n + 1), [])

  // Seeded with the first loader, then refreshed by an effect declared BEFORE
  // the fetch effect — effects run in declaration order, so the fetch always
  // sees the current render's closure without writing a ref during render.
  const loaderRef = useRef(loader)
  useEffect(() => {
    loaderRef.current = loader
  })

  // The run's identity. Serialized so the dependency list stays a literal;
  // callers only ever pass primitives (an id, a day count, a repo id).
  const runKey = `${nonce}|${JSON.stringify(deps)}`

  const [settled, setSettled] = useState({ key: null, data: null, error: null })

  useEffect(() => {
    let cancelled = false
    loaderRef
      .current()
      .then((data) => {
        if (!cancelled) setSettled({ key: runKey, data, error: null })
      })
      .catch((err) => {
        if (!cancelled) setSettled({ key: runKey, data: null, error: asApiError(err) })
      })
    return () => {
      cancelled = true
    }
  }, [runKey])

  const current = settled.key === runKey
  return {
    data: current ? settled.data : null,
    loading: !current,
    error: current ? settled.error : null,
    reload,
  }
}

/**
 * Wrap a mutating call with pending + error state.
 * Returns [run, { pending, error }] — `run(...args)` resolves to the result.
 */
export function useAction(fn) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState(null)
  const run = useCallback(async (...args) => {
    setPending(true)
    setError(null)
    try {
      return await fn(...args)
    } catch (err) {
      const e = err instanceof ApiError ? err : new ApiError(0, String(err?.message || err))
      setError(e)
      throw e
    } finally {
      setPending(false)
    }
  }, [fn])
  return [run, { pending, error }]
}
