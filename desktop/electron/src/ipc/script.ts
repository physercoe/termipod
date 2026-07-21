/// Script runner + local-agent CLI (ADR-055 M2.4) — the Electron port of
/// `src-tauri/src/script.rs` and `src-tauri/src/local_agent.rs`. Same command
/// names (`script_run` / `local_agent_run`) and return shapes, so `state/
/// scriptRun.ts` and the local-agent caller drive them unchanged.
///
/// Both are one-shot, non-interactive child-process runs (captured output) —
/// the right shape for a vault script snippet or a `claude -p` quick ask.
/// Interactive/long-running work belongs in the PTY terminal, not here. The
/// snippet is written to a temp file handed to the interpreter as a file
/// argument, and the agent prompt is a trailing argv element — never
/// interpolated into a shell string, so there is no injection surface the
/// author didn't already have on their own machine.
import { execFile } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import os from 'node:os';
import path from 'node:path';
import { rm, writeFile } from 'node:fs/promises';
import type { Handler } from './dispatch';

const RUN_TIMEOUT_MS = 120_000; // 120s wall-clock cap (script.rs RUN_TIMEOUT_SECS)
const MAX_OUTPUT = 256 * 1024; // clamp captured output shown in the UI

/// Map an interpreter label to [program, leading args, temp-file extension]. An
/// unknown non-empty label is used verbatim as the program (with a `.txt` temp
/// file), so any interpreter on PATH works without a code change. Mirrors
/// script.rs `interpreter_spec`.
function interpreterSpec(interp: string): [string, string[], string] {
  switch (interp.trim().toLowerCase()) {
    case 'bash':
      return ['bash', [], 'sh'];
    case 'sh':
      return ['sh', [], 'sh'];
    case 'zsh':
      return ['zsh', [], 'sh'];
    case 'python':
    case 'python3':
      return ['python3', [], 'py'];
    case 'node':
      return ['node', [], 'js'];
    case 'pwsh':
    case 'powershell':
      return ['pwsh', ['-NoProfile', '-File'], 'ps1'];
    case 'ruby':
      return ['ruby', [], 'rb'];
    default: {
      const other = interp.trim().toLowerCase();
      return other !== '' ? [other, [], 'txt'] : ['bash', [], 'sh'];
    }
  }
}

function clamp(s: string): string {
  return s.length > MAX_OUTPUT ? `${s.slice(0, MAX_OUTPUT)}\n… (output truncated)` : s;
}

interface ScriptResult {
  code: number | null;
  stdout: string;
  stderr: string;
  timedOut: boolean;
}

/// Run one child capturing stdout/stderr. Resolves with the outcome (a non-zero
/// exit is a successful call with a non-zero `code`, never a throw); `spawnError`
/// is set only when the program can't be launched at all (ENOENT/EACCES).
/// `timeoutMs` of 0 means no wall-clock cap. `execFile` (no shell) → the argv is
/// passed literally, so nothing is interpreted.
function runCaptured(
  program: string,
  args: string[],
  cwd: string | undefined,
  timeoutMs: number,
): Promise<{ code: number | null; stdout: string; stderr: string; timedOut: boolean; spawnError?: Error }> {
  return new Promise((resolve) => {
    execFile(
      program,
      args,
      { cwd, timeout: timeoutMs, maxBuffer: 16 * 1024 * 1024, encoding: 'utf8', windowsHide: true },
      (err, stdout, stderr) => {
        if (err === null) {
          resolve({ code: 0, stdout, stderr, timedOut: false });
          return;
        }
        const e = err as NodeJS.ErrnoException & { killed?: boolean };
        // A string `code` (ENOENT/EACCES) means the program never launched; a
        // numeric one is the child's exit code; killed === true is the timeout.
        if (typeof e.code === 'string') {
          resolve({ code: null, stdout, stderr, timedOut: false, spawnError: err });
          return;
        }
        const timedOut = e.killed === true;
        resolve({ code: typeof e.code === 'number' ? e.code : null, stdout, stderr, timedOut });
      },
    );
  });
}

export const scriptHandlers: Record<string, Handler> = {
  script_run: async (args): Promise<ScriptResult> => {
    const interpreter = String(args.interpreter ?? '');
    const content = String(args.content ?? '');
    const cwdRaw = args.cwd;
    const cwd = typeof cwdRaw === 'string' && cwdRaw !== '' ? cwdRaw : undefined;
    if (content.trim() === '') throw new Error('script is empty');

    const [program, leadArgs, ext] = interpreterSpec(interpreter);
    // Unique temp path: pid + random, matching script.rs's collision-free scheme.
    const file = path.join(os.tmpdir(), `termipod-script-${process.pid}-${randomBytes(8).toString('hex')}.${ext}`);
    try {
      await writeFile(file, content, 'utf8');
    } catch (e) {
      throw new Error(`could not stage script: ${(e as Error).message}`);
    }
    try {
      const r = await runCaptured(program, [...leadArgs, file], cwd, RUN_TIMEOUT_MS);
      if (r.spawnError !== undefined) {
        // Could not launch the interpreter at all (ENOENT/EACCES).
        throw new Error(`could not run '${program}': ${r.spawnError.message}`);
      }
      if (r.timedOut) {
        return { code: null, stdout: '', stderr: `timed out after ${RUN_TIMEOUT_MS / 1000}s — killed`, timedOut: true };
      }
      return { code: r.code, stdout: clamp(r.stdout), stderr: clamp(r.stderr), timedOut: false };
    } finally {
      await rm(file, { force: true }).catch(() => {}); // always remove the temp file
    }
  },

  local_agent_run: async (args): Promise<string> => {
    const program = String(args.program ?? '');
    const cmdArgs = Array.isArray(args.args) ? args.args.map(String) : [];
    const prompt = String(args.prompt ?? '');
    const cwdRaw = args.cwd;
    const cwd = typeof cwdRaw === 'string' && cwdRaw !== '' ? cwdRaw : undefined;
    if (program.trim() === '') throw new Error('no local agent command configured');

    // No wall-clock cap (timeoutMs 0) — local_agent.rs uses plain output(); a
    // `claude -p` reply can legitimately run well past a script's 120s bound.
    const r = await runCaptured(program, [...cmdArgs, prompt], cwd, 0);
    if (r.spawnError !== undefined) {
      throw new Error(`could not run '${program}': ${r.spawnError.message}`);
    }
    if (r.code !== 0) {
      const err = r.stderr.trim();
      throw new Error(err !== '' ? err : `'${program}' exited with ${r.code ?? 'signal'}`);
    }
    return r.stdout.trim();
  },
};
