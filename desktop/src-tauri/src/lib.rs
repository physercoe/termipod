use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;

use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Emitter, State};
use tokio::sync::{Mutex, Notify};

mod keychain;
mod pty;
mod ssh;
mod vault;
mod voice;

/// A REST request proxied through the Rust core (WS2/WS8). This lets the desktop
/// build keep the bearer token out of the webview JS AND sidestep CORS: the
/// webview origin is `tauri://localhost`, so a direct `fetch` to the hub is
/// cross-origin and the hub sends no CORS headers ("Failed to fetch"). reqwest
/// here is not a browser and is subject to neither. The plain-browser build
/// uses `fetch` directly.
#[derive(Deserialize)]
struct HubRequest {
    method: String,
    url: String,
    #[serde(default)]
    headers: HashMap<String, String>,
    #[serde(default)]
    body: Option<String>,
}

#[derive(Serialize)]
struct HubResponse {
    status: u16,
    body: String,
}

/// A binary hub response — the raw bytes base64-encoded so they survive the
/// string-typed IPC bridge (the webview's JSON transport corrupts non-UTF-8
/// bytes). Used for content-addressed blobs (run images, PDFs) via
/// `GET /v1/blobs/{sha}`.
#[derive(Serialize)]
struct HubBytesResponse {
    status: u16,
    mime: String,
    base64: String,
}

#[tauri::command]
async fn hub_request(req: HubRequest) -> Result<HubResponse, String> {
    let method = reqwest::Method::from_bytes(req.method.as_bytes()).map_err(|e| e.to_string())?;
    let client = reqwest::Client::new();
    let mut builder = client.request(method, &req.url);
    for (key, value) in req.headers {
        builder = builder.header(key, value);
    }
    if let Some(body) = req.body {
        builder = builder.body(body);
    }
    let resp = builder.send().await.map_err(|e| e.to_string())?;
    let status = resp.status().as_u16();
    let body = resp.text().await.map_err(|e| e.to_string())?;
    Ok(HubResponse { status, body })
}

/// Like `hub_request` but returns the response body as base64 plus its
/// content-type, so binary blobs (images, PDFs) survive the string IPC bridge.
#[tauri::command]
async fn hub_request_bytes(req: HubRequest) -> Result<HubBytesResponse, String> {
    use base64::Engine as _;
    let method = reqwest::Method::from_bytes(req.method.as_bytes()).map_err(|e| e.to_string())?;
    let client = reqwest::Client::new();
    let mut builder = client.request(method, &req.url);
    for (key, value) in req.headers {
        builder = builder.header(key, value);
    }
    if let Some(body) = req.body {
        builder = builder.body(body);
    }
    let resp = builder.send().await.map_err(|e| e.to_string())?;
    let status = resp.status().as_u16();
    let mime = resp
        .headers()
        .get(reqwest::header::CONTENT_TYPE)
        .and_then(|v| v.to_str().ok())
        .unwrap_or("")
        .to_string();
    let bytes = resp.bytes().await.map_err(|e| e.to_string())?;
    let base64 = base64::engine::general_purpose::STANDARD.encode(&bytes);
    Ok(HubBytesResponse { status, mime, base64 })
}

// ---- external proxy resolution (updater) ------------------------------------
// The GitHub updater (`latest.json` + the installer) is the only traffic that
// crosses the corporate boundary — the hub itself is on the intranet and is
// reached directly. reqwest, which the updater uses under the hood, honours
// proxy *environment variables* but NOT the Windows "system proxy" registry, so
// on a locked-down intranet the update request is attempted directly, never
// reaches the corporate proxy, and fails with "error sending request for url".
// This command resolves a proxy URL the frontend hands to `check({ proxy })`
// (which the plugin applies to both the check and the download).

