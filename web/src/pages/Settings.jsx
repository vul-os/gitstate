import { useNavigate, Link } from 'react-router-dom'
import {
  User, Building2, Plug, AlertTriangle, LogOut, Users, CreditCard,
  ChevronRight, Pencil,
} from 'lucide-react'
import { useAuth } from '../lib/useAuth.js'
import { useOrg } from '../lib/useOrg.js'
import { Card, Badge, Button } from '../components/ui/index.js'
import { Reveal } from '../components/Reveal.jsx'

function SectionCard({ icon: Icon, title, description, children, delay = 0, tone = 'default' }) {
  const iconColor = tone === 'danger' ? 'text-red-400' : 'text-[var(--text-faint)]'
  return (
    <Reveal delay={delay}>
      <Card padding="lg" className={`mb-4 ${tone === 'danger' ? 'border-red-500/20' : ''}`}>
        <div className="mb-4 flex items-start gap-2.5">
          {Icon && <Icon size={16} className={`mt-0.5 shrink-0 ${iconColor}`} />}
          <div>
            <h2 className="text-sm font-semibold text-[var(--text)]">{title}</h2>
            {description && <p className="text-xs text-[var(--text-faint)] mt-0.5">{description}</p>}
          </div>
        </div>
        {children}
      </Card>
    </Reveal>
  )
}

function FieldRow({ label, value, hint, action }) {
  return (
    <div className="flex items-center gap-4 py-3 border-b border-[var(--border)] last:border-0">
      <div className="flex-1 min-w-0">
        <p className="text-xs font-medium text-[var(--text-muted)]">{label}</p>
        {hint && <p className="text-xs text-[var(--text-faint)] mt-0.5">{hint}</p>}
      </div>
      <div className="text-sm font-mono text-[var(--text-dim)] truncate max-w-[200px]">{value ?? '—'}</div>
      {action ?? (
        <button className="flex items-center gap-1 text-xs text-[var(--brand-teal)] hover:text-[#5eead4] transition-colors duration-150 shrink-0">
          <Pencil size={11} /> Edit
        </button>
      )}
    </div>
  )
}

function Avatar({ user }) {
  const initials = user?.name
    ? user.name.split(' ').map(w => w[0]).join('').slice(0, 2).toUpperCase()
    : user?.email?.slice(0, 2).toUpperCase() ?? '?'
  return (
    <div className="w-12 h-12 rounded-full bg-gradient-to-br from-[var(--brand-teal)] to-[var(--brand-indigo)] flex items-center justify-center text-sm font-bold text-[#0B1120] select-none shrink-0">
      {initials}
    </div>
  )
}

