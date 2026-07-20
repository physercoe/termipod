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

/// Handler for the client side of the connection. Verifies the server's host key
/// with **trust-on-first-use pinning**: the first key seen for a `host:port` is
/// pinned to the OS keychain, and every later connection must present the same
/// key. A changed key (an active MITM presenting its own key on a network the
/// attacker controls) is **rejected**, not silently accepted as before.
struct Client {
    host: String,
    port: u16,
}

impl Client {
    fn pin_key(&self) -> String {
        format!("sshhostkey_{}_{}", self.host, self.port)
    }
}

impl client::Handler for Client {
    type Error = russh::Error;

    async fn check_server_key(&mut self, server_public_key: &PublicKey) -> Result<bool, Self::Error> {
        // The key's OpenSSH string form (algorithm + base64), for comparison.
        // Both the pinned and presented strings come from this same path, so the
        // exact formatting only needs to be self-consistent, not canonical.
        let presented = server_public_key.to_string();
        match crate::keychain::pin_get(&self.pin_key()) {
            // Known host: accept only if the key is byte-identical to the pin.
            Some(pinned) => Ok(pinned == presented),
            // First contact: pin it (TOFU) and accept. A keychain write failure
            // still connects — parity with the prior accept-any behaviour — but
            // the pin simply won't persist to catch a future change.
            None => {
                let _ = crate::keychain::pin_set(&self.pin_key(), &presented);
                Ok(true)
            }
        }
    }
}

/// Commands the per-session actor task accepts from invoke handlers.
enum SshCmd {
    Data(Vec<u8>),
    Resize { cols: u32, rows: u32 },
    Close,
}

/// A live session: the PTY actor's command channel plus a shared clone of the
/// russh `Handle`, which lets us open *additional* channels (one-shot `exec` for
/// tmux control, an SFTP subsystem for file transfer) on the same connection
/// without disturbing the interactive shell.
struct Session {
    tx: mpsc::Sender<SshCmd>,
    handle: Arc<Handle<Client>>,
}

/// Managed Tauri state: live sessions keyed by the id `ssh_connect` returns.
#[derive(Default)]
pub struct SshState {
    sessions: Arc<Mutex<HashMap<String, Session>>>,
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
    /// Frontend-minted attempt id, echoed back on `ssh-connect-progress` phase
    /// ticks so the connect form can match ticks to its in-flight attempt
    /// (#319) — the same pattern as the SFTP `transfer_id`. Absent = no ticks.
    #[serde(default)]
    connect_id: Option<String>,
}

#[derive(Serialize, Clone)]
struct DataPayload {
    id: String,
    bytes: Vec<u8>,
}

#[derive(Serialize, Clone)]
struct ExitPayload {
    id: String,
    /// The remote command's exit status, if the server sent one before closing —
    /// lets the UI distinguish a clean logout from a dropped/failed session.
    code: Option<u32>,
}

/// A connect-phase tick, emitted on `ssh-connect-progress` as `ssh_connect`
/// walks its handshake so the form can show which stage a slow connect is in
/// (#319). `phase`: "tcp" (connect + key exchange), "auth", or "channel".
#[derive(Serialize, Clone)]
struct ConnectProgress {
    connect_id: String,
    phase: &'static str,
}

/// Best-effort phase tick for the connect form; a no-op when the caller didn't
/// mint a `connect_id` (older callers simply get no ticks).
fn emit_phase(app: &AppHandle, req: &SshConnectReq, phase: &'static str) {
    if let Some(cid) = req.connect_id.as_ref() {
        let _ = app.emit("ssh-connect-progress", ConnectProgress { connect_id: cid.clone(), phase });
    }
}

