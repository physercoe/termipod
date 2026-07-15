//! Recursive folder (Obsidian-vault style) WebDAV sync for the Author workspace.
//!
//! The Read surface's `webdav.rs` speaks Zotero's flat `zotero/<KEY>.zip` layout;
//! that's wrong for an author workspace, which is an arbitrary tree of markdown
//! files and attachments — exactly an Obsidian vault. This module mirrors that
//! tree *verbatim* under the configured base URL (no `zotero/` subcollection), so
//! a WebDAV endpoint that already holds a user's Obsidian vault (e.g. synced by
//! the Remotely-Save plugin, or Nextcloud's Notes folder) imports straight in.
//!
//! Sync is **two-way and additive — it never deletes** (director's choice): for
//! every relative path present locally and/or remotely we pick a direction and
//! copy. A file removed on one side is left intact on the other, so a bad run can
//! never destroy vault data. Direction:
//!   * present one side only → copy to the other.
//!   * both sides, equal byte length → skip (treated as identical — avoids an
//!     upload/download ping-pong without hashing an entire tree).
//!   * both sides, different length → newest `getlastmodified` / mtime wins.
//!   * both sides, different length, mtime unknown either side → reported as a
//!     conflict and left untouched (never guess a direction).
//!
//! Credentials never touch disk here: the frontend keeps URL + username in its
//! own storage and the password in the consolidated keychain item, and passes all
//! three per call. Bytes stream through this process; nothing is cached.

use std::collections::{BTreeMap, BTreeSet};
use std::path::{Path, PathBuf};
use std::time::UNIX_EPOCH;

use reqwest::Url;
use serde::Serialize;

use crate::webdav::extract_all;

const DAV_TIMEOUT_SECS: u64 = 90;
const MAX_ENTRIES: usize = 5000; // per side — mirrors workspace_list's cap
const MAX_DEPTH: usize = 8;
const MAX_FILE_BYTES: u64 = 100 * 1024 * 1024; // don't buffer giant blobs in memory

// Build/VCS/cache dirs — noise in a vault, and never what a user means to sync.
// Dotfiles (incl. `.obsidian/`) are already skipped by the `.`-prefix rule, which
// also keeps this consistent with what the Author file tree shows (workspace.rs).
const SKIP_DIRS: &[&str] = &[
    "node_modules", ".git", "target", "dist", "build", ".next", ".venv", "venv",
    "__pycache__", ".cache", ".idea", ".vscode", ".svn", ".hg",
];

// ── shared plumbing ─────────────────────────────────────────────────────────
fn base_url(url: &str) -> Result<Url, String> {
    let mut s = url.trim().to_string();
    if !s.ends_with('/') {
        s.push('/');
    }
    Url::parse(&s).map_err(|e| format!("invalid WebDAV URL: {e}"))
}

/// The URL for a relative POSIX path under the base, with each segment
/// percent-encoded by the `url` crate. `dir` appends a trailing slash (collections
/// need one for MKCOL/PROPFIND). An empty `rel` is the base itself.
fn child_url(base: &Url, rel: &str, dir: bool) -> Result<Url, String> {
    let mut u = base.clone();
    {
        let mut seg = u.path_segments_mut().map_err(|_| "base URL cannot be a base".to_string())?;
        for part in rel.split('/').filter(|p| !p.is_empty()) {
            seg.push(part);
        }
        if dir {
            seg.push(""); // trailing slash
        }
    }
    Ok(u)
}

/// Minimal percent-decoder (only `%XX`) — enough to turn an encoded href segment
/// back into a filesystem name without pulling in `percent_encoding` as a direct
/// dependency.
fn pct_decode(s: &str) -> String {
    let b = s.as_bytes();
    let mut out: Vec<u8> = Vec::with_capacity(b.len());
    let mut i = 0;
    while i < b.len() {
        if b[i] == b'%' && i + 2 < b.len() {
            let hi = (b[i + 1] as char).to_digit(16);
            let lo = (b[i + 2] as char).to_digit(16);
            if let (Some(h), Some(l)) = (hi, lo) {
                out.push((h * 16 + l) as u8);
                i += 3;
                continue;
            }
        }
        out.push(b[i]);
        i += 1;
    }
    String::from_utf8_lossy(&out).into_owned()
}

