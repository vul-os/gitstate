/**
 * ThemeToggle — single light↔dark button.
 * Shows the icon of the theme you'll switch TO (moon while light, sun while dark)
 * and flips on click. Reduced-motion safe and accessible.
 *
 * Usage:
 *   <ThemeToggle />
 */
import { useTheme } from '../lib/theme.jsx'

const MoonIcon = (
  <svg width="15" height="15" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
    <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
  </svg>
)

const SunIcon = (
  <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
    <circle cx="12" cy="12" r="4.2" fill="currentColor" stroke="none" />
    <path d="M12 2v2.5M12 19.5V22M4.22 4.22l1.77 1.77M18.01 18.01l1.77 1.77M2 12h2.5M19.5 12H22M4.22 19.78l1.77-1.77M18.01 5.99l1.77-1.77" />
  </svg>
)

export function ThemeToggle({ className = '' }) {
  const { resolved, toggle } = useTheme()
  const isLight = resolved === 'light'
  const nextLabel = isLight ? 'Switch to dark theme' : 'Switch to light theme'

  return (
    <button
      type="button"
      onClick={toggle}
      role="switch"
      aria-checked={isLight}
      aria-label={nextLabel}
      title={nextLabel}
      className={[
        'flex items-center justify-center w-8 h-8 rounded-lg cursor-pointer',
        'border border-[var(--border)] bg-[var(--bg-surface3)] text-[var(--text-muted)]',
        'transition-colors duration-150 motion-reduce:transition-none',
        'hover:text-[var(--brand-teal)] hover:border-[var(--border2)]',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--brand-teal)]',
        className,
      ].join(' ')}
    >
      {/* Show the icon of the theme we'd switch to. */}
      {isLight ? MoonIcon : SunIcon}
    </button>
  )
}
