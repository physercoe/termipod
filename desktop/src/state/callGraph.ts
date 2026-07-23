/// IPC + persistence layer of the code→call-graph slice (plan §5, W4). The pure
/// core (helper, probe, remote-command assembly) is in [[callGraphCore]]; this
/// module drives it over the two venues, reusing the tracer's plumbing:
///
/// **Local**: the generic `trace_run` IPC pipes the helper to a local interpreter's
/// stdin (the same runner the tracer uses — it is engine-agnostic). **Remote**:
/// `ssh_exec` runs the assembled one-liner on a saved SSH host, so a box that has
/// code2flow (and, for JS/Ruby/PHP, Acorn / the Parser gem / PHP-Parser) installed
/// does the parse. The interpreter is a free-text preset, hence stdin-piping. The
/// **import-locality rule** is lighter here than for the tracer — code2flow only
/// reads source *files*, it does not import them — but running with cwd = the repo
/// root keeps relative target paths honest.
import { invoke } from '../bridge';
import { sshExec } from '../ssh/native';
import { connectSaved } from './inspectSources';
import type { Connection } from './connections';
import {
  CODE2FLOW_HELPER,
  CODE2FLOW_PROBE,
  callGraphEnv,
  extractDot,
  remoteCallGraphCommand,
  remoteCallGraphProbe,
  type CallGraphParams,
} from './callGraphCore';

export type { CallGraphParams, CallGraphLang } from './callGraphCore';

interface TraceResult {
  code: number | null;
  stdout: string;
  stderr: string;
  timedOut: boolean;
}

export interface CallGraphRequest extends CallGraphParams {
  /// Set → runs on this saved SSH host; undefined → local interpreter.
  connection?: Connection;
}

/// Run code2flow and return the DOT string. Throws with the interpreter's stderr
/// (or the raw output) when no graph came back.
export async function runCallGraph(p: CallGraphRequest): Promise<string> {
  if (p.connection === undefined) {
    const r = await invoke<TraceResult>('trace_run', {
      command: p.command,
      content: CODE2FLOW_HELPER,
      env: callGraphEnv(p),
      cwd: p.repoRoot || null,
    });
    if (r.timedOut) throw new Error(r.stderr || 'the call-graph run timed out');
    const dot = extractDot(r.stdout);
    if (dot !== null) return dot;
    throw new Error((r.stderr || r.stdout).trim() || 'code2flow produced no graph');
  }
  const sid = await connectSaved(p.connection);
  const out = await sshExec(sid, remoteCallGraphCommand(p));
  const dot = extractDot(out);
  if (dot !== null) return dot;
  throw new Error(out.trim() || 'code2flow produced no graph');
}

/// Probe an interpreter for code2flow. Returns the OK line on success; throws with
/// the failure otherwise.
export async function detectCode2flow(command: string, connection?: Connection): Promise<string> {
  if (connection === undefined) {
    const r = await invoke<TraceResult>('trace_run', { command, content: CODE2FLOW_PROBE, timeoutMs: 20_000 });
    if (r.code === 0) return r.stdout.trim();
    throw new Error(r.stderr.trim() || 'code2flow not importable on this interpreter');
  }
  const sid = await connectSaved(connection);
  const out = await sshExec(sid, remoteCallGraphProbe(command));
  if (/OK code2flow/.test(out)) return out.trim();
  throw new Error(out.trim() || 'code2flow not importable on this interpreter');
}

// ── persistence ────────────────────────────────────────────────────────────────
// The interpreter preset is shared with the tracer (same venue, same Python), so
// call-graph reuses [[trace]]'s per-venue `getInterp`/`setInterp`. Only the last
// language selection is remembered here.
const LKEY = 'termipod.inspect.callgraph.lang';

export function getLastLang(): string {
  return localStorage.getItem(LKEY) ?? '';
}
export function setLastLang(lang: string): void {
  localStorage.setItem(LKEY, lang);
}
