use std::io::Read;
use std::path::{Path, PathBuf};
use tauri::{AppHandle, Manager};
use tauri_plugin_dialog::DialogExt;

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
///
/// If this fails with a transport error ("error sending request for url…"), the
/// user's network can't reach the GitHub release CDN (release assets 302-redirect
/// to `*.githubusercontent.com`, which some networks/regions throttle). The
/// `drawio_install_file` command below is the offline fallback — the user
/// downloads `draw.war` manually and installs it from disk.
#[tauri::command]
pub async fn drawio_download(app: AppHandle, proxy: Option<String>) -> Result<DrawioStatus, String> {
    let root = drawio_root(&app)?;
    if root.join("index.html").is_file() {
        return Ok(status_of(&root));
    }
    let client = crate::net::client_builder(proxy.as_deref())
        // GitHub release-asset URLs redirect to the githubusercontent CDN; follow
        // them (default is 10, but be explicit) and identify a UA so no proxy
        // rejects a header-less request.
        .redirect(reqwest::redirect::Policy::limited(10))
        .user_agent("termipod-desktop")
        .build()
        .map_err(|e| e.to_string())?;
    let bytes = client
        .get(DRAWIO_WAR_URL)
        .send()
        .await
        .map_err(|e| format!("could not reach the draw.io download ({e}). Download draw.war manually and use “Install from file”."))?
        .error_for_status()
        .map_err(|e| e.to_string())?
        .bytes()
        .await
        .map_err(|e| e.to_string())?;
    install_war_bytes(&root, bytes.to_vec())
}

/// Offline fallback for `drawio_download`: pick a `draw.war` the user already has
/// on disk (native file dialog) and install it. Same extraction path; no network.
/// `Ok(None)` if the user cancels. Accepts the official `draw.war` (a ZIP) — any
/// zip whose root is the static webapp works. `async` so the blocking picker runs
/// off the main event-loop thread (see storage_pick_folder).
#[tauri::command]
pub async fn drawio_install_file(app: AppHandle) -> Result<Option<DrawioStatus>, String> {
    let picked = app
        .dialog()
        .file()
        .add_filter("draw.io webapp", &["war", "zip"])
        .blocking_pick_file();
    let Some(fp) = picked else {
        return Ok(None);
    };
    let path = fp.into_path().map_err(|e| e.to_string())?;
    let bytes = std::fs::read(&path).map_err(|e| format!("could not read {}: {e}", path.display()))?;
    let root = drawio_root(&app)?;
    install_war_bytes(&root, bytes).map(Some)
}

/// Extract a draw.war (ZIP) byte buffer into the version-keyed root. Staging +
/// rename so a partial/failed extract never leaves a root that `status` reports as
/// installed. Shared by both the network download and the local-file install.
fn install_war_bytes(root: &Path, bytes: Vec<u8>) -> Result<DrawioStatus, String> {
    let staging = root.with_extension("part");
    let _ = std::fs::remove_dir_all(&staging);
    std::fs::create_dir_all(&staging).map_err(|e| e.to_string())?;

    let mut zip = zip::ZipArchive::new(std::io::Cursor::new(bytes))
        .map_err(|e| format!("not a valid draw.war (ZIP) file: {e}"))?;
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
        return Err("draw.war has no index.html at its root — is this the webapp .war?".into());
    }
    let _ = std::fs::remove_dir_all(root);
    if let Some(parent) = root.parent() {
        std::fs::create_dir_all(parent).map_err(|e| e.to_string())?;
    }
    std::fs::rename(&staging, root).map_err(|e| e.to_string())?;
    Ok(status_of(root))
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
