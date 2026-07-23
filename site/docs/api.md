# HTTP API

The daemon serves a JSON API under `/api` and the React UI at `/` (with SPA fallback). Every non-`/api`
path that isn't a real file falls through to `index.html`. CORS is permissive for `localhost` origins
only.

- **Content type:** `application/json` throughout. Field names are **snake_case**.
- **Success:** the bare object or array (no wrapper) unless noted.
- **Errors:** HTTP 4xx/5xx with body `{ "error":"message", "code":"snake_code" }`.
- **Base URL:** in the desktop app the shell injects `window.__GITSTATE_API__`
  (`http://127.0.0.1:<ephemeral>`); headless, the UI uses same-origin relative paths.

---

## Endpoints

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
| GET | `/api/contexts` | — | `[ Context ]` |
| POST | `/api/contexts` | `NewContext` | `Context` (201) |
| GET | `/api/contexts/{id}` | — | `Context` |
| PATCH | `/api/contexts/{id}` | `ContextPatch` | `Context` |
| DELETE | `/api/contexts/{id}` | — | `{ "deleted":true }` (tombstone) |
| GET | `/api/categories` | — | `[ Category ]` |
| POST | `/api/categories` | `{ "key","label","parent_key"?,"color"? }` | `Category` (201) |
| PATCH | `/api/categories/{id}` | `{ "label"?,"color"?,"parent_key"? }` | `Category` |
| DELETE | `/api/categories/{id}` | — | `{ "deleted":true }` (tombstone) |
| POST | `/api/classify` | `{ "repo_id","item_ids"? }` | `[ Classification ]` |
| POST | `/api/classify/feedback` | `{ "item_id","category_key" }` | `{ "ok":true }` |
| POST | `/api/effort` | `{ "repo_id","item_ids"? }` | `[ EffortEstimate ]` |
| GET | `/api/taxonomy` | — | `Taxonomy` (full signed doc) |
| POST | `/api/taxonomy/verify` | `Taxonomy` | `{ "valid":true,"id":"…" }` |
| GET | `/api/sync/status` | — | `SyncStatus` |
| POST | `/api/sync/publish` | `{ "since"? }` | `{ "published":N }` (404 `sync_disabled` when off) |

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

// Classification
{ "item_id":"…","category_key":"bugfix","confidence":0.82,
  "method":"llm_judged|heuristic","rationale":"…" }

// EffortEstimate
{ "item_id":"…","difficulty":5.0,"method":"llm_judged|heuristic",
  "rationale":"…","confidence":0.7 }

// SyncStatus
{ "enabled":false,"peer_id":"…","peers":0,"last_op_hlc":null }
```

The web client wraps every one of these in typed calls in `web/src/lib/api.js` — no component calls
`fetch` directly.

Next: [Configuration](configuration.md) · [CLI reference](cli.md)
