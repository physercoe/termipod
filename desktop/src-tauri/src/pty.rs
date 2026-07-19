//! Local PTY for the desktop terminal dock (professional-terminal discussion,
//! → ADR-053). Where `ssh.rs` bridges a *remote* shell over russh, this opens a
//! *local* shell on the user's own machine via `portable-pty` (wezterm's
//! cross-platform PTY layer) — the desktop's "empower the user" lever beyond the
//! mobile SSH-only story. Same webview contract as ssh.rs, distinct event names
//! so the frontend can multiplex both kinds through one `<Screen>`:
//!
//!   frontend --invoke pty_open--------------------------------> core (creates it)
//!   frontend --invoke pty_start (after listeners are attached)-> core (reads it)
//!   core     --emit "pty-data" {id, bytes} / "pty-exit" {id}--> frontend
//!   frontend --invoke pty_write/pty_resize/pty_close----------> core
//!
//! Two properties this file gets right that a first cut got wrong on Windows:
//!
//!  1. **Commands are `async fn`.** portable-pty is blocking I/O — `write_all` to
//!     a ConPTY whose child hasn't drained stdin can block indefinitely. A *sync*
//!     `#[tauri::command]` runs on the **main thread**, so a blocked write froze
//!     the whole UI (the v0.3.11/12 Windows freeze). Declaring them `async fn`
//!     (as ssh.rs already does) dispatches them off the main thread via
//!     `async_runtime::spawn`, so a stuck session can never wedge the app. The
//!     bodies have no `.await`, so no `std::sync` guard is ever held across one.
//!
//!  2. **The reader is gated behind `pty_start`.** A local shell prints its prompt
//!     within microseconds of spawning; if the reader thread emits that first
//!     `pty-data` before the webview's async `listen('pty-data')` has registered,
//!     the prompt is dropped and the terminal shows a black screen with a live
//!     cursor (the v0.3.11/12 Windows "black local shell"). SSH masks the same
//!     latent race with network latency. So `pty_open` creates the shell but does
//!     NOT read it; the frontend attaches its listeners, then calls `pty_start`,
//!     which spawns the reader. The OS pipe buffers the banner in between, so
//!     nothing is lost.
//!
//! The reader loop is a dedicated std thread (a blocking pipe read can't be an
//! async task) that also reaps the child on exit; writes/resizes go straight
//! through the master under a std `Mutex`.

use std::collections::HashMap;
use std::io::{Read, Write};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex, OnceLock};

use portable_pty::{native_pty_system, Child, ChildKiller, CommandBuilder, MasterPty, PtySize};
use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Emitter, State};

static NEXT_ID: AtomicU64 = AtomicU64::new(1);

/// The reader end + child of a shell that has been opened but not yet started.
/// Held in the session until `pty_start` moves it into the reader thread — the
/// gate that closes the emit-before-subscribe race (see the module docs).
struct PendingIo {
    reader: Box<dyn Read + Send>,
    child: Box<dyn Child + Send + Sync>,
}

/// A live local shell: the master (kept for resize), a writer for keystrokes, a
/// killer so `pty_close` can terminate the child from outside the reader thread
/// (which is blocked in `read`), and the not-yet-started reader/child until
/// `pty_start` consumes them. All are `Send + Sync`, so the session lives in the
/// shared state map.
struct PtySession {
    master: Arc<Mutex<Box<dyn MasterPty + Send>>>,
    writer: Arc<Mutex<Box<dyn Write + Send>>>,
    killer: Box<dyn ChildKiller + Send + Sync>,
    pending: Mutex<Option<PendingIo>>,
}

/// Managed Tauri state: live local shells keyed by the id `pty_open` returns.
#[derive(Default)]
pub struct PtyState {
    sessions: Arc<Mutex<HashMap<String, PtySession>>>,
}

/// Lock a std `Mutex`, recovering the guard even if a previous holder panicked
/// (poisoning). `std::sync::Mutex` poisons on a panic-while-held, and `.unwrap()`
/// on the resulting `PoisonError` would panic again — so a single panic in the
/// reader thread would crash the app on the *next* terminal op. The protected
/// data (session map, writer, master) stays structurally valid across a panic, so
/// `into_inner()` hands back a usable guard instead.
fn lock_recover<T>(m: &Mutex<T>) -> std::sync::MutexGuard<'_, T> {
    m.lock().unwrap_or_else(std::sync::PoisonError::into_inner)
}

