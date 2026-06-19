/**
 * FeatureMatrix — fair, honest gitstate vs Linear / Jira / ClickUp / ZenHub /
 * GitHub Projects comparison.
 *
 * Fairness rules baked into the data:
 *   - gitstate wins its structural categories (git-derived state, per-builder /
 *     free stakeholders, evidence billing, included AI, OSS / self-host,
 *     GitHub+GitLab unified, involvement-as-texture).
 *   - But competitors are honestly credited where they lead or match: Linear's
 *     product polish & ecosystem maturity, Jira's enterprise depth & marketplace,
 *     ClickUp's breadth of views & docs, GitHub's native code proximity, etc.
 *
 * Cells: true = ✓ (Check), 'partial' = ~ (Minus), false = ✗ (X).
 * gitstate column is highlighted; a short note flags this is gitstate's view.
 */
import { Check, Minus, X } from 'lucide-react'

// Tool columns — gitstate first & highlighted.
const TOOLS = [
  { key: 'gitstate', label: 'gitstate', isGs: true },
  { key: 'linear', label: 'Linear' },
  { key: 'jira', label: 'Jira' },
  { key: 'clickup', label: 'ClickUp' },
  { key: 'zenhub', label: 'ZenHub' },
  { key: 'github', label: 'GitHub' },
]

// Grouped rows. Each value: true | 'partial' | false.
const GROUPS = [
  {
    title: 'Where gitstate is built different',
    rows: [
      {
        feature: 'State derived from git',
        detail: 'Merged = done, open PR = in progress — no manual status fields.',
        gitstate: true, linear: false, jira: false, clickup: false, zenhub: 'partial', github: 'partial',
      },
      {
        feature: 'Effort read from the diff (LLM)',
        detail: 'Semantic difficulty judged from the actual diff, not story-point poker.',
        gitstate: true, linear: false, jira: false, clickup: false, zenhub: false, github: false,
      },
      {
        feature: 'Evidence-based invoicing',
        detail: 'Every invoice line links to a commit SHA or PR; gaps flagged, never fabricated.',
        gitstate: true, linear: false, jira: false, clickup: false, zenhub: false, github: false,
      },
      {
        feature: 'Involvement as texture, not a score',
        detail: 'Multi-dimensional contribution view — never a single number in pay formulas.',
        gitstate: true, linear: false, jira: false, clickup: false, zenhub: false, github: false,
      },
    ],
  },
  {
    title: 'Pricing & access model',
    rows: [
      {
        feature: 'Per-builder pricing',
        detail: 'Pay for people who ship, not everyone with a login.',
        gitstate: true, linear: false, jira: false, clickup: false, zenhub: false, github: false,
      },
      {
        feature: 'Free unlimited stakeholders',
        detail: 'Clients, PMs and execs view without driving up your bill.',
        gitstate: true, linear: false, jira: false, clickup: 'partial', zenhub: false, github: 'partial',
      },
      {
        feature: 'AI included (no per-seat tax)',
        detail: 'Managed LLM bundled. Linear includes AI free; ClickUp & GitHub charge per seat.',
        gitstate: true, linear: true, jira: 'partial', clickup: false, zenhub: 'partial', github: false,
      },
      {
        feature: 'Open source + self-host',
        detail: 'AGPL-3.0 core — run it on your own infra, fork it, own your data.',
        gitstate: true, linear: false, jira: false, clickup: false, zenhub: false, github: false,
      },
    ],
  },
  {
    title: 'Git & platform integration',
    rows: [
      {
        feature: 'GitHub + GitLab unified',
        detail: 'One board spanning both platforms with two-way sync.',
        gitstate: true, linear: 'partial', jira: 'partial', clickup: 'partial', zenhub: false, github: false,
      },
      {
        feature: 'Native code proximity',
        detail: 'Lives next to the code & PRs. GitHub is unbeatable here — it is the repo.',
        gitstate: true, linear: 'partial', jira: 'partial', clickup: false, zenhub: true, github: true,
      },
      {
        feature: 'NL → report / queryable',
        detail: 'Ask “PRs by contributor last quarter” in plain language.',
        gitstate: true, linear: 'partial', jira: 'partial', clickup: 'partial', zenhub: false, github: 'partial',
      },
    ],
  },
  {
    title: 'Where competitors lead or match (honestly)',
    honest: true,
    rows: [
      {
        feature: 'Product polish & speed',
        detail: 'Linear set the bar for craft and keyboard-first UX. gitstate is younger here.',
        gitstate: 'partial', linear: true, jira: 'partial', clickup: 'partial', zenhub: 'partial', github: 'partial',
      },
      {
        feature: 'Enterprise depth & governance',
        detail: 'Jira’s permissions, audit, compliance and admin tooling are deep and battle-tested.',
        gitstate: 'partial', linear: 'partial', jira: true, clickup: 'partial', zenhub: false, github: true,
      },
      {
        feature: 'Marketplace & integrations breadth',
        detail: 'Jira’s ecosystem is vast; gitstate’s is young (and the add-ons inflate Jira’s real cost).',
        gitstate: 'partial', linear: 'partial', jira: true, clickup: 'partial', zenhub: false, github: 'partial',
      },
      {
        feature: 'Breadth of views, docs & wiki',
        detail: 'ClickUp packs docs, whiteboards and dozens of view types into one app.',
        gitstate: 'partial', linear: 'partial', jira: 'partial', clickup: true, zenhub: false, github: 'partial',
      },
      {
        feature: 'Ecosystem & community maturity',
        detail: 'Years of templates, plugins and StackOverflow answers. gitstate is building this.',
        gitstate: 'partial', linear: true, jira: true, clickup: 'partial', zenhub: 'partial', github: true,
      },
    ],
  },
]

