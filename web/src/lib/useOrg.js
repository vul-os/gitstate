import { createContext, useContext } from 'react'

export const OrgCtx = createContext(null)

export function useOrg() {
  const ctx = useContext(OrgCtx)
  if (!ctx) throw new Error('useOrg must be used inside OrgProvider')
  return ctx
}
