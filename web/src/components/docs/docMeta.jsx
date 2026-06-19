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
} from 'lucide-react'

// Canonical category order + the icon/accent shown on the home page.
export const CATEGORY_ORDER = [
  'Getting Started',
  'Concepts',
  'Guides',
  'Operations',
  'Reference',
  'Help',
]

export const CATEGORY_META = {
  'Getting Started': { icon: Rocket, blurb: 'Install, run, and connect your first repo.' },
  Concepts: { icon: Compass, blurb: 'The mental model behind derived state.' },
  Guides: { icon: BookOpen, blurb: 'Task-focused walkthroughs of each surface.' },
  Operations: { icon: Wrench, blurb: 'Configure, deploy, and secure your instance.' },
  Reference: { icon: Library, blurb: 'The API, data model, and command-line tools.' },
  Help: { icon: HelpCircle, blurb: 'Answers, status, and where things are headed.' },
}

// Per-slug icon for the doc cards (falls back to a category/keyword guess).
const SLUG_ICONS = {
  overview: Rocket,
  'the-wedge': GitMerge,
  quickstart: Terminal,
  concepts: Compass,
  'derived-state': GitMerge,
  'effort-and-estimation': Brain,
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
