# Peer-to-peer contexts &amp; categories

gitstate shares the **smarts, not the code**. The only things that ever cross the network between
peers are **contexts** (saved working sets) and **categories** — synced peer-to-peer as CRDTs, with no
central hub. Your commits, diffs, derived metrics, and contribution data stay local and are never
published.

## What a context is

A **context** is a saved working set — the sharable unit:

```
Context {
  id, name, description,
  repo_ids: [...],                 // OR-Set
  pr_refs: [{ repo_slug, number, note }],  // OR-Set
  notes,                           // LWW text
  tags: [...],                     // OR-Set
  created_at, updated_at,
}
```

Think "the repos + PRs + notes I care about for the Q3 refactor". You can share it with a teammate so
you're both looking at the same working set, and it converges even if you both edit it while offline.

```bash
gitstate context create --name "Q3 refactor" --repo <id> --pr vul-os/gitstate#42 --tag refactor
gitstate context edit <id> --add-tag perf --rm-repo <id2>
gitstate context export <id> --out q3.json     # portable JSON — shareable out-of-band
gitstate context import q3.json
```

Even without the sync feature built, `export`/`import` give you a portable working set you can hand to
anyone.

## The CRDT model

Contexts and categories are conflict-free replicated data types so peers converge with no authority in
the middle. Every operation carries a **hybrid logical clock** (`Hlc { wall_ms, counter, peer }`) with
a total order (wall time, then counter, then peer id). Peer ids are unique, so two nodes never tie and
every replica picks the same winner from the same history — the same rule the shared DMTAP sync engine
orders by (`wall, counter, author`). Ingesting an op also **folds its clock forward** into the local
one, so the next local edit sorts after everything already seen even if this machine's wall clock runs
behind the peer's; a clock more than ±120 s ahead of ours is recorded but not followed, so skew (or a
hostile peer) cannot strand this node in the future. The op log is the source of truth; the same ops
apply whether merged locally or from a remote peer.

`SyncOp` (defined in `gitstate-core` so the store and the sync engine agree) covers:

- Context scalar writes (`name`, `description`, `notes`), tag/repo/PR set membership, and a
  document-level tombstone.
- Category scalar writes (`label`, `color`, `parent_key`) and a tombstone.

### Merge semantics

- **Scalar fields** → **LWW**: the incoming op wins iff its `Hlc` is greater than the stored field's
  last-write clock. Per-field clocks are kept (a `*_field_clocks` side table).
- **Set members** (`tags`, `repo_ids`, `pr_refs`) → **OR-Set, add-wins on tie**: each element tracks
  the max add-clock and max remove-clock; present iff `add_hlc > remove_hlc` (equal ⇒ present). A
  `pr_ref`'s identity is `(repo_slug, number)`; its `note` is an LWW scalar on the element.
- **Deletion** → a document-level tombstone with its own clock. A doc is deleted iff its delete-clock
  is ≥ every field/member clock; a later higher-clock write **resurrects** it (whole-doc LWW).
  Tombstones are retained so late-joining peers still converge.
- **Convergence** → op application is commutative and idempotent; replaying `sync_ops_since` in any
  order yields identical state. `updated_at` is the max `Hlc.wall_ms` rendered as RFC3339.

Local edits and remote merges share the **same rules and the same tables**, reached by two entry
points. A local edit (`Store::upsert_context` / `upsert_category`) writes the object with a
freshly-minted `Hlc` and appends the minimal op set to the log. A remote op arrives at
`Store::merge_sync_op` (via `gitstate_sync::apply_op`), which replays it into those same rows under
the rules above — per-field clocks, member add/remove clocks, tombstone clock — and records it in the
log, in one transaction. Because a local edit stamps every clock it touches with its own `Hlc`, the
two meet in the same comparison: whichever clock is higher wins, whether it came from this machine or
a peer.

`sync_ops_since` returns the log in **arrival order** (`seq`), not clock order. That is deliberate:
merging is commutative and idempotent, so a peer handed the log in any order converges on the same
rows, and re-delivering an op is a no-op for both the rows and the log.

That is a proven property, not a design intention.
`crates/gitstate-sync/tests/convergence.rs` enumerates **every permutation** of a mixed op set —
LWW winners, an exact clock tie, add/remove on one element, a resurrecting tombstone, two ids for one
category key — delivers each op twice, and asserts one final observable state. It also runs two
replicas fed in opposite orders and exchanges their logs both ways.

### Whose algebra is it?

gitstate's merge decisions are taken by **gitstate's own** implementation, over SQLite, in
`gitstate-store`. It is *not* the shared KOTVA merge engine, and this repository does not claim it is.

What it does instead is hold itself against that engine.
`crates/gitstate-sync/tests/shared_engine_parity.rs` links the published `kotva-sync`
(`substrate/SYNC.md` capability ③) as a test dependency, replays the same op streams through both, and:

- **proves parity** for the §4.4 LWW register — every scalar field of a context and a category —
  over all 720 orderings of a set built so each discriminator in turn decides: clock, then peer id,
  then, at an exact tie, the value;
