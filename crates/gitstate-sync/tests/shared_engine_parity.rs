//! gitstate's merge algebra, held against the shared KOTVA engine.
//!
//! # What this test is, and what it is not
//!
//! gitstate does **not** run the shared engine. Its merge decisions are taken by
//! its own SQL-native algebra in `gitstate-store`, over the per-field clock maps,
//! member add/remove clocks and tombstone clock the schema carries. That is a
//! private implementation, and the honest thing to do about it is not to claim
//! otherwise but to *bound* it: link the shared engine (`kotva-sync`, the crates.io
//! publication of `substrate/SYNC.md` capability ③) as a test dependency, drive
//! the same op streams through both, and assert where they agree — and state
//! plainly where they do not.
//!
//! So this file is two things:
//!
//! 1. a **parity proof** for the part of gitstate's algebra that is a faithful
//!    mapping of a shared primitive: the §4.4 LWW register, which carries every
//!    scalar field of a context and a category; and
//! 2. an **executable record of the two divergences** that block gitstate from
//!    simply adopting `kotva_sync::SyncState` wholesale. Each is asserted, so if
//!    either algebra changes such that the divergence disappears — or widens — this
//!    test says so instead of the difference quietly rotting in a comment.
//!
//! # The mapping (`SYNC.md` §4.10 asks for this explicitly, per modelled object)
//!
//! | gitstate construct | shared primitive | faithful? |
//! |---|---|---|
//! | `Context.name` / `.description` / `.notes`, `Category.label` / `.color` / `.parent_key` | §4.4 LWW register, `target = "<kind>/<id>"`, `field = <column>` | **yes** — proven below |
//! | `Context.tags` / `.repo_ids` / `.pr_refs` | §4.3 OR-Set | **no** — see `DIVERGENCE 1` |
//! | `Context.deleted` / `Category.deleted` | §4.5 death certificate | **no** — see `DIVERGENCE 2` |
//!
//! ## DIVERGENCE 1 — the member set is an LWW-element-set, not an observed-remove OR-Set
//!
//! §4.3's remove is *observed*: it names the specific add-tags it cancels, so an
//! add it did not see survives it. gitstate's remove is a clock: an element is
//! present iff its max add-clock ≥ its max remove-clock.
//!
//! These disagree in both directions, on histories as simple as one add and one
//! remove — the test below tabulates all three cases. The root cause is not a
//! merge subtlety but the **wire**: gitstate's `SyncOp::ContextTag` has no field
//! in which to carry an observed add-tag set, so no gitstate remove can cancel an
//! add it did not itself mint. Adopting §4.3 therefore means changing the op
//! envelope and everything that has already been written into `sync_ops` under the
//! current one, not swapping a merge function.
//!
//! Neither is a bug in isolation — an LWW-element-set is a well-defined CRDT and
//! it converges. It is a *different* CRDT, and the cost of moving is a wire
//! migration, which is why it is not being done silently inside a refactor.
//!
//! ## DIVERGENCE 2 — the tombstone resurrects; a death certificate does not
//!
//! §4.5 is remove-wins: a certificate dominates every later write, forever, and
//! only an explicit `live` op undoes it. gitstate's tombstone is whole-document
//! LWW: a write with a strictly higher clock anywhere in the document resurrects
//! it. That is the right model for gitstate — §4.10's selection test asks *is
//! there any user action that restores this thing using the same ordinary
//! operation that created it?*, and the answer is yes, `gitstate context create`
//! with the same id — so the *faithful* shared mapping is not §4.5 at all but a
//! §4.4 register.
//!
//! But it cannot be a plain `deleted` register either, because gitstate compares
//! the tombstone clock against *every* field and member clock, not against other
//! writes to `deleted`. Expressing that in §4.4 would require every op that
//! touches a document to also write the `deleted` register — a wire change. So
//! this one is a mapping that exists but has not been taken.

use gitstate_core::{
    CatField, CategoryId, ContextId, CtxField, Hlc as GsHlc, PeerId, Store, SyncOp,
};
use gitstate_store::SqliteStore;
use gitstate_sync::apply_op;

use dmtap_sync::crdt::{DeathClass, DeathState};
use dmtap_sync::detcbor::SVal;
use dmtap_sync::wire::{
    AddTag, Hlc as KHlc, SyncOp as KSyncOp, OP_LWW_SET, OP_SET_ADD, OP_SET_REMOVE,
};
use dmtap_sync::SyncState;

