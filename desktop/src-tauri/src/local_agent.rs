use tokio::process::Command;

// local_agent.rs — the desktop's **local** assistant path. The AgentCompanion is
// hub-attached by default (it drives an agent running on some host via the hub);
// this lets it instead drive an engine CLI running on THIS machine, which is what
// a director usually wants for quick, on-device help.
//
// MVP shape: a one-shot, non-interactive ("print") run. The frontend configures
// the command (program + fixed args, e.g. `claude -p`); the user's message is
// passed as a separate trailing argument — never interpolated into a shell — so
// there is no injection surface. It runs in `cwd` when given (the Author
// workspace folder), so the local agent sees the files the user is editing.
//
// Streaming output + an interactive ConPTY session is the larger, separate runner
// tracked in author-agent-assist-and-diagrams; this covers the common ask without
// it.

/// Run a local agent CLI once and return its stdout. Errors carry stderr so a
/// missing binary / auth problem is visible in the companion.
#[tauri::command]
pub async fn local_agent_run(
    program: String,
    args: Vec<String>,
    prompt: String,
    cwd: Option<String>,
) -> Result<String, String> {
    if program.trim().is_empty() {
        return Err("no local agent command configured".into());
    }
    let mut cmd = Command::new(&program);
    cmd.args(&args).arg(&prompt);
    if let Some(dir) = cwd.as_ref().filter(|d| !d.is_empty()) {
        cmd.current_dir(dir);
    }
    let out = cmd
        .output()
        .await
        .map_err(|e| format!("could not run '{program}': {e}"))?;
    if !out.status.success() {
        let err = String::from_utf8_lossy(&out.stderr);
        return Err(if err.trim().is_empty() {
            format!("'{program}' exited with {}", out.status)
        } else {
            err.trim().to_string()
        });
    }
    Ok(String::from_utf8_lossy(&out.stdout).trim().to_string())
}
