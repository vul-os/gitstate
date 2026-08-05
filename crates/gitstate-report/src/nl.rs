//! NL→report — the security-relevant redesign, not a port.
//!
//! ## What Go did, and why it does not carry forward
//!
//! Go's `report.AnswerQuery` asked an LLM to write a raw PostgreSQL `SELECT`
//! from an English question, then ran it through `validateSQL` — a
//! regex/keyword blocklist plus a positive table allowlist — inside a
//! `db.WithOrg` read-only transaction with a 5s `statement_timeout`. That
//! design existed to answer one question its multi-tenant Postgres world
//! actually had: *"the LLM can see this org's tables — how do we stop it
//! from reading or touching another org's row, or the identity tables RLS
//! doesn't cover?"* RLS (`SET LOCAL app.current_org`) did the org-isolation
//! half; `validateSQL` did the "no DDL/DML, no non-reporting table" half.
//!
//! **That threat model is gone.** gitstate is single-user, single SQLite
//! file, no RLS, no second tenant to isolate from. Porting `validateSQL`
//! verbatim would carry forward a defence tuned for a threat this app no
//! longer has, while leaving the threat it *does* have — **the LLM emits
//! text that gets executed as a query against your only database file** —
//! defended by nothing more than "we think our regexes catch every dangerous
//! keyword and every dialect quirk, forever". A regex allowlist is a
//! maintenance liability that must be re-audited every time SQLite gains a
//! new pragma or function; it is also not needed here in a way it arguably
//! was needed for Postgres's much larger surface (COPY, dblink, pg_read_file,
//! LISTEN/NOTIFY, …).
//!
//! ## What replaces it: no SQL generation at all
//!
//! The LLM's job is narrowed from "write a SELECT statement" to "pick one
//! named [`ReportIntent`] from a fixed, closed enum and fill in a handful of
//! bounded scalar parameters (`repo_id`, `days`, `weeks`, `limit`)". There is
//! no code path anywhere in this crate that takes LLM output and hands it to
//! a SQL engine, a query builder, or string interpolation into anything
//! executable. [`parse_intent`] either produces one of nine known,
//! statically-dispatched Rust function calls ([`dispatch`]) or it produces
//! nothing — an `Err`. This is not "a narrower allowlist"; it is a different
//! *kind* of mechanism, one that is structurally incapable of expressing a
//! destructive statement, because a destructive statement is not a value
//! [`ReportIntent`] can hold. `serde`'s internally-tagged-enum deserializer
//! is the enforcement: an unrecognized `intent` tag, an out-of-schema field,
//! or a non-object payload are all deserialize failures, not policy
//! decisions made after the fact.
//!
//! This also incidentally closes a class of leak the Go design still had:
//! `AnswerQuery` could `SELECT` arbitrary columns off the allowlisted tables,
//! including free-text bodies (`issues.title`, `commits.message`, PR
//! rationale) verbatim into the response. Every [`ReportIntent`] here
//! resolves to an aggregate or a small bounded list produced by an existing
//! pure function in `gitstate_core::analytics` — there is no "return every
//! column of every matching row" shape available to ask for.
//!
//! ## The allowlist's replacement, documented here
//!
//! [`SYSTEM_PROMPT`] is this design's equivalent of Go's `allowedTablesCatalog`
//! — the complete, authoritative list of what the LLM may ask for, injected
//! verbatim into its system prompt. Nine intents, each named after the
//! `gitstate_core::analytics` function or `Store` read it resolves to:
//! `state_counts`, `throughput`, `cycle_time`, `burndown`, `recent_activity`,
//! `top_contributors`, `label_breakdown`. (Go's allowlist also named
//! `effort_estimates`, `agent_runs`, and `involvement`; `involvement` was
//! already dropped fleet-wide as SaaS-only in the 2026-08-04 sweep, and this
//! port deliberately narrows further by not exposing effort/agent-run detail
//! through NL→report at all — a smaller answerable surface than Go's, not an
//! oversight; both remain reachable through `gitstate agent runs` and the
//! effort/classify commands directly.) Every intent takes only `repo_id`
//! (an opaque id string, never interpolated into anything — see
//! [`dispatch`]'s doc) plus small bounded integers; there is no field of any
//! intent that accepts free text used for anything other than an equality
//! lookup.
//!
//! ## Refusal is tested, and the guard has been mutation-tested
//!
//! See this module's test suite: malformed JSON, an unrecognized intent tag,
//! a smuggled extra field (e.g. an attempted `"sql"` key), and an
//! out-of-bounds numeric parameter are all rejected before [`dispatch`] is
//! ever called — none of them produce a query. The wave that wrote this
//! module temporarily removed `#[serde(deny_unknown_fields)]` from
//! [`ReportIntent`] and confirmed
//! `parse_intent_rejects_a_smuggled_extra_field` **failed** (the extra field
//! was silently accepted) before restoring it — see
//! `docs/MIGRATION-NOTES.md`'s wave 5 entry for the exact commands and
//! output. A hostile `repo_id` (e.g. containing `'; DROP TABLE work_items;
//! --`) is exercised too: [`dispatch`] treats it as an opaque id that simply
//! matches no repo, per [`ReportIntent`]'s doc.

