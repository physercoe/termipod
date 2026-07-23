/// IPC + persistence layer of the code→model-graph tracer (plan §5, W4 Tier 1).
/// The pure core (helper, DOT extraction, remote-command assembly) is in
/// [[traceCore]]; this module drives it over the two venues.
///
/// **Local**: the `trace_run` IPC pipes the helper to a local interpreter's stdin.
/// **Remote**: `ssh_exec` runs the assembled one-liner (helper base64-embedded) on
/// a saved SSH host — so a GPU box's venv/conda/docker interpreter works. The
/// interpreter is a free-text **preset** (`/opt/venv/bin/python`,
/// `conda run -n rl python`, `docker exec -i box python`, `uv run python`), which
/// is why stdin-piping is used rather than a script file. The **import-locality
/// rule**: the model file's repo must be importable on the chosen venue (cwd = the
/// repo root); we do not copy single files across hosts.
import { invoke } from '../bridge';
import { sshExec } from '../ssh/native';
import { connectSaved } from './inspectSources';
import type { Connection } from './connections';
import { TORCHVIEW_HELPER, PROBE_HELPER, extractDot, remoteTraceCommand, remoteProbeCommand, type TraceParams } from './traceCore';

export type { TraceParams } from './traceCore';

interface TraceResult {
  code: number | null;
  stdout: string;
  stderr: string;
  timedOut: boolean;
}

export interface TraceRequest extends TraceParams {
  /// Set → the trace runs on this saved SSH host; undefined → local interpreter.
  connection?: Connection;
}

/// Run the trace and return the DOT string. Throws with the interpreter's stderr
/// (or the raw output) when no graph came back.
export async function runTrace(p: TraceRequest): Promise<string> {
  if (p.connection === undefined) {
    const env = { TRACE_ENTRY: p.entry, TRACE_INPUT: p.shape, TRACE_DEPTH: String(p.depth), TRACE_FILE: p.filePath };
    const r = await invoke<TraceResult>('trace_run', { command: p.command, content: TORCHVIEW_HELPER, env, cwd: p.repoRoot || null });
    if (r.timedOut) throw new Error(r.stderr || 'the trace timed out');
    const dot = extractDot(r.stdout);
    if (dot !== null) return dot;
    throw new Error((r.stderr || r.stdout).trim() || 'the trace produced no graph');
  }
  const sid = await connectSaved(p.connection);
  const out = await sshExec(sid, remoteTraceCommand(p));
  const dot = extractDot(out);
  if (dot !== null) return dot;
  throw new Error(out.trim() || 'the trace produced no graph');
}

/// Probe an interpreter for torch + torchview. Returns the version line on success;
/// throws with the failure otherwise.
export async function detectInterpreter(command: string, connection?: Connection): Promise<string> {
  if (connection === undefined) {
    const r = await invoke<TraceResult>('trace_run', { command, content: PROBE_HELPER, timeoutMs: 20_000 });
    if (r.code === 0) return r.stdout.trim();
    throw new Error(r.stderr.trim() || 'torch / torchview not importable on this interpreter');
  }
  const sid = await connectSaved(connection);
  const out = await sshExec(sid, remoteProbeCommand(command));
  if (/OK torch/.test(out)) return out.trim();
  throw new Error(out.trim() || 'torch / torchview not importable on this interpreter');
}

// ── persistence ────────────────────────────────────────────────────────────────
const IKEY = 'termipod.inspect.trace.interp.';
const FKEY = 'termipod.inspect.trace.last';

/// The interpreter preset is remembered per venue (`local` or a host id), since a
/// GPU box's interpreter differs from the laptop's.
export function getInterp(venueKey: string): string {
  return localStorage.getItem(IKEY + venueKey) ?? 'python3';
}
export function setInterp(venueKey: string, cmd: string): void {
  localStorage.setItem(IKEY + venueKey, cmd);
}

export interface TraceFormMemory {
  entry: string;
  shape: string;
  depth: number;
}
export function getLastForm(): TraceFormMemory {
  try {
    const j = localStorage.getItem(FKEY);
    if (j !== null) return JSON.parse(j) as TraceFormMemory;
  } catch {
    /* ignore */
  }
  return { entry: '', shape: '', depth: 3 };
}
export function setLastForm(f: TraceFormMemory): void {
  localStorage.setItem(FKEY, JSON.stringify(f));
}