export default function Settings() {
  const { user, logout } = useAuth()
  const { activeOrg, orgRole } = useOrg()
  const navigate = useNavigate()

  async function handleSignOut() {
    await logout()
    navigate('/login', { replace: true })
  }

  return (
    <div className="max-w-2xl">
      <Reveal>
        <div className="mb-8">
          <h1 className="font-display text-2xl font-semibold text-[var(--text)] tracking-tight">Settings</h1>
          <p className="text-sm text-[var(--text-faint)] mt-1">Workspace and account preferences.</p>
        </div>
      </Reveal>

      {/* Account section */}
      <SectionCard icon={User} title="Account" description="Your personal account details." delay={0.05}>
        <div className="flex items-center gap-4 pb-4 mb-2 border-b border-[var(--border)]">
          <Avatar user={user} />
          <div className="flex-1 min-w-0">
            <p className="text-sm font-semibold text-[var(--text)] truncate">{user?.name ?? 'Unknown'}</p>
            <p className="text-xs text-[var(--text-faint)] truncate mt-0.5">{user?.email ?? ''}</p>
            {user?.role && <Badge color="teal" className="mt-1.5">{user.role}</Badge>}
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={handleSignOut}
            leftIcon={<LogOut size={13} />}
            className="hover:border-red-500/30 hover:text-red-400 shrink-0"
          >
            Sign out
          </Button>
        </div>

        <FieldRow label="Display name" value={user?.name} hint="Shown on commits and mentions" />
        <FieldRow label="Email" value={user?.email} hint="Used for auth and notifications" />
        <FieldRow label="Password" value="••••••••" hint="Change your password" />
      </SectionCard>

      <SectionCard
        icon={Building2}
        title="Organization"
        description={activeOrg ? `Active workspace: ${activeOrg.name}` : 'Your workspace settings.'}
        delay={0.1}
      >
        <FieldRow label="Name" value={activeOrg?.name ?? '—'} hint="Shown to team members and clients" />
        <FieldRow label="Slug" value={activeOrg?.slug ?? '—'} hint="URL prefix for your workspace" />
        <FieldRow
          label="Plan"
          value={activeOrg?.planKey ? activeOrg.planKey.charAt(0).toUpperCase() + activeOrg.planKey.slice(1) : 'Free'}
          hint="Manage your plan and invoices"
          action={
            <Link to="/settings/billing" className="flex items-center gap-1 text-xs text-[var(--brand-teal)] hover:text-[#5eead4] transition-colors duration-150 shrink-0">
              <CreditCard size={11} /> Billing
            </Link>
          }
        />
        <FieldRow label="Your role" value={orgRole ?? '—'} hint="Your permission level in this org" />
        <div className="flex items-center justify-between py-3">
          <div className="flex items-start gap-2.5">
            <Users size={15} className="mt-0.5 text-[var(--text-faint)] shrink-0" />
            <div>
              <p className="text-xs font-medium text-[var(--text-muted)]">Members</p>
              <p className="text-xs text-[var(--text-faint)] mt-0.5">Invite teammates and clients (stakeholders are free)</p>
            </div>
          </div>
          <Link to="/settings/members">
            <Button variant="outline" size="sm" rightIcon={<ChevronRight size={13} />}>Manage members</Button>
          </Link>
        </div>
      </SectionCard>

      <SectionCard icon={Plug} title="Integrations" description="Connected git platforms." delay={0.15}>
        <div className="flex items-center gap-3 py-3">
          <div className="w-9 h-9 rounded-[var(--radius-btn)] bg-[var(--bg)] border border-[var(--border)] flex items-center justify-center shrink-0">
            <svg width="17" height="17" viewBox="0 0 24 24" fill="var(--text)" aria-hidden>
              <path d="M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12" />
            </svg>
          </div>
          <div className="flex-1 min-w-0">
            <p className="text-sm text-[var(--text)]">GitHub</p>
            <p className="text-xs text-[var(--text-faint)]">Connect from the Repositories page</p>
          </div>
          <Link to="/repos">
            <Button variant="outline" size="sm">Connect</Button>
          </Link>
        </div>
        <div className="flex items-center gap-3 py-3 border-t border-[var(--border)]">
          <div className="w-9 h-9 rounded-[var(--radius-btn)] bg-[var(--bg)] border border-[var(--border)] flex items-center justify-center shrink-0">
            <svg width="17" height="17" viewBox="0 0 380 380" fill="none">
              <path d="M282.8 170.3L195.5 7.7C193.3 3 189 0 184.2 0s-9.1 3-11.3 7.7L97 156.2l187.8-.6-2 14.7z" fill="#e24329" />
              <path d="M97 156.2L9.7 318.8c-2.2 4.7-.8 10.3 3.4 13.4 2 1.5 4.4 2.3 6.8 2.3 2.6 0 5.2-.9 7.2-2.7l157.1-131.9L97 156.2z" fill="#fc6d26" />
              <path d="M282.8 170.3l-98.6-.9 15.1 35.2 81.8 51.2L282.8 170.3z" fill="#e24329" />
              <path d="M280.1 319.8l-96.4-120.1-86.4 100.3 90.4 75.9c4.1 3.4 9.9 3.4 14 0l78.4-56.1z" fill="#fc6d26" />
            </svg>
          </div>
          <div className="flex-1 min-w-0">
            <p className="text-sm text-[var(--text)]">GitLab</p>
            <p className="text-xs text-[var(--text-faint)]">Connect from the Repositories page</p>
          </div>
          <Link to="/repos">
            <Button variant="outline" size="sm">Connect</Button>
          </Link>
        </div>
      </SectionCard>

      <SectionCard icon={AlertTriangle} title="Danger zone" description="Irreversible actions." delay={0.2} tone="danger">
        <div className="flex items-center justify-between py-2">
          <div>
            <p className="text-sm text-[var(--text)]">Delete organization</p>
            <p className="text-xs text-[var(--text-faint)] mt-0.5">Permanently deletes the workspace and all data.</p>
          </div>
          <Button variant="danger" size="sm">Delete</Button>
        </div>
      </SectionCard>
    </div>
  )
}
