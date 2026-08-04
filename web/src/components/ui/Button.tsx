/**
 * Button — variants: primary (gradient), ghost, outline, danger
 *
 * Usage:
 *   <Button variant="primary" size="md" onClick={...}>Deploy</Button>
 *   <Button variant="ghost" leftIcon={<Icon />}>Cancel</Button>
 */
import type { ComponentPropsWithoutRef, ReactNode } from 'react'

export type ButtonVariant = 'primary' | 'outline' | 'ghost' | 'danger'
export type ButtonSize = 'xs' | 'sm' | 'md' | 'lg' | 'xl'

export interface ButtonProps extends ComponentPropsWithoutRef<'button'> {
  variant?: ButtonVariant
  size?: ButtonSize
  leftIcon?: ReactNode
  rightIcon?: ReactNode
}

export function Button({
  variant = 'primary',
  size = 'md',
  leftIcon,
  rightIcon,
  className = '',
  disabled,
  children,
  ...props
}: ButtonProps) {
  const base = [
    'inline-flex items-center justify-center gap-2 font-medium',
    'rounded-[var(--radius-btn)] transition-all duration-150 cursor-pointer',
    'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--brand-teal)] focus-visible:ring-offset-1 focus-visible:ring-offset-[var(--bg)]',
    'disabled:opacity-40 disabled:cursor-not-allowed disabled:pointer-events-none',
  ]

  const sizes: Record<ButtonSize, string> = {
    xs: 'text-xs px-2.5 py-1.5 h-7',
    sm: 'text-sm px-3.5 py-2 h-8',
    md: 'text-sm px-4 py-2.5 h-9',
    lg: 'text-base px-6 py-3 h-11',
    xl: 'text-base px-8 py-3.5 h-12',
  }

  const variants: Record<ButtonVariant, string> = {
    primary: [
      'bg-gradient-to-r from-[var(--brand-teal)] to-[var(--brand-indigo)]',
      'text-[#0B1120] font-semibold shadow-sm',
      'hover:opacity-90 hover:shadow-[0_0_20px_rgba(45,212,191,0.3)]',
      'active:scale-[0.98]',
    ].join(' '),

    outline: [
      'border border-[var(--border2)] bg-transparent',
      'text-[var(--text-muted)] hover:border-[var(--brand-teal)] hover:text-[var(--text)]',
      'active:scale-[0.98]',
    ].join(' '),

    ghost: [
      'bg-transparent text-[var(--text-muted)]',
      'hover:bg-[var(--bg-surface2)] hover:text-[var(--text)]',
      'active:scale-[0.98]',
    ].join(' '),

    danger: [
      'bg-red-500/10 border border-red-500/30 text-red-400',
      'hover:bg-red-500/20 hover:border-red-500/50 hover:text-red-300',
      'active:scale-[0.98]',
    ].join(' '),
  }

  return (
    <button
      disabled={disabled}
      className={[...base, sizes[size] ?? sizes.md, variants[variant] ?? variants.primary, className].join(' ')}
      {...props}
    >
      {leftIcon && <span className="shrink-0">{leftIcon}</span>}
      {children}
      {rightIcon && <span className="shrink-0">{rightIcon}</span>}
    </button>
  )
}
