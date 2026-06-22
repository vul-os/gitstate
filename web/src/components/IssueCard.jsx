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
      className="rounded-[var(--radius-card)] p-3.5 cursor-pointer group transition-all duration-150 hover:translate-y-[-1px] hover:border-[var(--border2)]"
      style={{
        background: 'var(--bg-surface)',
        border: `1px solid ${isGit ? 'rgba(45,212,191,0.25)' : 'var(--border)'}`,
        boxShadow: 'var(--shadow-card)',
      }}
    >
      {/* Source badge row */}
      <div className="flex items-center gap-1.5 mb-2">
        {isGit ? <GitBadge /> : <NativeBadge />}
        {issue.platform && (
          <span className="text-[10px] font-mono text-[var(--text-faint)]">{issue.platform}</span>
        )}
        {issue.manualStateOverride && (
          <span
            className="text-[10px] font-mono ml-auto px-1.5 py-0.5 rounded"
            style={{ color: 'var(--warn)', background: 'color-mix(in srgb, var(--warn) 12%, transparent)' }}
          >
            overridden
          </span>
        )}
      </div>

      {/* Title */}
      <p className="text-sm font-medium text-[var(--text)] leading-snug line-clamp-2 mb-2 group-hover:text-[var(--brand-teal)] transition-colors">
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
            style={{ background: 'linear-gradient(135deg, var(--brand-teal), var(--brand-indigo))', color: '#0B1120' }}
            title={issue.assigneeId}
          >
            {issue.assigneeId.slice(0, 2).toUpperCase()}
          </div>
        )}
        {issue.effortEstimate?.score != null && (
          <span className="text-[10px] font-mono text-[var(--text-faint)]">
            D{issue.effortEstimate.score}
          </span>
        )}
        {issue.pullRequest && (
          <span className="text-[10px] font-mono text-[var(--brand-teal)] ml-auto">PR #{issue.pullRequest.number}</span>
        )}
      </div>
    </div>
  )
}