/// Days since the Unix epoch for a civil date (Howard Hinnant's algorithm) — lets
/// us turn an RFC-1123 `getlastmodified` into epoch ms without a date crate.
fn days_from_civil(y: i64, m: i64, d: i64) -> i64 {
    let y = if m <= 2 { y - 1 } else { y };
    let era = if y >= 0 { y } else { y - 399 } / 400;
    let yoe = y - era * 400;
    let doy = (153 * (if m > 2 { m - 3 } else { m + 9 }) + 2) / 5 + d - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    era * 146097 + doe - 719468
}

const MONTHS: [&str; 12] = [
    "jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec",
];

/// Parse an RFC-1123 date (`Wed, 15 Jul 2026 10:20:30 GMT`, the WebDAV standard for
/// `getlastmodified`) to epoch ms. `None` if it doesn't parse — the caller treats
/// an unknown mtime conservatively (never clobbers on it).
fn parse_http_date_ms(s: &str) -> Option<i64> {
    let toks: Vec<&str> = s.split_whitespace().collect();
    if toks.len() < 5 {
        return None;
    }
    let day: i64 = toks[1].parse().ok()?;
    let mon = MONTHS.iter().position(|m| toks[2].to_ascii_lowercase().starts_with(m))? as i64 + 1;
    let year: i64 = toks[3].parse().ok()?;
    let hms: Vec<&str> = toks[4].split(':').collect();
    if hms.len() != 3 {
        return None;
    }
    let hh: i64 = hms[0].parse().ok()?;
    let mm: i64 = hms[1].parse().ok()?;
    let ss: i64 = hms[2].parse().ok()?;
    let days = days_from_civil(year, mon, day);
    Some((days * 86400 + hh * 3600 + mm * 60 + ss) * 1000)
}

fn file_mtime_ms(p: &Path) -> Option<i64> {
    std::fs::metadata(p)
        .and_then(|m| m.modified())
        .ok()
        .and_then(|t| t.duration_since(UNIX_EPOCH).ok())
        .map(|d| d.as_millis() as i64)
}

// ── remote listing ──────────────────────────────────────────────────────────
struct RemoteFile {
    size: u64,
    mtime_ms: Option<i64>,
}

/// Split a multistatus body into its `<response>` blocks (namespace prefix
/// stripped), so href/size/mtime can be read per entry rather than flattened.
/// Delimiter-aware on the tag *localname* — so `<responsedescription>` (a real
/// WebDAV element sharing the "response" prefix) never opens a bogus block.
/// `<response>` elements don't nest, so a close simply ends the open block.
fn response_blocks(xml: &str) -> Vec<&str> {
    let mut out = Vec::new();
    let mut open_start: Option<usize> = None;
    let mut i = 0;
    while let Some(pos) = xml[i..].find('<') {
        let lt = i + pos;
        let rest = &xml[lt + 1..];
        let Some(gt) = rest.find('>') else { break };
        let tag = &rest[..gt];
        let content_start = lt + 1 + gt + 1;
        let closing = tag.starts_with('/');
        let head = tag.trim_start_matches('/');
        if head.starts_with('?') || head.starts_with('!') {
            i = content_start;
            continue;
        }
        let nm = head.split([' ', '\t', '\n', '\r', '/']).next().unwrap_or("");
        let localname = nm.rsplit(':').next().unwrap_or(nm);
        if localname.eq_ignore_ascii_case("response") {
            if closing {
                if let Some(cs) = open_start.take() {
                    out.push(&xml[cs..lt]);
                }
            } else if !tag.ends_with('/') {
                open_start = Some(content_start);
            }
        }
        i = content_start;
    }
    out
}

fn has_local_tag(xml: &str, name: &str) -> bool {
    let mut i = 0;
    let lname = name.to_ascii_lowercase();
    while let Some(pos) = xml[i..].find('<') {
        let start = i + pos;
        let rest = &xml[start + 1..];
        let Some(gt) = rest.find('>') else { break };
        let tag = &rest[..gt];
        let head = tag.trim_start_matches('/');
        let nm = head.split([' ', '\t', '\n', '\r', '/']).next().unwrap_or("");
        let localname = nm.rsplit(':').next().unwrap_or(nm);
        if localname.eq_ignore_ascii_case(&lname) {
            return true;
        }
        i = start + 1 + gt;
    }
    false
}

