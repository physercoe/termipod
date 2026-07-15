//! S3 (and S3-compatible) backend for the Author workspace sync — the second
//! backend behind the tree-sync engine WebDAV proved (foldersync.rs). Same
//! additive, never-delete rule (`decide_both`) and the same local enumeration;
//! only the remote listing + object transfer differ.
//!
//! Works against AWS S3 and any S3-compatible endpoint (Cloudflare R2, MinIO,
//! Backblaze B2, Wasabi) — the user gives an endpoint (blank ⇒ AWS
//! `s3.<region>.amazonaws.com`), region, bucket, an optional key prefix (the
//! "folder" inside the bucket), and an access-key pair. **Path-style** addressing
//! (`https://host/<bucket>/<key>`) is used throughout — the most compatible across
//! providers.
//!
//! Requests are signed with **AWS Signature V4**, hand-rolled on the RustCrypto
//! `hmac` + `sha2` already in the tree (no S3 SDK): a deterministic, well-specified
//! algorithm, so it compiles and signs correctly on the first CI build without a
//! live bucket to iterate against. The secret key never touches disk — the
//! frontend keeps it in the consolidated keychain item and passes it per call.

use std::collections::{BTreeMap, BTreeSet};
use std::path::{Path, PathBuf};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use hmac::{Hmac, Mac};
use reqwest::Url;
use sha2::{Digest, Sha256};

use crate::foldersync::{
    days_from_civil, decide_both, element_blocks, enumerate_local, FolderSyncReport, SyncAction,
    MAX_ENTRIES, MAX_FILE_BYTES,
};
use crate::webdav::extract_all;

type HmacSha256 = Hmac<Sha256>;

const S3_TIMEOUT_SECS: u64 = 90;
const EMPTY_SHA256: &str = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855";

struct S3Cfg {
    scheme: String,
    host: String, // host[:port], as it appears in the Host header
    bucket: String,
    prefix: String, // normalised: no leading '/', trailing '/' when non-empty
    region: String,
    access: String,
    secret: String,
}

fn make_cfg(
    endpoint: &str,
    region: &str,
    bucket: &str,
    prefix: &str,
    access: &str,
    secret: &str,
) -> Result<S3Cfg, String> {
    if bucket.trim().is_empty() {
        return Err("bucket is required".into());
    }
    let region = if region.trim().is_empty() { "us-east-1" } else { region.trim() };
    let ep = if endpoint.trim().is_empty() {
        format!("https://s3.{region}.amazonaws.com")
    } else {
        endpoint.trim().to_string()
    };
    let url = Url::parse(&ep).map_err(|e| format!("invalid endpoint: {e}"))?;
    let scheme = url.scheme().to_string();
    let host = match url.port() {
        Some(p) => format!("{}:{}", url.host_str().unwrap_or(""), p),
        None => url.host_str().unwrap_or("").to_string(),
    };
    if host.is_empty() {
        return Err("endpoint has no host".into());
    }
    let mut prefix = prefix.trim().trim_start_matches('/').to_string();
    if !prefix.is_empty() && !prefix.ends_with('/') {
        prefix.push('/');
    }
    Ok(S3Cfg {
        scheme,
        host,
        bucket: bucket.trim().to_string(),
        prefix,
        region: region.to_string(),
        access: access.trim().to_string(),
        secret: secret.to_string(),
    })
}

fn client() -> Result<reqwest::Client, String> {
    reqwest::Client::builder()
        .user_agent("termipod-desktop")
        .timeout(Duration::from_secs(S3_TIMEOUT_SECS))
        .build()
        .map_err(|e| e.to_string())
}

// ── SigV4 primitives ────────────────────────────────────────────────────────
fn sha256_hex(b: &[u8]) -> String {
    let mut h = Sha256::new();
    h.update(b);
    let out = h.finalize();
    data_encoding::HEXLOWER.encode(out.as_slice())
}

fn hmac_sha256(key: &[u8], data: &[u8]) -> Vec<u8> {
    let mut m = <HmacSha256 as Mac>::new_from_slice(key).expect("HMAC accepts any key length");
    m.update(data);
    m.finalize().into_bytes().to_vec()
}

/// RFC-3986 percent-encoding as AWS canonicalisation requires: only the unreserved
/// set is literal; `/` is preserved in a path (`encode_slash=false`) but encoded
/// in query values.
fn uri_encode(s: &str, encode_slash: bool) -> String {
    let mut out = String::with_capacity(s.len());
    for &b in s.as_bytes() {
        match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'.' | b'_' | b'~' => out.push(b as char),
            b'/' if !encode_slash => out.push('/'),
            _ => out.push_str(&format!("%{b:02X}")),
        }
    }
    out
}

