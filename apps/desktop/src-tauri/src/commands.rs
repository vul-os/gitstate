//! The minimal Tauri command surface (§4). No domain data crosses IPC — these
//! only expose the daemon base URL and open external links.

use serde::Serialize;
use tauri::State;

use crate::ApiState;

#[derive(Serialize)]
pub struct AppInfo {
    pub name: &'static str,
    pub version: &'static str,
    pub tauri: &'static str,
}

/// App + runtime versions, for a frontend liveness/about panel.
#[tauri::command]
pub fn app_info() -> AppInfo {
    AppInfo {
        name: "gitstate",
        version: env!("CARGO_PKG_VERSION"),
        tauri: tauri::VERSION,
    }
}

/// The in-process daemon's base URL. The web client prefixes every `/api` call
/// with this (it is also injected as `window.__GITSTATE_API__` at startup).
#[tauri::command]
pub fn daemon_base_url(state: State<ApiState>) -> String {
    state.base_url.clone()
}

/// Open a forge/PR/docs link in the system browser. Only http(s) URLs are
/// accepted, so this can never be turned into an arbitrary-command sink.
#[tauri::command]
pub async fn open_external(url: String) -> Result<(), String> {
    if !(url.starts_with("http://") || url.starts_with("https://")) {
        return Err("only http(s) URLs may be opened".to_string());
    }
    open_in_browser(&url).map_err(|e| e.to_string())
}

fn open_in_browser(url: &str) -> std::io::Result<()> {
    #[cfg(target_os = "macos")]
    {
        std::process::Command::new("open").arg(url).spawn()?;
    }
    #[cfg(target_os = "windows")]
    {
        std::process::Command::new("cmd")
            .args(["/C", "start", "", url])
            .spawn()?;
    }
    #[cfg(all(unix, not(target_os = "macos")))]
    {
        std::process::Command::new("xdg-open").arg(url).spawn()?;
    }
    Ok(())
}
