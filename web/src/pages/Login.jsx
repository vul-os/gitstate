import { useState, useEffect } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { login, fetchConfig, ApiError } from '../lib/api.js'
import { useAuth } from '../lib/useAuth.js'
import { LogoFull } from '../components/Logo.jsx'

function OAuthButton({ href, children, icon }) {
  return (
    <a
      href={href}
      className="flex items-center justify-center gap-2.5 w-full px-4 py-2.5 rounded-lg border border-[#1e2d45] bg-[#111827] text-sm font-medium text-[#e2e8f0] hover:bg-[#162032] hover:border-[#2a3f5f] transition-all duration-150"
    >
      {icon}
      {children}
    </a>
  )
}

const PROVIDER_META = {
  google: {
    label: 'Continue with Google',
    icon: (
      <svg width="18" height="18" viewBox="0 0 48 48" fill="none">
        <path fill="#EA4335" d="M24 9.5c3.54 0 6.71 1.22 9.21 3.6l6.85-6.85C35.9 2.38 30.47 0 24 0 14.62 0 6.51 5.38 2.56 13.22l7.98 6.19C12.43 13.72 17.74 9.5 24 9.5z" />
        <path fill="#4285F4" d="M46.98 24.55c0-1.57-.15-3.09-.38-4.55H24v9.02h12.94c-.58 2.96-2.26 5.48-4.78 7.18l7.73 6c4.51-4.18 7.09-10.36 7.09-17.65z" />
        <path fill="#FBBC05" d="M10.53 28.59c-.48-1.45-.76-2.99-.76-4.59s.27-3.14.76-4.59l-7.98-6.19C.92 16.46 0 20.12 0 24c0 3.88.92 7.54 2.56 10.78l7.97-6.19z" />
        <path fill="#34A853" d="M24 48c6.48 0 11.93-2.13 15.89-5.81l-7.73-6c-2.18 1.48-4.97 2.36-8.16 2.36-6.26 0-11.57-4.22-13.47-9.91l-7.98 6.19C6.51 42.62 14.62 48 24 48z" />
      </svg>
    ),
  },
  microsoft: {
    label: 'Continue with Microsoft',
    icon: (
      <svg width="18" height="18" viewBox="0 0 21 21" fill="none">
        <rect width="10" height="10" fill="#F25022" />
        <rect x="11" width="10" height="10" fill="#7FBA00" />
        <rect y="11" width="10" height="10" fill="#00A4EF" />
        <rect x="11" y="11" width="10" height="10" fill="#FFB900" />
      </svg>
    ),
  },
}

const BASE = import.meta.env.VITE_API_BASE_URL ?? ''

