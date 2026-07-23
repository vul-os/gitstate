import { ShieldCheck, CheckCircle2, XCircle } from 'lucide-react'
import { useState } from 'react'
import { Card } from '../components/ui/Card.jsx'
import { Button } from '../components/ui/Button.jsx'
import { Badge } from '../components/ui/Badge.jsx'
import { PageHeader, Spinner, ErrorState, MetricPill } from '../components/common.jsx'
import { useAsync, useAction } from '../lib/hooks.js'
import { taxonomy, verifyTaxonomy } from '../lib/api.js'

export default function Taxonomy() {
  const { data: tax, loading, error, reload } = useAsync(taxonomy, [])
  const [verify, { pending }] = useAction(verifyTaxonomy)
  const [result, setResult] = useState(null)

  async function doVerify() {
    setResult(null)
    try {
      const r = await verify(tax)
      setResult({ ok: true, ...r })
    } catch (e) {
      setResult({ ok: false, message: e.message })
    }
  }

  if (loading) return <div><PageHeader title="Taxonomy" /><Spinner /></div>
  if (error) return <div><PageHeader title="Taxonomy" /><ErrorState error={error} onRetry={reload} /></div>

  return (
    <div>
      <PageHeader
        title="Taxonomy"
        subtitle="A signed, versioned, content-addressed category tree shipped as data — not a service. It gives peers a shared label vocabulary without any central authority."
        actions={<Button onClick={doVerify} disabled={pending} leftIcon={<ShieldCheck size={15} />}>{pending ? 'Verifying…' : 'Verify signature'}</Button>}
      />

      {result && (
        <Card padding="md" className="mb-6 flex items-center gap-3">
          {result.ok
            ? <CheckCircle2 size={20} className="text-[var(--ok)]" />
            : <XCircle size={20} className="text-[var(--bad)]" />}
          <div>
            <p className="text-sm font-medium text-[var(--text)]">
              {result.ok ? 'Signature valid' : 'Verification failed'}
            </p>
            <p className="font-mono text-xs text-[var(--text-faint)]">
              {result.ok ? `id ${result.id?.slice(0, 16)}…` : result.message}
            </p>
          </div>
        </Card>
      )}

      <Card padding="lg" className="mb-6">
        <div className="grid grid-cols-2 gap-5 sm:grid-cols-4">
          <MetricPill label="Schema" value={tax.schema?.split('/').pop() || '—'} />
          <MetricPill label="Version" value={tax.version} />
          <MetricPill label="Categories" value={tax.categories?.length ?? 0} />
          <MetricPill label="Issued" value={tax.issued_at ? new Date(tax.issued_at).toLocaleDateString() : '—'} />
        </div>
        <div className="mt-5 flex flex-col gap-1.5 border-t border-[var(--border)] pt-4 font-mono text-xs text-[var(--text-faint)]">
          <span className="break-all">id: {tax.id}</span>
          <span className="break-all">pubkey: {tax.pubkey}</span>
        </div>
      </Card>

      <h2 className="mb-3 text-sm font-semibold text-[var(--text)]">Categories</h2>
      <Card padding="none">
        {(tax.categories || []).map((c) => (
          <div key={c.key} className="flex items-center gap-3 border-b border-[var(--border)] px-4 py-2.5 last:border-0">
            <span className="h-3 w-3 shrink-0 rounded-full" style={{ background: c.color || 'var(--text-faint)' }} />
            <span className="w-44 shrink-0 font-mono text-xs text-[var(--text-muted)]">{c.key}</span>
            <span className="flex-1 text-sm text-[var(--text)]">{c.label}</span>
            {c.parent && <Badge color="default">↳ {c.parent}</Badge>}
          </div>
        ))}
      </Card>
    </div>
  )
}
