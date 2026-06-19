/**
 * WeightTuner — five sliders that live re-rank contributors.
 *
 * Weights are kept raw; the composite recompute normalises them to sum 1, so the
 * displayed "effective %" reflects how much each axis actually pulls the ranking.
 * "Save" is gated to owner/admin via orgRole (handled by the parent).
 */
import { DIMENSIONS, dimColor } from '../../lib/useContribution.js'
import { Button, Badge } from '../ui/index.js'
import { RotateCcw, Save, SlidersHorizontal, Check, Lock } from 'lucide-react'

function Slider({ dim, value, onChange, effPct }) {
  const color = dimColor(dim.key, 60)
  return (
    <div className="space-y-1.5">
      <div className="flex items-baseline justify-between gap-2">
        <div className="flex items-center gap-2 min-w-0">
          <span className="h-2 w-2 rounded-full shrink-0" style={{ background: color }} />
          <span className="text-[13px] font-medium text-[var(--text-dim)]">{dim.label}</span>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          <span className="text-[10px] font-mono text-[var(--text-faint)] tabular-nums">{effPct}%</span>
          <span className="text-[11px] font-mono tabular-nums text-[var(--text-muted)] w-6 text-right">{value}</span>
        </div>
      </div>
      <input
        type="range" min="0" max="10" step="1" value={value}
        onChange={(e) => onChange(dim.key, Number(e.target.value))}
        aria-label={`${dim.label} weight`}
        className="w-full h-1.5 appearance-none rounded-full cursor-pointer outline-none focus-visible:ring-2 focus-visible:ring-[var(--brand-teal)]/50"
        style={{
          accentColor: color,
          background: `linear-gradient(90deg, ${color} 0%, ${color} ${(value / 10) * 100}%, var(--bg-surface3) ${(value / 10) * 100}%, var(--bg-surface3) 100%)`,
        }}
      />
      <p className="text-[10px] text-[var(--text-faint)] leading-snug">{dim.blurb}</p>
    </div>
  )
}

export function WeightTuner({
  weights, onChange, onReset, onSave, dirty, saving, saved, canEdit,
}) {
  let sum = 0
  for (const d of DIMENSIONS) sum += Math.max(0, weights[d.key] || 0)
  const effPct = (k) => (sum > 0 ? Math.round(((Math.max(0, weights[k] || 0)) / sum) * 100) : 0)

  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <SlidersHorizontal size={15} className="text-[var(--brand-teal)]" />
          <h2 className="text-sm font-semibold text-[var(--text)]">Weighting model</h2>
        </div>
        {dirty && <Badge color="yellow">unsaved</Badge>}
      </div>

      <p className="text-xs text-[var(--text-faint)] leading-relaxed -mt-2">
        Drag to weigh each axis. Ranks update live; the percentages show each axis’s
        real pull after normalising. This is a decision aid — not an automatic verdict.
      </p>

      <div className="space-y-4">
        {DIMENSIONS.map((d) => (
          <Slider key={d.key} dim={d} value={weights[d.key] ?? 0} onChange={onChange} effPct={effPct(d.key)} />
        ))}
      </div>

      <div className="flex items-center gap-2 pt-1">
        <Button variant="ghost" size="sm" onClick={onReset} disabled={!dirty || saving} leftIcon={<RotateCcw size={13} />}>
          Reset
        </Button>
        {canEdit ? (
          <Button
            variant="primary" size="sm" className="flex-1"
            onClick={onSave} disabled={!dirty || saving}
            leftIcon={saving
              ? <svg className="animate-spin" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M21 12a9 9 0 1 1-6.219-8.56" strokeLinecap="round" /></svg>
              : saved && !dirty ? <Check size={13} /> : <Save size={13} />}
          >
            {saving ? 'Saving…' : saved && !dirty ? 'Saved' : 'Save weights'}
          </Button>
        ) : (
          <div className="flex-1 flex items-center justify-center gap-1.5 h-8 rounded-[var(--radius-btn)] border border-dashed border-[var(--border)] text-[11px] text-[var(--text-faint)]">
            <Lock size={12} /> Owners &amp; admins can save
          </div>
        )}
      </div>
    </div>
  )
}
