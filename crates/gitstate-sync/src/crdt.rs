//! Decomposition of a full [`Context`]/[`Category`] into its minimal
//! [`SyncOp`] set, and the merge semantics they obey (§5).
//!
//! # Merge rules
//!
//! * **Scalar fields** (`name`, `description`, `notes`; `label`, `color`,
//!   `parent_key`): last-writer-wins by `Hlc`, per field. At an **exact** clock
//!   tie — same wall reading, counter and peer, two different values — the larger
//!   value wins, ranked `(length, bytes)`. That case cannot arise from an honest
//!   node (`next_hlc` always advances), only from a buggy or hostile peer; without
//!   the rule the incumbent would keep the field, so whichever op *arrived first*
//!   would win and two replicas would diverge silently. `(length, bytes)` rather
//!   than plain byte order because it is the order of the shared engine's
//!   deterministic-CBOR encoding, so the two agree.
//! * **Set members** (`tags`, `repo_ids`, `pr_refs`): present iff the element's
//!   max add-clock ≥ its max remove-clock — add wins the tie. A `pr_ref`'s `note`
//!   is an LWW scalar on the element, and obeys the same tie rule as above.
//! * **Deletion**: a document-level tombstone that a later higher-`Hlc` write
//!   resurrects (whole-doc LWW). Tombstones are retained, never hard-deleted.
//!
//! The functions in THIS module only decompose a local object into ops. The
//! rules above are applied on the receiving side by `Store::merge_sync_op`
//! (see [`super::apply_op`]), which is the only thing that moves rows: it
//! compares each op's clock against the per-field clock map, the OR-Set member
//! clocks and the tombstone clock the store keeps beside every context and
//! category.
//!
//! Every rule reduces to "take the maximum" under a **total** order, so applying
//! ops is commutative and idempotent: a peer converges on the same rows whatever
//! order the ops reach it in, and re-delivering one changes nothing. Totality is
//! the load-bearing word — the value tie-break above exists because the clock
//! alone is not quite total in the presence of a peer that does not follow the
//! rules. `tests/convergence.rs` proves the property over every permutation of a
//! mixed op set rather than asserting it here.
//!
//! # Relationship to the shared engine
//!
//! This is gitstate's **own** algebra, not the shared KOTVA engine's. The scalar
//! rule above is a faithful mapping of `SYNC.md` §4.4 and is proven equal to it
//! over every arrival order in `tests/shared_engine_parity.rs`. The member set and
//! the tombstone are **not** §4.3 and §4.5 respectively; the same test asserts
//! exactly how they differ and why closing the gap needs an op-envelope change.

use gitstate_core::{CatField, Category, Context, CtxField, SyncOp};

/// Decompose a full context into the ops that reconstruct it. Live contexts emit
/// their scalar + set-member ops; a tombstoned context emits a single
/// [`SyncOp::ContextDel`]. All ops share the context's current `Hlc`.
pub fn op_for_context(c: &Context) -> Vec<SyncOp> {
    if c.deleted {
        return vec![SyncOp::ContextDel {
            id: c.id.clone(),
            hlc: c.hlc.clone(),
        }];
    }
    let mut ops = Vec::new();
    ops.push(SyncOp::ContextLww {
        id: c.id.clone(),
        field: CtxField::Name,
        value: c.name.clone(),
        hlc: c.hlc.clone(),
    });
    ops.push(SyncOp::ContextLww {
        id: c.id.clone(),
        field: CtxField::Description,
        value: c.description.clone(),
        hlc: c.hlc.clone(),
    });
    ops.push(SyncOp::ContextLww {
        id: c.id.clone(),
        field: CtxField::Notes,
        value: c.notes.clone(),
        hlc: c.hlc.clone(),
    });
    for tag in &c.tags {
        ops.push(SyncOp::ContextTag {
            id: c.id.clone(),
            tag: tag.clone(),
            add: true,
            hlc: c.hlc.clone(),
        });
    }
    for repo in &c.repo_ids {
        ops.push(SyncOp::ContextRepo {
            id: c.id.clone(),
            repo_id: repo.clone(),
            add: true,
            hlc: c.hlc.clone(),
        });
    }
    for pr in &c.pr_refs {
        ops.push(SyncOp::ContextPr {
            id: c.id.clone(),
            repo_slug: pr.repo_slug.clone(),
            number: pr.number,
            note: pr.note.clone(),
            add: true,
            hlc: c.hlc.clone(),
        });
    }
    ops
}

/// Decompose a full category into its ops. A tombstoned category emits a single
/// [`SyncOp::CategoryDel`].
pub fn op_for_category(c: &Category) -> Vec<SyncOp> {
    if c.deleted {
        return vec![SyncOp::CategoryDel {
            id: c.id.clone(),
            hlc: c.hlc.clone(),
        }];
    }
    let mut ops = vec![
        SyncOp::CategoryLww {
            id: c.id.clone(),
            key: c.key.clone(),
            field: CatField::Label,
            value: c.label.clone(),
            hlc: c.hlc.clone(),
        },
        SyncOp::CategoryLww {
            id: c.id.clone(),
            key: c.key.clone(),
            field: CatField::Color,
            value: c.color.clone().unwrap_or_default(),
            hlc: c.hlc.clone(),
        },
    ];
    if let Some(parent) = &c.parent_key {
        ops.push(SyncOp::CategoryLww {
            id: c.id.clone(),
            key: c.key.clone(),
            field: CatField::ParentKey,
            value: parent.clone(),
            hlc: c.hlc.clone(),
        });
    }
    ops
}

#[cfg(test)]
mod tests {
    use super::*;
    use gitstate_core::{CategoryId, CategorySource, ContextId, Hlc, PeerId, RepoId};

    fn hlc() -> Hlc {
        Hlc {
            wall_ms: 1,
            counter: 0,
            peer: PeerId::from("p"),
        }
    }

    #[test]
    fn context_decomposes_scalars_and_members() {
        let c = Context {
            id: ContextId::from("c1"),
            name: "n".into(),
            description: "d".into(),
            repo_ids: vec![RepoId::from("r1")],
            pr_refs: vec![],
            notes: "notes".into(),
            tags: vec!["t1".into(), "t2".into()],
            created_at: "x".into(),
            updated_at: "x".into(),
            hlc: hlc(),
            deleted: false,
        };
        let ops = op_for_context(&c);
        // 3 scalars + 2 tags + 1 repo
        assert_eq!(ops.len(), 6);
    }

    #[test]
    fn tombstoned_context_is_one_del() {
        let mut c = Context {
            id: ContextId::from("c1"),
            name: String::new(),
            description: String::new(),
            repo_ids: vec![],
            pr_refs: vec![],
            notes: String::new(),
            tags: vec![],
            created_at: "x".into(),
            updated_at: "x".into(),
            hlc: hlc(),
            deleted: true,
        };
        assert_eq!(op_for_context(&c).len(), 1);
        c.deleted = false;
        assert!(op_for_context(&c).len() > 1);
    }

    #[test]
    fn category_decomposes() {
        let c = Category {
            id: CategoryId::from("k1"),
            key: "feature.api".into(),
            label: "API".into(),
            parent_key: Some("feature".into()),
            color: Some("#fff".into()),
            source: CategorySource::Local,
            taxonomy_version: None,
            hlc: hlc(),
            deleted: false,
        };
        // label + color + parent
        assert_eq!(op_for_category(&c).len(), 3);
    }
}