/// Inverse of `days_from_civil` (Howard Hinnant) — epoch days → civil date, so we
/// can format the current UTC instant for the `x-amz-date` header.
fn civil_from_days(z: i64) -> (i64, i64, i64) {
    let z = z + 719468;
    let era = if z >= 0 { z } else { z - 146096 } / 146097;
    let doe = z - era * 146097;
    let yoe = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365;
    let y = yoe + era * 400;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    (if m <= 2 { y + 1 } else { y }, m, d)
}

fn now_utc() -> (String, String) {
    let secs = SystemTime::now().duration_since(UNIX_EPOCH).map(|d| d.as_secs() as i64).unwrap_or(0);
    let (y, m, d) = civil_from_days(secs.div_euclid(86400));
    let tod = secs.rem_euclid(86400);
    let (hh, mm, ss) = (tod / 3600, (tod % 3600) / 60, tod % 60);
    (format!("{y:04}{m:02}{d:02}T{hh:02}{mm:02}{ss:02}Z"), format!("{y:04}{m:02}{d:02}"))
}

/// Parse an S3 `LastModified` (`2026-07-15T09:43:10.000Z`) to epoch ms.
fn iso8601_to_ms(s: &str) -> Option<i64> {
    let (date, time) = s.split_once('T')?;
    let d: Vec<&str> = date.split('-').collect();
    if d.len() != 3 {
        return None;
    }
    let y: i64 = d[0].parse().ok()?;
    let mon: i64 = d[1].parse().ok()?;
    let day: i64 = d[2].parse().ok()?;
    let hms: Vec<&str> = time.get(..8)?.split(':').collect();
    if hms.len() != 3 {
        return None;
    }
    let hh: i64 = hms[0].parse().ok()?;
    let mm: i64 = hms[1].parse().ok()?;
    let ss: i64 = hms[2].parse().ok()?;
    Some((days_from_civil(y, mon, day) * 86400 + hh * 3600 + mm * 60 + ss) * 1000)
}

fn host_of(url: &Url) -> String {
    match url.port() {
        Some(p) => format!("{}:{}", url.host_str().unwrap_or(""), p),
        None => url.host_str().unwrap_or("").to_string(),
    }
}

/// Sign and send one request. `query` is the exact canonical query string already
/// on `url` (empty for object ops). Minimal signed header set:
/// host;x-amz-content-sha256;x-amz-date.
async fn send_signed(
    c: &reqwest::Client,
    cfg: &S3Cfg,
    method: reqwest::Method,
    url: Url,
    query: &str,
    body: Option<Vec<u8>>,
) -> Result<reqwest::Response, String> {
    let (amz, stamp) = now_utc();
    let host = host_of(&url);
    let payload_hash = match body.as_deref() {
        Some(b) => sha256_hex(b),
        None => EMPTY_SHA256.to_string(),
    };
    let canonical_uri = url.path().to_string();
    let signed_headers = "host;x-amz-content-sha256;x-amz-date";
    let canonical_headers =
        format!("host:{host}\nx-amz-content-sha256:{payload_hash}\nx-amz-date:{amz}\n");
    let canonical_request = format!(
        "{}\n{}\n{}\n{}\n{}\n{}",
        method.as_str(),
        canonical_uri,
        query,
        canonical_headers,
        signed_headers,
        payload_hash
    );
    let scope = format!("{stamp}/{}/s3/aws4_request", cfg.region);
    let string_to_sign = format!(
        "AWS4-HMAC-SHA256\n{amz}\n{scope}\n{}",
        sha256_hex(canonical_request.as_bytes())
    );
    let k_date = hmac_sha256(format!("AWS4{}", cfg.secret).as_bytes(), stamp.as_bytes());
    let k_region = hmac_sha256(&k_date, cfg.region.as_bytes());
    let k_service = hmac_sha256(&k_region, b"s3");
    let k_signing = hmac_sha256(&k_service, b"aws4_request");
    let signature = data_encoding::HEXLOWER.encode(&hmac_sha256(&k_signing, string_to_sign.as_bytes()));
    let authorization = format!(
        "AWS4-HMAC-SHA256 Credential={}/{scope}, SignedHeaders={signed_headers}, Signature={signature}",
        cfg.access
    );

    let mut req = c
        .request(method, url)
        .header("x-amz-date", amz)
        .header("x-amz-content-sha256", payload_hash)
        .header(reqwest::header::AUTHORIZATION, authorization);
    if let Some(b) = body {
        req = req.header("content-type", "application/octet-stream").body(b);
    }
    req.send().await.map_err(|e| e.to_string())
}

