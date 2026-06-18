import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { AuthProvider } from './lib/auth.jsx'
import { AppShell } from './components/AppShell.jsx'
import Login from './pages/Login.jsx'
import Signup from './pages/Signup.jsx'
import Welcome from './pages/Welcome.jsx'
import Home from './pages/Home.jsx'
import Dashboard from './pages/Dashboard.jsx'
import Projects from './pages/Projects.jsx'
import Settings from './pages/Settings.jsx'
import Members from './pages/Members.jsx'
import InviteAccept from './pages/InviteAccept.jsx'
import Repos from './pages/Repos.jsx'
import Board from './pages/Board.jsx'
import CycleTime from './pages/CycleTime.jsx'
import Involvement from './pages/Involvement.jsx'
import Capacity from './pages/Capacity.jsx'
import Billing from './pages/Billing.jsx'
import NotFound from './pages/NotFound.jsx'

export default function App() {
  return (
    <AuthProvider>
      <BrowserRouter>
        <Routes>
          {/* Logged-out landing */}
          <Route path="/welcome" element={<Welcome />} />

          {/* Public auth routes */}
          <Route path="/login" element={<Login />} />
          <Route path="/signup" element={<Signup />} />

          {/* Invite accept — semi-public: checks auth inside and redirects if needed */}
          <Route path="/invite/accept" element={<InviteAccept />} />

          {/* Protected app shell — redirects to /login if not authed */}
          <Route element={<AppShell />}>
            {/* Post-login home is now the dashboard */}
            <Route index element={<Navigate to="/dashboard" replace />} />
            <Route path="/dashboard" element={<Dashboard />} />
            <Route path="/board" element={<Board />} />
            <Route path="/projects" element={<Projects />} />
            <Route path="/repos" element={<Repos />} />
            <Route path="/cycle-time" element={<CycleTime />} />
            <Route path="/involvement" element={<Involvement />} />
            <Route path="/capacity" element={<Capacity />} />
            <Route path="/settings" element={<Settings />} />
            <Route path="/settings/members" element={<Members />} />
            <Route path="/settings/billing" element={<Billing />} />
            {/* Legacy home stub still accessible */}
            <Route path="/home" element={<Home />} />
          </Route>

          {/* 404 — branded not-found */}
          <Route path="*" element={<NotFound />} />
        </Routes>
      </BrowserRouter>
    </AuthProvider>
  )
}
