/**
 * InviteAccept — /invite/accept?token=...
 * Calls POST /api/invites/accept {token} → {orgId}, then switches to that org and redirects home.
 */
import { useReducer, useEffect, useCallback } from 'react'
import { useSearchParams, useNavigate } from 'react-router-dom'
import { LogoFull } from '../components/Logo.jsx'
import { useAuth } from '../lib/useAuth.js'
import { useOrg } from '../lib/useOrg.js'
import * as api from '../lib/api.js'

function Spinner() {
  return (
    <svg className="animate-spin" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5">
      <path strokeLinecap="round" d="M12 2v4M12 18v4M4.93 4.93l2.83 2.83M16.24 16.24l2.83 2.83M2 12h4M18 12h4M4.93 19.07l2.83-2.83M16.24 7.76l2.83-2.83" />
    </svg>
  )
}

function reducer(state, action) {
  switch (action.type) {
    case 'SUCCESS': return { status: 'success', errorMsg: null }
    case 'ERROR': return { status: 'error', errorMsg: action.msg }
    default: return state
  }
}

export default function InviteAccept() {
  const [searchParams] = useSearchParams()
  const navigate = useNavigate()
  const { isAuthed } = useAuth()
  const { switchOrg, refetchOrgs } = useOrg()
  const token = searchParams.get('token')

  // Derive initial state without needing a synchronous setState call in effects
  const [state, dispatch] = useReducer(reducer, {
    status: token ? 'loading' : 'error',
    errorMsg: token ? null : 'No invite token found in the URL.',
  })

  const acceptInvite = useCallback(async (t) => {
    try {
      const data = await api.post('/api/invites/accept', { token: t })
      await refetchOrgs()
      if (data?.orgId) {
        switchOrg(data.orgId)
      }
      dispatch({ type: 'SUCCESS' })
      setTimeout(() => navigate('/', { replace: true }), 1500)
    } catch (err) {
      dispatch({
        type: 'ERROR',
        msg: err?.message ?? 'Failed to accept the invite. It may have expired or already been used.',
      })
    }
  }, [navigate, switchOrg, refetchOrgs])

  useEffect(() => {
    if (!token) return
    if (!isAuthed) {
      navigate(`/login?next=${encodeURIComponent(window.location.href)}`, { replace: true })
      return
    }
    acceptInvite(token).catch(() => {})
  }, [token, isAuthed, navigate, acceptInvite])

  const { status, errorMsg } = state

  return (
    <div className="min-h-screen bg-[#0B1120] flex flex-col items-center justify-center px-4 py-12">
      {/* Background glow */}
      <div aria-hidden className="pointer-events-none fixed inset-0 overflow-hidden">
        <div
          className="absolute -top-32 left-1/2 -translate-x-1/2 w-[600px] h-[400px] rounded-full opacity-[0.06]"
          style={{ background: 'radial-gradient(ellipse at center, #2DD4BF, #6366F1)' }}
        />
      </div>

      <div className="relative w-full max-w-[400px]">
        <div className="flex justify-center mb-8">
          <LogoFull height={38} />
        </div>

        <div className="bg-[#111827] border border-[#1e2d45] rounded-2xl p-8 shadow-2xl text-center">
          {status === 'loading' && (
            <>
              <div className="flex justify-center mb-4 text-[#2DD4BF]">
                <Spinner />
              </div>
              <h1 className="text-lg font-semibold text-[#e2e8f0] mb-1">Accepting invite…</h1>
              <p className="text-sm text-[#64748b]">Joining your new organization.</p>
            </>
          )}

          {status === 'success' && (
            <>
              <div className="flex justify-center mb-4">
                <div className="w-12 h-12 rounded-full bg-[#2DD4BF]/10 border border-[#2DD4BF]/20 flex items-center justify-center">
                  <svg width="24" height="24" fill="none" viewBox="0 0 24 24" stroke="#2DD4BF" strokeWidth="2.5">
                    <path strokeLinecap="round" strokeLinejoin="round" d="m5 13 4 4L19 7" />
                  </svg>
                </div>
              </div>
              <h1 className="text-lg font-semibold text-[#e2e8f0] mb-1">You&apos;re in!</h1>
              <p className="text-sm text-[#64748b]">Redirecting to your workspace…</p>
            </>
          )}

          {status === 'error' && (
            <>
              <div className="flex justify-center mb-4">
                <div className="w-12 h-12 rounded-full bg-red-500/10 border border-red-500/20 flex items-center justify-center">
                  <svg width="24" height="24" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2" className="text-red-400">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126ZM12 15.75h.007v.008H12v-.008Z" />
                  </svg>
                </div>
              </div>
              <h1 className="text-lg font-semibold text-[#e2e8f0] mb-2">Invite error</h1>
              <p className="text-sm text-[#64748b] mb-5">{errorMsg}</p>
              <button
                onClick={() => navigate('/', { replace: true })}
                className="px-4 py-2 rounded-lg text-sm font-medium border border-[#1e2d45] text-[#94a3b8] hover:border-[#2DD4BF]/40 hover:text-[#2DD4BF] transition-all"
              >
                Go to workspace
              </button>
            </>
          )}
        </div>
      </div>
    </div>
  )
}
