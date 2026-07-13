//! Local Zotero `storage/` folder access for the J1 Read surface.
//!
//! The webview's `<input webkitdirectory>` gives live `File` handles but no
//! absolute path, so a linked folder can't survive a restart (director report:
//! "the linked storage of Zotero is lost when I reopen"). Here the folder is
//! chosen by a native dialog, its **path** is returned to the frontend to persist
//! (localStorage), re-indexed on startup via [`storage_reindex`], and its files
//! read on demand via [`storage_read`]. Bytes never leave the device.
//!
//! Zotero lays attachments out as `storage/<attachment-key>/<filename>`; the
//! index keys each file by its immediate parent directory name (`key`) plus
//! filename (`file`), which is exactly what a Reference's `zoteroStorage`
//! resolves to. `rel` (the path relative to the linked root) lets `storage_read`
//! reopen a file without re-deriving the layout.

use std::path::{Path, PathBuf};

use rand_core::{OsRng, RngCore};
use serde::Serialize;
use tauri::{AppHandle, Manager};
use tauri_plugin_dialog::DialogExt;

#[derive(Serialize, Clone)]
pub struct StorageEntry {
    key: String,
    file: String,
    rel: String,
}

#[derive(Serialize, Clone)]
pub struct StorageIndex {
    path: String,
    #[serde(rename = "folderName")]
    folder_name: String,
    entries: Vec<StorageEntry>,
}

#[derive(Serialize)]
pub struct StorageFile {
    base64: String,
    mime: String,
}

fn mime_for(name: &str) -> &'static str {
    let lower = name.to_ascii_lowercase();
    if lower.ends_with(".pdf") {
        "application/pdf"
    } else if lower.ends_with(".html") || lower.ends_with(".htm") {
        "text/html"
    } else if lower.ends_with(".epub") {
        "application/epub+zip"
    } else if lower.ends_with(".txt") {
        "text/plain"
    } else {
        "application/octet-stream"
    }
}

/// Recursively index every file under `root`, keyed by parent-dir + filename.
/// Reads only directory entries (names) — never file bytes. Bounded depth and
/// count so a mis-picked huge tree (or a symlink cycle) can't run away.
fn index_dir(root: &Path) -> Vec<StorageEntry> {
    fn walk(root: &Path, dir: &Path, depth: usize, out: &mut Vec<StorageEntry>) {
        if depth > 6 || out.len() >= 200_000 {
            return;
        }
        let rd = match std::fs::read_dir(dir) {
            Ok(rd) => rd,
            Err(_) => return,
        };
        for entry in rd.flatten() {
            let ft = match entry.file_type() {
                Ok(ft) => ft,
                Err(_) => continue,
            };
            if ft.is_symlink() {
                continue;
            }
            let path = entry.path();
            if ft.is_dir() {
                walk(root, &path, depth + 1, out);
            } else if ft.is_file() {
                let file = entry.file_name().to_string_lossy().to_string();
                let key = path
                    .parent()
                    .and_then(|p| p.file_name())
                    .map(|s| s.to_string_lossy().to_string())
                    .unwrap_or_default();
                let rel = path
                    .strip_prefix(root)
                    .map(|p| p.to_string_lossy().to_string())
                    .unwrap_or_else(|_| file.clone());
                out.push(StorageEntry { key, file, rel });
            }
        }
    }
    let mut out = Vec::new();
    walk(root, root, 0, &mut out);
    out
}

fn build_index(path: PathBuf) -> Result<StorageIndex, String> {
    if !path.is_dir() {
        return Err("not a directory".into());
    }
    let folder_name = path
        .file_name()
        .map(|s| s.to_string_lossy().to_string())
        .unwrap_or_default();
    let entries = index_dir(&path);
    Ok(StorageIndex {
        path: path.to_string_lossy().to_string(),
        folder_name,
        entries,
    })
}

/// Open a native folder picker and index the chosen Zotero `storage/` tree.
/// Returns `Ok(None)` when the user cancels. The returned `path` is what the
/// frontend persists so the link survives a restart.
#[tauri::command]
pub async fn storage_pick_folder(app: AppHandle) -> Result<Option<StorageIndex>, String> {
    // `blocking_pick_folder` must not run on the main (event-loop) thread; a
    // tauri async command runs on a worker thread, so this is safe.
    let picked = app.dialog().file().blocking_pick_folder();
    let Some(fp) = picked else {
        return Ok(None);
    };
    let path = fp.into_path().map_err(|e| e.to_string())?;
    build_index(path).map(Some)
}

/// Re-index a previously-linked folder path (on app start). Errors when the
/// folder no longer exists / isn't readable so the frontend can drop the stale
/// path and prompt a re-link.
#[tauri::command]
pub async fn storage_reindex(path: String) -> Result<StorageIndex, String> {
    build_index(PathBuf::from(path))
}

/// Read one indexed file's bytes (base64 for the string IPC bridge) so the
/// frontend can build a blob URL for the PDF viewer. `rel` is canonicalised and
/// checked to stay under the linked root — no `..` escape.
#[tauri::command]
pub async fn storage_read(path: String, rel: String) -> Result<StorageFile, String> {
    use base64::Engine as _;
    let root = PathBuf::from(&path);
    let full = root.join(&rel);
    let canon_root = std::fs::canonicalize(&root).map_err(|e| e.to_string())?;
    let canon_full = std::fs::canonicalize(&full).map_err(|e| e.to_string())?;
    if !canon_full.starts_with(&canon_root) {
        return Err("path escapes storage root".into());
    }
    let bytes = std::fs::read(&canon_full).map_err(|e| e.to_string())?;
    let base64 = base64::engine::general_purpose::STANDARD.encode(&bytes);
    Ok(StorageFile {
        base64,
        mime: mime_for(&rel).to_string(),
    })
}