/// A wall reading far enough in the past that the shared engine's ±120 s skew
/// check never trips on the fixed clocks below, whatever the machine's clock says.
const RECEIVER_NOW_MS: u64 = 10_000_000_000_000;

fn gs_hlc(wall_ms: u64, counter: u32, peer: &str) -> GsHlc {
    GsHlc {
        wall_ms,
        counter,
        peer: PeerId::from(peer),
    }
}

/// The same clock in the shared engine's spelling.
///
/// `Hlc.author` there is a byte string and is the final tiebreak, exactly as
/// `Hlc.peer` is here — so the peer id's UTF-8 bytes ARE the author, and the two
/// total orders coincide rather than merely resembling each other. (Both crates'
/// `Ord` is lexicographic by `(wall, counter, author)`.)
fn k_hlc(h: &GsHlc) -> KHlc {
    KHlc {
        wall: h.wall_ms,
        counter: h.counter,
        author: h.peer.0.as_bytes().to_vec(),
    }
}

fn permutations<T: Clone>(items: &[T]) -> Vec<Vec<T>> {
    if items.len() <= 1 {
        return vec![items.to_vec()];
    }
    let mut out = Vec::new();
    for i in 0..items.len() {
        let mut rest = items.to_vec();
        let head = rest.remove(i);
        for mut tail in permutations(&rest) {
            tail.insert(0, head.clone());
            out.push(tail);
        }
    }
    out
}

/// Map a gitstate op onto its shared-engine equivalent, under the mapping table
/// in this file's header. Returns `None` for the ops whose mapping is a
/// divergence (the tombstones), so a caller cannot accidentally compare them.
fn to_shared(op: &SyncOp) -> Option<KSyncOp> {
    let base =
        |kind: u8, target: String, field: Option<String>, value: Option<SVal>, hlc: &GsHlc| {
            KSyncOp {
                kind,
                ns: String::new(),
                target,
                field,
                value,
                hlc: k_hlc(hlc),
                observed: None,
                reference: None,
            }
        };
    match op {
        SyncOp::ContextLww {
            id,
            field,
            value,
            hlc,
        } => Some(base(
            OP_LWW_SET,
            format!("context/{}", id.0),
            Some(field.as_str().to_string()),
            Some(SVal::Text(value.clone())),
            hlc,
        )),
        SyncOp::CategoryLww {
            key,
            field,
            value,
            hlc,
            ..
        } => Some(base(
            OP_LWW_SET,
            // Keyed on the dotted key, not the id: `key` carries the unique index
            // and is a category's effective identity in gitstate.
            format!("category/{key}"),
            Some(field.as_str().to_string()),
            Some(SVal::Text(value.clone())),
            hlc,
        )),
        SyncOp::ContextTag { id, tag, add, hlc } => {
            let mut k = base(
                if *add { OP_SET_ADD } else { OP_SET_REMOVE },
                format!("context/{}/tags", id.0),
                None,
                Some(SVal::Text(tag.clone())),
                hlc,
            );
            if !*add {
                // §4.3 requires a remove to name what it cancels. gitstate's op
                // carries no such list, so the closest honest translation is
                // "cancels every add at or below this clock" — which is precisely
                // where DIVERGENCE 1 lives.
                k.observed = Some(vec![AddTag {
                    author: hlc.peer.0.as_bytes().to_vec(),
                    hlc: k_hlc(hlc),
                }]);
            }
            Some(k)
        }
        SyncOp::ContextRepo { .. } | SyncOp::ContextPr { .. } => None,
        SyncOp::ContextDel { .. } | SyncOp::CategoryDel { .. } => None,
    }
}

/// Drive `ops` through gitstate's algebra and read back the scalar fields.
fn gitstate_scalars(ops: &[SyncOp]) -> Vec<(String, String)> {
    let store = SqliteStore::open_in_memory().unwrap();
    for op in ops {
        apply_op(&store, op).unwrap();
    }
    let mut out = Vec::new();
    for id in ["c1"] {
        if let Some(c) = store.get_context(&ContextId::from(id)).unwrap() {
            out.push((format!("context/{id}#name"), c.name));
            out.push((format!("context/{id}#description"), c.description));
            out.push((format!("context/{id}#notes"), c.notes));
        }
    }
    for key in ["feature.api"] {
        if let Some(c) = store.get_category(key).unwrap() {
            out.push((format!("category/{key}#label"), c.label));
            out.push((format!("category/{key}#color"), c.color.unwrap_or_default()));
        }
    }
    out.sort();
    out
}

