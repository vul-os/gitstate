//! Convergence as a property, over every arrival order.
//!
//! The claim gitstate's docs make is that two nodes handed the same set of ops
//! reach the same state whatever order the ops arrive in, and that re-delivering
//! an op changes nothing. A pair of hand-picked orderings does not establish that
//! — it establishes it for those two orderings. These tests enumerate **every
//! permutation** of a mixed op set and assert one final observable state.
//!
//! "Observable state" is deliberately the reconstructed [`Context`] /
//! [`Category`] a caller sees, not the internal clock tables: convergence is a
//! statement about what a user sees, and comparing internals would let a
//! divergence hide behind a projection that happens to agree.
//!
//! The op sets are built to include the cases where a naive implementation
//! diverges:
//!
//! * two writers on the same field at different clocks (plain LWW);
//! * two writers on the same field at the **same** wall time and counter,
//!   distinguished only by peer id (the tiebreak);
//! * an OR-Set element added and removed at the same clock (add-wins on tie);
//! * a document tombstone with a later write (resurrection) and with an earlier
//!   one (the tombstone stands);
//! * a category addressed by two different ids under the same key, which must
//!   converge on one row.

use std::collections::BTreeSet;

use gitstate_core::{
    CatField, Category, CategoryId, Context, ContextId, CtxField, Hlc, PeerId, RepoId, Store,
    SyncOp,
};
use gitstate_store::SqliteStore;
use gitstate_sync::apply_op;

fn hlc(wall_ms: u64, counter: u32, peer: &str) -> Hlc {
    Hlc {
        wall_ms,
        counter,
        peer: PeerId::from(peer),
    }
}

/// Every ordering of `items`. Kept small on purpose: 7 ops is 5 040 orderings,
/// and each one opens a fresh in-memory database.
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

/// The comparable projection of everything the ops in these tests can touch.
#[derive(Debug, PartialEq, Eq)]
struct Observable {
    contexts: Vec<ContextView>,
    categories: Vec<CategoryView>,
}

#[derive(Debug, PartialEq, Eq)]
struct ContextView {
    id: String,
    name: String,
    description: String,
    notes: String,
    tags: BTreeSet<String>,
    repos: BTreeSet<String>,
    prs: BTreeSet<String>,
    deleted: bool,
}

#[derive(Debug, PartialEq, Eq)]
struct CategoryView {
    key: String,
    label: String,
    color: Option<String>,
    parent_key: Option<String>,
    deleted: bool,
}

fn view_context(c: &Context) -> ContextView {
    ContextView {
        id: c.id.0.clone(),
        name: c.name.clone(),
        description: c.description.clone(),
        notes: c.notes.clone(),
        tags: c.tags.iter().cloned().collect(),
        repos: c.repo_ids.iter().map(|r| r.0.clone()).collect(),
        prs: c
            .pr_refs
            .iter()
            .map(|p| {
                format!(
                    "{}#{}:{}",
                    p.repo_slug,
                    p.number,
                    p.note.as_deref().unwrap_or("")
                )
            })
            .collect(),
        deleted: c.deleted,
    }
}

fn view_category(c: &Category) -> CategoryView {
    CategoryView {
        key: c.key.clone(),
        label: c.label.clone(),
        color: c.color.clone(),
        parent_key: c.parent_key.clone(),
        deleted: c.deleted,
    }
}

/// Read the whole observable state, INCLUDING tombstoned documents.
///
/// `list_contexts` hides tombstones, so it cannot be the basis of the comparison:
/// two replicas that disagree about whether something is deleted would both show
/// an empty list and the test would pass. Ids are collected from the ops instead.
fn observe(store: &dyn Store, ctx_ids: &[&str], cat_keys: &[&str]) -> Observable {
    let mut contexts: Vec<ContextView> = ctx_ids
        .iter()
        .filter_map(|id| store.get_context(&ContextId::from(*id)).unwrap())
        .map(|c| view_context(&c))
        .collect();
    contexts.sort_by(|a, b| a.id.cmp(&b.id));
    let mut categories: Vec<CategoryView> = cat_keys
        .iter()
        .filter_map(|k| store.get_category(k).unwrap())
        .map(|c| view_category(&c))
        .collect();
    categories.sort_by(|a, b| a.key.cmp(&b.key));
    Observable {
        contexts,
        categories,
    }
}

fn ctx_lww(id: &str, field: CtxField, value: &str, h: Hlc) -> SyncOp {
    SyncOp::ContextLww {
        id: ContextId::from(id),
        field,
        value: value.into(),
        hlc: h,
    }
}

