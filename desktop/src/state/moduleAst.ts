/// IPC layer of the W4b module reader (plan §4b). The pure core (helper, parse,
/// graph build, remote-command assembly) is in [[moduleAstCore]]; this drives it over
/// the two venues, reusing the tracer's generic `trace_run` IPC (local) and
/// `ssh_exec` (remote). The helper is stdlib-only, so the default `python3`
/// interpreter works everywhere — no preset/Detect ceremony (unlike the torch tracer
/// or code2flow, which need a specific package on the venue).
import { invoke } from '../bridge';
import { sshExec } from '../ssh/native';
import { connectSaved } from './inspectSources';
import type { Connection } from './connections';
import { MODULE_AST_HELPER, parseModuleAst, remoteModuleAstCommand, type ModuleModel } from './moduleAstCore';

export type { ModuleModel } from './moduleAstCore';

interface TraceResult {
  code: number | null;
  stdout: string;
  stderr: string;
  timedOut: boolean;
}

export interface ModuleAstRequest {
  /// The modeling file path as seen on the venue.
  filePath: string;
  /// Interpreter (default `python3`); the helper is stdlib so any python3 works.
  command?: string;
  /// Working directory (defaults to none).
  repoRoot?: string;
  /// Set → parse on this saved SSH host; undefined → local interpreter.
  connection?: Connection;
}

/// Parse a modeling file's class hierarchy on its venue. Throws with the
/// interpreter's stderr when the file couldn't be read/parsed.
export async function runModuleAst(p: ModuleAstRequest): Promise<ModuleModel> {
  const command = p.command ?? 'python3';
  if (p.connection === undefined) {
    const r = await invoke<TraceResult>('trace_run', {
      command,
      content: MODULE_AST_HELPER,
      env: { AST_FILE: p.filePath },
      cwd: p.repoRoot ?? null,
      timeoutMs: 30_000,
    });
    if (r.timedOut) throw new Error(r.stderr || 'the module parse timed out');
    const m = parseModuleAst(r.stdout);
    if (m !== null) return m;
    throw new Error((r.stderr || r.stdout).trim() || 'could not read the module');
  }
  const sid = await connectSaved(p.connection);
  const out = await sshExec(sid, remoteModuleAstCommand(p.filePath, command, p.repoRoot ?? ''));
  const m = parseModuleAst(out);
  if (m !== null) return m;
  throw new Error(out.trim() || 'could not read the module');
}
