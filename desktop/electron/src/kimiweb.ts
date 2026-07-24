/// kimiweb manager (agent-transcript-redesign P0) — spawn/lifecycle for the
/// local `kimi web` server whose SPA is embedded in a session web panel.
/// Local-process management mirrors the pty family's approach (ipc/pty.ts):
/// spawn in the main process, kill on last-consumer close + app quit, a clear
/// typed error when the binary is missing.
///
/// One shared server per app: kimi-web is an SPA with client-side session
/// switching and NO per-session deep link (plan caveat (a) — the URL hash
/// carries the token, not routes), so every web panel mounts the same embed
/// URL and the user picks the session in kimi's own sidebar. `kimiweb_start`
/// is refcounted — the server dies when the last panel closes (and always at
/// `before-quit`, so quitting never orphans it).
///
/// The embed URL (`http://127.0.0.1:<port>/#token=<tok>`) is parsed from the
/// server's stdout banner — kimi-code 0.28.x prints:
///   "  Local:    http://127.0.0.1:17331/#token=9Omd…"
/// The token authenticates the SPA's API calls; the guest's navigation is
/// pinned to loopback by the `kimiweb` partition policy (webtab_policy.ts),
/// so the token never leaves the machine in a top-frame load.
///
/// Deliberately electron-free (like ipc/download.ts's core) so the unit tests
/// run under plain `node --test`.
import { spawn, type ChildProcess } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import type { AddressInfo } from 'node:net';
import type { Handler } from './ipc/dispatch';

export const KIMIWEB_START_TIMEOUT_MS = 15_000;

/// Extract the embed URL from kimi web's stdout banner. Matches the hash-token
/// URL itself (not the `Local:` label) so wording/whitespace changes in the
/// banner don't break the parse; returns null when the token line never came.
export function extractServerUrl(text: string): string | null {
  const m = /https?:\/\/[^\s"'#]+#token=[^\s"']+/.exec(text);
  return m === null ? null : m[0];
}

/// The well-known binary location: `$KIMI_CODE_HOME/bin/kimi` (default
/// `~/.kimi-code`). Injectable for tests.
export function kimiBinaryPath(env: NodeJS.ProcessEnv = process.env, home: string = os.homedir()): string {
  const base = env.KIMI_CODE_HOME ?? path.join(home, '.kimi-code');
  return path.join(base, 'bin', process.platform === 'win32' ? 'kimi.cmd' : 'kimi');
}

/// The binary to spawn: the well-known install path when it exists, else the
/// bare name as a PATH fallback (a launch from a real terminal resolves it; a
/// Finder/Dock launch with no login-shell PATH may not — the spawn then fails
/// with ENOENT and the handler surfaces the "binary not found" error below).
export function resolveKimiBinary(
  env: NodeJS.ProcessEnv = process.env,
  home: string = os.homedir(),
  exists: (p: string) => boolean = fs.existsSync,
): string {
  const explicit = kimiBinaryPath(env, home);
  return exists(explicit) ? explicit : process.platform === 'win32' ? 'kimi.cmd' : 'kimi';
}

/// An ephemeral loopback port for the server. There is an inherent close→bind
/// race before kimi takes it; a collision surfaces as an early exit, which the
/// spawn error path reports (and the panel offers retry).
export function pickFreePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.once('error', reject);
    srv.listen(0, '127.0.0.1', () => {
      const { port } = srv.address() as AddressInfo;
      srv.close(() => resolve(port));
    });
  });
}

// ── Server lifecycle (refcounted, serialized) ────────────────────────────────

let proc: ChildProcess | null = null;
let serverUrl: string | null = null;
let users = 0;
// All lifecycle mutations run through this chain: a stop that lands between a
// start and a later start (React StrictMode dev double-mount) then replays
// strictly in order — kill, then respawn — instead of racing the child.
let tail: Promise<unknown> = Promise.resolve();

function enqueue<T>(op: () => Promise<T>): Promise<T> {
  const p = tail.then(op, op);
  tail = p.catch(() => undefined);
  return p;
}

/// On Windows the binary is a shim ConPTY/CreateProcess can't exec directly —
/// run it through `cmd.exe /C`, mirroring pty.ts's buildSpawn.
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
    serverUrl = null;
  }
}

