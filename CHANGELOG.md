# Changelog

All notable changes to gitstate are documented in this file.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased] — the transform to local-first

gitstate is being rebuilt from a multi-tenant Go+Postgres+React SaaS into a **standalone, local-first,
peer-to-peer desktop app** in the vulos suite style (`slipscan` / `diwan` / `wede`). The product
essence is unchanged — *derive true project state, effort, contribution, and classification directly
from git and your forge* — but the delivery flips: no multi-tenant server, no Postgres SaaS, no
billing-collection cloud. It runs on your machine.

### Changed
- **Peer replication actually replicates, over a real transport.** Previously the CRDT model, the op
  log and the CLI surface existed and the transport did not: both implementations behind the
  `sync-dmtap` feature returned success and an empty list, while the docs described a signed transport
  riding the shared substrate. Now:
  - an HTTP peer transport to an **operator-supplied URL**, with manual enrolment
    (`gitstate sync identity`, `sync peer add --url --key`, `sync peer list|remove`, `sync run`).
    No discovery, no default endpoint, no broker in any path — an empty peer list means this node
    replicates with nobody;
  - **individually-signed ops** (ed25519 over a domain-separated canonical preimage), verified on
    their own rather than trusted for arriving over an authenticated connection. A node stores and
    relays the *original author's* signature instead of re-signing per hop, so a three-node topology
    does not require trusting the middle node (new nullable `sync_ops.author`/`sig` columns);
  - the clock's tiebreak identity is **bound to the signer**: an op whose `Hlc.peer` is not the
    enrolled id of the signing key is refused, so an enrolled peer cannot steer another node's LWW
    decisions;
  - **single-use signed request tokens** (method + path + timestamp, ±120 s, replay-guarded) and a
    **signed response body** the caller checks against the key it enrolled, so a hijacked address is a
    refusal rather than accepted ops;
  - ops with a clock beyond the skew bound are **refused**, not merged — one op stamped at the end of
    time would otherwise win every field forever;
  - every authentication failure is one `401 unauthenticated` with no detail.
- **`gitstate-sync` no longer depends on another product, and is no longer excluded from the
  workspace.** Its optional `dmtap-sync` **git** dependency on the `envoir` repository is replaced by
  the published substrate crate `kotva-sync` from crates.io, as a **dev** dependency. `Cargo.lock` now
  contains no `git` sources at all. Because a registry dependency needs no network at build time, the
  workspace exclusion is gone — which also means `cargo test --workspace` finally compiles and runs the
  crate's tests, which it never did before. The `sync-dmtap` feature and its two stub transports were
  removed rather than documented.
- **Relicensed AGPL-3.0 → MIT OR Apache-2.0** (at your option), matching every sibling in the vulos
  suite. Root now carries `LICENSE-MIT` and `LICENSE-APACHE`.
- **Dropped the `ee/` commercial Enterprise tier.** With no multi-tenant service to fence off, the
  open-core split (Paystack billing, cross-org super-admin behind the `ee` build tag) no longer
  applies. Its history remains in git.
- Rewrote the project identity — README, roadmap, decisions, docs — to the standalone local-first +
  P2P story.
- CLI commands that take a repo now accept the full id, an unambiguous id prefix, or the slug
  (`gitstate state atlas-api`); an ambiguous reference lists the candidates instead of guessing.
- `gitstate contributions` prints merged display names (agents marked) rather than raw contributor ids.

### Added
- **A cloud-node deployment path.** A configurable bind (`GITSTATE_ADDR`) that now **fails closed**:
  the daemon refuses to bind a non-loopback address while the management API has no authentication,
  because an exposed unauthenticated management API is a total compromise of the node rather than a
  weakness in sync. Set `GITSTATE_ADMIN_TOKEN` (bearer-checked on every management request) or
  explicitly assert an external gate with `GITSTATE_ADMIN_UNAUTHENTICATED=i-accept`. `/health` stays
  open so a load balancer can ask, and the peer endpoints keep their own stronger gate — the admin
  token does not open them. Ships with `deploy/gitstated.service` (hardened systemd unit),
  `deploy/Dockerfile`, and [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) covering TLS (gitstate does not
  terminate it — the reverse proxy is documented, not implied), enrolment, backup, and what the
  deployment does *not* give you.
- **Convergence proved as a property, not asserted.** `crates/gitstate-sync/tests/convergence.rs`
  enumerates **every permutation** of mixed op sets — LWW winners, a peer-id tiebreak, an exact clock
  tie, add/remove on one element, a resurrecting tombstone, two ids for one category key — delivers
  each op twice, and asserts one final *observable* state (including tombstoned documents, which
  `list_contexts` hides). Plus two replicas fed in opposite orders that then exchange logs both ways.