use serde::{Deserialize, Serialize};

use gitstate_core::{analytics, Error, RepoId, Result, Store};

/// Largest `limit`/`weeks`/`days` any intent will accept. Chosen to match
/// `gitstate_daemon::ops::MAX_ANALYTICS_DAYS` (3653 ≈ 10 years) for `days`;
/// this crate does not depend on `gitstate-daemon` (the dependency runs the
/// other way), so the value is restated here rather than imported — see the
/// module doc's "no SQL generation" point for why a duplicated constant is
/// an acceptable cost and a shared query engine is not.
pub const MAX_DAYS: u32 = 3653;
/// Largest `weeks` parameter ([`ReportIntent::Throughput`]).
pub const MAX_WEEKS: u32 = 104;
/// Largest `limit` parameter ([`ReportIntent::RecentActivity`],
/// [`ReportIntent::TopContributors`]).
pub const MAX_LIMIT: u32 = 200;

/// A fixed, closed set of questions NL→report is allowed to answer. Every
/// variant carries only `repo_id` (an opaque lookup key, see [`dispatch`])
/// plus bounded scalar parameters — never a free-text field used for
/// anything but an equality match. See the module doc for why this replaces
/// SQL generation entirely rather than allowlisting a narrower one.
///
/// `deny_unknown_fields` is load-bearing, not decorative: it is what turns a
/// smuggled extra field (an attempt to widen what a request can carry) into
/// a parse failure instead of a silently-ignored key. See this module's
/// tests and their mutation-testing note.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(tag = "intent", rename_all = "snake_case", deny_unknown_fields)]
pub enum ReportIntent {
    /// Issue state rollup (open/in-progress/done/closed), one repo or all.
    StateCounts { repo_id: Option<String> },
    /// Merged-PR + closed-issue counts per ISO week, most recent `weeks`.
    Throughput {
        repo_id: Option<String>,
        weeks: Option<u32>,
    },
    /// Lead-time (created→merged) trend for merged PRs in the trailing
    /// `days`, plus p50/p90.
    CycleTime {
        repo_id: Option<String>,
        days: Option<u32>,
    },
    /// Daily open-vs-total issue count over the trailing `days`.
    Burndown {
        repo_id: Option<String>,
        days: Option<u32>,
    },
    /// The most recent `limit` issues/PRs/commits, newest first.
    RecentActivity {
        repo_id: Option<String>,
        limit: Option<u32>,
    },
    /// Commit leaderboard over the trailing `days`, top `limit` contributors.
    TopContributors {
        repo_id: Option<String>,
        days: Option<u32>,
        limit: Option<u32>,
    },
    /// Issue/PR label tally, most common first.
    LabelBreakdown { repo_id: Option<String> },
}

