/**
 * Sidebar — local-first nav rail.
 *
 * No org switcher, no account footer — gitstate now runs on your machine against
 * your git + forge. The rail links the local screens the daemon serves.
 */
import { NavLink } from 'react-router-dom'
import { LogoMark } from './Logo.jsx'
import {
  LayoutDashboard,
  FolderGit2,
  BarChart3,
  KanbanSquare,
  Scale,
  HeartPulse,
  Users,
  Contact,
  DownloadCloud,
  Layers,
  Tags,
  Sparkles,
  ShieldCheck,
  Settings as SettingsIcon,
} from 'lucide-react'

const NAV = [
  { label: 'Dashboard', to: '/dashboard', end: true, icon: LayoutDashboard },
  { label: 'Board', to: '/board', icon: KanbanSquare },
  { label: 'Repos', to: '/repos', icon: FolderGit2 },
  { group: 'Insights' },
  { label: 'Insights', to: '/insights', icon: BarChart3 },
  { label: 'Contribution', to: '/contribution', icon: Scale },
  { label: 'Eng Health', to: '/eng-health', icon: HeartPulse },
  { label: 'Involvement', to: '/involvement', icon: Users },
  { label: 'People', to: '/people', icon: Contact },
  { group: 'Working sets' },
  { label: 'Contexts', to: '/contexts', icon: Layers },
  { label: 'Categories', to: '/categories', icon: Tags },
  { label: 'Classify', to: '/classify', icon: Sparkles },
  { group: 'end' },
  { label: 'Import', to: '/import', icon: DownloadCloud },
  { label: 'Taxonomy', to: '/taxonomy', icon: ShieldCheck },
  { label: 'Settings', to: '/settings', end: true, icon: SettingsIcon },
]

/**
 * @param {object} props
 * @param {() => void} [props.onNavigate]
 * @param {string} [props.navLabel]  distinguishes the desktop rail from the
 *   mobile drawer — both are in the DOM at all times, and two navigation
 *   landmarks sharing one name is ambiguous to a screen reader.
 */
export function Sidebar({ onNavigate, navLabel = 'Primary' }) {
  return (
    <aside className="flex flex-col w-full lg:w-[216px] shrink-0 h-screen lg:sticky lg:top-0 border-r border-[var(--border)] bg-[var(--bg-surface)]">
      {/* Logo */}
      <div className="flex items-center gap-2.5 px-4 h-14 border-b border-[var(--border)] shrink-0">
        <LogoMark size={28} />
        <span className="font-mono font-bold text-[15px] tracking-tight text-[var(--text)]">
          git<span className="text-[#2DD4BF]">state</span>
        </span>
      </div>

      {/* Nav */}
      <nav aria-label={navLabel} className="flex-1 py-3 px-2.5 overflow-y-auto">
        <div className="space-y-px">
          {NAV.map((item, idx) => {
            if (item.group === 'end') {
              return <div key={`sep-${idx}`} className="h-px bg-[var(--border)] my-2.5" />
            }
            if (item.group) {
              return (
                <div key={`group-${idx}`} className="pt-3 pb-1.5 px-2.5">
                  <span className="text-[9px] font-mono font-bold text-[var(--text-faint)] uppercase tracking-[0.12em]">
                    {item.group}
                  </span>
                </div>
              )
            }
            const { label, to, end, icon: Icon } = item
            return (
              <NavLink
                key={to}
                to={to}
                end={end}
                onClick={onNavigate}
                className={({ isActive }) =>
                  [
                    'flex items-center gap-2.5 px-2.5 py-[7px] rounded-lg text-[13px] font-medium transition-all duration-150',
                    'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--brand-teal)]',
                    isActive
                      ? 'bg-[#2DD4BF]/10 text-[#2DD4BF]'
                      : 'text-[var(--text-faint)] hover:bg-[var(--bg-surface2)] hover:text-[var(--text)]',
                  ].join(' ')
                }
              >
                <span className="shrink-0" aria-hidden="true">
                  <Icon size={16} strokeWidth={1.8} />
                </span>
                {label}
              </NavLink>
            )
          })}
        </div>
      </nav>

      {/* Footer — local-first badge */}
      <div className="border-t border-[var(--border)] p-3 shrink-0">
        <div className="flex items-center gap-2 rounded-lg bg-[var(--bg-surface2)] px-2.5 py-2">
          <span className="h-1.5 w-1.5 rounded-full bg-[#2DD4BF]" />
          <span className="text-[11px] font-mono text-[var(--text-faint)]">
            local · on your machine
          </span>
        </div>
      </div>
    </aside>
  )
}
