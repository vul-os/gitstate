import { createContext, useContext } from 'react'

export interface AuthUser {
  id: string
  name: string
  email: string
}

export interface AuthContextValue {
  user: AuthUser
  isAuthed: boolean
  setToken: (token?: string) => void
  logout: () => void
}

export const AuthCtx = createContext<AuthContextValue | null>(null)

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthCtx)
  if (!ctx) throw new Error('useAuth must be used inside AuthProvider')
  return ctx
}
