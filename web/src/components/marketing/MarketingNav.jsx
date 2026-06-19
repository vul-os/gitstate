/**
 * MarketingNav — sticky top nav for marketing pages.
 * Logo + nav links + ThemeToggle + CurrencySelector + auth CTAs.
 */
import { useState, useEffect } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { LogoFull } from '../Logo.jsx'
import { ThemeToggle } from '../ThemeToggle.jsx'
import { CurrencySelector } from '../CurrencySelector.jsx'

const NAV_LINKS = [
  { label: 'Product',  to: '/' },
  { label: 'Pricing',  to: '/pricing' },
  { label: 'Compare',  to: '/compare' },
  { label: 'Docs',     to: '/docs' },
]

export function MarketingNav() {
  const [scrolled, setScrolled] = useState(false)
  const [mobileOpen, setMobileOpen] = useState(false)
  const [progress, setProgress] = useState(0)
  const location = useLocation()

  useEffect(() => {
    function onScroll() {
      setScrolled(window.scrollY > 8)
      const doc = document.documentElement
      const max = doc.scrollHeight - doc.clientHeight
      setProgress(max > 0 ? Math.min(1, window.scrollY / max) : 0)
    }
    onScroll()
    window.addEventListener('scroll', onScroll, { passive: true })
    window.addEventListener('resize', onScroll, { passive: true })
    return () => {
      window.removeEventListener('scroll', onScroll)
      window.removeEventListener('resize', onScroll)
    }
  }, [])

  function closeMenu() { setMobileOpen(false) }

  return (
    <header
      className={[
        'fixed top-0 left-0 right-0 z-50 transition-all duration-300',
        scrolled
          ? 'border-b border-[var(--border)] bg-[var(--bg)]/90 backdrop-blur-xl shadow-sm shadow-black/20'
          : 'bg-transparent',
      ].join(' ')}
    >
      <div className="mx-auto max-w-7xl px-5 md:px-8 h-14 flex items-center gap-6">
        {/* Logo */}
        <Link to="/" aria-label="gitstate home" className="shrink-0" onClick={closeMenu}>
          <LogoFull height={30} />
        </Link>

        {/* Desktop nav links */}
        <nav className="hidden md:flex items-center gap-1 ml-2" aria-label="Main navigation">
          {NAV_LINKS.map(({ label, to }) => {
            const active = location.pathname === to
            return (
              <Link
                key={to}
                to={to}
                className={[
                  'px-3 py-1.5 rounded-lg text-sm font-medium transition-colors duration-150',
                  active
                    ? 'text-[var(--text)] bg-[var(--bg-surface2)]'
                    : 'text-[var(--text-muted)] hover:text-[var(--text)] hover:bg-[var(--bg-surface2)]',
                ].join(' ')}
              >
                {label}
              </Link>
            )
          })}
        </nav>

        {/* Spacer */}
        <div className="flex-1" />

        {/* Right cluster */}
        <div className="hidden md:flex items-center gap-2.5">
          <ThemeToggle />
          <CurrencySelector />
          <div className="w-px h-5 bg-[var(--border)]" aria-hidden="true" />
          <Link
            to="/login"
            className="px-3.5 py-1.5 text-sm font-medium text-[var(--text-muted)] hover:text-[var(--text)] transition-colors duration-150 rounded-lg hover:bg-[var(--bg-surface2)]"
          >
            Sign in
          </Link>
          <Link
            to="/signup"
            className="inline-flex items-center gap-1.5 px-4 py-1.5 rounded-[var(--radius-btn)] text-sm font-semibold text-[#0B1120] bg-gradient-to-r from-[var(--brand-teal)] to-[var(--brand-indigo)] hover:opacity-90 hover:shadow-[0_0_20px_rgba(45,212,191,0.3)] transition-all duration-150 active:scale-[0.98]"
          >
            Get started
            <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M3 8h10M9 4l4 4-4 4"/>
            </svg>
          </Link>
        </div>

        {/* Mobile hamburger */}
        <button
          className="md:hidden flex items-center justify-center w-9 h-9 rounded-lg text-[var(--text-muted)] hover:text-[var(--text)] hover:bg-[var(--bg-surface2)] transition-colors duration-150"
          aria-label={mobileOpen ? 'Close menu' : 'Open menu'}
          aria-expanded={mobileOpen}
          onClick={() => setMobileOpen(v => !v)}
        >
          {mobileOpen ? (
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
              <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
            </svg>
          ) : (
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
              <line x1="4" y1="7" x2="20" y2="7"/><line x1="4" y1="12" x2="20" y2="12"/><line x1="4" y1="17" x2="20" y2="17"/>
            </svg>
          )}
        </button>
      </div>

      {/* Scroll progress bar — premium reading-position indicator */}
      <div
        aria-hidden="true"
        className={[
          'absolute bottom-0 left-0 right-0 h-px origin-left transition-opacity duration-300',
          scrolled ? 'opacity-100' : 'opacity-0',
        ].join(' ')}
        style={{
          transform: `scaleX(${progress})`,
          background: 'linear-gradient(to right, var(--brand-teal), var(--brand-indigo))',
          willChange: 'transform',
        }}
      />

      {/* Mobile menu drawer */}
      {mobileOpen && (
        <div className="md:hidden border-t border-[var(--border)] bg-[var(--bg)]/95 backdrop-blur-xl">
          <div className="px-5 py-4 flex flex-col gap-1">
            {NAV_LINKS.map(({ label, to }) => (
              <Link
                key={to}
                to={to}
                onClick={closeMenu}
                className="px-3 py-2.5 rounded-lg text-sm font-medium text-[var(--text-muted)] hover:text-[var(--text)] hover:bg-[var(--bg-surface2)] transition-colors duration-150"
              >
                {label}
              </Link>
            ))}
            <div className="h-px bg-[var(--border)] my-2" />
            <div className="flex items-center gap-2.5 px-1 py-1">
              <ThemeToggle />
              <CurrencySelector />
            </div>
            <div className="flex flex-col gap-2 mt-2">
              <Link
                to="/login"
                onClick={closeMenu}
                className="px-4 py-2.5 rounded-[var(--radius-btn)] text-sm font-medium text-center text-[var(--text-muted)] border border-[var(--border)] hover:border-[var(--border2)] hover:text-[var(--text)] transition-all duration-150"
              >
                Sign in
              </Link>
              <Link
                to="/signup"
                onClick={closeMenu}
                className="px-4 py-2.5 rounded-[var(--radius-btn)] text-sm font-semibold text-center text-[#0B1120] bg-gradient-to-r from-[var(--brand-teal)] to-[var(--brand-indigo)] hover:opacity-90 transition-all duration-150"
              >
                Get started free
              </Link>
            </div>
          </div>
        </div>
      )}
    </header>
  )
}
