use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;

use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Emitter, State};
use tokio::sync::{Mutex, Notify};

mod ssh;

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
    tauri::Builder::default()
        .manage(ssh::SshState::default())
        .manage(SseState::default())
        .invoke_handler(tauri::generate_handler![
            hub_request,
            hub_sse_open,
            hub_sse_close,
            ssh::ssh_connect,
            ssh::ssh_write,
            ssh::ssh_resize,
            ssh::ssh_close,
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
