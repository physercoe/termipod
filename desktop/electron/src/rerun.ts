/// Rerun companion manager (J8 Replay W4) — spawn/lifecycle for a local
/// `rerun --serve-web` process hosting one recording, embedded in a `rerunweb`
/// web panel.
///
/// **INTEGRATE, not embed** (plan §7, and the landscape survey's correction):
/// Rerun has no plugin API and its SDK and viewer move in lock-step, so the
/// viewer is a companion in an iframe, not something we render inside our own
/// panels. What our player deliberately does not do — point clouds, depth,
/// transforms, free multimodal layout — this is the answer for.
///
/// Shape borrowed wholesale from `kimiweb.ts`: spawn in the main process,
/// serialized lifecycle, kill on last close and at `before-quit`, a clear typed
/// error when the binary is missing. Electron-free so the pure half runs under
/// plain `node --test`.
///
/// **One server at a time, not one per recording.** Each rerun process holds a
/// web server, a gRPC server and the whole recording in memory; a user scrubbing
/// through episodes would otherwise accumulate one per episode they glanced at.
/// Opening a different recording replaces the current one.
///
/// Every flag below was read off rerun's own CLI definition
/// (`crates/top/rerun/src/commands/entrypoint.rs`) rather than remembered, and
/// the viewer URL off `crates/top/re_sdk/src/web_viewer.rs`. What has NOT
/// happened is a live run: no rerun process has been started against this code.
import { spawn, type ChildProcess } from 'node:child_process';
// Explicit `.ts` specifiers so `node --test` can load this module directly
// (rerun_spawn.test.ts exercises the real spawn). Same reason browserbridge.ts
// spells its import that way; esbuild and `allowImportingTsExtensions` both
// accept it, and the alternative is a manager nothing can test against a
// process.
import { buildSpawnEnv, pickFreePort, sanitizeTail } from './kimiweb.ts';
import {
  RERUN_START_TIMEOUT_MS,
  isRecordingPath,
  extractViewerUrl,
  rerunArgs,
  resolveRerunBinary,
} from './rerun_policy.ts';
import type { Handler } from './ipc/dispatch';

// ── lifecycle (single server, serialized) ────────────────────────────────────

let proc: ChildProcess | null = null;
let current: { recording: string; url: string } | null = null;
// Same serialization as kimiweb: a stop landing between two starts replays in
// order — kill, then respawn — instead of racing the child.
let tail: Promise<unknown> = Promise.resolve();

function enqueue<T>(op: () => Promise<T>): Promise<T> {
  const p = tail.then(op, op);
  tail = p.catch(() => undefined);
  return p;
}

function spawnCmd(bin: string, args: string[]): { file: string; args: string[] } {
  if (process.platform === 'win32' && !bin.toLowerCase().endsWith('.exe')) {
    return { file: 'cmd.exe', args: ['/C', bin, ...args] };
  }
  return { file: bin, args };
}

function killChild(child: ChildProcess): void {
  try {
    child.kill();
  } catch {
    /* already gone */
  }
}

function resetIfCurrent(child: ChildProcess): void {
  if (proc === child) {
    proc = null;
    current = null;
  }
}

async function spawnServer(recording: string): Promise<string> {
  const env = await buildSpawnEnv();
  const bin = resolveRerunBinary(env);
  if (bin === null) {
    throw new Error(
      'rerun not found — install it (`pip install rerun-sdk`) or set TERMIPOD_RERUN_BIN to its path',
    );
  }
  const webPort = await pickFreePort();
  let grpcPort = await pickFreePort();
  // pickFreePort binds ephemeral ports independently, so the same one can come
  // back twice; rerun refuses the collision and would exit before serving.
  while (grpcPort === webPort) grpcPort = await pickFreePort();
  const ports = { webPort, grpcPort };

  const cmd = spawnCmd(bin, rerunArgs(recording, ports));
  const child = spawn(cmd.file, cmd.args, { stdio: ['ignore', 'pipe', 'pipe'], env });
  proc = child;
  child.on('exit', () => resetIfCurrent(child));

  let buf = '';
  return await new Promise<string>((resolve, reject) => {
    const fail = (msg: string): void => {
      clearTimeout(timer);
      resetIfCurrent(child);
      killChild(child);
      reject(new Error(msg));
    };
    const timer = setTimeout(() => {
      fail(`rerun did not start serving within ${RERUN_START_TIMEOUT_MS / 1000}s`);
    }, RERUN_START_TIMEOUT_MS);
    child.once('error', (e) => {
      const enoent = (e as NodeJS.ErrnoException).code === 'ENOENT';
      fail(
        enoent
          ? `rerun binary not found (tried '${bin}') — install rerun-sdk or set TERMIPOD_RERUN_BIN`
          : `failed to spawn '${bin}': ${e.message}`,
      );
    });
    child.once('exit', (code) => {
      const tailOut = sanitizeTail(buf.trim().split('\n').slice(-3).join(' | '));
      fail(`rerun exited (code ${String(code)}) before serving${tailOut !== '' ? `: ${tailOut}` : ''}`);
    });
    // rerun logs through `re_log`, which writes to stderr — watching stdout
    // alone would wait out the whole timeout on a server that came up fine.
    const onChunk = (d: Buffer): void => {
      buf += d.toString('utf8');
      const url = extractViewerUrl(buf);
      if (url === null) return;
      clearTimeout(timer);
      child.removeAllListeners('error');
      child.removeAllListeners('exit');
      child.on('exit', () => resetIfCurrent(child));
      resolve(url);
    };
    child.stdout?.on('data', onChunk);
    child.stderr?.on('data', onChunk);
  });
}

/// Start (or reuse) a server for one recording, resolving with the embed URL.
export function rerunStart(recording: string): Promise<{ url: string }> {
  return enqueue(async () => {
    if (!isRecordingPath(recording)) {
      throw new Error(`not a Rerun recording: ${recording === '' ? '(empty path)' : recording}`);
    }
    if (proc !== null && current !== null && current.recording === recording) {
      return { url: current.url };
    }
    // A different recording replaces the current one rather than joining it:
    // see the one-server-at-a-time note at the top.
    if (proc !== null) {
      killChild(proc);
      proc = null;
      current = null;
    }
    const url = await spawnServer(recording);
    current = { recording, url };
    return { url };
  });
}

export function rerunStop(): Promise<void> {
  return enqueue(async () => {
    if (proc !== null) killChild(proc);
    proc = null;
    current = null;
  });
}

export function rerunStatus(): { running: boolean; url: string | null; recording: string | null } {
  return {
    running: proc !== null,
    url: current?.url ?? null,
    recording: current?.recording ?? null,
  };
}

/// Synchronous teardown for `before-quit` — quitting must never orphan a server
/// holding two loopback ports open.
export function disposeRerun(): void {
  if (proc !== null) killChild(proc);
  proc = null;
  current = null;
}

export const rerunHandlers: Record<string, Handler> = {
  rerun_start: (args) => rerunStart(String(args.recording ?? '')),
  rerun_stop: () => rerunStop(),
  rerun_status: () => rerunStatus(),
};
