use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;

use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Emitter, State};
use tokio::sync::{Mutex, Notify};

mod docfile;
mod drawio;
mod foldersync;
mod handoff;
mod keychain;
mod local_agent;
mod localfs;
mod migration;
mod net;
mod pty;
mod s3;
mod script;
mod ssh;
mod storage;
mod vault;
mod voice;
mod webdav;
mod workspace;
mod workspacefs;

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
    /// Proxy URL to route this request through (Network settings, `hub` toggle
    /// on), or `None` for a direct connection. See `net::client_builder`.
    #[serde(default)]
    proxy: Option<String>,
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
    // Bound the request so a hung/unreachable hub fails instead of pending
    // forever (the webview poller would otherwise stay "fetching" indefinitely).
    // JSON API calls are small, so a 30s total cap is ample.
    let client = net::client_builder(req.proxy.as_deref())
        .connect_timeout(std::time::Duration::from_secs(15))
        .timeout(std::time::Duration::from_secs(30))
        .build()
        .map_err(|e| e.to_string())?;
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
    // Bound the connection, but allow a generous total window — blobs (images,
    // PDFs) can be large and download slowly over a constrained link.
    let client = net::client_builder(req.proxy.as_deref())
        .connect_timeout(std::time::Duration::from_secs(15))
        .timeout(std::time::Duration::from_secs(120))
        .build()
        .map_err(|e| e.to_string())?;
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
    #[cfg(target_os = "macos")]
    {
        if let Some(p) = macos_system_proxy() {
            return Some(normalize_proxy(&p));
        }
    }
    None
}

/// The machine's hostname, for the vault "last synced from <machine>" label.
/// Dependency-free and best-effort: env first (`COMPUTERNAME` on Windows,
/// `HOSTNAME` on unix), then `/etc/hostname`, then the `hostname` command. Trims
/// a trailing DNS suffix so "mac-studio.local" reads as "mac-studio". `None` when
/// nothing resolves (the caller falls back to the OS-platform label).
#[tauri::command]
fn system_hostname() -> Option<String> {
    #[cfg(windows)]
    let env_keys: [&str; 1] = ["COMPUTERNAME"];
    #[cfg(not(windows))]
    let env_keys: [&str; 2] = ["HOSTNAME", "HOST"];
    for key in env_keys {
        if let Ok(v) = std::env::var(key) {
            if let Some(name) = clean_hostname(&v) {
                return Some(name);
            }
        }
    }
    #[cfg(unix)]
    {
        if let Ok(v) = std::fs::read_to_string("/etc/hostname") {
            if let Some(name) = clean_hostname(&v) {
                return Some(name);
            }
        }
        if let Ok(out) = std::process::Command::new("hostname").output() {
            if let Some(name) = clean_hostname(&String::from_utf8_lossy(&out.stdout)) {
                return Some(name);
            }
        }
    }
    None
}

/// The OS this build is running on, as a stable lowercase token
/// ("windows" | "macos" | "linux" | …). `std::env::consts::OS` is resolved at
/// COMPILE time from the target triple, so it can't be spoofed the way the
/// webview's navigator.userAgent can — the frontend uses it to gate the terminal
/// GPU renderer, whose only known failure is a black screen on Windows WebView2
/// (#333), so the gate must be exact.
#[tauri::command]
fn platform_os() -> String {
    std::env::consts::OS.to_string()
}

/// The Windows OS build number (e.g. 22631), or `None` off Windows / on any
/// failure. Read the same cheap `reg query` way `windows_system_proxy` reads the
/// proxy — no extra crate. The desktop terminal hands it to xterm's `windowsPty`
/// so xterm applies the ConPTY scrollback/reflow behaviour correct for THIS build
/// (native line-wrap sequences landed in build 21376; reflow should stay on above
/// it, off below). Without a build number xterm can't tell, disables reflow, and —
/// worse — ConPTY grows the viewport by making empty rows at the bottom instead of
/// pulling scrollback in, so a repainting TUI's intermediate frames pile up in the
/// scrollback (director report, Windows: "scroll up to see the intermediate
/// drawing content"). On `None` the frontend still passes `{ backend: 'conpty' }`,
/// which fixes the duplicate-scrollback bug but loses the build-gated reflow.
#[tauri::command]
fn os_build_number() -> Option<u32> {
    #[cfg(target_os = "windows")]
    {
        windows_build_number()
    }
    #[cfg(not(target_os = "windows"))]
    {
        None
    }
}

