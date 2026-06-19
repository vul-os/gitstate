<!-- title: Metrics & Reporting | order: 22 | category: Guides | summary: Cycle time, involvement texture, dashboards, and the safe NL→report path. -->

# Metrics & Reporting

All metrics are derived from git — nothing here is a number a human invented. Reporting layers
dashboards, burndown, and a natural-language query path on top.

## Cycle time (DORA)

For every merged PR, gitstate writes a `cycle_times` row:

| Measure | Definition |
|---|---|
| `lead_time_secs` | first commit → merged (the DORA lead time) |
| `review_secs` | PR created → merged (time in review) |

These power the cycle-time dashboards and calibrate forecasts.

![cycle-time dashboard](/shots/cycle-time.png)

```http
GET /api/metrics/cycle-time
```

## Involvement — texture, not a score

Involvement is stored as **texture**: independent, observable dimensions, each a fact derived from
git/PR history. **No composite score is ever computed or returned** — there is no `score` column in
the data model.

| Dimension | Derived from |
|---|---|
| `features_shipped` | merged PRs authored by the user in the period |
| `reviews_done` | the invisible senior work — reviews on others' merged PRs |
| `areas_owned` | code areas the user owns (by authorship/blame) |
| `active` | true when `features_shipped + areas_owned > 0` |
| `dimensions` | extensible JSONB for extra texture (e.g. additions/deletions) |

The point is to answer *"who is relevant to this area?"* (routing) — never *"who is worth the most?"*
(ranking). See [Concepts → Involvement](/docs/concepts).

![involvement texture](/shots/involvement.png)

```http
GET /api/metrics/involvement
```

## Dashboards & burndown

```http
GET /api/reports/dashboard?synthesize=true
GET /api/reports/burndown?projectId=<id>&days=30
```

The dashboard rolls up project state; with `synthesize=true` and an LLM configured, it adds a
leadership-readable prose summary (best-effort — a missing key is non-fatal, the field is just left
empty). Burndown plots remaining work over a window.

## Natural-language → report

Ask the database a question in plain English; gitstate generates a SQL `SELECT`, runs it safely, and
returns **both the rows and the SQL it ran** so the answer is auditable.

```http
POST /api/reports/query
{ "question": "which PRs took longest to merge last month?" }
```

### The four-layer safety path

The NL→report path is read-only by construction:

1. **Constrained prompt** — the LLM is told to emit a single `SELECT`, no markdown, no semicolons, no
   DDL/CTE-mutations, and may only reference an explicit allowlist of tables.
2. **`validateSQL`** — before execution, the generated SQL is scanned and rejected unless it begins
   with `SELECT`, contains no semicolons, and contains none of a denylist of mutation/DDL keywords
   (`insert`, `update`, `delete`, `drop`, `create`, `alter`, `truncate`, `copy`, `set`, `pg_read_file`,
   `pg_sleep`, `dblink`, …). The whole string is scanned, so a `DELETE` smuggled into a CTE is caught.
3. **Read-only transaction** — execution runs in a `READ ONLY` transaction with a hard 5-second
   `statement_timeout`, bounding the impact of any expensive query.
4. **RLS** — the org id is **never interpolated into the SQL**. Isolation is enforced entirely by
   `db.WithOrg` → `SET LOCAL app.current_org`, so the model is explicitly told *not* to add a
   `WHERE org_id = …` clause. See [Security](/docs/security).

### Queryable tables

The allowlist exposes a curated, read-only view of the schema: `issues`, `pull_requests`, `commits`,
`projects`, `repos`, `effort_estimates`, `cycle_times`, `involvement`, `agent_runs`. Identity,
billing, and audit tables are deliberately **not** reachable from NL queries.
