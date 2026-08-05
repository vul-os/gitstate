# Security model (local-first)

gitstate is a **standalone, local-first** application: a Rust core over a local SQLite database,
wrapped in a Tauri desktop app or run as a headless daemon. There is **no multi-tenant server, no
hosted account, and no cloud data store**. This reshapes the threat model entirely — the SaaS-era
boundaries (tenant isolation, session tokens, payment webhooks, cross-org admin) are gone, replaced by
a much smaller surface centered on *keeping your data on your machine*.

> The legacy Go server's security properties (RLS tenancy, JWT auth, Paystack webhook verification,
> at-rest token encryption) are documented at the end of this file for provenance. That server's
> staged port is complete and its source is gone ([MIGRATION-NOTES.md](MIGRATION-NOTES.md)); it was
> never part of the standalone app's runtime.

---

## 1. No default network calls

A scan of a local repository touches only your disk (`gitstate repo scan <id> --no-forge` makes **zero**
network calls). Network access happens **only** for actions you explicitly initiate:

- Reading your forge (only when you scan with forge enabled), using **your** `gh`/`glab` login or a
  token you placed in the environment — see [FORGE-SETUP.md](FORGE-SETUP.md).
- Classifying against an **LLM endpoint you configured** (`VULOS_LLMUX_URL` / `OPENAI_BASE_URL`). With
  none set, classification uses a local deterministic heuristic and stays offline.
- Peer-to-peer sync of contexts/categories — and only to peers **you enrolled by hand**, and only when
  you run `gitstate sync run`. There is no discovery, no default endpoint and no background
  replication thread, so a node with no peers enrolled makes no outbound sync connection at all.

The daemon binds `127.0.0.1` by default, and refuses to bind anything wider without an explicit
authentication posture (§5).

## 2. Your credentials stay yours

gitstate registers no OAuth application and brokers no tokens. Forge access reuses the credentials
already on your machine (the `gh`/`glab` session, or a PAT you export). LLM keys are read from the
environment and used only against the endpoint you set. **No forge or LLM secret is persisted to the
database.**

The one exception, stated plainly: **Jira and Linear personal tokens are stored locally**, because
those products have no CLI to borrow a session from. They live in your SQLite file, are returned by
the API only as a masked hint (`…9f2c`), and are transmitted only to the vendor they belong to — no
broker, no OAuth callback, no gitstate server in the path. If you would rather store nothing, the
import screen's export-file path performs no network I/O and needs no credential at all.

## 3. Code never leaves the box

gitstate stores **aggregates, not source**:

- Commit records keep the **first line** of the message only, plus counts (additions, deletions,
  files) — never file contents or diffs.
- Effort judging operates on a `DiffSummary` (counts, languages, touched paths) — the *shape* of a
  change, not its text.
- Derived caches (project state, contributions, work items, classifications) are **local** and never
  synced.

What can be shared peer-to-peer is limited to **contexts** (saved working sets: repos, PR refs, notes,
tags) and **categories**. Your commits, diffs, and contribution data are never published.

## 4. Signed taxonomy, fail-closed

The shared category taxonomy is an ed25519-signed, content-addressed data file. `verify()` recomputes
the content hash, checks the **pinned** public key (`GITSTATE_TAXONOMY_PUBKEY` or the compiled-in
`DEFAULT_TAXONOMY_PUBKEY`), and verifies the signature. On any mismatch → `Error::TaxonomyUntrusted`,
and gitstate refuses to serve taxonomy-sourced categories, falling back to local-only categories. It
never silently trusts an unverified taxonomy. (Full detail: [CLASSIFICATION-AND-TAXONOMY.md](CLASSIFICATION-AND-TAXONOMY.md).)

> The taxonomy currently ships with a **development** signing key; production must re-sign with the
> offline release key ([decisions.md](../decisions.md) T5).

## 5. P2P is hub-less, manually enrolled, and authenticated per op

CRDT sync carries only context/category ops — no code, no diffs, no metrics in the payload — and it is
compiled in unconditionally (there is no build feature to enable, and no dependency on any broker or
reachability service in any code path).

Sync is nonetheless **inert until an operator acts**. There is no discovery of any kind: a peer exists
because somebody ran `gitstate sync peer add --url <url> --key <hex>` with values obtained out of
band. An empty peer list means this node talks to nobody.

The authentication is built for a node on the open internet:

| Property | How |
|---|---|
| A replicated change is verified on its own | every op carries an ed25519 signature over a domain-separated canonical preimage of that one op; the original author's signature is stored and relayed verbatim rather than replaced hop by hop |
| A valid signature is not admission | the signature must verify under the key the operator *enrolled*; a stranger's own valid signature is refused |
| A peer cannot impersonate another peer's clock | an op is refused if its `Hlc.peer` is not the enrolled id of the key that signed it — that field is the final LWW tiebreak |
| Requests cannot be replayed | `Authorization: GitState-Sync <pubkey>.<unix-ms>.<sig>`, signature over method + path + timestamp, ±120 s window, and a used signature refused inside that window |
| The caller authenticates the responder | the responder signs the response body; the caller checks it against the enrolled key, so a hijacked address is a refusal rather than accepted ops |
| A hostile clock cannot capture a field forever | an op more than 120 s in the future is refused at the boundary, not merged |
| Refusals leak nothing | every failure is one `401 unauthenticated`; the reason is logged locally and never returned |

Exposure is fail-closed at startup: the daemon **refuses to bind a non-loopback address** while the
management API has no authentication (`GITSTATE_ADMIN_TOKEN`, or an explicit written opt-out when
something in front of gitstate authenticates instead). gitstate does not terminate TLS — see
[DEPLOYMENT.md](DEPLOYMENT.md) §2 for what the op signatures do and do not give you over cleartext.

## 6. Local data at rest

Your database is a plain SQLite file under the resolved data directory (`gitstate data path`,
overridable with `GITSTATE_DATA_DIR`). It is protected by your operating system's file permissions and
whatever disk encryption you run; gitstate adds no separate encryption layer over it. Because it holds
only aggregates (not source), and lives solely on your machine, the blast radius of the file is your
own device. Back it up by copying the folder.

## Residual items for the standalone app

- [ ] **CORS tightening** — the daemon allows `localhost` origins; confirm no broader origin is
      accepted in any build.
- [ ] **Forge CLI argument hygiene** — ensure repo slugs/refs passed to `gh`/`glab` are validated so a
      crafted slug can't inject flags.
- [ ] **LLM endpoint egress** — document that a user-configured LLM URL receives work-item titles/bodies
      and diff shapes; keep it a conscious, configured choice.
- [ ] **Taxonomy release key** — replace the development signing key before any signed distribution.
- [x] **Unauthenticated API surface** — done. `Daemon::serve` refuses a non-loopback bind unless the
      operator has chosen a posture (`GITSTATE_ADMIN_TOKEN`, or `GITSTATE_ADMIN_UNAUTHENTICATED=i-accept`
      to assert an external gate), and the management API is behind a bearer check when a token is set.
      The peer endpoints have their own, stronger gate and are deliberately not covered by the admin
      token. Tests: `gitstate_daemon::state` (the bind guard) and
      `crates/gitstate-daemon/tests/sync_peers.rs` (the request gates, over real sockets).
- [ ] **Sync transport: no confidentiality without TLS.** The peer transport authenticates both ends
      and every individual op over plain HTTP, but does not encrypt. Context names, notes, tags and
      category labels are readable in flight on an `http://` peer URL. Documented in
      [DEPLOYMENT.md](DEPLOYMENT.md) §2; a terminating reverse proxy is the intended answer, and
      in-protocol encryption has not been attempted.
- [ ] **Sync key rotation is manual** — a node's sync keypair lives in the SQLite file and cannot be
      rotated without re-enrolling with every peer by hand. There is no revocation list: removing an
      enrolment stops future ops but does not retract ops already replicated.
- [ ] **Tracker token storage** — tokens are stored as plaintext in the local SQLite file, protected
      only by file permissions and disk encryption. The node's **sync secret key** now lives in the same
      file and inherits the same exposure. Evaluate OS keychain storage (Keychain / Secret Service /
      DPAPI) for both.

---

## Appendix — legacy SaaS security (provenance only)

The pre-transform Go server (deleted; the staged port is complete, see
[MIGRATION-NOTES.md](MIGRATION-NOTES.md)) enforced: multi-tenant isolation via PostgreSQL Row-Level
Security (`SET LOCAL app.current_org` per request tx, proven by its own `internal/store/rls_test.go`
returning zero cross-org rows); audited super-admin access (`audit_log`, never ambient); env-only
secrets; Paystack webhook HMAC-SHA512 verification with idempotency; per-IP rate limiting; and
AES-256-GCM at-rest encryption of repo tokens. These properties applied only to the legacy code, none
of which shipped in the local-first app described above, and none of which exists in the tree anymore.
