/**
 * planData — canonical 5-tier ladder metadata for the marketing Pricing page.
 *
 * The live API (GET /api/plans) returns the priced numbers per tier:
 *   { key, name, perBuilderUsd, byokPerBuilderUsd, includedLlmUsd,
 *     overageMarkup, builders }
 * where builders=0 means unlimited and perBuilderUsd=null → Enterprise/custom.
 *
 * The API does NOT serialize the per-tier `features` blob, so the human-readable
 * checklist + icon/tagline are derived here, keyed on the canonical ladder
 * (free / starter / pro / scale / enterprise). This file is presentation-only;
 * all prices are read live and formatted through useCurrency().
 */
import {
  Rocket, Sprout, Sparkles, Building2, Gem,
  Check, KeyRound, Eye, Infinity as InfinityIcon, ShieldCheck,
  FileText, Zap, LineChart, GitBranch, Lock, Headset, Server,
} from 'lucide-react'

export const RECOMMENDED_KEY = 'pro'
export const LADDER = ['free', 'starter', 'pro', 'scale', 'enterprise']

// Static fallback mirroring the canonical seed ladder
// (migrations/20260624_002_pricing_ladder.sql) — used only if /api/plans is
// unreachable, so the page still renders the correct tiers.
export const LADDER_FALLBACK = [
  { key: 'free',       name: 'Free',       perBuilderUsd: 0,    byokPerBuilderUsd: null, includedLlmUsd: 0,  overageMarkup: 1.05, builders: 2 },
  { key: 'starter',    name: 'Starter',    perBuilderUsd: 7,    byokPerBuilderUsd: 6,    includedLlmUsd: 1,  overageMarkup: 1.05, builders: 10 },
  { key: 'pro',        name: 'Pro',        perBuilderUsd: 15,   byokPerBuilderUsd: 10,   includedLlmUsd: 5,  overageMarkup: 1.05, builders: 0 },
  { key: 'scale',      name: 'Scale',      perBuilderUsd: 29,   byokPerBuilderUsd: 9,    includedLlmUsd: 20, overageMarkup: 1.05, builders: 0 },
  { key: 'enterprise', name: 'Enterprise', perBuilderUsd: null, byokPerBuilderUsd: null, includedLlmUsd: null, overageMarkup: 1.05, builders: 0 },
]

export const PLAN_META = {
  free:       { icon: Rocket,    tagline: 'Solo builders & weekend projects' },
  starter:    { icon: Sprout,    tagline: 'Small teams shipping their first product' },
  pro:        { icon: Sparkles,  tagline: 'Growing teams that live in their repos' },
  scale:      { icon: Building2, tagline: 'Orgs that need SSO, audit & an SLA' },
  enterprise: { icon: Gem,       tagline: 'Self-host, BYOK & bespoke compliance' },
}

// Per-tier feature checklist. `kind` drives the leading glyph:
//   stakeholder → indigo ∞ · byok → indigo key · default → teal check.
const F = (label, kind = 'default') => ({ label, kind })

export const PLAN_FEATURES = {
  free: [
    F('Up to 2 builder seats'),
    F('Unlimited stakeholders — free', 'stakeholder'),
    F('3 repos · 90-day history'),
    F('Scale-to-zero — sleeps when idle'),
    F('BYOK — bring your own LLM key', 'byok'),
    F('Community support'),
  ],
  starter: [
    F('Up to 10 builder seats'),
    F('Unlimited stakeholders — free', 'stakeholder'),
    F('Unlimited repos & full history'),
    F('$1 / builder managed-AI credit'),
    F('Any model at standard rate · or BYOK', 'byok'),
    F('Email support'),
  ],
  pro: [
    F('Unlimited builder seats'),
    F('Unlimited stakeholders — free', 'stakeholder'),
    F('$5 / builder managed-AI credit'),
    F('Advanced analytics & DORA'),
    F('Priority sync — fastest pipeline'),
    F('PDF invoices'),
    F('Priority email support'),
  ],
  scale: [
    F('Everything in Pro'),
    F('$20 / builder managed-AI credit'),
    F('Google / Microsoft SSO'),
    F('Audit logs & access controls'),
    F('99.9% uptime SLA'),
    F('Dedicated Slack support'),
  ],
  enterprise: [
    F('Everything in Scale'),
    F('Self-host on your own infra'),
    F('BYOK across every provider', 'byok'),
    F('Custom SLA & compliance (SOC2, DPA)'),
    F('Unlimited everything'),
    F('Dedicated CSM & onboarding'),
  ],
}

export const FEATURE_GLYPH = {
  stakeholder: { Icon: InfinityIcon, color: '#818cf8', bg: 'rgba(99,102,241,0.14)' },
  byok:        { Icon: KeyRound,     color: '#818cf8', bg: 'rgba(99,102,241,0.10)' },
  default:     { Icon: Check,        color: '#2DD4BF', bg: 'rgba(45,212,191,0.12)' },
}

// ── Comparison-matrix row spec ────────────────────────────────────────────────
// `vals` are keyed free → starter → pro → scale → enterprise.
// A boolean renders a check / dash; a string renders as a value cell.
export const COMPARE_GROUPS = [
  {
    title: 'Pricing',
    rows: [
      { label: 'Per builder · managed AI', icon: Sparkles,   priceKey: 'managed' },
      { label: 'Per builder · BYOK',       icon: KeyRound,   priceKey: 'byok' },
      { label: 'Builder seats',            icon: GitBranch,  vals: ['Up to 2', 'Up to 10', '∞', '∞', '∞'] },
      { label: 'Stakeholders',             icon: Eye,        vals: ['∞', '∞', '∞', '∞', '∞'], accent: true },
      { label: 'Included AI / builder',    icon: Zap,        creditRow: true },
    ],
  },
  {
    title: 'Platform',
    rows: [
      { label: 'Repositories',       icon: GitBranch, vals: ['3', '∞', '∞', '∞', '∞'] },
      { label: 'History retention',  icon: LineChart, vals: ['90 days', 'Full', 'Full', 'Full', 'Full'] },
      { label: 'Scale-to-zero',      icon: Zap,       vals: [true, true, true, true, true] },
      { label: 'BYOK LLM keys',      icon: KeyRound,  vals: [true, true, true, true, true] },
      { label: 'PDF invoices',       icon: FileText,  vals: [false, false, true, true, true] },
      { label: 'Advanced analytics', icon: LineChart, vals: [false, false, true, true, true] },
      { label: 'Priority sync',      icon: Zap,       vals: [false, false, true, true, true] },
    ],
  },
  {
    title: 'Security & support',
    rows: [
      { label: 'SSO (Google / Microsoft)', icon: Lock,       vals: [false, false, false, true, true] },
      { label: 'Audit logs',               icon: ShieldCheck, vals: [false, false, false, true, true] },
      { label: 'Uptime SLA',               icon: ShieldCheck, vals: [false, false, false, '99.9%', 'Custom'] },
      { label: 'Self-host / on-prem',      icon: Server,     vals: [false, false, false, false, true] },
      { label: 'Support',                  icon: Headset,    vals: ['Community', 'Email', 'Priority', 'Slack', 'CSM'] },
    ],
  },
]

/** Resolve a plan by canonical key from a live or fallback list. */
export function planByKey(plans, key) {
  return (plans ?? []).find(p => p.key === key)
}

/** True for the custom/contact tier. */
export function isEnterprise(plan) {
  if (!plan) return false
  if (plan.key === 'enterprise' || plan.key === 'ent') return true
  if (plan.perBuilderUsd == null) return true
  return false
}