/// PROPFIND one collection (Depth: 1). Returns (child collection rel-paths to
/// recurse into, file entries keyed by rel-path). `base_path` is the base URL's
/// percent-encoded path with a trailing slash; `dir_rel` is the encoded relative
/// path of the collection being listed ("" for the base).
async fn propfind_dir(
    c: &reqwest::Client,
    base: &Url,
    base_path: &str,
    dir_rel: &str,
    files: &mut BTreeMap<String, RemoteFile>,
    subdirs: &mut Vec<String>,
) -> Result<(), String> {
    let url = child_url(base, dir_rel, true)?;
    let method = reqwest::Method::from_bytes(b"PROPFIND").map_err(|e| e.to_string())?;
    let resp = c
        .request(method, url.as_str())
        .header("Depth", "1")
        .header("Content-Type", "application/xml; charset=utf-8")
        .body(r#"<?xml version="1.0" encoding="utf-8"?><propfind xmlns="DAV:"><prop><resourcetype/><getcontentlength/><getlastmodified/></prop></propfind>"#)
        .send()
        .await
        .map_err(|e| e.to_string())?;
    let s = resp.status().as_u16();
    if s == 404 {
        return Ok(()); // nothing remote yet — first sync just uploads
    }
    if s == 401 {
        return Err("authentication failed (check username / password)".into());
    }
    if !(s == 207 || (200..300).contains(&s)) {
        return Err(format!("PROPFIND → HTTP {s}"));
    }
    let body = resp.text().await.map_err(|e| e.to_string())?;
    // The listed collection's own encoded path, to skip its self-entry.
    let self_path = child_url(base, dir_rel, true)?.path().to_string();
    let self_trim = self_path.trim_end_matches('/');
    for block in response_blocks(&body) {
        let Some(href) = extract_all(block, "href").into_iter().next() else {
            continue;
        };
        // Resolve to an absolute path, then strip the base to get the rel path.
        let abs = base.join(href.trim()).map_err(|_| "bad href".to_string())?;
        let path = abs.path();
        if path.trim_end_matches('/') == self_trim {
            continue; // the collection itself
        }
        let Some(enc_rel) = path.strip_prefix(base_path) else {
            continue; // outside our tree — ignore
        };
        let is_dir = has_local_tag(block, "collection") || path.ends_with('/');
        let dec_rel = enc_rel
            .trim_end_matches('/')
            .split('/')
            .map(pct_decode)
            .collect::<Vec<_>>()
            .join("/");
        if dec_rel.is_empty() {
            continue;
        }
        if is_dir {
            let name = dec_rel.rsplit('/').next().unwrap_or("");
            if name.starts_with('.') || SKIP_DIRS.contains(&name) {
                continue;
            }
            // Recurse on the *decoded* path — child_url percent-encodes segments
            // itself, so passing an already-encoded path would double-encode it.
            subdirs.push(dec_rel);
        } else {
            let name = dec_rel.rsplit('/').next().unwrap_or("");
            if name.starts_with('.') {
                continue;
            }
            let size = extract_all(block, "getcontentlength")
                .into_iter()
                .next()
                .and_then(|v| v.trim().parse::<u64>().ok())
                .unwrap_or(0);
            let mtime_ms = extract_all(block, "getlastmodified")
                .into_iter()
                .next()
                .and_then(|v| parse_http_date_ms(v.trim()));
            files.insert(dec_rel, RemoteFile { size, mtime_ms });
        }
    }
    Ok(())
}

/// Walk the whole remote tree (BFS, depth/entry-capped). Keyed by decoded
/// relative POSIX path.
async fn enumerate_remote(
    c: &reqwest::Client,
    base: &Url,
) -> Result<BTreeMap<String, RemoteFile>, String> {
    let base_path = base.path().to_string(); // trailing-slash, percent-encoded
    let mut files = BTreeMap::new();
    let mut queue: Vec<(String, usize)> = vec![(String::new(), 0)];
    while let Some((dir_rel, depth)) = queue.pop() {
        if depth >= MAX_DEPTH || files.len() >= MAX_ENTRIES {
            continue;
        }
        let mut subdirs = Vec::new();
        propfind_dir(c, base, &base_path, &dir_rel, &mut files, &mut subdirs).await?;
        for sd in subdirs {
            queue.push((sd, depth + 1));
        }
    }
    Ok(files)
}

// ── local listing ───────────────────────────────────────────────────────────
struct LocalFile {
    abs: PathBuf,
    size: u64,
    mtime_ms: Option<i64>,
}

fn enumerate_local(root: &Path) -> BTreeMap<String, LocalFile> {
    let mut out = BTreeMap::new();
    walk_local(root, "", 0, &mut out);
    out
}

fn walk_local(dir: &Path, rel: &str, depth: usize, out: &mut BTreeMap<String, LocalFile>) {
    if depth >= MAX_DEPTH || out.len() >= MAX_ENTRIES {
        return;
    }
    let Ok(rd) = std::fs::read_dir(dir) else {
        return;
    };
    for ent in rd.flatten() {
        if out.len() >= MAX_ENTRIES {
            break;
        }
        let name = ent.file_name().to_string_lossy().to_string();
        if name.starts_with('.') {
            continue;
        }
        let p = ent.path();
        let child_rel = if rel.is_empty() { name.clone() } else { format!("{rel}/{name}") };
        let is_dir = p.is_dir();
        if is_dir {
            if SKIP_DIRS.contains(&name.as_str()) {
                continue;
            }
            walk_local(&p, &child_rel, depth + 1, out);
        } else {
            let size = std::fs::metadata(&p).map(|m| m.len()).unwrap_or(0);
            out.insert(
                child_rel,
                LocalFile { abs: p.clone(), size, mtime_ms: file_mtime_ms(&p) },
            );
        }
    }
}

// ── transfers ───────────────────────────────────────────────────────────────
async fn mkcol_parents(
    c: &reqwest::Client,
    base: &Url,
    rel: &str,
    made: &mut BTreeSet<String>,
) -> Result<(), String> {
    let parts: Vec<&str> = rel.split('/').filter(|p| !p.is_empty()).collect();
    if parts.len() < 2 {
        return Ok(()); // top-level file — parent is the (verified) base
    }
    let mut acc = String::new();
    for p in &parts[..parts.len() - 1] {
        if !acc.is_empty() {
            acc.push('/');
        }
        acc.push_str(p);
        if made.contains(&acc) {
            continue;
        }
        let url = child_url(base, &acc, true)?;
        let method = reqwest::Method::from_bytes(b"MKCOL").map_err(|e| e.to_string())?;
        let resp = c.request(method, url.as_str()).send().await.map_err(|e| e.to_string())?;
        let s = resp.status().as_u16();
        // 201 created · 405 exists · 200/301 tolerated. 409 = a parent still
        // missing (shouldn't happen — we go shallow→deep) — surface it.
        if !matches!(s, 200 | 201 | 301 | 405) {
            return Err(format!("MKCOL {acc}/ → HTTP {s}"));
        }
        made.insert(acc.clone());
    }
    Ok(())
}

async fn upload(
    c: &reqwest::Client,
    base: &Url,
    rel: &str,
    local: &LocalFile,
    made: &mut BTreeSet<String>,
) -> Result<(), String> {
    if local.size > MAX_FILE_BYTES {
        return Err("file exceeds 100 MB sync cap".into());
    }
    mkcol_parents(c, base, rel, made).await?;
    let bytes = std::fs::read(&local.abs).map_err(|e| e.to_string())?;
    let url = child_url(base, rel, false)?;
    let resp = c
        .put(url.as_str())
        .header("Content-Type", "application/octet-stream")
        .body(bytes)
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

async fn download(c: &reqwest::Client, base: &Url, rel: &str, root: &Path) -> Result<(), String> {
    let url = child_url(base, rel, false)?;
    let resp = c.get(url.as_str()).send().await.map_err(|e| e.to_string())?;
    let s = resp.status().as_u16();
    if !(200..300).contains(&s) {
        return Err(format!("GET → HTTP {s}"));
    }
    let bytes = resp.bytes().await.map_err(|e| e.to_string())?;
    // Confine the write to the workspace root — a hostile server can't escape it.
    let mut dest = root.to_path_buf();
    for part in rel.split('/').filter(|p| !p.is_empty()) {
        if part == ".." || part == "." {
            return Err(format!("unsafe remote path: {rel}"));
        }
        dest.push(part);
    }
    if let Some(parent) = dest.parent() {
        std::fs::create_dir_all(parent).map_err(|e| e.to_string())?;
    }
    std::fs::write(&dest, &bytes).map_err(|e| e.to_string())?;
    Ok(())
}

#[derive(Serialize, Default)]
pub struct FolderSyncReport {
    uploaded: usize,
    downloaded: usize,
    skipped: usize,
    conflicts: usize,
    errors: Vec<String>,
}

// ── commands ────────────────────────────────────────────────────────────────
/// Verify connectivity + auth: PROPFIND the base collection. Distinguishes an
/// auth failure from an unreachable/other error.
#[tauri::command]
pub async fn folder_webdav_verify(url: String, user: String, pass: String) -> Result<String, String> {
    let base = base_url(&url)?;
    let c = client_with_auth(&user, &pass)?;
    let method = reqwest::Method::from_bytes(b"PROPFIND").map_err(|e| e.to_string())?;
    let resp = c
        .request(method, base.as_str())
        .header("Depth", "0")
        .body(r#"<?xml version="1.0" encoding="utf-8"?><propfind xmlns="DAV:"><prop><resourcetype/></prop></propfind>"#)
        .send()
        .await
        .map_err(|e| e.to_string())?;
    let s = resp.status().as_u16();
    if s == 401 {
        return Err("authentication failed (check username / password)".into());
    }
    if s == 404 {
        return Err("folder not found at that URL (check the path)".into());
    }
    if s == 207 || (200..300).contains(&s) {
        Ok("ok".into())
    } else {
        Err(format!("PROPFIND → HTTP {s}"))
    }
}

/// Two-way, additive (never-delete) sync of the workspace `root` against the
/// WebDAV tree at `url`.
#[tauri::command]
pub async fn folder_webdav_sync(
    root: String,
    url: String,
    user: String,
    pass: String,
) -> Result<FolderSyncReport, String> {
    let root_path = PathBuf::from(&root);
    if !root_path.is_dir() {
        return Err("workspace root is not a directory".into());
    }
    let base = base_url(&url)?;
    let c = client_with_auth(&user, &pass)?;

    let locals = enumerate_local(&root_path);
    let remotes = enumerate_remote(&c, &base).await?;

    let mut all: BTreeSet<String> = locals.keys().cloned().collect();
    all.extend(remotes.keys().cloned());

    let mut report = FolderSyncReport::default();
    let mut made: BTreeSet<String> = BTreeSet::new();

    for rel in all {
        let local = locals.get(&rel);
        let remote = remotes.get(&rel);
        let step: Result<(), String> = async {
            match (local, remote) {
                (Some(l), None) => {
                    upload(&c, &base, &rel, l, &mut made).await?;
                    report.uploaded += 1;
                }
                (None, Some(r)) => {
                    if r.size > MAX_FILE_BYTES {
                        report.skipped += 1;
                    } else {
                        download(&c, &base, &rel, &root_path).await?;
                        report.downloaded += 1;
                    }
                }
                (Some(l), Some(r)) => {
                    if l.size == r.size {
                        report.skipped += 1; // equal length ⇒ treat as identical
                    } else {
                        match (l.mtime_ms, r.mtime_ms) {
                            (Some(lm), Some(rm)) if lm > rm => {
                                upload(&c, &base, &rel, l, &mut made).await?;
                                report.uploaded += 1;
                            }
                            (Some(lm), Some(rm)) if rm > lm => {
                                download(&c, &base, &rel, &root_path).await?;
                                report.downloaded += 1;
                            }
                            _ => report.conflicts += 1, // equal or unknown mtime — never guess
                        }
                    }
                }
                (None, None) => {}
            }
            Ok(())
        }
        .await;
        if let Err(e) = step {
            report.errors.push(format!("{rel}: {e}"));
        }
    }
    Ok(report)
}

/// A client that carries Basic auth on every request (PROPFIND/GET/PUT/MKCOL all
/// need it). Simpler than threading `.basic_auth()` through each call.
fn client_with_auth(user: &str, pass: &str) -> Result<reqwest::Client, String> {
    use reqwest::header::{HeaderMap, HeaderValue, AUTHORIZATION};
    let token = data_encoding::BASE64.encode(format!("{user}:{pass}").as_bytes());
    let mut headers = HeaderMap::new();
    let mut val = HeaderValue::from_str(&format!("Basic {token}")).map_err(|e| e.to_string())?;
    val.set_sensitive(true);
    headers.insert(AUTHORIZATION, val);
    reqwest::Client::builder()
        .user_agent("termipod-desktop")
        .default_headers(headers)
        .timeout(std::time::Duration::from_secs(DAV_TIMEOUT_SECS))
        .build()
        .map_err(|e| e.to_string())
}