fn ctx_tag(id: &str, tag: &str, add: bool, h: Hlc) -> SyncOp {
    SyncOp::ContextTag {
        id: ContextId::from(id),
        tag: tag.into(),
        add,
        hlc: h,
    }
}

fn cat_lww(id: &str, key: &str, field: CatField, value: &str, h: Hlc) -> SyncOp {
    SyncOp::CategoryLww {
        id: CategoryId::from(id),
        key: key.into(),
        field,
        value: value.into(),
        hlc: h,
    }
}

/// Apply `ops` to a fresh store in the given order and return the observable
/// state. Each op is delivered TWICE, so idempotence is exercised on every
/// ordering rather than in a separate test with one ordering.
fn replay(ops: &[SyncOp], ctx_ids: &[&str], cat_keys: &[&str]) -> Observable {
    let store = SqliteStore::open_in_memory().unwrap();
    for op in ops {
        apply_op(&store, op).unwrap();
        assert!(
            !apply_op(&store, op).unwrap(),
            "re-delivering an op must report no change: {op:?}"
        );
    }
    observe(&store, ctx_ids, cat_keys)
}

#[test]
fn every_arrival_order_of_a_mixed_context_op_set_converges() {
    let ops = vec![
        // Two writers on `name`; 20 beats 10.
        ctx_lww("c1", CtxField::Name, "older", hlc(10, 0, "peer-a")),
        ctx_lww("c1", CtxField::Name, "newer", hlc(20, 0, "peer-b")),
        // Same wall time AND counter: the peer id is the only discriminator, and
        // "peer-z" > "peer-a" lexicographically.
        ctx_lww("c1", CtxField::Notes, "from a", hlc(30, 0, "peer-a")),
        ctx_lww("c1", CtxField::Notes, "from z", hlc(30, 0, "peer-z")),
        // An element added and removed at exactly the same clock: add wins.
        ctx_tag("c1", "contested", true, hlc(40, 0, "peer-a")),
        ctx_tag("c1", "contested", false, hlc(40, 0, "peer-a")),
    ];

    // Six ops, not seven: each ordering opens a fresh migrated database and
    // delivers every op twice, so the cost is 720 × 12 merges here and grows by a
    // factor of `n` per op added. The seventh op that used to be in this set was
    // an *uncontested* member add, which discriminates nothing this set does not
    // already cover — the OR-Set paths for repos and PR refs are permuted in
    // `every_arrival_order_of_a_delete_and_a_later_write_converges_on_resurrection`
    // instead, where the set is small.
    let perms = permutations(&ops);
    assert_eq!(perms.len(), 720, "every ordering of six ops");

    let expected = replay(&perms[0], &["c1"], &[]);
    // Spell the expectation out, so this is a test of the RULES and not merely of
    // self-consistency across orderings.
    assert_eq!(expected.contexts.len(), 1);
    let c = &expected.contexts[0];
    assert_eq!(c.name, "newer", "higher wall clock wins the field");
    assert_eq!(c.notes, "from z", "peer id is the final tiebreak");
    assert!(
        c.tags.contains("contested"),
        "add wins an exact clock tie: {:?}",
        c.tags
    );
    assert!(!c.deleted);

    for perm in &perms {
        assert_eq!(
            replay(perm, &["c1"], &[]),
            expected,
            "arrival order changed the converged state"
        );
    }
}

#[test]
fn every_arrival_order_of_a_delete_and_a_later_write_converges_on_resurrection() {
    let ops = vec![
        ctx_lww("c1", CtxField::Name, "before", hlc(10, 0, "peer-a")),
        SyncOp::ContextDel {
            id: ContextId::from("c1"),
            hlc: hlc(20, 0, "peer-a"),
        },
        // Strictly later than the tombstone: resurrects the document.
        ctx_lww("c1", CtxField::Name, "after", hlc(30, 0, "peer-b")),
        ctx_tag("c1", "kept", true, hlc(25, 0, "peer-b")),
        // The other two OR-Set member kinds, permuted here rather than in the
        // wider set above: a repo id, and a PR ref whose `note` is an LWW scalar
        // on the element.
        SyncOp::ContextRepo {
            id: ContextId::from("c1"),
            repo_id: RepoId::from("r1"),
            add: true,
            hlc: hlc(26, 0, "peer-b"),
        },
        SyncOp::ContextPr {
            id: ContextId::from("c1"),
            repo_slug: "vul-os/gitstate".into(),
            number: 7,
            note: Some("core".into()),
            add: true,
            hlc: hlc(27, 0, "peer-b"),
        },
    ];

    let perms = permutations(&ops);
    assert_eq!(perms.len(), 720, "every ordering of six ops");

    let expected = replay(&perms[0], &["c1"], &[]);
    let c = &expected.contexts[0];
    assert!(!c.deleted, "a strictly later write resurrects the document");
    assert_eq!(c.name, "after");
    assert!(c.tags.contains("kept"));
    assert!(c.repos.contains("r1"));
    assert!(
        c.prs.contains("vul-os/gitstate#7:core"),
        "the PR ref and its LWW note survive: {:?}",
        c.prs
    );

    for perm in &perms {
        assert_eq!(replay(perm, &["c1"], &[]), expected);
    }
}

