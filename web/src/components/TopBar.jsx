/**
 * Top bar — shows current section title + a subtle git-derived status pill.
 */
import { useLocation } from 'react-router-dom'

const TITLES = {
  '/': 'Home',
  '/projects': 'Projects',
  '/settings': 'Settings',
}

function Breadcrumb() {
  const { pathname } = useLocation()
  const title = TITLES[pathname] ?? pathname.replace('/', '')
  return (
    <span className="text-sm font-semibold text-[#e2e8f0] tracking-tight">
      {title}
    </span>
  )
}

export function TopBar() {
  return (
    <header className="h-14 border-b border-[#1e2d45] bg-[#0d1628]/80 backdrop-blur-sm flex items-center px-6 gap-4 sticky top-0 z-20">
      <Breadcrumb />
      <div className="flex-1" />
      {/* Status pill */}
      <div className="flex items-center gap-1.5 px-2.5 py-1 rounded-full bg-[#162032] border border-[#1e2d45] text-[11px] font-mono text-[#2DD4BF]">
        <span className="w-1.5 h-1.5 rounded-full bg-[#2DD4BF] animate-pulse" />
        synced
      </div>
    </header>
  )
}
