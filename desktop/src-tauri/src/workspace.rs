use std::path::{Path, PathBuf};
use tauri::AppHandle;
use tauri_plugin_dialog::DialogExt;

// workspace.rs — the Author (J2) left file tree. Lets the director open a folder
// on disk and browse/open its files as documents. Listing is read-only and depth-
// and entry-capped so a huge tree can't hang the UI or blow up the IPC payload.

#[derive(serde::Serialize)]
pub struct FileNode {
    name: String,
    path: String,
    dir: bool,
    children: Vec<FileNode>,
}

// Build/VCS/cache dirs that are noise in an author workspace — never descended.
const SKIP_DIRS: &[&str] = &[
    "node_modules", ".git", "target", "dist", "build", ".next", ".venv", "venv",
    "__pycache__", ".cache", ".idea", ".vscode", ".svn", ".hg",
];
const MAX_DEPTH: usize = 8;
const MAX_ENTRIES: usize = 5000;

/// Pick a workspace folder (native dialog). `Ok(None)` on cancel. `async` so the
/// blocking picker runs off the main event-loop thread (see storage_pick_folder).
#[tauri::command]
pub async fn workspace_pick_folder(app: AppHandle) -> Result<Option<String>, String> {
    let picked = app.dialog().file().blocking_pick_folder();
    let Some(fp) = picked else {
        return Ok(None);
    };
    let path = fp.into_path().map_err(|e| e.to_string())?;
    Ok(Some(path.to_string_lossy().to_string()))
}

/// List a folder recursively (dirs first, then alphabetical), skipping hidden and
/// build/VCS dirs. Depth- and count-capped so it always returns promptly.
#[tauri::command]
pub async fn workspace_list(path: String) -> Result<Vec<FileNode>, String> {
    let root = PathBuf::from(&path);
    if !root.is_dir() {
        return Err(format!("not a folder: {path}"));
    }
    let mut count = 0usize;
    list_dir(&root, 0, &mut count)
}

fn list_dir(dir: &Path, depth: usize, count: &mut usize) -> Result<Vec<FileNode>, String> {
    if depth >= MAX_DEPTH {
        return Ok(vec![]);
    }
    let mut entries: Vec<(bool, String, PathBuf)> = vec![];
    let rd = std::fs::read_dir(dir).map_err(|e| e.to_string())?;
    for ent in rd.flatten() {
        if *count >= MAX_ENTRIES {
            break;
        }
        let name = ent.file_name().to_string_lossy().to_string();
        if name.starts_with('.') {
            continue;
        }
        let p = ent.path();
        let is_dir = p.is_dir();
        if is_dir && SKIP_DIRS.contains(&name.as_str()) {
            continue;
        }
        *count += 1;
        entries.push((is_dir, name, p));
    }
    // Dirs before files, then case-insensitive alphabetical.
    entries.sort_by(|a, b| b.0.cmp(&a.0).then(a.1.to_lowercase().cmp(&b.1.to_lowercase())));
    let mut out = Vec::with_capacity(entries.len());
    for (is_dir, name, p) in entries {
        let children = if is_dir { list_dir(&p, depth + 1, count)? } else { vec![] };
        out.push(FileNode {
            name,
            path: p.to_string_lossy().to_string(),
            dir: is_dir,
            children,
        });
    }
    Ok(out)
}
