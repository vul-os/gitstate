/**
 * Hero — landing page hero section.
 * Big confident headline, sharp subhead, dual CTAs, atmospheric browser-frame
 * product visual with floating annotation chips, and a trust ticker row.
 */
import { Link } from 'react-router-dom'
import { ArrowRight, Zap, GitFork, GitBranch, RefreshCw } from 'lucide-react'
import { motion, useReducedMotion } from 'motion/react'
import { Reveal } from '../../components/Reveal.jsx'
import {
  Button,
  Pill,
  GradientText,
  Section,
  Container,
  Glow,
  BrowserFrame,
} from '../../components/ui/index.js'

/* ── Animated git-graph motif — draws a commit DAG that self-traces ─────────── */
function HeroGitGraph() {
  const reduce = useReducedMotion()

  // Branch lanes (x) and commit rows (y) in viewBox units.
  const LANES = [40, 96, 152]
  const main = [40, 100, 160, 220, 280, 340, 400, 460]   // y positions on lane 0
  const feat = [160, 220, 280, 340]                        // lane 1 branch life
  const hotfix = [280, 340, 400]                           // lane 2

  const draw = (delay) =>
    reduce
      ? { pathLength: 1, opacity: 0.5 }
      : {
          pathLength: [0, 1],
          opacity: [0, 0.55],
          transition: { duration: 2.2, delay, ease: [0.4, 0, 0.2, 1] },
        }

  const node = (delay) =>
    reduce
      ? { scale: 1, opacity: 0.9 }
      : {
          scale: [0, 1],
          opacity: [0, 0.95],
          transition: { duration: 0.4, delay, ease: 'backOut' },
        }

  return (
    <svg
      viewBox="0 0 192 500"
      fill="none"
      aria-hidden="true"
      className="w-full h-full"
      preserveAspectRatio="xMidYMid slice"
    >
      <defs>
        <linearGradient id="hg-main" x1="0" y1="0" x2="0" y2="500" gradientUnits="userSpaceOnUse">
          <stop stopColor="#2DD4BF" stopOpacity="0.1" />
          <stop offset="0.5" stopColor="#2DD4BF" />
          <stop offset="1" stopColor="#6366F1" stopOpacity="0.1" />
        </linearGradient>
      </defs>

      {/* main trunk */}
      <motion.path
        d={`M ${LANES[0]} ${main[0]} L ${LANES[0]} ${main[main.length - 1]}`}
        stroke="url(#hg-main)" strokeWidth="2" strokeLinecap="round"
        initial={{ pathLength: 0, opacity: 0 }} animate={draw(0.2)}
      />
      {/* feature branch out + back */}
      <motion.path
        d={`M ${LANES[0]} ${feat[0]} C ${LANES[1]} ${feat[0]}, ${LANES[1]} ${feat[0] + 30}, ${LANES[1]} ${feat[1]} L ${LANES[1]} ${feat[2]} C ${LANES[1]} ${feat[3] - 30}, ${LANES[1]} ${feat[3]}, ${LANES[0]} ${feat[3]}`}
        stroke="#818cf8" strokeWidth="2" strokeLinecap="round" fill="none"
        initial={{ pathLength: 0, opacity: 0 }} animate={draw(0.7)}
      />
      {/* hotfix branch */}
      <motion.path
        d={`M ${LANES[0]} ${hotfix[0]} C ${LANES[2]} ${hotfix[0]}, ${LANES[2]} ${hotfix[0] + 30}, ${LANES[2]} ${hotfix[1]} C ${LANES[2]} ${hotfix[2] - 30}, ${LANES[2]} ${hotfix[2]}, ${LANES[0]} ${hotfix[2]}`}
        stroke="#2DD4BF" strokeWidth="2" strokeLinecap="round" fill="none"
        initial={{ pathLength: 0, opacity: 0 }} animate={draw(1.1)}
      />

      {/* commit nodes on main */}
      {main.map((y, i) => (
        <motion.circle
          key={`m${i}`} cx={LANES[0]} cy={y} r="5"
          fill={i % 2 ? '#6366F1' : '#2DD4BF'}
          initial={{ scale: 0, opacity: 0 }} animate={node(0.4 + i * 0.18)}
          style={{ transformOrigin: `${LANES[0]}px ${y}px` }}
        />
      ))}
      {/* feature nodes */}
      {[feat[1], feat[2]].map((y, i) => (
        <motion.circle
          key={`f${i}`} cx={LANES[1]} cy={y} r="4.5" fill="#818cf8"
          initial={{ scale: 0, opacity: 0 }} animate={node(1.0 + i * 0.25)}
          style={{ transformOrigin: `${LANES[1]}px ${y}px` }}
        />
      ))}
      {/* hotfix node */}
      <motion.circle
        cx={LANES[2]} cy={hotfix[1]} r="4.5" fill="#2DD4BF"
        initial={{ scale: 0, opacity: 0 }} animate={node(1.4)}
        style={{ transformOrigin: `${LANES[2]}px ${hotfix[1]}px` }}
      />
    </svg>
  )
}

