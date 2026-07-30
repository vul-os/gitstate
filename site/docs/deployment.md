# Deployment — running gitstate as a node

gitstate is a local tool first. `gitstate serve` on `127.0.0.1` needs none of this.

This page is about the other case: running a node **on a machine with a routable address**, so another
node can reach it. That is what makes replication practical — a node behind NAT with no forwarded port
cannot be dialled, so one side of a pair needs an address, and that side is the one you deploy. There
is no broker, no relay and no rendezvous service involved.

The canonical, longer version of this page lives in the repository at
[`docs/DEPLOYMENT.md`](https://github.com/vul-os/gitstate/blob/main/docs/DEPLOYMENT.md), alongside the
`deploy/gitstated.service` unit and `deploy/Dockerfile` it refers to.

---

## Three surfaces, three gates

| Surface | Paths | Gate |
|---|---|---|
| **Liveness** | `GET /health` | none — a load balancer must be able to ask |
| **Peer replication** | `GET /api/sync/pull`, `POST /api/sync/push` | a signed, single-use request token from an **enrolled peer**, plus a signature on every individual op |
| **Management + the web app** | everything else under `/api`, and the SPA | `GITSTATE_ADMIN_TOKEN`, or nothing on a loopback bind |

The gates are separate deliberately. A peer needs to replicate and has no business holding your
management token; you need to manage the node and have no business holding a peer's signing key.
Presenting the admin token to `/api/sync/pull` does not work.

## The bind-time guard

The daemon **refuses to start** when you bind a non-loopback address while the management API has no
gate:

```
$ gitstate serve --addr 0.0.0.0
gitstate: invalid value: refusing to bind 0.0.0.0:7473: that address is reachable from outside this
machine, and the management API has no authentication. Set GITSTATE_ADMIN_TOKEN=<secret> …
```

It is a startup failure rather than a per-request check because by the time a request arrives the
mistake is already published. Choose one:

```bash
# gitstate authenticates management requests itself
export GITSTATE_ADMIN_TOKEN="$(openssl rand -hex 32)"

# …or something in front of it already does, and you are saying so
export GITSTATE_ADMIN_UNAUTHENTICATED=i-accept
```

## TLS

**gitstate does not terminate TLS.** It speaks plain HTTP and expects a reverse proxy on anything
internet-reachable. Certificate issuance and renewal are solved better by Caddy/nginx/a load balancer
than by a re-implementation in this binary — but that makes it your step, not an omission you can
ignore.

Without TLS you still get authentication: the caller signs a request token, the responder signs the
response body, and every op carries its author's signature, so nothing can be forged, injected or
altered and neither node can be impersonated. What you do **not** get is confidentiality — context
names, notes, tags and category labels are readable on the path. `http://` peer URLs are accepted (a
WireGuard link or an SSH tunnel is a real deployment); a public peer URL should be `https://`.

```caddyfile
gitstate.example.org {
	reverse_proxy 127.0.0.1:7473
}
```

With a proxy in front, bind gitstate to loopback and let the proxy be the only public listener. Note
that the guard then does not fire — so if the proxy forwards the management paths onward, **you** are
the gate: set the token anyway, or restrict the proxy to `/health` and `/api/sync/*`.

## Enrolling two nodes

Manual in both directions. On each node:

```bash
gitstate sync identity
# peer_id  019fb0c0-2f2a-7e90-9cdd-7a6f6157a777
# pubkey   51c2be5a98c8dd29115182b2d2cf300eb7965fcb79d2a7e4da3c42d452474b7e
```

Hand the pair, plus your URL, to the other operator **out of band**. The public key is what authorises
that node's writes, so the strength of the enrolment is the strength of that channel. Then each side:

```bash
gitstate sync peer add --id <their peer_id> --key <their pubkey> --url https://gitstate.example.org
gitstate sync peer list
gitstate sync run          # one round: push, then pull
```

`sync run` is explicit — there is no background replication thread, so a node never opens an outbound
connection you did not ask for. Put it on a systemd timer if you want it periodic.

### Reading the output

```
019fb0c0…  https://gitstate.example.org  pushed=92 pulled=48 applied=3 skipped=45 rejected=0
```

- `applied` — ops that changed local state.
- `skipped` — verified but lost their merge, or already held. In steady state this is most of them,
  including your own ops coming back: a peer re-exports everything it was given.
- `rejected` — **failed verification**. In a healthy deployment this is `0`. Anything else means a peer
  offered something whose signature did not check out, whose author is not enrolled, whose clock claimed
  another node's identity, or whose clock was implausibly far in the future. It is not noise.

Removing an enrolment stops that key being admitted immediately. It does **not** retract ops already
replicated: history is durable, and a bad write has to be superseded by a later one.

## Data and backup

Everything a node holds is one SQLite file (`gitstate data path`). It contains the contexts, the op
log, the enrolled peer list **and this node's sync secret key** — treat the whole file as a secret, and
copy it with SQLite's own backup rather than `cp`:

```bash
sqlite3 "$(gitstate data path | awk '/db_path/{print $2}')" \
  ".backup '/var/backups/gitstate-$(date -u +%FT%H%M%SZ).db'"
```

Contexts and categories replicate, so a lost node converges back from its peer. **The sync identity
does not**: lose the file and the keypair is gone, the rebuilt node mints a new one, and every peer must
re-enrol it. That is the main reason to back the file up even though the data replicates. Derived caches
(commits, contributions, work items, effort, classifications) never replicate and are re-derivable from
your own git and forge.

## What this does not give you

- **No NAT traversal.** A node without a routable address can dial out and converge fully, but cannot
  be dialled. No hole-punching, no relay. If one is added it will sit behind the same transport seam as
  the direct path, and removing it must leave the direct path working — a node must never *need* a third
  party to sync.
- **No confidentiality without TLS.** See above.
- **No multi-tenancy.** No accounts, no per-user authorization: everyone with the admin token sees
  everything.
- **No replication of anything but contexts and categories.** By design — see
  [Contexts & P2P sync](contexts-sync.md).
- **No automatic re-keying.** Rotating a node's sync key means re-enrolling with every peer by hand.

Next: [Contexts & P2P sync](contexts-sync.md) · [Threat model](threat-model.md)
