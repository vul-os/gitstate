/**
 * Org localStorage helpers — split into a plain JS module so the
 * fast-refresh linter rule (react-refresh/only-export-components) is satisfied.
 */

const ACTIVE_ORG_KEY = 'gs_active_org'

export function getActiveOrgId() {
  return localStorage.getItem(ACTIVE_ORG_KEY) ?? null
}

export function setActiveOrgId(id) {
  if (id) {
    localStorage.setItem(ACTIVE_ORG_KEY, id)
  } else {
    localStorage.removeItem(ACTIVE_ORG_KEY)
  }
}
