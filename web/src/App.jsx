import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { AuthProvider } from './lib/auth.jsx'
import { AppShell } from './components/AppShell.jsx'
import Login from './pages/Login.jsx'
import Signup from './pages/Signup.jsx'
import Home from './pages/Home.jsx'
import Projects from './pages/Projects.jsx'
import Settings from './pages/Settings.jsx'
import Members from './pages/Members.jsx'
import InviteAccept from './pages/InviteAccept.jsx'

export default function App() {
  return (
    <AuthProvider>
      <BrowserRouter>
        <Routes>
          {/* Public auth routes */}
          <Route path="/login" element={<Login />} />
          <Route path="/signup" element={<Signup />} />

          {/* Invite accept — semi-public: checks auth inside and redirects if needed */}
          <Route path="/invite/accept" element={<InviteAccept />} />

          {/* Protected app shell — redirects to /login if not authed */}
          <Route element={<AppShell />}>
            <Route index element={<Home />} />
            <Route path="/projects" element={<Projects />} />
            <Route path="/settings" element={<Settings />} />
            <Route path="/settings/members" element={<Members />} />
          </Route>

          {/* Catch-all */}
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </BrowserRouter>
    </AuthProvider>
  )
}
