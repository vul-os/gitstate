# gitstate Design System — "The Ledger"

Dark-first, technical-editorial. Hairline borders, tabular numerals, a mono
voice for metadata, and a teal→indigo accent pair. Light mode is a warm paper
base, not an inverted dark theme.

Everything here is what the app actually ships. The single source of truth for
values is `src/index.css`; this document explains the intent behind them.

---

## Fonts

| Role    | Family                       | Variable         | Import path                                |
| ------- | ---------------------------- | ---------------- | ------------------------------------------ |
| Display | Bricolage Grotesque Variable | Yes (wght axis)  | `@fontsource-variable/bricolage-grotesque` |
| Body    | Hanken Grotesk Variable      | Yes (wght axis)  | `@fontsource-variable/hanken-grotesk`      |
| Mono    | JetBrains Mono               | No (400/500/700) | `@fontsource/jetbrains-mono`               |

```jsx
<h1 className="font-display text-4xl font-semibold">Heading</h1>
<p className="font-body">Body text</p>
<code className="font-mono">metadata label</code>
```

**Mono carries metadata, not code.** Timestamps, counts, axis ticks, stat-card
labels and the `local · on your machine` badge are all mono — it's the voice of
"this came from the ledger", not decoration.

**Numbers are `tabular-nums`.** Any figure that updates in place (stat values,
table columns, axis labels) must not reflow as digits change.

---

## Color tokens

Semantic aliases on `:root` (dark, the default) and `.light`. Components use
these — never a raw hex, and never a `--chart-*` token for text.

### Surfaces and text

| Token           | Dark      | Light     | Purpose                         |
| --------------- | --------- | --------- | ------------------------------- |
| `--bg`          | `#0B1120` | `#f4f2ed` | Page background (warm on light) |
| `--bg-surface`  | `#101827` | `#ffffff` | Card / panel surface            |
| `--bg-surface2` | `#172032` | `#f0ede6` | Recessed well / hover state     |
| `--bg-surface3` | `#1d2b41` | `#e8e4db` | Deepest inset (pills, tracks)   |
| `--border`      | `#1c2a40` | `#e3ddd2` | Hairline divider                |
| `--border2`     | `#22334e` | `#cfc8ba` | Stronger edge / focus border    |
| `--text`        | `#e2e8f0` | `#0f172a` | Primary text                    |
| `--text-dim`    | `#cbd5e1` | `#283548` | Secondary text                  |
| `--text-muted`  | `#94a3b8` | `#51606f` | Muted / metadata                |
| `--text-faint`  | `#8593a8` | `#6b7787` | Small meta — still AA (~4.5:1)  |

`--text-faint` is deliberately not as faint as it sounds: it sits at ~4.5:1 on
every surface so the small mono metadata it carries stays AA-legible.

### Brand

| Token            | Value     |
| ---------------- | --------- |
| `--brand-teal`   | `#2DD4BF` |
| `--brand-indigo` | `#6366F1` |

### Status — reserved

`--ok` `--warn` `--bad` `--info`. These are **reserved for state** and never
reused as a chart series colour. Dark: `#34D399` `#FBBF24` `#F87171` `#38BDF8`.
Light (deepened for paper): `#047857` `#B45309` `#DC2626` `#0284C7`.

### Radii

`--radius-card` 12px · `--radius-btn` 8px · `--radius-badge` 6px

---

## Data visualization

The rules the charts are built on. Breaking one is a bug, not a style choice.

### Categorical — identity

`--chart-1` … `--chart-6`, assigned **in fixed order, never cycled**. Colour
follows the entity, so a filter that changes the series count must not repaint
the survivors.

| Slot | Dark      | Light     |
| ---- | --------- | --------- |
| 1    | `#2DD4BF` | `#0D9488` |
| 2    | `#6366F1` | `#4F46E5` |
| 3    | `#F59E0B` | `#D97706` |
| 4    | `#FB7185` | `#E11D5E` |
| 5    | `#A78BFA` | `#7C3AED` |
| 6    | `#22D3EE` | `#0891B2` |

Light mode is **selected**, not an automatic flip — each slot is deepened so it
holds against the warm off-white without vibrating. Both sets pass adjacent-pair
CVD separation, the normal-vision floor, and 3:1 contrast against their surface.

### Sequential — magnitude

`--heat-0` … `--heat-5`: **one hue, monotonic in lightness**, used only by the
contribution heatmap. Never the categorical palette — the cells mean "more vs
less", not "which". `--heat-0` is the empty-cell well rather than a ramp step,
so "no commits" reads as absence instead of as the lowest bucket.