/// Drive the same ops through the shared engine and read back the same fields.
fn shared_scalars(ops: &[SyncOp]) -> Vec<(String, String)> {
    let mut state = SyncState::new();
    for op in ops {
        if let Some(k) = to_shared(op) {
            state
                .ingest(&k, RECEIVER_NOW_MS)
                .expect("the shared engine must accept a mapped op");
        }
    }
    let mut out = Vec::new();
    let read = |state: &SyncState, target: &str, field: &str| -> Option<String> {
        state.lww.get(target, field).and_then(|v| match v {
            SVal::Text(s) => Some(s.clone()),
            _ => None,
        })
    };
    for (target, fields) in [
        ("context/c1", &["name", "description", "notes"][..]),
        ("category/feature.api", &["label", "color"][..]),
    ] {
        for f in fields {
            if let Some(v) = read(&state, target, f) {
                out.push((format!("{target}#{f}"), v));
            }
        }
    }
    out.sort();
    out
}

fn ctx_lww(field: CtxField, value: &str, h: GsHlc) -> SyncOp {
    SyncOp::ContextLww {
        id: ContextId::from("c1"),
        field,
        value: value.into(),
        hlc: h,
    }
}

fn cat_lww(field: CatField, value: &str, h: GsHlc) -> SyncOp {
    SyncOp::CategoryLww {
        id: CategoryId::from("cat-1"),
        key: "feature.api".into(),
        field,
        value: value.into(),
        hlc: h,
    }
}

/// The parity claim, over every arrival order: gitstate's LWW registers select
/// the same winners as the shared engine's §4.4 registers.
///
/// The op set is built so each of the three discriminators in the shared engine's
/// `lww_wins` is exercised: a clock difference, a peer-id tiebreak at an equal
/// wall+counter, and an **exact** clock tie decided on the value bytes.
#[test]
fn lww_registers_agree_with_the_shared_engine_over_every_arrival_order() {
    let ops = vec![
        // Plain clock ordering.
        ctx_lww(CtxField::Name, "older", gs_hlc(10, 0, "peer-a")),
        ctx_lww(CtxField::Name, "newer", gs_hlc(20, 0, "peer-b")),
        // Equal wall and counter: the author/peer id decides.
        ctx_lww(CtxField::Notes, "from-a", gs_hlc(30, 0, "peer-a")),
        ctx_lww(CtxField::Notes, "from-z", gs_hlc(30, 0, "peer-z")),
        // EXACT clock tie (same wall, counter AND peer), two different values of
        // the same length: both engines must break it on the value, in the same
        // direction, or two replicas hold different values forever.
        cat_lww(CatField::Label, "aaa", gs_hlc(40, 0, "peer-a")),
        cat_lww(CatField::Label, "bbb", gs_hlc(40, 0, "peer-a")),
    ];

    let perms = permutations(&ops);
    assert_eq!(perms.len(), 720);

    for perm in &perms {
        let mine = gitstate_scalars(perm);
        let theirs = shared_scalars(perm);
        // Compare only the fields the shared side has an opinion about: gitstate
        // materializes a row with empty strings for fields nobody wrote, while an
        // absent register is simply absent. Comparing those would be comparing
        // storage models, not merge outcomes.
        for (key, shared_value) in &theirs {
            let mine_value = mine
                .iter()
                .find(|(k, _)| k == key)
                .map(|(_, v)| v.clone())
                .unwrap_or_else(|| panic!("gitstate has no {key}"));
            assert_eq!(
                &mine_value, shared_value,
                "the two engines disagree on {key} for arrival order {perm:?}"
            );
        }
    }

    // And the specific winners, spelled out, so this is a test of the RULES.
    let winners = shared_scalars(&perms[0]);
    let get = |k: &str| {
        winners
            .iter()
            .find(|(key, _)| key == k)
            .map(|(_, v)| v.as_str())
            .unwrap()
    };
    assert_eq!(get("context/c1#name"), "newer");
    assert_eq!(get("context/c1#notes"), "from-z");
    assert_eq!(
        get("category/feature.api#label"),
        "bbb",
        "an exact clock tie is decided by the value"
    );
}

