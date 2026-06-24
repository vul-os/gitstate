/**
 * MarketingFooter — handsome multi-column site footer.
 * Product / Resources / Company columns, logo + tagline,
 * GitHub link, license line, copyright.
 */
import { Link } from 'react-router-dom'
import { ExternalLink } from 'lucide-react'

// GitHub SVG mark (not in lucide-react) — inline brand logo
function GitHubMark({ size = 13 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <path d="M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12"/>
    </svg>
  )
}
import { LogoFull } from '../Logo.jsx'

const FOOTER_LINKS = {
  Product: [
    { label: 'Overview',      to: '/' },
    { label: 'Pricing',       to: '/pricing' },
    { label: 'Model pricing', to: '/models' },
    { label: 'Compare',       to: '/compare' },
    { label: 'Changelog',     to: '/changelog' },
  ],
  Resources: [
    { label: 'Docs',             to: '/docs' },
    { label: 'Self-host guide',  to: '/docs/self-host' },
    { label: 'API reference',    to: '/docs/api' },
    { label: 'GitHub',           href: 'https://github.com/gitstate/gitstate', external: true },
  ],
  Company: [
    { label: 'About',    to: '/about' },
    { label: 'Blog',     to: '/blog' },
    { label: 'Privacy',  to: '/privacy' },
    { label: 'Terms',    to: '/terms' },
  ],
}

function FooterLink({ label, to, href, external = false }) {
  const cls = [
    'text-sm text-[var(--text-faint)] hover:text-[var(--text-muted)]',
    'transition-colors duration-150 flex items-center gap-1.5 group',
  ].join(' ')

  if (href) {
    return (
      <a href={href} target="_blank" rel="noopener noreferrer" className={cls}>
        {label}
        {external && (
          <ExternalLink
            size={11}
            className="opacity-0 group-hover:opacity-60 transition-opacity duration-150"
            aria-hidden="true"
          />
        )}
      </a>
    )
  }
  return <Link to={to} className={cls}>{label}</Link>
}

export function MarketingFooter() {
  return (
    <footer className="w-full border-t border-[var(--border)] bg-[var(--bg)]">
      <div className="mx-auto max-w-7xl px-6 md:px-8">

        {/* Main grid */}
        <div className="py-14 grid grid-cols-1 sm:grid-cols-2 md:grid-cols-4 gap-10 md:gap-8">

          {/* Brand column */}
          <div className="sm:col-span-2 md:col-span-1 flex flex-col gap-5">
            <LogoFull height={28} />
            <p className="text-sm text-[var(--text-faint)] leading-relaxed max-w-[200px]">
              The project tracker nobody updates by hand. Git is the ledger.
            </p>

            {/* GitHub pill */}
            <a
              href="https://github.com/gitstate/gitstate"
              target="_blank"
              rel="noopener noreferrer"
              className={[
                'inline-flex items-center gap-2 w-fit',
                'px-3.5 py-2 rounded-lg',
                'border border-[var(--border)] bg-[var(--bg-surface)]',
                'text-xs font-mono text-[var(--text-faint)]',
                'hover:text-[var(--text-muted)] hover:border-[var(--border2)]',
                'hover:bg-[var(--bg-surface2)]',
                'transition-all duration-150',
              ].join(' ')}
            >
              <GitHubMark size={13} />
              gitstate/gitstate
            </a>

            {/* Currency/theme note */}
            <p className="text-[11px] font-mono text-[var(--text-faint)] leading-relaxed">
              Prices in USD, ZAR, GBP, EUR.
              <br />
              Theme: dark · light · system.
            </p>
          </div>

          {/* Nav columns */}
          {Object.entries(FOOTER_LINKS).map(([group, links]) => (
            <div key={group} className="flex flex-col gap-4">
              <span className="text-[11px] font-mono uppercase tracking-[0.12em] text-[var(--text-muted)] font-semibold">
                {group}
              </span>
              <ul className="flex flex-col gap-3">
                {links.map(link => (
                  <li key={link.label}>
                    <FooterLink {...link} />
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>

        {/* Bottom bar */}
        <div className="border-t border-[var(--border)] py-6 flex flex-col md:flex-row items-center justify-between gap-4">
          {/* License + copyright */}
          <div className="flex flex-col sm:flex-row sm:items-center gap-1.5 sm:gap-3 text-center sm:text-left">
            <p className="text-xs font-mono text-[var(--text-faint)]">
              © {new Date().getFullYear()} gitstate.
            </p>
            <span className="hidden sm:block w-px h-3.5 bg-[var(--border)]" aria-hidden="true" />
            <p className="text-xs font-mono text-[var(--text-faint)]">
              <span className="text-[var(--brand-teal)]/80">AGPL-3.0</span>
              {' '}open core · commercial EE license available.
            </p>
          </div>

          {/* Live indicator */}
          <div className="flex items-center gap-2">
            <span
              className="inline-block w-1.5 h-1.5 rounded-full bg-[var(--brand-teal)] animate-pulse"
              aria-hidden="true"
            />
            <span className="text-xs font-mono text-[var(--text-faint)]">
              git is the ledger
            </span>
          </div>
        </div>
      </div>
    </footer>
  )
}
