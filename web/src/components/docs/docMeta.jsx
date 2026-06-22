/**
 * Shared doc taxonomy helpers — category ordering, icons, grouping.
 * Used by both DocsHome and the Docs page sidebar so they stay in sync.
 */
import {
  Rocket,
  Compass,
  BookOpen,
  Wrench,
  Server,
  Library,
  HelpCircle,
  GitMerge,
  Gauge,
  Receipt,
  Users,
  Plug,
  Terminal,
  Settings,
  ShieldCheck,
  Network,
  Code2,
  FileText,
  Database,
  Map as MapIcon,
  ListTree,
  Brain,
  CalendarClock,
  Cloud,
  CreditCard,
  GitBranch,
} from 'lucide-react'

// ── Tiers ──────────────────────────────────────────────────────────────────────
// The three top-level audiences, top → bottom. Every category belongs to exactly
// one tier (see CATEGORY_TIER below). Both the docs home and the sidebar render
// these as labelled dividers above their category groups.
export const TIER_USING = 'Using gitstate'
export const TIER_CLOUD = 'Cloud'
export const TIER_DEV = 'Developers & contributors'

export const TIER_ORDER = [TIER_USING, TIER_CLOUD, TIER_DEV]

export const TIER_META = {
  [TIER_USING]: { icon: BookOpen, blurb: 'Run gitstate day to day — concepts, surfaces, and answers.' },
  [TIER_CLOUD]: { icon: Cloud, blurb: 'Billing and everything specific to gitstate Cloud.' },
  [TIER_DEV]: { icon: GitBranch, blurb: 'Self-host, internals, and the API — for OSS contributors.' },
}

// Canonical category order + the icon/accent shown on the home page.
// Ordered top → bottom across all three tiers.
export const CATEGORY_ORDER = [
  // Tier 1 — Using gitstate
  'Getting Started',
  'Concepts',
  'Using gitstate',
  'Help',
  // Tier 2 — Cloud
  'Cloud',
  // Tier 3 — Developers & contributors
  'Self-hosting & operations',
  'Reference',
  'Project',
]

// Maps each category to its tier. Categories not listed fall back to TIER_USING.
export const CATEGORY_TIER = {
  'Getting Started': TIER_USING,
  Concepts: TIER_USING,
  'Using gitstate': TIER_USING,
  Help: TIER_USING,
  Cloud: TIER_CLOUD,
  'Self-hosting & operations': TIER_DEV,
  Reference: TIER_DEV,
  Project: TIER_DEV,
}

export const CATEGORY_META = {
  'Getting Started': { icon: Rocket, blurb: 'Install, run, and connect your first repo.' },
  Concepts: { icon: Compass, blurb: 'The mental model behind derived state.' },
  'Using gitstate': { icon: BookOpen, blurb: 'Task-focused walkthroughs of each surface.' },
  Help: { icon: HelpCircle, blurb: 'Answers to the questions people ask first.' },
  Cloud: { icon: CreditCard, blurb: 'Pricing, invoices, and account billing.' },
  'Self-hosting & operations': { icon: Wrench, blurb: 'Deploy, configure, and secure your own instance.' },
  Reference: { icon: Library, blurb: 'The API, data model, internals, and command-line tools.' },
  Project: { icon: MapIcon, blurb: 'Status, what’s shipped, and where gitstate is heading.' },
}

// Per-slug icon for the doc cards (falls back to a category/keyword guess).
const SLUG_ICONS = {
  overview: Rocket,
  'the-wedge': GitMerge,
  quickstart: Terminal,
  concepts: Compass,
  'derived-state': GitMerge,
  'effort-and-estimation': Brain,
  'agents-and-mcp': Brain,
  'data-model': Database,
  'connecting-repos': Plug,
  integrations: Plug,
  'metrics-and-reporting': Gauge,
  'capacity-and-planning': CalendarClock,
  billing: Receipt,
  configuration: Settings,
  'self-hosting': Server,
  security: ShieldCheck,
  architecture: Network,
  'api-reference': Code2,
  'cli-and-tools': Terminal,
  glossary: ListTree,
  faq: HelpCircle,
  'roadmap-and-status': MapIcon,
}

const KEYWORD_RULES = [
  [/(overview|intro|start|welcome|getting)/, Rocket],
  [/(member|stakeholder|people|team|seat)/, Users],
  [/(state|board|git|status|workflow)/, GitMerge],
  [/(invoice|billing|payment|pricing)/, Receipt],
  [/(connect|integration|github|gitlab|webhook)/, Plug],
  [/(cli|command|terminal|tool)/, Terminal],
  [/(config|setting|admin)/, Settings],
  [/(security|encrypt|auth)/, ShieldCheck],
]

export function iconForSlug(slug = '', title = '') {
  if (SLUG_ICONS[slug]) return SLUG_ICONS[slug]
  const key = `${slug} ${title}`.toLowerCase()
  for (const [re, Icon] of KEYWORD_RULES) {
    if (re.test(key)) return Icon
  }
  return FileText
}

/**
 * Group a flat doc list into ordered { category, docs[] } sections.
 * Categories follow CATEGORY_ORDER; unknown categories are appended.
 * Docs inside a category keep their `order`.
 */
export function groupByCategory(docs = []) {
  const buckets = new Map()
  for (const d of docs) {
    const cat = d.category || 'General'
    if (!buckets.has(cat)) buckets.set(cat, [])
    buckets.get(cat).push(d)
  }
  for (const list of buckets.values()) {
    list.sort((a, b) => (a.order ?? 99) - (b.order ?? 99))
  }
  const ordered = []
  for (const cat of CATEGORY_ORDER) {
    if (buckets.has(cat)) {
      ordered.push({ category: cat, docs: buckets.get(cat) })
      buckets.delete(cat)
    }
  }
  for (const [cat, list] of buckets) {
    ordered.push({ category: cat, docs: list })
  }
  return ordered
}

/**
 * Group a flat doc list into ordered tiers, each holding its category sections:
 *   [{ tier, blurb, icon, sections: [{ category, docs[] }] }]
 * Tiers follow TIER_ORDER; categories within a tier follow CATEGORY_ORDER
 * (already enforced by groupByCategory). Empty tiers are dropped.
 */
export function groupByTier(docs = []) {
  const sections = groupByCategory(docs)
  const byTier = new Map(TIER_ORDER.map((t) => [t, []]))
  for (const s of sections) {
    const tier = CATEGORY_TIER[s.category] ?? TIER_USING
    if (!byTier.has(tier)) byTier.set(tier, [])
    byTier.get(tier).push(s)
  }
  const out = []
  for (const tier of TIER_ORDER) {
    const list = byTier.get(tier) ?? []
    if (list.length === 0) continue
    const meta = TIER_META[tier] ?? {}
    out.push({ tier, blurb: meta.blurb, icon: meta.icon, sections: list })
  }
  // Append any unknown tiers (defensive).
  for (const [tier, list] of byTier) {
    if (TIER_ORDER.includes(tier) || list.length === 0) continue
    out.push({ tier, blurb: '', icon: undefined, sections: list })
  }
  return out
}