impl ReportIntent {
    /// Reject any in-range-shaped-but-out-of-bounds parameter. Runs *after*
    /// a successful deserialize — a value the enum's own types can hold but
    /// that would be an unreasonable ask (`limit: 4000000000`) is still a
    /// rejection, not a silently-clamped success, so the caller (and this
    /// module's tests) can tell "you asked for too much" apart from "that
    /// parameter doesn't exist here".
    fn validate(&self) -> std::result::Result<(), RejectedQuery> {
        fn bounded(name: &str, v: Option<u32>, max: u32) -> std::result::Result<(), RejectedQuery> {
            match v {
                Some(0) => Err(RejectedQuery::new(format!("{name} must be at least 1"))),
                Some(n) if n > max => Err(RejectedQuery::new(format!(
                    "{name} of {n} exceeds the maximum of {max}"
                ))),
                _ => Ok(()),
            }
        }
        match self {
            ReportIntent::StateCounts { .. } => Ok(()),
            ReportIntent::Throughput { weeks, .. } => bounded("weeks", *weeks, MAX_WEEKS),
            ReportIntent::CycleTime { days, .. } => bounded("days", *days, MAX_DAYS),
            ReportIntent::Burndown { days, .. } => bounded("days", *days, MAX_DAYS),
            ReportIntent::RecentActivity { limit, .. } => bounded("limit", *limit, MAX_LIMIT),
            ReportIntent::TopContributors { days, limit, .. } => {
                bounded("days", *days, MAX_DAYS)?;
                bounded("limit", *limit, MAX_LIMIT)
            }
            ReportIntent::LabelBreakdown { .. } => Ok(()),
        }
    }

    fn repo_id(&self) -> Option<&str> {
        match self {
            ReportIntent::StateCounts { repo_id }
            | ReportIntent::Throughput { repo_id, .. }
            | ReportIntent::CycleTime { repo_id, .. }
            | ReportIntent::Burndown { repo_id, .. }
            | ReportIntent::RecentActivity { repo_id, .. }
            | ReportIntent::TopContributors { repo_id, .. }
            | ReportIntent::LabelBreakdown { repo_id } => repo_id.as_deref(),
        }
    }
}

/// A query the safety mechanism refused to run. Carries a human-readable
/// reason for transparency (matching the spirit of Go's own error messages)
/// but never the raw LLM output verbatim, so a rejected response cannot be
/// used to smuggle its own content back out unexamined.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct RejectedQuery {
    pub reason: String,
}

impl RejectedQuery {
    fn new(reason: impl Into<String>) -> Self {
        RejectedQuery {
            reason: reason.into(),
        }
    }
}

impl std::fmt::Display for RejectedQuery {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "NL\u{2192}report query rejected: {}", self.reason)
    }
}

impl std::error::Error for RejectedQuery {}

impl From<RejectedQuery> for Error {
    fn from(e: RejectedQuery) -> Self {
        Error::invalid(e.to_string())
    }
}

/// Parse (and validate the bounds of) one [`ReportIntent`] from raw LLM
/// output. This is the entire enforcement boundary: anything that is not a
/// well-formed, in-bounds, exactly-matching intent is rejected here, before
/// [`dispatch`] is ever reached — no query is built from anything that fails
/// this function.
pub fn parse_intent(raw: &str) -> std::result::Result<ReportIntent, RejectedQuery> {
    let candidate = extract_json_object(raw);
    let intent: ReportIntent = serde_json::from_str(&candidate).map_err(|e| {
        RejectedQuery::new(format!(
            "output did not match a recognized report intent: {e}"
        ))
    })?;
    intent.validate()?;
    Ok(intent)
}

/// Some endpoints wrap JSON in prose or ```` ```json ```` fences despite being
/// instructed not to (mirrors `gitstate_classify`'s `parse_json_array`
/// fence-stripping, adapted for a single object instead of an array).
fn extract_json_object(raw: &str) -> String {
    let trimmed = raw
        .trim()
        .trim_start_matches("```json")
        .trim_start_matches("```")
        .trim_end_matches("```")
        .trim();
    if serde_json::from_str::<serde_json::Value>(trimmed).is_ok() {
        return trimmed.to_string();
    }
    if let (Some(start), Some(end)) = (trimmed.find('{'), trimmed.rfind('}')) {
        if end > start {
            return trimmed[start..=end].to_string();
        }
    }
    trimmed.to_string()
}

// ── dispatch: intent -> data, over structures gitstate_core already has ──

