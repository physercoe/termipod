//! Zotero-compatible WebDAV file sync for the Read-surface storage root.
//!
//! Zotero users sync their attachment *files* to a WebDAV server (Settings →
//! Sync → File Syncing → WebDAV). This mirrors that exact on-server layout so the
//! same server a user already points Zotero at just works, and files show up in
//! both apps:
//!
//! - Everything lives under a `zotero/` collection at the configured base URL
//!   (Zotero always appends `zotero/`; so do we).
//! - Each attachment `<KEY>` (the `storage/<KEY>/` folder) is stored as two files:
//!     * `zotero/<KEY>.zip`  — a flat ZIP of the files in `storage/<KEY>/`.
//!     * `zotero/<KEY>.prop` — `<properties version="1"><mtime>ms</mtime>
//!                              <hash>md5</hash></properties>`, the completion
//!                              marker Zotero writes last.
//!   `hash` is the MD5 of the (single) attachment file — real Zotero clients
//!   reject a `.prop` whose hash doesn't match, so we compute it for real.
//!
//! Sync is two-way and content-addressed: for each key present locally and/or
//! remotely we compare the remote `.prop` hash against the local file's MD5.
//! Equal → skip. Different → newest `mtime` wins (upload or download). Same mtime
//! but different hash → a genuine conflict; we leave both sides untouched and
//! report it rather than clobber. Because the hash is checked *first*, a
//! just-downloaded file matches on the next run, so there's no upload/download
//! ping-pong even though we never rewrite local file mtimes.
//!
//! Credentials never touch disk here — the frontend keeps the URL + username in
//! its own storage and the password in the OS keychain (keychain.rs), and passes
//! all three per call. Bytes stream through this process; nothing is cached.

use std::collections::{BTreeMap, BTreeSet};
use std::io::{Cursor, Read, Write};
use std::path::{Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};

use md5::{Digest, Md5};
use serde::Serialize;

const DAV_TIMEOUT_SECS: u64 = 90;

pub(crate) fn client(proxy: Option<&str>) -> Result<reqwest::Client, String> {
    crate::net::client_builder(proxy)
        .user_agent("termipod-desktop")
        .timeout(std::time::Duration::from_secs(DAV_TIMEOUT_SECS))
        .build()
        .map_err(|e| e.to_string())
}

/// The `zotero/` collection URL under the user's base URL. Zotero always nests
/// everything under `zotero/`, so we do too (a user who pastes their Zotero
/// WebDAV URL gets the identical location).
fn dav_dir(base: &str) -> String {
    let mut b = base.trim().to_string();
    if !b.ends_with('/') {
        b.push('/');
    }
    format!("{b}zotero/")
}

fn now_ms() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_millis() as i64)
        .unwrap_or(0)
}

/// Inner text of every element whose *local* name (namespace prefix stripped)
/// equals `local`, case-insensitively. A deliberately tiny, dependency-free XML
/// scan — enough for WebDAV `<D:href>` listings and our own flat `.prop` files,
/// without pulling in a full XML parser.
pub(crate) fn extract_all(xml: &str, local: &str) -> Vec<String> {
    let mut out = Vec::new();
    let mut i = 0;
    while let Some(pos) = xml[i..].find('<') {
        let start = i + pos;
        let rest = &xml[start + 1..];
        if rest.starts_with('/') || rest.starts_with('?') || rest.starts_with('!') {
            i = start + 1;
            continue;
        }
        let Some(gt) = rest.find('>') else { break };
        let tag = &rest[..gt];
        let name = tag.split([' ', '\t', '\n', '\r']).next().unwrap_or("");
        let localname = name.rsplit(':').next().unwrap_or(name);
        let content_start = start + 1 + gt + 1;
        if !tag.ends_with('/') && localname.eq_ignore_ascii_case(local) {
            if let Some(lt) = xml[content_start..].find('<') {
                out.push(xml[content_start..content_start + lt].trim().to_string());
            }
        }
        i = content_start;
    }
    out
}

async fn mkcol(c: &reqwest::Client, dav: &str, user: &str, pass: &str) -> Result<(), String> {
    let method = reqwest::Method::from_bytes(b"MKCOL").map_err(|e| e.to_string())?;
    let resp = c
        .request(method, dav)
        .basic_auth(user, Some(pass))
        .send()
        .await
        .map_err(|e| e.to_string())?;
    let s = resp.status().as_u16();
    // 201 created · 405 already exists · 200/301 tolerated by some servers.
    if matches!(s, 200 | 201 | 301 | 405) {
        Ok(())
    } else if s == 401 {
        Err("authentication failed (check username / password)".into())
    } else {
        Err(format!("MKCOL zotero/ → HTTP {s}"))
    }
}

