<!-- title: Agents & MCP | order: 24 | category: Using gitstate | tier: Using gitstate | summary: The AI/agent flywheel — API tokens, the gittrack CLI, the MCP server, and self-calibrating estimates. -->

# Agents & MCP

gitstate is built so AI agents can do real work against your repos — and so every
run they do makes the *next* estimate sharper. This is the **flywheel**:

> An agent **discovers work** (`/api/search`) → **pulls a token-efficient context
> bundle** for the issue (`/api/context/issue/{id}`) → **does it** → **logs the
> run** (`/api/agent-runs`) → the run's predicted-vs-actual outcome **calibrates
> future estimates** for that org. The more the loop runs, the better it forecasts.

There are three ways to drive it: raw HTTP, the [`gittrack`](#the-gittrack-cli)
CLI (pipe-friendly), and the [MCP server](#mcp-server) (native tool UI in
Claude Code, Cursor, …). All three authenticate with the same **API token**.

## API tokens

API tokens are **machine credentials** (`gsk_…`). They are:

- **Scoped** — each token carries an explicit closed set of scopes; a request
  outside them is rejected.
- **Hashed at rest** — only a hash and a short display `prefix` are stored. The
  raw secret is shown **exactly once**, at creation, and is never recoverable.
- **Org-bound** — the org is resolved from the token itself, so machines never
  send `X-Org-ID`.

Tokens are **human-managed**: creating, listing, and revoking them requires a
logged-in **owner or admin** (machines authenticate *with* tokens but can't
manage them).

### Scopes

| Scope              | Unlocks                                                                 |
| ------------------ | ----------------------------------------------------------------------- |
| `read:issues`      | List issues (`GET /api/issues`) and **search** (`GET /api/search`).     |
| `read:context`     | The agent context bundles (`GET /api/context/issue/{id}` and `…/pr/{id}`). |
| `read:prs`         | Reserved for PR reads (a valid scope; not yet gating a dedicated route). |
| `write:agent_runs` | Log agent runs (`POST /api/agent-runs`).                                |
| `write:issues`     | Move issue state (`PATCH /api/issues/{id}`), which writes back to the git platform. |

Grant the least a token needs. A read-only research agent wants
`read:issues` + `read:context`; an agent that also records its work adds
`write:agent_runs`; one that closes issues adds `write:issues`.

### Create a token

In the app: **Settings → API tokens → New token**, pick the scopes (and an
optional expiry), then copy the secret immediately — it is not shown again.

The same thing over HTTP (owner/admin JWT session):

```http
POST /api/tokens
Authorization: Bearer <your-jwt>
X-Org-ID: <org-id>
Content-Type: application/json

{ "name": "claude-code", "scopes": ["read:issues", "read:context", "write:agent_runs"], "expiresInDays": 90 }
```

The `201` response includes `token` (the raw `gsk_…`, once) and `tokenInfo`
(the stored row: `id`, `name`, `prefix`, `scopes`, timestamps). Then point your
tooling at gitstate:

```bash
export GITSTATE_TOKEN=gsk_your_token_here
export GITSTATE_URL=http://localhost:8080   # base URL of your gitstate server
```

Every request is sent with `Authorization: Bearer $GITSTATE_TOKEN`.

## The `gittrack` CLI

`gittrack` is gitstate's pipe-friendly CLI for agents and developer harnesses. It
pulls token-efficient context so an agent can start work on an issue in **one
command**:

```bash
gittrack context 123 --json | your-agent
```

Build it from the repo:

```bash
go build ./cmd/gittrack        # produces ./gittrack; put it on your PATH
```

It reads `GITSTATE_TOKEN` and `GITSTATE_URL` (default
`http://localhost:8080`) from the environment.

### Commands

| Command               | Does                                                                          |
| --------------------- | ----------------------------------------------------------------------------- |
| `context <issue-id>`  | Fetch the full issue context bundle (issue, related PRs, commits, touched paths, similar past issues). |
| `pr <id>`             | Fetch a PR bundle (diff summary, cycle time, calibrated estimate).            |
| `issues`              | List issues for the token's org (table by default).                           |
| `runs`                | List logged agent runs, newest-first.                                         |
| `log-run`             | Record an agent run so it feeds attribution + estimation. Requires `--goal`.  |
| `whoami`              | Validate the token and print the configured URL.                              |

**Global flags** (any command): `--json` (emit raw server JSON, pipe straight
into an LLM), `--url <url>` (override `$GITSTATE_URL`), `--token <gsk_…>`
(override `$GITSTATE_TOKEN`).

**`issues`** filters with `--state <open|in_progress|done|closed>` and caps
output with `--limit N`.

**`runs`** filters with `--repo`, `--pr`, `--issue`, `--agent`, and `--limit N`.

**`log-run`** flags: `--goal "…"` (required), `--repo ID`, `--pr ID`,
`--issue ID`, `--agent NAME` (default `gittrack`), `--branch B`,
`--action accepted|edited|reverted`, `--iterations N`, `--cost F`,
`--additions N`, `--deletions N`, `--files N` (the last three fold into the
diff summary), and `--tests-passed`.

```bash
# Start work, do it, then record the outcome:
gittrack context 123 --json | your-agent
gittrack log-run --goal "Fix auth redirect loop" --issue 123 --pr 88 \
  --agent claude-code --action accepted --iterations 3 --cost 0.42 \
  --additions 24 --deletions 6 --files 2 --tests-passed
```

## MCP server

[MCP (Model Context Protocol)](https://modelcontextprotocol.io) is a standard
that lets an agent host (Claude Code, Cursor, …) call external tools natively.
`gitstate-mcp` is a thin **MCP ↔ HTTP bridge**: every tool call proxies to the
gitstate API with your token, so the token's scopes decide which tools succeed.

Build it: `go build -o gitstate-mcp ./cmd/gitstate-mcp` and put the binary on
your `PATH`.

### Tools

| Tool                 | Required scope     | Backed by                      |
| -------------------- | ------------------ | ------------------------------ |
| `search_issues`      | `read:issues`      | `GET /api/search`              |
| `get_issue`          | `read:context`     | `GET /api/context/issue/{id}`  |
| `get_pr_context`     | `read:context`     | `GET /api/context/pr/{id}`     |
| `list_issues`        | `read:issues`      | `GET /api/issues`              |
| `log_agent_run`      | `write:agent_runs` | `POST /api/agent-runs`         |
| `update_issue_state` | `write:issues`     | `PATCH /api/issues/{id}`       |

A call that the token's scopes don't permit fails **in-band** (a tool result
with `isError: true`) so the host shows the model the message instead of
aborting the session.

### Client configuration

Add the server to your MCP client config (e.g. Claude Code's `~/.claude.json`
or a project `.mcp.json`, or Cursor's `mcp.json`):

```json
{
  "mcpServers": {
    "gitstate": {
      "command": "gitstate-mcp",
      "env": {
        "GITSTATE_TOKEN": "gsk_your_token_here",
        "GITSTATE_URL": "http://localhost:8080"
      }
    }
  }
}
```

If the binary is not on your `PATH`, use its absolute path as `command`.

## The context bundle

`GET /api/context/issue/{id}` (scope `read:context`) returns a single
**token-efficient bundle** an agent can start work from — instead of making it
crawl the API endpoint-by-endpoint. It contains:

- **`issue`** — number, title, body, state, labels, assignee, repo.
- **`estimate`** *(if estimated)* — the calibrated difficulty / predicted vs
  actual time for this issue (see [self-calibrating estimates](#self-calibrating-estimates)).
- **`relatedPRs`** — PRs linked to the issue (state, merged, lead time).
- **`recentCommits`** — recent commits (sha, subject, author, whether agent-authored).
- **`codeAreas`** — the file paths this work touches, so the agent knows *where* to look.
- **`similarIssues`** — past issues that shared labels **and the PR that resolved
  each one** — i.e. worked examples of how this team solved adjacent problems.

That last point is the leverage: the bundle hands the model the issue *plus the
proven prior solution*, in one compact payload, so it spends its context budget on
the work, not on discovery.

`GET /api/context/pr/{id}` is the PR analogue: PR header, diff summary, cycle
time, and the calibrated estimate.

## Search

`GET /api/search?q=<query>` (scope `read:issues`) lets an agent **find work by
meaning** across issues, PRs, and commits. It runs three layers, fused:

1. **Full-text search** — Postgres `websearch_to_tsquery` over a generated
   search vector, ranked by `ts_rank`.
2. **Vector search** — issues are embedded into a pgvector column; the query is
   embedded too and matched by cosine distance (HNSW index). FTS and vector
   rankings are combined with **Reciprocal Rank Fusion**, so morphology/partial
   overlap that exact FTS misses still surfaces. `semantic: true` in the response
   means the vector layer contributed. The default embedder is local and
   deterministic (no external service); a neural embedding provider can be
   swapped in for full synonym-level semantics.
3. **Fuzzy fallback** — when FTS matches nothing (a typo or partial word, e.g.
   `athentication`), it falls back to typo-tolerant `pg_trgm` similarity. The
   response field `fuzzy: true` tells you the fallback produced the hits.

Filter by `type` (a comma-separated subset of `issues,prs,commits`, default
all) and cap with `limit` (default 20, max 100):

```bash
curl -s -H "Authorization: Bearer $GITSTATE_TOKEN" \
  "$GITSTATE_URL/api/search?q=auth+redirect+loop&type=issues,prs&limit=10"
```

Each result is compact and LLM-friendly: `type`, `id`, `number`, `title`,
`snippet` (highlighted excerpt), `rank`, `repoId`, `state`.

## Self-calibrating estimates

This is the back half of the flywheel. Effort estimates aren't static — they
**learn from each org's own history**. gitstate groups work into **cohorts**
(by change type and size) and, for each cohort, tracks **predicted vs actual**
time. Two read endpoints expose what it has learned:

- `GET /api/estimation/accuracy` — per-cohort **MAE** and **bias ratio**
  (`mean(predicted/actual)`): below 1 means estimates run *low*. The UI surfaces
  this as e.g. *"20% low on payments"*.
- `GET /api/estimation/calibration` — the learned **difficulty → time** curve
  (median / p25 / p75 per difficulty bucket per cohort).

**Logging agent runs feeds this loop.** Every run you record with
`log_agent_run` / `gittrack log-run` — its diff size, iterations, and whether a
human accepted, edited, or reverted it — becomes another predicted-vs-actual
data point, so the cohort's forecast and bias correct over time. Closing the
loop is what makes gitstate's estimates *yours*, not a generic guess.

See the **Engineering Health → Calibration** view in the app for the live
accuracy/calibration panels, and [Effort & Estimation](/docs/effort-and-estimation)
for how the underlying difficulty score is computed (an LLM reading the real
diff, never line count).

## See also

- [CLI & Tools](/docs/cli-and-tools) — the other command-line tools beside the server.
- [Effort & Estimation](/docs/effort-and-estimation) — how difficulty is scored.
- [Security](/docs/security) — token hashing, RLS tenancy, and at-rest encryption.
- [API Reference](/docs/api-reference) — every REST endpoint.
