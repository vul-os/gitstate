# HTTP API

The daemon serves a JSON API under `/api` and the React UI at `/` (with SPA fallback). Every non-`/api`
path that isn't a real file falls through to `index.html`. CORS is permissive for `localhost` origins
only, and the listener binds `127.0.0.1` unless you override `GITSTATE_ADDR`.

- **Content type:** `application/json` throughout. Field names are **snake_case**.
- **Success:** the bare object or array (no wrapper) unless noted.
- **Errors:** HTTP 4xx/5xx with body `{ "error":"message", "code":"snake_code" }`.
- **Base URL:** in the desktop app the shell injects `window.__GITSTATE_API__`
  (`http://127.0.0.1:<ephemeral>`); headless, the UI uses same-origin relative paths.
- **Auth:** none. There are no accounts, sessions or tokens — the security boundary is the loopback
  interface and your OS user (see the [threat model](threat-model.md)).

---

## Repos and derivation

| Method | Path | Body | Returns |
|---|---|---|---|
| GET | `/health` | — | `{ "status":"ok","version":"0.1.0","sync":false,"classifier":"heuristic" }` |
| GET | `/api/repos` | — | `[ Repo ]` |
| POST | `/api/repos` | `{ "path" }` or `{ "remote_url" }` | `Repo` (201) |
| DELETE | `/api/repos/{id}` | — | `{ "deleted":true }` |
| POST | `/api/repos/{id}/scan` | `{ "with_forge":true, "since"? }` | `ScanResult` |
| GET | `/api/repos/{id}/project-state` | — | `ProjectState` |
| GET | `/api/repos/{id}/contributions?from=&to=` | — | `[ Contribution ]` |
| GET | `/api/repos/{id}/work-items?kind=&state=` | — | `[ WorkItem ]` |
| GET | `/api/contributors` | — | `[ Contributor ]` |

`with_forge:false` is the API equivalent of `--no-forge`: git only, zero network calls.

## Analytics and health

| Method | Path | Body | Returns |
|---|---|---|---|
| GET | `/api/analytics?repo_id=&days=&from=&to=` | — | `Analytics` |
| GET | `/api/health-metrics?repo_id=&days=&from=&to=` | — | `EngHealth` |
| GET | `/api/involvement?repo_id=&days=&from=&to=` | — | `Involvement` |
| GET | `/api/contributions/rollup?from=&to=` | — | `[ RollupRow ]` |

All four resolve their window the same way, so the screens agree with each other: the range anchors on
the **newest commit in the store**, not on wall-clock now — a repo last scanned months ago still
renders a populated view. `days` selects a trailing window (default 180) and is ignored when `from` is
given.

`/api/analytics` is the single round-trip behind the dashboard and insights screens:
`{ range, totals, heatmap[], weekly[], contributors[], cycle_time[], throughput[], work_kinds[],
work_states[], labels[] }`.

