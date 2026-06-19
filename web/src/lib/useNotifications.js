/**
 * useNotifications — data hooks for the Settings → Notifications section.
 *
 * Wraps the /api/notifications/* endpoints (channels CRUD, live digest preview,
 * test send, delivery log). All calls go through the shared get/post/patch/del
 * helpers in ../lib/api.js (which inject the Bearer token + X-Org-ID header).
 */
import { useCallback, useEffect, useState } from 'react'
import { get, post, patch, del } from './api.js'

// ── Plain API calls (also usable outside a component) ───────────────────────────

/** Fetch all channels + whether email delivery is configured on the server. */
export function fetchChannels() {
  return get('/api/notifications/channels')
}

/** Create a notification channel. */
export function createChannel(body) {
  return post('/api/notifications/channels', body)
}

/** Partially update a channel. */
export function updateChannel(id, body) {
  return patch(`/api/notifications/channels/${id}`, body)
}

/** Delete a channel. */
export function deleteChannel(id) {
  return del(`/api/notifications/channels/${id}`)
}

/** Render a digest preview without sending. kind: weeklyStatus | stalePRs | ooo */
export function previewDigest(kind) {
  return get(`/api/notifications/preview?kind=${encodeURIComponent(kind)}`)
}

/** Send the channel's enabled digests now (test send). */
export function testSendChannel(id) {
  return post(`/api/notifications/channels/${id}/test`, {})
}

/** Fetch the recent send/preview log. */
export function fetchLog() {
  return get('/api/notifications/log')
}

// ── useChannels — channels list with CRUD + reload ──────────────────────────────

export function useChannels() {
  const [channels, setChannels] = useState([])
  const [emailConfigured, setEmailConfigured] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)

  const reload = useCallback(async () => {
    try {
      const data = await fetchChannels()
      setChannels(Array.isArray(data?.channels) ? data.channels : [])
      setEmailConfigured(Boolean(data?.emailConfigured))
      setError(null)
    } catch (e) {
      setError(e?.message ?? 'Failed to load channels')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    let active = true
    fetchChannels()
      .then((data) => {
        if (!active) return
        setChannels(Array.isArray(data?.channels) ? data.channels : [])
        setEmailConfigured(Boolean(data?.emailConfigured))
      })
      .catch((e) => active && setError(e?.message ?? 'Failed to load channels'))
      .finally(() => active && setLoading(false))
    return () => { active = false }
  }, [])

  return { channels, emailConfigured, loading, error, reload }
}

// ── usePreview — live digest preview for a given kind ───────────────────────────

export function usePreview(kind) {
  // A single state object so the in-flight/loaded transitions happen as one
  // update inside the async callbacks — never a synchronous setState in the
  // effect body (which React flags as a cascading render).
  const [state, setState] = useState({ preview: null, loading: true, error: null, kind: null })

  useEffect(() => {
    let active = true
    previewDigest(kind)
      .then((data) => active && setState({ preview: data, loading: false, error: null, kind }))
      .catch((e) => active && setState({ preview: null, loading: false, error: e?.message ?? 'Failed to build preview', kind }))
    return () => { active = false }
  }, [kind])

  // While the kind has changed but the new fetch hasn't resolved, present a
  // loading state without writing to state synchronously during render.
  const loading = state.loading || state.kind !== kind
  return { preview: state.kind === kind ? state.preview : null, loading, error: state.kind === kind ? state.error : null }
}
