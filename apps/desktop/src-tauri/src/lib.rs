//! gitstate desktop shell.
//!
//! Unlike a classic Tauri app, no domain logic crosses IPC. During `setup` the
//! shell starts the **same** `gitstate-daemon` the headless CLI runs — on an
//! ephemeral loopback port — and points the webview (the reused React app in
//! `web/`) at it. So one HTTP API surface serves both desktop and headless
//! modes, and the frontend is identical in both.
//!
//! The daemon's base URL is injected into the webview via an initialization
//! script (`window.__GITSTATE_API__`) that runs before first paint, so the
//! React client (`web/src/lib/api.js`) resolves it synchronously.

mod commands;
mod daemon;

use tauri::{WebviewUrl, WebviewWindowBuilder};

/// Managed state: the base URL of the in-process daemon.
pub struct ApiState {
    pub base_url: String,
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    // Start the daemon before building the window so the base URL is known and
    // can be injected before the page loads.
    let base_url = daemon::start().expect("failed to start gitstate daemon");
    let init_script = format!(
        "window.__GITSTATE_API__ = {};",
        serde_json::to_string(&base_url).expect("serialize base url")
    );

    tauri::Builder::default()
        .manage(ApiState {
            base_url: base_url.clone(),
        })
        .invoke_handler(tauri::generate_handler![
            commands::app_info,
            commands::daemon_base_url,
            commands::open_external,
        ])
        .setup(move |app| {
            WebviewWindowBuilder::new(app, "main", WebviewUrl::App("index.html".into()))
                .title("gitstate")
                .inner_size(1280.0, 800.0)
                .min_inner_size(1024.0, 700.0)
                .center()
                .initialization_script(&init_script)
                .build()?;
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running gitstate");
}
