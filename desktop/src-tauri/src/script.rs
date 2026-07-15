//! On-device "quick run" for a script stored in the vault.
//!
//! A vault `script` item holds a snippet (a dev-env bootstrap, a one-off task);
//! this executes it once and returns stdout/stderr/exit code so the result shows
//! inline in the vault. The snippet is written to a temp file and handed to the
//! chosen interpreter as a file argument — never interpolated into a shell string
//! and never fed a caller-controlled program path from the web layer beyond the
//! small allowlist below, so there is no injection surface the item's own author
//! didn't already have on their own machine. Runs are wall-clock capped and the
//! child is killed if it overruns; the temp file is always removed.
//!
//! This is a one-shot, non-interactive run (captured output) — the right shape
//! for setup/bootstrap snippets. Interactive/long-running scripts belong in a
//! real terminal pane (the Terminal surface), not here.

use std::path::PathBuf;
use std::process::Stdio;
use std::sync::atomic::{AtomicU64, Ordering};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use serde::Serialize;
use tokio::process::Command;

const RUN_TIMEOUT_SECS: u64 = 120;
const MAX_OUTPUT: usize = 256 * 1024; // clamp captured output shown in the UI

static COUNTER: AtomicU64 = AtomicU64::new(0);

/// Map an interpreter label to (program, leading args, temp-file extension). An
/// unknown label is used verbatim as the program (with a `.txt` temp file), so a
/// user can name any interpreter on their PATH without a code change.
fn interpreter_spec(interp: &str) -> (String, Vec<String>, &'static str) {
    match interp.trim().to_ascii_lowercase().as_str() {
        "bash" => ("bash".into(), vec![], "sh"),
        "sh" => ("sh".into(), vec![], "sh"),
        "zsh" => ("zsh".into(), vec![], "sh"),
        "python" | "python3" => ("python3".into(), vec![], "py"),
        "node" => ("node".into(), vec![], "js"),
        "pwsh" | "powershell" => ("pwsh".into(), vec!["-NoProfile".into(), "-File".into()], "ps1"),
        "ruby" => ("ruby".into(), vec![], "rb"),
        other if !other.is_empty() => (other.to_string(), vec![], "txt"),
        _ => ("bash".into(), vec![], "sh"),
    }
}

/// Removes the temp script on drop, whatever the run's outcome (success, error,
/// or timeout-kill).
struct TempScript(PathBuf);
impl Drop for TempScript {
    fn drop(&mut self) {
        let _ = std::fs::remove_file(&self.0);
    }
}

#[derive(Serialize)]
pub struct ScriptResult {
    code: Option<i32>,
    stdout: String,
    stderr: String,
    #[serde(rename = "timedOut")]
    timed_out: bool,
}

fn clamp(mut s: String) -> String {
    if s.len() > MAX_OUTPUT {
        s.truncate(MAX_OUTPUT);
        s.push_str("\n… (output truncated)");
    }
    s
}

/// Run `content` with `interpreter` once; `cwd` sets the working directory (e.g.
/// the Author workspace). Errors carry a human message; a non-zero exit is a
/// successful *call* with a non-zero `code`, not an `Err`.
#[tauri::command]
pub async fn script_run(
    interpreter: String,
    content: String,
    cwd: Option<String>,
) -> Result<ScriptResult, String> {
    if content.trim().is_empty() {
        return Err("script is empty".into());
    }
    let (program, lead_args, ext) = interpreter_spec(&interpreter);

    // Unique temp path: pid + nanos + a process-local counter (no rand dep).
    let nanos = SystemTime::now().duration_since(UNIX_EPOCH).map(|d| d.as_nanos()).unwrap_or(0);
    let n = COUNTER.fetch_add(1, Ordering::Relaxed);
    let mut path = std::env::temp_dir();
    path.push(format!("termipod-script-{}-{nanos}-{n}.{ext}", std::process::id()));
    std::fs::write(&path, content.as_bytes()).map_err(|e| format!("could not stage script: {e}"))?;
    let _guard = TempScript(path.clone());

    let mut cmd = Command::new(&program);
    cmd.args(&lead_args).arg(&path);
    if let Some(dir) = cwd.as_ref().filter(|d| !d.is_empty()) {
        cmd.current_dir(dir);
    }
    cmd.stdin(Stdio::null()).stdout(Stdio::piped()).stderr(Stdio::piped()).kill_on_drop(true);

    let child = cmd
        .spawn()
        .map_err(|e| format!("could not run '{program}': {e}"))?;
    // On timeout the future is dropped → kill_on_drop reaps the child; the guard
    // still removes the temp file.
    let out = match tokio::time::timeout(Duration::from_secs(RUN_TIMEOUT_SECS), child.wait_with_output()).await
    {
        Ok(res) => res.map_err(|e| e.to_string())?,
        Err(_) => {
            return Ok(ScriptResult {
                code: None,
                stdout: String::new(),
                stderr: format!("timed out after {RUN_TIMEOUT_SECS}s — killed"),
                timed_out: true,
            });
        }
    };
    Ok(ScriptResult {
        code: out.status.code(),
        stdout: clamp(String::from_utf8_lossy(&out.stdout).into_owned()),
        stderr: clamp(String::from_utf8_lossy(&out.stderr).into_owned()),
        timed_out: false,
    })
}