function Cell({ value, isGs }) {
  if (value === true) {
    return (
      <span
        className={[
          'inline-flex items-center justify-center w-6.5 h-6.5 rounded-full',
          isGs
            ? 'text-[#2DD4BF]'
            : 'text-green-400/90',
        ].join(' ')}
        style={isGs ? { width: 26, height: 26, background: 'rgba(45,212,191,0.14)', boxShadow: '0 0 12px rgba(45,212,191,0.3)' } : { width: 26, height: 26, background: 'rgba(34,197,94,0.10)' }}
        aria-label="Full support"
      >
        <Check size={14} strokeWidth={3} />
      </span>
    )
  }
  if (value === 'partial') {
    return (
      <span
        className="inline-flex items-center justify-center rounded-full text-yellow-400/85"
        style={{ width: 26, height: 26, background: 'rgba(234,179,8,0.09)' }}
        aria-label="Partial"
      >
        <Minus size={14} strokeWidth={3} />
      </span>
    )
  }
  return (
    <span
      className="inline-flex items-center justify-center rounded-full text-[var(--text-faint)]/45"
      style={{ width: 26, height: 26 }}
      aria-label="Not supported"
    >
      <X size={13} strokeWidth={2.5} />
    </span>
  )
}

export default function FeatureMatrix() {
  return (
    <div className="w-full">
      <div className="overflow-x-auto rounded-2xl border border-[var(--border2)] bg-[var(--bg-surface)]">
        <table className="w-full border-collapse min-w-[760px]" style={{ tableLayout: 'fixed' }}>
          <colgroup>
            <col style={{ width: '34%' }} />
            {TOOLS.map((t) => (
              <col key={t.key} style={{ width: `${66 / TOOLS.length}%` }} />
            ))}
          </colgroup>

          {/* Sticky-ish header */}
          <thead>
            <tr className="border-b border-[var(--border)]">
              <th className="text-left p-4 align-bottom">
                <span className="text-[11px] font-mono font-medium uppercase tracking-widest text-[var(--text-faint)]">
                  capability
                </span>
              </th>
              {TOOLS.map((t) => (
                <th key={t.key} className="p-3 text-center align-bottom">
                  {t.isGs ? (
                    <span
                      className="inline-flex font-mono text-[13px] font-bold px-3 py-1.5 rounded-md"
                      style={{
                        background: 'linear-gradient(135deg, rgba(45,212,191,0.18), rgba(99,102,241,0.16))',
                        border: '1px solid rgba(45,212,191,0.4)',
                        color: '#2DD4BF',
                        boxShadow: '0 0 18px rgba(45,212,191,0.16)',
                      }}
                    >
                      {t.label}
                    </span>
                  ) : (
                    <span className="font-mono text-xs font-medium text-[var(--text-muted)]">{t.label}</span>
                  )}
                </th>
              ))}
            </tr>
          </thead>

          <tbody>
            {GROUPS.map((group) => (
              <GroupRows key={group.title} group={group} />
            ))}
          </tbody>

          {/* Legend */}
          <tfoot>
            <tr className="border-t border-[var(--border)]">
              <td colSpan={TOOLS.length + 1} className="p-4">
                <div className="flex flex-wrap items-center gap-5 text-[11px] font-mono text-[var(--text-faint)]">
                  <span className="flex items-center gap-1.5">
                    <Check size={13} strokeWidth={3} className="text-green-400/90" /> full
                  </span>
                  <span className="flex items-center gap-1.5">
                    <Minus size={13} strokeWidth={3} className="text-yellow-400/85" /> partial / plugin
                  </span>
                  <span className="flex items-center gap-1.5">
                    <X size={13} strokeWidth={2.5} className="text-[var(--text-faint)]/55" /> not supported
                  </span>
                </div>
              </td>
            </tr>
          </tfoot>
        </table>
      </div>

      {/* Honest note */}
      <p className="mt-4 text-[12px] text-[var(--text-faint)] leading-relaxed max-w-3xl">
        <span className="font-mono text-[var(--text-muted)]">A note on fairness —</span> this is gitstate&apos;s
        own view, and these competitors are genuinely excellent tools with real strengths we respect: Linear&apos;s
        product craft, Jira&apos;s enterprise depth and marketplace, ClickUp&apos;s breadth, GitHub&apos;s code
        proximity. We mark partial / behind honestly wherever they lead or match. The point isn&apos;t that
        gitstate checks more boxes — it&apos;s that the top rows describe categories that only exist once git is
        the source of truth.
      </p>
    </div>
  )
}