#[derive(Deserialize)]
pub struct PtyOpenReq {
    /// Program to launch; defaults to `$SHELL` (unix) / `%COMSPEC%` (windows).
    /// For a local *agent* this is the engine CLI (e.g. `claude`, `codex`).
    #[serde(default)]
    shell: Option<String>,
    /// Extra argv passed to the program (e.g. `--model …` for an agent). Each is a
    /// distinct arg — never a shell string — so there is no injection surface.
    #[serde(default)]
    args: Vec<String>,
    /// Working directory; defaults to the process's cwd when absent.
    #[serde(default)]
    cwd: Option<String>,
    cols: u16,
    rows: u16,
}

/// Build the PTY command. On Windows an npm-installed agent CLI (`claude`,
/// `codex`, `gemini`) is usually a `.cmd`/`.ps1` shim that CreateProcess — and
/// therefore ConPTY — cannot exec directly; it must run through `cmd.exe /C`.
/// Native `.exe` programs (including the default `cmd.exe`/`powershell.exe`
/// shells) are launched directly. On unix everything is launched directly.
fn build_command(program: &str, args: &[String]) -> CommandBuilder {
    if cfg!(windows) && !program.to_ascii_lowercase().ends_with(".exe") {
        let mut cmd = CommandBuilder::new("cmd.exe");
        cmd.arg("/C");
        cmd.arg(program);
        for a in args {
            cmd.arg(a.as_str());
        }
        cmd
    } else {
        let mut cmd = CommandBuilder::new(program);
        for a in args {
            cmd.arg(a.as_str());
        }
        cmd
    }
}

/// What `pty_open` hands back: the session id plus the shell it actually
/// launched, so the frontend can decide whether the POSIX OSC-133 shell-
/// integration script applies (it does not for cmd.exe / PowerShell).
#[derive(Serialize)]
pub struct PtyOpened {
    id: String,
    shell: String,
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

/// The user's real login-shell `PATH`, resolved once and cached.
///
/// A GUI app launched from Finder/Dock on macOS (and some Linux desktop
/// environments) inherits only a minimal `PATH` — *not* the one the user's
/// `.zshrc` / `.zprofile` / `.bash_profile` builds — so an agent CLI installed via
/// npm / Homebrew / nvm (`kimi`, `claude`, …) isn't found in TermiPod's terminal
/// even though it runs fine in Terminal.app. We resolve the login shell's PATH by
/// spawning `$SHELL -ilc` (interactive + login, so it sources the same startup
/// files a real terminal does) and print `$PATH` between markers to strip any
/// startup-script stdout noise. Cached — stable for the app's lifetime; `None` on
/// any failure, which leaves the inherited PATH untouched.
#[cfg(unix)]
fn login_path() -> Option<&'static str> {
    static CACHE: OnceLock<Option<String>> = OnceLock::new();
    CACHE
        .get_or_init(|| {
            use std::process::Command;
            const MARK: &str = "__TP_PATH__";
            let shell = std::env::var("SHELL").unwrap_or_else(|_| "/bin/bash".into());
            let script = format!("printf '{MARK}%s{MARK}' \"$PATH\"");
            // `output()` gives the child a closed stdin, so an interactive shell
            // reading input just sees EOF and exits (no hang).
            let out = Command::new(&shell).args(["-ilc", &script]).output().ok()?;
            let s = String::from_utf8_lossy(&out.stdout);
            let start = s.find(MARK)? + MARK.len();
            let rest = &s[start..];
            let end = rest.find(MARK)?;
            let path = rest[..end].trim();
            if path.is_empty() {
                None
            } else {
                Some(path.to_string())
            }
        })
        .as_deref()
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

/// Open a local shell in a PTY but do NOT begin reading it yet — the frontend
/// attaches its `pty-data`/`pty-exit` listeners and then calls `pty_start`, so
/// the shell's first prompt can't race ahead of the subscriber. Returns a session
/// id the frontend uses for start/write/resize/close and to filter `pty-data`.
/// `async fn` (like ssh.rs) so the blocking spawn is dispatched off the UI main
/// thread — a synchronous command runs on the main thread and would freeze it.
#[tauri::command]
pub async fn pty_open(state: State<'_, PtyState>, req: PtyOpenReq) -> Result<PtyOpened, String> {
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
    let mut cmd = build_command(shell.as_str(), &req.args);
    if let Some(cwd) = req.cwd.as_ref().filter(|s| !s.trim().is_empty()) {
        cmd.cwd(cwd.as_str());
    }
    // TERM so full-screen apps (vim, tmux, an agent TUI) and colour work out of the box.
    cmd.env("TERM", "xterm-256color");
    // Inject the login-shell PATH so npm/Homebrew/nvm-installed agent CLIs resolve
    // even when launched from a Finder/Dock GUI with a minimal inherited PATH.
    #[cfg(unix)]
    if let Some(path) = login_path() {
        cmd.env("PATH", path);
    }

    let child = pair.slave.spawn_command(cmd).map_err(|e| format!("spawn: {e}"))?;
    // Drop the slave once the child holds it, so the master's reader observes EOF
    // when the child exits (otherwise our own slave handle keeps it open forever).
    drop(pair.slave);

    let killer = child.clone_killer();
    let reader = pair.master.try_clone_reader().map_err(|e| format!("reader: {e}"))?;
    let writer = pair.master.take_writer().map_err(|e| format!("writer: {e}"))?;

    let id = format!("p{}", NEXT_ID.fetch_add(1, Ordering::Relaxed));
    lock_recover(&state.sessions).insert(
        id.clone(),
        PtySession {
            master: Arc::new(Mutex::new(pair.master)),
            writer: Arc::new(Mutex::new(writer)),
            killer,
            pending: Mutex::new(Some(PendingIo { reader, child })),
        },
    );

    Ok(PtyOpened { id, shell })
}

/// Begin streaming the shell opened by `pty_open`. Idempotent: the reader/child
/// are taken once, so a second call (or a call after the tab already closed) is a
/// harmless no-op. Spawns the reader thread: blocking reads → `pty-data`; EOF/err
/// → reap child, drop the session, emit `pty-exit`.
#[tauri::command]
pub async fn pty_start(app: AppHandle, state: State<'_, PtyState>, id: String) -> Result<(), String> {
    let pending = {
        let map = lock_recover(&state.sessions);
        match map.get(&id) {
            Some(s) => lock_recover(&s.pending).take(),
            None => return Ok(()), // closed before it started
        }
    };
    let Some(PendingIo { reader, child }) = pending else {
        return Ok(()); // already started
    };

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
        lock_recover(&sessions).remove(&task_id);
        let _ = app.emit("pty-exit", ExitPayload { id: task_id });
    });

    Ok(())
}

