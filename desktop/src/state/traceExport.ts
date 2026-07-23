/// IPC layer of tracer Tier 2 (plan §5 — `torch.export` → Model Explorer). The pure
/// core (helper, parse, remote-command assembly) is in [[traceExportCore]]; the
/// `ExportGraph → GraphCollection` build in [[modelGraph]]. Reuses the tracer's generic
/// `trace_run` IPC (local) / `ssh_exec` (remote) and the shared `TraceParams` shape.
import { invoke } from '../bridge';
import { sshExec } from '../ssh/native';
import { connectSaved } from './inspectSources';
import type { Connection } from './connections';
import type { TraceParams } from './traceCore';
import { exportToGraphCollection, type GraphCollection } from './modelGraph';
import { TORCH_EXPORT_HELPER, TORCH_PROBE, parseExportGraph, remoteExportCommand, remoteTorchProbe } from './traceExportCore';

interface TraceResult {
  code: number | null;
  stdout: string;
  stderr: string;
  timedOut: boolean;
}

export interface ExportRequest extends TraceParams {
  connection?: Connection;
}

/// Run `torch.export` on the model and return the traced graph as a Model Explorer
/// `GraphCollection`. Throws with the interpreter's stderr when no graph came back.
export async function runTraceExport(p: ExportRequest): Promise<GraphCollection> {
  const label = p.entry.trim() !== '' ? p.entry : 'traced';
  if (p.connection === undefined) {
    const env = { TRACE_ENTRY: p.entry, TRACE_INPUT: p.shape, TRACE_FILE: p.filePath };
    const r = await invoke<TraceResult>('trace_run', { command: p.command, content: TORCH_EXPORT_HELPER, env, cwd: p.repoRoot || null, timeoutMs: 180_000 });
    if (r.timedOut) throw new Error(r.stderr || 'the export timed out');
    const g = parseExportGraph(r.stdout);
    if (g !== null) return exportToGraphCollection(g, label);
    throw new Error((r.stderr || r.stdout).trim() || 'torch.export produced no graph');
  }
  const sid = await connectSaved(p.connection);
  const out = await sshExec(sid, remoteExportCommand(p.entry, p.shape, p.filePath, p.command, p.repoRoot));
  const g = parseExportGraph(out);
  if (g !== null) return exportToGraphCollection(g, label);
  throw new Error(out.trim() || 'torch.export produced no graph');
}

/// Probe an interpreter for torch (Tier 2 needs torch, not torchview). Returns the
/// version line on success; throws otherwise.
export async function detectTorch(command: string, connection?: Connection): Promise<string> {
  if (connection === undefined) {
    const r = await invoke<TraceResult>('trace_run', { command, content: TORCH_PROBE, timeoutMs: 20_000 });
    if (r.code === 0) return r.stdout.trim();
    throw new Error(r.stderr.trim() || 'torch not importable on this interpreter');
  }
  const sid = await connectSaved(connection);
  const out = await sshExec(sid, remoteTorchProbe(command));
  if (/OK torch/.test(out)) return out.trim();
  throw new Error(out.trim() || 'torch not importable on this interpreter');
}
