# Jira & Linear import

gitstate derives state from git. Some of your history, though, lives in a tracker — and there is no
reason a local-first tool should make you abandon it. The Import screen pulls Jira and Linear issues
in **from your machine, with your own credential**, and lands them as ordinary work items.

---

## Why this is still local-first

Both vendors issue **personal** API tokens. So the daemon calls their public HTTPS API directly, the
same posture it already takes with your `gh`/`glab` login:

- **No broker.** There is no gitstate server in the path, no OAuth callback, no redirect through
  anyone's domain.
- **The token stays local.** It is written to your SQLite file and sent only to the vendor it belongs
  to. Reads are redacted: `GET /api/trackers` returns a masked hint (`…9f2c`), never the secret, so
  the UI can show *that* a credential exists without re-exposing it.
- **Nothing is written blind.** Preview fetches without persisting; you see the rows before they land.

If a token isn't an option — an air-gapped machine, a locked-down Jira Server/DC instance, or you'd
simply rather not store a credential — the **export-file path performs no network I/O at all**.

---

## Connecting a tracker

### Jira Cloud

| Field | Value |
|---|---|
| Site URL | `https://your-site.atlassian.net` |
| Account email | the Atlassian account the token belongs to |
| API token | an Atlassian API token (*Security → Create and manage API tokens*) |
| Project key | optional scope, e.g. `ENG` — leave blank to import everything the token can see |

gitstate uses Jira Cloud REST v3 with Basic auth (email + token) against the token-paginated
`/rest/api/3/search/jql` endpoint. Issue state comes from `statusCategory.key` — the one status field
that is stable across custom workflows — mapping `done` → done, `indeterminate` → in progress, and
everything else (including anything a future Jira adds) → open. Rich-text descriptions in Atlassian
Document Format are flattened to plain text.

### Linear

| Field | Value |
|---|---|
| API key | a Linear personal API key (*Settings → API → Personal API keys*) |
| Team key | optional scope, e.g. `ENG` |

Linear's public GraphQL API, paged 100 issues at a time, ordered by `updatedAt`. (Personal keys go in
the `Authorization` header **without** a `Bearer` prefix — the usual reason a hand-rolled Linear
integration returns 400.)

Use **Test connection** before importing; it exercises the credential without writing anything.

---

## Importing

```http
POST /api/import/preview   { "kind":"jira", "limit":50 }        → items, count (nothing written)
POST /api/import/run       { "kind":"jira", "repo_id":"…" }     → { "imported": N }
POST /api/import/file      { "repo_id":"…", "content":"…" }     → { "imported": N }
```

Every import targets a **registered repo** — the daemon 404s rather than writing orphan rows — because
work items belong to a repository in the same way PRs and issues do.

Ids are derived from `(source, key)`, so `jira:ENG-412` is the same row every time. Re-importing
**updates in place** instead of accumulating a duplicate per sync.

### The offline path

Paste or drop a Jira or Linear **export** (Jira exports JSON and CSV; Linear exports CSV). The parser
sniffs JSON vs CSV and Jira vs Linear from the content itself — the `source` hint only disambiguates a
CSV whose headers could belong to either, and clear evidence in the file wins. This path never opens a
socket.

---

## What happens to imported issues

They become `WorkItem`s in exactly the shape the forge clients produce, which means every downstream
feature treats them identically to native ones:

- they appear on the **Board** in the derived column their state maps to;
- **classification** labels them against the [signed taxonomy](taxonomy.md);
- **effort judging** sizes them like any other item;
- they count in the **analytics** rollups and work-kind slices.

What imported issues do *not* do is override git. A Jira ticket marked "Done" is recorded as a Jira
ticket marked Done; the merged pull request is still what tells gitstate the work shipped. When the
two disagree, that disagreement is the interesting signal — and it stays visible rather than being
reconciled away.

Next: [Analytics & health](analytics.md) · [Derivation model](derivation.md) · [HTTP API](api.md)
