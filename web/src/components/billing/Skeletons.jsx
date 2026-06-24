/**
 * Loading skeletons for the billing console. Pure presentation — a soft pulsing
 * placeholder block plus a few composed layouts that mirror the real sections so
 * the page doesn't reflow when data lands.
 */

export function Skel({ className = '', style }) {
  return (
    <div
      className={`animate-pulse rounded-md ${className}`}
      style={{ background: 'var(--bg-surface3)', ...style }}
    />
  )
}

function Panel({ children }) {
  return (
    <div
      className="rounded-[var(--radius-card)] p-6"
      style={{ background: 'var(--bg-surface)', border: '1px solid var(--border)' }}
    >
      {children}
    </div>
  )
}

export function HeaderSkeleton() {
  return (
    <Panel>
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-3">
          <Skel className="h-3 w-24" />
          <Skel className="h-7 w-40" />
          <Skel className="h-3 w-56" />
        </div>
        <Skel className="h-7 w-24 rounded-full" />
      </div>
    </Panel>
  )
}

export function MetersSkeleton() {
  return (
    <Panel>
      <Skel className="h-4 w-32 mb-6" />
      <div className="space-y-7">
        {[0, 1, 2].map((i) => (
          <div key={i} className="space-y-2">
            <div className="flex justify-between">
              <Skel className="h-3 w-28" />
              <Skel className="h-3 w-16" />
            </div>
            <Skel className="h-2.5 w-full rounded-full" />
          </div>
        ))}
      </div>
    </Panel>
  )
}

export function HeroSkeleton() {
  return (
    <div className="space-y-4">
      <Panel>
        <div className="flex items-start justify-between gap-4">
          <div className="space-y-3">
            <Skel className="h-3 w-24" />
            <Skel className="h-8 w-44" />
            <Skel className="h-3 w-56" />
          </div>
          <Skel className="h-8 w-28 rounded-[var(--radius-btn)]" />
        </div>
      </Panel>
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
        {[0, 1, 2].map((i) => (
          <Panel key={i}>
            <Skel className="h-3 w-24 mb-4" />
            <Skel className="h-8 w-20 mb-3" />
            <Skel className="h-2.5 w-full rounded-full" />
          </Panel>
        ))}
      </div>
    </div>
  )
}

export function BreakdownSkeleton() {
  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
      {[0, 1, 2].map((i) => (
        <Panel key={i}>
          <Skel className="h-3 w-24 mb-4" />
          <div className="flex items-end justify-between">
            <Skel className="h-8 w-16" />
            <Skel className="h-4 w-14" />
          </div>
        </Panel>
      ))}
    </div>
  )
}

export function PlansSkeleton() {
  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
      {[0, 1, 2, 3, 4].map((i) => (
        <Panel key={i}>
          <Skel className="h-4 w-20 mb-4" />
          <Skel className="h-9 w-28 mb-5" />
          <div className="space-y-2.5">
            {[0, 1, 2, 3].map((j) => <Skel key={j} className="h-3 w-full" />)}
          </div>
          <Skel className="h-9 w-full mt-6 rounded-[var(--radius-btn)]" />
        </Panel>
      ))}
    </div>
  )
}

export function InvoicesSkeleton() {
  return (
    <Panel>
      <Skel className="h-3 w-24 mb-5" />
      <div className="space-y-3">
        {[0, 1, 2].map((i) => (
          <div key={i} className="flex items-center gap-4">
            <Skel className="h-9 w-9 rounded-lg" />
            <div className="flex-1 space-y-2">
              <Skel className="h-3 w-32" />
              <Skel className="h-2.5 w-24" />
            </div>
            <Skel className="h-4 w-16" />
            <Skel className="h-5 w-14 rounded-full" />
          </div>
        ))}
      </div>
    </Panel>
  )
}
