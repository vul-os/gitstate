import { useState, useCallback } from 'react'
import { AuthCtx } from './useAuth.js'
import { OrgProvider } from './org.jsx'
import {
  getToken,
  setTokenPair as storeTokenPair,
  clearTokens,
  logout as apiLogout,
} from './api.js'

function parseJwt(token) {
  try {
    const payload = token.split('.')[1]
    return JSON.parse(atob(payload.replace(/-/g, '+').replace(/_/g, '/')))
  } catch {
    return null
  }
}

function userFromToken(token) {
  if (!token) return null
  const claims = parseJwt(token)
  if (!claims) return null
  return {
    id: claims.user_id ?? claims.sub ?? null,
    orgId: claims.org_id ?? null,
    role: claims.role ?? null,
    email: claims.email ?? null,
    name: claims.name ?? null,
  }
}

export function AuthProvider({ children }) {
  const [accessToken, setAccessToken] = useState(() => getToken())
  const user = userFromToken(accessToken)

  /**
   * Store a full token pair and update React state so the app reflects the new auth status.
   * Called after login / signup / refresh.
   */
  const setToken = useCallback((accessTok, refreshTok) => {
    storeTokenPair(accessTok, refreshTok ?? null)
    setAccessToken(accessTok ?? null)
  }, [])

  /**
   * Sign out: call the API (fire-and-forget), then clear all local tokens.
   */
  const logout = useCallback(async () => {
    try {
      await apiLogout()
    } catch {
      // If the API call fails, we still clear local state
      clearTokens()
    }
    setAccessToken(null)
  }, [])

  const isAuthed = !!accessToken

  return (
    <AuthCtx.Provider
      value={{
        token: accessToken,
        user,
        setToken,
        logout,
        isAuthed,
      }}
    >
      <OrgProvider isAuthed={isAuthed}>
        {children}
      </OrgProvider>
    </AuthCtx.Provider>
  )
}
