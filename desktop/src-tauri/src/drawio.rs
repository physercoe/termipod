use std::io::Read;
use std::path::{Path, PathBuf};
use tauri::{AppHandle, Manager};

// drawio.rs — the optional, offline **draw.io** diagram editor for J2 Author.
//
// draw.io is Apache-2.0 and fully client-side. We do NOT bundle it in the
// installer (~50 MB); instead the user downloads it once via a button. The
// artifact is the official `draw.war` release asset — a `.war` is a ZIP whose
// root IS the static webapp (plus a Java `WEB-INF/` we skip). We extract it into
// a **version-keyed** app-data dir so it survives app updates and is never
// re-downloaded, then serve it to an in-app iframe via the custom `drawio://`
// URI scheme (registered in lib.rs) so relative asset URLs resolve offline.

const DRAWIO_VERSION: &str = "v30.3.6";
const DRAWIO_WAR_URL: &str = "https://github.com/jgraph/drawio/releases/download/v30.3.6/draw.war";

fn drawio_root(app: &AppHandle) -> Result<PathBuf, String> {
    let base = app.path().app_data_dir().map_err(|e| e.to_string())?;
    // Version-keyed: a new pinned version downloads into its own dir; the old one
    // stays until cleaned, so an app update never forces a re-download.
    Ok(base.join("drawio").join(DRAWIO_VERSION))
}

#[derive(serde::Serialize)]
pub struct DrawioStatus {
    installed: bool,
    version: String,
}

fn status_of(root: &Path) -> DrawioStatus {
    DrawioStatus {
        installed: root.join("index.html").is_file(),
        version: DRAWIO_VERSION.to_string(),
    }
}

#[tauri::command]
pub async fn drawio_status(app: AppHandle) -> Result<DrawioStatus, String> {
    Ok(status_of(&drawio_root(&app)?))
}

/// Download the draw.io webapp (once) and extract it into the version-keyed
/// app-data dir. Idempotent: a no-op if already installed.
#[tauri::command]
pub async fn drawio_download(app: AppHandle) -> Result<DrawioStatus, String> {
    let root = drawio_root(&app)?;
    if root.join("index.html").is_file() {
        return Ok(status_of(&root));
    }
    let bytes = reqwest::Client::new()
        .get(DRAWIO_WAR_URL)
        .send()
        .await
        .map_err(|e| e.to_string())?
        .error_for_status()
        .map_err(|e| e.to_string())?
        .bytes()
        .await
        .map_err(|e| e.to_string())?;

    // Extract into a temp dir first, then rename into place — so a failed/partial
    // download never leaves a half-populated root that `status` reports installed.
    let staging = root.with_extension("part");
    let _ = std::fs::remove_dir_all(&staging);
    std::fs::create_dir_all(&staging).map_err(|e| e.to_string())?;

    let mut zip = zip::ZipArchive::new(std::io::Cursor::new(bytes)).map_err(|e| e.to_string())?;
    for i in 0..zip.len() {
        let mut entry = zip.by_index(i).map_err(|e| e.to_string())?;
        let name = match entry.enclosed_name() {
            Some(p) => p.to_path_buf(), // own it — `entry` is borrowed mutably below
            None => continue,           // skip unsafe (traversal) names
        };
        // We only need the static webapp — drop the Java servlet/manifest dirs.
        if name.starts_with("WEB-INF") || name.starts_with("META-INF") {
            continue;
        }
        let out = staging.join(&name);
        if entry.is_dir() {
            std::fs::create_dir_all(&out).map_err(|e| e.to_string())?;
            continue;
        }
        if let Some(parent) = out.parent() {
            std::fs::create_dir_all(parent).map_err(|e| e.to_string())?;
        }
        let mut buf = Vec::with_capacity(entry.size() as usize);
        entry.read_to_end(&mut buf).map_err(|e| e.to_string())?;
        std::fs::write(&out, buf).map_err(|e| e.to_string())?;
    }
    if !staging.join("index.html").is_file() {
        let _ = std::fs::remove_dir_all(&staging);
        return Err("draw.io webapp has no index.html after extract".into());
    }
    let _ = std::fs::remove_dir_all(&root);
    if let Some(parent) = root.parent() {
        std::fs::create_dir_all(parent).map_err(|e| e.to_string())?;
    }
    std::fs::rename(&staging, &root).map_err(|e| e.to_string())?;
    Ok(status_of(&root))
}

/// Read an extracted draw.io file for the custom `drawio://` scheme. Returns the
/// bytes + a content-type. Path-traversal guarded: the resolved file must stay
/// under the version-keyed root.
pub fn serve(app: &AppHandle, path: &str) -> Result<(Vec<u8>, String), String> {
    let root = drawio_root(app)?;
    let rel = path.trim_start_matches('/');
    let rel = if rel.is_empty() { "index.html" } else { rel };
    let full = root.join(rel);
    let canon_root = std::fs::canonicalize(&root).map_err(|e| e.to_string())?;
    let canon_full = std::fs::canonicalize(&full).map_err(|e| e.to_string())?;
    if !canon_full.starts_with(&canon_root) {
        return Err("path escapes drawio root".into());
    }
    let bytes = std::fs::read(&canon_full).map_err(|e| e.to_string())?;
    Ok((bytes, mime_for(&canon_full)))
}

fn mime_for(p: &Path) -> String {
    let ext = p
        .extension()
        .and_then(|e| e.to_str())
        .unwrap_or("")
        .to_lowercase();
    let m = match ext.as_str() {
        "html" | "htm" => "text/html; charset=utf-8",
        "js" | "mjs" => "text/javascript; charset=utf-8",
        "css" => "text/css; charset=utf-8",
        "json" => "application/json",
        "svg" => "image/svg+xml",
        "png" => "image/png",
        "jpg" | "jpeg" => "image/jpeg",
        "gif" => "image/gif",
        "webp" => "image/webp",
        "ico" => "image/x-icon",
        "woff" => "font/woff",
        "woff2" => "font/woff2",
        "ttf" => "font/ttf",
        "eot" => "application/vnd.ms-fontobject",
        "xml" => "application/xml",
        "txt" => "text/plain; charset=utf-8",
        "wasm" => "application/wasm",
        _ => "application/octet-stream",
    };
    m.to_string()
}