Direction flips per theme: brighter = more on dark, darker = more on light.

### Chart chrome

`--chart-grid` (recessive horizontal gridlines only — vertical lines compete
with the data), `--chart-axis`, `--chart-dot-ring` (the surface-coloured ring
that separates an overlapping marker from the line beneath it).

### Non-negotiables

- **One y-axis. Never a dual-axis chart.** Two measures of different scale get
  two charts. Multi-series input therefore assumes a shared unit.
- **≥ 2 series ⇒ a legend is always present**, so identity is never colour-alone.
  A single series needs none — the panel title names it.
- **Text wears text tokens**, never the series colour. A coloured swatch beside
  a label carries identity; the label itself stays in ink.
- **Thin marks, recessive axes**, and no number printed on every point.
- Charts are `role="img"` with a real accessible name, and the heatmap ships a
  live-region text readout plus a less→more legend.

### Test hooks

Icon `<svg>`s also carry `role="img"`, so charts expose explicit attributes:

| Hook                                | On                            |
| ----------------------------------- | ----------------------------- |
| `data-chart="trend"` / `="heatmap"` | the chart `<svg>`             |
| `data-series` / `data-points`       | TrendChart `<svg>`            |
| `data-trend-line="<key>"`           | each TrendChart line `<path>` |
| `data-days`                         | Heatmap `<svg>`               |
| `data-day` / `data-level`           | each Heatmap cell `<rect>`    |
| `data-stat` / `data-stat-value`     | StatCard                      |

---

## Theme system

Dark is the default; a first visit with no stored choice follows
`prefers-color-scheme`. An explicit pick persists to `localStorage['gs-theme']`
and wins from then on. The resolved theme applies a `.light` class to `<html>`.

```jsx
import { useTheme } from './lib/theme.jsx'
const { theme, resolved, setTheme, toggle } = useTheme()
// theme:    'system' | 'dark' | 'light'  — the persisted choice
// resolved: 'dark' | 'light'             — what's actually rendered
```

`<ThemeToggle />` (in the TopBar) is the user-facing 2-state light↔dark control.

Dark-mode ambience — grain, gradient mesh, glow — is **muted or disabled** under
`.light`, where it would wash the page out and crush contrast.

---

## UI primitives (`src/components/ui/`)

Imported by direct path (`./components/ui/Card.jsx`), not through a barrel.

| Component          | Purpose                                                                |
| ------------------ | ---------------------------------------------------------------------- |
| `Button`           | `primary` (gradient) / `outline` / `ghost` / `danger`; sizes `xs`–`xl`  |
| `Card`             | Themed surface; `padding` `none`–`xl`, plus `glow` and `hoverable`      |
| `Badge` / `Pill`   | Compact status chips; `Pill` is the fully-rounded variant               |
| `StatCard`         | Headline tile: accent edge, icon chip, big tabular value, delta, spark  |
| `Sparkline`        | Dependency-free inline SVG trend, sized for a StatCard                  |
| `TrendChart`       | Responsive line/area chart with crosshair tooltip and optional legend   |
| `Heatmap`          | Contribution calendar on the sequential ramp; fits its container width  |
| `BarList`          | Ranked, directly-labelled horizontal bars                               |
| `SegmentedControl` | Inline range filter, rendered as a real `radiogroup`                    |

### Page chrome (`src/components/common.jsx`)

`PageHeader`, `Spinner`, `ErrorState` (understands the `daemon_unreachable`
code and tells you to start the daemon), `EmptyState`, `MetricPill`.

### Shell

`AppShell` (sidebar + top bar + routed outlet, with a skip link and an
off-canvas mobile drawer), `Sidebar`, `TopBar`, `ThemeToggle`, `Logo`.

The desktop rail and the mobile drawer are **both always in the DOM**, so they
carry distinct labels — `Primary` and `Primary (mobile)` — rather than two
navigation landmarks with the same name.

---

## Accessibility baseline

- Skip link is the first tab stop and moves focus into `<main>`.
- Focus-visible rings on every interactive element (`--brand-teal`).
- Filters are real `radiogroup`s; the mobile drawer traps focus and closes on
  Escape or scrim click.
- Charts carry accessible names; the heatmap has a text readout so its content
  is not colour-only.
- Contrast: body and small meta text clear AA on every surface in both themes.