/* ── Floating annotation chip ─────────────────────────────────────────────── */
function Chip({ icon: Icon, label, color = 'teal', className = '', delay = 0 }) {
  const colorMap = {
    teal:   { bg: 'rgba(45,212,191,0.10)', border: 'rgba(45,212,191,0.22)', text: '#2DD4BF' },
    indigo: { bg: 'rgba(99,102,241,0.10)', border: 'rgba(99,102,241,0.22)', text: '#818cf8' },
    green:  { bg: 'rgba(34,197,94,0.10)',  border: 'rgba(34,197,94,0.22)',  text: '#4ade80' },
  }
  const c = colorMap[color] ?? colorMap.teal
  return (
    <Reveal delay={delay} className={['absolute', className].join(' ')}>
      <div
        className="inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-lg text-[11px] font-mono font-medium whitespace-nowrap backdrop-blur-md select-none"
        style={{
          background: c.bg,
          border: `1px solid ${c.border}`,
          color: c.text,
          boxShadow: `0 2px 8px rgba(0,0,0,0.35), 0 0 0 1px rgba(255,255,255,0.04)`,
        }}
      >
        {Icon && <Icon size={11} strokeWidth={2} aria-hidden="true" />}
        {label}
      </div>
    </Reveal>
  )
}

/* ── Gentle infinite float wrapper (respects reduced-motion) ──────────────── */
function FloatingFrame({ children }) {
  const reduce = useReducedMotion()
  return (
    <motion.div
      className="relative z-10"
      animate={reduce ? {} : { y: [0, -10, 0] }}
      transition={{ duration: 7, ease: 'easeInOut', repeat: Infinity }}
    >
      {children}
    </motion.div>
  )
}

/* ── Trust ticker item ────────────────────────────────────────────────────── */
function TickerItem({ icon: Icon, label }) {
  return (
    <span className="inline-flex items-center gap-2 text-[13px] text-[var(--text-muted)]">
      <Icon size={13} strokeWidth={1.8} className="text-[var(--brand-teal)] shrink-0" aria-hidden="true" />
      {label}
    </span>
  )
}

const TRUST_ITEMS = [
  { icon: Zap,       label: 'Connects in 60s' },
  { icon: GitFork,   label: 'Works with GitHub & GitLab' },
  { icon: RefreshCw, label: 'No manual updates ever' },
  { icon: GitBranch, label: 'Self-host with 1 binary' },
]

