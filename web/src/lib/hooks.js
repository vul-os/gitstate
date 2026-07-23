/**
 * Small data-fetching helpers shared by the local-first pages.
 * No global cache — each hook owns its own request lifecycle against the daemon.
 */
import { useState, useEffect, useCallback, useRef } from 'react'
import { ApiError } from './api.js'

/**
 * Run an async loader, exposing { data, loading, error, reload, setData }.
 * Re-runs whenever `deps` change. Ignores results from stale runs.
 *
 * @template T
 * @param {() => Promise<T>} loader
 * @param {any[]} deps
 */
export function useAsync(loader, deps = []) {
  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const runId = useRef(0)

  // eslint-disable-next-line react-hooks/exhaustive-deps
  const stableLoader = useCallback(loader, deps)

  const reload = useCallback(async () => {
    const id = ++runId.current
    setLoading(true)
    setError(null)
    try {
      const result = await stableLoader()
      if (id === runId.current) setData(result)
    } catch (err) {
      if (id === runId.current) {
        setError(err instanceof ApiError ? err : new ApiError(0, String(err?.message || err)))
      }
    } finally {
      if (id === runId.current) setLoading(false)
    }
  }, [stableLoader])

  useEffect(() => {
    reload()
  }, [reload])

  return { data, loading, error, reload, setData }
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
