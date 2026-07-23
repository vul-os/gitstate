# Signed taxonomy

Labels line up across peers not because a server hands them out, but because the category tree ships as
a **static, signed, content-addressed data file**. It is data, not a service.

---

## Why signed data

Personal categorization is local — better privacy, no cloud. But if two peers are going to share
contexts, their category *keys* need to mean the same thing. gitstate solves alignment with a signed
taxonomy rather than a running registry:

- **Signed** — an ed25519 signature over the canonical bytes, verified against a **pinned** public key.
- **Versioned** — semver, so peers can reason about compatibility.
- **Content-addressed** — the `id` is `sha256` of the canonical category bytes; tampering is detectable.

There is nothing to run, nothing to phone home to, and nothing to breach.

---

## Format

```jsonc
{
  "schema": "gitstate.taxonomy/v1",
  "version": "1.0.0",
  "id": "<sha256 hex of canonical bytes>",
  "issued_at": "2026-07-23T00:00:00Z",
  "categories": [
    { "key":"feature",     "label":"Feature",         "parent":null,      "color":"#2dd4bf", "description":"…" },
    { "key":"feature.api", "label":"API / interface", "parent":"feature", "color":"#…",       "description":"…" }
    // …
  ],
  "pubkey": "<ed25519 public key, 64 hex chars>",
  "sig": "<ed25519 signature over canonical bytes, 128 hex chars>"
}
```

`canonical_bytes` serializes `schema, version, issued_at, categories` (each category as
`{key,label,parent,color,description}` in array order) as compact UTF-8 JSON, **without** the signature
fields. That's what gets signed and verified.

---

## Verification (fail-closed)

`Taxonomy::verify` performs three checks:

1. Recompute `sha256(canonical_bytes)` and assert it equals `id`.
2. Assert `pubkey` equals the **pinned** key — `GITSTATE_TAXONOMY_PUBKEY`, falling back to the
   compiled-in `DEFAULT_TAXONOMY_PUBKEY`.
3. Verify `sig` over the canonical bytes with `pubkey`.

Any mismatch raises `TaxonomyUntrusted`. The daemon then **refuses to serve** `source:"taxonomy"`
categories and falls back to your local-only categories. It never silently trusts an unverified tree.

You can inspect or verify a taxonomy from the CLI:

```bash
gitstate taxonomy show --json
gitstate taxonomy verify --file ./my-taxonomy.json
```

Override the embedded default at runtime with `GITSTATE_TAXONOMY_PATH`.

---

## The default tree

A starter tree for software work ships embedded and signed (19 categories):

```
feature            Feature
  feature.api      API / interface
  feature.ui       UI / frontend
  feature.data     Data / storage
feature-flag       Feature flag / rollout
bugfix             Bug fix
hotfix             Hotfix / incident
refactor           Refactor
perf               Performance
security           Security
test               Tests
docs               Documentation
build              Build / packaging
ci                 CI / pipeline
deps               Dependencies
config             Configuration
chore              Chore / housekeeping
revert             Revert
release            Release / versioning
infra              Infrastructure / ops
agent              Agent-authored change
```

Keys are stable; labels and colors are advisory. The `agent` category is part of gitstate being
agent-native — it flags autonomous work explicitly.

> The default doc is signed with a development key for now; production re-signs with the release key.
> The key in use is recorded in the repository's `decisions.md`.

Next: [Classification & effort](classification.md) · [Threat model](threat-model.md)