/* ── Hero ─────────────────────────────────────────────────────────────────── */
export default function Hero() {
  return (
    <Section py="2xl" className="relative overflow-hidden">
      {/* Ambient backdrop glows */}
      <Glow variant="teal"   size={900} className="top-[-15%] left-[15%]" />
      <Glow variant="indigo" size={700} className="top-[35%]  right-[-8%]" />

      {/* Extra depth: subtle indigo top-centre pulse */}
      <div
        aria-hidden="true"
        className="pointer-events-none absolute inset-x-0 top-0 h-[420px]"
        style={{
          background:
            'radial-gradient(ellipse 72% 48% at 50% 0%, rgba(99,102,241,0.07) 0%, transparent 70%)',
        }}
      />

      {/* Live git-graph motif — anchored to the left margin, fades into the page */}
      <div
        aria-hidden="true"
        className="pointer-events-none absolute left-0 top-0 bottom-0 w-[180px] hidden lg:block"
        style={{
          maskImage: 'linear-gradient(to right, #000 0%, #000 35%, transparent 100%), linear-gradient(to bottom, transparent 0%, #000 18%, #000 70%, transparent 100%)',
          WebkitMaskImage: 'linear-gradient(to right, #000 0%, #000 35%, transparent 100%), linear-gradient(to bottom, transparent 0%, #000 18%, #000 70%, transparent 100%)',
          WebkitMaskComposite: 'source-in',
          maskComposite: 'intersect',
          opacity: 0.5,
        }}
      >
        <HeroGitGraph />
      </div>

      <Container size="xl" className="relative z-10">
        {/* ── COPY + VISUAL ROW ───────────────────────────────────────────── */}
        <div className="flex flex-col lg:flex-row items-center gap-12 lg:gap-16 xl:gap-20">

          {/* LEFT: copy */}
          <div className="flex-1 max-w-xl lg:max-w-lg xl:max-w-xl">

            {/* Live status eyebrow + category pills */}
            <Reveal delay={0}>
              <div className="flex flex-wrap items-center gap-2 mb-7">
                <span
                  className="inline-flex items-center gap-2 px-2.5 py-1 rounded-full text-[11px] font-mono font-medium text-[var(--text-dim)]"
                  style={{
                    background: 'var(--bg-surface)',
                    border: '1px solid var(--border)',
                    boxShadow: 'inset 0 0 0 1px rgba(45,212,191,0.06)',
                  }}
                >
                  <span className="relative flex h-1.5 w-1.5">
                    <span className="absolute inline-flex h-full w-full rounded-full bg-[var(--brand-teal)] opacity-70 motion-safe:animate-ping" />
                    <span className="relative inline-flex h-1.5 w-1.5 rounded-full bg-[var(--brand-teal)]" />
                  </span>
                  Syncing from git
                </span>
                <Pill color="indigo">
                  <svg width="9" height="9" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
                    <path d="M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12"/>
                  </svg>
                  GitHub + GitLab
                </Pill>
                <Pill color="teal">open core · AGPL-3.0</Pill>
              </div>
            </Reveal>

            {/* Headline */}
            <Reveal delay={0.07}>
              <h1 className="font-display text-5xl md:text-6xl lg:text-[4.5rem] font-semibold leading-[1.04] tracking-[-0.03em] text-[var(--text)] mb-5">
                The project tracker{' '}
                <GradientText
                  as="span"
                  className="font-display text-5xl md:text-6xl lg:text-[4.5rem] font-semibold leading-[1.04] tracking-[-0.03em]"
                >
                  nobody updates by hand.
                </GradientText>
              </h1>
            </Reveal>

            {/* Sub-copy */}
            <Reveal delay={0.15}>
              <p className="text-lg md:text-xl text-[var(--text-muted)] leading-relaxed mb-8 max-w-md">
                gitstate reads your commits and PRs and{' '}
                <span className="text-[var(--text)] font-medium">automatically derives</span>{' '}
                ticket status, effort estimates, and evidence-backed progress — no stand-ups, no Jira
                grooming, no Friday timesheets.
              </p>
            </Reveal>

            {/* CTAs */}
            <Reveal delay={0.22}>
              <div className="flex flex-col sm:flex-row items-start gap-3 mb-6">
                <Link to="/signup">
                  <Button variant="primary" size="lg">
                    Start free
                    <ArrowRight size={14} aria-hidden="true" />
                  </Button>
                </Link>
                <Link to="#derived-demo">
                  <Button variant="outline" size="lg">
                    See how it works
                  </Button>
                </Link>
              </div>
            </Reveal>

            {/* Micro-copy */}
            <Reveal delay={0.27}>
              <p className="text-xs font-mono text-[var(--text-faint)] leading-relaxed">
                Free forever for solo devs · No credit card · 1 binary to self-host
              </p>
            </Reveal>
          </div>

          {/* RIGHT: browser frame with depth + floating chips */}
          <div className="flex-1 w-full max-w-sm sm:max-w-md lg:max-w-none">
            <Reveal delay={0.1} className="relative w-full">

              {/* Outer ambient glow halo behind the frame */}
              <div
                aria-hidden="true"
                className="absolute pointer-events-none"
                style={{
                  inset: '-48px -32px',
                  background:
                    'radial-gradient(ellipse 70% 55% at 50% 50%, rgba(45,212,191,0.13) 0%, rgba(99,102,241,0.10) 40%, transparent 72%)',
                  filter: 'blur(1px)',
                  zIndex: 0,
                }}
              />

              {/* The frame itself — slight forward perspective tilt + gentle float */}
              <FloatingFrame>
                <div
                  style={{
                    transform: 'perspective(1200px) rotateY(-3deg) rotateX(2deg)',
                    transformOrigin: 'center center',
                    filter: 'drop-shadow(0 32px 64px rgba(0,0,0,0.45)) drop-shadow(0 0 40px rgba(45,212,191,0.08))',
                  }}
                >
                  <BrowserFrame
                    src="/shots/dashboard.png"
                    alt="gitstate dashboard — ticket status derived automatically from git commits"
                    url="app.gitstate.dev/dashboard"
                  />
                </div>
              </FloatingFrame>

              {/* Floating annotation chips */}
              <Chip
                icon={RefreshCw}
                label="0 tickets updated manually"
                color="teal"
                delay={0.38}
                className="-top-4 -left-4 sm:-left-8 z-20"
              />
              <Chip
                icon={GitBranch}
                label="derived from git"
                color="indigo"
                delay={0.44}
                className="top-1/3 -right-4 sm:-right-10 z-20"
              />
              <Chip
                icon={Zap}
                label="Real-time sync"
                color="green"
                delay={0.50}
                className="-bottom-5 left-1/4 z-20"
              />
            </Reveal>
          </div>
        </div>

        {/* ── TRUST TICKER ────────────────────────────────────────────────── */}
        <Reveal delay={0.32} className="mt-16 pt-8 border-t border-[var(--border)]">
          <div className="flex flex-wrap items-center justify-center gap-x-8 gap-y-3">
            {TRUST_ITEMS.map((item, i) => (
              <TickerItem key={i} icon={item.icon} label={item.label} />
            ))}
          </div>
        </Reveal>
      </Container>
    </Section>
  )
}