/// Resolve `intent` against `store`. **No SQL is constructed anywhere in this
/// function** — every branch calls an existing read (`Store::list_*`) and an
/// existing pure function (`gitstate_core::analytics::*`), the same calls
/// `gitstate_daemon::ops` already makes for `/api/analytics` and the CLI.
///
/// `repo_id` is looked up by plain equality inside `Store::list_work_items`'s
/// SQL (a parameterised `WHERE repo_id = ?`, never string-built from the
/// intent), so a hostile value — say, one containing `'; DROP TABLE
/// work_items; --` — is just an id that matches no repo: the caller gets an
/// empty result, not an error, and certainly not an executed statement. This
/// module's tests exercise exactly that string.
pub fn dispatch(intent: &ReportIntent, store: &dyn Store) -> Result<serde_json::Value> {
    let repo_id = intent.repo_id().map(|s| RepoId::from(s.to_string()));
    let items = load_items(store, repo_id.as_ref())?;
    let commits = store.list_commits(repo_id.as_ref())?;

    let value = match intent {
        ReportIntent::StateCounts { .. } => {
            let issues: Vec<&str> = items
                .iter()
                .filter(|w| w.kind == gitstate_core::WorkKind::Issue)
                .map(|w| w.state.as_str())
                .collect();
            serde_json::to_value(analytics::tally(issues.into_iter()))?
        }
        ReportIntent::Throughput { weeks, .. } => {
            let weeks = weeks.unwrap_or(12).clamp(1, MAX_WEEKS);
            let (from, to) = all_time_window();
            let series = analytics::throughput(&items, &from, &to);
            let tail = series.len().saturating_sub(weeks as usize);
            serde_json::to_value(&series[tail..])?
        }
        ReportIntent::CycleTime { days, .. } => {
            let (from, to) = trailing_window(*days, 90)?;
            let series = analytics::cycle_times(&items, &from, &to);
            let mut hours: Vec<f64> = series.iter().map(|p| p.hours).collect();
            hours.sort_by(|a, b| a.partial_cmp(b).unwrap_or(std::cmp::Ordering::Equal));
            serde_json::json!({
                "points": series,
                "p50_hours": analytics::percentile(&hours, 0.5),
                "p90_hours": analytics::percentile(&hours, 0.9),
            })
        }
        ReportIntent::Burndown { days, .. } => {
            let (from, to) = trailing_window(*days, 30)?;
            serde_json::to_value(analytics::burndown(&items, &from, &to))?
        }
        ReportIntent::RecentActivity { limit, .. } => {
            let limit = limit.unwrap_or(20).clamp(1, MAX_LIMIT) as usize;
            serde_json::to_value(analytics::recent_activity(&items, &commits, limit))?
        }
        ReportIntent::TopContributors { days, limit, .. } => {
            let (from, to) = trailing_window(*days, 90)?;
            let known = store.list_contributors()?;
            let limit = limit.unwrap_or(10).clamp(1, MAX_LIMIT) as usize;
            let mut stats = analytics::contributor_stats(&commits, &known, &from, &to);
            stats.truncate(limit);
            serde_json::to_value(stats)?
        }
        ReportIntent::LabelBreakdown { .. } => serde_json::to_value(analytics::tally(
            items
                .iter()
                .flat_map(|w| w.labels.iter().map(|s| s.as_str())),
        ))?,
    };
    Ok(value)
}

fn load_items(store: &dyn Store, repo_id: Option<&RepoId>) -> Result<Vec<gitstate_core::WorkItem>> {
    match repo_id {
        Some(r) => store.list_work_items(r),
        None => store.list_all_work_items(),
    }
}

/// A window wide enough to include everything this machine has scanned, for
/// intents (like [`ReportIntent::Throughput`]) that want "the last N weeks
/// that have data" rather than "the last N weeks of wall-clock time" — the
/// tail of the resulting series is what actually gets returned.
fn all_time_window() -> (String, String) {
    ("0001-01-01".to_string(), "9999-12-31".to_string())
}

/// `days` back from *today* (wall clock), clamped to [`MAX_DAYS`]. `default`
/// is used when the caller left `days` unset.
fn trailing_window(days: Option<u32>, default: u32) -> Result<(String, String)> {
    let days = days.unwrap_or(default).clamp(1, MAX_DAYS);
    let today = {
        let now = time::OffsetDateTime::now_utc();
        format!(
            "{:04}-{:02}-{:02}",
            now.year(),
            now.month() as u8,
            now.day()
        )
    };
    analytics::range_ending(&today, days)
        .ok_or_else(|| Error::invalid("could not compute a trailing date window"))
}

/// The system prompt handed to the LLM verbatim — this design's equivalent
/// of Go's `allowedTablesCatalog`: the complete, authoritative statement of
/// what may be asked for. See the module doc for why the shape changed from
/// "these tables/columns" to "these named intents".
pub const SYSTEM_PROMPT: &str = r#"You answer questions about a local, single-user software project tracker by choosing ONE named intent and filling in its parameters. You never write SQL, and you never invent a field that is not listed below.