// ---- user-managed attachments (add / read / delete) -----------------------
//
// Attachments the user adds in-app (not imported from Zotero) are copied into a
// storage root laid out exactly like Zotero — `<root>/<KEY>/<filename>` with a
// fresh 8-char key — so the same resolution model works and the files travel
// with the library. The root is the linked Zotero `storage/` folder when one is
// linked, else a default (app-data) or user-set location (chosen by the
// frontend; here we just act on the `root` it passes).

/// A freshly-added attachment's coordinates, returned to the frontend to store
/// on the reference.
#[derive(Serialize)]
pub struct AddedAttachment {
    key: String,
    file: String,
    path: String,
    #[serde(rename = "contentType")]
    content_type: String,
}

/// A file chosen via the native picker (for adding an attachment).
#[derive(Serialize)]
pub struct PickedFile {
    path: String,
    name: String,
}

/// A fresh Zotero-style attachment key: 8 chars from Zotero's alphabet (no
/// 0/1/I/O, to avoid visual ambiguity). Collision odds are negligible; the
/// caller also re-rolls if the directory already exists.
fn gen_key() -> String {
    const ALPHABET: &[u8] = b"23456789ABCDEFGHIJKLMNPQRSTUVWXYZ";
    let mut raw = [0u8; 8];
    OsRng.fill_bytes(&mut raw);
    raw.iter()
        .map(|b| ALPHABET[(*b as usize) % ALPHABET.len()] as char)
        .collect()
}

/// The default attachment storage root: `<app-data>/storage`. Created if absent.
/// Used when no Zotero folder is linked and the user hasn't set a custom path.
#[tauri::command]
pub async fn attachment_default_dir(app: AppHandle) -> Result<String, String> {
    let base = app.path().app_data_dir().map_err(|e| e.to_string())?;
    let dir = base.join("storage");
    std::fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    Ok(dir.to_string_lossy().to_string())
}

/// Native file picker for adding an attachment. Returns the picked file's
/// absolute path + name, or `Ok(None)` on cancel.
#[tauri::command]
pub async fn attachment_pick_file(app: AppHandle) -> Result<Option<PickedFile>, String> {
    let picked = app
        .dialog()
        .file()
        .add_filter(
            "Documents",
            &[
                "pdf", "epub", "html", "htm", "txt", "md", "png", "jpg", "jpeg", "gif", "webp",
                "mp4", "webm", "mov", "mp3", "wav", "m4a", "flac",
            ],
        )
        .blocking_pick_file();
    let Some(fp) = picked else {
        return Ok(None);
    };
    let path = fp.into_path().map_err(|e| e.to_string())?;
    let name = path
        .file_name()
        .map(|s| s.to_string_lossy().to_string())
        .unwrap_or_default();
    Ok(Some(PickedFile {
        path: path.to_string_lossy().to_string(),
        name,
    }))
}

/// Native folder picker for setting a custom attachment storage location.
/// Returns the chosen absolute path, or `Ok(None)` on cancel.
#[tauri::command]
pub async fn attachment_pick_dir(app: AppHandle) -> Result<Option<String>, String> {
    let picked = app.dialog().file().blocking_pick_folder();
    let Some(fp) = picked else {
        return Ok(None);
    };
    let path = fp.into_path().map_err(|e| e.to_string())?;
    Ok(Some(path.to_string_lossy().to_string()))
}

/// Copy `src` into `root` under a fresh `<key>/` dir (Zotero layout) and return
/// the new attachment's coordinates. Never overwrites an existing file.
#[tauri::command]
pub async fn attachment_add(root: String, src: String) -> Result<AddedAttachment, String> {
    let src_path = PathBuf::from(&src);
    let file = src_path
        .file_name()
        .map(|s| s.to_string_lossy().to_string())
        .filter(|s| !s.is_empty())
        .ok_or_else(|| "source has no filename".to_string())?;
    let root_path = PathBuf::from(&root);
    std::fs::create_dir_all(&root_path).map_err(|e| e.to_string())?;
    let mut key = gen_key();
    let mut dir = root_path.join(&key);
    for _ in 0..10 {
        if !dir.exists() {
            break;
        }
        key = gen_key();
        dir = root_path.join(&key);
    }
    std::fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    let dest = dir.join(&file);
    std::fs::copy(&src_path, &dest).map_err(|e| e.to_string())?;
    Ok(AddedAttachment {
        key,
        file: file.clone(),
        path: dest.to_string_lossy().to_string(),
        content_type: mime_for(&file).to_string(),
    })
}

/// Read a user-managed attachment by its absolute path (base64 for the IPC
/// bridge). The path comes from our own stored coordinates, not user input.
#[tauri::command]
pub async fn attachment_read(path: String) -> Result<StorageFile, String> {
    use base64::Engine as _;
    let bytes = std::fs::read(&path).map_err(|e| e.to_string())?;
    let base64 = base64::engine::general_purpose::STANDARD.encode(&bytes);
    Ok(StorageFile {
        base64,
        mime: mime_for(&path).to_string(),
    })
}

/// Delete a user-managed attachment file, then remove its now-empty `<key>/`
/// dir (Zotero layout is one file per key folder). Only ever touches the file
/// we were handed and an empty parent — never recursive.
#[tauri::command]
pub async fn attachment_delete(path: String) -> Result<(), String> {
    let p = PathBuf::from(&path);
    if p.is_file() {
        std::fs::remove_file(&p).map_err(|e| e.to_string())?;
    }
    if let Some(parent) = p.parent() {
        if let Ok(mut rd) = std::fs::read_dir(parent) {
            if rd.next().is_none() {
                let _ = std::fs::remove_dir(parent);
            }
        }
    }
    Ok(())
}