/// Best-effort resolve of an HTTP(S) proxy for reaching external services.
/// Precedence: standard proxy env vars, then the Windows system-proxy registry.
/// Returns `None` when no proxy is configured (direct connection).
#[tauri::command]
fn system_proxy() -> Option<String> {
    for key in [
        "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "HTTP_PROXY", "http_proxy",
    ] {
        if let Ok(v) = std::env::var(key) {
            let v = v.trim();
            if !v.is_empty() {
                return Some(normalize_proxy(v));
            }
        }
    }
    #[cfg(windows)]
    {
        if let Some(p) = windows_system_proxy() {
            return Some(normalize_proxy(&p));
        }
    }
    None
}

/// reqwest wants a scheme-qualified URL; the Windows registry (and bare env
/// values) may be a plain `host:port`.
fn normalize_proxy(raw: &str) -> String {
    let s = raw.trim();
    if s.contains("://") {
        s.to_string()
    } else {
        format!("http://{s}")
    }
}

#[cfg(windows)]
fn windows_system_proxy() -> Option<String> {
    use std::os::windows::process::CommandExt;
    use std::process::Command;
    // CREATE_NO_WINDOW: keep the child `reg` process from flashing a console
    // window. Without it, each spawn pops (then closes) a black console — the
    // director saw "two windows open/close" when Settings queried the proxy
    // (this runs `reg` twice: ProxyEnable, then ProxyServer).
    const CREATE_NO_WINDOW: u32 = 0x0800_0000;
    let base = r"HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings";
    // ProxyEnable (REG_DWORD) must be 0x1.
    let enabled = Command::new("reg")
        .args(["query", base, "/v", "ProxyEnable"])
        .creation_flags(CREATE_NO_WINDOW)
        .output()
        .ok()?;
    let on = String::from_utf8_lossy(&enabled.stdout)
        .lines()
        .find(|l| l.contains("ProxyEnable"))
        .map(|l| l.trim_end().ends_with("0x1"))
        .unwrap_or(false);
    if !on {
        return None;
    }
    let server = Command::new("reg")
        .args(["query", base, "/v", "ProxyServer"])
        .creation_flags(CREATE_NO_WINDOW)
        .output()
        .ok()?;
    let raw = String::from_utf8_lossy(&server.stdout)
        .lines()
        .find(|l| l.contains("ProxyServer"))
        .and_then(|l| l.split("REG_SZ").nth(1))
        .map(|s| s.trim().to_string())?;
    if raw.is_empty() {
        return None;
    }
    Some(pick_https_proxy(&raw))
}

/// The registry `ProxyServer` value is either a bare `host:port` shared by all
/// protocols, or a per-scheme list like `http=h:p;https=h:p;ftp=h:p`. Prefer the
/// https entry (falling back to http, then the whole string).
#[cfg(windows)]
fn pick_https_proxy(raw: &str) -> String {
    if raw.contains('=') {
        for scheme in ["https=", "http="] {
            for part in raw.split(';') {
                if let Some(rest) = part.trim().strip_prefix(scheme) {
                    let rest = rest.trim();
                    if !rest.is_empty() {
                        return rest.to_string();
                    }
                }
            }
        }
    }
    raw.to_string()
}

// ---- SSE streaming proxy ----------------------------------------------------
// The hub's live streams (`…/agents/{id}/stream`, `…/channels/{ch}/stream`) are
// SSE over a bearer header. In the browser build the frontend reads them with
// `fetch`; under Tauri that is the same cross-origin/no-CORS problem, so the
// core streams the bytes and re-emits them as `hub-sse` events. The frontend
// keeps owning frame parsing, the `since` cursor, and reconnect/backoff — the
// core is a dumb pipe (one task per connection attempt).

static NEXT_SSE: AtomicU64 = AtomicU64::new(1);

/// Live SSE streams keyed by the id `hub_sse_open` returns. The value is a
/// cancellation `Notify` (inserted *before* the task spawns, so there is no
/// insert-vs-finish race) that `hub_sse_close` fires; the task removes its own
/// entry on exit, so naturally-ended streams don't leak across reconnects.
#[derive(Default)]
pub struct SseState {
    streams: Arc<Mutex<HashMap<String, Arc<Notify>>>>,
}

