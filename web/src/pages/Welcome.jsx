/**
 * Welcome page — logged-out landing at /welcome (and / when unauthenticated).
 * Linear/Vercel-grade marketing hero for gitstate.
 * Brand: teal #2DD4BF → indigo #6366F1 on near-black #0B1120.
 */
import { Link } from 'react-router-dom'
import { LogoFull } from '../components/Logo.jsx'

// ── Micro-components ───────────────────────────────────────────────────────────

function GradientText({ children }) {
  return (
    <span
      style={{
        background: 'linear-gradient(135deg, #2DD4BF 0%, #6366F1 100%)',
        WebkitBackgroundClip: 'text',
        WebkitTextFillColor: 'transparent',
        backgroundClip: 'text',
      }}
    >
      {children}
    </span>
  )
}

function NavBar() {
  return (
    <nav
      className="fixed top-0 left-0 right-0 z-50 flex items-center justify-between px-6 md:px-10 py-4"
      style={{
        background: 'rgba(11,17,32,0.88)',
        backdropFilter: 'blur(14px)',
        WebkitBackdropFilter: 'blur(14px)',
        borderBottom: '1px solid rgba(30,45,69,0.7)',
      }}
    >
      <LogoFull height={32} />
      <div className="flex items-center gap-3">
        <Link
          to="/login"
          className="px-4 py-2 text-sm font-medium text-[#94a3b8] hover:text-[#e2e8f0] transition-colors"
        >
          Sign in
        </Link>
        <Link
          to="/signup"
          className="px-4 py-2 rounded-lg text-sm font-semibold text-[#0B1120] transition-all duration-150 hover:opacity-90"
          style={{ background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' }}
        >
          Get started free
        </Link>
      </div>
    </nav>
  )
}

// ── Feature highlights ─────────────────────────────────────────────────────────

const FEATURES = [
  {
    icon: (
      <svg width="22" height="22" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="1.6">
        <path strokeLinecap="round" strokeLinejoin="round" d="M17.25 6.75 22.5 12l-5.25 5.25m-10.5 0L1.5 12l5.25-5.25m7.5-3-4.5 16.5" />
      </svg>
    ),
    color: '#2DD4BF',
    title: 'Git is the ledger',
    body: 'Merged PR = done. Open PR = in progress. No tickets to maintain, no status fields to update. State is derived, not declared.',
  },
  {
    icon: (
      <svg width="22" height="22" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="1.6">
        <path strokeLinecap="round" strokeLinejoin="round" d="M13.5 16.875h3.375m0 0h3.375m-3.375 0V13.5m0 3.375v3.375M6 10.5h2.25a2.25 2.25 0 0 0 2.25-2.25V6a2.25 2.25 0 0 0-2.25-2.25H6A2.25 2.25 0 0 0 3.75 6v2.25A2.25 2.25 0 0 0 6 10.5Zm0 9.75h2.25A2.25 2.25 0 0 0 10.5 18v-2.25a2.25 2.25 0 0 0-2.25-2.25H6a2.25 2.25 0 0 0-2.25 2.25V18A2.25 2.25 0 0 0 6 20.25Zm9.75-9.75H18a2.25 2.25 0 0 0 2.25-2.25V6A2.25 2.25 0 0 0 18 3.75h-2.25A2.25 2.25 0 0 0 13.5 6v2.25a2.25 2.25 0 0 0 2.25 2.25Z" />
      </svg>
    ),
    color: '#6366F1',
    title: 'GitHub + GitLab, unified',
    body: 'Connect your repos from either platform. Issues sync two-way. Your board derives state from real git activity, not your sprint ceremony.',
  },
  {
    icon: (
      <svg width="22" height="22" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="1.6">
        <path strokeLinecap="round" strokeLinejoin="round" d="M9.813 15.904 9 18.75l-.813-2.846a4.5 4.5 0 0 0-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 0 0 3.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 0 0 3.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 0 0-3.09 3.09Z" />
      </svg>
    ),
    color: '#f59e0b',
    title: 'LLM diff-difficulty sizing',
    body: 'Effort comes from an LLM reading the actual diff — not story-point poker. Calibrated from your observed cycle time, not vibes.',
  },
  {
    icon: (
      <svg width="22" height="22" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="1.6">
        <path strokeLinecap="round" strokeLinejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 0 0-3.375-3.375h-1.5A1.125 1.125 0 0 1 13.5 7.125v-1.5a3.375 3.375 0 0 0-3.375-3.375H8.25m0 12.75h7.5m-7.5 3H12M10.5 2.25H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 0 0-9-9Z" />
      </svg>
    ),
    color: '#22c55e',
    title: 'Evidence billing',
    body: 'Every invoice line links to a commit SHA or pull request. Work git can\'t see — meetings, research — is flagged for you to fill in, never silently invented.',
  },
  {
    icon: (
      <svg width="22" height="22" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="1.6">
        <path strokeLinecap="round" strokeLinejoin="round" d="M15 19.128a9.38 9.38 0 0 0 2.625.372 9.337 9.337 0 0 0 4.121-.952 4.125 4.125 0 0 0-7.533-2.493M15 19.128v-.003c0-1.113-.285-2.16-.786-3.07M15 19.128v.106A12.318 12.318 0 0 1 8.624 21c-2.331 0-4.512-.645-6.374-1.766l-.001-.109a6.375 6.375 0 0 1 11.964-3.07M12 6.375a3.375 3.375 0 1 1-6.75 0 3.375 3.375 0 0 1 6.75 0Zm8.25 2.25a2.625 2.625 0 1 1-5.25 0 2.625 2.625 0 0 1 5.25 0Z" />
      </svg>
    ),
    color: '#2DD4BF',
    title: 'Free stakeholder seats',
    body: 'Pricing is per builder — devs and PMs. Clients, stakeholders, and read-only viewers are always free. The seat-tax killer incumbents can\'t match.',
  },
  {
    icon: (
      <svg width="22" height="22" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="1.6">
        <path strokeLinecap="round" strokeLinejoin="round" d="M9 12.75 11.25 15 15 9.75m-3-7.036A11.959 11.959 0 0 1 3.598 6 11.99 11.99 0 0 0 3 9.749c0 5.592 3.824 10.29 9 11.623 5.176-1.332 9-6.03 9-11.622 0-1.31-.21-2.571-.598-3.751h-.152c-3.196 0-6.1-1.248-8.25-3.285Z" />
      </svg>
    ),
    color: '#6366F1',
    title: 'Open core, self-hostable',
    body: 'AGPL core runs on your infra with a single binary + Postgres. No vendor lock-in. EE billing features available on the cloud edition.',
  },
]

function FeatureCard({ feature }) {
  return (
    <div
      className="rounded-2xl p-6 flex flex-col gap-4 group transition-all duration-300"
      style={{
        background: 'rgba(17,24,39,0.8)',
        border: '1px solid rgba(30,45,69,0.8)',
      }}
    >
      <div
        className="w-11 h-11 rounded-xl flex items-center justify-center shrink-0"
        style={{ background: `${feature.color}12`, color: feature.color }}
      >
        {feature.icon}
      </div>
      <div>
        <h3 className="text-sm font-semibold text-[#e2e8f0] mb-1.5">{feature.title}</h3>
        <p className="text-sm text-[#64748b] leading-relaxed">{feature.body}</p>
      </div>
    </div>
  )
}

// ── Social proof / stat strip ─────────────────────────────────────────────────

function StatStrip() {
  const stats = [
    { value: '0', label: 'tickets to maintain' },
    { value: '100%', label: 'git-derived state' },
    { value: 'free', label: 'stakeholder seats' },
    { value: '1', label: 'binary to self-host' },
  ]
  return (
    <div className="grid grid-cols-2 md:grid-cols-4 gap-px" style={{ background: 'rgba(30,45,69,0.5)' }}>
      {stats.map((s, i) => (
        <div
          key={i}
          className="flex flex-col items-center justify-center py-8 px-4 gap-1"
          style={{ background: '#0B1120' }}
        >
          <span
            className="text-3xl font-extrabold font-mono tracking-tight"
            style={{ color: i % 2 === 0 ? '#2DD4BF' : '#6366F1' }}
          >
            {s.value}
          </span>
          <span className="text-xs text-[#475569] text-center">{s.label}</span>
        </div>
      ))}
    </div>
  )
}

// ── Who it's for strip ─────────────────────────────────────────────────────────

function IcpStrip() {
  return (
    <div
      className="rounded-2xl px-8 py-6 flex flex-col md:flex-row items-center gap-6 md:gap-12"
      style={{
        background: 'linear-gradient(135deg, rgba(45,212,191,0.04), rgba(99,102,241,0.04))',
        border: '1px solid rgba(45,212,191,0.12)',
      }}
    >
      <div className="shrink-0">
        <div
          className="w-14 h-14 rounded-2xl flex items-center justify-center"
          style={{ background: 'rgba(45,212,191,0.1)', border: '1px solid rgba(45,212,191,0.2)' }}
        >
          <svg width="26" height="26" fill="none" viewBox="0 0 24 24" stroke="#2DD4BF" strokeWidth="1.6">
            <path strokeLinecap="round" strokeLinejoin="round" d="M2.25 12.75V12A2.25 2.25 0 0 1 4.5 9.75h15A2.25 2.25 0 0 1 21.75 12v.75m-8.69-6.44-2.12-2.12a1.5 1.5 0 0 0-1.061-.44H4.5A2.25 2.25 0 0 0 2.25 6v12a2.25 2.25 0 0 0 2.25 2.25h15A2.25 2.25 0 0 0 21.75 18V9a2.25 2.25 0 0 0-2.25-2.25h-5.379a1.5 1.5 0 0 1-1.06-.44Z" />
          </svg>
        </div>
      </div>
      <div>
        <p className="text-sm font-semibold text-[#e2e8f0] mb-1">Built for client-billing dev shops first</p>
        <p className="text-sm text-[#64748b] leading-relaxed max-w-2xl">
          Agencies and consultancies have an acute pain: defensible invoices. gitstate generates evidence-backed invoices from git activity — your clients see the commit SHAs and PRs behind every line item. Expanding to multi-repo teams running in an agent-native world.
        </p>
      </div>
    </div>
  )
}

// ── CTA section ────────────────────────────────────────────────────────────────

function CtaSection() {
  return (
    <div className="text-center py-8">
      <div
        className="inline-block rounded-full px-4 py-1.5 mb-8 text-xs font-semibold"
        style={{
          background: 'rgba(45,212,191,0.08)',
          border: '1px solid rgba(45,212,191,0.2)',
          color: '#2DD4BF',
        }}
      >
        Open core · AGPL-3.0 · Free to self-host
      </div>
      <h2 className="text-3xl md:text-4xl font-extrabold text-[#e2e8f0] mb-4 tracking-tight leading-tight">
        Stop maintaining the fiction.<br />
        <GradientText>Let git tell the truth.</GradientText>
      </h2>
      <p className="text-base text-[#64748b] max-w-lg mx-auto mb-10 leading-relaxed">
        Get started in minutes — connect a repo, and gitstate derives your board, metrics, and invoices automatically.
      </p>
      <div className="flex flex-col sm:flex-row gap-4 justify-center">
        <Link
          to="/signup"
          className="px-8 py-3.5 rounded-xl text-base font-bold text-[#0B1120] transition-all duration-150 hover:opacity-90 hover:scale-[1.02] active:scale-[0.98]"
          style={{ background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' }}
        >
          Get started free
        </Link>
        <Link
          to="/login"
          className="px-8 py-3.5 rounded-xl text-base font-semibold text-[#94a3b8] transition-all duration-150 hover:text-[#e2e8f0] hover:border-[#334155]"
          style={{ border: '1px solid #1e2d45' }}
        >
          Sign in
        </Link>
      </div>
    </div>
  )
}

// ── Grid background decoration ─────────────────────────────────────────────────

function GridBg() {
  return (
    <div
      className="pointer-events-none absolute inset-0"
      style={{
        backgroundImage: `
          linear-gradient(rgba(45,212,191,0.03) 1px, transparent 1px),
          linear-gradient(90deg, rgba(45,212,191,0.03) 1px, transparent 1px)
        `,
        backgroundSize: '64px 64px',
      }}
    />
  )
}

// ── Hero glow blobs ────────────────────────────────────────────────────────────

function HeroGlow() {
  return (
    <>
      <div
        className="pointer-events-none absolute top-0 left-1/2 -translate-x-1/2 w-[700px] h-[400px] rounded-full"
        style={{
          background: 'radial-gradient(ellipse at center, rgba(45,212,191,0.08) 0%, transparent 70%)',
          filter: 'blur(40px)',
        }}
      />
      <div
        className="pointer-events-none absolute top-32 left-1/4 w-[400px] h-[400px] rounded-full"
        style={{
          background: 'radial-gradient(ellipse at center, rgba(99,102,241,0.06) 0%, transparent 70%)',
          filter: 'blur(60px)',
        }}
      />
    </>
  )
}

// ── Root page ──────────────────────────────────────────────────────────────────

export default function Welcome() {
  return (
    <div className="min-h-screen" style={{ background: '#0B1120', color: '#e2e8f0' }}>
      <NavBar />

      {/* Hero section */}
      <section className="relative overflow-hidden pt-32 pb-20 px-6 md:px-10 text-center">
        <GridBg />
        <HeroGlow />

        <div className="relative z-10 max-w-4xl mx-auto">
          {/* Pill */}
          <div className="inline-flex items-center gap-2 rounded-full px-4 py-1.5 mb-8 text-xs font-semibold"
            style={{
              background: 'rgba(99,102,241,0.08)',
              border: '1px solid rgba(99,102,241,0.2)',
              color: '#a5b4fc',
            }}
          >
            <svg width="10" height="10" viewBox="0 0 24 24" fill="#6366F1">
              <path d="M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12" />
            </svg>
            GitHub + GitLab · open core · AGPL-3.0
          </div>

          {/* Headline */}
          <h1 className="text-4xl md:text-6xl font-extrabold tracking-tight leading-[1.1] mb-6">
            The project tracker<br />
            <GradientText>nobody updates by hand.</GradientText>
          </h1>

          {/* Subhead */}
          <p className="text-lg md:text-xl text-[#64748b] max-w-2xl mx-auto mb-10 leading-relaxed">
            gitstate reads your repos and <strong className="text-[#94a3b8]">derives</strong> true project state, effort, and evidence-backed invoices from git itself — built for a world where agents write the code and humans supervise.
          </p>

          {/* CTAs */}
          <div className="flex flex-col sm:flex-row gap-4 justify-center mb-12">
            <Link
              to="/signup"
              className="px-8 py-3.5 rounded-xl text-base font-bold text-[#0B1120] transition-all duration-150 hover:opacity-90 hover:scale-[1.02] active:scale-[0.98] shadow-lg"
              style={{
                background: 'linear-gradient(135deg, #2DD4BF, #6366F1)',
                boxShadow: '0 0 30px rgba(45,212,191,0.2)',
              }}
            >
              Get started free
            </Link>
            <Link
              to="/login"
              className="px-8 py-3.5 rounded-xl text-base font-semibold text-[#94a3b8] transition-all duration-150 hover:text-[#e2e8f0] hover:border-[#334155]"
              style={{ border: '1px solid #1e2d45' }}
            >
              Sign in
            </Link>
          </div>

          {/* Wedge truths — three honest disciplines */}
          <div className="grid grid-cols-1 md:grid-cols-3 gap-4 text-left max-w-3xl mx-auto">
            {[
              {
                n: '01',
                title: 'Derived, not entered',
                text: 'State comes from git. Merged = done. PR open = in progress. Nobody maintains tickets.',
              },
              {
                n: '02',
                title: 'Measure work, not workers',
                text: 'Involvement is texture across dimensions — features, reviews, ownership — never a single score or bonus formula.',
              },
              {
                n: '03',
                title: 'Evidence with visible gaps',
                text: 'Invoices are backed by git. The part git can\'t see is flagged for a human, never silently invented.',
              },
            ].map(d => (
              <div
                key={d.n}
                className="rounded-xl p-5"
                style={{ background: 'rgba(17,24,39,0.6)', border: '1px solid rgba(30,45,69,0.8)' }}
              >
                <span className="text-[10px] font-mono font-bold text-[#334155] block mb-2">{d.n}</span>
                <h3 className="text-sm font-semibold text-[#e2e8f0] mb-1.5">{d.title}</h3>
                <p className="text-xs text-[#64748b] leading-relaxed">{d.text}</p>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* Stat strip */}
      <StatStrip />

      {/* Features */}
      <section className="px-6 md:px-10 py-24 max-w-6xl mx-auto">
        <div className="text-center mb-14">
          <h2 className="text-2xl md:text-3xl font-extrabold text-[#e2e8f0] tracking-tight mb-3">
            Everything derived from git.{' '}
            <GradientText>Nothing entered by hand.</GradientText>
          </h2>
          <p className="text-sm text-[#64748b] max-w-xl mx-auto">
            Jira, Linear, ClickUp — manually maintained fictions sitting next to git.
            gitstate eliminates the fiction.
          </p>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {FEATURES.map((f, i) => <FeatureCard key={i} feature={f} />)}
        </div>
      </section>

      {/* ICP callout */}
      <section className="px-6 md:px-10 pb-16 max-w-4xl mx-auto">
        <IcpStrip />
      </section>

      {/* Final CTA */}
      <section
        className="relative overflow-hidden px-6 md:px-10 py-24"
        style={{ borderTop: '1px solid rgba(30,45,69,0.6)' }}
      >
        <div
          className="pointer-events-none absolute inset-0"
          style={{
            background: 'radial-gradient(ellipse at 50% 100%, rgba(99,102,241,0.06) 0%, transparent 60%)',
          }}
        />
        <div className="relative z-10 max-w-2xl mx-auto">
          <CtaSection />
        </div>
      </section>

      {/* Footer */}
      <footer
        className="px-6 md:px-10 py-8 flex flex-col md:flex-row items-center justify-between gap-4 text-xs text-[#334155]"
        style={{ borderTop: '1px solid rgba(30,45,69,0.5)' }}
      >
        <div className="flex items-center gap-2">
          <LogoFull height={24} />
        </div>
        <p>
          AGPL-3.0 core — EE billing + admin features available on cloud.
          gitstate reads your git; it does not invent your project state.
        </p>
        <div className="flex items-center gap-4">
          <Link to="/login" className="hover:text-[#64748b] transition-colors">Sign in</Link>
          <Link to="/signup" className="hover:text-[#64748b] transition-colors">Sign up</Link>
        </div>
      </footer>
    </div>
  )
}
