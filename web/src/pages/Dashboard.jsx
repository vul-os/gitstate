import { useNavigate } from 'react-router-dom'
import { FolderGit2, GitPullRequest, CircleDot, GitMerge, Timer, ArrowRight } from 'lucide-react'
import { Card } from '../components/ui/Card.jsx'
import { Button } from '../components/ui/Button.jsx'
import { Stat } from '../components/ui/Stat.jsx'
import { PageHeader, Spinner, ErrorState, EmptyState } from '../components/common.jsx'
import { useAsync } from '../lib/hooks.js'
import { listRepos, projectState } from '../lib/api.js'

// Load every repo, then its (possibly-cached) project state. A repo with no
// derived state yet simply contributes zeros to the roll-up.
async function loadOverview() {
  const repos = await listRepos()
  const states = await Promise.all(
    repos.map((r) =>
      projectState(r.id)
        .then((s) => ({ repo: r, state: s }))
        .catch(() => ({ repo: r, state: null })),
    ),
  )
  return states
}

function median(nums) {
  const xs = nums.filter((n) => typeof n === 'number').sort((a, b) => a - b)
  if (!xs.length) return null
  const mid = Math.floor(xs.length / 2)
  return xs.length % 2 ? xs[mid] : (xs[mid - 1] + xs[mid]) / 2
}

function StatTile({ icon: Icon, label, value }) {
  return (
    <Card padding="md" className="flex items-center gap-4">
      <span className="grid h-10 w-10 shrink-0 place-items-center rounded-lg bg-[var(--bg-surface2)] text-[var(--brand-teal)]">
        <Icon size={18} />
      </span>
      <Stat label={label} value={value} />
    </Card>
  )
}

export default function Dashboard() {
  const navigate = useNavigate()
  const { data, loading, error, reload } = useAsync(loadOverview, [])

  if (loading) return <div><PageHeader title="Dashboard" /><Spinner /></div>
  if (error) return <div><PageHeader title="Dashboard" /><ErrorState error={error} onRetry={reload} /></div>

  if (!data?.length) {
    return (
      <div>
        <PageHeader title="Dashboard" subtitle="Your local project ledger — derived from git + forge, on your machine." />
        <EmptyState
          icon={<FolderGit2 size={22} />}
          title="Add your first repo"
          description="gitstate derives true project state, effort and contribution directly from your git history and forge — no server, no upload."
          action={<Button onClick={() => navigate('/repos')} rightIcon={<ArrowRight size={15} />}>Add a repo</Button>}
        />
      </div>
    )
  }

  const scanned = data.filter((d) => d.state)
  const sum = (sel) => scanned.reduce((acc, d) => acc + (sel(d.state) || 0), 0)
  const openPrs = sum((s) => s.open_prs)
  const mergedPrs = sum((s) => s.merged_prs)
  const openIssues = sum((s) => s.open_issues)
  const cycleP50 = median(scanned.map((d) => d.state?.cycle_time_p50_hours).filter((n) => n != null))

  return (
    <div>
      <PageHeader
        title="Dashboard"
        subtitle={`${data.length} repo${data.length === 1 ? '' : 's'} tracked locally · ${scanned.length} with derived state`}
        actions={<Button variant="outline" onClick={() => navigate('/repos')}>Manage repos</Button>}
      />

      <div className="mb-8 grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <StatTile icon={GitPullRequest} label="Open PRs" value={openPrs} />
        <StatTile icon={GitMerge} label="Merged PRs" value={mergedPrs} />
        <StatTile icon={CircleDot} label="Open issues" value={openIssues} />
        <StatTile icon={Timer} label="Cycle p50 (h)" value={cycleP50 != null ? cycleP50.toFixed(0) : '—'} />
      </div>

      <h2 className="mb-3 text-sm font-semibold text-[var(--text)]">Repositories</h2>
      <div className="flex flex-col gap-3">
        {data.map(({ repo, state }) => (
          <Card key={repo.id} padding="md" hoverable className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <button
              type="button"
              onClick={() => navigate(`/repos/${repo.id}`)}
              className="flex min-w-0 items-center gap-3 text-left"
            >
              <span className="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-[var(--bg-surface2)] text-[var(--brand-teal)]">
                <FolderGit2 size={17} />
              </span>
              <span className="min-w-0">
                <span className="block truncate font-semibold text-[var(--text)]">{repo.slug}</span>
                <span className="mt-0.5 block text-xs text-[var(--text-faint)]">
                  {state
                    ? `${state.open_prs} open · ${state.merged_prs} merged · ${state.done} done`
                    : 'not scanned yet'}
                </span>
              </span>
            </button>
            <Button variant="ghost" size="sm" onClick={() => navigate(`/repos/${repo.id}`)} rightIcon={<ArrowRight size={14} />}>
              Open
            </Button>
          </Card>
        ))}
      </div>
    </div>
  )
}
