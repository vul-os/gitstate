/**
 * Theme system — light | dark, defaulting to the OS preference.
 *
 * First visit (no stored value): resolve from `prefers-color-scheme` and track
 * the system preference live. Once the user explicitly picks light or dark, we
 * persist that choice to localStorage ('gs-theme') and honor it from then on.
 *
 * The user-facing control is a simple 2-state light↔dark toggle, but we keep
 * 'system' internally as the initial (unpinned) state so a never-chosen visitor
 * follows their PC. Applies the `.light` class to <html>.
 *
 * Usage:
 *   import { ThemeProvider, useTheme } from './lib/theme.tsx'
 *   const { resolved, toggle, setTheme } = useTheme()
 *   // resolved: 'dark' | 'light'  (what's actually rendered)
 *   // toggle(): flip between explicit light/dark
 */
import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'

/** An explicit user pin, or 'system' while unpinned (follows the OS live). */
export type Theme = 'system' | 'light' | 'dark'
/** What is actually rendered — 'system' is always resolved to one of these. */
export type ResolvedTheme = 'light' | 'dark'

export interface ThemeContextValue {
  theme: Theme
  resolved: ResolvedTheme
  setTheme: (next: 'light' | 'dark') => void
  toggle: () => void
}

const ThemeCtx = createContext<ThemeContextValue | null>(null)

const STORAGE_KEY = 'gs-theme'

function prefersLight(): boolean {
  return typeof window !== 'undefined'
    && window.matchMedia('(prefers-color-scheme: light)').matches
}

function getStored(): 'light' | 'dark' | null {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    return v === 'light' || v === 'dark' ? v : null
  } catch {
    return null
  }
}

function applyTheme(resolved: ResolvedTheme): void {
  const html = document.documentElement
  if (resolved === 'light') {
    html.classList.add('light')
    html.style.colorScheme = 'light'
  } else {
    html.classList.remove('light')
    html.style.colorScheme = 'dark'
  }
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  // 'system' = no explicit choice yet → follow OS. Otherwise an explicit pin.
  const [theme, setThemeState] = useState<Theme>(() => getStored() ?? 'system')

  const resolved: ResolvedTheme = theme === 'system'
    ? (prefersLight() ? 'light' : 'dark')
    : theme

  useEffect(() => {
    applyTheme(resolved)
  }, [resolved])

  // While unpinned, track live OS preference changes.
  useEffect(() => {
    if (theme !== 'system') return
    const mq = window.matchMedia('(prefers-color-scheme: light)')
    const handler = () => applyTheme(mq.matches ? 'light' : 'dark')
    mq.addEventListener('change', handler)
    return () => mq.removeEventListener('change', handler)
  }, [theme])

  function setTheme(next: 'light' | 'dark') {
    // Only 'light' | 'dark' are persistable user choices.
    try { localStorage.setItem(STORAGE_KEY, next) } catch { /* ignore */ }
    setThemeState(next)
  }

  function toggle() {
    setTheme(resolved === 'light' ? 'dark' : 'light')
  }

  return (
    <ThemeCtx.Provider value={{ theme, resolved, setTheme, toggle }}>
      {children}
    </ThemeCtx.Provider>
  )
}

// eslint-disable-next-line react-refresh/only-export-components
export function useTheme(): ThemeContextValue {
  const ctx = useContext(ThemeCtx)
  if (!ctx) throw new Error('useTheme must be used inside ThemeProvider')
  return ctx
}
