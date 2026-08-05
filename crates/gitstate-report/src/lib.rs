//! gitstate-report — dashboard assembly plus NL→report.
//!
//! Ported from Go's `internal/report` (`report.go`), the last of the five
//! domains in T11's port plan (`docs/PORT-PLAN.md` §7 explains why it is
//! sequenced last). Two capabilities, matching the Go package's own split:
//!
//! 1. **Dashboard assembly** — burndown, a recent-activity feed, and
//!    optional LLM status synthesis. The rollup math itself (throughput,
//!    cycle-time trend, state counts) already existed in
//!    `gitstate_core::analytics` before this wave; only [`gitstate_core::analytics::burndown`]
//!    and [`gitstate_core::analytics::recent_activity`] were net-new (added directly
//!    there, next to `throughput`, per the port plan's own recommendation —
//!    not duplicated here). This crate does not re-wrap that assembly: it
//!    happens in `gitstate_daemon::ops` (the same place wave 3's
//!    `context_bundle` assembly lives), because it is read-only orchestration
//!    over data structures that already exist, not a new domain algorithm.
//!
//! 2. **NL→report** ([`nl`]) — the one piece of this whole plan that is a
//!    **security-relevant redesign, not a port**. Read [`nl`]'s module doc
//!    before touching anything in it: the Go version generated raw SQL from
//!    an LLM completion and validated it with a regex allowlist; this port
//!    does not carry that shape forward. See [`nl`] for what replaced it and
//!    why.

pub mod nl;
