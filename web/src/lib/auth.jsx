import { useState, useCallback } from 'react'
import { AuthCtx } from './useAuth.js'
import { getToken, setToken as storeToken, logout as apiLogout } from './api.js'

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
    userId: claims.user_id ?? claims.sub,
    orgId: claims.org_id,
    role: claims.role,
    email: claims.email,
    name: claims.name,
  }
}

export function AuthProvider({ children }) {
  const [token, setTokenState] = useState(() => getToken())
  const user = userFromToken(token)

  const setToken = useCallback((t) => {
    storeToken(t)
    setTokenState(t)
  }, [])

  const logout = useCallback(() => {
    apiLogout()
    setTokenState(null)
  }, [])

  return (
    <AuthCtx.Provider value={{ token, user, setToken, logout, isAuthed: !!token }}>
      {children}
    </AuthCtx.Provider>
  )
}
