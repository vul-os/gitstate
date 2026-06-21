/**
 * Inline gitstate logo — mark + wordmark variant or mark-only.
 * Avoids a network request; safe to use in the sidebar and auth pages.
 *
 * The wordmark's "git" is theme-aware: it inherits `currentColor`, which we
 * drive from the active theme (dark text on light bg, light text on dark bg).
 * "state" stays brand teal. The navy mark badge is an intentional branded
 * element kept in both themes.
 */
import { useTheme } from '../lib/theme.jsx'

export function LogoMark({ size = 36, className = '' }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 72 72"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      role="img"
      aria-label="gitstate mark"
      className={className}
    >
      <defs>
        <linearGradient id="gsm-inline" x1="8" y1="8" x2="64" y2="64" gradientUnits="userSpaceOnUse">
          <stop stopColor="#2DD4BF" />
          <stop offset="1" stopColor="#6366F1" />
        </linearGradient>
      </defs>
      <rect x="4" y="4" width="64" height="64" rx="16" fill="#0B1120" />
      <path d="M22 52 V20" stroke="url(#gsm-inline)" strokeWidth="4" strokeLinecap="round" />
      <path
        d="M22 34 C22 26 30 26 38 26 C46 26 50 30 50 38 C50 46 46 50 38 50 C30 50 22 50 22 42"
        stroke="url(#gsm-inline)"
        strokeWidth="4"
        fill="none"
        strokeLinecap="round"
      />
      <circle cx="22" cy="20" r="5" fill="#0B1120" stroke="url(#gsm-inline)" strokeWidth="4" />
      <circle cx="22" cy="52" r="5" fill="#0B1120" stroke="url(#gsm-inline)" strokeWidth="4" />
      <circle cx="50" cy="38" r="9" fill="url(#gsm-inline)" />
      <path
        d="M46 38 l3 3 l5 -6"
        stroke="#0B1120"
        strokeWidth="3"
        strokeLinecap="round"
        strokeLinejoin="round"
        fill="none"
      />
    </svg>
  )
}

export function LogoFull({ height = 40, className = '' }) {
  // Preserve aspect ratio: original is 280×72, so width = height * (280/72)
  const width = Math.round(height * (280 / 72))
  const { resolved } = useTheme()
  // "git" inherits currentColor — dark ink in light mode, near-white in dark.
  const gitColor = resolved === 'light' ? '#0f172a' : '#e2e8f0'
  return (
    <svg
      width={width}
      height={height}
      viewBox="0 0 280 72"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      role="img"
      aria-label="gitstate"
      style={{ color: gitColor }}
      className={className}
    >
      <defs>
        <linearGradient id="gs-inline" x1="8" y1="8" x2="64" y2="64" gradientUnits="userSpaceOnUse">
          <stop stopColor="#2DD4BF" />
          <stop offset="1" stopColor="#6366F1" />
        </linearGradient>
      </defs>
      <g>
        <rect x="4" y="4" width="64" height="64" rx="16" fill="#0B1120" />
        <path d="M22 52 V20" stroke="url(#gs-inline)" strokeWidth="4" strokeLinecap="round" />
        <path
          d="M22 34 C22 26 30 26 38 26 C46 26 50 30 50 38 C50 46 46 50 38 50 C30 50 22 50 22 42"
          stroke="url(#gs-inline)"
          strokeWidth="4"
          fill="none"
          strokeLinecap="round"
        />
        <circle cx="22" cy="20" r="5" fill="#0B1120" stroke="url(#gs-inline)" strokeWidth="4" />
        <circle cx="22" cy="52" r="5" fill="#0B1120" stroke="url(#gs-inline)" strokeWidth="4" />
        <circle cx="50" cy="38" r="9" fill="url(#gs-inline)" />
        <path
          d="M46 38 l3 3 l5 -6"
          stroke="#0B1120"
          strokeWidth="3"
          strokeLinecap="round"
          strokeLinejoin="round"
          fill="none"
        />
      </g>
      <text
        x="84"
        y="46"
        fontFamily="ui-monospace, 'SF Mono', Menlo, monospace"
        fontSize="30"
        fontWeight="700"
        fill="currentColor"
      >
        {'git'}
        <tspan fill="#2DD4BF">state</tspan>
      </text>
    </svg>
  )
}
