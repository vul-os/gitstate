/**
 * TableView — dense spreadsheet-style view of all issues.
 */
import { GitBadge, NativeBadge, StateChip } from './IssueBadge.jsx'

const DIFFICULTY_LABELS = ['', 'Trivial', 'Easy', 'Medium', 'Hard', 'Complex']
const DIFFICULTY_COLORS = ['', '#2DD4BF', '#22c55e', '#f59e0b', '#f97316', '#ef4444']

export function TableView({ issues, onCardClick }) {
  return (
    <div
      className="rounded-xl overflow-hidden"
      style={{ border: '1px solid #1e2d45' }}
    >
      <table className="w-full text-sm border-collapse">
        <thead>
          <tr style={{ background: '#0d1628' }}>
            <th className="text-left px-4 py-2.5 text-[10px] font-semibold text-[#64748b] uppercase tracking-widest w-[60px]">Source</th>
            <th className="text-left px-4 py-2.5 text-[10px] font-semibold text-[#64748b] uppercase tracking-widest w-[100px]">State</th>
            <th className="text-left px-4 py-2.5 text-[10px] font-semibold text-[#64748b] uppercase tracking-widest">Title</th>
            <th className="text-left px-4 py-2.5 text-[10px] font-semibold text-[#64748b] uppercase tracking-widest w-[80px] hidden md:table-cell">Platform</th>
            <th className="text-left px-4 py-2.5 text-[10px] font-semibold text-[#64748b] uppercase tracking-widest w-[80px] hidden lg:table-cell">Difficulty</th>
            <th className="text-left px-4 py-2.5 text-[10px] font-semibold text-[#64748b] uppercase tracking-widest w-[70px] hidden md:table-cell">PR</th>
            <th className="text-left px-4 py-2.5 text-[10px] font-semibold text-[#64748b] uppercase tracking-widest w-[80px]">Assignee</th>
          </tr>
        </thead>
        <tbody>
          {issues.length === 0 && (
            <tr>
              <td colSpan={7} className="text-center py-14 text-sm text-[#64748b]">
                No issues match the current filters.
              </td>
            </tr>
          )}
          {issues.map((issue, idx) => {
            const isGit = issue.source === 'git'
            const diff = issue.effortEstimate?.score
              ? Math.min(5, Math.max(1, Math.round(issue.effortEstimate.score)))
              : null
            return (
              <tr
                key={issue.id}
                onClick={() => onCardClick(issue)}
                className="cursor-pointer transition-colors duration-100 hover:bg-[#0d1628]/70 group"
                style={{
                  borderTop: idx === 0 ? 'none' : '1px solid #1e2d45',
                  background: idx % 2 === 0 ? 'transparent' : 'rgba(13,22,40,0.2)',
                }}
              >
                <td className="px-4 py-2.5">
                  {isGit ? <GitBadge /> : <NativeBadge />}
                </td>
                <td className="px-4 py-2.5">
                  <StateChip
                    state={issue.state}
                    derivedState={issue.manualStateOverride ?? issue.derivedState}
                  />
                </td>
                <td className="px-4 py-2.5 max-w-xs">
                  <span className="text-sm font-medium text-[#e2e8f0] truncate block group-hover:text-white transition-colors">
                    {issue.title}
                  </span>
                </td>
                <td className="px-4 py-2.5 hidden md:table-cell">
                  {issue.platform && (
                    <span className="text-xs font-mono text-[#64748b]">{issue.platform}</span>
                  )}
                </td>
                <td className="px-4 py-2.5 hidden lg:table-cell">
                  {diff != null && (
                    <span
                      className="text-[10px] font-semibold font-mono"
                      style={{ color: DIFFICULTY_COLORS[diff] }}
                    >
                      {DIFFICULTY_LABELS[diff]}
                    </span>
                  )}
                </td>
                <td className="px-4 py-2.5 hidden md:table-cell">
                  {issue.pullRequest && (
                    <span className="text-[10px] font-mono text-[#2DD4BF]">
                      #{issue.pullRequest.number}
                    </span>
                  )}
                </td>
                <td className="px-4 py-2.5">
                  {issue.assigneeId ? (
                    <div
                      className="w-6 h-6 rounded-full flex items-center justify-center text-[9px] font-bold"
                      style={{ background: 'linear-gradient(135deg, #2DD4BF, #6366F1)', color: '#0B1120' }}
                      title={issue.assigneeId}
                    >
                      {issue.assigneeId.slice(0, 2).toUpperCase()}
                    </div>
                  ) : (
                    <span className="text-[#334155] text-xs font-mono">—</span>
                  )}
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
