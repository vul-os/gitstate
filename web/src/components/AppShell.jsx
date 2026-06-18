import { Outlet, Navigate } from 'react-router-dom'
import { Sidebar } from './Sidebar.jsx'
import { TopBar } from './TopBar.jsx'
import { useAuth } from '../lib/useAuth.js'

/**
 * App shell — sidebar + top bar + routed content area.
 * Redirects to /login if not authenticated.
 */
export function AppShell() {
  const { isAuthed } = useAuth()
  if (!isAuthed) return <Navigate to="/login" replace />

  return (
    <div className="flex min-h-screen bg-[#0B1120]">
      <Sidebar />
      <div className="flex flex-col flex-1 min-w-0">
        <TopBar />
        <main className="flex-1 p-8 overflow-y-auto">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