async fn put(
    c: &reqwest::Client,
    url: &str,
    body: Vec<u8>,
    content_type: &str,
    user: &str,
    pass: &str,
) -> Result<(), String> {
    let resp = c
        .put(url)
        .basic_auth(user, Some(pass))
        .header("Content-Type", content_type)
        .body(body)
        .send()
        .await
        .map_err(|e| e.to_string())?;
    let s = resp.status();
    if s.is_success() {
        Ok(())
    } else {
        Err(format!("PUT → HTTP {}", s.as_u16()))
    }
}

/// The set of attachment keys present on the server (those with a `.prop` — the
/// authoritative marker). Returns an empty set if the `zotero/` dir doesn't yet
/// exist (404) so a first sync just uploads.
async fn propfind_keys(
    c: &reqwest::Client,
    dav: &str,
    user: &str,
    pass: &str,
) -> Result<BTreeSet<String>, String> {
    let method = reqwest::Method::from_bytes(b"PROPFIND").map_err(|e| e.to_string())?;
    let resp = c
        .request(method, dav)
        .basic_auth(user, Some(pass))
        .header("Depth", "1")
        .header("Content-Type", "application/xml; charset=utf-8")
        .body(r#"<?xml version="1.0" encoding="utf-8"?><propfind xmlns="DAV:"><prop><getlastmodified/></prop></propfind>"#)
        .send()
        .await
        .map_err(|e| e.to_string())?;
    let s = resp.status().as_u16();
    if s == 404 {
        return Ok(BTreeSet::new());
    }
    if s == 401 {
        return Err("authentication failed (check username / password)".into());
    }
    if !(s == 207 || (200..300).contains(&s)) {
        return Err(format!("PROPFIND zotero/ → HTTP {s}"));
    }
    let body = resp.text().await.map_err(|e| e.to_string())?;
    let mut keys = BTreeSet::new();
    for href in extract_all(&body, "href") {
        let trimmed = href.trim_end_matches('/');
        let name = trimmed.rsplit('/').next().unwrap_or("");
        if let Some(k) = name.strip_suffix(".prop") {
            if is_key(k) {
                keys.insert(k.to_string());
            }
        }
    }
    Ok(keys)
}

/// The remote `<mtime, hash>` from `<KEY>.prop`; `None` if absent (404).
async fn get_prop(
    c: &reqwest::Client,
    dav: &str,
    key: &str,
    user: &str,
    pass: &str,
) -> Result<Option<(i64, String)>, String> {
    let url = format!("{dav}{key}.prop");
    let resp = c
        .get(url.as_str())
        .basic_auth(user, Some(pass))
        .send()
        .await
        .map_err(|e| e.to_string())?;
    let s = resp.status().as_u16();
    if s == 404 {
        return Ok(None);
    }
    if !(200..300).contains(&s) {
        return Err(format!("GET {key}.prop → HTTP {s}"));
    }
    let body = resp.text().await.map_err(|e| e.to_string())?;
    let mtime = extract_all(&body, "mtime")
        .into_iter()
        .next()
        .and_then(|v| v.parse::<i64>().ok())
        .unwrap_or(0);
    let hash = extract_all(&body, "hash").into_iter().next().unwrap_or_default();
    Ok(Some((mtime, hash)))
}

pub(crate) fn build_prop(mtime_ms: i64, hash: &str) -> String {
    format!("<properties version=\"1\"><mtime>{mtime_ms}</mtime><hash>{hash}</hash></properties>")
}

/// A Zotero attachment key is exactly 8 alphanumeric chars. Restricting to that
/// shape keeps stray folders in the storage root out of the sync.
pub(crate) fn is_key(k: &str) -> bool {
    k.len() == 8 && k.bytes().all(|b| b.is_ascii_alphanumeric())
}

pub(crate) struct LocalAtt {
    pub(crate) files: Vec<PathBuf>,
    pub(crate) mtime_ms: i64,
    pub(crate) hash: String,
}

fn file_mtime_ms(p: &Path) -> i64 {
    std::fs::metadata(p)
        .and_then(|m| m.modified())
        .ok()
        .and_then(|t| t.duration_since(UNIX_EPOCH).ok())
        .map(|d| d.as_millis() as i64)
        .unwrap_or(0)
}

fn md5_file(p: &Path) -> Result<String, String> {
    let bytes = std::fs::read(p).map_err(|e| e.to_string())?;
    let mut h = Md5::new();
    h.update(&bytes);
    let digest = h.finalize();
    Ok(data_encoding::HEXLOWER.encode(digest.as_slice()))
}

/// Every `storage/<KEY>/` folder that holds at least one file, keyed by KEY. The
/// hash + mtime come from the primary (alphabetically first) file — our
/// attachments are single-file, matching Zotero's `imported_file` model.
pub(crate) fn enumerate_local(root: &Path) -> BTreeMap<String, LocalAtt> {
    let mut out = BTreeMap::new();
    let Ok(rd) = std::fs::read_dir(root) else {
        return out;
    };
    for entry in rd.flatten() {
        let Ok(ft) = entry.file_type() else { continue };
        if !ft.is_dir() {
            continue;
        }
        let key = entry.file_name().to_string_lossy().to_string();
        if !is_key(&key) {
            continue;
        }
        let mut files: Vec<PathBuf> = match std::fs::read_dir(entry.path()) {
            Ok(inner) => inner
                .flatten()
                .filter(|e| e.file_type().map(|t| t.is_file()).unwrap_or(false))
                .map(|e| e.path())
                .filter(|p| {
                    p.file_name()
                        .map(|n| !n.to_string_lossy().starts_with('.'))
                        .unwrap_or(false)
                })
                .collect(),
            Err(_) => continue,
        };
        if files.is_empty() {
            continue;
        }
        files.sort();
        let primary = &files[0];
        let hash = match md5_file(primary) {
            Ok(h) => h,
            Err(_) => continue,
        };
        let mtime_ms = files.iter().map(|f| file_mtime_ms(f)).max().unwrap_or(0);
        out.insert(key, LocalAtt { files, mtime_ms, hash });
    }
    out
}

pub(crate) fn zip_files(files: &[PathBuf]) -> Result<Vec<u8>, String> {
    let mut buf = Vec::new();
    {
        let mut zw = zip::ZipWriter::new(Cursor::new(&mut buf));
        let opts = zip::write::SimpleFileOptions::default()
            .compression_method(zip::CompressionMethod::Deflated);
        for f in files {
            let name = f
                .file_name()
                .map(|s| s.to_string_lossy().to_string())
                .unwrap_or_default();
            if name.is_empty() {
                continue;
            }
            zw.start_file(name, opts).map_err(|e| e.to_string())?;
            let data = std::fs::read(f).map_err(|e| e.to_string())?;
            zw.write_all(&data).map_err(|e| e.to_string())?;
        }
        zw.finish().map_err(|e| e.to_string())?;
    }
    Ok(buf)
}

/// Extract a downloaded `<KEY>.zip` flat into `dest` (one folder per key). Entry
/// names are reduced to their basename so a maliciously-crafted zip can't escape
/// the folder (Zotero zips are flat anyway).
pub(crate) fn unzip_into(bytes: &[u8], dest: &Path) -> Result<usize, String> {
    let mut ar = zip::ZipArchive::new(Cursor::new(bytes)).map_err(|e| e.to_string())?;
    std::fs::create_dir_all(dest).map_err(|e| e.to_string())?;
    let mut n = 0;
    for i in 0..ar.len() {
        let mut f = ar.by_index(i).map_err(|e| e.to_string())?;
        if f.is_dir() {
            continue;
        }
        let raw = f.name().to_string();
        let name = Path::new(&raw)
            .file_name()
            .map(|s| s.to_string_lossy().to_string())
            .unwrap_or_default();
        if name.is_empty() || name.starts_with('.') {
            continue;
        }
        let mut data = Vec::new();
        f.read_to_end(&mut data).map_err(|e| e.to_string())?;
        std::fs::write(dest.join(&name), &data).map_err(|e| e.to_string())?;
        n += 1;
    }
    Ok(n)
}

async fn upload(
    c: &reqwest::Client,
    dav: &str,
    key: &str,
    local: &LocalAtt,
    user: &str,
    pass: &str,
) -> Result<(), String> {
    let zipped = zip_files(&local.files)?;
    put(c, &format!("{dav}{key}.zip"), zipped, "application/zip", user, pass).await?;
    // Prop is written last — its presence marks a completed upload (Zotero's rule).
    let prop = build_prop(local.mtime_ms, &local.hash).into_bytes();
    put(c, &format!("{dav}{key}.prop"), prop, "text/xml; charset=utf-8", user, pass).await?;
    Ok(())
}

/// Download + extract `<KEY>.zip` into `root/<KEY>/`. Returns false (skipped) if
/// the zip is missing on the server.
async fn download(
    c: &reqwest::Client,
    dav: &str,
    key: &str,
    root: &Path,
    user: &str,
    pass: &str,
) -> Result<bool, String> {
    let url = format!("{dav}{key}.zip");
    let resp = c
        .get(url.as_str())
        .basic_auth(user, Some(pass))
        .send()
        .await
        .map_err(|e| e.to_string())?;
    let s = resp.status().as_u16();
    if s == 404 {
        return Ok(false);
    }
    if !(200..300).contains(&s) {
        return Err(format!("GET {key}.zip → HTTP {s}"));
    }
    let bytes = resp.bytes().await.map_err(|e| e.to_string())?;
    unzip_into(&bytes, &root.join(key))?;
    Ok(true)
}

#[derive(Serialize, Default)]
pub struct SyncReport {
    pub(crate) uploaded: usize,
    pub(crate) downloaded: usize,
    pub(crate) skipped: usize,
    pub(crate) conflicts: usize,
    /// Keys whose files were pulled down — the frontend re-indexes so they show.
    #[serde(rename = "downloadedKeys")]
    pub(crate) downloaded_keys: Vec<String>,
    pub(crate) errors: Vec<String>,
}

/// Verify connectivity + write access: ensure the `zotero/` collection exists and
/// a probe file can be written. Surfaces auth failures distinctly.
#[tauri::command]
pub async fn webdav_verify(
    url: String,
    user: String,
    pass: String,
    proxy: Option<String>,
) -> Result<String, String> {
    let c = client(proxy.as_deref())?;
    let dav = dav_dir(&url);
    mkcol(&c, &dav, &user, &pass).await?;
    put(
        &c,
        &format!("{dav}lastsync.txt"),
        now_ms().to_string().into_bytes(),
        "text/plain",
        &user,
        &pass,
    )
    .await?;
    Ok("ok".into())
}

/// Two-way sync the storage `root` against the WebDAV `zotero/` collection.
/// Content-addressed (MD5) with newest-mtime-wins; genuine same-mtime/diff-hash
/// collisions are reported, never clobbered.
#[tauri::command]
pub async fn webdav_sync(
    root: String,
    url: String,
    user: String,
    pass: String,
    proxy: Option<String>,
) -> Result<SyncReport, String> {
    let c = client(proxy.as_deref())?;
    let dav = dav_dir(&url);
    let root_path = PathBuf::from(&root);
    if !root_path.is_dir() {
        return Err("storage root is not a directory".into());
    }
    // Best-effort ensure the collection exists (first-ever sync). Ignore failure —
    // PROPFIND/PUT below will surface a real connectivity/auth problem.
    let _ = mkcol(&c, &dav, &user, &pass).await;

    let mut report = SyncReport::default();
    let locals = enumerate_local(&root_path);
    let remote_keys = propfind_keys(&c, &dav, &user, &pass).await?;

    let mut all: BTreeSet<String> = locals.keys().cloned().collect();
    all.extend(remote_keys.iter().cloned());

    for key in all {
        let local = locals.get(&key);
        let remote = remote_keys.contains(&key);
        let step: Result<(), String> = async {
            match (local, remote) {
                (Some(l), false) => {
                    upload(&c, &dav, &key, l, &user, &pass).await?;
                    report.uploaded += 1;
                }
                (None, true) => {
                    if download(&c, &dav, &key, &root_path, &user, &pass).await? {
                        report.downloaded += 1;
                        report.downloaded_keys.push(key.clone());
                    } else {
                        report.skipped += 1;
                    }
                }
                (Some(l), true) => match get_prop(&c, &dav, &key, &user, &pass).await? {
                    None => {
                        // Zip listed but prop vanished — re-upload to restore it.
                        upload(&c, &dav, &key, l, &user, &pass).await?;
                        report.uploaded += 1;
                    }
                    Some((rmtime, rhash)) => {
                        if !rhash.is_empty() && rhash.eq_ignore_ascii_case(&l.hash) {
                            report.skipped += 1;
                        } else if l.mtime_ms > rmtime {
                            upload(&c, &dav, &key, l, &user, &pass).await?;
                            report.uploaded += 1;
                        } else if rmtime > l.mtime_ms {
                            if download(&c, &dav, &key, &root_path, &user, &pass).await? {
                                report.downloaded += 1;
                                report.downloaded_keys.push(key.clone());
                            } else {
                                report.skipped += 1;
                            }
                        } else {
                            // Same mtime, different content — don't guess; report it.
                            report.conflicts += 1;
                        }
                    }
                },
                (None, false) => {}
            }
            Ok(())
        }
        .await;
        if let Err(e) = step {
            report.errors.push(format!("{key}: {e}"));
        }
    }
    Ok(report)
}