fn object_url(cfg: &S3Cfg, rel: &str) -> Result<Url, String> {
    let key = format!("{}{}", cfg.prefix, rel);
    let raw = format!(
        "{}://{}/{}/{}",
        cfg.scheme,
        cfg.host,
        uri_encode(&cfg.bucket, true),
        uri_encode(&key, false)
    );
    Url::parse(&raw).map_err(|e| e.to_string())
}

fn xml_unescape(s: &str) -> String {
    s.replace("&amp;", "&")
        .replace("&lt;", "<")
        .replace("&gt;", ">")
        .replace("&quot;", "\"")
        .replace("&apos;", "'")
}

// ── remote listing (ListObjectsV2, paginated) ───────────────────────────────
struct RemoteObj {
    size: u64,
    mtime_ms: Option<i64>,
}

async fn list_objects(c: &reqwest::Client, cfg: &S3Cfg) -> Result<BTreeMap<String, RemoteObj>, String> {
    let mut out = BTreeMap::new();
    let mut token: Option<String> = None;
    loop {
        // Canonical query: params sorted by key, each URI-encoded.
        let mut params: Vec<(String, String)> =
            vec![("list-type".into(), "2".into()), ("max-keys".into(), "1000".into())];
        if let Some(t) = &token {
            params.push(("continuation-token".into(), t.clone()));
        }
        if !cfg.prefix.is_empty() {
            params.push(("prefix".into(), cfg.prefix.clone()));
        }
        params.sort_by(|a, b| a.0.cmp(&b.0));
        let query = params
            .iter()
            .map(|(k, v)| format!("{}={}", uri_encode(k, true), uri_encode(v, true)))
            .collect::<Vec<_>>()
            .join("&");
        let raw =
            format!("{}://{}/{}?{}", cfg.scheme, cfg.host, uri_encode(&cfg.bucket, true), query);
        let url = Url::parse(&raw).map_err(|e| e.to_string())?;
        let resp = send_signed(c, cfg, reqwest::Method::GET, url, &query, None).await?;
        let s = resp.status().as_u16();
        if s == 403 {
            return Err("access denied (check the access key / secret / permissions)".into());
        }
        if s == 404 {
            return Err("bucket not found (check the bucket name / endpoint / region)".into());
        }
        if !(200..300).contains(&s) {
            let body = resp.text().await.unwrap_or_default();
            let code = extract_all(&body, "Code").into_iter().next().unwrap_or_default();
            return Err(if code.is_empty() {
                format!("list objects → HTTP {s}")
            } else {
                format!("list objects → HTTP {s} ({code})")
            });
        }
        let body = resp.text().await.map_err(|e| e.to_string())?;
        for block in element_blocks(&body, "Contents") {
            if out.len() >= MAX_ENTRIES {
                break;
            }
            let Some(key_raw) = extract_all(block, "Key").into_iter().next() else { continue };
            let key = xml_unescape(&key_raw);
            if key.ends_with('/') {
                continue; // a "folder" marker object
            }
            let Some(rel) = key.strip_prefix(&cfg.prefix) else { continue };
            if rel.is_empty() {
                continue;
            }
            let size = extract_all(block, "Size")
                .into_iter()
                .next()
                .and_then(|v| v.trim().parse::<u64>().ok())
                .unwrap_or(0);
            let mtime_ms = extract_all(block, "LastModified")
                .into_iter()
                .next()
                .and_then(|v| iso8601_to_ms(v.trim()));
            out.insert(rel.to_string(), RemoteObj { size, mtime_ms });
        }
        let truncated = extract_all(&body, "IsTruncated")
            .into_iter()
            .next()
            .map(|v| v.trim().eq_ignore_ascii_case("true"))
            .unwrap_or(false);
        if !truncated || out.len() >= MAX_ENTRIES {
            break;
        }
        token = extract_all(&body, "NextContinuationToken").into_iter().next();
        if token.is_none() {
            break;
        }
    }
    Ok(out)
}

async fn put_object(c: &reqwest::Client, cfg: &S3Cfg, rel: &str, abs: &Path) -> Result<(), String> {
    let bytes = std::fs::read(abs).map_err(|e| e.to_string())?;
    if bytes.len() as u64 > MAX_FILE_BYTES {
        return Err("file exceeds 100 MB sync cap".into());
    }
    let url = object_url(cfg, rel)?;
    let resp = send_signed(c, cfg, reqwest::Method::PUT, url, "", Some(bytes)).await?;
    let s = resp.status();
    if s.is_success() {
        Ok(())
    } else {
        Err(format!("PUT → HTTP {}", s.as_u16()))
    }
}

