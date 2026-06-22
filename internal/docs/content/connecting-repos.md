<!-- title: Connecting Repos | order: 20 | category: Using gitstate | tier: Using gitstate | summary: Connect GitHub/GitLab, sync issues & PRs, and auto-progress from git. -->

# Connecting Repos

gitstate connects to **GitHub** and **GitLab** (including self-hosted GitLab). Once a repo is
connected, sync pulls its issues and pull requests into a single unified board and auto-progresses
issues from git.

![connect a repo](/shots/repos.png)

## Connect a repo

Connecting a repo requires a personal access token (or OAuth token) with read access. From the UI,
or directly against the API:

```http
POST /api/repos
X-Org-ID: <org-id>
Authorization: Bearer <access-token>

{
  "platform": "github",                       // "github" | "gitlab"  (required)
  "fullName": "acme/widgets",                 // owner/repo            (required)
  "token":    "<pat or oauth token>",         // required
  "baseURL":  "https://gitlab.example.com"    // optional, self-hosted GitLab only
}
```

The server verifies the token by listing the repo on the platform; a bad token or wrong `fullName`
returns *"repo not found on platform (check token scope and fullName)"*.

### Token scopes

| Platform | Needed access |
|---|---|
| GitHub | `repo` (read) for private repos; public repos need only public read |
| GitLab | `read_api` / `read_repository` |

## Tokens & encryption

Tokens can be stored **encrypted at rest** (AES-256-GCM, keyed by `TOKEN_ENC_KEY`) in
`repos.token_encrypted`, written and read inside an org-scoped (RLS-enforced) transaction. If no
token is persisted, the client re-supplies it on each sync call. See [Security](/docs/security).

## Sync

Trigger a sync to pull the latest state:

```http
POST /api/repos/{id}/sync
{ "token": "<token>", "baseURL": "..." }   // optional if a token is stored
```

### What's pulled

| Pulled | Into | Notes |
|---|---|---|
| **Issues** (open + closed) | `issues` | unified GitHub/GitLab model; merged with native tasks on one board |
| **Pull requests** (open, merged, closed) | `pull_requests` | platform states normalised to `open` / `merged` / `closed` |
| **Commits** | `commits` | walked by the [git engine](/docs/derived-state) on demand |

## Auto-progress (issue ↔ PR linking)

gitstate scans PR titles and bodies for issue references and derives issue state from the linked PR.
The reference pattern matches GitHub/GitLab closing keywords and bare numbers:

```
closes #42   fixes #7   resolves #3   #19
```

The rules:

| Linked PR state | Issue `derived_state` |
|---|---|
| open PR references the issue | `in_progress` |
| merged PR references the issue | `done` |

**Merged always wins** — if one PR is open and another (or the same) is merged for the same issue,
the issue is `done`. gitstate never overwrites a canonical platform state with a weaker derived one;
the derived state is the projection of git activity, layered on top.

This is *derived, not entered* in action: you don't move the card — merging the PR does. For the
mechanics of how commits, diffs, and lead times are read, continue to
[Derived state](/docs/derived-state).
