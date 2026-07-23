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
a total order (wall time, then counter, then peer id). The op log is the source of truth; the same ops
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

Local edits and remote merges share **one** path: `upsert_context` / `upsert_category` decompose a full
object into the minimal op set with a freshly-minted `Hlc`, append to the op log, and apply — exactly
what a merged remote op does.

## The sync engine (opt-in)

`gitstate-sync` implements `SyncEngine` (`publish` / `merge` / `export_since` / `status`) as
`CrdtSyncEngine`. It is **excluded from the default workspace** and lives behind the `sync-dmtap`
feature — so a plain `cargo build` pulls no P2P or network dependencies, and the local app is fully
usable without it. Transport rides the shared vulos/DMTAP sync substrate rather than a bespoke stack;
it is signed and hub-less.

```bash
# Build with sync enabled
cargo build -p gitstate-sync --features sync-dmtap

gitstate sync status               # { enabled, peer_id, peers, last_op_hlc }
gitstate sync publish              # broadcast local ops (no-op / disabled when built without the feature)
```

Over HTTP, `GET /api/sync/status` always answers (`{ "enabled": false, … }` when off) and
`POST /api/sync/publish` returns `sync_disabled` when the feature isn't built.

## What never syncs

Derived caches — commits, contributions, project state, work items, effort, classifications — are
**local**. They are re-derivable from your own git and forge, they can contain sensitive detail, and
they are not the point of sharing. Only contexts and categories travel. Your code never leaves your
machine.
