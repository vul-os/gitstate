/**
 * TableView — dense spreadsheet-style view of all issues.
 * Fully token-driven so it renders correctly in both themes.
 */
import { GitBadge, NativeBadge, StateChip } from './IssueBadge.jsx'

const DIFFICULTY_LABELS = ['', 'Trivial', 'Easy', 'Medium', 'Hard', 'Complex']
const DIFFICULTY_COLORS = ['', '#2DD4BF', '#22c55e', '#f59e0b', '#f97316', '#ef4444']

const TH = 'text-left px-4 py-2.5 text-[10px] font-semibold text-[var(--text-faint)] uppercase tracking-widest'

export function TableView({ issues, onCardClick }) {
  return (
    <div className="rounded-[var(--radius-card)] overflow-x-auto border border-[var(--border)] bg-[var(--bg-surface)] shadow-[var(--shadow-card)]">
      <table className="w-full text-sm border-collapse">
        <thead>
          <tr className="bg-[var(--bg-surface2)]/40 border-b border-[var(--border)]">
            <th className={`${TH} w-[60px]`}>Source</th>
            <th className={`${TH} w-[100px]`}>State</th>
            <th className={TH}>Title</th>
            <th className={`${TH} w-[80px] hidden md:table-cell`}>Platform</th>
            <th className={`${TH} w-[80px] hidden lg:table-cell`}>Difficulty</th>
            <th className={`${TH} w-[70px] hidden md:table-cell`}>PR</th>
            <th className={`${TH} w-[80px]`}>Assignee</th>
          </tr>
        </thead>
        <tbody>
          {issues.length === 0 && (
            <tr>
              <td colSpan={7} className="text-center py-14 text-sm text-[var(--text-faint)]">
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
                tabIndex={0}
                aria-label={`Open issue: ${issue.title}`}
                onClick={() => onCardClick(issue)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault()
                    onCardClick(issue)
                  }
                }}
                className={[
                  'cursor-pointer transition-colors duration-100 hover:bg-[var(--bg-surface2)]/70 group focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-[var(--brand-teal)]',
                  idx === 0 ? '' : 'border-t border-[var(--border)]',
                  idx % 2 === 0 ? '' : 'bg-[var(--bg-surface2)]/20',
                ].join(' ')}
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
                  <span className="text-sm font-medium text-[var(--text)] truncate block group-hover:text-[var(--brand-teal)] transition-colors">
                    {issue.title}
                  </span>
                </td>
                <td className="px-4 py-2.5 hidden md:table-cell">
                  {issue.platform && (
                    <span className="text-xs font-mono text-[var(--text-faint)]">{issue.platform}</span>
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
                    <span className="text-[10px] font-mono text-[var(--brand-teal)]">
                      #{issue.pullRequest.number}
                    </span>
                  )}
                </td>
                <td className="px-4 py-2.5">
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
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
