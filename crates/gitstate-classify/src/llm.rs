//! The LLM-backed classifier: any OpenAI-compatible chat endpoint (llmux, a
//! local model, OpenAI itself). Everything stays on the user's machine unless
//! they point it at a remote endpoint. On any transport/parse failure it falls
//! back to the deterministic heuristic so classification never hard-fails.

use async_trait::async_trait;
use gitstate_core::{
    Classification, Classifier, ClassifierCapability, DiffSummary, EffortEstimate, EffortMethod,
    Error, Result, Taxonomy, WorkItem,
};
use serde::Deserialize;
use serde_json::json;

use crate::heuristic::{classify_all, effort_all, HeuristicClassifier};

#[async_trait]
impl Classifier for LlmClassifier {
    fn capability(&self) -> ClassifierCapability {
        ClassifierCapability::Llm
    }
    async fn classify(
        &self,
        items: &[WorkItem],
        taxonomy: &Taxonomy,
    ) -> Result<Vec<Classification>> {
        // Never hard-fail: a flaky endpoint falls back to the deterministic path.
        match self.classify_llm(items, taxonomy).await {
            Ok(v) => Ok(v),
            Err(_) => Ok(classify_all(&self.fallback, items, taxonomy)),
        }
    }
    async fn judge_effort(&self, diffs: &[DiffSummary]) -> Result<Vec<EffortEstimate>> {
        match self.effort_llm(diffs).await {
            Ok(v) => Ok(v),
            Err(_) => Ok(effort_all(diffs)),
        }
    }
}

/// An OpenAI-compatible chat classifier.
pub struct LlmClassifier {
    base_url: String,
    api_key: Option<String>,
    model: String,
    http: reqwest::Client,
    fallback: HeuristicClassifier,
}

impl LlmClassifier {
    /// Build from the environment. Returns `None` when no endpoint is set, so
    /// callers can cleanly drop back to the heuristic.
    ///
    /// - `VULOS_LLMUX_URL` or `OPENAI_BASE_URL` — the base URL (required)
    /// - `VULOS_LLMUX_API_KEY` / `OPENAI_API_KEY` / `GITSTATE_CLASSIFY_API_KEY`
    /// - `GITSTATE_CLASSIFY_MODEL` — model id (default `gpt-4o-mini`)
    pub fn from_env() -> Option<Self> {
        let base_url = std::env::var("VULOS_LLMUX_URL")
            .ok()
            .filter(|s| !s.is_empty())
            .or_else(|| {
                std::env::var("OPENAI_BASE_URL")
                    .ok()
                    .filter(|s| !s.is_empty())
            })?;
        let api_key = std::env::var("VULOS_LLMUX_API_KEY")
            .ok()
            .or_else(|| std::env::var("OPENAI_API_KEY").ok())
            .or_else(|| std::env::var("GITSTATE_CLASSIFY_API_KEY").ok())
            .filter(|s| !s.is_empty());
        let model = std::env::var("GITSTATE_CLASSIFY_MODEL")
            .ok()
            .filter(|s| !s.is_empty())
            .unwrap_or_else(|| "gpt-4o-mini".to_string());
        Some(LlmClassifier {
            base_url: base_url.trim_end_matches('/').to_string(),
            api_key,
            model,
            http: reqwest::Client::new(),
            fallback: HeuristicClassifier::new(),
        })
    }

    /// Explicit constructor (tests, embedding).
    pub fn new(
        base_url: impl Into<String>,
        api_key: Option<String>,
        model: impl Into<String>,
    ) -> Self {
        LlmClassifier {
            base_url: base_url.into().trim_end_matches('/').to_string(),
            api_key,
            model: model.into(),
            http: reqwest::Client::new(),
            fallback: HeuristicClassifier::new(),
        }
    }

    async fn chat(&self, system: &str, user: &str) -> Result<String> {
        let url = format!("{}/chat/completions", self.base_url);
        let body = json!({
            "model": self.model,
            "temperature": 0,
            "messages": [
                { "role": "system", "content": system },
                { "role": "user", "content": user },
            ],
        });
        let mut req = self.http.post(&url).json(&body);
        if let Some(key) = &self.api_key {
            req = req.bearer_auth(key);
        }
        let resp = req.send().await.map_err(Error::http)?;
        if !resp.status().is_success() {
            return Err(Error::classify(format!(
                "endpoint returned {}",
                resp.status()
            )));
        }
        let parsed: ChatResponse = resp.json().await.map_err(Error::http)?;
        parsed
            .choices
            .into_iter()
            .next()
            .map(|c| c.message.content)
            .ok_or_else(|| Error::classify("empty completion"))
    }

