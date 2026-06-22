<!-- title: CLI & Tools | order: 52 | category: Reference | tier: Developers & contributors | summary: migrate, seed, and billsim — the command-line tools beside the server. -->

# CLI & Tools

gitstate ships three command-line tools alongside the main server (`cmd/gitstate`).

## `cmd/migrate` — forward-only migrations

Migrations are Supabase-style: `migrations/YYYYMMDD_NNN_name.sql`, **forward-only, no up/down.** A
rollback is a new forward migration. The runner tracks applied migrations in `schema_migrations`
(version, name, applied_at, checksum) and is environment-aware (loads `.env`, or `.env.dev` in dev).

```bash
go run ./cmd/migrate <command>
```

| Command | Description |
|---|---|
| `new <name>` | Scaffold a new timestamped migration file |
| `up` | Apply all pending migrations |
| `status` | Show applied vs pending migrations |
| `version` | Print the current schema version |
| `reset` | Drop + reapply everything — **dev only**, refused when `GITSTATE_ENV=prod` |

Checksums detect a migration edited after it was applied. Current migrations:

| File | Adds |
|---|---|
| `20260618_001_init.sql` | Base schema (identity, repos/issues/PRs, metrics, billing, platform) + RLS |
| `20260618_002_capacity.sql` | `leave_entries`, `availability`, `time_entries` + RLS |
| `20260618_003_repo_tokens.sql` | `repos.token_encrypted` for at-rest token encryption |

## `cmd/seed` — demo data

Seeds an **"Acme Dev Shop"** demo org: 5 users (including a stakeholder and an agent), 2 repos
(GitHub + GitLab), git-backed and native issues, merged/open PRs, agent commits, computed cycle
times/involvement/estimates, and leave/capacity rows.

```bash
go run ./cmd/seed            # idempotent-ish (ON CONFLICT); safe to re-run
go run ./cmd/seed -reset     # wipe the demo org (slug "acme-dev") first, then re-seed
```

Login: **demo@gitstate.dev / demo1234**.

## `cmd/billsim` — viability simulator

Models gitstate's billing viability across the [plan ladder](/docs/billing) and a 100 / 1 000 /
10 000-org scenario sweep, accounting for conversion, churn, LLM COGS, and USD→ZAR FX. LLM cost is the
dominant lever; the tool flags any tier that runs underwater.

```bash
go run ./cmd/billsim                 # default: 1000 orgs, 8% conversion, 3% churn, FX 18.5
go run ./cmd/billsim -orgs 5000 -conv 12 -churn 2 -fx 19
```

| Flag | Default | Meaning |
|---|---|---|
| `-orgs` | 1000 | total orgs simulated |
| `-conv` | 8.0 | free→paid conversion % |
| `-churn` | 3.0 | monthly paid churn % |
| `-fx` | 18.5 | USD→ZAR rate |
| `-llm-tokens` | 200000 | tokens per paid org/month |
| `-llm-cost` | 5.0 | LLM cost per 1M tokens (USD, blended) |
| `-scenarios` | true | run the 100/1k/10k sweep |

## Build targets (`make`)

| Target | Result |
|---|---|
| `make build` | web build + embed + OSS binary `./gitstate` |
| `make build-ee` | same, with `-tags ee` (Paystack billing + cross-org admin) |
