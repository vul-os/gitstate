# Classification &amp; taxonomy

gitstate classifies work items (features, bugfixes, refactors, security work, agent-authored changes,
…) and judges effort. Two design decisions keep this **honest** and **decentralized** at once.

## Classification is local-only

Classification and effort judging run through `gitstate-classify`, which offers two implementations of
the `Classifier` trait:

- **`LlmClassifier`** — talks to an LLM endpoint **you** configure via the environment:
  `VULOS_LLMUX_URL` or `OPENAI_BASE_URL` (plus the matching `*_API_KEY` and an optional
  `GITSTATE_CLASSIFY_MODEL`). This can be a local llmux instance, a local model server, or any
  OpenAI-compatible endpoint you point it at. gitstate **never** hosts a model for you.
- **`HeuristicClassifier`** — a deterministic keyword/path ruleset that is **always available**. When
  no LLM is configured, gitstate falls back to it and everything still works, fully offline.

`default_classifier()` picks the LLM if the env is set, otherwise the heuristic. Effort is judged from
a `DiffSummary` — the *shape* of a change (additions, deletions, files, languages, touched paths,
title, body) — never raw source, and the result is a difficulty on a ~1–13 fibonacci-ish scale, not a
line count.

### Effort, not line count

The `EffortEstimate` carries a `difficulty`, a `method` (`LlmJudged` | `Heuristic`), a `rationale`,
and a `confidence`. A large mechanical rename and a small subtle concurrency fix are *not* the same
effort, and lines-changed can't tell them apart — so gitstate judges the change, and always shows the
rationale as evidence.

## Local personalization (replaces pooled fine-tuning)

When you correct a classification, gitstate records the choice locally
(`Store::record_feedback` → the `classify_feedback` table). A `Personalizer` re-ranks future
classifications by these local priors, so each box learns **its own** conventions — one team's
`chore` is another's `build`. Nothing about your work is ever pooled or uploaded; there is no shared
model to poison or leak into.

```
classify → base predictions → Personalizer.adjust(local priors) → ranked result
you correct → record() → next run reflects it
```

## Label alignment: a signed data file, not a service

For peers to agree on what a category *means* (so a shared context's `feature.api` label lines up),
gitstate ships a **taxonomy** — but as **static signed data**, never a running service.

The taxonomy is:

- **Versioned** — a semver `version` and an `issued_at`.
- **Content-addressed** — `id = sha256(canonical_bytes)`, where `canonical_bytes` is a deterministic
  serialization of `(schema, version, issued_at, categories)`.
- **Signed** — an **ed25519** signature (`ed25519-dalek`) over `canonical_bytes`, with the signer's
  public key embedded.

It is embedded in `gitstate-classify` as `default_taxonomy.json` (compiled via `include_str!`) and can
be overridden at runtime with `GITSTATE_TAXONOMY_PATH`.

### Verification is fail-closed

`Taxonomy::verify(pinned_pubkey_hex)`:

1. Recompute `sha256(canonical_bytes)` and assert it equals `id`.
2. Assert `pubkey` equals the **pinned** key — from `GITSTATE_TAXONOMY_PUBKEY`, falling back to the
   compiled-in `DEFAULT_TAXONOMY_PUBKEY`.
3. Verify `sig` over `canonical_bytes` with `pubkey`.

Any mismatch → `Error::TaxonomyUntrusted`, and the daemon then refuses to serve `source: "taxonomy"`
categories and falls back to **local-only** categories. gitstate never silently trusts an unverified
taxonomy.

> **Dev key note.** The taxonomy currently ships signed with a **development** ed25519 keypair
> generated during the transform, with its public key pinned as `DEFAULT_TAXONOMY_PUBKEY`. This proves
> the fail-closed path end-to-end. Production must re-sign the default doc with the offline release key
> and update the pinned constant. (Recorded in [decisions.md](../decisions.md), T5.)

### The default category tree

19 starter categories for software work — `feature` (with children `feature.api`, `feature.ui`,
`feature.data`), `feature-flag`, `bugfix`, `hotfix`, `refactor`, `perf`, `security`, `test`, `docs`,
`build`, `ci`, `deps`, `config`, `chore`, `revert`, `release`, `infra`, and `agent` (agent-native:
flags autonomous work). Keys are stable; labels and colors are advisory.

Categories carry a **provenance** (`source`): `Taxonomy` (from the signed doc), `Local` (you made it),
or `Peer` (arrived via P2P sync). Local and peer categories are ordinary CRDT-backed records
([P2P-CONTEXTS.md](P2P-CONTEXTS.md)); taxonomy categories additionally record the `taxonomy_version`
they came from.

## Inspecting it

```bash
gitstate taxonomy show            # print the signed doc (--json for raw)
gitstate taxonomy verify          # verify the embedded doc against the pinned key
gitstate taxonomy verify --file some-taxonomy.json
```

Over HTTP: `GET /api/taxonomy` returns the full signed doc; `POST /api/taxonomy/verify` verifies a
submitted one.

## What is NOT built

Cross-population classification signals — "trending categories", "others tagged this the same way",
"repos similar to yours" — require a view of strangers you'll never meet, so they are **not** part of
a local git tool. They are left only as a dormant, optional coordinator seam
([ROADMAP.md](../ROADMAP.md), *Later / optional*). There is no anti-spam/sybil tier and no pooled
fine-tuning; both are taxes on that unbuilt discovery layer.
