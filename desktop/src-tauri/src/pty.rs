//! Local PTY for the desktop terminal dock (professional-terminal discussion,
//! → ADR-053). Where `ssh.rs` bridges a *remote* shell over russh, this opens a
//! *local* shell on the user's own machine via `portable-pty` (wezterm's
//! cross-platform PTY layer) — the desktop's "empower the user" lever beyond the
//! mobile SSH-only story. Same webview contract as ssh.rs, distinct event names
//! so the frontend can multiplex both kinds through one `<Screen>`:
//!
//!   frontend --invoke pty_open/pty_write/pty_resize/pty_close--> core
//!   core     --emit "pty-data" {id, bytes} / "pty-exit" {id}--> frontend
//!
//! portable-pty is blocking I/O, so each session runs a dedicated reader thread
//! (a std thread, not a tokio task) that also reaps the child on exit; writes and
//! resizes go straight through the master under a std `Mutex`. The commands are
//! therefore sync, which sidesteps the async `State` lifetime dance in ssh.rs.

use std::collections::HashMap;
use std::io::{Read, Write};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};

use portable_pty::{native_pty_system, ChildKiller, CommandBuilder, MasterPty, PtySize};
use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Emitter, State};

static NEXT_ID: AtomicU64 = AtomicU64::new(1);

/// A live local shell: the master (kept for resize), a writer for keystrokes,
/// and a killer so `pty_close` can terminate the child from outside the reader
/// thread (which is blocked in `read`). All are `Send + Sync`, so the session
/// lives in the shared state map.
struct PtySession {
    master: Arc<Mutex<Box<dyn MasterPty + Send>>>,
    writer: Arc<Mutex<Box<dyn Write + Send>>>,
    killer: Box<dyn ChildKiller + Send + Sync>,
}

/// Managed Tauri state: live local shells keyed by the id `pty_open` returns.
#[derive(Default)]
pub struct PtyState {
    sessions: Arc<Mutex<HashMap<String, PtySession>>>,
}

#[derive(Deserialize)]
pub struct PtyOpenReq {
    /// Shell to launch; defaults to `$SHELL` (unix) / `%COMSPEC%` (windows).
    #[serde(default)]
    shell: Option<String>,
    /// Working directory; defaults to the process's cwd when absent.
    #[serde(default)]
    cwd: Option<String>,
    cols: u16,
    rows: u16,
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

/// The user's login shell, or a sensible per-OS default when `$SHELL` /
/// `%COMSPEC%` is unset (e.g. a bare service environment).
fn default_shell() -> String {
    if cfg!(windows) {
        std::env::var("COMSPEC").unwrap_or_else(|_| "powershell.exe".into())
    } else {
        std::env::var("SHELL").unwrap_or_else(|_| "/bin/bash".into())
    }
}

/// Open a local shell in a PTY and stream it to the webview. Returns a session
/// id the frontend uses for write/resize/close and to filter `pty-data`.
#[tauri::command]
pub fn pty_open(app: AppHandle, state: State<'_, PtyState>, req: PtyOpenReq) -> Result<String, String> {
    let pty_system = native_pty_system();
    let pair = pty_system
        .openpty(PtySize {
            rows: req.rows.max(1),
            cols: req.cols.max(1),
            pixel_width: 0,
            pixel_height: 0,
        })
        .map_err(|e| format!("openpty: {e}"))?;

    let shell = req.shell.filter(|s| !s.trim().is_empty()).unwrap_or_else(default_shell);
    let mut cmd = CommandBuilder::new(shell.as_str());
    if let Some(cwd) = req.cwd.as_ref().filter(|s| !s.trim().is_empty()) {
        cmd.cwd(cwd.as_str());
    }
    // TERM so full-screen apps (vim, tmux) and colour work out of the box.
    cmd.env("TERM", "xterm-256color");

    let child = pair.slave.spawn_command(cmd).map_err(|e| format!("spawn: {e}"))?;
    // Drop the slave once the child holds it, so the master's reader observes EOF
    // when the child exits (otherwise our own slave handle keeps it open forever).
    drop(pair.slave);

    let killer = child.clone_killer();
    let reader = pair.master.try_clone_reader().map_err(|e| format!("reader: {e}"))?;
    let writer = pair.master.take_writer().map_err(|e| format!("writer: {e}"))?;

    let id = format!("p{}", NEXT_ID.fetch_add(1, Ordering::Relaxed));
    state.sessions.lock().unwrap().insert(
        id.clone(),
        PtySession {
            master: Arc::new(Mutex::new(pair.master)),
            writer: Arc::new(Mutex::new(writer)),
            killer,
        },
    );

    // Reader thread: blocking reads → `pty-data`; EOF/err → reap child, drop the
    // session, emit `pty-exit`. Owns `child` so the process is reaped here rather
    // than left a zombie.
    let sessions = state.sessions.clone();
    let task_id = id.clone();
    std::thread::spawn(move || {
        let mut reader = reader;
        let mut child = child; // rebind mut so `child.wait()` (needs &mut) is callable
        let mut buf = [0u8; 8192];
        loop {
            match reader.read(&mut buf) {
                Ok(0) | Err(_) => break,
                Ok(n) => {
                    let _ = app.emit(
                        "pty-data",
                        DataPayload { id: task_id.clone(), bytes: buf[..n].to_vec() },
                    );
                }
            }
        }
        let _ = child.wait();
        sessions.lock().unwrap().remove(&task_id);
        let _ = app.emit("pty-exit", ExitPayload { id: task_id });
    });

    Ok(id)
}

#[tauri::command]
pub fn pty_write(state: State<'_, PtyState>, id: String, data: String) -> Result<(), String> {
    let writer = state
        .sessions
        .lock()
        .unwrap()
        .get(&id)
        .map(|s| s.writer.clone())
        .ok_or_else(|| "no such session".to_string())?;
    let mut w = writer.lock().unwrap();
    w.write_all(data.as_bytes()).map_err(|e| format!("write: {e}"))?;
    w.flush().map_err(|e| format!("flush: {e}"))
}

#[tauri::command]
pub fn pty_resize(state: State<'_, PtyState>, id: String, cols: u16, rows: u16) -> Result<(), String> {
    let master = state
        .sessions
        .lock()
        .unwrap()
        .get(&id)
        .map(|s| s.master.clone())
        .ok_or_else(|| "no such session".to_string())?;
    master
        .lock()
        .unwrap()
        .resize(PtySize { rows: rows.max(1), cols: cols.max(1), pixel_width: 0, pixel_height: 0 })
        .map_err(|e| format!("resize: {e}"))
}

#[tauri::command]
pub fn pty_close(state: State<'_, PtyState>, id: String) -> Result<(), String> {
    // Remove + kill; the reader thread then hits EOF, removes its (now absent)
    // entry harmlessly, and emits `pty-exit`. Best-effort — a child that already
    // exited is simply gone.
    if let Some(mut session) = state.sessions.lock().unwrap().remove(&id) {
        let _ = session.killer.kill();
    }
    Ok(())
}
