//! Small shared helpers for the git derivation crate.

use gitstate_core::Contributor;
use time::format_description::well_known::Rfc3339;
use time::OffsetDateTime;

/// Convert a git epoch-seconds timestamp to an RFC3339 string.
pub fn epoch_to_rfc3339(secs: i64) -> String {
    OffsetDateTime::from_unix_timestamp(secs)
        .ok()
        .and_then(|dt| dt.format(&Rfc3339).ok())
        .unwrap_or_else(|| "1970-01-01T00:00:00Z".to_string())
}

/// Parse an RFC3339 string to epoch seconds (for since-filters).
pub fn rfc3339_to_epoch(s: &str) -> Option<i64> {
    OffsetDateTime::parse(s, &Rfc3339)
        .ok()
        .map(|dt| dt.unix_timestamp())
}

/// Difference in hours between two RFC3339 timestamps (`to - from`), if both
/// parse and `to >= from`.
pub fn hours_between(from: &str, to: &str) -> Option<f64> {
    let a = OffsetDateTime::parse(from, &Rfc3339).ok()?;
    let b = OffsetDateTime::parse(to, &Rfc3339).ok()?;
    let secs = (b - a).whole_seconds();
    if secs < 0 {
        None
    } else {
        Some(secs as f64 / 3600.0)
    }
}

/// Map a file path to a coarse language label by extension.
pub fn ext_to_lang(path: &str) -> Option<&'static str> {
    let ext = path.rsplit('.').next()?.to_lowercase();
    Some(match ext.as_str() {
        "rs" => "Rust",
        "go" => "Go",
        "js" | "mjs" | "cjs" => "JavaScript",
        "jsx" => "JavaScript",
        "ts" => "TypeScript",
        "tsx" => "TypeScript",
        "py" => "Python",
        "rb" => "Ruby",
        "java" => "Java",
        "kt" | "kts" => "Kotlin",
        "c" | "h" => "C",
        "cc" | "cpp" | "cxx" | "hpp" => "C++",
        "cs" => "C#",
        "php" => "PHP",
        "swift" => "Swift",
        "scala" => "Scala",
        "sh" | "bash" | "zsh" => "Shell",
        "sql" => "SQL",
        "css" | "scss" | "sass" | "less" => "CSS",
        "html" | "htm" => "HTML",
        "md" | "mdx" => "Markdown",
        "json" => "JSON",
        "yaml" | "yml" => "YAML",
        "toml" => "TOML",
        _ => return None,
    })
}

/// The top-level directory of a path ("crates/foo/bar.rs" → "crates").
pub fn top_dir(path: &str) -> String {
    match path.split_once('/') {
        Some((head, _)) => head.to_string(),
        None => "/".to_string(),
    }
}

/// Detect a bot/agent identity from a name/email/login, returning its kind.
pub fn detect_agent(name: &str, email: &str, login: Option<&str>) -> Option<&'static str> {
    let hay = format!(
        "{} {} {}",
        name.to_lowercase(),
        email.to_lowercase(),
        login.unwrap_or("").to_lowercase()
    );
    let checks: &[(&str, &str)] = &[
        ("dependabot", "dependabot"),
        ("renovate", "renovate"),
        ("claude", "claude-code"),
        ("copilot", "github-copilot"),
        ("github-actions", "github-actions"),
        ("[bot]", "bot"),
        ("greenkeeper", "greenkeeper"),
        ("snyk", "snyk"),
    ];
    for (needle, kind) in checks {
        if hay.contains(needle) {
            return Some(kind);
        }
    }
    None
}

/// True if any of a contributor's emails matches `email` (case-insensitive).
pub fn contributor_owns_email(c: &Contributor, email: &str) -> bool {
    c.primary_email.eq_ignore_ascii_case(email)
        || c.emails.iter().any(|e| e.eq_ignore_ascii_case(email))
}
