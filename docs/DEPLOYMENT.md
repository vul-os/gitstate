# Running gitstate as a node

gitstate is a local tool first: `gitstate serve` on `127.0.0.1` is the normal way to use it, and
nothing in this document is required for that.

This document is about the other case — running a gitstate node **on a machine with a routable
address**, so that another node can reach it. That is what makes peer replication practical: a node
behind NAT with no forwarded port cannot be dialled, so one side of a pair needs an address, and the
side with the address is the one you deploy.

There is no broker, no relay and no rendezvous service anywhere in this. A node is reached by its
URL, typed by an operator.

---

## 1. What is exposed, and what protects it

A gitstate daemon serves three different things on one port, with three different gates.

| Surface | Paths | Gate |
|---|---|---|
| **Liveness** | `GET /health` | none — a load balancer must be able to ask |
| **Peer replication** | `GET /api/sync/pull`, `POST /api/sync/push` | a signed request token from an **enrolled peer**, plus a signature on every individual op |
| **Management + the web app** | everything else under `/api`, and the SPA | `GITSTATE_ADMIN_TOKEN`, or nothing on a loopback bind |

The two gates are separate on purpose. A peer needs to replicate and has no business holding your
management token; you need to manage the node and have no business holding a peer's signing key.
Presenting the admin token to `/api/sync/pull` does not work, and a peer's token does not open the
management API.

### The bind-time guard

The daemon **refuses to start** if you bind a non-loopback address while the management API has no
gate:

```
$ gitstate serve --addr 0.0.0.0
gitstate: invalid value: refusing to bind 0.0.0.0:7473: that address is reachable from outside this
machine, and the management API has no authentication. Set GITSTATE_ADMIN_TOKEN=<secret> …
```

This is a startup failure rather than a request-time check because by the time a request arrives the
mistake has already been published. Pick one of:

```bash
# (a) gitstate authenticates management requests itself.
export GITSTATE_ADMIN_TOKEN="$(openssl rand -hex 32)"

# (b) Something in front of gitstate already authenticates them — a reverse proxy doing mTLS or
#     OIDC, a private network, a WireGuard interface, an SSH tunnel. You are asserting this.
export GITSTATE_ADMIN_UNAUTHENTICATED=i-accept
```

With (a), management requests carry `Authorization: Bearer $GITSTATE_ADMIN_TOKEN`.

---

## 2. TLS

**gitstate does not terminate TLS.** It speaks plain HTTP and expects a reverse proxy in front of it
on any deployment reachable from the internet. That is a deliberate choice — certificate issuance,
renewal and rotation are solved better by nginx/Caddy/a cloud load balancer than by a re-implementation
inside this binary — but it is a choice you have to act on, not one you can ignore.

What you get without TLS, and what you do not:

* Both ends of a replication exchange are **authenticated** over cleartext: the caller signs a request
  token, the responder signs the response body, and every op carries its author's signature. A
  man-in-the-middle cannot forge, inject or alter an op, and cannot impersonate either node.
* Nothing is **confidential**. Your context names, notes, tags and category labels are readable by
  anything on the path. Ops are not encrypted.

So `http://` peer URLs are accepted — a WireGuard link or an SSH tunnel is a real deployment — but a
peer URL reachable across the public internet should be `https://`.

A minimal Caddy front end:

```caddyfile
gitstate.example.org {
	reverse_proxy 127.0.0.1:7473
}
```

and nginx:

```nginx
server {
    listen 443 ssl;
    server_name gitstate.example.org;

    ssl_certificate     /etc/letsencrypt/live/gitstate.example.org/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/gitstate.example.org/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:7473;
        proxy_set_header Host $host;
        # The peer endpoints authenticate on the Authorization header; it must
        # survive the hop. Most proxies forward it by default — do not strip it.
        proxy_set_header Authorization $http_authorization;
    }
}
```

With a proxy in front, bind gitstate to loopback and let the proxy be the only thing listening
publicly. That is both simpler and safer than binding `0.0.0.0`:

```bash
GITSTATE_ADDR=127.0.0.1 GITSTATE_PORT=7473 gitstate serve
```

Note that a loopback bind needs no `GITSTATE_ADMIN_TOKEN` as far as gitstate is concerned — the guard
does not fire — so if the proxy forwards the management paths to the internet, **you** are the gate.
Either set the token anyway, or restrict the proxy to `/health` and `/api/sync/*`.

---

## 3. systemd

`deploy/gitstated.service` is a unit file to copy and edit. It runs the daemon as a dedicated
unprivileged user with a private data directory, and it is hardened to the extent a process that only
needs one directory and one socket can be.

```bash
sudo useradd --system --home /var/lib/gitstate --shell /usr/sbin/nologin gitstate
sudo install -d -o gitstate -g gitstate -m 0700 /var/lib/gitstate
sudo install -m 0755 target/release/gitstated /usr/local/bin/gitstated

sudo install -d -m 0700 /etc/gitstate
printf 'GITSTATE_ADMIN_TOKEN=%s\n' "$(openssl rand -hex 32)" | sudo tee /etc/gitstate/env >/dev/null
sudo chmod 0600 /etc/gitstate/env

sudo cp deploy/gitstated.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now gitstated
curl -sf http://127.0.0.1:7473/health
```

## 4. Container

`deploy/Dockerfile` builds a static-ish release binary and runs it as a non-root user with the data
directory on a volume.

```bash
docker build -f deploy/Dockerfile -t gitstate .
docker run -d --name gitstate \
  -p 127.0.0.1:7473:7473 \
  -v gitstate-data:/var/lib/gitstate \
  -e GITSTATE_ADMIN_TOKEN="$(openssl rand -hex 32)" \
  gitstate
```

