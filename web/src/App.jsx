import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { AuthProvider } from './lib/auth.jsx'
import { useAuth } from './lib/useAuth.js'
import { AppShell } from './components/AppShell.jsx'
import MarketingLayout from './components/marketing/MarketingLayout.jsx'
import Landing from './pages/Landing.jsx'
import Pricing from './pages/Pricing.jsx'
import Compare from './pages/Compare.jsx'
import Docs from './pages/Docs.jsx'
import Login from './pages/Login.jsx'
import Signup from './pages/Signup.jsx'
import Dashboard from './pages/Dashboard.jsx'
import Projects from './pages/Projects.jsx'
import Settings from './pages/Settings.jsx'
import Members from './pages/Members.jsx'
import InviteAccept from './pages/InviteAccept.jsx'
import Repos from './pages/Repos.jsx'
import Board from './pages/Board.jsx'
import CycleTime from './pages/CycleTime.jsx'
import Analytics from './pages/Analytics.jsx'
import Contribution from './pages/Contribution.jsx'
import Involvement from './pages/Involvement.jsx'
import Capacity from './pages/Capacity.jsx'
import Billing from './pages/Billing.jsx'
import EngHealth from './pages/EngHealth.jsx'
import Planning from './pages/Planning.jsx'
import Invoices from './pages/Invoices.jsx'
import InvoiceShare from './pages/InvoiceShare.jsx'
import Import from './pages/Import.jsx'
import NotFound from './pages/NotFound.jsx'

// Root: the marketing landing for logged-out visitors, the app for authed users.
function Root() {
  const { isAuthed } = useAuth()
  return isAuthed ? <Navigate to="/dashboard" replace /> : <Landing />
}

export default function App() {
  return (
    <AuthProvider>
      <BrowserRouter>
        <Routes>
          {/* Public landing (Landing brings its own MarketingLayout) */}
          <Route path="/" element={<Root />} />
          <Route path="/welcome" element={<Navigate to="/" replace />} />

          {/* Public marketing pages share the marketing chrome via <Outlet/> */}
          <Route element={<MarketingLayout />}>
            <Route path="/pricing" element={<Pricing />} />
            <Route path="/compare" element={<Compare />} />
            <Route path="/docs" element={<Docs />} />
            <Route path="/docs/:slug" element={<Docs />} />
          </Route>

          {/* Public auth routes */}
          <Route path="/login" element={<Login />} />
          <Route path="/signup" element={<Signup />} />

          {/* Invite accept — checks auth inside and redirects if needed */}
          <Route path="/invite/accept" element={<InviteAccept />} />

          {/* Public, unauthenticated client-invoice share view (token in URL) */}
          <Route path="/i/:token" element={<InvoiceShare />} />

          {/* Protected app shell — redirects to /login if not authed */}
          <Route element={<AppShell />}>
            <Route path="/dashboard" element={<Dashboard />} />
            <Route path="/board" element={<Board />} />
            <Route path="/projects" element={<Projects />} />
            <Route path="/repos" element={<Repos />} />
            <Route path="/import" element={<Import />} />
            <Route path="/analytics" element={<Analytics />} />
            <Route path="/contribution" element={<Contribution />} />
            <Route path="/cycle-time" element={<CycleTime />} />
            <Route path="/eng-health" element={<EngHealth />} />
            <Route path="/involvement" element={<Involvement />} />
            <Route path="/capacity" element={<Capacity />} />
            <Route path="/planning" element={<Planning />} />
            <Route path="/invoices" element={<Invoices />} />
            <Route path="/settings" element={<Settings />} />
            <Route path="/settings/members" element={<Members />} />
            <Route path="/settings/billing" element={<Billing />} />
            {/* Legacy /home — superseded by /dashboard (Home.jsx kept but unrouted). */}
            <Route path="/home" element={<Navigate to="/dashboard" replace />} />
          </Route>

          {/* 404 — branded not-found */}
          <Route path="*" element={<NotFound />} />
        </Routes>
      </BrowserRouter>
    </AuthProvider>
  )
}
