import { lazy, Suspense, Component, type ReactNode } from 'react'
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { AuthProvider } from './lib/auth.tsx'
import { AppShell } from './components/AppShell.tsx'

// Local-first screens — each is its own on-demand chunk so the initial bundle
// stays small (just the shell + router).
const Dashboard = lazy(() => import('./pages/Dashboard.tsx'))
const Repos = lazy(() => import('./pages/Repos.tsx'))
const RepoDetail = lazy(() => import('./pages/RepoDetail.tsx'))
const Insights = lazy(() => import('./pages/Insights.tsx'))
const Contribution = lazy(() => import('./pages/Contribution.tsx'))
const EngHealth = lazy(() => import('./pages/EngHealth.tsx'))
const Involvement = lazy(() => import('./pages/Involvement.tsx'))
const People = lazy(() => import('./pages/People.tsx'))
const Board = lazy(() => import('./pages/Board.tsx'))
const Import = lazy(() => import('./pages/Import.tsx'))
const Contexts = lazy(() => import('./pages/Contexts.tsx'))
const Categories = lazy(() => import('./pages/Categories.tsx'))
const Classify = lazy(() => import('./pages/Classify.tsx'))
const Taxonomy = lazy(() => import('./pages/Taxonomy.tsx'))
const Settings = lazy(() => import('./pages/Settings.tsx'))
const NotFound = lazy(() => import('./pages/NotFound.tsx'))

/** Theme-aware fallback while a lazy route chunk loads. */
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

interface ChunkErrorBoundaryProps {
  children?: ReactNode
}

interface ChunkErrorBoundaryState {
  failed: boolean
}

/** Degrade a failed lazy chunk to a retry card instead of a white screen. */
class ChunkErrorBoundary extends Component<ChunkErrorBoundaryProps, ChunkErrorBoundaryState> {
  constructor(props: ChunkErrorBoundaryProps) {
    super(props)
    this.state = { failed: false }
  }
  static getDerivedStateFromError(): ChunkErrorBoundaryState {
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

export default function App() {
  return (
    <AuthProvider>
      <BrowserRouter>
        <ChunkErrorBoundary>
          <Suspense fallback={<RouteFallback />}>
            <Routes>
              <Route element={<AppShell />}>
                <Route path="/" element={<Navigate to="/dashboard" replace />} />
                <Route path="/dashboard" element={<Dashboard />} />
                <Route path="/repos" element={<Repos />} />
                <Route path="/repos/:id" element={<RepoDetail />} />
                <Route path="/insights" element={<Insights />} />
                <Route path="/contribution" element={<Contribution />} />
                <Route path="/eng-health" element={<EngHealth />} />
                <Route path="/involvement" element={<Involvement />} />
                <Route path="/people" element={<People />} />
                <Route path="/board" element={<Board />} />
                <Route path="/import" element={<Import />} />
                <Route path="/contexts" element={<Contexts />} />
                <Route path="/categories" element={<Categories />} />
                <Route path="/classify" element={<Classify />} />
                <Route path="/taxonomy" element={<Taxonomy />} />
                <Route path="/settings" element={<Settings />} />
                {/* Legacy SaaS links → their local-first equivalents */}
                <Route path="/projects" element={<Navigate to="/repos" replace />} />
                <Route path="/home" element={<Navigate to="/dashboard" replace />} />
                <Route path="/analytics" element={<Navigate to="/insights" replace />} />
              </Route>
              <Route path="*" element={<NotFound />} />
            </Routes>
          </Suspense>
        </ChunkErrorBoundary>
      </BrowserRouter>
    </AuthProvider>
  )
}