- **The merge algebra is held against the shared engine.**
  `crates/gitstate-sync/tests/shared_engine_parity.rs` links the published `kotva-sync` and proves
  parity for the §4.4 LWW register over all 720 orderings, while **asserting** the two divergences that
  block a drop-in adoption instead of describing them in a comment: gitstate's member set is an
  LWW-element-set rather than §4.3's observed-remove OR-Set (its op envelope has no field for an
  observed add-tag list, so moving is a *wire* change), and its document tombstone resurrects where a
  §4.5 death certificate never does (whose three classes — `redact`, `expires`, `sensitive` — contain
  nothing meaning "the user deleted their working set", which is itself §4.10's answer).
- **Analytics in one round-trip** — `/api/analytics` (heatmap, weekly volume, cycle-time and
  throughput series, work-kind/state/label slices, headline totals) behind the Dashboard and Insights
  screens, with the window anchored on the newest commit rather than wall-clock now.
- **Eng Health** (`/api/health-metrics`) — DORA-flavoured cycle time, a change-failure *text* proxy,
  merge frequency, a labelled deploy proxy, bus factor, review coverage and quality signals.
- **Involvement** (`/api/involvement`) and a cross-repo **contribution rollup**, plus persisted
  six-dimension weights (`GET`/`PUT /api/weights`, `POST /api/weights/reset`) driving a live tuner in
  the UI and `gitstate contributions --weights`.
- **Derived Board** — open / in progress / merged / done, read-only by design.
- **People** — identities merged from commit emails, each `@username` shown with its linked addresses.
- **`gitstate-tracker`** — Jira and Linear import: their public APIs called from your machine with
  your own personal token (stored locally, redacted on read), plus a CSV/JSON export path that makes
  no network calls at all.
- **`gitstate seed --demo`** — a deterministic synthetic dataset, and a Playwright pipeline
  (`web/scripts/screenshots.mjs`) that captures every screen from it in dark and light.
- Read-only `GET /api/repos/{id}/classifications` and `/api/repos/{id}/effort`, so the Classify screen
  shows what has already been judged instead of an empty state.
- **Rust Cargo workspace** (`crates/*`) modeled on `slipscan`: `gitstate-core` (pure domain + traits),
  `gitstate-git` (git2-rs derivation), `gitstate-forge` (`gh`/`glab` + REST/GraphQL), `gitstate-classify`
  (local LLM + signed taxonomy + heuristic fallback + local personalization), `gitstate-store`
  (rusqlite), `gitstate-daemon` (axum: JSON API + SPA), `gitstate-cli` (clap), and `gitstate-sync`
  (P2P CRDT: the merge algebra, individually-signed ops, and the HTTP peer transport).
- **Tauri desktop shell** (`apps/desktop`) that boots the daemon in-process and reuses the existing
  React `web/` UI — the desktop app and the headless daemon serve the *same* JSON API.
- **Signed taxonomy** — a versioned, content-addressed, ed25519-signed category tree shipped as data,
  verified fail-closed against a pinned key.
- New static marketing/docs `site/`, published at `vulos.org/products/gitstate`.

### Fixed
- **An exact clock tie was resolved by arrival order.** When two ops carried the *same* `Hlc` — same
  wall reading, counter and peer — for one field with different values, the incumbent kept the field, so
  whichever arrived first won. Two replicas that received the pair in opposite orders then held
  different values permanently, with nothing anywhere reporting a problem. An honest node cannot mint
  such a pair (`next_hlc` always advances the counter), but a buggy or hostile enrolled peer can, and an
  exposed node has enrolled peers. The tie is now broken on the value, **length-major** — which is the
  order of the shared engine's deterministic-CBOR encoding, so the two agree; a plain byte comparison
  disagrees with it wherever a shorter string sorts higher (`"z"` vs `"aa"`). Applies to context and
  category scalars and to an OR-Set element's `note`. Found by writing the parity test.
- **A scalar clock was being used as a multi-author pull cursor — a silent lost write.** `sync_ops_since`
  filters on each op's own `Hlc`, and one clock cannot summarise "what I already have" across several
  authors. A peer relaying an op from author C at clock 50 and one from author D at clock 100 pushed the
  puller's watermark to 100; C's *next* op, legitimately stamped 60 because C's wall clock trails D's,
  was then filtered out of every future pull — gone for good, with no error anywhere. (The doc comment on
  `sync_ops_since` also claimed the cursor was a watermark over *arrival*, which would have been safe;
  the code always compared clocks.) The sound structure is a per-author version vector, which gitstate
  does not keep, so the puller now asks for the whole log and relies on dedup and idempotence — the same
  trade the push direction already made, for the same reason. `last_pull_hlc` is kept as an
  operator-visible record, not a filter. Guarded by
  `an_op_whose_clock_is_below_the_recorded_high_water_still_arrives`, which fails if the filter is
  reintroduced.
- **A rejected op could advance a peer's pull cursor.** The watermark was the maximum clock over the
  whole received batch, so one forged op at a high clock moved the cursor past an honest op at that
  clock that had not arrived yet — permanently filtering it out of every future pull. The cursor now
  advances only over ops that passed the *same* admission predicate the ingest uses.
- **Our own ops coming back from a peer were counted as rejections.** A peer re-exports everything it
  was given, so most of a steady-state pull is this node's own writes returning; they were refused as
  "author not enrolled", making `rejected` nonzero on every healthy round and destroying the one number
  an operator is meant to read as "somebody sent me something that failed verification". They are now
  admitted under this node's own key — through the same `verify_from` as any peer, so a forged claim on
  our key is still refused — and counted as `skipped`, which is what they are.
- **Remote ops are now replayed into rows, not just logged.** `gitstate_sync::apply_op` appended a
  merged op to `sync_ops` and stopped there, so two peers could exchange a whole history and neither
  one's contexts or categories moved — while `crdt.rs` documented an "op-log replay" that did not
  exist. Ingest is now `Store::merge_sync_op`: it replays the op into the context/category rows under
  the documented rules (per-field LWW by `Hlc`, add-wins OR-Set over the member add/remove clocks,
  whole-document tombstone that a strictly later write resurrects) **and** records it in the log, in
  one transaction. It uses the per-field clock maps, member clocks and tombstone clock the schema
  already carried — no migration. Merging is commutative and idempotent, pinned by a test that
  replays a six-op set in all 720 arrival orders and asserts byte-identical state, then replays it
  again and asserts nothing changed. Re-delivering an op no longer grows the log either.
- **CI covers the in-tree Go tree.** 243 Go files across 37 packages (29 with tests) had no job at
  all. A new `go` job runs build, vet, gofmt and `go test -race` via `scripts/go-gate.sh`, which
  fails closed and asserts coverage counts so it cannot pass by checking nothing.
- **`web/npm ci` was broken** — the lockfile pinned `@emnapi/wasi-threads@1.2.2` against a
  `1.2.3` requirement in the `@tailwindcss/oxide-wasm32-wasi` bundle, which took down every JS job
  including the e2e preflight. Lockfile regenerated (one transitive optional patch bump; no direct
  dependency and no major version changed).
- Documented `cargo build -p gitstate-sync …` in CONTRIBUTING and P2P-CONTEXTS could never have
  worked: the crate was excluded from the workspace, so `-p` could not address it. (Superseded later in
  this same release — the exclusion is gone, so `-p gitstate-sync` is now the correct form after all.)
- **The HLC receive rule.** Ingesting a remote op now folds its clock into the local one
  (`Hlc::observe`), so the next local edit sorts *after* the op it causally follows even when this
  machine's wall clock trails the peer's. Previously the local clock advanced only from its own last
  reading, so an edit made in reply to a peer's write could mint a *lower* clock and lose to it under
  LWW forever. A remote clock more than `HLC_SKEW_MS` (±120 s, the bound the shared DMTAP engine
  applies) ahead of ours is still recorded but not followed. The total order itself is unchanged —
  `(wall_ms, counter, peer)`, the suite's rule — and is now pinned by a permutation test.

### Removed
- SaaS deploy artifacts: `Dockerfile`, `docker-compose.yml`, `deploy/` (fly.toml + systemd unit), and
  the SaaS `config.example.yaml`. gitstate no longer targets a hosted deployment.
- The AGPL `LICENSE` file (replaced by the dual MIT/Apache licenses).
- **The staged port is complete; the legacy Go tree is gone.** `internal/`, `cmd/gittrack`,
  `cmd/gitstate-mcp`, `cmd/migrate`, root `migrations/`, `go.mod`, `go.sum` and
  `scripts/go-gate.sh` were retained byte-for-byte as the reference source while each domain
  (DORA/effort/involvement, then agent_runs+MCP, calibration, context bundle, search/embeddings,
  report+NL→report) was ported to Rust one wave at a time; once nothing depended on a Go package,
  it was deleted (proven by `go build`/`go vet` staying clean at every step). See
  [docs/MIGRATION-NOTES.md](docs/MIGRATION-NOTES.md) and [docs/PORT-PLAN.md](docs/PORT-PLAN.md).
  Zero `.go` files remain in the repo; gitstate is pure Rust + TypeScript.

---

_Prior to the transform, gitstate shipped as a multi-tenant Go+Postgres SaaS with Row-Level Security
tenancy, JWT auth, Paystack billing (EE), and a server-rendered super-admin console. That history is
preserved in the git log._