- **records the two divergences** that stop gitstate simply adopting the shared engine's state
  machine, as assertions rather than as prose:
  - the member set is an **LWW-element-set**, not §4.3's observed-remove OR-Set. gitstate's remove
    carries no observed add-tag list — there is no field for one in `SyncOp` — so moving to §4.3 is a
    *wire* change, not a merge change.
  - the document tombstone **resurrects** on a later write, where a §4.5 death certificate never
    does. §4.5's three classes are `redact`, `expires` and `sensitive`; none of them is "the user
    deleted their saved working set", which is itself the answer to §4.10's selection test — a context
    delete is an ordinary reversible edit.

The parity work has already paid for itself once: comparing the two exposed that gitstate resolved an
**exact clock tie** by arrival order, so two replicas that received the same pair of ops in different
orders would hold different values forever with nothing reporting it. gitstate now breaks that tie the
way the shared engine does, on the value, length-major (which is the order of the shared engine's
CBOR encoding — plain byte comparison disagrees with it).

## The sync engine and the peer transport

`gitstate-sync` is an ordinary workspace member, compiled unconditionally. There is **no build feature
to enable**: replication needs none.

> A `sync-dmtap` feature used to live here. It linked a crate out of the `envoir` repository — one
> product importing another to get a merge engine — and wired nothing to it: both transports it
> exposed returned success and an empty list. It has been removed, and the shared engine it was
> reaching for is now consumed from crates.io as `kotva-sync`, in tests, where the claim it supports
> is one that can be checked. `gitstate-sync` was also excluded from the workspace to stop a plain
> `cargo build` fetching that git remote; with a registry dependency that is unnecessary, so it is a
> normal member and `cargo test --workspace` finally runs its tests.

Two halves:

- `CrdtSyncEngine` — the local half. `publish` / `merge` / `export_since` / `status` over the store.
- `SyncNode` + `HttpPeerClient` — the network half. One round with each enrolled peer: push, then pull.

```bash
gitstate sync identity             # this node's peer id + public key
gitstate sync peer add --id <id> --url <url> --key <hex>
gitstate sync peer list
gitstate sync run                  # one round with every enrolled peer
gitstate sync status               # { enabled, peer_id, peers, last_op_hlc }
```

### Discovery is manual, and that is the whole design

A peer exists because an operator typed its URL and its public key. There is no directory, no default
endpoint, no seed list, no mDNS, and no rendezvous or hole-punching broker in **any** path, default or
fallback. An empty peer list means this node replicates with nobody — the correct state for a fresh
install, not a degraded one.

A node with no routable address cannot be *dialled*; that is IP, not a missing feature. Both
directions of a round travel over the connection the caller opens, so a laptop behind NAT converges
fully with a node that has an address. Giving one side an address is what
[DEPLOYMENT.md](DEPLOYMENT.md) is for. If a traversal broker is ever added it goes behind the
`PeerClient` seam as one more implementation, and removing it must leave the direct transport working.

### Authentication

Built for a node on the open internet, because that is what a deployed node is.

- **Every op is individually signed** (ed25519, over a domain-separated canonical preimage of that one
  op) and verified on its own. Ops are *relayed* — a node re-exports changes it did not author — so
  "the connection authenticated" says nothing about who wrote the change inside it. A node stores the
  original author's signature and re-exports it verbatim rather than substituting its own, so a
  three-node topology does not require trusting the middle node.
- **The clock's tiebreak identity is bound to the signer.** An op is refused if its `Hlc.peer` is not
  the enrolled id of the key that signed it — otherwise an enrolled peer could steer every LWW
  decision on another node's behalf.
- **Admission is the enrolled key, not a valid signature.** A stranger's own perfectly valid signature
  is refused: identity is not authorisation.
- **Requests are single-use.** `Authorization: GitState-Sync <pubkey>.<unix-ms>.<sig>`, the signature
  covering the method, the path and the timestamp. The timestamp must be within ±120 s, and inside that
  window a signature already accepted is refused.
- **Mutual.** The responder signs the response body; the caller checks it against the key it enrolled.
  So a wrong DNS answer or a transparent proxy produces a refusal, not accepted ops.
- **Far-future clocks are refused**, not merged and remembered: one op stamped at the end of time would
  win every field forever.
- Every failure is one `401 unauthenticated` with **no detail** — the reason is logged locally, never
  returned, so the endpoint is not a probe for the peer list.

Over HTTP:

| Route | Audience | Gate |
|---|---|---|
| `GET /health` | anyone | none, deliberately |
| `GET /api/sync/pull`, `POST /api/sync/push` | peers | signed request token + per-op signatures |
| `GET /api/sync/identity`, `…/peers`, `POST /api/sync/run`, everything else under `/api` | the operator | `GITSTATE_ADMIN_TOKEN` on a non-loopback bind |

The daemon **refuses to start** on a non-loopback bind while the management API has no gate. See
[DEPLOYMENT.md](DEPLOYMENT.md) §1.

## What never syncs

Derived caches — commits, contributions, project state, work items, effort, classifications — are
**local**. They are re-derivable from your own git and forge, they can contain sensitive detail, and
they are not the point of sharing. Only contexts and categories travel. Your code never leaves your
machine.