/// Read `CurrentBuildNumber` (REG_SZ, e.g. "22631") from the Windows version key.
#[cfg(target_os = "windows")]
fn windows_build_number() -> Option<u32> {
    use std::os::windows::process::CommandExt;
    use std::process::Command;
    // CREATE_NO_WINDOW so the child `reg` never flashes a console window (same
    // reason as windows_system_proxy).
    const CREATE_NO_WINDOW: u32 = 0x0800_0000;
    let out = Command::new("reg")
        .args([
            "query",
            r"HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion",
            "/v",
            "CurrentBuildNumber",
        ])
        .creation_flags(CREATE_NO_WINDOW)
        .output()
        .ok()?;
    // A match line reads: "    CurrentBuildNumber    REG_SZ    22631".
    String::from_utf8_lossy(&out.stdout)
        .lines()
        .find(|l| l.contains("CurrentBuildNumber"))
        .and_then(|l| l.split_whitespace().last())
        .and_then(|v| v.parse::<u32>().ok())
}

/// Trim whitespace and any DNS suffix from a raw hostname; `None` when empty.
fn clean_hostname(raw: &str) -> Option<String> {
    let s = raw.trim();
    let short = s.split('.').next().unwrap_or(s).trim();
    if short.is_empty() {
        None
    } else {
        Some(short.to_string())
    }
}

