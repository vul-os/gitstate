/**
 * Small data-fetching helpers shared by the local-first pages.
 * No global cache — each hook owns its own request lifecycle against the daemon.
 */
import { useState, useEffect, useCallback, useRef } from 'react'
import { ApiError } from './api.ts'

function asApiError(err: unknown): ApiError {
  if (err instanceof ApiError) return err
  const message = err instanceof Error ? err.message : String(err)
  return new ApiError(0, message)
}

interface Settled<T> {
  key: string | null
  data: T | null
  error: ApiError | null
}

export interface AsyncResult<T> {
  data: T | null
  loading: boolean
  error: ApiError | null
  reload: () => void
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
 */
export function useAsync<T>(loader: () => Promise<T>, deps: unknown[] = []): AsyncResult<T> {
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

  const [settled, setSettled] = useState<Settled<T>>({ key: null, data: null, error: null })

  useEffect(() => {
    let cancelled = false
    loaderRef
      .current()
      .then((data) => {
        if (!cancelled) setSettled({ key: runKey, data, error: null })
      })
      .catch((err: unknown) => {
        if (!cancelled) setSettled({ key: runKey, data: null, error: asApiError(err) })
      })
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runKey])

  const current = settled.key === runKey
  return {
    data: current ? settled.data : null,
    loading: !current,
    error: current ? settled.error : null,
    reload,
  }
}

export interface ActionResult {
  pending: boolean
  error: ApiError | null
}

/**
 * Wrap a mutating call with pending + error state.
 * Returns [run, { pending, error }] — `run(...args)` resolves to the result.
 */
export function useAction<Args extends unknown[], R>(
  fn: (...args: Args) => Promise<R>,
): [(...args: Args) => Promise<R>, ActionResult] {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<ApiError | null>(null)
  const run = useCallback(
    async (...args: Args): Promise<R> => {
      setPending(true)
      setError(null)
      try {
        return await fn(...args)
      } catch (err) {
        const e = asApiError(err)
        setError(e)
        throw e
      } finally {
        setPending(false)
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [fn],
  )
  return [run, { pending, error }]
}