/// The tie-break's DIRECTION, isolated: it ranks the deterministic-CBOR encoding,
/// and CBOR text is length-prefixed, so it is length-major and NOT plain
/// lexicographic order on the UTF-8.
///
/// This is the case a byte-wise implementation gets wrong, and it is a silent
/// wrong: `"z"` sorts above `"aa"` lexicographically but below it once encoded, so
/// an engine comparing raw bytes and an engine comparing CBOR would settle the same
/// tie on different values, with no error raised on either side. It is checked on
/// its own rather than folded into the permutation set above, because two more ops
/// there would multiply 720 arrival orders by 56.
#[test]
fn the_exact_tie_break_is_length_major_in_both_engines() {
    // "z" is lexicographically greater than "aa"; "aa" is the longer encoding.
    let ops = vec![
        cat_lww(CatField::Label, "z", gs_hlc(50, 0, "peer-a")),
        cat_lww(CatField::Label, "aa", gs_hlc(50, 0, "peer-a")),
    ];
    for perm in permutations(&ops) {
        let mine = gitstate_scalars(&perm);
        let label = mine
            .iter()
            .find(|(k, _)| k == "category/feature.api#label")
            .map(|(_, v)| v.clone())
            .unwrap();
        assert_eq!(
            label, "aa",
            "gitstate must rank by length first, not lexicographically"
        );
        let theirs = shared_scalars(&perm);
        assert!(
            theirs.contains(&("category/feature.api#label".to_string(), "aa".to_string())),
            "…and so must the shared engine, or the two disagree: {theirs:?}"
        );
    }
}

/// DIVERGENCE 1, asserted rather than described.
///
/// gitstate's member set is an LWW-element-set: present iff max add-clock ≥ max
/// remove-clock. §4.3 is an observed-remove OR-Set: present iff some add-tag is
/// not covered by a tombstone. The two disagree in **both** directions, and this
/// test pins all three cases rather than picking the flattering one:
///
/// | history | gitstate | shared §4.3 |
/// |---|---|---|
/// | add@10, remove@20 (no observed set on the wire) | absent | **present** |
/// | remove@20, re-add@15 by another peer | absent | **present** |
/// | add@30 and remove@30, same clock | **present** | absent |
///
/// The first row is the important one, and it is not a subtlety of concurrency: it
/// is structural. §4.3 requires a remove to *name* the add-tags it cancels, and
/// gitstate's `SyncOp` envelope has no field to carry them. So there is no
/// translation of a gitstate remove that cancels anything it did not itself mint,
/// and adopting §4.3 is therefore a **wire change**, not a merge change. That is
/// the concrete cost of the shared-engine adoption, stated in code.
#[test]
fn the_member_set_is_an_lww_element_set_and_not_an_observed_remove_or_set() {
    let tag = |t: &str, add: bool, h: GsHlc| SyncOp::ContextTag {
        id: ContextId::from("c1"),
        tag: t.into(),
        add,
        hlc: h,
    };

    let gitstate_has = |ops: &[SyncOp], t: &str| -> bool {
        let store = SqliteStore::open_in_memory().unwrap();
        for op in ops {
            apply_op(&store, op).unwrap();
        }
        store
            .get_context(&ContextId::from("c1"))
            .unwrap()
            .map(|c| c.tags.contains(&t.to_string()))
            .unwrap_or(false)
    };
    let shared_has = |ops: &[SyncOp], t: &str| -> bool {
        let mut state = SyncState::new();
        for op in ops {
            if let Some(k) = to_shared(op) {
                state.ingest(&k, RECEIVER_NOW_MS).unwrap();
            }
        }
        state.is_present("context/c1/tags", &SVal::Text(t.to_string()))
    };

    // ── Row 1: a plain sequential add-then-remove. ──
    let sequential = vec![
        tag("t", true, gs_hlc(10, 0, "peer-a")),
        tag("t", false, gs_hlc(20, 0, "peer-a")),
    ];
    // gitstate: max remove-clock (20) > max add-clock (10) ⇒ absent.
    assert!(
        !gitstate_has(&sequential, "t"),
        "gitstate: the later remove clock wins"
    );
    // The shared engine: the remove's own tag is the only thing this mapping can
    // put in `observed`, and it does not cover the add at 10 — so §4.3 keeps the
    // element. The divergence shows up in the SIMPLEST possible history, because
    // gitstate's op envelope cannot carry an observed set at all.
    assert!(
        shared_has(&sequential, "t"),
        "shared: an observed-remove cannot cancel an add it did not name"
    );

    // ── Row 2: a re-add at a clock BELOW an existing remove. ──
    let concurrent = vec![
        tag("t", false, gs_hlc(20, 0, "peer-a")),
        tag("t", true, gs_hlc(15, 0, "peer-b")),
    ];
    assert!(
        !gitstate_has(&concurrent, "t"),
        "gitstate: the higher remove clock wins, whoever added"
    );
    assert!(
        shared_has(&concurrent, "t"),
        "shared: the add was never observed by the remove, so it survives"
    );

    // ── Row 3: an exact clock tie — and here the disagreement REVERSES. ──
    // gitstate resolves an add/remove tie in favour of the add. Under this
    // mapping the remove names exactly the tag the add minted, so §4.3 tombstones
    // it. Neither engine is wrong; they are different CRDTs.
    let tie = vec![
        tag("t2", true, gs_hlc(30, 0, "peer-a")),
        tag("t2", false, gs_hlc(30, 0, "peer-a")),
    ];
    assert!(gitstate_has(&tie, "t2"), "gitstate: add wins an exact tie");
    assert!(
        !shared_has(&tie, "t2"),
        "shared: the remove names the add's own tag, so it is cancelled"
    );
}