Reply with ONLY a single JSON object, no markdown fences, no prose, matching exactly one of:

{"intent":"state_counts","repo_id":"<optional repo id>"}
{"intent":"throughput","repo_id":"<optional>","weeks":<optional int, 1-104>}
{"intent":"cycle_time","repo_id":"<optional>","days":<optional int, 1-3653>}
{"intent":"burndown","repo_id":"<optional>","days":<optional int, 1-3653>}
{"intent":"recent_activity","repo_id":"<optional>","limit":<optional int, 1-200>}
{"intent":"top_contributors","repo_id":"<optional>","days":<optional int>,"limit":<optional int, 1-200>}
{"intent":"label_breakdown","repo_id":"<optional>"}

Rules:
- Every field not shown above is forbidden — do not add "sql", "query", or any other key.
- repo_id, when given, is an opaque identifier; you do not need to know real repo ids to answer generically (omit it to mean "all repos").
- If the question cannot be answered by any intent above, reply exactly: {"intent":"unanswerable"}
"#;

/// The system prompt for the second, best-effort call that turns a
/// dispatched result into 1-3 sentences of prose. Given only the already
///-computed aggregate (never raw row content, never SQL), matching Go's own
/// "no technical details" instruction.
pub const PROSE_SYSTEM_PROMPT: &str = "You are a helpful data analyst summarising a small JSON result for a software project lead. Given the original question and the JSON result, write a clear, concise answer in 1-3 sentences. Do not mention JSON, field names, or technical details — describe what the data means.";

/// The result of a fully-resolved NL→report question: which intent was
/// selected, the structured data [`dispatch`] produced, and (best-effort) a
/// prose summary.
#[derive(Debug, Clone, Serialize)]
pub struct NlAnswer {
    pub intent: ReportIntent,
    pub data: serde_json::Value,
    /// `None` when prose synthesis failed or was skipped — never fatal, the
    /// structured `data` is always present on success.
    pub prose: Option<String>,
}

/// Translate `question` into an intent, dispatch it, and (best-effort)
/// synthesize prose — the full NL→report round trip. Returns an error
/// immediately (not best-effort) if no LLM is configured or the question is
/// empty: unlike status synthesis, a "translate the question" step that
/// cannot run has nothing useful to fall back to.
pub async fn answer_question(
    llm: &gitstate_classify::LlmClassifier,
    store: &dyn Store,
    question: &str,
) -> Result<NlAnswer> {
    let question = question.trim();
    if question.is_empty() {
        return Err(Error::invalid("NL\u{2192}report: question is required"));
    }
    let raw = llm.chat(SYSTEM_PROMPT, question).await?;
    let intent = parse_intent(&raw)?;
    let data = dispatch(&intent, store)?;
    let prose = synthesize_prose(llm, question, &data).await.ok();
    Ok(NlAnswer {
        intent,
        data,
        prose,
    })
}

async fn synthesize_prose(
    llm: &gitstate_classify::LlmClassifier,
    question: &str,
    data: &serde_json::Value,
) -> Result<String> {
    let user = format!("Question: {question}\n\nResult:\n{data}");
    let text = llm.chat(PROSE_SYSTEM_PROMPT, &user).await?;
    Ok(text.trim().to_string())
}

#[cfg(test)]
mod tests {
    use super::*;

    // ── the refusal tests: nothing here ever reaches `dispatch` ──

    #[test]
    fn parse_intent_accepts_every_documented_shape() {
        for raw in [
            r#"{"intent":"state_counts"}"#,
            r#"{"intent":"state_counts","repo_id":"r1"}"#,
            r#"{"intent":"throughput","weeks":8}"#,
            r#"{"intent":"cycle_time","days":30}"#,
            r#"{"intent":"burndown","repo_id":"r1","days":14}"#,
            r#"{"intent":"recent_activity","limit":5}"#,
            r#"{"intent":"top_contributors","days":30,"limit":3}"#,
            r#"{"intent":"label_breakdown"}"#,
        ] {
            parse_intent(raw).unwrap_or_else(|e| panic!("{raw} should parse: {e}"));
        }
    }

    #[test]
    fn parse_intent_strips_markdown_fences() {
        let raw = "```json\n{\"intent\":\"state_counts\"}\n```";
        assert!(parse_intent(raw).is_ok());
    }

