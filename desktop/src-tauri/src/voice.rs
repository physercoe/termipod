//! Voice dictation bridge (parity — mobile cloud_stt.dart). DashScope's realtime
//! ASR is a WebSocket that authenticates with an `Authorization: bearer <key>`
//! header — which the webview's own `WebSocket` cannot set. So the socket lives
//! in the Rust core: the frontend streams PCM16/16k frames down via `voice_send`
//! and receives transcript events back over the `voice-event` Tauri channel.
//!
//!   frontend --voice_open--> core opens WS, sends run-task
//!   frontend --voice_send(pcm)--> core forwards binary audio frames
//!   frontend --voice_finish--> core sends finish-task; server flushes final
//!   core --emit "voice-event" {id, kind, text}--> frontend
//!
//! This is the personal-key path (the key is the director's own DashScope key,
//! held in the OS keychain); routing it through a hub-side proxy is a later idea.

use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;

use futures_util::{SinkExt, StreamExt};
use serde::{Deserialize, Serialize};
use serde_json::json;
use tauri::{AppHandle, Emitter, State};
use tokio::sync::{mpsc, Mutex};
use tokio_tungstenite::tungstenite::client::IntoClientRequest;
use tokio_tungstenite::tungstenite::Message;

const ENDPOINT: &str = "wss://dashscope.aliyuncs.com/api-ws/v1/inference";

static NEXT_ID: AtomicU64 = AtomicU64::new(1);

#[derive(Deserialize)]
pub struct VoiceOpenReq {
    api_key: String,
    model: String,
}

#[derive(Serialize, Clone)]
struct VoiceEvent {
    id: String,
    kind: String,
    text: String,
}

enum VoiceCmd {
    Audio(Vec<u8>),
    Finish,
    Close,
}

#[derive(Default)]
pub struct VoiceState {
    sessions: Arc<Mutex<HashMap<String, mpsc::Sender<VoiceCmd>>>>,
}

/// Removes a session-map entry when dropped, so a panic in the actor task can't
/// leak the entry (insert happens before spawn; an in-task `.remove()` is skipped
/// on an unwinding panic). Removal needs the async lock, so Drop schedules it.
struct SessionGuard {
    sessions: Arc<Mutex<HashMap<String, mpsc::Sender<VoiceCmd>>>>,
    id: String,
}

impl Drop for SessionGuard {
    fn drop(&mut self) {
        let sessions = self.sessions.clone();
        let id = std::mem::take(&mut self.id);
        tauri::async_runtime::spawn(async move {
            sessions.lock().await.remove(&id);
        });
    }
}

/// A random 32-hex task id (DashScope requires a unique id per task). Uses the
/// same getrandom-backed OsRng already pulled in for the vault crypto.
fn task_id() -> String {
    use rand_core::RngCore;
    let mut b = [0u8; 16];
    rand_core::OsRng.fill_bytes(&mut b);
    data_encoding::HEXLOWER.encode(&b)
}

fn run_task_json(task: &str, model: &str) -> String {
    json!({
        "header": {"action": "run-task", "task_id": task, "streaming": "duplex"},
        "payload": {
            "task_group": "audio",
            "task": "asr",
            "function": "recognition",
            "model": model,
            "parameters": {"format": "pcm", "sample_rate": 16000},
            "input": {}
        }
    })
    .to_string()
}

fn finish_task_json(task: &str) -> String {
    json!({
        "header": {"action": "finish-task", "task_id": task, "streaming": "duplex"},
        "payload": {"input": {}}
    })
    .to_string()
}

/// Parse one server frame into (kind, text). Returns None for frames that carry
/// no user-visible change (task-started, heartbeats).
fn parse_event(text: &str) -> Option<(String, String)> {
    let v: serde_json::Value = serde_json::from_str(text).ok()?;
    let event = v.get("header")?.get("event")?.as_str()?;
    match event {
        "result-generated" => {
            let sentence = v.get("payload")?.get("output")?.get("sentence")?;
            let t = sentence.get("text").and_then(|x| x.as_str()).unwrap_or("").to_string();
            let ended = sentence
                .get("sentence_end")
                .and_then(|x| x.as_bool())
                .unwrap_or(false);
            Some((if ended { "final" } else { "partial" }.to_string(), t))
        }
        "task-finished" => Some(("done".to_string(), String::new())),
        "task-failed" => {
            let m = v
                .get("header")
                .and_then(|h| h.get("error_message"))
                .and_then(|x| x.as_str())
                .unwrap_or("task failed")
                .to_string();
            Some(("error".to_string(), m))
        }
        _ => None,
    }
}

