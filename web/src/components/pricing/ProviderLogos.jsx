/**
 * ProviderLogos — clean inline SVG brand marks for the three managed-AI
 * providers. lucide-react has no brand logos, so these are hand-built marks
 * sized off `size` and tinted via `currentColor` (wrap in a coloured span) or an
 * explicit per-mark brand colour.
 *
 * Usage:
 *   <AnthropicMark size={20} />
 *   <ProviderMark provider="openai" size={18} />
 */

/** Anthropic — the four-stroke "A" sunburst glyph. */
export function AnthropicMark({ size = 20, className = '' }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 46 32"
      fill="currentColor"
      aria-hidden="true"
      className={className}
    >
      <path d="M32.73 0H25.7l12.82 32h7.03L32.73 0Zm-19.46 0L0 32h7.17l2.62-6.8h13.42l2.62 6.8h7.17L19.78 0h-6.51Zm-1.38 19.3 4.39-11.4 4.39 11.4h-8.78Z" />
    </svg>
  )
}

/** OpenAI — the interlocking-knot blossom mark. */
export function OpenAIMark({ size = 20, className = '' }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="currentColor"
      aria-hidden="true"
      className={className}
    >
      <path d="M22.282 9.821a5.985 5.985 0 0 0-.516-4.91 6.046 6.046 0 0 0-6.51-2.9A6.065 6.065 0 0 0 4.981 4.18a5.985 5.985 0 0 0-3.998 2.9 6.046 6.046 0 0 0 .743 7.097 5.98 5.98 0 0 0 .51 4.911 6.051 6.051 0 0 0 6.515 2.9A5.985 5.985 0 0 0 13.26 24a6.056 6.056 0 0 0 5.772-4.206 5.99 5.99 0 0 0 3.997-2.9 6.056 6.056 0 0 0-.747-7.073zM13.26 22.43a4.476 4.476 0 0 1-2.876-1.04l.141-.081 4.779-2.758a.795.795 0 0 0 .392-.681v-6.737l2.02 1.168a.071.071 0 0 1 .038.052v5.583a4.504 4.504 0 0 1-4.494 4.494zM3.6 18.304a4.47 4.47 0 0 1-.535-3.014l.142.085 4.783 2.759a.771.771 0 0 0 .78 0l5.843-3.369v2.332a.08.08 0 0 1-.033.062L9.74 19.95a4.5 4.5 0 0 1-6.14-1.646zM2.34 7.896a4.485 4.485 0 0 1 2.366-1.973V11.6a.766.766 0 0 0 .388.676l5.815 3.355-2.02 1.168a.076.076 0 0 1-.071.006l-4.83-2.786A4.504 4.504 0 0 1 2.34 7.872zm16.597 3.855-5.833-3.387L15.119 7.2a.076.076 0 0 1 .071-.006l4.83 2.791a4.494 4.494 0 0 1-.676 8.105v-5.678a.79.79 0 0 0-.407-.667zm2.01-3.023-.141-.085-4.774-2.782a.776.776 0 0 0-.785 0L9.409 9.23V6.897a.066.066 0 0 1 .028-.061l4.83-2.787a4.5 4.5 0 0 1 6.68 4.66zm-12.64 4.135-2.02-1.164a.08.08 0 0 1-.038-.057V6.075a4.5 4.5 0 0 1 7.375-3.453l-.142.08L8.704 5.46a.795.795 0 0 0-.393.681zm1.097-2.365 2.602-1.5 2.607 1.5v3l-2.597 1.5-2.607-1.5z" />
    </svg>
  )
}

/** Google / Gemini — the four-colour spark glyph (own colours, ignores currentColor). */
export function GoogleMark({ size = 20, className = '' }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      aria-hidden="true"
      className={className}
    >
      <path
        d="M12 2C12 7.523 7.523 12 2 12c5.523 0 10 4.477 10 10 0-5.523 4.477-10 10-10-5.523 0-10-4.477-10-10Z"
        fill="url(#gemini-grad)"
      />
      <defs>
        <linearGradient id="gemini-grad" x1="2" y1="2" x2="22" y2="22" gradientUnits="userSpaceOnUse">
          <stop stopColor="#4285F4" />
          <stop offset="0.35" stopColor="#9B72CB" />
          <stop offset="0.7" stopColor="#D96570" />
          <stop offset="1" stopColor="#F9AB00" />
        </linearGradient>
      </defs>
    </svg>
  )
}

/** Per-provider visual metadata: mark, label, accent colour, accent tint background. */
// eslint-disable-next-line react-refresh/only-export-components
export const PROVIDER_META = {
  anthropic: {
    label: 'Anthropic',
    Mark: AnthropicMark,
    accent: '#D97757',
    tint: 'rgba(217,119,87,0.10)',
    border: 'rgba(217,119,87,0.28)',
    brandColor: true, // mark uses currentColor → tint it with accent
  },
  openai: {
    label: 'OpenAI',
    Mark: OpenAIMark,
    accent: '#10A37F',
    tint: 'rgba(16,163,127,0.10)',
    border: 'rgba(16,163,127,0.28)',
    brandColor: true,
  },
  google: {
    label: 'Google',
    Mark: GoogleMark,
    accent: '#4285F4',
    tint: 'rgba(66,133,244,0.10)',
    border: 'rgba(66,133,244,0.28)',
    brandColor: false, // mark is self-coloured
  },
}

/** Convenience: render a provider's mark by key, coloured with its accent. */
export function ProviderMark({ provider, size = 20, className = '' }) {
  const meta = PROVIDER_META[provider]
  if (!meta) return null
  const { Mark, accent, brandColor } = meta
  return (
    <span style={brandColor ? { color: accent } : undefined} className="inline-flex">
      <Mark size={size} className={className} />
    </span>
  )
}
