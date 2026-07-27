# Classification & effort

gitstate tags work items against a shared taxonomy and judges effort from the diff — both **locally**,
with a deterministic fallback so the tool is fully functional with no LLM at all.

---

## Classification

`POST /api/classify` (or `gitstate classify <repo>`) runs each work item through a `Classifier` and
returns a `Classification` per item:

```json
{ "item_id":"…", "category_key":"feature.api", "confidence":0.91,
  "method":"llm_judged", "rationale":"adds a streaming diff endpoint" }
```

- **Category keys** come from the signed [taxonomy](taxonomy.md) plus any local or peer categories.
- **`method`** is `llm_judged` when an LLM endpoint is configured, otherwise `heuristic`.
- **`rationale`** is always present, so a classification is never a black box.

With no `item_ids`, classification processes only the **uncategorized** items and returns just those —
re-running it is cheap and never overwrites a label you corrected. To read what has already been
judged without triggering a pass, use `GET /api/repos/{id}/classifications` and
`GET /api/repos/{id}/effort`; that is what the Classify screen does when you select a repo.

### Two classifiers

| Classifier | When | How |
|---|---|---|
| **LLM** (`LlmClassifier`) | An OpenAI-compatible endpoint is set (`VULOS_LLMUX_URL` / `OPENAI_BASE_URL`). | Sends the item's title, body, labels, and touched paths — **never source code** — and asks for the best taxonomy key with a rationale. |
| **Heuristic** (`HeuristicClassifier`) | No endpoint configured. Always available. | Deterministic keyword/path rules (e.g. a `test/` touch ⇒ `test`, `revert` in the title ⇒ `revert`). Reproducible and offline. |

`default_classifier()` picks the LLM if the environment is set, else the heuristic.

---

## Effort

`POST /api/effort` (or `gitstate effort <repo>`) judges **difficulty**, not line count:

```json
{ "item_id":"…", "difficulty":5.0, "method":"llm_judged",
  "rationale":"cross-module change with a new invariant", "confidence":0.7 }
```

Difficulty sits on a Fibonacci-ish `1.0..=13.0` scale. The LLM reads a `DiffSummary` (shape only —
additions/deletions/files/languages/paths + title/body) and returns a difficulty with a rationale; the
heuristic derives a comparable score from the same shape. Effort feeds the **Effort** contribution
dimension as `effort_points = Σ difficulty`.

> Why not lines? A 500-line generated migration is trivial; a 20-line lock-ordering fix is not. Line
> count rewards volume; difficulty rewards judgment.

### What the judge actually sees today

The `DiffSummary` for a forge work item is built from its metadata — the files it touched, their
paths and languages, and its title and body. Exact per-PR add/delete counts would mean resolving each
PR's base and head against your local worktree; that is on the [roadmap](roadmap.md), not in the code
yet. The consequence is worth being blunt about: **without an LLM endpoint, the heuristic has file and
path signal only, and items with little of it collapse toward difficulty 1.0.** With an endpoint
configured, the model reads titles, bodies and paths and spreads the scale properly. Either way the
`method` and `confidence` fields tell you which one you're looking at.

---

## Local personalization

Every team labels things a little differently. Instead of pooling everyone's corrections into a
central fine-tune, gitstate **learns your box's conventions locally**:

- Correct a label once (`POST /api/classify/feedback`, or the UI) and the choice is recorded on your
  disk.
- The `Personalizer` re-ranks future classifications by those local priors — your conventions win,
  and nothing about them ever leaves the machine.

This is a deliberate decentralization decision: personal categorization is local-only (better privacy,
no cloud), while *alignment* across peers is handled by the signed [taxonomy](taxonomy.md) shipped as
data — not by a shared model or a running service.

Next: [Signed taxonomy](taxonomy.md) · [Configuration](configuration.md)