    #[test]
    fn parse_intent_rejects_an_unrecognized_intent() {
        let err = parse_intent(r#"{"intent":"drop_all_tables"}"#).unwrap_err();
        assert!(err.reason.contains("not a recognized") || err.reason.contains("did not match"));
    }

    #[test]
    fn parse_intent_rejects_free_form_sql_instead_of_json() {
        let err = parse_intent("SELECT * FROM sqlite_master; DROP TABLE work_items;").unwrap_err();
        assert!(!err.reason.is_empty());
    }

    #[test]
    fn parse_intent_rejects_a_smuggled_extra_field() {
        // A hostile or confused model tries to widen the schema with an
        // extra key. `deny_unknown_fields` is what catches this — see the
        // module doc's mutation-testing note for proof this assertion is
        // load-bearing, not decorative.
        let raw = r#"{"intent":"recent_activity","limit":5,"sql":"DROP TABLE work_items"}"#;
        let err = parse_intent(raw).unwrap_err();
        assert!(!err.reason.is_empty());
    }

    #[test]
    fn parse_intent_rejects_an_out_of_bounds_limit() {
        let err = parse_intent(r#"{"intent":"recent_activity","limit":999999999}"#).unwrap_err();
        assert!(err.reason.contains("limit"), "reason was: {}", err.reason);
    }

    #[test]
    fn parse_intent_rejects_zero_as_a_limit() {
        let err = parse_intent(r#"{"intent":"top_contributors","limit":0}"#).unwrap_err();
        assert!(err.reason.contains("limit"), "reason was: {}", err.reason);
    }

    #[test]
    fn parse_intent_rejects_an_empty_payload() {
        assert!(parse_intent("").is_err());
        assert!(parse_intent("   ").is_err());
    }

    #[test]
    fn parse_intent_rejects_the_models_own_explicit_escape_hatch() {
        // The prompt tells the model to emit this for an unanswerable
        // question. It is intentionally NOT a variant of `ReportIntent` —
        // there is no dispatch-nothing/echo-back intent — so this, too, is a
        // parse rejection, not a special-cased success.
        let err = parse_intent(r#"{"intent":"unanswerable"}"#).unwrap_err();
        assert!(!err.reason.is_empty());
    }

    // ── containment: a hostile repo_id cannot do anything but miss ──

    #[test]
    fn a_hostile_repo_id_is_just_an_id_that_matches_nothing() {
        use gitstate_store::SqliteStore;
        let store = SqliteStore::open_in_memory().unwrap();
        let intent = ReportIntent::RecentActivity {
            repo_id: Some("'; DROP TABLE work_items; --".to_string()),
            limit: Some(10),
        };
        let value = dispatch(&intent, &store).expect("a hostile id must not error, only miss");
        assert_eq!(value, serde_json::json!([]));
        // The table is still there and still queryable — nothing executed.
        assert!(store.list_repos().is_ok());
    }

    #[test]
    fn dispatch_computes_state_counts_over_a_real_store() {
        use gitstate_core::{RepoId, WorkItem, WorkItemId, WorkKind, WorkState};
        use gitstate_store::SqliteStore;
        let store = SqliteStore::open_in_memory().unwrap();
        let repo = gitstate_core::Repo {
            id: RepoId::new(),
            slug: "demo".into(),
            path: "/tmp/demo".into(),
            remote_url: None,
            forge: gitstate_core::Forge::Local,
            default_branch: "main".into(),
            last_scanned_at: None,
            added_at: "2026-01-01T00:00:00Z".into(),
        };
        store.upsert_repo(&repo).unwrap();
        store
            .save_work_items(&[WorkItem {
                id: WorkItemId::new(),
                repo_id: repo.id.clone(),
                kind: WorkKind::Issue,
                external_ref: "#1".into(),
                title: "t".into(),
                body: String::new(),
                state: WorkState::Open,
                author_login: None,
                labels: vec![],
                created_at: "2026-01-01T00:00:00Z".into(),
                updated_at: "2026-01-01T00:00:00Z".into(),
                merged_at: None,
                closed_at: None,
                files_touched: vec![],
            }])
            .unwrap();

        let intent = ReportIntent::StateCounts {
            repo_id: Some(repo.id.0.clone()),
        };
        let value = dispatch(&intent, &store).unwrap();
        assert_eq!(value, serde_json::json!([{"key": "open", "count": 1}]));
    }
}
