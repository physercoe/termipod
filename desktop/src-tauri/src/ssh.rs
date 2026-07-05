//! Personal direct-SSH transport for the breakglass terminal (ADR-052, D-2
//! Path 2). A `russh` client in the Tauri core opens an interactive PTY and
//! bridges it to xterm.js in the webview over Tauri events:
//!
//!   frontend --invoke ssh_connect/ssh_write/ssh_resize/ssh_close--> core
//!   core     --emit "ssh-data" {id, bytes} / "ssh-exit" {id}------> frontend
//!
//! Keys/passwords are supplied per-connect from the webview and held only in
//! this process for the session's lifetime — never persisted, never sent to the
//! hub (the zero-knowledge vault sync of D-4 is a separate, later workstream).
//! This is the personal, host-runner-less path; the managed-host hub-brokered
//! PTY (D-2 Path 1 / D-6) is deferred.

use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;

use russh::client::{self, Handle};
use russh::keys::{decode_secret_key, PrivateKeyWithHashAlg, PublicKey};
use russh::ChannelMsg;
use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Emitter, State};
use tokio::sync::{mpsc, Mutex};

static NEXT_ID: AtomicU64 = AtomicU64::new(1);

/// Handler for the client side of the connection. Breakglass MVP accepts any
/// host key (trust-on-first-use, no known_hosts DB yet) — a deliberate,
/// documented tradeoff for occasional-use direct SSH; host-key pinning is a
/// follow-up that rides on the same vault sync as the keys (ADR-052).
struct Client;

impl client::Handler for Client {
    type Error = russh::Error;

    async fn check_server_key(&mut self, _server_public_key: &PublicKey) -> Result<bool, Self::Error> {
        Ok(true)
    }
}

/// Commands the per-session actor task accepts from invoke handlers.
enum SshCmd {
    Data(Vec<u8>),
    Resize { cols: u32, rows: u32 },
    Close,
}

/// Managed Tauri state: live sessions keyed by the id `ssh_connect` returns.
#[derive(Default)]
pub struct SshState {
    sessions: Arc<Mutex<HashMap<String, mpsc::Sender<SshCmd>>>>,
}

#[derive(Deserialize)]
pub struct SshConnectReq {
    host: String,
    port: u16,
    user: String,
    #[serde(default)]
    password: Option<String>,
    #[serde(default)]
    private_key: Option<String>,
    #[serde(default)]
    passphrase: Option<String>,
    cols: u32,
    rows: u32,
}

#[derive(Serialize, Clone)]
struct DataPayload {
    id: String,
    bytes: Vec<u8>,
}

#[derive(Serialize, Clone)]
struct ExitPayload {
    id: String,
}

/// Open a direct SSH session + interactive shell PTY. Returns a session id the
/// frontend uses for subsequent write/resize/close and to filter `ssh-data`.
#[tauri::command]
pub async fn ssh_connect(
    app: AppHandle,
    state: State<'_, SshState>,
    req: SshConnectReq,
) -> Result<String, String> {
    let config = Arc::new(client::Config::default());
    let mut session: Handle<Client> = client::connect(config, (req.host.as_str(), req.port), Client)
        .await
        .map_err(|e| format!("connect: {e}"))?;

    // Auth: a private key wins over a password when both are supplied.
    let authed = if let Some(pem) = req.private_key.as_ref().filter(|s| !s.trim().is_empty()) {
        let key = decode_secret_key(pem, req.passphrase.as_deref())
            .map_err(|e| format!("key parse: {e}"))?;
        let hash = session
            .best_supported_rsa_hash()
            .await
            .map_err(|e| format!("auth: {e}"))?
            .flatten();
        session
            .authenticate_publickey(&req.user, PrivateKeyWithHashAlg::new(Arc::new(key), hash))
            .await
            .map_err(|e| format!("auth: {e}"))?
            .success()
    } else if let Some(pw) = req.password.as_ref() {
        session
            .authenticate_password(&req.user, pw)
            .await
            .map_err(|e| format!("auth: {e}"))?
            .success()
    } else {
        return Err("no credentials supplied".into());
    };
    if !authed {
        return Err("authentication failed".into());
    }

    let mut channel = session
        .channel_open_session()
        .await
        .map_err(|e| format!("open channel: {e}"))?;
    channel
        .request_pty(false, "xterm-256color", req.cols, req.rows, 0, 0, &[])
        .await
        .map_err(|e| format!("request pty: {e}"))?;
    channel
        .request_shell(true)
        .await
        .map_err(|e| format!("request shell: {e}"))?;

    let id = format!("s{}", NEXT_ID.fetch_add(1, Ordering::Relaxed));
    let (tx, mut rx) = mpsc::channel::<SshCmd>(64);
    state.sessions.lock().await.insert(id.clone(), tx);

    let sessions = state.sessions.clone();
    let task_id = id.clone();
    tauri::async_runtime::spawn(async move {
        // The session Handle is moved in and kept alive for the channel's life.
        let _keepalive = session;
        loop {
            tokio::select! {
                msg = channel.wait() => match msg {
                    Some(ChannelMsg::Data { ref data }) => {
                        let _ = app.emit("ssh-data", DataPayload { id: task_id.clone(), bytes: data.to_vec() });
                    }
                    Some(ChannelMsg::ExtendedData { ref data, .. }) => {
                        let _ = app.emit("ssh-data", DataPayload { id: task_id.clone(), bytes: data.to_vec() });
                    }
                    Some(ChannelMsg::Eof) | Some(ChannelMsg::Close) | None => break,
                    _ => {}
                },
                cmd = rx.recv() => match cmd {
                    Some(SshCmd::Data(bytes)) => { let _ = channel.data(&bytes[..]).await; }
                    Some(SshCmd::Resize { cols, rows }) => { let _ = channel.window_change(cols, rows, 0, 0).await; }
                    Some(SshCmd::Close) | None => break,
                },
            }
        }
        sessions.lock().await.remove(&task_id);
        let _ = app.emit("ssh-exit", ExitPayload { id: task_id });
    });

    Ok(id)
}

async fn send(state: &State<'_, SshState>, id: &str, cmd: SshCmd) -> Result<(), String> {
    let tx = {
        let map = state.sessions.lock().await;
        map.get(id).cloned()
    };
    match tx {
        Some(tx) => tx.send(cmd).await.map_err(|_| "session closed".to_string()),
        None => Err("no such session".into()),
    }
}

#[tauri::command]
pub async fn ssh_write(state: State<'_, SshState>, id: String, data: String) -> Result<(), String> {
    send(&state, &id, SshCmd::Data(data.into_bytes())).await
}

#[tauri::command]
pub async fn ssh_resize(state: State<'_, SshState>, id: String, cols: u32, rows: u32) -> Result<(), String> {
    send(&state, &id, SshCmd::Resize { cols, rows }).await
}

#[tauri::command]
pub async fn ssh_close(state: State<'_, SshState>, id: String) -> Result<(), String> {
    // Best-effort: if the actor already exited the session is simply gone.
    let _ = send(&state, &id, SshCmd::Close).await;
    Ok(())
}
