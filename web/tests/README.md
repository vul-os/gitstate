# gitstate e2e tests

End-to-end browser tests for the gitstate desktop UI, built on a tiny custom
runner that uses the **`playwright`** library directly. There is **no
`@playwright/test`** dependency and nothing new to `npm install` — only the
already-present `playwright` package is used.

gitstate is a single-user local-first app: the daemon serves the SPA *and* the
JSON API on one origin, and there is no auth, no orgs and no tokens. So the
suite needs exactly one URL and no credentials.

## Running

1. Seed a demo database, build the SPA, and start the daemon:

   ```bash
   cargo run -p gitstate-cli -- seed --demo
   (cd web && npm run build)
   cargo run -p gitstate-cli -- serve --port 8080
   ```

2. In another terminal, run the suite:

   ```bash
   npm run test:e2e          # BOTH dark and light themes
   npm run test:e2e:dark     # dark only
   npm run test:e2e:light    # light only
   ```

   Or drive the runner directly:

   ```bash
   node tests/runner.mjs --headed          # show the browser window
   node tests/runner.mjs --grep=heatmap    # only specs matching "heatmap"
   ```

The runner **preflights** the daemon before launching a browser: if it can't
reach `/health`, or the database has no repos, it fails immediately with the
command you need to run. A suite that silently asserts against an empty app is
worse than no suite.

It prints a green/red per-spec summary and exits non-zero if anything fails, so
it drops straight into CI.

## Environment variables

| Var        | Default                 | Purpose                              |
| ---------- | ----------------------- | ------------------------------------ |
| `BASE_URL` | `http://localhost:8080` | the daemon (serves both SPA and API) |
| `HEADLESS` | `1`                     | `0`/`false` shows the browser window |

## How the runner works (`runner.mjs`)

- Launches **one** chromium instance and reuses it across every spec.
- Each spec gets a **fresh `BrowserContext` + `Page`** so specs are isolated.
- Specs register themselves at import time via `test(name, fn, opts)`.
- The theme is applied per context by seeding `localStorage` `gs-theme` in an
  init script **and** setting Playwright's `colorScheme` before any app JS runs.
  The suite runs in **both** dark and light; tag a spec `{ themes: false }` to
  run it once.
- Every spec **fails if the page logs an uncaught error or `console.error`**
  (a short allowlist filters unavoidable noise — see `IGNORED_CONSOLE`). This is
  what catches the class of bug where a page renders but a component throws
  under it.
- Helpers exposed to specs: `goto` (navigate + wait for content), `pageHeading`
  (reads the `<main> h1`, since the TopBar also renders an `h1`), `api` (plain
  `GET` against the daemon), and `assert` / `assertVisible` /
  `assertCountAtLeast` (failures name the page and the expectation).

## What's covered (`e2e/*.mjs`)

| Spec | Asserts |
| ---- | ------- |
| `01-shell` | Every nav route renders its own heading; the rail links all of them and marks exactly one active; unknown routes hit a not-found page; legacy SaaS paths (`/analytics`, `/projects`, `/home`) still redirect; the theme toggle flips the document theme. |
| `02-dashboard` | Stat cards match `/api/analytics` (not a hardcoded value); the cycle-time line has real multi-vertex geometry; the heatmap draws one cell per day in range across ≥4 ramp steps including the empty well; the leaderboard leads with the API's top contributor; the range filter refetches and shrinks the grid; every repo is listed and links through. |
| `03-insights` | All ten scalar cards render and cross-check against the API; all five chart panels plot real geometry; the two-series throughput chart carries a legend; the contributor table row count matches the API and agents are badged; the repo filter scopes the cards *and* the table; the range filter narrows the window. |
| `04-repos` | The repo list shows every registered repo; repo detail renders project state, per-repo scoped charts (verified against a scoped API query), work items, and all six contribution dimensions. |
| `05-working-sets` | Contexts, categories (the full default taxonomy), the classify repo picker, the signed taxonomy version, and the settings daemon status. |
| `06-analytics-contract` | `/api/analytics` invariants with no browser: a dense, gapless, zero-filled grid with a correctly advancing weekday; totals that sum to the heatmap and satisfy `net = adds − dels` and `p90 ≥ p50`; chronologically ordered series with no negative lead times; per-repo scoping that sums back to the unscoped total; a window anchored on the newest commit rather than wall-clock now; and clamped/empty handling for absurd or unknown input. |
| `07-charts-a11y` | Every chart has `role="img"` plus a non-empty accessible name; the heatmap has a live-region text readout and a less→more ramp legend; the range filter is a labelled radiogroup with exactly one checked option; multi-series charts carry legends; the skip link is the first tab stop and moves focus to `<main>`. |

## Conventions baked into the specs

**Assert against the API, not against magic numbers.** Specs fetch the same
endpoint the page does and compare. That keeps the suite working when the seed
data changes, while still catching a card that renders a stale or hardcoded value.

**Charts carry `data-*` hooks.** Icon `<svg>`s from the icon set *also* have
`role="img"`, so structural selectors alone match them too — an early version of
this suite asserted "chart geometry" against a stopwatch icon's path and passed.
Use the explicit hooks instead:

- `svg[data-chart="trend"]` / `svg[data-chart="heatmap"]`
- `path[data-trend-line="<series key>"]` — assert `d` contains `L` for real geometry
- `svg[data-chart="heatmap"][data-days]`, and `rect[data-day][data-level]` per cell
- `[data-stat="<label>"] [data-stat-value]` for stat-card values

**Stat-card labels are title-case in the DOM.** They only *look* uppercase —
that's `text-transform` in CSS. Address them by `data-stat`, not by rendered text.

**There are two nav landmarks.** The desktop rail and the mobile drawer are both
always in the DOM; they carry distinct labels (`Primary` and `Primary (mobile)`)
so neither the specs nor a screen reader has to guess.
