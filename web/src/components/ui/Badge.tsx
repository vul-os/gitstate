/**
 * Badge / Pill — compact status/label chips.
 *
 * Usage:
 *   <Badge color="teal">Synced</Badge>
 *   <Pill color="add">+42</Pill>  // diff-style green
 */
import type { ComponentPropsWithoutRef } from 'react'

const BADGE_COLORS = {
  default:  'bg-[var(--bg-surface3)] text-[var(--text-muted)] border-[var(--border)]',
  teal:     'bg-[#2DD4BF]/10 text-[#2DD4BF] border-[#2DD4BF]/25',
  indigo:   'bg-[#6366F1]/10 text-[#818cf8] border-[#6366F1]/25',
  green:    'bg-green-500/10 text-green-400 border-green-500/25',
  red:      'bg-red-500/10 text-red-400 border-red-500/25',
  yellow:   'bg-yellow-500/10 text-yellow-400 border-yellow-500/25',
  blue:     'bg-blue-500/10 text-blue-400 border-blue-500/25',
  add:      'bg-[var(--color-gs-add-bg,rgba(34,197,94,0.08))] text-green-400 border-green-500/20',
  del:      'bg-[var(--color-gs-del-bg,rgba(239,68,68,0.08))] text-red-400 border-red-500/20',
} as const

export type BadgeColor = keyof typeof BADGE_COLORS

export interface BadgeProps extends ComponentPropsWithoutRef<'span'> {
  color?: BadgeColor
}

export function Badge({ color = 'default', className = '', children, ...props }: BadgeProps) {
  return (
    <span
      className={[
        'inline-flex items-center gap-1 px-2 py-0.5 rounded-[var(--radius-badge)]',
        'text-[11px] font-mono font-medium border',
        BADGE_COLORS[color] ?? BADGE_COLORS.default,
        className,
      ].join(' ')}
      {...props}
    >
      {children}
    </span>
  )
}

/** Pill — same as Badge but fully rounded */
export function Pill({ color = 'default', className = '', children, ...props }: BadgeProps) {
  return (
    <Badge color={color} className={`rounded-full ${className}`} {...props}>
      {children}
    </Badge>
  )
}
