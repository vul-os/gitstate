//! Local personalization — replaces pooled fine-tuning. Each box learns *its
//! own* conventions from the user's accept/override feedback, stored locally,
//! and re-ranks the base classifier's output toward keys this box favours.
//!
//! Uses only the [`Store`] trait's `record_feedback` + `kv` methods, so it
//! carries no schema of its own: a running per-key tally lives in `kv` under
//! `feedback_count:<key>`.

use gitstate_core::{Classification, Result, Store, WorkItemId};

const PREFIX: &str = "feedback_count:";

/// Re-ranks classifications by locally-learned priors.
pub struct Personalizer<'a> {
    store: &'a dyn Store,
}

impl<'a> Personalizer<'a> {
    pub fn new(store: &'a dyn Store) -> Self {
        Personalizer { store }
    }

    /// Nudge each classification's confidence up when this box has historically
    /// chosen its category, so favoured conventions surface first. Deterministic
    /// and bounded: the prior can add at most ~0.15 to confidence.
    pub fn adjust(&self, base: &[Classification]) -> Result<Vec<Classification>> {
        let total = self.total_feedback()?.max(1) as f64;
        let mut out = Vec::with_capacity(base.len());
        for c in base {
            let count = self.count_for(&c.category_key)? as f64;
            let prior = count / total; // 0..1 share of past choices
            let boost = 0.15 * prior;
            let mut adj = c.clone();
            adj.confidence = (adj.confidence + boost).clamp(0.0, 1.0);
            if boost > 0.0 {
                adj.rationale = if adj.rationale.is_empty() {
                    format!("local prior +{boost:.2}")
                } else {
                    format!("{} (local prior +{boost:.2})", adj.rationale)
                };
            }
            out.push(adj);
        }
        // Stable re-rank: highest adjusted confidence first, ties keep order.
        out.sort_by(|a, b| {
            b.confidence
                .partial_cmp(&a.confidence)
                .unwrap_or(std::cmp::Ordering::Equal)
        });
        Ok(out)
    }

    /// Record that the user chose `chosen_key` for `item`, both as an audit row
    /// and as an increment to the running per-key tally.
    pub fn record(&self, item: &WorkItemId, chosen_key: &str) -> Result<()> {
        self.store.record_feedback(item, chosen_key)?;
        let key = format!("{PREFIX}{chosen_key}");
        let next = self
            .store
            .kv_get(&key)?
            .and_then(|v| v.parse::<u64>().ok())
            .unwrap_or(0)
            + 1;
        self.store.kv_set(&key, &next.to_string())?;
        let tkey = format!("{PREFIX}__total__");
        let tnext = self
            .store
            .kv_get(&tkey)?
            .and_then(|v| v.parse::<u64>().ok())
            .unwrap_or(0)
            + 1;
        self.store.kv_set(&tkey, &tnext.to_string())?;
        Ok(())
    }

    fn count_for(&self, key: &str) -> Result<u64> {
        Ok(self
            .store
            .kv_get(&format!("{PREFIX}{key}"))?
            .and_then(|v| v.parse::<u64>().ok())
            .unwrap_or(0))
    }

    fn total_feedback(&self) -> Result<u64> {
        Ok(self
            .store
            .kv_get(&format!("{PREFIX}__total__"))?
            .and_then(|v| v.parse::<u64>().ok())
            .unwrap_or(0))
    }
}