/// Read the active macOS system HTTP(S) proxy from `scutil --proxy` (the same
/// config the System Settings → Network → Proxies panel writes). No new
/// dependency — mirrors the Windows `reg` approach. Prefers HTTPS, falls back to
/// HTTP; returns `host:port` (normalised to a URL by the caller). `None` when no
/// proxy is enabled, or on any parse/spawn failure (best-effort).
#[cfg(target_os = "macos")]
fn macos_system_proxy() -> Option<String> {
    use std::process::Command;
    let out = Command::new("scutil").arg("--proxy").output().ok()?;
    let text = String::from_utf8_lossy(&out.stdout);
    // `scutil --proxy` prints a dictionary, one `Key : Value` per line, e.g.
    //   HTTPSEnable : 1
    //   HTTPSProxy : proxy.corp
    //   HTTPSPort : 8080
    let field = |key: &str| -> Option<String> {
        text.lines()
            .find_map(|l| l.trim().strip_prefix(key)?.trim().strip_prefix(':').map(|v| v.trim().to_string()))
    };
    for (enable, host_key, port_key) in [
        ("HTTPSEnable", "HTTPSProxy", "HTTPSPort"),
        ("HTTPEnable", "HTTPProxy", "HTTPPort"),
    ] {
        if field(enable).as_deref() != Some("1") {
            continue;
        }
        let host = field(host_key).filter(|h| !h.is_empty())?;
        return Some(match field(port_key).filter(|p| !p.is_empty()) {
            Some(port) => format!("{host}:{port}"),
            None => host,
        });
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
    /// Proxy URL (Network settings, `hub` toggle on), or `None` for direct.
    #[serde(default)]
    proxy: Option<String>,
}

#[derive(Serialize, Clone)]
struct SseChunk {
    id: String,
    /// Base64 of the chunk bytes. A raw `Vec<u8>` crosses the Tauri IPC boundary
    /// as a JSON array (`[104,105,…]`) — ~4–6 JSON characters per byte, so a
    /// busy stream serializes/parses several times its own size on every frame.
    /// Base64 is ~1.33× the byte length and decodes in one call on the JS side.
    b64: String,
}

#[derive(Serialize, Clone)]
struct SseEnd {
    id: String,
    error: Option<String>,
}

#[tauri::command]
async fn hub_sse_open(app: AppHandle, state: State<'_, SseState>, req: SseOpenReq) -> Result<String, String> {
    let client = net::client_builder(req.proxy.as_deref()).build().map_err(|e| e.to_string())?;
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
                        use base64::Engine as _;
                        let b64 = base64::engine::general_purpose::STANDARD.encode(&bytes);
                        let _ = app.emit("hub-sse", SseChunk { id: task_id.clone(), b64 });
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

// ---- open external URL ------------------------------------------------------
// A raw navigation to an external URL (e.g. clicking a link) replaces the SPA in
// the single webview and strands the user with no in-app way back (director
// report: a link inside a PDF "jumped the whole app"). The frontend routes every
// external link here so it opens in the OS default browser instead — the app
// webview never navigates away.

/// Open an http(s)/mailto URL in the OS default handler. Refuses any other scheme
/// so an arbitrary string never reaches the shell.
#[tauri::command]
fn open_external(url: String) -> Result<(), String> {
    let ok = ["http://", "https://", "mailto:"]
        .iter()
        .any(|p| url.starts_with(p));
    if !ok {
        return Err("unsupported url scheme".into());
    }
    open_url(&url)
}

#[cfg(target_os = "windows")]
fn open_url(url: &str) -> Result<(), String> {
    use std::os::windows::process::CommandExt;
    use std::process::Command;
    // CREATE_NO_WINDOW: no flashing console (same rationale as the proxy query).
    // The empty "" is `start`'s title arg, so a quoted URL isn't taken as a title.
    const CREATE_NO_WINDOW: u32 = 0x0800_0000;
    Command::new("cmd")
        .args(["/C", "start", "", url])
        .creation_flags(CREATE_NO_WINDOW)
        .spawn()
        .map(|_| ())
        .map_err(|e| e.to_string())
}

#[cfg(target_os = "macos")]
fn open_url(url: &str) -> Result<(), String> {
    std::process::Command::new("open")
        .arg(url)
        .spawn()
        .map(|_| ())
        .map_err(|e| e.to_string())
}

#[cfg(all(unix, not(target_os = "macos")))]
fn open_url(url: &str) -> Result<(), String> {
    std::process::Command::new("xdg-open")
        .arg(url)
        .spawn()
        .map(|_| ())
        .map_err(|e| e.to_string())
}

/// Reveal a file in the OS file manager (selecting it where the platform supports
/// it), so the user can find a linked attachment on disk. The path is passed as a
/// single process argument — never through a shell — so it can't inject a command.
#[tauri::command]
fn reveal_path(path: String) -> Result<(), String> {
    if path.trim().is_empty() {
        return Err("empty path".into());
    }
    reveal(&path)
}

#[cfg(target_os = "windows")]
fn reveal(path: &str) -> Result<(), String> {
    use std::os::windows::process::CommandExt;
    use std::process::Command;
    const CREATE_NO_WINDOW: u32 = 0x0800_0000;
    // `explorer /select,"<path>"` selects the file in its folder. explorer returns
    // a non-zero exit code even on success, so we only require that it spawned.
    // raw_arg keeps our exact quoting (a Windows path can't contain a `"`), which
    // Rust's default arg-quoting would otherwise mangle for explorer.
    //
    // CRITICAL: explorer's /select needs BACKSLASH separators. The path arrives
    // from the frontend with forward slashes (the linked storage folder joined to a
    // Zotero storage key, which is `/`-delimited); a forward-slash path makes
    // explorer silently ignore the selection and open "This PC" instead of the
    // containing folder. Normalize `/` → `\` before handing it over.
    let win_path = path.replace('/', "\\");
    Command::new("explorer")
        .raw_arg(format!("/select,\"{win_path}\""))
        .creation_flags(CREATE_NO_WINDOW)
        .spawn()
        .map(|_| ())
        .map_err(|e| e.to_string())
}

#[cfg(target_os = "macos")]
fn reveal(path: &str) -> Result<(), String> {
    // `open -R` reveals and selects the file in Finder.
    std::process::Command::new("open")
        .args(["-R", path])
        .spawn()
        .map(|_| ())
        .map_err(|e| e.to_string())
}

#[cfg(all(unix, not(target_os = "macos")))]
fn reveal(path: &str) -> Result<(), String> {
    // No portable "select the file" on Linux desktops — open the containing folder.
    let target = std::path::Path::new(path)
        .parent()
        .map(std::path::Path::to_path_buf)
        .unwrap_or_else(|| std::path::PathBuf::from(path));
    std::process::Command::new("xdg-open")
        .arg(target)
        .spawn()
        .map(|_| ())
        .map_err(|e| e.to_string())
}

// ---- in-app browser window --------------------------------------------------
// The J1 in-app browser tab is an <iframe>, which many sites forbid via
// `X-Frame-Options` / `frame-ancestors` (arxiv.org, Google Scholar, most
// publishers) — the director saw "arxiv.org 拒绝连接". A real **webview window**
// is a top-level browsing context, NOT an iframe, so those headers don't apply
// and every site loads. This is the app acting as a browser for framing-blocked
// sites; the user navigates via the page's own links (Alt+← / Alt+→ for history).

static NEXT_WIN: AtomicU64 = AtomicU64::new(1);

// A Tauri webview window never spawns a *child* window, so a link that opens in a
// new tab (`target="_blank"` or `window.open(...)`) is silently dropped — the
// reported "some sites' links don't open". Inject a script (runs at document
// start on every navigation) that rewrites those into a same-window navigation,
// so every link keeps working inside the one browser window.
const KEEP_LINKS_IN_WINDOW: &str = r#"
(function () {
  try {
    var nativeOpen = window.open;
    window.open = function (u) { if (u) { window.location.href = u; } return null; };
    window.__nativeOpen = nativeOpen;
    document.addEventListener('click', function (e) {
      var el = e.target;
      var a = el && el.closest ? el.closest('a[target]') : null;
      if (a && a.target && a.target !== '_self') { a.target = '_self'; }
    }, true);
  } catch (_) {}
})();
"#;

/// Open a URL in a real in-app browser window (a Tauri webview window, not an
/// iframe) so `X-Frame-Options` sites load. Only http(s).
#[tauri::command]
async fn open_browser_window(app: AppHandle, url: String) -> Result<(), String> {
    if !(url.starts_with("http://") || url.starts_with("https://")) {
        return Err("unsupported url scheme".into());
    }
    let parsed = tauri::Url::parse(&url).map_err(|e| e.to_string())?;
    let label = format!("browser-{}", NEXT_WIN.fetch_add(1, Ordering::Relaxed));
    tauri::WebviewWindowBuilder::new(&app, label, tauri::WebviewUrl::External(parsed))
        .title("TermiPod Browser")
        .inner_size(1024.0, 800.0)
        .initialization_script(KEEP_LINKS_IN_WINDOW)
        .build()
        .map_err(|e| e.to_string())?;
    Ok(())
}

/// Detect that a page REFUSES to be framed — `X-Frame-Options: DENY/SAMEORIGIN`,
/// or a CSP `frame-ancestors` our custom tauri:// origin can't match — so the
/// in-app browser tab can show an actionable error instead of a silently blank
/// iframe (#322). The webview's own fetch can't read cross-origin headers
/// (CORS), so the preflight runs here through reqwest. Best-effort: an
/// unreachable site answers `false` (not refused) and the iframe gets its chance.
#[tauri::command]
async fn frame_check(url: String) -> Result<bool, String> {
    if !(url.starts_with("http://") || url.starts_with("https://")) {
        return Err("unsupported url scheme".into());
    }
    let client = net::client_builder(None)
        .redirect(reqwest::redirect::Policy::limited(5))
        .timeout(std::time::Duration::from_secs(10))
        // A browser-ish UA: some CDNs only emit the framing headers to those.
        .user_agent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15")
        .build()
        .map_err(|e| e.to_string())?;
    // Headers only — the response body is dropped unread, so this stays cheap.
    let resp = match client.get(&url).send().await {
        Ok(r) => r,
        Err(_) => return Ok(false), // unreachable ≠ refused — let the iframe try
    };
    let headers = resp.headers();
    if let Some(xfo) = headers.get("x-frame-options").and_then(|v| v.to_str().ok()) {
        let v = xfo.trim();
        // SAMEORIGIN never matches our tauri:// origin. ALLOW-FROM is obsolete
        // and ignored by modern engines, so it is NOT a refusal.
        if v.eq_ignore_ascii_case("deny") || v.eq_ignore_ascii_case("sameorigin") {
            return Ok(true);
        }
    }
    for value in headers.get_all("content-security-policy").iter() {
        let Ok(csp) = value.to_str() else { continue };
        for directive in csp.split(';') {
            let mut parts = directive.trim().split_whitespace();
            let Some(name) = parts.next() else { continue };
            if !name.eq_ignore_ascii_case("frame-ancestors") {
                continue;
            }
            // Only a bare `*` lets a tauri://localhost ancestor through.
            if !parts.any(|s| s == "*") {
                return Ok(true);
            }
        }
    }
    Ok(false)
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    // Register the OS credential store before any keychain command can run —
    // keyring 4.1.3's own lazy registration is a no-op (see keychain.rs).
    keychain::init_default_store();
    tauri::Builder::default()
        .plugin(tauri_plugin_process::init())
        .plugin(tauri_plugin_updater::Builder::new().build())
        .plugin(tauri_plugin_dialog::init())
        // Serve the downloaded, offline draw.io webapp to an in-app iframe. A
        // custom scheme (not the asset protocol) so the webapp's own relative
        // asset URLs resolve. drawio.rs guards path traversal.
        .register_uri_scheme_protocol("drawio", |ctx, request| {
            let path = request.uri().path().to_string();
            match drawio::serve(ctx.app_handle(), &path) {
                Ok((bytes, mime)) => tauri::http::Response::builder()
                    .status(200)
                    .header(tauri::http::header::CONTENT_TYPE, mime)
                    .header("Access-Control-Allow-Origin", "*")
                    .body(bytes)
                    .unwrap(),
                Err(_) => tauri::http::Response::builder()
                    .status(404)
                    .body(Vec::new())
                    .unwrap(),
            }
        })
        .manage(ssh::SshState::default())
        .manage(pty::PtyState::default())
        .manage(voice::VoiceState::default())
        .manage(SseState::default())
        .invoke_handler(tauri::generate_handler![
            hub_request,
            hub_request_bytes,
            system_proxy,
            system_hostname,
            platform_os,
            os_build_number,
            open_external,
            reveal_path,
            open_browser_window,
            frame_check,
            handoff::handoff_check,
            migration::migration_export,
            migration::migration_read,
            storage::storage_pick_folder,
            storage::storage_reindex,
            storage::storage_read,
            storage::attachment_default_dir,
            storage::attachment_pick_file,
            storage::attachment_pick_dir,
            storage::attachment_add,
            storage::attachment_write_bytes,
            storage::attachment_read,
            storage::attachment_delete,
            storage::save_image_as,
            webdav::webdav_verify,
            webdav::webdav_sync,
            foldersync::folder_webdav_verify,
            foldersync::folder_webdav_sync,
            s3::s3_sync_verify,
            s3::s3_sync,
            s3::s3_zotero_sync,
            docfile::doc_open,
            docfile::doc_read,
            docfile::doc_save,
            docfile::doc_write,
            workspace::workspace_pick_folder,
            workspace::workspace_list,
            workspacefs::workspace_new_file,
            workspacefs::workspace_new_folder,
            workspacefs::workspace_rename,
            workspacefs::workspace_delete,
            workspacefs::workspace_move,
            workspacefs::workspace_copy,
            local_agent::local_agent_run,
            script::script_run,
            localfs::localfs_home,
            localfs::localfs_list,
            localfs::localfs_read,
            localfs::localfs_write,
            drawio::drawio_status,
            drawio::drawio_download,
            drawio::drawio_install_file,
            hub_sse_open,
            hub_sse_close,
            keychain::keychain_set,
            keychain::keychain_get,
            keychain::keychain_delete,
            keychain::keychain_is_windows,
            ssh::ssh_connect,
            ssh::ssh_duplicate,
            ssh::ssh_write,
            ssh::ssh_resize,
            ssh::ssh_close,
            ssh::ssh_parse_key,
            ssh::ssh_generate_key,
            ssh::ssh_exec,
            ssh::sftp_list,
            ssh::sftp_read,
            ssh::sftp_write,
            pty::pty_open,
            pty::pty_start,
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
