/**
 * TopBar — shows current section title, live sync pill, Ask-AI chat toggle, theme toggle.
 * On narrow screens it also hosts the hamburger that toggles the nav drawer.
 */
import { useLocation } from 'react-router-dom'
import { Sparkles, Menu } from 'lucide-react'
import { ThemeToggle } from './ThemeToggle.jsx'
import { useChatPanel, useNavDrawer } from './AppShell.jsx'

const TITLES = {
  '/dashboard':          'Dashboard',
  '/board':              'Board',
  '/repos':              'Projects',
  '/analytics':          'Analytics',
  '/cycle-time':         'Cycle Time',
  '/involvement':        'Involvement',
  '/capacity':           'Capacity',
  '/settings':           'Settings',
  '/settings/members':   'Members',
  '/settings/billing':   'Billing',
  '/home':               'Home',
}

function Breadcrumb() {
  const { pathname } = useLocation()
  const title = TITLES[pathname] ?? pathname.replace(/^\//, '').replace(/-/g, ' ')
  return (
    <h1 className="text-[14px] font-semibold text-[var(--text)] tracking-tight capitalize truncate">
      {title}
    </h1>
  )
}

export function TopBar() {
  const { chatOpen, toggleChat } = useChatPanel()
  const { navOpen, toggleNav } = useNavDrawer()

  return (
    <header className="h-14 border-b border-[var(--border)] bg-[var(--bg-surface)]/80 backdrop-blur-sm flex items-center px-3 sm:px-6 gap-2 sm:gap-4 sticky top-0 z-20">
      {/* Hamburger — toggles the off-canvas nav drawer on mobile/tablet only. */}
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

      {/* Live sync pill — hidden on the smallest screens to save room. */}
      <div className="hidden sm:flex items-center gap-1.5 px-2.5 py-1 rounded-full bg-[var(--bg-surface2)] border border-[var(--border)] text-[11px] font-mono text-[#2DD4BF]">
        <span className="w-1.5 h-1.5 rounded-full bg-[#2DD4BF] animate-pulse" />
        synced
      </div>

      {/* Ask AI / chat toggle */}
      <button
        type="button"
        onClick={toggleChat}
        aria-pressed={chatOpen}
        title="Ask AI about your repos"
        className={[
          'flex items-center gap-1.5 h-8 px-2.5 sm:px-3 rounded-lg border text-[12px] font-medium transition-all duration-150 cursor-pointer shrink-0',
          chatOpen
            ? 'bg-[var(--brand-teal)]/10 border-[var(--brand-teal)]/40 text-[var(--brand-teal)]'
            : 'bg-[var(--bg-surface3)] border-[var(--border)] text-[var(--text-muted)] hover:text-[var(--text)] hover:border-[var(--border2)]',
        ].join(' ')}
      >
        <Sparkles size={14} strokeWidth={2} />
        <span className="hidden sm:inline">Ask AI</span>
      </button>

      {/* Theme toggle */}
      <ThemeToggle />
    </header>
  )
}
