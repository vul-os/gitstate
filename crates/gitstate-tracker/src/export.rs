//! Offline import from a Jira or Linear **export file**.
//!
//! Not every setup can issue a token — an air-gapped machine, a locked-down
//! Jira Server/DC, or simply someone who would rather not paste a credential.
//! Both products export CSV (and Jira exports JSON), so this path turns those
//! files into the same [`ImportedItem`]s the API clients produce. Nothing here
//! touches the network.

use gitstate_core::{Error, Result, WorkState};

use crate::map::{adf_to_text, state_from_jira_category, state_from_linear_type, ImportedItem};

/// Parse an export, sniffing JSON vs CSV and Jira vs Linear from the content.
///
/// `source_hint` ("jira" / "linear") disambiguates a CSV whose headers could
/// belong to either; it is advisory, and clear evidence in the file wins.
pub fn from_export(raw: &str, source_hint: Option<&str>) -> Result<Vec<ImportedItem>> {
    let trimmed = raw.trim_start();
    if trimmed.is_empty() {
        return Err(Error::invalid("the export file is empty"));
    }
    if trimmed.starts_with('{') || trimmed.starts_with('[') {
        return from_json(trimmed);
    }
    from_csv(raw, source_hint)
}

// ─────────────────────────────── JSON ───────────────────────────────

/// Jira's JSON export is `{"issues":[…]}`; a raw `[…]` array is also accepted.
fn from_json(raw: &str) -> Result<Vec<ImportedItem>> {
    let value: serde_json::Value =
        serde_json::from_str(raw).map_err(|e| Error::invalid(format!("bad JSON export: {e}")))?;

    let issues = match &value {
        serde_json::Value::Array(a) => a.clone(),
        serde_json::Value::Object(o) => o
            .get("issues")
            .and_then(|v| v.as_array())
            .cloned()
            .ok_or_else(|| Error::invalid("JSON export has no `issues` array"))?,
        _ => return Err(Error::invalid("unrecognized JSON export shape")),
    };

    let mut out = Vec::with_capacity(issues.len());
    for issue in issues {
        let key = issue
            .get("key")
            .or_else(|| issue.get("identifier"))
            .and_then(|v| v.as_str())
            .unwrap_or_default()
            .to_string();
        if key.is_empty() {
            continue; // no stable identity ⇒ nothing we could update in place
        }
        let f = issue.get("fields").unwrap_or(&issue);
        let s = |k: &str| f.get(k).and_then(|v| v.as_str()).unwrap_or("").to_string();

        let category = f
            .get("status")
            .and_then(|st| st.get("statusCategory"))
            .and_then(|c| c.get("key"))
            .and_then(|v| v.as_str())
            .unwrap_or("new");

        let labels = f
            .get("labels")
            .and_then(|v| v.as_array())
            .map(|a| {
                a.iter()
                    .filter_map(|x| x.as_str().map(String::from))
                    .collect()
            })
            .unwrap_or_default();

        let resolution = f
            .get("resolutiondate")
            .and_then(|v| v.as_str())
            .map(String::from);

        out.push(ImportedItem {
            source: "jira".into(),
            key,
            title: {
                let t = s("summary");
                if t.is_empty() {
                    s("title")
                } else {
                    t
                }
            },
            body: f.get("description").map(adf_to_text).unwrap_or_default(),
            state: state_from_jira_category(category),
            author: f
                .get("assignee")
                .and_then(|a| a.get("displayName").or_else(|| a.get("emailAddress")))
                .and_then(|v| v.as_str())
                .map(String::from),
            labels,
            created_at: s("created"),
            updated_at: s("updated"),
            closed_at: resolution,
            url: None,
        });
    }
    Ok(out)
}

// ─────────────────────────────── CSV ───────────────────────────────

/// A minimal RFC4180 reader: quoted fields, embedded commas/newlines, and
/// `""` escapes. A dependency-free parser is worth it here — the alternative is
/// pulling a CSV crate into the workspace for one file.
fn parse_csv(raw: &str) -> Vec<Vec<String>> {
    let mut rows = Vec::new();
    let mut row: Vec<String> = Vec::new();
    let mut field = String::new();
    let mut in_quotes = false;
    let mut chars = raw.chars().peekable();

    while let Some(c) = chars.next() {
        if in_quotes {
            if c == '"' {
                if chars.peek() == Some(&'"') {
                    chars.next();
                    field.push('"');
                } else {
                    in_quotes = false;
                }
            } else {
                field.push(c);
            }
            continue;
        }
        match c {
            '"' => in_quotes = true,
            ',' => row.push(std::mem::take(&mut field)),
            '\r' => {}
            '\n' => {
                row.push(std::mem::take(&mut field));
                rows.push(std::mem::take(&mut row));
            }
            _ => field.push(c),
        }
    }
    if !field.is_empty() || !row.is_empty() {
        row.push(field);
        rows.push(row);
    }
    rows.retain(|r| !(r.len() == 1 && r[0].trim().is_empty()));
    rows
}

