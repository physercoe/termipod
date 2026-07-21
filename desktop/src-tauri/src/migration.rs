//! Migration data egress (ADR-055 / plan M0).
//!
//! localStorage is bound to the webview profile and will NOT follow the app
//! into Electron's Chromium profile. So the frontend snapshots every
//! `termipod.*` key to a versioned JSON file under app-data; Electron's first
//! boot re-imports it (M1), and it doubles as a free local backup. Bytes never
//! leave the device.

use std::fs;
use std::path::PathBuf;

use tauri::{AppHandle, Manager};

/// `<app-data>/migration/state-v1.json`. The `migration/` dir is created on
/// demand (mirrors `storage::attachment_default_dir`).
fn state_path(app: &AppHandle) -> Result<PathBuf, String> {
    let base = app.path().app_data_dir().map_err(|e| e.to_string())?;
    let dir = base.join("migration");
    fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    Ok(dir.join("state-v1.json"))
}

/// Persist the frontend's serialized localStorage snapshot. Written via a temp
/// file + rename so a crash mid-write never leaves a truncated JSON file.
#[tauri::command]
pub async fn migration_export(app: AppHandle, json: String) -> Result<(), String> {
    let path = state_path(&app)?;
    let tmp = path.with_extension("tmp");
    fs::write(&tmp, json.as_bytes()).map_err(|e| e.to_string())?;
    fs::rename(&tmp, &path).map_err(|e| e.to_string())?;
    Ok(())
}

/// Read back the snapshot, or `Ok(None)` if none has been written yet.
#[tauri::command]
pub async fn migration_read(app: AppHandle) -> Result<Option<String>, String> {
    let path = state_path(&app)?;
    match fs::read_to_string(&path) {
        Ok(s) => Ok(Some(s)),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(None),
        Err(e) => Err(e.to_string()),
    }
}
