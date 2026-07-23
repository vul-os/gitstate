# Forge setup (GitHub &amp; GitLab)

gitstate reads your forge **locally, with your own credentials**. It does not host an OAuth app, does
not broker tokens, and stores nothing on a server. There are two ways it authenticates, in order of
preference.

## 1. The `gh` / `glab` CLIs (recommended)

gitstate shells out to the official forge CLIs, using whatever login they already hold. Install and
log in once:

**GitHub — [`gh`](https://cli.github.com/):**

```bash
gh auth login          # interactive; pick GitHub.com or an Enterprise host
gh auth status         # confirm you're logged in
```

**GitLab — [`glab`](https://gitlab.com/gitlab-org/cli):**

```bash
glab auth login        # interactive; gitlab.com or self-managed
glab auth status
```

gitstate invokes these with `--json` and parses the result. If a required CLI is missing it returns a
typed `Error::ForgeCliMissing` rather than failing silently — install the CLI (or provide a token,
below) and retry.

## 2. A token in the environment (fallback)

If the CLI isn't present but a token is set, gitstate uses the forge REST/GraphQL API directly. It
reads, in order:

- **GitHub:** `GITSTATE_GH_TOKEN`, then `GH_TOKEN`, then `GITHUB_TOKEN`.
- **GitLab:** `GITSTATE_GLAB_TOKEN`, then `GITLAB_TOKEN`.

```bash
export GITSTATE_GH_TOKEN=ghp_xxx        # a fine-grained or classic PAT with repo read scope
export GITSTATE_GLAB_TOKEN=glpat_xxx    # a GitLab PAT with read_api
```

Give the token the **least** scope that lets it read the repos you care about (read-only on
issues/PRs/reviews is enough). gitstate never needs write access.

## Which forge for which repo

When you add a repo by remote URL, gitstate detects the forge from the host (`detect_forge`) and
resolves the `owner/name` slug from the remote. A repo with no remote — or added by local path with
`--no-forge` — is treated as `Local`: git-only, **zero network calls**.

```bash
gitstate repo add https://github.com/vul-os/gitstate   # → forge: github
gitstate repo add https://gitlab.com/group/project     # → forge: gitlab
gitstate repo add ~/code/experiment                    # → forge: local (no network)
```

## What gitstate reads

For a forge scan (`gitstate repo scan <id>`, i.e. without `--no-forge`), gitstate pulls **pull
requests, issues, and reviews** as `WorkItem`s — title, body, state, labels, author, timestamps, and
touched files. It stores these as aggregates in your local database; it never fetches or stores file
contents from the forge, and it never writes back to it.

Use `--since <rfc3339>` to bound how far back a scan reaches:

```bash
gitstate repo scan <id> --since 2026-01-01T00:00:00Z
```

## Privacy

Everything here uses credentials **you** already hold on **your** machine. gitstate adds no
intermediary: your token or `gh`/`glab` session talks to the forge directly, the results land in your
local SQLite database, and nothing is relayed through any gitstate service (there isn't one). If you
never configure a forge, gitstate is a purely local git tool.