/// Find the first header whose lowercased name matches any candidate.
fn col(headers: &[String], candidates: &[&str]) -> Option<usize> {
    headers.iter().position(|h| {
        let h = h.trim().to_ascii_lowercase();
        candidates.iter().any(|c| h == *c)
    })
}

fn from_csv(raw: &str, source_hint: Option<&str>) -> Result<Vec<ImportedItem>> {
    let rows = parse_csv(raw);
    let Some(headers) = rows.first().cloned() else {
        return Err(Error::invalid("the CSV export has no header row"));
    };

    // Linear exports an "ID"/"Identifier" column; Jira uses "Issue key".
    let key_col = col(&headers, &["issue key", "key", "identifier", "id"])
        .ok_or_else(|| Error::invalid("CSV export has no issue key column"))?;
    let title_col = col(&headers, &["summary", "title"])
        .ok_or_else(|| Error::invalid("CSV export has no summary/title column"))?;

    let desc_col = col(&headers, &["description"]);
    let status_col = col(&headers, &["status", "state"]);
    let created_col = col(&headers, &["created", "created at", "createdat"]);
    let updated_col = col(&headers, &["updated", "updated at", "updatedat"]);
    let closed_col = col(
        &headers,
        &["resolved", "resolutiondate", "completed at", "completedat"],
    );
    let assignee_col = col(&headers, &["assignee", "assignee name"]);
    let labels_col = col(&headers, &["labels", "label"]);

    let source = match source_hint.map(|s| s.to_ascii_lowercase()).as_deref() {
        Some("linear") => "linear",
        Some("jira") => "jira",
        // No hint: Linear's export is the one with an "Identifier" column.
        _ if col(&headers, &["identifier"]).is_some() => "linear",
        _ => "jira",
    };

    let get = |row: &[String], idx: Option<usize>| -> String {
        idx.and_then(|i| row.get(i)).cloned().unwrap_or_default()
    };

    let mut out = Vec::new();
    for row in rows.iter().skip(1) {
        let key = row.get(key_col).cloned().unwrap_or_default();
        if key.trim().is_empty() {
            continue;
        }
        let status_raw = get(row, status_col);
        let state = state_from_csv_status(&status_raw, source);
        let closed = get(row, closed_col);

        out.push(ImportedItem {
            source: source.to_string(),
            key: key.trim().to_string(),
            title: row.get(title_col).cloned().unwrap_or_default(),
            body: get(row, desc_col),
            state,
            author: Some(get(row, assignee_col)).filter(|s| !s.trim().is_empty()),
            labels: get(row, labels_col)
                .split(&[',', ';'][..])
                .map(|s| s.trim().to_string())
                .filter(|s| !s.is_empty())
                .collect(),
            created_at: get(row, created_col),
            updated_at: get(row, updated_col),
            closed_at: Some(closed).filter(|s| !s.trim().is_empty()),
            url: None,
        });
    }
    Ok(out)
}

