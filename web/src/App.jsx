import { lazy, Suspense, Component } from 'react'
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { AuthProvider } from './lib/auth.jsx'
import { useAuth } from './lib/useAuth.js'
// Shared chrome stays eager so layouts never flash on navigation.
import { AppShell } from './components/AppShell.jsx'
import MarketingLayout from './components/marketing/MarketingLayout.jsx'

// Page-level routes are lazy-loaded so the initial bundle only ships shared
// chrome + the auth provider. Each page becomes its own on-demand chunk.
const Landing = lazy(() => import('./pages/Landing.jsx'))
const Pricing = lazy(() => import('./pages/Pricing.jsx'))
const ModelPricing = lazy(() => import('./pages/ModelPricing.jsx'))
const Compare = lazy(() => import('./pages/Compare.jsx'))
const Docs = lazy(() => import('./pages/Docs.jsx'))
const Login = lazy(() => import('./pages/Login.jsx'))
const Signup = lazy(() => import('./pages/Signup.jsx'))
const Dashboard = lazy(() => import('./pages/Dashboard.jsx'))
const Settings = lazy(() => import('./pages/Settings.jsx'))
const Members = lazy(() => import('./pages/Members.jsx'))
const People = lazy(() => import('./pages/People.jsx'))
const InviteAccept = lazy(() => import('./pages/InviteAccept.jsx'))
const Repos = lazy(() => import('./pages/Repos.jsx'))
const Board = lazy(() => import('./pages/Board.jsx'))
const CycleTime = lazy(() => import('./pages/CycleTime.jsx'))
const Analytics = lazy(() => import('./pages/Analytics.jsx'))
const Contribution = lazy(() => import('./pages/Contribution.jsx'))
const Involvement = lazy(() => import('./pages/Involvement.jsx'))
const Capacity = lazy(() => import('./pages/Capacity.jsx'))
const Billing = lazy(() => import('./pages/Billing.jsx'))
const EngHealth = lazy(() => import('./pages/EngHealth.jsx'))
const Planning = lazy(() => import('./pages/Planning.jsx'))
const Invoices = lazy(() => import('./pages/Invoices.jsx'))
const InvoiceShare = lazy(() => import('./pages/InvoiceShare.jsx'))
const Import = lazy(() => import('./pages/Import.jsx'))
const NotFound = lazy(() => import('./pages/NotFound.jsx'))

/**
 * Theme-aware loading fallback for lazy route chunks — a centered, gently
 * pulsing brand spinner on the app background. Tokens keep it correct in both
 * dark and light themes. Fills the viewport so there's no flash-of-nothing.
 */
function RouteFallback() {
  return (
    <div
      role="status"
      aria-label="Loading"
      className="flex min-h-screen w-full items-center justify-center bg-[var(--bg)]"
    >
      <div
        className="h-9 w-9 rounded-full border-2 border-[var(--border2)] border-t-[var(--brand-teal)] animate-spin"
        style={{ animationDuration: '0.7s' }}
      />
      <span className="sr-only">Loading…</span>
    </div>
  )
}

/**
 * Error boundary so a lazy chunk that fails to load (e.g. transient network /
 * stale hashed asset after a deploy) degrades to a small retry card instead of
 * a white screen.
 */
class ChunkErrorBoundary extends Component {
  constructor(props) {
    super(props)
    this.state = { failed: false }
  }
  static getDerivedStateFromError() {
    return { failed: true }
  }
  render() {
    if (this.state.failed) {
      return (
        <div className="flex min-h-screen w-full flex-col items-center justify-center gap-4 bg-[var(--bg)] px-6 text-center">
          <p className="text-[var(--text)]">Something went wrong loading this page.</p>
          <button
            type="button"
            onClick={() => window.location.reload()}
            className="rounded-[var(--radius-btn)] border border-[var(--border2)] bg-[var(--bg-surface)] px-4 py-2 text-sm text-[var(--text)] hover:bg-[var(--bg-surface2)]"
          >
            Reload
          </button>
        </div>
      )
    }
    return this.props.children
  }
}

// Root: the marketing landing for logged-out visitors, the app for authed users.
function Root() {
  const { isAuthed } = useAuth()
  return isAuthed ? <Navigate to="/dashboard" replace /> : <Landing />
}

export default function App() {
  return (
    <AuthProvider>
      <BrowserRouter>
        <ChunkErrorBoundary>
          <Suspense fallback={<RouteFallback />}>
            <Routes>
              {/* Public landing (Landing brings its own MarketingLayout) */}
              <Route path="/" element={<Root />} />
              <Route path="/welcome" element={<Navigate to="/" replace />} />

              {/* Public AI model pricing — brings its own MarketingLayout */}
              <Route path="/models" element={<ModelPricing />} />

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
                {/* Projects + Repos are unified: a "project" is a repo's owner-org.
                    /projects redirects to the unified /repos view so old links work. */}
                <Route path="/projects" element={<Navigate to="/repos" replace />} />
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
                <Route path="/people" element={<People />} />
                <Route path="/settings" element={<Settings />} />
                <Route path="/settings/members" element={<Members />} />
                <Route path="/settings/billing" element={<Billing />} />
                {/* Legacy /home — superseded by /dashboard (Home.jsx kept but unrouted). */}
                <Route path="/home" element={<Navigate to="/dashboard" replace />} />
              </Route>

              {/* 404 — branded not-found */}
              <Route path="*" element={<NotFound />} />
            </Routes>
          </Suspense>
        </ChunkErrorBoundary>
      </BrowserRouter>
    </AuthProvider>
  )
}
