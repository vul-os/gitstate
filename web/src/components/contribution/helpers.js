/**
 * Pure helpers for the Contribution page — formatting + the live composite engine.
 * Kept out of the JSX module so Fast Refresh stays happy (components-only exports there).
 */
import { DIMENSIONS } from '../../lib/useContribution.js'

export const fmtNum = (n) => (n == null ? '—' : Number(n).toLocaleString())

export function fmtDate(s, opts = { month: 'short', day: 'numeric', year: 'numeric' }) {
  if (!s) return '—'
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return s
  return d.toLocaleDateString(undefined, opts)
}

export function relTime(s) {
  if (!s) return ''
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return ''
  const days = Math.floor((Date.now() - d.getTime()) / 86400000)
  if (days <= 0) return 'today'
  if (days === 1) return 'yesterday'
  if (days < 30) return `${days}d ago`
  if (days < 365) return `${Math.floor(days / 30)}mo ago`
  return `${Math.floor(days / 365)}y ago`
}

export const clamp01to100 = (n) => Math.max(0, Math.min(100, Number(n) || 0))

/**
 * Composite from per-dimension scores + slider weights (normalised to sum 1).
 * Pure — the live re-rank engine.
 */
export function computeComposite(dimensions, weights) {
  let wsum = 0
  for (const k of Object.keys(weights)) wsum += Math.max(0, Number(weights[k]) || 0)
  if (wsum <= 0) return 0
  let acc = 0
  for (const d of DIMENSIONS) {
    const score = clamp01to100(dimensions?.[d.key]?.score)
    const w = Math.max(0, Number(weights[d.key]) || 0) / wsum
    acc += score * w
  }
  return acc
}

export function hueFromStr(str = '') {
  let h = 0
  for (let i = 0; i < str.length; i++) h = (h * 31 + str.charCodeAt(i)) >>> 0
  return h % 360
}
