/**
 * SectionCard — a titled, icon-chipped card used to group a block of settings or
 * integration controls. Shared by the Settings and Integrations pages so both
 * keep an identical look (Reveal fade-up, accent chip, optional danger tone).
 */
import { Card } from './ui/index.js'
import { Reveal } from './Reveal.jsx'

export function SectionCard({ icon: Icon, title, description, children, delay = 0, tone = 'default', accent = 'var(--brand-teal)', id }) {
  const isDanger = tone === 'danger'
  const chipColor = isDanger ? 'var(--bad)' : accent
  return (
    <Reveal delay={delay}>
      <Card
        id={id}
        padding="lg"
        className="mb-4 scroll-mt-20"
        style={isDanger ? { borderColor: 'color-mix(in srgb, var(--bad) 30%, transparent)' } : undefined}
      >
        <div className="mb-4 flex items-start gap-2.5">
          {Icon && (
            <span
              className="grid place-items-center w-7 h-7 rounded-[6px] shrink-0"
              style={{ color: chipColor, background: `color-mix(in srgb, ${chipColor} 14%, transparent)` }}
            >
              <Icon size={15} />
            </span>
          )}
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

export default SectionCard