#[derive(Deserialize)]
struct SseOpenReq {
    url: String,
    token: String,
}

#[derive(Serialize, Clone)]
struct SseChunk {
    id: String,
    bytes: Vec<u8>,
}

#[derive(Serialize, Clone)]
struct SseEnd {
    id: String,
    error: Option<String>,
}

#[tauri::command]
async fn hub_sse_open(app: AppHandle, state: State<'_, SseState>, req: SseOpenReq) -> Result<String, String> {
    let client = reqwest::Client::new();
    let resp = client
        .get(&req.url)
        .header("authorization", format!("Bearer {}", req.token))
        .header("accept", "text/event-stream")
        .send()
        .await
        .map_err(|e| e.to_string())?;
    if !resp.status().is_success() {
        return Err(format!("sse status {}", resp.status().as_u16()));
    }

    let id = format!("e{}", NEXT_SSE.fetch_add(1, Ordering::Relaxed));
    let cancel = Arc::new(Notify::new());
    // Insert before spawning so a stream that ends instantly can't remove an
    // entry that isn't there yet.
    state.streams.lock().await.insert(id.clone(), cancel.clone());

    let streams = state.streams.clone();
    let task_id = id.clone();
    tauri::async_runtime::spawn(async move {
        let mut resp = resp;
        let err = loop {
            tokio::select! {
                _ = cancel.notified() => break None,
                chunk = resp.chunk() => match chunk {
                    Ok(Some(bytes)) => {
                        let _ = app.emit("hub-sse", SseChunk { id: task_id.clone(), bytes: bytes.to_vec() });
                    }
                    Ok(None) => break None,
                    Err(e) => break Some(e.to_string()),
                },
            }
        };
        streams.lock().await.remove(&task_id);
        let _ = app.emit("hub-sse-end", SseEnd { id: task_id, error: err });
    });
    Ok(id)
}

#[tauri::command]
async fn hub_sse_close(state: State<'_, SseState>, id: String) -> Result<(), String> {
    // Fire the cancel; the task removes its own map entry on exit.
    if let Some(cancel) = state.streams.lock().await.get(&id) {
        cancel.notify_one();
    }
    Ok(())
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    // Register the OS credential store before any keychain command can run —
    // keyring 4.1.3's own lazy registration is a no-op (see keychain.rs).
    keychain::init_default_store();
    tauri::Builder::default()
        .plugin(tauri_plugin_process::init())
        .plugin(tauri_plugin_updater::Builder::new().build())
        .manage(ssh::SshState::default())
        .manage(pty::PtyState::default())
        .manage(voice::VoiceState::default())
        .manage(SseState::default())
        .invoke_handler(tauri::generate_handler![
            hub_request,
            hub_request_bytes,
            system_proxy,
            hub_sse_open,
            hub_sse_close,
            keychain::keychain_set,
            keychain::keychain_get,
            keychain::keychain_delete,
            ssh::ssh_connect,
            ssh::ssh_write,
            ssh::ssh_resize,
            ssh::ssh_close,
            ssh::ssh_parse_key,
            ssh::ssh_exec,
            ssh::sftp_list,
            ssh::sftp_read,
            ssh::sftp_write,
            pty::pty_open,
            pty::pty_write,
            pty::pty_resize,
            pty::pty_close,
            voice::voice_open,
            voice::voice_send,
            voice::voice_finish,
            voice::voice_close,
            vault::vault_generate_key,
            vault::vault_seal,
            vault::vault_open,
            vault::vault_generate_device,
            vault::vault_wrap_for_device,
            vault::vault_unwrap_device,
            vault::vault_wrap_for_recovery,
            vault::vault_unwrap_recovery,
            vault::vault_generate_recovery_code,
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