/// DIVERGENCE 2, asserted. gitstate resurrects a tombstoned document on a later
/// write; a §4.5 death certificate never resurrects on an ordinary write.
#[test]
fn the_tombstone_resurrects_where_a_death_certificate_would_not() {
    let ops = vec![
        ctx_lww(CtxField::Name, "before", gs_hlc(10, 0, "peer-a")),
        SyncOp::ContextDel {
            id: ContextId::from("c1"),
            hlc: gs_hlc(20, 0, "peer-a"),
        },
        ctx_lww(CtxField::Name, "after", gs_hlc(30, 0, "peer-b")),
    ];

    let store = SqliteStore::open_in_memory().unwrap();
    for op in &ops {
        apply_op(&store, op).unwrap();
    }
    let c = store
        .get_context(&ContextId::from("c1"))
        .unwrap()
        .expect("the row exists either way");
    assert!(
        !c.deleted,
        "gitstate: a strictly later write resurrects the document"
    );
    assert_eq!(c.name, "after");

    // The same history under §4.5: the certificate dominates, and the later
    // register write does not touch the death dimension at all.
    //
    // Note which class has to be borrowed to express this at all. §4.5's three
    // classes are `redact`, `expires` and `sensitive` — a privacy redaction, an
    // expiry, a policy removal. None of them is "the user deleted their saved
    // working set", and that absence is itself the §4.10 answer: a context delete
    // is an ordinary reversible edit, not a certificate, so its faithful shared
    // mapping is a §4.4 register and never this dimension.
    let mut state = SyncState::new();
    for op in &ops {
        if let Some(k) = to_shared(op) {
            state.ingest(&k, RECEIVER_NOW_MS).unwrap();
        }
    }
    state.deaths.write(
        "context/c1",
        k_hlc(&gs_hlc(20, 0, "peer-a")),
        DeathState::Deleted(DeathClass::Redact),
    );
    assert!(
        state.deaths.is_deleted("context/c1"),
        "shared: a death certificate is not undone by a later ordinary write"
    );
    // …and the register still moved, which is exactly why the two models differ:
    // the shared engine holds "deleted, and its name is 'after'".
    assert_eq!(
        state.lww.get("context/c1", "name"),
        Some(&SVal::Text("after".into()))
    );
}

/// The two crates' total orders coincide, which is the precondition for any of
/// the above to mean anything. Asserted over every permutation of a set built so
/// each field in turn is the sole discriminator.
#[test]
fn the_two_hlc_total_orders_coincide() {
    let clocks = vec![
        gs_hlc(10, 0, "peer-a"),
        gs_hlc(10, 0, "peer-b"),
        gs_hlc(10, 1, "peer-a"),
        gs_hlc(11, 0, "peer-a"),
        gs_hlc(9, 9, "peer-b"),
    ];
    for perm in permutations(&clocks) {
        let mut mine = perm.clone();
        mine.sort();
        let mut theirs: Vec<KHlc> = perm.iter().map(k_hlc).collect();
        theirs.sort();
        let mine_as_shared: Vec<KHlc> = mine.iter().map(k_hlc).collect();
        assert_eq!(
            mine_as_shared, theirs,
            "the orders disagree, so no merge comparison below is meaningful"
        );
    }
}