/// A CSV export carries the *display* status, not the category, so this is a
/// best-effort name match — the one place the import is necessarily fuzzy.
fn state_from_csv_status(status: &str, source: &str) -> WorkState {
    let s = status.trim().to_ascii_lowercase();
    if s.is_empty() {
        return WorkState::Open;
    }
    // Linear CSVs sometimes carry the state type verbatim.
    if source == "linear" {
        match s.as_str() {
            "completed" | "started" | "backlog" | "unstarted" | "canceled" | "cancelled"
            | "triage" => return state_from_linear_type(&s),
            _ => {}
        }
    }
    match s.as_str() {
        "done" | "closed" | "resolved" | "complete" | "completed" | "shipped" | "released" => {
            WorkState::Done
        }
        "cancelled" | "canceled" | "won't do" | "wont do" | "duplicate" | "rejected" => {
            WorkState::Closed
        }
        "in progress" | "in-progress" | "in review" | "started" | "doing" => WorkState::InProgress,
        _ => WorkState::Open,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rejects_an_empty_file() {
        assert!(from_export("   ", None).is_err());
    }

    #[test]
    fn parses_a_jira_json_export() {
        let raw = r#"{"issues":[{
            "key":"ENG-1",
            "fields":{
                "summary":"Stale search",
                "description":{"type":"doc","content":[{"type":"paragraph","content":[
                    {"type":"text","text":"Cache bug."}]}]},
                "status":{"statusCategory":{"key":"done"}},
                "labels":["bug"],
                "created":"2026-05-01T09:00:00.000+0000",
                "updated":"2026-06-01T09:00:00.000+0000",
                "resolutiondate":"2026-06-01T09:00:00.000+0000",
                "assignee":{"displayName":"Ada"}
            }}]}"#;
        let items = from_export(raw, None).unwrap();
        assert_eq!(items.len(), 1);
        assert_eq!(items[0].key, "ENG-1");
        assert_eq!(items[0].title, "Stale search");
        assert_eq!(items[0].body, "Cache bug.");
        assert_eq!(items[0].state, WorkState::Done);
        assert_eq!(items[0].labels, vec!["bug".to_string()]);
        assert_eq!(items[0].author.as_deref(), Some("Ada"));
        assert!(items[0].closed_at.is_some());
    }

    #[test]
    fn json_export_skips_rows_with_no_key() {
        let raw = r#"[{"fields":{"summary":"orphan"}},{"key":"E-2","fields":{"summary":"ok"}}]"#;
        let items = from_export(raw, None).unwrap();
        assert_eq!(items.len(), 1);
        assert_eq!(items[0].key, "E-2");
    }

    #[test]
    fn csv_reader_handles_quotes_commas_and_newlines() {
        let raw = "a,b\n\"x,1\",\"line\nbreak\"\n\"say \"\"hi\"\"\",z\n";
        let rows = parse_csv(raw);
        assert_eq!(rows[0], vec!["a", "b"]);
        assert_eq!(rows[1], vec!["x,1", "line\nbreak"]);
        assert_eq!(rows[2], vec!["say \"hi\"", "z"]);
    }

    #[test]
    fn parses_a_jira_csv_export() {
        let raw = "Issue key,Summary,Status,Assignee,Labels,Created,Updated,Resolved\n\
                   ENG-7,Fix the parser,In Progress,Ada,\"bug,parser\",2026-05-01,2026-06-01,\n";
        let items = from_export(raw, Some("jira")).unwrap();
        assert_eq!(items.len(), 1);
        assert_eq!(items[0].source, "jira");
        assert_eq!(items[0].key, "ENG-7");
        assert_eq!(items[0].state, WorkState::InProgress);
        assert_eq!(
            items[0].labels,
            vec!["bug".to_string(), "parser".to_string()]
        );
        assert!(items[0].closed_at.is_none(), "blank Resolved ⇒ still open");
    }

    #[test]
    fn parses_a_linear_csv_export_and_sniffs_the_source() {
        let raw = "Identifier,Title,Status,Assignee,Labels,Created At,Completed At\n\
                   ENG-9,Ship it,completed,ada@example.com,bug,2026-05-01,2026-06-02\n";
        // No hint given: the "Identifier" header identifies it as Linear.
        let items = from_export(raw, None).unwrap();
        assert_eq!(items[0].source, "linear");
        assert_eq!(items[0].state, WorkState::Done);
        assert_eq!(items[0].closed_at.as_deref(), Some("2026-06-02"));
        assert_eq!(items[0].author.as_deref(), Some("ada@example.com"));
    }

    #[test]
    fn csv_status_names_map_including_cancelled() {
        assert_eq!(state_from_csv_status("Done", "jira"), WorkState::Done);
        assert_eq!(state_from_csv_status("Resolved", "jira"), WorkState::Done);
        // Cancelled is closed, not done — it must not inflate delivery counts.
        assert_eq!(state_from_csv_status("Won't Do", "jira"), WorkState::Closed);
        assert_eq!(
            state_from_csv_status("In Review", "jira"),
            WorkState::InProgress
        );
        assert_eq!(state_from_csv_status("Backlog", "jira"), WorkState::Open);
        assert_eq!(state_from_csv_status("", "jira"), WorkState::Open);
        assert_eq!(
            state_from_csv_status("canceled", "linear"),
            WorkState::Closed
        );
    }

    #[test]
    fn csv_without_a_key_column_is_a_clear_error() {
        let err = from_export("Summary,Status\nx,Done\n", Some("jira")).unwrap_err();
        assert!(err.to_string().contains("issue key"), "{err}");
    }
}
