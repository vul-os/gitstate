# Screenshots

The desktop shell and the headless daemon render the **same** React UI over the same JSON API. These
are representative views; the images are on-brand mockups with descriptive alt text (real capture
gallery lands with the first tagged release).

## Project state

![Project state dashboard showing DORA cycle-time cards, pull-request and issue counts, and a six-dimension contribution texture with a locally-classified work-item list](screenshots/dashboard.svg)

DORA cycle time (first-commit → merge), change-failure rate, PR/issue flow, and the six-dimension
contribution texture — all derived from git + forge.

## Involvement

![Involvement view: three contributor cards each showing the six dimensions as bars, merged email identities, and agent-versus-human commit share](screenshots/involvement.svg)

Contribution as texture per contributor, with merged identities and agent share broken out — never a
ranking.

## Classify

![Classification table with categories, confidence bars and rationale, a verified signed-taxonomy panel, and a local-learning panel](screenshots/classify.svg)

Work items tagged locally against a signed taxonomy, each with a rationale and confidence, plus on-box
personalization that learns your conventions.

## Contexts

![Contexts view: saved working sets of repos, pull requests and tags, with a peer-to-peer sync panel showing a node graph of connected peers and CRDT convergence stats](screenshots/contexts.svg)

Saved working sets — repos, PRs, tags, notes — that sync peer-to-peer over CRDT, with no central
server.

## CLI & headless daemon

![Terminal running gitstate repo scan, gitstate state, gitstate classify, and gitstate serve](screenshots/cli.svg)

`gitstate serve` is an always-on peer for servers and scripts — the same API the desktop app talks to,
no UI required.