/// Spawn `kimi web --no-open --port <free>` and resolve with the embed URL once
/// the banner prints it. Rejects with a clear error when the binary is missing,
/// the server exits early, or the token never prints (timeout).
async function spawnServer(): Promise<string> {
  const bin = resolveKimiBinary();
  const port = await pickFreePort();
  const cmd = spawnCmd(bin, ['web', '--no-open', '--port', String(port)]);
  const child = spawn(cmd.file, cmd.args, { stdio: ['ignore', 'pipe', 'pipe'] });
  proc = child;
  // A later exit (user Ctrl+C in a stray terminal, crash) invalidates the
  // cached URL so the next `kimiweb_start` respawns instead of handing the
  // panel a dead origin.
  child.on('exit', () => resetIfCurrent(child));

  let buf = '';
  let errBuf = '';
  return await new Promise<string>((resolve, reject) => {
    const fail = (msg: string): void => {
      clearTimeout(timer);
      resetIfCurrent(child);
      killChild(child);
      reject(new Error(msg));
    };
    const timer = setTimeout(() => {
      fail(`kimi web did not print its server URL within ${KIMIWEB_START_TIMEOUT_MS / 1000}s`);
    }, KIMIWEB_START_TIMEOUT_MS);
    child.once('error', (e) => {
      const enoent = (e as NodeJS.ErrnoException).code === 'ENOENT';
      fail(
        enoent
          ? `kimi binary not found (tried '${bin}') — install kimi-code or set KIMI_CODE_HOME`
          : `failed to spawn '${bin}': ${e.message}`,
      );
    });
    // An exit before the URL printed means the server never came up.
    child.once('exit', (code) => {
      const tailOut = (errBuf + buf).trim().split('\n').slice(-3).join(' | ');
      fail(`kimi web exited (code ${String(code)}) before printing its server URL${tailOut !== '' ? `: ${tailOut}` : ''}`);
    });
    child.stdout?.on('data', (d: Buffer) => {
      buf += d.toString('utf8');
      const url = extractServerUrl(buf);
      if (url === null) return;
      clearTimeout(timer);
      // Detach the one-shot startup-failure listeners: the server is up, and a
      // later exit is handled by the reset hook above, not a start failure.
      child.removeAllListeners('error');
      child.removeAllListeners('exit');
      child.on('exit', () => resetIfCurrent(child));
      resolve(url);
    });
    child.stderr?.on('data', (d: Buffer) => {
      errBuf += d.toString('utf8');
    });
  });
}

async function ensureStarted(): Promise<string> {
  if (proc !== null && serverUrl !== null) return serverUrl;
  serverUrl = await spawnServer();
  return serverUrl;
}

export function kimiwebStart(): Promise<string> {
  return enqueue(async () => {
    users += 1;
    return ensureStarted();
  });
}

export function kimiwebStop(): Promise<void> {
  return enqueue(async () => {
    users = Math.max(0, users - 1);
    if (users > 0) return;
    if (proc !== null) killChild(proc);
    proc = null;
    serverUrl = null;
  });
}

export function kimiwebStatus(): { running: boolean; url: string | null } {
  return { running: proc !== null && serverUrl !== null, url: serverUrl };
}

/// Kill the server on app quit (wired to `before-quit` in main.ts, next to
/// disposeAllPtys). Best-effort and synchronous — the app is going down.
export function disposeKimiWeb(): void {
  users = 0;
  if (proc !== null) killChild(proc);
  proc = null;
  serverUrl = null;
}

export const kimiwebHandlers: Record<string, Handler> = {
  /// Start (or reuse) the shared `kimi web` server and hand back the embed URL
  /// — `http://127.0.0.1:<port>/#token=<tok>`, parsed from the server banner.
  /// Refcounted: pair every panel's start with a stop on close.
  kimiweb_start: async (): Promise<{ url: string }> => ({ url: await kimiwebStart() }),
  kimiweb_stop: async (): Promise<void> => {
    await kimiwebStop();
  },
  kimiweb_status: async (): Promise<{ running: boolean; url: string | null }> => kimiwebStatus(),
};
