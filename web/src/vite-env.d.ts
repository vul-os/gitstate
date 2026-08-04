/// <reference types="vite/client" />

/**
 * Ambient globals injected by the Tauri shell (or absent entirely in the
 * headless/browser build) — see `lib/api.ts#resolveBaseUrl` and
 * `#openExternal`. All optional: none of these exist outside Tauri.
 */
interface Window {
  /** The daemon origin, injected before first paint when running inside Tauri. */
  __GITSTATE_API__?: string
  /** Legacy/alternate Tauri v1-style invoke bridge. */
  __TAURI_INTERNALS__?: {
    invoke?: (cmd: string, args?: Record<string, unknown>) => unknown
  }
  /** Tauri v2 invoke bridge. */
  __TAURI__?: {
    core?: {
      invoke?: (cmd: string, args?: Record<string, unknown>) => unknown
    }
  }
}
