/**
 * TopBar — section title, live daemon-health pill, theme toggle, and (on narrow
 * screens) the hamburger that toggles the nav drawer.
 */
import { useLocation } from 'react-router-dom'
import { useEffect, useState } from 'react'
import { Menu } from 'lucide-react'
import { ThemeToggle } from './ThemeToggle.jsx'
import { useNavDrawer } from './AppShell.jsx'
import { health } from '../lib/api.js'

const TITLES = {
  '/dashboard': 'Dashboard',
  '/repos': 'Repos',
  '/contexts': 'Contexts',
  '/categories': 'Categories',
  '/classify': 'Classify',
  '/taxonomy': 'Taxonomy',
  '/settings': 'Settings',
}

function Breadcrumb() {
  const { pathname } = useLocation()
  const title =
    TITLES[pathname] ??
    (pathname.startsWith('/repos/') ? 'Repo' : pathname.replace(/^\//, '').replace(/-/g, ' '))
  return (
    <h1 className="text-[14px] font-semibold text-[var(--text)] tracking-tight capitalize truncate">
      {title}
    </h1>
  )
}

/** Polls /health so the pill reflects whether the daemon is actually reachable. */
function DaemonPill() {
  const [ok, setOk] = useState(null)
  useEffect(() => {
    let alive = true
    const check = () => health().then(() => alive && setOk(true)).catch(() => alive && setOk(false))
    check()
    const t = setInterval(check, 15000)
    return () => { alive = false; clearInterval(t) }
  }, [])

  const color = ok === false ? 'var(--bad)' : 'var(--brand-teal)'
  const label = ok === false ? 'daemon offline' : ok === null ? 'connecting' : 'connected'
  return (
    <div
      className="hidden sm:flex items-center gap-1.5 px-2.5 py-1 rounded-full bg-[var(--bg-surface2)] border border-[var(--border)] text-[11px] font-mono"
      style={{ color }}
    >
      <span className="w-1.5 h-1.5 rounded-full animate-pulse" style={{ background: color }} />
      {label}
    </div>
  )
}

export function TopBar() {
  const { navOpen, toggleNav } = useNavDrawer()

  return (
    <header className="h-14 border-b border-[var(--border)] bg-[var(--bg-surface)]/80 backdrop-blur-sm flex items-center px-3 sm:px-6 gap-2 sm:gap-4 sticky top-0 z-20">
      <button
        type="button"
        onClick={toggleNav}
        aria-expanded={navOpen}
        aria-controls="main-content"
        aria-label="Toggle navigation menu"
        className="lg:hidden flex items-center justify-center w-9 h-9 -ml-1 shrink-0 rounded-lg text-[var(--text-muted)] hover:text-[var(--text)] hover:bg-[var(--bg-surface2)] transition-colors duration-150 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--brand-teal)] cursor-pointer"
      >
        <Menu size={18} strokeWidth={2} aria-hidden="true" />
      </button>

      <Breadcrumb />
      <div className="flex-1" />
      <DaemonPill />
      <ThemeToggle />
    </header>
  )
}
