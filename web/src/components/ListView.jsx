/**
 * ListView — flat list of issues grouped by state.
 */
import { GitBadge, NativeBadge, StateChip, LabelPills } from './IssueBadge.jsx'

function IssueRow({ issue, onClick }) {
  const isGit = issue.source === 'git'

  return (
    <div
      onClick={() => onClick(issue)}
      className="flex items-center gap-4 px-5 py-3.5 border-b border-[#1e2d45] last:border-0 hover:bg-[#0d1628]/60 cursor-pointer transition-colors duration-100 group"
    >
      {/* Source badge */}
      <div className="shrink-0">
        {isGit ? <GitBadge /> : <NativeBadge />}
      </div>

      {/* State */}
      <div className="shrink-0 w-[90px]">
        <StateChip
          state={issue.state}
          derivedState={issue.manualStateOverride ?? issue.derivedState}
        />
      </div>

      {/* Title */}
      <div className="flex-1 min-w-0">
        <p className="text-sm font-medium text-[#e2e8f0] truncate group-hover:text-white transition-colors">
          {issue.title}
        </p>
        {issue.body && (
          <p className="text-xs text-[#64748b] truncate mt-0.5">{issue.body.slice(0, 100)}</p>
        )}
      </div>

      {/* Labels */}
      <div className="hidden md:block shrink-0">
        <LabelPills labels={issue.labels?.slice(0, 2)} />
      </div>

      {/* Effort */}
      {issue.effortEstimate?.score != null && (
        <div className="shrink-0 hidden lg:block">
          <span className="text-[10px] font-mono text-[#6366F1] bg-[#6366F118] px-1.5 py-0.5 rounded">
            D{issue.effortEstimate.score}
          </span>
        </div>
      )}

      {/* PR link */}
      {issue.pullRequest && (
        <div className="shrink-0 hidden md:block">
          <span className="text-[10px] font-mono text-[#2DD4BF]">PR #{issue.pullRequest.number}</span>
        </div>
      )}

      {/* Assignee */}
      {issue.assigneeId && (
        <div
          className="w-6 h-6 rounded-full flex items-center justify-center text-[9px] font-bold shrink-0"
          style={{ background: 'linear-gradient(135deg, #2DD4BF, #6366F1)', color: '#0B1120' }}
          title={issue.assigneeId}
        >
          {issue.assigneeId.slice(0, 2).toUpperCase()}
        </div>
      )}

      <svg width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2"
        className="shrink-0 text-[#334155] group-hover:text-[#64748b] transition-colors">
        <path strokeLinecap="round" strokeLinejoin="round" d="m8.25 4.5 7.5 7.5-7.5 7.5" />
      </svg>
    </div>
  )
}

export function ListView({ issues, onCardClick }) {
  return (
    <div className="bg-[#111827] border border-[#1e2d45] rounded-xl overflow-hidden">
      {/* Column headers */}
      <div className="flex items-center gap-4 px-5 py-2.5 border-b border-[#1e2d45] bg-[#0d1628]/50">
        <span className="text-[10px] font-semibold text-[#64748b] uppercase tracking-widest w-[54px]">Source</span>
        <span className="text-[10px] font-semibold text-[#64748b] uppercase tracking-widest w-[90px]">State</span>
        <span className="text-[10px] font-semibold text-[#64748b] uppercase tracking-widest flex-1">Title</span>
        <span className="text-[10px] font-semibold text-[#64748b] uppercase tracking-widest hidden md:block w-20">Labels</span>
        <span className="text-[10px] font-semibold text-[#64748b] uppercase tracking-widest hidden lg:block w-10">Diff</span>
        <span className="text-[10px] font-semibold text-[#64748b] uppercase tracking-widest hidden md:block w-16">PR</span>
        <span className="text-[10px] font-semibold text-[#64748b] uppercase tracking-widest w-10">Who</span>
        <span className="w-4" />
      </div>
      {issues.length === 0 ? (
        <div className="py-16 text-center">
          <p className="text-sm text-[#64748b]">No issues match the current filters.</p>
        </div>
      ) : (
        issues.map(issue => (
          <IssueRow key={issue.id} issue={issue} onClick={onCardClick} />
        ))
      )}
    </div>
  )
}
