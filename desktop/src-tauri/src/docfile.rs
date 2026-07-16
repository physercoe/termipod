//! On-disk file save/open for the J2 Author documents.
//!
//! Author documents are device-local (persisted to the WebView's localStorage)
//! by default; these commands let the user additionally save a document to a
//! real file and reopen it, so "where are my files" has a concrete answer the
//! user controls. Reuses the same native dialog plugin as the Zotero storage
//! link; the bytes never leave the machine.

use serde::Serialize;
use tauri::AppHandle;
use tauri_plugin_dialog::DialogExt;

#[derive(Serialize)]
pub struct OpenedDoc {
    path: String,
    content: String,
}

// Openable Author document files: markdown/diagram plus the canvas board
// (`.canvas` JSON) and table/database (`.csv`) document kinds. The frontend maps
// the extension to a document kind (see state/documents.ts `kindForExt`).
const TEXT_EXTS: &[&str] = &["md", "markdown", "txt", "drawio", "xml", "svg", "canvas", "csv"];

/// Pick a text file and read it. `Ok(None)` if the user cancels.
#[tauri::command]
pub async fn doc_open(app: AppHandle) -> Result<Option<OpenedDoc>, String> {
    let picked = app
        .dialog()
        .file()
        .add_filter("Documents", TEXT_EXTS)
        .blocking_pick_file();
    let Some(fp) = picked else {
        return Ok(None);
    };
    let path = fp.into_path().map_err(|e| e.to_string())?;
    let content = std::fs::read_to_string(&path).map_err(|e| e.to_string())?;
    Ok(Some(OpenedDoc {
        path: path.to_string_lossy().to_string(),
        content,
    }))
}

/// Read a text file by a known path (no dialog) — used by the Author file tree
/// when the user clicks an entry to open it. Errors on binary/unreadable files so
/// the caller can skip them.
#[tauri::command]
pub async fn doc_read(path: String) -> Result<OpenedDoc, String> {
    let content = std::fs::read_to_string(&path).map_err(|e| e.to_string())?;
    Ok(OpenedDoc { path, content })
}

/// Show a Save dialog (seeded with `default_name`), write `content`, and return
/// the chosen path. `Ok(None)` if the user cancels.
#[tauri::command]
pub async fn doc_save(app: AppHandle, content: String, default_name: String) -> Result<Option<String>, String> {
    let picked = app
        .dialog()
        .file()
        .set_file_name(&default_name)
        .blocking_save_file();
    let Some(fp) = picked else {
        return Ok(None);
    };
    let path = fp.into_path().map_err(|e| e.to_string())?;
    std::fs::write(&path, content).map_err(|e| e.to_string())?;
    Ok(Some(path.to_string_lossy().to_string()))
}

/// Write `content` to an already-known path (a re-save; no dialog).
#[tauri::command]
pub async fn doc_write(path: String, content: String) -> Result<(), String> {
    std::fs::write(&path, content).map_err(|e| e.to_string())
}
