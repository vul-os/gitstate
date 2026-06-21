# gitstate e2e tests

End-to-end browser tests for the gitstate portal, built on a tiny custom
runner that uses the **`playwright`** library directly. There is **no
`@playwright/test`** dependency and nothing new to `npm install` — only the
already-present `playwright` package is used.

## Running

1. Start the app (Go API on :8080 + Vite dev server on :5173) with seeded data:

   ```bash
   npm run dev:full
   ```

2. In another terminal, run the suite:

   ```bash
   npm run test:e2e          # runs the core suite in BOTH dark and light themes
   npm run test:e2e:light    # light theme only
   ```

   Or drive the runner directly:

   ```bash
   node tests/runner.mjs --theme=dark      # dark only
   node tests/runner.mjs --theme=light     # light only
   node tests/runner.mjs --headed          # show the browser window
   node tests/runner.mjs --grep=board      # only specs whose name matches "board"
   ```

The runner prints a green/red per-spec summary and exits non-zero if anything
fails, so it drops straight into CI.

## Environment variables

| Var        | Default                  | Purpose                                  |
| ---------- | ------------------------ | ---------------------------------------- |
| `BASE_URL` | `http://localhost:5173`  | Vite web app base URL                    |
| `API_URL`  | `http://localhost:8080`  | Go API base URL (login, share, admin)    |
| `EMAIL`    | `demo@gitstate.dev`      | Login email (also the super-admin)       |
| `PASSWORD` | `demo1234`               | Login password                           |
| `HEADLESS` | `1`                      | Set to `0`/`false` to show the browser   |

## How the runner works (`runner.mjs`)

- Launches **one** chromium instance and reuses it across every spec.
- Each spec gets a **fresh `BrowserContext` + `Page`** so specs are isolated
  (no shared cookies/localStorage between specs).
- Specs register themselves at import time via `test(name, fn, opts)`.
- The theme (`--theme`) is applied per context by seeding `localStorage`
  `gs-theme` in an init script **and** setting Playwright's `colorScheme`
  before any app JS runs. The core suite runs in **both** dark and light;
  data-mutating / single-shot specs are tagged `{ themes: false }` and run once.
- Every spec **fails if the page logs an uncaught error or `console.error`**
  (a small allowlist filters known dev/SSE noise — see `IGNORED_CONSOLE`).
- Shared helpers exposed to specs: `login(page)` (drives the real `/login`
  form), `gotoApp` / `gotoPublic` (navigate + wait for content), `pageHeading`
  (reads the `<main> h1`, since the app's TopBar also renders an `h1`),
  `api` / `apiPatch` (authenticated API calls used to cross-check persistence),
  and `assert` / `assertVisible` (failures name the page + the expectation).

## What's covered (`e2e/*.mjs`)

| Spec | Asserts |
| ---- | ------- |
| `01-auth` | Valid login reaches the dashboard + stores a token; bad creds stay on `/login` with an error and no token. |
| `02-dashboard` | Stat tiles render; the cycle-time-trend `LineChart` has real data points (`data-point-count > 0` + a line path with geometry). |
| `03-board` | All four kanban columns render; **drags a card to another column** and verifies the move **persisted** via the API, then restores it. |
| `04-analytics` | Analytics heatmap (cells) + Commits-over-time chart + leaderboard/data tables; Cycle Time chart + merged-PRs table; Involvement member cards. |
| `05-contribution` | Roster loads; **People / Over time** tabs switch; **moving a weight slider re-orders the ranking**; opening a contributor drawer shows evidence + composite. |
| `06-capacity` | Balances render; the **leave-request form** opens with its controls; the **Approvals** tab shows pending requests. |
| `07-eng-health` | DORA cards render: **change-failure rate, lead time p50, deploy frequency** + the change-failure-over-time chart. |
| `08-invoices` | Invoice list + detail with line items; reads the share token and loads the **public `/i/:token`** invoice in a fresh, **unauthenticated** context (client-facing line items + totals render). |
| `09-planning-import` | Planning capacity timeline + forecast tiles; Import wizard source picker (Jira/Linear) advances to the Connect step. |
| `10-settings` | Calendars / Notifications / Webhooks sections; the notifications digest preview (3 tabs) resolves to real content. |
| `11-marketing` | Landing hero + CTA, `/pricing`, `/compare` (calculator recomputes), `/docs` home — all without page errors. |
| `12-admin` | Logs into the server-rendered admin console at `${API_URL}/admin/login` and asserts Analytics / Users / Orgs render real data. |

## Notes & gotchas baked into the specs

- **Two `h1`s on app pages.** The authed layout's TopBar renders an `h1` with
  the route title; the page's own heading lives in `<main>`. Use `pageHeading`.
- **Board drag** uses real pointer events with an 8px activation move + stepped
  glide so dnd-kit's `PointerSensor` fires; persistence is checked against the
  API (the authoritative source) rather than the optimistic UI.
- **Weight sliders** are controlled range inputs — `fill()` doesn't move them,
  so specs use keyboard `Home`/`End`. The re-rank weights by **Review** (a
  dimension that discriminates the seeded roster); `durability` is 0 for all
  seed members and would tie.
- **Invoice share** requires an issued/`sent` invoice with a `shareToken`
  (the seed's `INV-2026-001` has one).
- **Admin console uses SSE**, so the admin spec never waits for `networkidle`
  (it would hang) — it waits on concrete elements instead.
