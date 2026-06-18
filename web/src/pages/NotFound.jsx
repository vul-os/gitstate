/**
 * 404 Not Found page — branded, links back to dashboard or landing.
 */
import { Link, useNavigate } from 'react-router-dom'
import { LogoMark } from '../components/Logo.jsx'
import { useAuth } from '../lib/useAuth.js'

export default function NotFound() {
  const { isAuthed } = useAuth()
  const navigate = useNavigate()

  return (
    <div
      className="min-h-screen flex flex-col items-center justify-center px-6 text-center"
      style={{ background: '#0B1120' }}
    >
      {/* Subtle glow */}
      <div
        className="pointer-events-none absolute inset-0"
        style={{
          background: 'radial-gradient(ellipse at 50% 40%, rgba(99,102,241,0.06) 0%, transparent 60%)',
        }}
      />

      <div className="relative z-10 flex flex-col items-center gap-8 max-w-md">
        {/* Logo mark */}
        <LogoMark size={56} />

        {/* 404 display */}
        <div>
          <p
            className="text-8xl font-extrabold font-mono tracking-tighter mb-0"
            style={{
              background: 'linear-gradient(135deg, #2DD4BF 0%, #6366F1 100%)',
              WebkitBackgroundClip: 'text',
              WebkitTextFillColor: 'transparent',
              backgroundClip: 'text',
              lineHeight: 1,
            }}
          >
            404
          </p>
        </div>

        <div>
          <h1 className="text-xl font-bold text-[#e2e8f0] mb-2">
            This state was not derived from git.
          </h1>
          <p className="text-sm text-[#64748b] leading-relaxed">
            The page you're looking for doesn't exist — or it was merged and the ticket is still open somewhere.
          </p>
        </div>

        {/* Git branch decoration */}
        <div
          className="flex items-center gap-2 px-4 py-2 rounded-lg font-mono text-xs"
          style={{
            background: 'rgba(45,212,191,0.06)',
            border: '1px solid rgba(45,212,191,0.15)',
            color: '#2DD4BF',
          }}
        >
          <svg width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
            <path strokeLinecap="round" strokeLinejoin="round" d="M17.25 6.75 22.5 12l-5.25 5.25m-10.5 0L1.5 12l5.25-5.25m7.5-3-4.5 16.5" />
          </svg>
          git: HEAD detached from reality
        </div>

        {/* Actions */}
        <div className="flex flex-col sm:flex-row gap-3 w-full">
          <button
            onClick={() => navigate(-1)}
            className="flex-1 px-5 py-2.5 rounded-xl text-sm font-semibold text-[#94a3b8] transition-all duration-150 hover:text-[#e2e8f0] hover:border-[#334155]"
            style={{ border: '1px solid #1e2d45' }}
          >
            Go back
          </button>
          {isAuthed ? (
            <Link
              to="/dashboard"
              className="flex-1 px-5 py-2.5 rounded-xl text-sm font-bold text-[#0B1120] text-center transition-all duration-150 hover:opacity-90"
              style={{ background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' }}
            >
              Dashboard
            </Link>
          ) : (
            <Link
              to="/welcome"
              className="flex-1 px-5 py-2.5 rounded-xl text-sm font-bold text-[#0B1120] text-center transition-all duration-150 hover:opacity-90"
              style={{ background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' }}
            >
              Home
            </Link>
          )}
        </div>
      </div>
    </div>
  )
}
