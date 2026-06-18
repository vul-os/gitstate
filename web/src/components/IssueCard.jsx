/**
 * IssueCard — used on the Kanban board.
 * Visually distinguishes git-derived vs native issues.
 */
import { GitBadge, NativeBadge, LabelPills } from './IssueBadge.jsx'

export function IssueCard({ issue, onClick }) {
  const isGit = issue.source === 'git'

  return (
    <div
      onClick={() => onClick(issue)}
      className="rounded-xl p-3.5 cursor-pointer group transition-all duration-150 hover:translate-y-[-1px]"
      style={{
        background: '#0d1628',
        border: `1px solid ${isGit ? 'rgba(45,212,191,0.15)' : '#1e2d45'}`,
        boxShadow: '0 1px 6px rgba(0,0,0,0.25)',
      }}
    >
      {/* Source badge row */}
      <div className="flex items-center gap-1.5 mb-2">
        {isGit ? <GitBadge /> : <NativeBadge />}
        {issue.platform && (
          <span className="text-[10px] font-mono text-[#334155]">{issue.platform}</span>
        )}
        {issue.manualStateOverride && (
          <span className="text-[10px] font-mono text-[#f59e0b] ml-auto">overridden</span>
        )}
      </div>

      {/* Title */}
      <p className="text-sm font-medium text-[#e2e8f0] leading-snug line-clamp-2 mb-2 group-hover:text-white transition-colors">
        {issue.title}
      </p>

      {/* Labels */}
      {issue.labels?.length > 0 && (
        <div className="mb-2">
          <LabelPills labels={issue.labels.slice(0, 3)} />
        </div>
      )}

      {/* Footer */}
      <div className="flex items-center gap-2 mt-2">
        {issue.assigneeId && (
          <div
            className="w-5 h-5 rounded-full flex items-center justify-center text-[9px] font-bold shrink-0"
            style={{ background: 'linear-gradient(135deg, #2DD4BF, #6366F1)', color: '#0B1120' }}
            title={issue.assigneeId}
          >
            {issue.assigneeId.slice(0, 2).toUpperCase()}
          </div>
        )}
        {issue.effortEstimate?.score != null && (
          <span className="text-[10px] font-mono text-[#64748b]">
            D{issue.effortEstimate.score}
          </span>
        )}
        {issue.pullRequest && (
          <span className="text-[10px] font-mono text-[#2DD4BF] ml-auto">PR #{issue.pullRequest.number}</span>
        )}
      </div>
    </div>
  )
}
