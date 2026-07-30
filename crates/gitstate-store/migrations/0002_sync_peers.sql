-- Manually-enrolled sync peers.
--
-- gitstate has no peer discovery: a row exists here because an operator typed
-- the other node's URL and public key. There is no directory to query, no
-- bootstrap list to seed and no default endpoint, so an empty table means "this
-- node syncs with nobody" — the correct state for a fresh install, not a
-- degraded one.
--
-- `id` is the peer's PeerId, which is the identity its HLCs tiebreak on; the
-- ingest path refuses an op whose clock names a different id than the key that
-- signed it, so this column is security-relevant and not a label.
--
-- `pubkey` is a hex ed25519 public key. It is the ONLY thing that authorises a
-- remote op: the transport authenticates the connection too, but a relayed op is
-- verified on its own signature against this column.
--
-- `last_pull_hlc` is this node's high-water mark over what it has already pulled
-- from that peer — a cursor, kept per peer because two peers are at different
-- points in their own logs.
CREATE TABLE IF NOT EXISTS sync_peers (
  id            TEXT PRIMARY KEY,
  url           TEXT NOT NULL,
  pubkey        TEXT NOT NULL,
  label         TEXT,
  added_at      TEXT NOT NULL,
  last_pull_hlc TEXT
);

-- One enrolment per key. Enrolling the same key twice under two ids would let a
-- single holder of one secret present itself as two clock identities, which is
-- exactly the tiebreak forgery the ingest check exists to stop.
CREATE UNIQUE INDEX IF NOT EXISTS idx_sync_peers_pubkey ON sync_peers(pubkey);

-- The author and signature of a REMOTE op, carried alongside the op in the log.
--
-- Additive columns on `sync_ops`, not a rewrite of it: the existing rows and
-- their `seq` numbering are untouched, and both columns are nullable because a
-- locally-authored op has no stored signature (it is signed on export, from this
-- node's own key, which is the only key that may attest to it).
--
-- They exist because ops are RELAYED. Node C's change reaches node A through node
-- B, and A must verify C's authorship — not B's assurance that C really said it.
-- That only works if C's signature travels with the op instead of being replaced
-- by each hop's own, so B stores what C sent and re-exports exactly that.
-- Without these columns a three-node topology could only ever be as trustworthy
-- as its most talkative member.
ALTER TABLE sync_ops ADD COLUMN author TEXT;
ALTER TABLE sync_ops ADD COLUMN sig TEXT;
