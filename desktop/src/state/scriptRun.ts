import { isTauri } from '../platform';

/// Thin wrapper over the Rust `script_run` command (script.rs) — runs a vault
/// `script` item's body once and returns its captured output. One-shot and
/// wall-clock capped; interactive scripts belong in a real terminal.

export interface ScriptResult {
  code: number | null;
  stdout: string;
  stderr: string;
  timedOut: boolean;
}

export async function runScript(
  interpreter: string,
  content: string,
  cwd?: string,
): Promise<ScriptResult> {
  if (!isTauri()) throw new Error('running scripts requires the desktop app');
  const { invoke } = await import('@tauri-apps/api/core');
  return invoke<ScriptResult>('script_run', { interpreter, content, cwd: cwd ?? null });
}
