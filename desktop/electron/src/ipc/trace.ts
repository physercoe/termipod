/// Model-graph tracer runner (plan §5, W4 code→graph tracer) — runs a vendored
/// Python helper on a **local** interpreter, piping the helper to the process's
/// **stdin** (never a temp file) so a multi-word interpreter preset works
/// uniformly: `python3`, `/opt/venv/bin/python`, `conda run -n rl python`,
/// `docker exec -i box python`, `uv run python`. The trace parameters ride in as
/// environment variables (`TRACE_ENTRY` / `TRACE_INPUT` / …), never interpolated
/// into a command string, so there is no injection surface the author didn't
/// already have on their own machine. Remote (SSH) venues go a different route
/// (`ssh_exec` with the helper base64-embedded) — this handler is local only.
///
/// One-shot, wall-clock capped, captured output — the same shape as `script.ts`.
import { spawn } from 'node:child_process';
import type { Handler } from './dispatch';

const RUN_TIMEOUT_MS = 120_000; // 120s cap (a meta-device trace is fast; a real forward is not this path)
const MAX_OUTPUT = 512 * 1024; // DOT for a large graph can be sizeable; clamp anyway

function clamp(s: string): string {
  return s.length > MAX_OUTPUT ? `${s.slice(0, MAX_OUTPUT)}\n… (output truncated)` : s;
}

// Split an interpreter preset into argv on whitespace. Good for the presets the
// tracer form offers; a path with embedded spaces is the documented caveat (use a
// wrapper script for that). Empty → falls back to `python3`.
function argvOf(command: string): string[] {
  const parts = command.trim().split(/\s+/).filter((p) => p !== '');
  return parts.length > 0 ? parts : ['python3'];
}

interface TraceResult {
  code: number | null;
  stdout: string;
  stderr: string;
  timedOut: boolean;
}

export const traceHandlers: Record<string, Handler> = {
  trace_run: async (args): Promise<TraceResult> => {
    const content = String(args.content ?? '');
    if (content.trim() === '') throw new Error('trace helper is empty');
    const [program, ...argv] = argvOf(String(args.command ?? ''));
    const cwdRaw = args.cwd;
    const cwd = typeof cwdRaw === 'string' && cwdRaw !== '' ? cwdRaw : undefined;
    const timeoutMs = typeof args.timeoutMs === 'number' && args.timeoutMs >= 0 ? args.timeoutMs : RUN_TIMEOUT_MS;
    const envExtra: Record<string, string> = {};
    if (args.env !== null && typeof args.env === 'object') {
      for (const [k, v] of Object.entries(args.env as Record<string, unknown>)) {
        if (typeof v === 'string') envExtra[k] = v;
      }
    }

    return new Promise<TraceResult>((resolve, reject) => {
      let child;
      try {
        child = spawn(program, argv, {
          cwd,
          env: { ...process.env, ...envExtra },
          stdio: ['pipe', 'pipe', 'pipe'],
          windowsHide: true,
        });
      } catch (e) {
        reject(new Error(`could not launch '${program}': ${(e as Error).message}`));
        return;
      }
      let out = '';
      let err = '';
      let done = false;
      const timer = timeoutMs > 0 ? setTimeout(() => {
        if (!done) {
          done = true;
          child.kill('SIGKILL');
          resolve({ code: null, stdout: clamp(out), stderr: `timed out after ${Math.round(timeoutMs / 1000)}s — killed`, timedOut: true });
        }
      }, timeoutMs) : null;

      child.stdout.on('data', (d: Buffer) => {
        if (out.length < MAX_OUTPUT * 2) out += d.toString('utf8');
      });
      child.stderr.on('data', (d: Buffer) => {
        if (err.length < MAX_OUTPUT * 2) err += d.toString('utf8');
      });
      child.on('error', (e) => {
        if (done) return;
        done = true;
        if (timer !== null) clearTimeout(timer);
        reject(new Error(`could not run '${program}': ${e.message}`));
      });
      child.on('close', (code) => {
        if (done) return;
        done = true;
        if (timer !== null) clearTimeout(timer);
        resolve({ code, stdout: clamp(out), stderr: clamp(err), timedOut: false });
      });

      child.stdin.on('error', () => {}); // ignore EPIPE if the child exits before reading
      child.stdin.write(content);
      child.stdin.end();
    });
  },
};
