/**
 * useContributors — load + mutate the active org's canonical contributors.
 *
 * Git history names people by raw email/login, so one human appears as many
 * "identities". The backend auto-clusters those into canonical contributors;
 * this hook surfaces them plus the link/invite/exclude/merge/split mutations.
 *
 * Built defensively: if the contributors endpoints aren't live yet (404 /
 * network error) the hook reports `notReady` so the page can show a "run
 * detect" empty state instead of a hard error.
 */
import { useState, useEffect, useCallback, useRef } from 'react'
import * as api from './api.js'
import { useOrg } from './useOrg.js'

export function useContributors() {
  const { activeOrg } = useOrg()
  const orgId = activeOrg?.id

  const [contributors, setContributors] = useState([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)
  const [notReady, setNotReady] = useState(false)
  const [detecting, setDetecting] = useState(false)
  const [detectSummary, setDetectSummary] = useState(null)

  // Avoid setting state after unmount / org switch races.
  const reqRef = useRef(0)

  const load = useCallback(async () => {
    if (!orgId) return
    const seq = ++reqRef.current
    setLoading(true)
    setError(null)
    setNotReady(false)
    try {
      const data = await api.fetchContributors()
      if (seq !== reqRef.current) return
      if (Array.isArray(data)) {
        setContributors(data)
      } else {
        // A non-array payload means the endpoint isn't live yet (e.g. the SPA
        // index.html fallback before the API is deployed) → soft "not ready".
        setContributors([])
        setNotReady(true)
      }
    } catch (err) {
      if (seq !== reqRef.current) return
      // Endpoint not deployed yet → soft "not ready" state, not a red error.
      if (err?.status === 404 || err?.status === 501 || err?.status == null) {
        setNotReady(true)
        setContributors([])
      } else {
        setError(err?.message ?? 'Failed to load contributors')
      }
    } finally {
      if (seq === reqRef.current) setLoading(false)
    }
  }, [orgId])

  useEffect(() => {
    // load() flips loading/error state as it runs — intentional fetch sync.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    load().catch(() => {})
  }, [load])

  const detect = useCallback(async () => {
    setDetecting(true)
    setError(null)
    try {
      const res = await api.detectContributors()
      setDetectSummary({
        contributors: res?.contributors ?? null,
        identities: res?.identities ?? null,
        merged: res?.merged ?? null,
      })
      await load()
      return res
    } catch (err) {
      setError(err?.message ?? 'Detection failed')
      throw err
    } finally {
      setDetecting(false)
    }
  }, [load])

  // ── Mutations — optimistic where safe, always re-load on structural change ──

  const patch = useCallback(async (id, body) => {
    // Optimistic local update for inline edits / toggles.
    setContributors(prev => prev.map(c => (c.id === id ? { ...c, ...body } : c)))
    try {
      const updated = await api.patchContributor(id, body)
      if (updated && typeof updated === 'object' && updated.id) {
        setContributors(prev => prev.map(c => (c.id === id ? { ...c, ...updated } : c)))
      }
    } catch (err) {
      await load() // revert to server truth
      throw err
    }
  }, [load])

  const merge = useCallback(async (id, intoId) => {
    await api.mergeContributors(id, intoId)
    await load()
  }, [load])

  const split = useCallback(async (id, value) => {
    await api.splitIdentity(id, value)
    await load()
  }, [load])

  const link = useCallback(async (id, userId) => {
    await api.linkContributor(id, userId)
    await load()
  }, [load])

  const invite = useCallback(async (id, email) => {
    await api.inviteContributor(id, email)
    await load()
  }, [load])

  return {
    contributors,
    loading,
    error,
    notReady,
    detecting,
    detectSummary,
    reload: load,
    detect,
    patch,
    merge,
    split,
    link,
    invite,
  }
}

/** Members of the active org, for the "link to member" dropdown. */
export function useOrgMembers() {
  const { activeOrg } = useOrg()
  const orgId = activeOrg?.id
  const [members, setMembers] = useState([])
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (!orgId) return
    let alive = true
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLoading(true)
    api.get(`/api/orgs/${orgId}/members`)
      .then(data => { if (alive) setMembers(Array.isArray(data) ? data : []) })
      .catch(() => { if (alive) setMembers([]) })
      .finally(() => { if (alive) setLoading(false) })
    return () => { alive = false }
  }, [orgId])

  return { members, loading }
}