#[test]
fn every_arrival_order_of_a_delete_that_outranks_every_write_converges_on_deleted() {
    let ops = vec![
        ctx_lww("c1", CtxField::Name, "doomed", hlc(10, 0, "peer-a")),
        ctx_tag("c1", "doomed", true, hlc(11, 0, "peer-a")),
        SyncOp::ContextDel {
            id: ContextId::from("c1"),
            hlc: hlc(99, 0, "peer-a"),
        },
    ];
    let perms = permutations(&ops);
    assert_eq!(perms.len(), 6);
    let expected = replay(&perms[0], &["c1"], &[]);
    assert!(
        expected.contexts[0].deleted,
        "the tombstone outranks every write"
    );
    for perm in &perms {
        assert_eq!(replay(perm, &["c1"], &[]), expected);
    }
}

/// Two peers independently minting a category for the same dotted key must
/// converge on ONE row, whatever order the ops arrive in. `key` is the effective
/// identity (it carries the unique index), so an implementation that keyed on the
/// id would either duplicate the category or hit a constraint depending on order.
#[test]
fn every_arrival_order_of_two_ids_for_one_category_key_converges_on_one_row() {
    let ops = vec![
        cat_lww(
            "cat-a",
            "feature.api",
            CatField::Label,
            "API",
            hlc(10, 0, "peer-a"),
        ),
        cat_lww(
            "cat-b",
            "feature.api",
            CatField::Label,
            "API v2",
            hlc(20, 0, "peer-b"),
        ),
        cat_lww(
            "cat-a",
            "feature.api",
            CatField::Color,
            "#4f46e5",
            hlc(15, 0, "peer-a"),
        ),
        cat_lww(
            "cat-b",
            "feature.api",
            CatField::ParentKey,
            "feature",
            hlc(25, 0, "peer-b"),
        ),
    ];

    let perms = permutations(&ops);
    assert_eq!(perms.len(), 24);
    let expected = replay(&perms[0], &[], &["feature.api"]);
    assert_eq!(expected.categories.len(), 1, "one row for one key");
    assert_eq!(expected.categories[0].label, "API v2");
    assert_eq!(
        expected.categories[0].parent_key.as_deref(),
        Some("feature")
    );

    for perm in &perms {
        assert_eq!(
            replay(perm, &[], &["feature.api"]),
            expected,
            "arrival order changed the converged category"
        );
    }
}

/// Convergence across TWO replicas rather than one: node A receives the ops in
/// one order, node B in the reverse, and both are then handed each other's whole
/// exported log. The two must agree — this is the actual topology, and it also
/// exercises the export/merge round trip rather than only the merge.
#[test]
fn two_replicas_fed_in_opposite_orders_then_exchanged_agree() {
    let ops = vec![
        ctx_lww("c1", CtxField::Name, "a-name", hlc(10, 0, "peer-a")),
        ctx_lww("c1", CtxField::Description, "b-desc", hlc(11, 0, "peer-b")),
        ctx_tag("c1", "shared", true, hlc(12, 0, "peer-a")),
        ctx_tag("c1", "shared", false, hlc(13, 0, "peer-b")),
        cat_lww("cat-1", "bug", CatField::Label, "Bug", hlc(14, 0, "peer-a")),
    ];

    for perm in permutations(&ops) {
        let a = SqliteStore::open_in_memory().unwrap();
        let b = SqliteStore::open_in_memory().unwrap();
        for op in &perm {
            apply_op(&a, op).unwrap();
        }
        for op in perm.iter().rev() {
            apply_op(&b, op).unwrap();
        }
        // Exchange complete logs, both ways.
        for op in b.sync_ops_since(None).unwrap() {
            apply_op(&a, &op).unwrap();
        }
        for op in a.sync_ops_since(None).unwrap() {
            apply_op(&b, &op).unwrap();
        }
        assert_eq!(
            observe(&a, &["c1"], &["bug"]),
            observe(&b, &["c1"], &["bug"]),
            "two replicas diverged after exchanging logs"
        );
        // And the remove at the higher clock did win.
        let c = a.get_context(&ContextId::from("c1")).unwrap().unwrap();
        assert!(!c.tags.contains(&"shared".to_string()), "{:?}", c.tags);
    }
}