async fn get_object(c: &reqwest::Client, cfg: &S3Cfg, rel: &str, root: &Path) -> Result<(), String> {
    let url = object_url(cfg, rel)?;
    let resp = send_signed(c, cfg, reqwest::Method::GET, url, "", None).await?;
    let s = resp.status().as_u16();
    if !(200..300).contains(&s) {
        return Err(format!("GET → HTTP {s}"));
    }
    let bytes = resp.bytes().await.map_err(|e| e.to_string())?;
    // Confine the write to the workspace root.
    let mut dest = root.to_path_buf();
    for part in rel.split('/').filter(|p| !p.is_empty()) {
        if part == ".." || part == "." {
            return Err(format!("unsafe key path: {rel}"));
        }
        dest.push(part);
    }
    if let Some(parent) = dest.parent() {
        std::fs::create_dir_all(parent).map_err(|e| e.to_string())?;
    }
    std::fs::write(&dest, &bytes).map_err(|e| e.to_string())?;
    Ok(())
}

// ── commands ────────────────────────────────────────────────────────────────
/// Verify credentials + reachability: a 1-key ListObjectsV2. Surfaces an auth
/// failure distinctly.
#[tauri::command]
pub async fn s3_sync_verify(
    endpoint: String,
    region: String,
    bucket: String,
    prefix: String,
    access_key: String,
    secret_key: String,
) -> Result<String, String> {
    let cfg = make_cfg(&endpoint, &region, &bucket, &prefix, &access_key, &secret_key)?;
    let c = client()?;
    let query = "list-type=2&max-keys=1";
    let raw = format!("{}://{}/{}?{}", cfg.scheme, cfg.host, uri_encode(&cfg.bucket, true), query);
    let url = Url::parse(&raw).map_err(|e| e.to_string())?;
    let resp = send_signed(&c, &cfg, reqwest::Method::GET, url, query, None).await?;
    let s = resp.status().as_u16();
    if s == 403 {
        return Err("access denied (check the access key / secret / permissions)".into());
    }
    if s == 404 {
        return Err("bucket not found (check the bucket name / endpoint / region)".into());
    }
    if (200..300).contains(&s) {
        Ok("ok".into())
    } else {
        let body = resp.text().await.unwrap_or_default();
        let code = extract_all(&body, "Code").into_iter().next().unwrap_or_default();
        Err(if code.is_empty() { format!("HTTP {s}") } else { format!("HTTP {s} ({code})") })
    }
}

/// Two-way, additive (never-delete) sync of the workspace `root` against the S3
/// bucket/prefix. Same rule as the WebDAV backend (`decide_both`).
#[tauri::command]
pub async fn s3_sync(
    root: String,
    endpoint: String,
    region: String,
    bucket: String,
    prefix: String,
    access_key: String,
    secret_key: String,
) -> Result<FolderSyncReport, String> {
    let root_path = PathBuf::from(&root);
    if !root_path.is_dir() {
        return Err("workspace root is not a directory".into());
    }
    let cfg = make_cfg(&endpoint, &region, &bucket, &prefix, &access_key, &secret_key)?;
    let c = client()?;

    let locals = enumerate_local(&root_path);
    let remotes = list_objects(&c, &cfg).await?;

    let mut all: BTreeSet<String> = locals.keys().cloned().collect();
    all.extend(remotes.keys().cloned());

    let mut report = FolderSyncReport::default();
    for rel in all {
        let local = locals.get(&rel);
        let remote = remotes.get(&rel);
        let step: Result<(), String> = async {
            match (local, remote) {
                (Some(l), None) => {
                    put_object(&c, &cfg, &rel, &l.abs).await?;
                    report.uploaded += 1;
                }
                (None, Some(r)) => {
                    if r.size > MAX_FILE_BYTES {
                        report.skipped += 1;
                    } else {
                        get_object(&c, &cfg, &rel, &root_path).await?;
                        report.downloaded += 1;
                    }
                }
                (Some(l), Some(r)) => match decide_both(l.size, l.mtime_ms, r.size, r.mtime_ms) {
                    SyncAction::Skip => report.skipped += 1,
                    SyncAction::Upload => {
                        put_object(&c, &cfg, &rel, &l.abs).await?;
                        report.uploaded += 1;
                    }
                    SyncAction::Download => {
                        get_object(&c, &cfg, &rel, &root_path).await?;
                        report.downloaded += 1;
                    }
                    SyncAction::Conflict => report.conflicts += 1,
                },
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
