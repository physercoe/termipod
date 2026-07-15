// localfs.rs — the *local* side of the two-pane file transfer (FileZilla-style).
// The remote side rides SFTP over the SSH session (see ssh.rs sftp_*); this lets
// the paired local pane browse the user's own machine and read/write the files
// being transferred. Read-only listing plus explicit read/write of a single file
// the user picked in the UI — no recursion, no privilege beyond the user's own fs
// (the app already exposes stronger native capabilities, e.g. pty_open).
//
// Commands are `async` so they run off the UI/main thread — a large read/write
// must not freeze the webview (see pty.rs: a sync command blocks the main thread).

use std::path::PathBuf;

/// The user's home directory, from the platform env (`HOME`, or `USERPROFILE` on
/// Windows). Used as the local pane's default landing directory.
fn home() -> Option<PathBuf> {
    std::env::var_os("HOME")
        .or_else(|| std::env::var_os("USERPROFILE"))
        .map(PathBuf::from)
}

// Cap a single listing so a directory with a huge number of entries can't blow up
// the IPC payload or hang the pane.
const MAX_ENTRIES: usize = 10_000;

#[derive(serde::Serialize)]
pub struct LocalEntry {
    name: String,
    /// Absolute path — the UI navigates and transfers by this, never by re-joining
    /// (so separators stay correct across platforms).
    path: String,
    is_dir: bool,
    size: u64,
}

#[derive(serde::Serialize)]
pub struct LocalListing {
    /// The absolute directory that was listed.
    path: String,
    /// Its parent (None at a filesystem root), for the "up" button.
    parent: Option<String>,
    entries: Vec<LocalEntry>,
}

/// The default local directory (home).
#[tauri::command]
pub async fn localfs_home() -> Result<String, String> {
    home()
        .map(|p| p.to_string_lossy().to_string())
        .ok_or_else(|| "no home directory".to_string())
}

/// List a local directory (non-recursive). An empty path or "~" resolves to home.
/// Dirs first, then case-insensitive alphabetical; hidden files are included (an
/// SSH user wants `~/.ssh`).
#[tauri::command]
pub async fn localfs_list(path: String) -> Result<LocalListing, String> {
    let base = if path.is_empty() || path == "~" {
        home().ok_or_else(|| "no home directory".to_string())?
    } else {
        PathBuf::from(&path)
    };
    if !base.is_dir() {
        return Err(format!("not a folder: {}", base.display()));
    }
    let rd = std::fs::read_dir(&base).map_err(|e| e.to_string())?;
    let mut entries: Vec<LocalEntry> = Vec::new();
    for ent in rd.flatten() {
        if entries.len() >= MAX_ENTRIES {
            break;
        }
        let name = ent.file_name().to_string_lossy().to_string();
        let md = ent.metadata().ok();
        let is_dir = md.as_ref().map(|m| m.is_dir()).unwrap_or(false);
        let size = if is_dir {
            0
        } else {
            md.as_ref().map(|m| m.len()).unwrap_or(0)
        };
        entries.push(LocalEntry {
            name,
            path: ent.path().to_string_lossy().to_string(),
            is_dir,
            size,
        });
    }
    entries.sort_by(|a, b| {
        b.is_dir
            .cmp(&a.is_dir)
            .then(a.name.to_lowercase().cmp(&b.name.to_lowercase()))
    });
    Ok(LocalListing {
        path: base.to_string_lossy().to_string(),
        parent: base.parent().map(|p| p.to_string_lossy().to_string()),
        entries,
    })
}

/// Read a local file, returning its bytes base64-encoded (→ upload to remote).
#[tauri::command]
pub async fn localfs_read(path: String) -> Result<String, String> {
    use base64::Engine as _;
    let bytes = std::fs::read(&path).map_err(|e| e.to_string())?;
    Ok(base64::engine::general_purpose::STANDARD.encode(&bytes))
}

/// Write base64 bytes to a local path, creating/overwriting it (← download from
/// remote).
#[tauri::command]
pub async fn localfs_write(path: String, data_b64: String) -> Result<(), String> {
    use base64::Engine as _;
    let bytes = base64::engine::general_purpose::STANDARD
        .decode(data_b64.as_bytes())
        .map_err(|e| e.to_string())?;
    std::fs::write(&path, &bytes).map_err(|e| e.to_string())
}