    pub(crate) async fn classify_llm(
        &self,
        items: &[WorkItem],
        taxonomy: &Taxonomy,
    ) -> Result<Vec<Classification>> {
        if items.is_empty() {
            return Ok(vec![]);
        }
        let cats: Vec<String> = taxonomy
            .categories
            .iter()
            .map(|c| format!("- {} : {}", c.key, c.label))
            .collect();
        let system = format!(
            "You classify software work items into exactly one category KEY from this taxonomy:\n{}\n\
             Reply ONLY with a compact JSON array of objects \
             {{\"ref\":\"<external_ref>\",\"key\":\"<category key>\",\"confidence\":0..1,\"why\":\"short\"}}.",
            cats.join("\n")
        );
        let items_json: Vec<_> = items
            .iter()
            .map(|i| {
                json!({
                    "ref": i.external_ref,
                    "kind": i.kind.as_str(),
                    "title": i.title,
                    "body": truncate(&i.body, 800),
                    "labels": i.labels,
                    "files": i.files_touched.iter().take(20).collect::<Vec<_>>(),
                })
            })
            .collect();
        let user = serde_json::to_string(&items_json)?;

        let raw = self.chat(&system, &user).await?;
        let arr: Vec<LlmClass> = parse_json_array(&raw)?;

        let valid_keys: Vec<&str> = taxonomy.categories.iter().map(|c| c.key.as_str()).collect();
        let mut out = Vec::with_capacity(items.len());
        for item in items {
            match arr.iter().find(|r| r.reference == item.external_ref) {
                Some(r) if valid_keys.contains(&r.key.as_str()) => out.push(Classification {
                    item_id: item.id.clone(),
                    category_key: r.key.clone(),
                    confidence: r.confidence.unwrap_or(0.6).clamp(0.0, 1.0),
                    method: EffortMethod::LlmJudged,
                    rationale: r.why.clone().unwrap_or_default(),
                }),
                // Unknown or missing → deterministic fallback for that item.
                _ => out.push(self.fallback.classify_one(item, taxonomy)),
            }
        }
        Ok(out)
    }

    pub(crate) async fn effort_llm(&self, diffs: &[DiffSummary]) -> Result<Vec<EffortEstimate>> {
        if diffs.is_empty() {
            return Ok(vec![]);
        }
        let system = "You are a senior engineer estimating change DIFFICULTY (not size) on a \
             fibonacci scale [1,2,3,5,8,13], reading only the diff shape and description. \
             Reply ONLY with a compact JSON array of \
             {\"ref\":\"<external_ref>\",\"difficulty\":<one of 1,2,3,5,8,13>,\"confidence\":0..1,\"why\":\"short\"}.";
        let diffs_json: Vec<_> = diffs
            .iter()
            .map(|d| {
                json!({
                    "ref": d.external_ref,
                    "title": d.title,
                    "body": truncate(&d.body, 600),
                    "additions": d.additions,
                    "deletions": d.deletions,
                    "files": d.files,
                    "languages": d.languages,
                    "paths": d.touched_paths.iter().take(20).collect::<Vec<_>>(),
                })
            })
            .collect();
        let user = serde_json::to_string(&diffs_json)?;

        let raw = self.chat(system, &user).await?;
        let arr: Vec<LlmEffort> = parse_json_array(&raw)?;

        let mut out = Vec::with_capacity(diffs.len());
        for d in diffs {
            match arr.iter().find(|r| r.reference == d.external_ref) {
                Some(r) => out.push(EffortEstimate {
                    item_id: d.item_id.clone(),
                    difficulty: snap_fib(r.difficulty),
                    method: EffortMethod::LlmJudged,
                    rationale: r.why.clone().unwrap_or_default(),
                    confidence: r.confidence.unwrap_or(0.6).clamp(0.0, 1.0),
                }),
                None => out.push(crate::effort::heuristic_effort(d)),
            }
        }
        Ok(out)
    }
}

// ── wire types ──

#[derive(Deserialize)]
struct ChatResponse {
    choices: Vec<Choice>,
}
#[derive(Deserialize)]
struct Choice {
    message: ChatMessage,
}
#[derive(Deserialize)]
struct ChatMessage {
    content: String,
}

#[derive(Deserialize)]
struct LlmClass {
    #[serde(rename = "ref")]
    reference: String,
    key: String,
    confidence: Option<f64>,
    why: Option<String>,
}
#[derive(Deserialize)]
struct LlmEffort {
    #[serde(rename = "ref")]
    reference: String,
    difficulty: f64,
    confidence: Option<f64>,
    why: Option<String>,
}

/// Some endpoints wrap the JSON in prose or ```json fences; extract the array.
fn parse_json_array<T: serde::de::DeserializeOwned>(raw: &str) -> Result<Vec<T>> {
    let trimmed = raw
        .trim()
        .trim_start_matches("```json")
        .trim_start_matches("```")
        .trim_end_matches("```")
        .trim();
    if let Ok(v) = serde_json::from_str::<Vec<T>>(trimmed) {
        return Ok(v);
    }
    // Fall back to slicing from the first '[' to the last ']'.
    if let (Some(start), Some(end)) = (trimmed.find('['), trimmed.rfind(']')) {
        if end > start {
            if let Ok(v) = serde_json::from_str::<Vec<T>>(&trimmed[start..=end]) {
                return Ok(v);
            }
        }
    }
    Err(Error::classify(
        "could not parse JSON array from completion",
    ))
}

fn snap_fib(v: f64) -> f64 {
    const FIB: [f64; 6] = [1.0, 2.0, 3.0, 5.0, 8.0, 13.0];
    FIB.into_iter()
        .min_by(|a, b| (a - v).abs().partial_cmp(&(b - v).abs()).unwrap())
        .unwrap_or(1.0)
}

fn truncate(s: &str, max: usize) -> String {
    if s.len() <= max {
        s.to_string()
    } else {
        let mut end = max;
        while !s.is_char_boundary(end) && end > 0 {
            end -= 1;
        }
        format!("{}…", &s[..end])
    }
}