#[tauri::command]
pub async fn voice_open(app: AppHandle, state: State<'_, VoiceState>, req: VoiceOpenReq) -> Result<String, String> {
    let mut request = ENDPOINT
        .into_client_request()
        .map_err(|e| format!("request: {e}"))?;
    request.headers_mut().insert(
        "Authorization",
        format!("bearer {}", req.api_key)
            .parse()
            .map_err(|_| "bad api key".to_string())?,
    );
    let (ws, _) = tokio_tungstenite::connect_async(request)
        .await
        .map_err(|e| format!("connect: {e}"))?;
    let (mut write, mut read) = ws.split();

    let task = task_id();
    write
        .send(Message::Text(run_task_json(&task, &req.model).into()))
        .await
        .map_err(|e| format!("run-task: {e}"))?;

    let id = format!("v{}", NEXT_ID.fetch_add(1, Ordering::Relaxed));
    let (tx, mut rx) = mpsc::channel::<VoiceCmd>(64);
    state.sessions.lock().await.insert(id.clone(), tx);

    let sessions = state.sessions.clone();
    let emit_id = id.clone();
    let finish_task = task.clone();
    tauri::async_runtime::spawn(async move {
        // Guarantees the map entry is removed on ANY exit, incl. an unwinding
        // panic in the loop below.
        let _guard = SessionGuard { sessions, id: emit_id.clone() };
        let _ = app.emit("voice-event", VoiceEvent { id: emit_id.clone(), kind: "open".into(), text: String::new() });
        loop {
            tokio::select! {
                cmd = rx.recv() => match cmd {
                    Some(VoiceCmd::Audio(bytes)) => { let _ = write.send(Message::Binary(bytes.into())).await; }
                    Some(VoiceCmd::Finish) => { let _ = write.send(Message::Text(finish_task_json(&finish_task).into())).await; }
                    Some(VoiceCmd::Close) | None => { let _ = write.send(Message::Close(None)).await; break; }
                },
                msg = read.next() => match msg {
                    Some(Ok(Message::Text(t))) => {
                        let s: &str = &t;
                        if let Some((kind, text)) = parse_event(s) {
                            let done = kind == "done" || kind == "error";
                            let _ = app.emit("voice-event", VoiceEvent { id: emit_id.clone(), kind, text });
                            if done { break; }
                        }
                    }
                    Some(Ok(Message::Close(_))) | Some(Err(_)) | None => break,
                    _ => {}
                },
            }
        }
        // `_guard` removes the map entry when this task returns (or unwinds).
    });

    Ok(id)
}

async fn send(state: &State<'_, VoiceState>, id: &str, cmd: VoiceCmd) -> Result<(), String> {
    let tx = {
        let map = state.sessions.lock().await;
        map.get(id).cloned()
    };
    match tx {
        Some(tx) => tx.send(cmd).await.map_err(|_| "voice session closed".to_string()),
        None => Err("no such voice session".into()),
    }
}

#[tauri::command]
pub async fn voice_send(state: State<'_, VoiceState>, id: String, pcm_b64: String) -> Result<(), String> {
    use base64::Engine as _;
    let bytes = base64::engine::general_purpose::STANDARD
        .decode(pcm_b64.as_bytes())
        .map_err(|e| format!("decode: {e}"))?;
    send(&state, &id, VoiceCmd::Audio(bytes)).await
}

#[tauri::command]
pub async fn voice_finish(state: State<'_, VoiceState>, id: String) -> Result<(), String> {
    send(&state, &id, VoiceCmd::Finish).await
}

#[tauri::command]
pub async fn voice_close(state: State<'_, VoiceState>, id: String) -> Result<(), String> {
    let _ = send(&state, &id, VoiceCmd::Close).await;
    Ok(())
}
