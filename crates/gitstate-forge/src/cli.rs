//! Shelling out to the `gh` / `glab` CLIs, and shared slug/forge parsing.

use gitstate_core::{Error, Forge, Result};

/// Run a CLI and return stdout as a string. A missing binary maps to the typed
/// [`Error::ForgeCliMissing`] so callers can fall back to REST.
pub async fn run_cli(bin: &str, args: &[&str]) -> Result<String> {
    let out = tokio::process::Command::new(bin)
        .args(args)
        .output()
        .await
        .map_err(|e| {
            if e.kind() == std::io::ErrorKind::NotFound {
                Error::ForgeCliMissing(bin.to_string())
            } else {
                Error::forge(format!("{bin}: {e}"))
            }
        })?;
    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        return Err(Error::forge(format!("{bin} failed: {}", stderr.trim())));
    }
    Ok(String::from_utf8_lossy(&out.stdout).into_owned())
}

/// Whether a CLI binary is resolvable on PATH.
pub async fn cli_available(bin: &str) -> bool {
    tokio::process::Command::new(bin)
        .arg("--version")
        .output()
        .await
        .map(|o| o.status.success())
        .unwrap_or(false)
}

/// Host-based forge detection from a remote URL.
pub fn detect_forge(remote_url: &str) -> Forge {
    let l = remote_url.to_lowercase();
    if l.contains("gitlab") {
        Forge::GitLab
    } else if l.contains("github") {
        Forge::GitHub
    } else {
        Forge::Local
    }
}

/// Parse "owner/name" from a git remote URL (https or scp-style ssh).
pub fn slug_from_remote(remote_url: &str) -> Result<String> {
    let s = remote_url.trim();
    // scp-style: git@host:owner/name(.git)
    let after_host = if let Some(idx) = s.find('@') {
        let rest = &s[idx + 1..];
        rest.split_once(':').map(|(_, p)| p).unwrap_or(rest)
    } else if let Some(idx) = s.find("://") {
        let rest = &s[idx + 3..];
        // strip host
        rest.split_once('/').map(|(_, p)| p).unwrap_or(rest)
    } else {
        s
    };
    let path = after_host.trim_start_matches('/').trim_end_matches('/');
    let path = path.strip_suffix(".git").unwrap_or(path);
    let parts: Vec<&str> = path.split('/').filter(|p| !p.is_empty()).collect();
    if parts.len() >= 2 {
        // owner is second-to-last, name is last (handles gitlab subgroups by
        // taking the final two path segments).
        let name = parts[parts.len() - 1];
        let owner = parts[parts.len() - 2];
        Ok(format!("{owner}/{name}"))
    } else {
        Err(Error::forge(format!(
            "cannot parse owner/name from remote: {remote_url}"
        )))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_various_remotes() {
        assert_eq!(
            slug_from_remote("git@github.com:vul-os/gitstate.git").unwrap(),
            "vul-os/gitstate"
        );
        assert_eq!(
            slug_from_remote("https://github.com/vul-os/gitstate").unwrap(),
            "vul-os/gitstate"
        );
        assert_eq!(
            slug_from_remote("https://gitlab.com/group/sub/proj.git").unwrap(),
            "sub/proj"
        );
    }

    #[test]
    fn detects_forge() {
        assert_eq!(detect_forge("https://github.com/a/b"), Forge::GitHub);
        assert_eq!(detect_forge("git@gitlab.com:a/b.git"), Forge::GitLab);
        assert_eq!(detect_forge("/local/path"), Forge::Local);
    }
}
