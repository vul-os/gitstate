/**
 * Settings page stub — org/member/billing/API settings live here in Wave B3+.
 */

function Section({ title, description, children }) {
  return (
    <section className="bg-[#111827] border border-[#1e2d45] rounded-xl p-6 mb-4">
      <div className="mb-4">
        <h2 className="text-sm font-semibold text-[#e2e8f0]">{title}</h2>
        {description && (
          <p className="text-xs text-[#64748b] mt-0.5">{description}</p>
        )}
      </div>
      {children}
    </section>
  )
}

function FieldRow({ label, value, hint }) {
  return (
    <div className="flex items-center gap-4 py-3 border-b border-[#1e2d45] last:border-0">
      <div className="flex-1 min-w-0">
        <p className="text-xs font-medium text-[#94a3b8]">{label}</p>
        {hint && <p className="text-xs text-[#334155] mt-0.5">{hint}</p>}
      </div>
      <div className="text-sm font-mono text-[#64748b] truncate max-w-[200px]">{value ?? '—'}</div>
      <button className="text-xs text-[#2DD4BF] hover:text-[#5eead4] transition-colors duration-150 shrink-0">
        Edit
      </button>
    </div>
  )
}

export default function Settings() {
  return (
    <div className="max-w-2xl">
      <div className="mb-8">
        <h1 className="text-2xl font-bold text-[#e2e8f0] tracking-tight">Settings</h1>
        <p className="text-sm text-[#64748b] mt-1">Workspace and account preferences.</p>
      </div>

      <Section title="Profile" description="Your personal account details.">
        <FieldRow label="Display name" value="—" hint="Shown on commits and mentions" />
        <FieldRow label="Email" value="—" hint="Used for auth and notifications" />
        <FieldRow label="Password" value="••••••••" hint="Last changed: —" />
      </Section>

      <Section title="Organization" description="Your workspace settings. (Wave B2)">
        <FieldRow label="Name" value="—" hint="Shown to team members and clients" />
        <FieldRow label="Slug" value="—" hint="URL prefix for your workspace" />
        <FieldRow label="Plan" value="Free" hint="Billing available in Wave E" />
      </Section>

      <Section title="Integrations" description="Connected git platforms. (Wave C1)">
        <div className="flex items-center gap-3 py-3">
          <div className="w-8 h-8 rounded-lg bg-[#0d1628] border border-[#1e2d45] flex items-center justify-center">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="#e2e8f0">
              <path d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z" />
            </svg>
          </div>
          <div className="flex-1">
            <p className="text-sm text-[#e2e8f0]">GitHub</p>
            <p className="text-xs text-[#64748b]">Not connected</p>
          </div>
          <button
            className="text-xs font-medium px-3 py-1.5 rounded-lg border border-[#1e2d45] text-[#94a3b8] hover:border-[#2DD4BF]/40 hover:text-[#2DD4BF] transition-all duration-150"
          >
            Connect
          </button>
        </div>
        <div className="flex items-center gap-3 py-3 border-t border-[#1e2d45]">
          <div className="w-8 h-8 rounded-lg bg-[#0d1628] border border-[#1e2d45] flex items-center justify-center">
            <svg width="16" height="16" viewBox="0 0 380 380" fill="none">
              <path d="M282.8 170.3L195.5 7.7C193.3 3 189 0 184.2 0s-9.1 3-11.3 7.7L97 156.2l187.8-.6-2 14.7z" fill="#e24329" />
              <path d="M97 156.2L9.7 318.8c-2.2 4.7-.8 10.3 3.4 13.4 2 1.5 4.4 2.3 6.8 2.3 2.6 0 5.2-.9 7.2-2.7l157.1-131.9L97 156.2z" fill="#fc6d26" />
              <path d="M282.8 170.3l-98.6-.9 15.1 35.2 81.8 51.2L282.8 170.3z" fill="#e24329" />
              <path d="M280.1 319.8l-96.4-120.1-86.4 100.3 90.4 75.9c4.1 3.4 9.9 3.4 14 0l78.4-56.1z" fill="#fc6d26" />
            </svg>
          </div>
          <div className="flex-1">
            <p className="text-sm text-[#e2e8f0]">GitLab</p>
            <p className="text-xs text-[#64748b]">Not connected</p>
          </div>
          <button
            className="text-xs font-medium px-3 py-1.5 rounded-lg border border-[#1e2d45] text-[#94a3b8] hover:border-[#2DD4BF]/40 hover:text-[#2DD4BF] transition-all duration-150"
          >
            Connect
          </button>
        </div>
      </Section>

      <Section title="Danger zone" description="Irreversible actions.">
        <div className="flex items-center justify-between py-2">
          <div>
            <p className="text-sm text-[#e2e8f0]">Delete organization</p>
            <p className="text-xs text-[#64748b] mt-0.5">Permanently deletes the workspace and all data.</p>
          </div>
          <button className="text-xs font-medium px-3 py-1.5 rounded-lg border border-red-500/30 text-red-400 hover:bg-red-500/10 transition-all duration-150">
            Delete
          </button>
        </div>
      </Section>
    </div>
  )
}