/// Removes a session-map entry when dropped, so the entry can't leak if the actor
/// task panics (the insert happens before the task is spawned; an in-task
/// `.remove()` is skipped when the task unwinds). Removal needs the async lock,
/// so Drop schedules it on the runtime. Ids are monotonic, so a deferred removal
/// never races a new session onto the same key.
struct SessionGuard {
    sessions: Arc<Mutex<HashMap<String, Session>>>,
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

/// Open a direct SSH session + interactive shell PTY. Returns a session id the
/// frontend uses for subsequent write/resize/close and to filter `ssh-data`.
#[tauri::command]
pub async fn ssh_connect(
    app: AppHandle,
    state: State<'_, SshState>,
    req: SshConnectReq,
) -> Result<String, String> {
    let config = Arc::new(client::Config::default());
    let handler = Client { host: req.host.clone(), port: req.port };
    emit_phase(&app, &req, "tcp");
    let mut session: Handle<Client> = client::connect(config, (req.host.as_str(), req.port), handler)
        .await
        .map_err(|e| format!("connect: {e}"))?;
    emit_phase(&app, &req, "auth");

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
    emit_phase(&app, &req, "channel");

    // Share the session Handle so exec/SFTP channels can be opened later; the
    // actor keeps one clone alive for the interactive shell's lifetime.
    let handle = Arc::new(session);

    let mut channel = handle
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

    Ok(spawn_shell_session(&app, &state, channel, handle).await)
}

/// Open a second interactive shell on an existing session's connection — the
/// split-duplicate path (#319). One TCP connect + auth handshake serves many
/// shell channels, so duplicating never re-prompts for credentials.
#[tauri::command]
pub async fn ssh_duplicate(
    app: AppHandle,
    state: State<'_, SshState>,
    id: String,
    cols: u32,
    rows: u32,
) -> Result<String, String> {
    let handle = {
        let map = state.sessions.lock().await;
        map.get(&id).map(|s| s.handle.clone())
    }
    .ok_or_else(|| "no such session".to_string())?;

    let mut channel = handle
        .channel_open_session()
        .await
        .map_err(|e| format!("open channel: {e}"))?;
    channel
        .request_pty(false, "xterm-256color", cols, rows, 0, 0, &[])
        .await
        .map_err(|e| format!("request pty: {e}"))?;
    channel
        .request_shell(true)
        .await
        .map_err(|e| format!("request shell: {e}"))?;

    Ok(spawn_shell_session(&app, &state, channel, handle).await)
}

/// Register a freshly-opened interactive shell channel as a new session and
/// spawn the actor bridging it to the webview (`ssh-data` / `ssh-exit`).
/// Shared by `ssh_connect` (a brand-new connection) and `ssh_duplicate` (a
/// second shell channel on an existing connection, #319).
async fn spawn_shell_session(
    app: &AppHandle,
    state: &State<'_, SshState>,
    mut channel: russh::Channel<client::Msg>,
    handle: Arc<Handle<Client>>,
) -> String {
    let id = format!("s{}", NEXT_ID.fetch_add(1, Ordering::Relaxed));
    let (tx, mut rx) = mpsc::channel::<SshCmd>(64);
    state
        .sessions
        .lock()
        .await
        .insert(id.clone(), Session { tx, handle: handle.clone() });

    let sessions = state.sessions.clone();
    let task_id = id.clone();
    let app = app.clone();
    tauri::async_runtime::spawn(async move {
        // A Handle clone is moved in and kept alive for the channel's life.
        let _keepalive = handle;
        // Guarantees the session-map entry is removed on ANY exit, including an
        // unwinding panic in the loop below (emit / russh channel ops).
        let _guard = SessionGuard { sessions, id: task_id.clone() };
        // The server sends an ExitStatus message just before Eof/Close; capture it
        // so the UI can tell a clean logout (0) from a failure.
        let mut exit_code: Option<u32> = None;
        loop {
            tokio::select! {
                msg = channel.wait() => match msg {
                    Some(ChannelMsg::Data { ref data }) => {
                        let _ = app.emit("ssh-data", DataPayload { id: task_id.clone(), bytes: data.to_vec() });
                    }
                    Some(ChannelMsg::ExtendedData { ref data, .. }) => {
                        let _ = app.emit("ssh-data", DataPayload { id: task_id.clone(), bytes: data.to_vec() });
                    }
                    Some(ChannelMsg::ExitStatus { exit_status }) => { exit_code = Some(exit_status); }
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
        // `_guard` removes the map entry when this task returns (or unwinds).
        let _ = app.emit("ssh-exit", ExitPayload { id: task_id, code: exit_code });
    });

    id
}

async fn send(state: &State<'_, SshState>, id: &str, cmd: SshCmd) -> Result<(), String> {
    let tx = {
        let map = state.sessions.lock().await;
        map.get(id).map(|s| s.tx.clone())
    };
    match tx {
        Some(tx) => tx.send(cmd).await.map_err(|_| "session closed".to_string()),
        None => Err("no such session".into()),
    }
}

/// Run a one-shot command over a fresh `exec` channel on an existing session and
/// return its stdout as a string. This is the substrate for tmux control (list/
/// new/kill/split/send-keys/capture, all shelled `tmux …` invocations) — the
/// interactive PTY can't cleanly capture per-command stdout, so tmux management
/// runs here while the PTY renders the attached pane. stderr is folded in so
/// tmux error messages surface to the caller.
#[tauri::command]
pub async fn ssh_exec(state: State<'_, SshState>, id: String, command: String) -> Result<String, String> {
    let handle = {
        let map = state.sessions.lock().await;
        map.get(&id).map(|s| s.handle.clone())
    }
    .ok_or_else(|| "no such session".to_string())?;

    let mut channel = handle
        .channel_open_session()
        .await
        .map_err(|e| format!("open channel: {e}"))?;
    channel
        .exec(true, command.as_bytes())
        .await
        .map_err(|e| format!("exec: {e}"))?;

    let mut out: Vec<u8> = Vec::new();
    loop {
        match channel.wait().await {
            Some(ChannelMsg::Data { ref data }) => out.extend_from_slice(data),
            Some(ChannelMsg::ExtendedData { ref data, .. }) => out.extend_from_slice(data),
            Some(ChannelMsg::Eof) | Some(ChannelMsg::Close) | None => break,
            _ => {}
        }
    }
    Ok(String::from_utf8_lossy(&out).into_owned())
}

/// One remote directory entry (parity — mobile SftpService.listDir).
#[derive(Serialize)]
pub struct SftpEntry {
    name: String,
    is_dir: bool,
    size: u64,
}

/// Open an SFTP subsystem on a fresh channel of an existing session.
async fn sftp_open(
    state: &State<'_, SshState>,
    id: &str,
) -> Result<russh_sftp::client::SftpSession, String> {
    let handle = {
        let map = state.sessions.lock().await;
        map.get(id).map(|s| s.handle.clone())
    }
    .ok_or_else(|| "no such session".to_string())?;
    let channel = handle
        .channel_open_session()
        .await
        .map_err(|e| format!("open channel: {e}"))?;
    channel
        .request_subsystem(true, "sftp")
        .await
        .map_err(|e| format!("sftp subsystem: {e}"))?;
    russh_sftp::client::SftpSession::new(channel.into_stream())
        .await
        .map_err(|e| format!("sftp init: {e}"))
}

/// List a remote directory (dirs + files with sizes). Parity: SftpService.listDir.
#[tauri::command]
pub async fn sftp_list(state: State<'_, SshState>, id: String, path: String) -> Result<Vec<SftpEntry>, String> {
    let sftp = sftp_open(&state, &id).await?;
    let dir = sftp.read_dir(&path).await.map_err(|e| format!("read_dir: {e}"))?;
    let mut out = Vec::new();
    for entry in dir {
        out.push(SftpEntry {
            name: entry.file_name(),
            is_dir: entry.file_type().is_dir(),
            size: entry.metadata().size.unwrap_or(0),
        });
    }
    out.sort_by(|a, b| b.is_dir.cmp(&a.is_dir).then_with(|| a.name.cmp(&b.name)));
    Ok(out)
}

/// Progress tick for an in-flight SFTP transfer, emitted as `sftp-progress` so
/// the file panel can show a live bar. `done` = bytes moved so far; the frontend
/// already knows the total (the listed size / the picked file's size) and
/// computes the percentage, so we don't stat here. `transfer_id` is a
/// frontend-minted id that lets it match ticks to the row it started.
#[derive(Serialize, Clone)]
struct SftpProgress {
    transfer_id: String,
    done: u64,
}

/// The chunk size for streamed SFTP transfers — big enough to keep the pipe busy,
/// small enough that a progress tick per chunk feels live on a slow link.
const SFTP_CHUNK: usize = 256 * 1024;

/// Download a remote file, returning its bytes base64-encoded (parity: download).
/// Reads in chunks and emits `sftp-progress` so the transfer shows a live bar.
#[tauri::command]
pub async fn sftp_read(
    app: AppHandle,
    state: State<'_, SshState>,
    id: String,
    path: String,
    transfer_id: String,
) -> Result<String, String> {
    use base64::Engine as _;
    use tokio::io::AsyncReadExt as _;
    let sftp = sftp_open(&state, &id).await?;
    let mut file = sftp.open(&path).await.map_err(|e| format!("open: {e}"))?;
    let mut buf = Vec::new();
    let mut chunk = vec![0u8; SFTP_CHUNK];
    let mut last_emit = 0u64;
    loop {
        let n = file.read(&mut chunk).await.map_err(|e| format!("read: {e}"))?;
        if n == 0 {
            break;
        }
        buf.extend_from_slice(&chunk[..n]);
        let done = buf.len() as u64;
        if done - last_emit >= SFTP_CHUNK as u64 {
            last_emit = done;
            let _ = app.emit("sftp-progress", SftpProgress { transfer_id: transfer_id.clone(), done });
        }
    }
    // Final tick so the bar lands exactly on the total even for a short file.
    let _ = app.emit("sftp-progress", SftpProgress { transfer_id, done: buf.len() as u64 });
    Ok(base64::engine::general_purpose::STANDARD.encode(&buf))
}

/// Upload bytes (base64) to a remote path, creating/overwriting it (parity:
/// upload). Writes in chunks and emits `sftp-progress` for a live bar.
#[tauri::command]
pub async fn sftp_write(
    app: AppHandle,
    state: State<'_, SshState>,
    id: String,
    path: String,
    data_b64: String,
    transfer_id: String,
) -> Result<(), String> {
    use base64::Engine as _;
    use tokio::io::AsyncWriteExt as _;
    let data = base64::engine::general_purpose::STANDARD
        .decode(data_b64.as_bytes())
        .map_err(|e| format!("decode: {e}"))?;
    let total = data.len() as u64;
    let sftp = sftp_open(&state, &id).await?;
    let mut file = sftp.create(&path).await.map_err(|e| format!("create: {e}"))?;
    let mut done = 0u64;
    let mut last_emit = 0u64;
    for chunk in data.chunks(SFTP_CHUNK) {
        file.write_all(chunk).await.map_err(|e| format!("write: {e}"))?;
        done += chunk.len() as u64;
        if done - last_emit >= SFTP_CHUNK as u64 || done == total {
            last_emit = done;
            let _ = app.emit("sftp-progress", SftpProgress { transfer_id: transfer_id.clone(), done });
        }
    }
    file.flush().await.map_err(|e| format!("flush: {e}"))?;
    file.shutdown().await.map_err(|e| format!("close: {e}"))?;
    Ok(())
}

/// Metadata extracted from an imported private key (parity Phase 2a key store).
#[derive(Serialize)]
pub struct ParsedKey {
    algorithm: String,
    public_openssh: String,
}

/// Validate + introspect a pasted private key: confirm it parses (with the
/// passphrase if encrypted) and return its algorithm + OpenSSH public key so the
/// key store can show it and offer the public half to copy onto servers. Uses
/// the same `decode_secret_key` path the connect flow uses.
#[tauri::command]
pub fn ssh_parse_key(pem: String, passphrase: Option<String>) -> Result<ParsedKey, String> {
    let pass = passphrase.as_deref().filter(|s| !s.is_empty());
    let key = decode_secret_key(&pem, pass).map_err(|e| format!("key parse: {e}"))?;
    Ok(ParsedKey {
        algorithm: key.algorithm().to_string(),
        public_openssh: key.public_key().to_openssh().map_err(|e| e.to_string())?,
    })
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
