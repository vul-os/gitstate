/**
 * Home / dashboard stub.
 * Will be replaced by Wave 4 (metrics, burndown, cycle-time).
 */

function StatCard({ label, value, trend, mono }) {
  return (
    <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-5 flex flex-col gap-1">
      <span className="text-xs font-medium text-[var(--text-muted)] uppercase tracking-widest">{label}</span>
      <span className={`text-3xl font-bold text-[var(--text)] tracking-tight mt-1 ${mono ? 'font-mono' : ''}`}>
        {value}
      </span>
      {trend && (
        <span className="text-xs text-[var(--brand-teal)] font-mono mt-0.5">{trend}</span>
      )}
    </div>
  )
}

function EmptyGraph() {
  // Decorative sparkline placeholder
  return (
    <div className="flex items-end gap-1 h-12">
      {[40, 65, 45, 80, 55, 90, 70, 100, 75, 88, 60, 95].map((h, i) => (
        <div
          key={i}
          className="flex-1 rounded-sm"
          style={{
            height: `${h}%`,
            background: i === 11
              ? 'linear-gradient(to top, #2DD4BF, #6366F1)'
              : `rgba(99,102,241,${0.2 + i * 0.05})`,
          }}
        />
      ))}
    </div>
  )
}

export default function Home() {
  return (
    <div className="w-full">
      {/* Page header */}
      <div className="mb-8">
        <h1 className="text-2xl font-bold text-[var(--text)] tracking-tight">Overview</h1>
        <p className="text-sm text-[var(--text-muted)] mt-1">
          Derived from git — no tickets to maintain.
        </p>
      </div>

      {/* Stats grid */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-8">
        <StatCard label="Open PRs" value="—" trend="wave 3 connects git" />
        <StatCard label="Avg cycle time" value="—" trend="derived from merges" mono />
        <StatCard label="Commits / 7d" value="—" trend="once git sync is live" mono />
        <StatCard label="Agent runs" value="—" trend="wave 5 billing" />
      </div>

      {/* Activity area */}
      <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-6 mb-6">
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-sm font-semibold text-[var(--text)]">Commit activity</h2>
          <span className="text-xs font-mono text-[var(--text-muted)] bg-[var(--bg-surface2)] px-2 py-0.5 rounded">last 12 weeks</span>
        </div>
        <EmptyGraph />
        <p className="text-xs text-[var(--text-faint)] mt-3 font-mono">
          Connect a repository in Projects to start tracking.
        </p>
      </div>

      {/* Wedge callout */}
      <div
        className="rounded-xl border border-[#2DD4BF]/20 p-5"
        style={{ background: 'linear-gradient(135deg, rgba(45,212,191,0.04), rgba(99,102,241,0.04))' }}
      >
        <div className="flex items-start gap-3">
          <div className="mt-0.5">
            <svg width="18" height="18" fill="none" viewBox="0 0 24 24" stroke="#2DD4BF" strokeWidth="1.8">
              <path strokeLinecap="round" strokeLinejoin="round"
                d="m11.25 11.25.041-.02a.75.75 0 0 1 1.063.852l-.708 2.836a.75.75 0 0 0 1.063.853l.041-.021M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Zm-9-3.75h.008v.008H12V8.25Z" />
            </svg>
          </div>
          <div>
            <h3 className="text-sm font-semibold text-[var(--brand-teal)] mb-1">Git is the real ledger</h3>
            <p className="text-xs text-[var(--text-muted)] leading-relaxed">
              gitstate derives project state, cycle time, and involvement directly from your
              repositories. Merged = done. PR open = in progress. No manual ticket updates.
            </p>
          </div>
        </div>
      </div>
    </div>
  )
}
