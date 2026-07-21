//! Updater handoff (ADR-055 plan §2.3 / §5).
//!
//! The Tauri auto-updater installs Tauri-format bundles in place, so the final
//! Tauri release can't silently swap itself for the Electron build — a
//! different installer format (notably a macOS `.dmg` vs a `.app.tar.gz`). The
//! handoff manifest (`handoff.json`, published beside `latest.json` on the
//! release AT M3) lets that release surface the successor installer as a normal
//! "a new version is available — Download" prompt that opens the OS installer
//! instead of attempting an incompatible in-place update.
//!
//! Manifest shape (keyed by `platform_os` — `windows` | `macos` | `linux`):
//! ```json
//! { "version": "1.0.0", "notes": "…",
//!   "platforms": { "macos": "https://…/TermiPod_1.0.0_universal.dmg" } }
//! ```
//! Absent today (404 → `None`), so this ships dormant with zero behaviour
//! change.

use std::time::Duration;

/// Fetch the handoff manifest. `Ok(Some(body))` when present, `Ok(None)` on 404
/// (no handoff published — the steady state until M3), `Err` on transport
/// failure. `proxy` mirrors the updater's resolved proxy so it works behind a
/// corporate proxy exactly like the update check.
#[tauri::command]
pub async fn handoff_check(url: String, proxy: Option<String>) -> Result<Option<String>, String> {
    let client = crate::net::client_builder(proxy.as_deref())
        .connect_timeout(Duration::from_secs(15))
        .timeout(Duration::from_secs(30))
        .build()
        .map_err(|e| e.to_string())?;
    let resp = client.get(&url).send().await.map_err(|e| e.to_string())?;
    if resp.status() == reqwest::StatusCode::NOT_FOUND {
        return Ok(None);
    }
    if !resp.status().is_success() {
        return Err(format!("handoff status {}", resp.status().as_u16()));
    }
    let body = resp.text().await.map_err(|e| e.to_string())?;
    Ok(Some(body))
}
