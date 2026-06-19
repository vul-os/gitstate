/**
 * Shared helpers for the leave-management UI.
 */

/** Two-letter initials from a name (fallback: email). */
export function initials(name, email) {
  return name
    ? name.split(' ').map(w => w[0]).join('').slice(0, 2).toUpperCase()
    : (email ?? '?').slice(0, 2).toUpperCase()
}

/** Parse a YYYY-MM-DD string into a local Date at midnight (avoids TZ drift). */
export function parseDate(s) {
  if (!s) return null
  const [y, m, d] = s.split('-').map(Number)
  return new Date(y, (m ?? 1) - 1, d ?? 1)
}

/** Inclusive whole-day count between two YYYY-MM-DD dates (half-day → 0.5). */
export function leaveDays(entry) {
  if (entry.halfDay) return 0.5
  const a = parseDate(entry.startDate)
  const b = parseDate(entry.endDate)
  if (!a || !b) return 0
  return Math.round((b - a) / 86400000) + 1
}

/** Short date label, e.g. "Mar 4". */
export function shortDate(s) {
  const d = parseDate(s)
  return d ? d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' }) : '—'
}

/** Build a fast lookup from leave-type id → type object. */
export function typeIndex(types) {
  const m = {}
  for (const t of types) m[t.id] = t
  return m
}

/** A readable contrast colour (#0B1120 dark text on light chips) is fine for
 *  our bright type colours; we just dim the background to a tint. */
export function tint(hex, alpha = 0.16) {
  const h = (hex ?? '#2DD4BF').replace('#', '')
  const n = parseInt(h.length === 3 ? h.split('').map(c => c + c).join('') : h, 16)
  const r = (n >> 16) & 255
  const g = (n >> 8) & 255
  const b = n & 255
  return `rgba(${r}, ${g}, ${b}, ${alpha})`
}

const STATUS_COLORS = {
  approved: 'green',
  rejected: 'red',
  pending: 'yellow',
}
export function statusColor(s) {
  return STATUS_COLORS[s] ?? 'default'
}
