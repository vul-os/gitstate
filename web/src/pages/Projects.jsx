/**
 * Projects page stub — Wave C4 will fill in the real board/list/table views.
 */

function ProjectRow({ name, repo, status, commits, lastActivity }) {
  const statusColors = {
    active: { dot: '#2DD4BF', label: 'Active' },
    stale: { dot: '#f59e0b', label: 'Stale' },
    done: { dot: '#6366F1', label: 'Done' },
  }
  const s = statusColors[status] ?? statusColors.active

  return (
    <div className="flex items-center gap-4 px-5 py-4 border-b border-[#1e2d45] last:border-0 hover:bg-[#0d1628]/50 transition-colors duration-100 cursor-pointer">
      <div className="w-2 h-2 rounded-full shrink-0" style={{ background: s.dot }} />
      <div className="flex-1 min-w-0">
        <p className="text-sm font-medium text-[#e2e8f0] truncate">{name}</p>
        <p className="text-xs text-[#64748b] font-mono truncate mt-0.5">{repo}</p>
      </div>
      <div className="hidden sm:flex items-center gap-1 text-xs font-mono text-[#64748b]">
        <svg width="12" height="12" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
          <path strokeLinecap="round" strokeLinejoin="round" d="M9.568 3H5.25A2.25 2.25 0 0 0 3 5.25v4.318c0 .597.237 1.17.659 1.591l9.581 9.581c.699.699 1.78.872 2.607.33a18.095 18.095 0 0 0 5.223-5.223c.542-.827.369-1.908-.33-2.607L11.16 3.66A2.25 2.25 0 0 0 9.568 3Z" />
        </svg>
        {commits} commits
      </div>
      <div className="text-xs text-[#64748b] hidden md:block">{lastActivity}</div>
      <div
        className="text-xs font-mono px-2 py-0.5 rounded-full"
        style={{ color: s.dot, background: `${s.dot}18` }}
      >
        {s.label}
      </div>
    </div>
  )
}

const DEMO_PROJECTS = [
  { name: 'gitstate', repo: 'github.com/exo/gitstate', status: 'active', commits: 147, lastActivity: '2 hours ago' },
  { name: 'api-gateway', repo: 'github.com/exo/api-gateway', status: 'stale', commits: 32, lastActivity: '8 days ago' },
  { name: 'dashboard-v2', repo: 'github.com/exo/dashboard-v2', status: 'done', commits: 89, lastActivity: '3 weeks ago' },
]

export default function Projects() {
  return (
    <div className="max-w-4xl">
      {/* Header */}
      <div className="flex items-center justify-between mb-8">
        <div>
          <h1 className="text-2xl font-bold text-[#e2e8f0] tracking-tight">Projects</h1>
          <p className="text-sm text-[#64748b] mt-1">Git-derived status · no ticket maintenance.</p>
        </div>
        <button
          className="px-4 py-2 rounded-lg text-sm font-semibold text-[#0B1120] transition-all duration-150"
          style={{ background: 'linear-gradient(135deg, #2DD4BF, #6366F1)' }}
        >
          + Connect repo
        </button>
      </div>

      {/* Demo table — rendered so the shell has something to show */}
      <div className="bg-[#111827] border border-[#1e2d45] rounded-xl overflow-hidden">
        <div className="flex items-center gap-4 px-5 py-3 border-b border-[#1e2d45]">
          <span className="text-xs font-medium text-[#64748b] uppercase tracking-widest flex-1">Project</span>
          <span className="text-xs font-medium text-[#64748b] uppercase tracking-widest hidden sm:block w-28">Activity</span>
          <span className="text-xs font-medium text-[#64748b] uppercase tracking-widest hidden md:block w-28">Last commit</span>
          <span className="text-xs font-medium text-[#64748b] uppercase tracking-widest w-16">Status</span>
        </div>
        {DEMO_PROJECTS.map((p) => (
          <ProjectRow key={p.name} {...p} />
        ))}
      </div>

      <p className="text-xs text-[#334155] font-mono mt-4 text-center">
        Demo data — connect a real repo in Wave C
      </p>
    </div>
  )
}
