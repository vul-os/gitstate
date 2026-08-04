/**
 * Local-first "auth" provider.
 *
 * gitstate is now a single-user desktop / headless app — there is no sign-in,
 * no tokens, no org. This provider exists only so the shell keeps a stable
 * `useAuth()` shape (isAuthed is always true). Kept intentionally tiny; the old
 * multi-tenant JWT/refresh machinery was removed with the SaaS backend.
 */
import type { ReactNode } from 'react'
import { AuthCtx, type AuthContextValue, type AuthUser } from './useAuth.ts'

const LOCAL_USER: AuthUser = { id: 'local', name: 'You', email: '' }

export function AuthProvider({ children }: { children: ReactNode }) {
  const value: AuthContextValue = {
    user: LOCAL_USER,
    isAuthed: true,
    // No-ops kept so any stray caller doesn't throw.
    setToken: () => {},
    logout: () => {},
  }
  return <AuthCtx.Provider value={value}>{children}</AuthCtx.Provider>
}