`/api/health-metrics` returns `{ range, dora, bus_factor, review, quality }` — the Eng Health screen.
`/api/contributions/rollup` accumulates each contributor's rows across every repo into one
six-dimension line using the stored [weights](#contribution-weights).

## Classification and effort

| Method | Path | Body | Returns |
|---|---|---|---|
| POST | `/api/classify` | `{ "repo_id","item_ids"? }` | `[ Classification ]` |
| POST | `/api/classify/feedback` | `{ "item_id","category_key" }` | `{ "ok":true }` |
| POST | `/api/effort` | `{ "repo_id","item_ids"? }` | `[ EffortEstimate ]` |
| GET | `/api/repos/{id}/classifications` | — | `[ Classification ]` |
| GET | `/api/repos/{id}/effort` | — | `[ EffortEstimate ]` |

The two POSTs **judge and write**: `/api/classify` with no `item_ids` processes only the
*uncategorized* items and returns just those; `/api/effort` re-judges every item in the repo. The two
GETs are pure reads of what is already stored — use them to display state without triggering a
classifier pass (this is what the Classify screen does when you pick a repo).

## Contribution weights

| Method | Path | Body | Returns |
|---|---|---|---|
| GET | `/api/weights` | — | `Weights` |
| PUT | `/api/weights` | `Weights` | `Weights` (normalized) |
| POST | `/api/weights/reset` | — | `Weights` (all 1.0) |

Weights must be finite, non-negative and sum to more than zero; the response is normalized to sum to 1
so the UI shows exactly what the composite is computed with.

## Trackers and import

| Method | Path | Body | Returns |
|---|---|---|---|
| GET | `/api/trackers` | — | `[ TrackerView ]` (token masked) |
| PUT | `/api/trackers/{kind}` | `TrackerConfig` | `TrackerView` |
| DELETE | `/api/trackers/{kind}` | — | `{ "deleted":true }` |
| POST | `/api/trackers/{kind}/test` | — | `{ "ok":true }` |
| POST | `/api/import/preview` | `{ "kind","limit"? }` | `{ "items":[ ImportedItem ], "count":N }` |
| POST | `/api/import/run` | `{ "kind","repo_id","limit"? }` | `{ "imported":N,"repo_id":"…" }` |
| POST | `/api/import/file` | `{ "repo_id","content","source"? }` | `{ "imported":N,"repo_id":"…" }` |

`kind` is `jira` or `linear`. A stored token is **never** returned — reads are redacted to a masked
hint (`…9f2c`). `/api/import/file` performs **no network I/O at all**; it parses a pasted CSV/JSON
export. See [Jira & Linear import](import.md).

## Categories, taxonomy, contexts, sync

| Method | Path | Body | Returns |
|---|---|---|---|
| GET | `/api/categories` | — | `[ Category ]` |
| POST | `/api/categories` | `{ "key","label","parent_key"?,"color"? }` | `Category` (201) |
| PATCH | `/api/categories/{id}` | `{ "label"?,"color"?,"parent_key"? }` | `Category` |
| DELETE | `/api/categories/{id}` | — | `{ "deleted":true }` (tombstone) |
| GET | `/api/taxonomy` | — | `Taxonomy` (full signed doc) |
| POST | `/api/taxonomy/verify` | `Taxonomy` | `{ "valid":true,"id":"…" }` |
| GET | `/api/contexts` | — | `[ Context ]` |
| POST | `/api/contexts` | `NewContext` | `Context` (201) |
| GET | `/api/contexts/{id}` | — | `Context` |
| PATCH | `/api/contexts/{id}` | `ContextPatch` | `Context` |
| DELETE | `/api/contexts/{id}` | — | `{ "deleted":true }` (tombstone) |
| GET | `/api/sync/status` | — | `SyncStatus` |
| POST | `/api/sync/publish` | `{ "since"? }` | `{ "published":N }` (404 `sync_disabled` when off) |

Category and context deletes are **tombstones**, not row removals — that is what makes deletion
converge across peers (see [Contexts & P2P sync](contexts-sync.md)).

---

## Selected shapes

```jsonc
// Repo
{ "id":"…","slug":"vul-os/gitstate","path":"/abs","remote_url":"…|null",
  "forge":"github|gitlab|local","default_branch":"main",
  "last_scanned_at":"…|null","added_at":"…" }

// ScanResult
{ "repo_id":"…","head_sha":"…","commits_scanned":1234,"contributors":8,
  "work_items":57,"project_state":{ /* ProjectState */ },"warnings":[] }

// ProjectState
{ "repo_id":"…","head_sha":"…","open_prs":3,"merged_prs":120,"draft_prs":1,
  "open_issues":9,"closed_issues":88,"in_progress":3,"done":208,
  "cycle_time_p50_hours":41.2,"cycle_time_p90_hours":190.0,
  "change_failure_rate":0.07,"computed_at":"…","warnings":[] }

// Contribution
{ "contributor_id":"…","repo_id":"…","from":"…","to":"…",
  "dimensions":{ "shipped":72.0,"review":40.0,"effort":55.5,"quality":88.0,
                 "ownership":33.0,"durability":61.0 },
  "raw":{ "merged_prs":12,"closed_issues":5,"reviews_done":18,"effort_points":34.0,
          "reverts_caused":1,"bug_intros":2,"areas_owned":3,
          "surviving_lines":4200,"authored_lines":6800,
          "human_commits":40,"agent_commits":10 },
  "agent_pct":0.20,"composite":58.4 }

// Weights (normalized on write; defaults are all 1.0)
{ "shipped":0.1667,"review":0.1667,"effort":0.1667,
  "quality":0.1667,"ownership":0.1667,"durability":0.1667 }

// Classification
{ "item_id":"…","category_key":"bugfix","confidence":0.82,
  "method":"llm_judged|heuristic","rationale":"…" }

// EffortEstimate
{ "item_id":"…","difficulty":5.0,"method":"llm_judged|heuristic",
  "rationale":"…","confidence":0.7 }

// TrackerView — the token is a masked hint, never the secret
{ "kind":"jira","configured":true,"base_url":"https://acme.atlassian.net",
  "email":"you@example.com","project":"ENG","token":"…9f2c" }

// SyncStatus
{ "enabled":false,"peer_id":"…","peers":0,"last_op_hlc":null }
```

The web client wraps every one of these in typed calls in `web/src/lib/api.js` — no component calls
`fetch` directly.

Next: [Configuration](configuration.md) · [CLI reference](cli.md) · [Architecture](architecture.md)