/// `async fn` so a blocking `write_all` (ConPTY whose child hasn't drained
/// stdin) stalls only this session's worker, never the UI main thread.
#[tauri::command]
pub async fn pty_write(state: State<'_, PtyState>, id: String, data: String) -> Result<(), String> {
    let writer = lock_recover(&state.sessions)
        .get(&id)
        .map(|s| s.writer.clone())
        .ok_or_else(|| "no such session".to_string())?;
    let mut w = lock_recover(&writer);
    w.write_all(data.as_bytes()).map_err(|e| format!("write: {e}"))?;
    w.flush().map_err(|e| format!("flush: {e}"))
}

#[tauri::command]
pub async fn pty_resize(state: State<'_, PtyState>, id: String, cols: u16, rows: u16) -> Result<(), String> {
    let master = lock_recover(&state.sessions)
        .get(&id)
        .map(|s| s.master.clone())
        .ok_or_else(|| "no such session".to_string())?;
    // Bind the guard to a local so it drops before `master` (reverse declaration
    // order) — chaining it as the block's tail expression outlives `master`'s drop.
    let guard = lock_recover(&master);
    guard
        .resize(PtySize { rows: rows.max(1), cols: cols.max(1), pixel_width: 0, pixel_height: 0 })
        .map_err(|e| format!("resize: {e}"))
}

#[tauri::command]
pub async fn pty_close(state: State<'_, PtyState>, id: String) -> Result<(), String> {
    // Remove + kill; the reader thread then hits EOF, removes its (now absent)
    // entry harmlessly, and emits `pty-exit`. Best-effort — a child that already
    // exited (or never started) is simply gone.
    if let Some(mut session) = lock_recover(&state.sessions).remove(&id) {
        let _ = session.killer.kill();
    }
    Ok(())
}
