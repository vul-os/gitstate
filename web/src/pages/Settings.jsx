import { Server, Cpu, Radio, ShieldCheck, Sun, Moon } from 'lucide-react'
import { Card } from '../components/ui/Card.jsx'
import { Badge } from '../components/ui/Badge.jsx'
import { PageHeader, Spinner, ErrorState, MetricPill } from '../components/common.jsx'
import { useAsync } from '../lib/hooks.js'
import { health, syncStatus } from '../lib/api.js'
import { useTheme } from '../lib/theme.jsx'

async function loadStatus() {
  const [h, sync] = await Promise.all([
    health(),
    syncStatus().catch(() => ({ enabled: false, peers: 0, peer_id: null, last_op_hlc: null })),
  ])
  return { h, sync }
}

function Row({ icon: Icon, title, children }) {
  return (
    <Card padding="lg" className="mb-4">
      <div className="mb-4 flex items-center gap-2.5">
        <span className="grid h-7 w-7 place-items-center rounded-[6px] bg-[#2DD4BF]/14 text-[var(--brand-teal)]">
          <Icon size={15} />
        </span>
        <h2 className="text-sm font-semibold text-[var(--text)]">{title}</h2>
      </div>
      {children}
    </Card>
  )
}

export default function Settings() {
  const { data, loading, error, reload } = useAsync(loadStatus, [])
  const { resolved, toggle } = useTheme()

  return (
    <div className="max-w-3xl">
      <PageHeader title="Settings" subtitle="gitstate runs as a local daemon on your machine. This is its live status." />

      {loading && <Spinner />}
      {error && <ErrorState error={error} onRetry={reload} />}

      {!loading && !error && data && (
        <>
          <Row icon={Server} title="Daemon">
            <div className="grid grid-cols-2 gap-5 sm:grid-cols-3">
              <MetricPill label="Status" value={<Badge color="teal">{data.h.status || 'ok'}</Badge>} />
              <MetricPill label="Version" value={data.h.version || '—'} />
              <MetricPill label="Port" value={typeof window !== 'undefined' && window.location.port ? window.location.port : '7473'} />
            </div>
          </Row>

          <Row icon={Cpu} title="Classifier">
            <p className="text-sm text-[var(--text-muted)]">
              Active engine: <Badge color={data.h.classifier === 'llm' ? 'indigo' : 'default'}>{data.h.classifier || 'heuristic'}</Badge>
            </p>
            <p className="mt-2 text-xs text-[var(--text-faint)]">
              Set <code className="font-mono">VULOS_LLMUX_URL</code> or <code className="font-mono">OPENAI_BASE_URL</code> (with an API key)
              in the daemon's environment to enable LLM classification and effort judging. Without it, a deterministic heuristic is used — everything stays on-device.
            </p>
          </Row>

          <Row icon={Radio} title="Peer-to-peer sync">
            <div className="grid grid-cols-2 gap-5 sm:grid-cols-3">
              <MetricPill label="Enabled" value={<Badge color={data.sync.enabled ? 'teal' : 'default'}>{data.sync.enabled ? 'on' : 'off'}</Badge>} />
              <MetricPill label="Peers" value={data.sync.peers ?? 0} />
              <MetricPill label="Peer ID" value={data.sync.peer_id ? `${String(data.sync.peer_id).slice(0, 8)}…` : '—'} />
            </div>
            {!data.sync.enabled && (
              <p className="mt-3 text-xs text-[var(--text-faint)]">
                CRDT sync of contexts + categories ships in the optional <code className="font-mono">sync-dmtap</code> build. A plain build never touches P2P deps.
              </p>
            )}
          </Row>

          <Row icon={ShieldCheck} title="Taxonomy">
            <p className="text-sm text-[var(--text-muted)]">
              Signed taxonomy: <Badge color={data.h.sync ? 'teal' : 'default'}>loaded</Badge>
            </p>
            <p className="mt-2 text-xs text-[var(--text-faint)]">
              View and verify the signed category tree on the <a href="/taxonomy" className="text-[var(--brand-teal)] hover:underline">Taxonomy</a> page.
            </p>
          </Row>

          <Row icon={resolved === 'light' ? Sun : Moon} title="Appearance">
            <button
              type="button"
              onClick={toggle}
              className="rounded-[var(--radius-btn)] border border-[var(--border2)] bg-[var(--bg-surface2)] px-4 py-2 text-sm text-[var(--text)] hover:bg-[var(--bg-surface3)]"
            >
              Switch to {resolved === 'light' ? 'dark' : 'light'} theme
            </button>
          </Row>
        </>
      )}
    </div>
  )
}
