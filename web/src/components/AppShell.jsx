import { Outlet, Navigate } from 'react-router-dom'
import { createContext, useContext, useState, useRef, useEffect, lazy, Suspense } from 'react'
import { Sidebar } from './Sidebar.jsx'
import { TopBar } from './TopBar.jsx'
import { useAuth } from '../lib/useAuth.js'
import { useFocusTrap } from '../lib/useFocusTrap.js'

// The AI chat rail is only mounted when the user opens it, so keep it out of
// the eager shell bundle and load its chunk on first open.
const ChatPanel = lazy(() =>
  import('./chat/ChatPanel.jsx').then((m) => ({ default: m.ChatPanel })),
)

const ChatCtx = createContext(null)
const NavCtx = createContext(null)

/** Access the chat panel open/close state from anywhere in the shell. */
// eslint-disable-next-line react-refresh/only-export-components
export function useChatPanel() {
  const ctx = useContext(ChatCtx)
  if (!ctx) throw new Error('useChatPanel must be used inside AppShell')
  return ctx
}

/** Access the mobile nav-drawer open/close state (used by TopBar hamburger). */
// eslint-disable-next-line react-refresh/only-export-components
export function useNavDrawer() {
  const ctx = useContext(NavCtx)
  if (!ctx) throw new Error('useNavDrawer must be used inside AppShell')
  return ctx
}

/**
 * App shell — sidebar + (top bar + content row).
 *
 * Layout is responsive:
 *   - Desktop (≥lg): the Sidebar is a fixed in-flow rail; main content fills the
 *     rest, with an optional right-hand AI chat rail.
 *   - Mobile / tablet (<lg): the Sidebar collapses to an off-canvas drawer that
 *     the TopBar hamburger toggles. The drawer overlays the content, traps focus,
 *     and closes on Escape / scrim click / navigation. Main content is full-width
 *     and the chat panel becomes a full-screen overlay.
 *
 * Chat & nav-drawer open/closed state are lifted here so they persist across
 * navigation and the TopBar buttons can toggle them.
 * Redirects to /login if not authenticated.
 */
export function AppShell() {
  const { isAuthed } = useAuth()
  const [chatOpen, setChatOpen] = useState(false)
  const [navOpen, setNavOpen] = useState(false)
  const drawerRef = useRef(null)

  // Trap focus inside the mobile drawer while it's open.
  useFocusTrap(drawerRef, navOpen, () => setNavOpen(false))

  // Lock body scroll while the off-canvas drawer is open on small screens.
  useEffect(() => {
    if (!navOpen) return
    const prev = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.body.style.overflow = prev
    }
  }, [navOpen])

  if (!isAuthed) return <Navigate to="/login" replace />

  const chat = {
    chatOpen,
    openChat: () => setChatOpen(true),
    closeChat: () => setChatOpen(false),
    toggleChat: () => setChatOpen(v => !v),
  }

  const nav = {
    navOpen,
    openNav: () => setNavOpen(true),
    closeNav: () => setNavOpen(false),
    toggleNav: () => setNavOpen(v => !v),
  }

  return (
    <NavCtx.Provider value={nav}>
    <ChatCtx.Provider value={chat}>
      <a
        href="#main-content"
        className="sr-only focus:not-sr-only focus:fixed focus:top-3 focus:left-3 focus:z-[100] focus:px-4 focus:py-2 focus:rounded-[var(--radius-btn)] focus:bg-[var(--bg-surface)] focus:text-[var(--text)] focus:border focus:border-[var(--brand-teal)] focus:shadow-[var(--shadow-float)] focus:outline-none"
      >
        Skip to content
      </a>
      <div className="flex min-h-screen bg-[var(--bg)]">
        {/* Desktop sidebar rail — hidden below lg, where the drawer takes over. */}
        <div className="hidden lg:flex">
          <Sidebar />
        </div>

        {/* Mobile / tablet off-canvas drawer + scrim. */}
        <div
          className={[
            'lg:hidden fixed inset-0 z-50 transition-opacity duration-200',
            navOpen ? 'opacity-100' : 'pointer-events-none opacity-0',
          ].join(' ')}
          aria-hidden={!navOpen}
        >
          <button
            type="button"
            aria-label="Close navigation menu"
            tabIndex={navOpen ? 0 : -1}
            onClick={nav.closeNav}
            className="absolute inset-0 bg-black/50 backdrop-blur-sm"
          />
          <div
            ref={drawerRef}
            role="dialog"
            aria-modal="true"
            aria-label="Navigation menu"
            className={[
              'absolute inset-y-0 left-0 w-[268px] max-w-[85vw] shadow-[var(--shadow-float)] transition-transform duration-200 ease-out',
              navOpen ? 'translate-x-0' : '-translate-x-full',
            ].join(' ')}
          >
            <Sidebar onNavigate={nav.closeNav} />
          </div>
        </div>

        <div className="flex flex-col flex-1 min-w-0">
          <TopBar />
          <div className="flex flex-1 min-h-0">
            <main id="main-content" tabIndex={-1} className="flex-1 min-w-0 overflow-y-auto">
              <div className="mx-auto w-full max-w-[1400px] px-4 py-5 sm:px-6 sm:py-6 lg:px-8 lg:py-8">
                <Outlet />
              </div>
            </main>
            {chatOpen && (
              <aside className="fixed inset-0 z-50 w-full h-screen lg:static lg:inset-auto lg:z-auto lg:w-[380px] lg:h-[calc(100vh-3.5rem)] shrink-0 border-l border-[var(--border)] bg-[var(--bg-surface)] flex flex-col lg:sticky lg:top-14">
                <Suspense fallback={null}>
                  <ChatPanel onClose={chat.closeChat} />
                </Suspense>
              </aside>
            )}
          </div>
        </div>
      </div>
    </ChatCtx.Provider>
    </NavCtx.Provider>
  )
}
