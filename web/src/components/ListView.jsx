/**
 * ListView — flat list of issues, one row each.
 * Fully token-driven so it renders correctly in both themes.
 */
import { GitBadge, NativeBadge, StateChip, LabelPills } from './IssueBadge.jsx'

function IssueRow({ issue, onClick }) {
  const isGit = issue.source === 'git'

  return (
    <div
      onClick={() => onClick(issue)}
      className="flex items-center gap-4 px-5 py-3.5 border-b border-[var(--border)] last:border-0 hover:bg-[var(--bg-surface2)]/60 cursor-pointer transition-colors duration-100 group"
    >
      {/* Source badge */}
      <div className="shrink-0 w-[54px]">
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
        <p className="text-sm font-medium text-[var(--text)] truncate group-hover:text-[var(--brand-teal)] transition-colors">
          {issue.title}
        </p>
        {issue.body && (
          <p className="text-xs text-[var(--text-faint)] truncate mt-0.5">{issue.body.slice(0, 100)}</p>
        )}
      </div>

      {/* Labels */}
      <div className="hidden md:block shrink-0 w-20">
        <LabelPills labels={issue.labels?.slice(0, 2)} />
      </div>

      {/* Effort */}
      <div className="shrink-0 hidden lg:block w-10">
        {issue.effortEstimate?.score != null && (
          <span className="text-[10px] font-mono text-[var(--brand-indigo)] bg-[var(--brand-indigo)]/10 px-1.5 py-0.5 rounded-[var(--radius-badge)]">
            D{issue.effortEstimate.score}
          </span>
        )}
      </div>

      {/* PR link */}
      <div className="shrink-0 hidden md:block w-16">
        {issue.pullRequest && (
          <span className="text-[10px] font-mono text-[var(--brand-teal)]">PR #{issue.pullRequest.number}</span>
        )}
      </div>

      {/* Assignee */}
      <div className="shrink-0 w-10 flex justify-center">
        {issue.assigneeId ? (
          <div
            className="w-6 h-6 rounded-full flex items-center justify-center text-[9px] font-bold"
            style={{ background: 'linear-gradient(135deg, var(--brand-teal), var(--brand-indigo))', color: '#0B1120' }}
            title={issue.assigneeId}
          >
            {issue.assigneeId.slice(0, 2).toUpperCase()}
          </div>
        ) : (
          <span className="text-[var(--text-faint)] text-xs font-mono">—</span>
        )}
      </div>

      <svg width="14" height="14" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2"
        className="shrink-0 text-[var(--text-faint)]/50 group-hover:text-[var(--text-faint)] group-hover:translate-x-0.5 transition-all duration-150">
        <path strokeLinecap="round" strokeLinejoin="round" d="m8.25 4.5 7.5 7.5-7.5 7.5" />
      </svg>
    </div>
  )
}

export function ListView({ issues, onCardClick }) {
  return (
    <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-[var(--radius-card)] overflow-hidden shadow-[var(--shadow-card)]">
      {/* Column headers */}
      <div className="flex items-center gap-4 px-5 py-2.5 border-b border-[var(--border)] bg-[var(--bg-surface2)]/40">
        <span className="text-[10px] font-semibold text-[var(--text-faint)] uppercase tracking-widest w-[54px]">Source</span>
        <span className="text-[10px] font-semibold text-[var(--text-faint)] uppercase tracking-widest w-[90px]">State</span>
        <span className="text-[10px] font-semibold text-[var(--text-faint)] uppercase tracking-widest flex-1">Title</span>
        <span className="text-[10px] font-semibold text-[var(--text-faint)] uppercase tracking-widest hidden md:block w-20">Labels</span>
        <span className="text-[10px] font-semibold text-[var(--text-faint)] uppercase tracking-widest hidden lg:block w-10">Diff</span>
        <span className="text-[10px] font-semibold text-[var(--text-faint)] uppercase tracking-widest hidden md:block w-16">PR</span>
        <span className="text-[10px] font-semibold text-[var(--text-faint)] uppercase tracking-widest w-10 text-center">Who</span>
        <span className="w-3.5" />
      </div>
      {issues.length === 0 ? (
        <div className="py-16 text-center">
          <p className="text-sm text-[var(--text-faint)]">No issues match the current filters.</p>
        </div>
      ) : (
        issues.map(issue => (
          <IssueRow key={issue.id} issue={issue} onClick={onCardClick} />
        ))
      )}
    </div>
  )
}
