/// Local PTY command family (ADR-055 M2) — the Electron port of
/// `src-tauri/src/pty.rs`. Same command names (`pty_open` / `pty_start` /
/// `pty_write` / `pty_resize` / `pty_close`) and the same `pty-data` / `pty-exit`
/// event contract, so `src/terminal/pty.ts` drives it unchanged through the
/// bridge. Engine: `node-pty` (portable-pty's Node analogue) rather than
/// portable-pty.
///
/// Two properties carried over from pty.rs's hard-won Windows fixes:
///
///  1. **The reader is gated behind `pty_start`.** A local shell prints its
///     prompt within microseconds of spawning; if it were delivered before the
///     renderer's async `listen('pty-data')` registered, the banner would be
///     lost (the "black local shell"). portable-pty gated this by not spawning
///     its reader thread until `pty_start`; node-pty has no separate reader — it
///     auto-flows `onData` the moment the shell writes. So we attach `onData` at
///     `pty_open` but **buffer** every chunk in JS until `pty_start` flushes it.
///     Same guarantee, one layer up: nothing emitted reaches a renderer that
///     isn't yet listening, because the frontend calls `pty_start` only after its
///     listeners are attached. A pre-start `onExit` is likewise deferred to the
///     flush, so a shell that dies instantly still reports its exit.
///
///  2. **Login-shell PATH recovery on unix.** A GUI-launched app (Finder/Dock)
///     inherits a minimal PATH, so npm/Homebrew/nvm agent CLIs (`claude`,
///     `codex`, …) aren't found. We resolve the login shell's PATH once by
///     spawning `$SHELL -ilc` and inject it per spawn. Resolved with async
///     `execFile` (not `execFileSync`) so the ~100–500ms shell startup never
///     blocks the main/UI thread (feedback: sync command = main thread).
///
/// node-pty is a native addon: it stays an esbuild external and M3 packaging must
/// asarUnpack it and rebuild it for Electron's ABI (like `@napi-rs/keyring`).
///
/// BYTE FIDELITY CAVEAT: node-pty's `onData` delivers a UTF-8 *string*, not raw
/// bytes, so we re-encode with `Buffer.from(d, 'utf8')` to honour the `bytes`
/// contract. For well-formed UTF-8 this is lossless; a multibyte sequence split
/// across two chunks is decoded to U+FFFD by node-pty before we ever see it (the
/// raw-byte path in pty.rs let xterm buffer the split itself). This is an
/// inherent node-pty limitation the whole Electron-terminal ecosystem lives with.
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import type { WebContents } from 'electron';
import { emit } from '../events';
import type { Handler } from './dispatch';

const pexecFile = promisify(execFile);

// node-pty is a native external module; load it lazily and once. A load failure
// rejects the first `pty_open`, surfacing an error to the renderer rather than
// crashing the app at require time.
type PtyModule = typeof import('node-pty');
let ptyModP: Promise<PtyModule> | null = null;
function loadPty(): Promise<PtyModule> {
  if (ptyModP === null) ptyModP = import('node-pty');
  return ptyModP;
}

interface PtyOpenReq {
  shell?: string;
  args?: string[];
  cwd?: string;
  cols: number;
  rows: number;
}

interface Session {
  term: import('node-pty').IPty;
  sender: WebContents;
  started: boolean;
  buffered: Buffer[]; // data seen before pty_start (flushed on start)
  exited: number | null | undefined; // undefined = still running; set if exit fired pre-start
}

let nextId = 1;
const sessions = new Map<string, Session>();

/// The user's real login-shell PATH, resolved once and cached as a promise. On a
/// Finder/Dock launch the inherited PATH omits npm/Homebrew/nvm dirs; spawning
/// `$SHELL -ilc` sources the same startup files a real terminal does. Markers
/// strip startup-script stdout noise. `null` on any failure (or on Windows),
/// which leaves the inherited PATH untouched.
let loginPathP: Promise<string | null> | null = null;
function loginPath(): Promise<string | null> {
  if (process.platform === 'win32') return Promise.resolve(null);
  if (loginPathP === null) loginPathP = resolveLoginPath();
  return loginPathP;
}
async function resolveLoginPath(): Promise<string | null> {
  try {
    const shell = process.env.SHELL ?? '/bin/bash';
    const MARK = '__TP_PATH__';
    // A closed stdin makes an interactive shell see EOF and exit (no hang).
    const { stdout } = await pexecFile(shell, ['-ilc', `printf '${MARK}%s${MARK}' "$PATH"`], {
      timeout: 5000,
      encoding: 'utf8',
    });
    const start = stdout.indexOf(MARK);
    if (start < 0) return null;
    const rest = stdout.slice(start + MARK.length);
    const end = rest.indexOf(MARK);
    if (end < 0) return null;
    const p = rest.slice(0, end).trim();
    return p === '' ? null : p;
  } catch {
    return null;
  }
}

