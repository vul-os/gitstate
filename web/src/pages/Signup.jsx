import { useState, useEffect } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { signup, ApiError } from '../lib/api.js'
import { useAuth } from '../lib/useAuth.js'
import { LogoFull } from '../components/Logo.jsx'

export default function Signup() {
  const navigate = useNavigate()
  const { setToken, isAuthed } = useAuth()

  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)

  useEffect(() => {
    if (isAuthed) navigate('/', { replace: true })
  }, [isAuthed, navigate])

  async function handleSubmit(e) {
    e.preventDefault()
    setError(null)
    setLoading(true)
    try {
      const data = await signup(email, password, name)
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

  return (
    <div className="min-h-screen bg-[#0B1120] flex flex-col items-center justify-center px-4 py-12">
      {/* Background glow */}
      <div aria-hidden className="pointer-events-none fixed inset-0 overflow-hidden">
        <div
          className="absolute -top-32 left-1/2 -translate-x-1/2 w-[600px] h-[400px] rounded-full opacity-[0.06]"
          style={{ background: 'radial-gradient(ellipse at center, #6366F1, #2DD4BF)' }}
        />
      </div>

      <div className="relative w-full max-w-[400px]">
        <div className="flex justify-center mb-8">
          <LogoFull height={38} />
        </div>

        <div className="bg-[#111827] border border-[#1e2d45] rounded-2xl p-8 shadow-2xl">
          <h1 className="text-xl font-semibold text-[#e2e8f0] mb-1 text-center tracking-tight">
            Create your account
          </h1>
          <p className="text-sm text-[#64748b] text-center mb-7">
            Start tracking your projects from git
          </p>

          <form onSubmit={handleSubmit} className="space-y-4">
            <div>
              <label className="block text-xs font-medium text-[#94a3b8] mb-1.5" htmlFor="name">
                Full name
              </label>
              <input
                id="name"
                type="text"
                autoComplete="name"
                required
                value={name}
                onChange={e => setName(e.target.value)}
                placeholder="Jane Smith"
                className="w-full px-3.5 py-2.5 rounded-lg bg-[#0d1628] border border-[#1e2d45] text-sm text-[#e2e8f0] placeholder-[#334155] outline-none focus:border-[#2DD4BF] focus:ring-1 focus:ring-[#2DD4BF]/30 transition-all duration-150"
              />
            </div>

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
              <label className="block text-xs font-medium text-[#94a3b8] mb-1.5" htmlFor="password">
                Password
              </label>
              <input
                id="password"
                type="password"
                autoComplete="new-password"
                required
                minLength={8}
                value={password}
                onChange={e => setPassword(e.target.value)}
                placeholder="At least 8 characters"
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
              {loading ? 'Creating account…' : 'Create account'}
            </button>
          </form>

          <p className="text-center text-xs text-[#334155] mt-5 leading-relaxed">
            By signing up you agree to the{' '}
            <a href="#" className="text-[#2DD4BF]/70 hover:text-[#2DD4BF]">Terms</a>
            {' '}and{' '}
            <a href="#" className="text-[#2DD4BF]/70 hover:text-[#2DD4BF]">Privacy Policy</a>.
          </p>
        </div>

        <p className="text-center text-sm text-[#64748b] mt-6">
          Already have an account?{' '}
          <Link
            to="/login"
            className="text-[#2DD4BF] hover:text-[#5eead4] font-medium transition-colors duration-150"
          >
            Sign in
          </Link>
        </p>

        <p className="text-center text-xs text-[#334155] mt-3 font-mono">
          open-source · self-hostable · AGPL-3.0
        </p>
      </div>
    </div>
  )
}