function GroupRows({ group }) {
  return (
    <>
      <tr>
        <td colSpan={TOOLS.length + 1} className="pt-6 pb-2 px-4">
          <div className="flex items-center gap-2.5">
            <span
              className="text-[10px] font-mono font-semibold uppercase tracking-widest"
              style={{ color: group.honest ? 'var(--text-muted)' : '#2DD4BF' }}
            >
              {group.title}
            </span>
            <span
              className="h-px flex-1"
              style={{
                background: group.honest
                  ? 'var(--border)'
                  : 'linear-gradient(to right, rgba(45,212,191,0.4), var(--border) 60%, transparent)',
              }}
            />
          </div>
        </td>
      </tr>
      {group.rows.map((row) => (
        <tr key={row.feature} className="group border-b border-[var(--border)] last:border-0">
          <td className="py-3 px-4 align-top group-hover:bg-[var(--bg-surface2)]/40 transition-colors">
            <span className="block text-[13px] font-medium text-[var(--text-dim)] font-display leading-snug">
              {row.feature}
            </span>
            <span className="block text-[11px] text-[var(--text-faint)] leading-relaxed mt-0.5 hidden sm:block">
              {row.detail}
            </span>
          </td>
          {TOOLS.map((t) => (
            <td
              key={t.key}
              className={[
                'py-3 px-2 text-center align-middle transition-colors',
                t.isGs
                  ? 'bg-[#2DD4BF]/[0.04] group-hover:bg-[#2DD4BF]/[0.07]'
                  : 'group-hover:bg-[var(--bg-surface2)]/40',
              ].join(' ')}
            >
              <Cell value={row[t.key]} isGs={t.isGs} />
            </td>
          ))}
        </tr>
      ))}
    </>
  )
}