/// The default shell when the request omits one, or `$SHELL` / `%COMSPEC%` is
/// unset (a bare service environment).
function defaultShell(): string {
  if (process.platform === 'win32') return process.env.COMSPEC ?? 'powershell.exe';
  return process.env.SHELL ?? '/bin/bash';
}

/// On Windows an npm-installed agent CLI (`claude`, `codex`) is a `.cmd`/`.ps1`
/// shim that ConPTY cannot exec directly — it must run through `cmd.exe /C`.
/// Native `.exe` programs (incl. the default `cmd.exe`/`powershell.exe`) and all
/// unix programs are launched directly. Mirrors pty.rs `build_command`.
function buildSpawn(program: string, extraArgs: string[]): { file: string; args: string[] } {
  if (process.platform === 'win32' && !program.toLowerCase().endsWith('.exe')) {
    return { file: 'cmd.exe', args: ['/C', program, ...extraArgs] };
  }
  return { file: program, args: extraArgs };
}

export const ptyHandlers: Record<string, Handler> = {
  // Open a local shell but do NOT flush its output until `pty_start` (see the
  // module header's subscribe-gate note). Returns the id + the shell launched.
  pty_open: async (args, ctx): Promise<{ id: string; shell: string }> => {
    const req = (args.req !== null && typeof args.req === 'object' ? args.req : {}) as PtyOpenReq;
    const cols = Math.max(1, Math.trunc(Number(req.cols)) || 80);
    const rows = Math.max(1, Math.trunc(Number(req.rows)) || 24);
    const shell = typeof req.shell === 'string' && req.shell.trim() !== '' ? req.shell : defaultShell();
    const cwd = typeof req.cwd === 'string' && req.cwd.trim() !== '' ? req.cwd : undefined;
    const extraArgs = Array.isArray(req.args) ? req.args.map(String) : [];
    const { file, args: spawnArgs } = buildSpawn(shell, extraArgs);

    // TERM so full-screen apps (vim, tmux, an agent TUI) and colour work; inject
    // the login-shell PATH so GUI-launched agent CLIs resolve.
    const env: Record<string, string> = {};
    for (const [k, v] of Object.entries(process.env)) if (v !== undefined) env[k] = v;
    env.TERM = 'xterm-256color';
    const lp = await loginPath();
    if (lp !== null) env.PATH = lp;

    const pty = await loadPty();
    const term = pty.spawn(file, spawnArgs, { name: 'xterm-256color', cols, rows, cwd, env });

    const id = `p${nextId}`;
    nextId += 1;
    const session: Session = { term, sender: ctx.sender, started: false, buffered: [], exited: undefined };

    term.onData((d: string) => {
      const buf = Buffer.from(d, 'utf8');
      if (!session.started) {
        session.buffered.push(buf);
        return;
      }
      emit(session.sender, 'pty-data', { id, bytes: buf });
    });
    term.onExit(({ exitCode }: { exitCode: number }) => {
      session.exited = exitCode ?? null;
      if (!session.started) return; // delivered by pty_start after the flush
      sessions.delete(id);
      emit(session.sender, 'pty-exit', { id, code: session.exited });
    });

    sessions.set(id, session);
    return { id, shell };
  },

  // Flush buffered output to the (now-listening) renderer and switch to live
  // emit. Idempotent: a second call, or one after the tab closed, is a no-op.
  pty_start: async (args): Promise<void> => {
    const id = String(args.id ?? '');
    const s = sessions.get(id);
    if (s === undefined || s.started) return;
    s.started = true;
    for (const buf of s.buffered) emit(s.sender, 'pty-data', { id, bytes: buf });
    s.buffered = [];
    if (s.exited !== undefined) {
      // The shell died before the listeners attached — report it now.
      sessions.delete(id);
      emit(s.sender, 'pty-exit', { id, code: s.exited });
    }
  },

  pty_write: async (args): Promise<void> => {
    const id = String(args.id ?? '');
    const data = String(args.data ?? '');
    const s = sessions.get(id);
    if (s === undefined) throw new Error('no such session');
    s.term.write(data);
  },

  pty_resize: async (args): Promise<void> => {
    const id = String(args.id ?? '');
    const cols = Math.max(1, Math.trunc(Number(args.cols)) || 1);
    const rows = Math.max(1, Math.trunc(Number(args.rows)) || 1);
    const s = sessions.get(id);
    if (s === undefined) throw new Error('no such session');
    s.term.resize(cols, rows);
  },

  pty_close: async (args): Promise<void> => {
    const id = String(args.id ?? '');
    const s = sessions.get(id);
    if (s === undefined) return; // already gone / never opened
    // Remove first; the ensuing `onExit` then finds no entry and is harmless.
    sessions.delete(id);
    try {
      s.term.kill();
    } catch {
      /* child already exited — nothing to kill */
    }
  },
};

/// Kill every live shell — wired to `before-quit` so closing the app never
/// orphans a child process. Best-effort.
export function disposeAllPtys(): void {
  for (const s of sessions.values()) {
    try {
      s.term.kill();
    } catch {
      /* already gone */
    }
  }
  sessions.clear();
}
