/**
 * Local-first "auth" provider.
 *
 * gitstate is now a single-user desktop / headless app — there is no sign-in,
 * no tokens, no org. This provider exists only so the shell keeps a stable
 * `useAuth()` shape (isAuthed is always true). Kept intentionally tiny; the old
 * multi-tenant JWT/refresh machinery was removed with the SaaS backend.
 */
import { AuthCtx } from './useAuth.js'

const LOCAL_USER = { id: 'local', name: 'You', email: '' }

export function AuthProvider({ children }) {
  const value = {
    user: LOCAL_USER,
    isAuthed: true,
    // No-ops kept so any stray caller doesn't throw.
    setToken: () => {},
    logout: () => {},
  }
  return <AuthCtx.Provider value={value}>{children}</AuthCtx.Provider>
}