`GITSTATE_ADDR` defaults to `0.0.0.0` inside the image, because a container's port is only reachable
if you publish it — which is why the image also requires you to have chosen an admin posture, exactly
as the guard demands. Publish it to `127.0.0.1` and put the proxy in front, as above.

---

## 5. Enrolling the two nodes with each other

Discovery is manual in both directions. On each node:

```bash
gitstate sync identity
# peer_id  019fb0c0-2f2a-7e90-9cdd-7a6f6157a777
# pubkey   51c2be5a98c8dd29115182b2d2cf300eb7965fcb79d2a7e4da3c42d452474b7e
```

Give the pair — and your node's URL — to the other operator **out of band**. The public key is what
authorises that node's writes, so how you hand it over is how strong the enrolment is; over a channel
an attacker controls, they can substitute their own key. Then each side runs:

```bash
gitstate sync peer add \
  --id  <the other node's peer_id> \
  --key <the other node's pubkey> \
  --url https://gitstate.example.org \
  --label laptop

gitstate sync peer list
gitstate sync run          # one round: push, then pull
```

`sync run` is explicit. There is no background replication thread, so a node never opens an outbound
connection you did not ask for. Put it on a timer if you want it periodic:

```
# /etc/systemd/system/gitstate-sync.timer  (with a matching .service running `gitstate sync run`)
[Timer]
OnUnitActiveSec=5min
```

### Reading the output

```
019fb0c0…  https://gitstate.example.org  pushed=92 pulled=48 applied=3 skipped=45 rejected=0
```

* `applied` — ops that changed local state.
* `skipped` — ops that verified but lost their merge, or were already held. In steady state this is
  most of them, including your own ops coming back: a peer re-exports everything it was given.
* `rejected` — ops that **failed verification**. In a healthy deployment this is `0`. Anything else
  means a peer offered something whose signature did not check out, whose author is not enrolled,
  whose clock claimed another node's identity, or whose clock was implausibly far in the future.
  Investigate it; it is not noise.

To revoke a peer, remove the enrolment. Its key stops being admitted immediately, and ops it has
already sent are *not* retracted — replicated history is durable, and a bad write has to be superseded
by a later one, not deleted:

```bash
gitstate sync peer remove --id <peer_id>
```

---

## 6. Data, durability and backup

Everything a node holds is one SQLite file.

```bash
gitstate data path
# data_dir  /var/lib/gitstate
# db_path   /var/lib/gitstate/gitstate.db
```

`GITSTATE_DATA_DIR` overrides the location. The directory holds:

* `gitstate.db` — repos, derived caches, contexts, categories, the `sync_ops` log, the enrolled peer
  list, and **this node's sync secret key**. Treat the whole file as a secret.

### Backing it up

Copy it with SQLite's own backup, not `cp` — a plain copy of a live database can capture a torn
write:

```bash
sqlite3 /var/lib/gitstate/gitstate.db ".backup '/var/backups/gitstate-$(date -u +%FT%H%M%SZ).db'"
```

### What is and is not recoverable

* **Contexts and categories** are replicated. If a node is lost and you still have its peer, the
  contexts converge back from that peer once the rebuilt node is enrolled again — this is a CRDT log,
  not a master copy.
* **The sync identity is not replicated.** Lose `gitstate.db` and the node's keypair is gone; the
  rebuilt node mints a new one and every peer must re-enrol it (`sync peer add` with the new key and
  a new `peer_id`). This is the main reason to back the file up even though the data replicates.
* **Derived caches** — commits, contributions, project state, work items, effort, classifications —
  are re-derivable from your own git and forge, and never replicate. Losing them costs a re-scan.

### Restoring

Stop the daemon, put the file back, start it. Migrations are forward-only and idempotent, so a file
from an older build is migrated on open. There is no downgrade path: a file opened by a newer build is
not readable by an older one.

---

## 7. Environment reference

| Variable | Default | Meaning |
|---|---|---|
| `GITSTATE_ADDR` | `127.0.0.1` | Bind address. A non-loopback value requires an admin posture (§1). |
| `GITSTATE_PORT` | `7473` | Bind port. |
| `GITSTATE_ADMIN_TOKEN` | unset | Bearer token required on the management API. |
| `GITSTATE_ADMIN_UNAUTHENTICATED` | unset | Set to `i-accept` to assert the management API is protected outside this process. |
| `GITSTATE_DATA_DIR` | platform data dir | Where `gitstate.db` lives. |
| `GITSTATE_WEB_DIST` | probed | The built SPA to serve. Unset ⇒ API only. |
| `GITSTATE_TAXONOMY_PATH` | unset | A signed taxonomy document to load instead of the embedded one. |

---

## 8. What this deployment does not give you

Stated plainly, because the gap between what a deployment guide implies and what it delivers is
usually where the trouble is.

* **No NAT traversal.** A node with no routable address can dial out and converge fully, but cannot be
  dialled. There is no hole-punching and no relay. If one is added later it will sit behind the same
  `PeerClient` seam as the direct transport, and removing it will have to leave the direct transport
  working — a node must never *need* a third party to sync.
* **No confidentiality without TLS.** See §2.
* **No multi-tenancy.** A node is one person's or one team's; there are no accounts and no
  per-user authorization. Everyone with the admin token sees everything.
* **No replication of anything but contexts and categories.** By design — see
  [P2P-CONTEXTS.md](P2P-CONTEXTS.md).
* **No automatic peer re-keying.** Rotating a node's sync key means re-enrolling with every peer by
  hand.