export default function Login() {
  const navigate = useNavigate()
  const { setToken, isAuthed } = useAuth()

  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)

  const [config, setConfig] = useState(null)
  const [configLoading, setConfigLoading] = useState(true)

  // Already logged in
  useEffect(() => {
    if (isAuthed) navigate('/', { replace: true })
  }, [isAuthed, navigate])

  // Fetch /api/config to discover enabled OAuth providers
  useEffect(() => {
    let cancelled = false
    fetchConfig()
      .then(data => { if (!cancelled) setConfig(data) })
      .catch(() => { if (!cancelled) setConfig(null) })
      .finally(() => { if (!cancelled) setConfigLoading(false) })
    return () => { cancelled = true }
  }, [])

  async function handleSubmit(e) {
    e.preventDefault()
    setError(null)
    setLoading(true)
    try {
      const data = await login(email, password)
      setToken(data?.token)
      navigate('/')
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.message)
      } else {
        setError('Unable to reach the server. Try again.')
      }
    } finally {
      setLoading(false)
    }
  }

  const providers = config?.auth?.providers ?? {}
  const enabledProviders = Object.entries(providers).filter(([, enabled]) => enabled)
  const showOAuth = !configLoading && enabledProviders.length > 0

  return (
    <div className="min-h-screen bg-[#0B1120] flex flex-col items-center justify-center px-4 py-12">
      {/* Background gradient glow */}
      <div
        aria-hidden
        className="pointer-events-none fixed inset-0 overflow-hidden"
      >
        <div className="absolute -top-32 left-1/2 -translate-x-1/2 w-[600px] h-[400px] rounded-full opacity-[0.06]"
          style={{ background: 'radial-gradient(ellipse at center, #2DD4BF, #6366F1)' }} />
      </div>

      {/* Card */}
      <div className="relative w-full max-w-[400px]">
        {/* Logo */}
        <div className="flex justify-center mb-8">
          <LogoFull height={38} />
        </div>

        <div className="bg-[#111827] border border-[#1e2d45] rounded-2xl p-8 shadow-2xl">
          <h1 className="text-xl font-semibold text-[#e2e8f0] mb-1 text-center tracking-tight">
            Welcome back
          </h1>
          <p className="text-sm text-[#64748b] text-center mb-7">
            Sign in to your workspace
          </p>

          {/* OAuth providers — only rendered if /api/config says they're enabled */}
          {showOAuth && (
            <>
              <div className="space-y-2.5 mb-6">
                {enabledProviders.map(([name]) => {
                  const meta = PROVIDER_META[name]
                  if (!meta) return null
                  return (
                    <OAuthButton
                      key={name}
                      provider={name}
                      href={`${BASE}/auth/oauth/${name}`}
                      icon={meta.icon}
                    >
                      {meta.label}
                    </OAuthButton>
                  )
                })}
              </div>
              <div className="flex items-center gap-3 mb-6">
                <div className="flex-1 h-px bg-[#1e2d45]" />
                <span className="text-xs text-[#64748b] font-mono">or</span>
                <div className="flex-1 h-px bg-[#1e2d45]" />
              </div>
            </>
          )}

          {/* Password form */}
          <form onSubmit={handleSubmit} className="space-y-4">
            <div>
              <label className="block text-xs font-medium text-[#94a3b8] mb-1.5" htmlFor="email">
                Email
              </label>
              <input
                id="email"
                type="email"
                autoComplete="email"
                required
                value={email}
                onChange={e => setEmail(e.target.value)}
                placeholder="you@example.com"
                className="w-full px-3.5 py-2.5 rounded-lg bg-[#0d1628] border border-[#1e2d45] text-sm text-[#e2e8f0] placeholder-[#334155] outline-none focus:border-[#2DD4BF] focus:ring-1 focus:ring-[#2DD4BF]/30 transition-all duration-150"
              />
            </div>

            <div>
              <div className="flex items-center justify-between mb-1.5">
                <label className="block text-xs font-medium text-[#94a3b8]" htmlFor="password">
                  Password
                </label>
                <button
                  type="button"
                  className="text-xs text-[#2DD4BF] hover:text-[#5eead4] transition-colors duration-150"
                  onClick={() => {/* forgot password — Wave B */}}
                >
                  Forgot password?
                </button>
              </div>
              <input
                id="password"
                type="password"
                autoComplete="current-password"
                required
                value={password}
                onChange={e => setPassword(e.target.value)}
                placeholder="••••••••"
                className="w-full px-3.5 py-2.5 rounded-lg bg-[#0d1628] border border-[#1e2d45] text-sm text-[#e2e8f0] placeholder-[#334155] outline-none focus:border-[#2DD4BF] focus:ring-1 focus:ring-[#2DD4BF]/30 transition-all duration-150"
              />
            </div>

            {error && (
              <div className="flex items-start gap-2 px-3.5 py-2.5 rounded-lg bg-red-500/10 border border-red-500/20 text-xs text-red-400">
                <svg width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2" className="mt-0.5 shrink-0">
                  <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126ZM12 15.75h.007v.008H12v-.008Z" />
                </svg>
                {error}
              </div>
            )}

            <button
              type="submit"
              disabled={loading}
              className="w-full py-2.5 px-4 rounded-lg text-sm font-semibold text-[#0B1120] transition-all duration-150 disabled:opacity-50 disabled:cursor-not-allowed"
              style={{
                background: loading
                  ? '#2DD4BF'
                  : 'linear-gradient(135deg, #2DD4BF, #6366F1)',
              }}
            >
              {loading ? 'Signing in…' : 'Sign in'}
            </button>
          </form>
        </div>

        {/* Sign up link */}
        <p className="text-center text-sm text-[#64748b] mt-6">
          Don&apos;t have an account?{' '}
          <Link
            to="/signup"
            className="text-[#2DD4BF] hover:text-[#5eead4] font-medium transition-colors duration-150"
          >
            Create one
          </Link>
        </p>

        {/* Open source note */}
        <p className="text-center text-xs text-[#334155] mt-3 font-mono">
          open-source · self-hostable · AGPL-3.0
        </p>
      </div>
    </div>
  )
}
