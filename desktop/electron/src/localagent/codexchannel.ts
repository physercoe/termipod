/// The byte path to a codex app-server (vision-parity **L4b**).
///
/// `codexattach.ts` decides WHICH rung; this module opens it and presents both
/// as one thing: a bidirectional stream of JSON-RPC frames. The driver above
/// (L4c) should not be able to tell whether it is talking to a child process or
/// to a shared daemon — that is the whole point of the abstraction, and it is
/// why `send`/`onFrame` are the entire surface.
///
/// ## Two transports, one contract
///
///  - **spawn** — `codex app-server` as a child. Its stdout is a *byte stream*
///    of newline-delimited JSON, so frames must be reassembled across chunk
///    boundaries (`LineBuffer`).
///  - **daemon** — a **WebSocket over a Unix domain socket**. WebSocket is a
///    message protocol, so each message arrives whole and no reassembly is
///    needed; a message may still contain more than one newline-separated
///    object, so it goes through the same decoder minus the buffering.
///
/// ## `perMessageDeflate: false` is load-bearing
///
/// Measured against codex-cli 0.147.0: `ws` offers `permessage-deflate` by
/// default, and the daemon **hangs up on the handshake** rather than declining
/// the extension — `socket hang up`, before `open`. Isolated against the live
/// daemon by varying one option at a time: deflate on fails with or without a
/// `Host` header, deflate off succeeds with or without it. So the option below
/// is the fix, and the `Host` header is not part of it.
///
/// ## Closing a daemon channel must not stop the daemon
///
/// The daemon rung exists so a session outlives us. `close()` therefore closes
/// **our socket** and nothing else; only an explicit `codex app-server daemon
/// stop` should end the shared server. The spawn rung is the opposite by
/// nature: its child dies with us, which is what `close()` does there.
import { spawn as nodeSpawn } from 'node:child_process';
import type { CodexAttachMode, CodexAttachPlan } from './codexattach.ts';

/// A parsed JSON-RPC frame. Deliberately untyped beyond "an object" — the
/// app-server vocabulary is large and versioned, and the frame profile is what
/// gives it meaning (see `frameprofile/`). Typing it here would be a second,
/// staler copy of the vendor's schema.
export type CodexFrame = Record<string, unknown>;

/// What one decode pass produced. `junk` is carried rather than dropped: a line
/// we cannot parse is a fact about the engine, and silently swallowing engine
/// output is precisely how E3 shipped a feature nobody could see.
export interface DecodeResult {
  frames: CodexFrame[];
  junk: string[];
}

/// Decode a COMPLETE unit of text (one WebSocket message, or one full line from
/// a stream) into frames. Pure. Empty and whitespace-only pieces are not junk —
/// they are just framing.
export function decodeMessage(text: string): DecodeResult {
  const frames: CodexFrame[] = [];
  const junk: string[] = [];
  for (const piece of text.split('\n')) {
    const trimmed = piece.trim();
    if (trimmed === '') continue;
    let parsed: unknown;
    try {
      parsed = JSON.parse(trimmed);
    } catch {
      junk.push(trimmed);
      continue;
    }
    if (parsed !== null && typeof parsed === 'object' && !Array.isArray(parsed)) {
      frames.push(parsed as CodexFrame);
    } else {
      junk.push(trimmed);
    }
  }
  return { frames, junk };
}

/// Reassemble newline-delimited JSON from a byte STREAM. A chunk may split a
/// frame anywhere, so the tail is held until its newline arrives. Pure, and
/// separate from `decodeMessage` because the WebSocket rung must NOT buffer: a
/// message with no trailing newline is complete, and holding it would stall the
/// channel forever.
export class LineBuffer {
  private tail = '';

  push(chunk: string): DecodeResult {
    const combined = this.tail + chunk;
    const lastBreak = combined.lastIndexOf('\n');
    if (lastBreak < 0) {
      this.tail = combined;
      return { frames: [], junk: [] };
    }
    this.tail = combined.slice(lastBreak + 1);
    return decodeMessage(combined.slice(0, lastBreak));
  }

  /// Whatever is left when the stream ends — a final frame with no trailing
  /// newline is still a frame.
  flush(): DecodeResult {
    const rest = this.tail;
    this.tail = '';
    return decodeMessage(rest);
  }
}

/// The `ws` URL for a Unix domain socket. `ws+unix://<socket path>:<pathname>`
/// is the form `ws` understands; the daemon serves the upgrade at `/`.
export function codexSocketUrl(socketPath: string): string {
  return `ws+unix://${socketPath}:/`;
}

/// Handshake options for the daemon rung. See the module note: the daemon hangs
/// up on a handshake that offers `permessage-deflate`.
export const CODEX_WS_OPTIONS = { perMessageDeflate: false } as const;

export interface CodexChannel {
  /// Which rung actually opened — not necessarily the one first planned, since
  /// a daemon that will not come up falls back to a spawned child.
  readonly mode: CodexAttachMode;
  /// A sentence naming the rung, for the session record and the UI.
  readonly reason: string;
  send(frame: CodexFrame): void;
  close(): void;
}

export interface CodexChannelHandlers {
  onFrame(frame: CodexFrame): void;
  /// The channel ended. `reason` is always a sentence; `code` is the WebSocket
  /// close code or the child's exit code when there is one.
  onClose(info: { code: number | null; reason: string }): void;
  /// Output that was not a JSON object. Optional, but offered so that engine
  /// chatter has somewhere to go other than the floor.
  onJunk?(line: string): void;
}

/// The `ws` surface this module uses, structurally, so a test can supply a fake
/// without the library or a socket.
export interface WsLike {
  on(event: 'open', cb: () => void): void;
  on(event: 'message', cb: (data: unknown) => void): void;
  on(event: 'error', cb: (err: Error) => void): void;
  on(event: 'close', cb: (code: number, reason: unknown) => void): void;
  send(data: string): void;
  close(): void;
}

type WsCtor = new (url: string, options: unknown) => WsLike;

export interface SpawnedProcess {
  stdout: NodeJS.ReadableStream;
  stderr: NodeJS.ReadableStream | null;
  stdin: NodeJS.WritableStream;
  kill(signal?: NodeJS.Signals): void;
  on(event: 'exit', cb: (code: number | null) => void): void;
}

export interface ChannelDeps {
  /// Open a WebSocket. Injected so the daemon rung is testable with no daemon.
  /// Async because the real one loads `ws` lazily — see `defaultDeps`.
  connect(url: string, options: typeof CODEX_WS_OPTIONS): Promise<WsLike>;
  /// Spawn the app-server child.
  spawn(bin: string, args: string[], opts: { cwd: string; env: NodeJS.ProcessEnv }): SpawnedProcess;
  /// Bring the shared daemon up. Resolves when it is running, rejects with a
  /// usable message when it will not start.
  startDaemon(argv: string[], opts: { env: NodeJS.ProcessEnv }): Promise<void>;
}

export interface OpenOptions {
  cwd: string;
  env?: NodeJS.ProcessEnv;
  /// How long to wait for the WebSocket to open before giving up on the daemon
  /// rung. A daemon that accepts the connection but never completes the upgrade
  /// would otherwise hang the session with no explanation.
  connectTimeoutMs?: number;
}

const DEFAULT_CONNECT_TIMEOUT_MS = 10_000;

/// Open the planned rung, falling back to a spawned child when the shared
/// daemon cannot be reached.
///
/// The fallback is **reported, never silent**: the returned channel's `mode`
/// and `reason` say which rung is live and why, because "your session is shared
/// with the codex TUI" and "your session dies with this window" are different
/// promises and the user is entitled to know which one they got.
export async function openCodexChannel(
  plan: CodexAttachPlan,
  handlers: CodexChannelHandlers,
  opts: OpenOptions,
  deps: ChannelDeps = defaultDeps(),
): Promise<CodexChannel> {
  const env = opts.env ?? process.env;
  if (plan.mode === 'daemon') {
    try {
      await deps.startDaemon(plan.startArgv, { env });
      return await openDaemonChannel(plan.socketPath, plan.reason, handlers, opts, deps);
    } catch (err) {
      const why = err instanceof Error ? err.message : String(err);
      const fallback: CodexAttachPlan = {
        mode: 'spawn',
        argv: [plan.startArgv[0] ?? 'codex', 'app-server'],
        reason: `the shared codex daemon could not be reached (${why}); running a per-session app server instead, so this session will not outlive the app`,
      };
      return openSpawnChannel(fallback, handlers, opts, deps);
    }
  }
  return openSpawnChannel(plan, handlers, opts, deps);
}

async function openDaemonChannel(
  socketPath: string,
  reason: string,
  handlers: CodexChannelHandlers,
  opts: OpenOptions,
  deps: ChannelDeps,
): Promise<CodexChannel> {
  const ws = await deps.connect(codexSocketUrl(socketPath), CODEX_WS_OPTIONS);
  return new Promise<CodexChannel>((resolve, reject) => {
    let opened = false;
    let settled = false;
    // A socket that fails after opening emits BOTH `error` and `close`, and a
    // driver told twice that its channel ended would tear the session down
    // twice. First one wins.
    let closeReported = false;
    const reportClose = (code: number | null, why: string): void => {
      if (!opened || closeReported) return;
      closeReported = true;
      handlers.onClose({ code, reason: why });
    };
    const timer = setTimeout(() => {
      if (settled) return;
      settled = true;
      try {
        ws.close();
      } catch {
        /* the socket is already gone; nothing to unwind */
      }
      reject(new Error(`timed out opening the app-server socket at ${socketPath}`));
    }, opts.connectTimeoutMs ?? DEFAULT_CONNECT_TIMEOUT_MS);

    ws.on('open', () => {
      opened = true;
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      resolve({
        mode: 'daemon',
        reason,
        send(frame) {
          ws.send(JSON.stringify(frame));
        },
        close() {
          // Our socket only — the daemon keeps running, which is the point.
          ws.close();
        },
      });
    });

    ws.on('message', (data) => {
      const { frames, junk } = decodeMessage(textOf(data));
      for (const f of frames) handlers.onFrame(f);
      for (const j of junk) handlers.onJunk?.(j);
    });

    ws.on('error', (err) => {
      if (!settled) {
        settled = true;
        clearTimeout(timer);
        reject(err);
        return;
      }
      reportClose(null, err.message);
    });

    ws.on('close', (code) => {
      clearTimeout(timer);
      if (!settled) {
        settled = true;
        reject(new Error(`app-server socket closed during handshake (code ${String(code)})`));
        return;
      }
      reportClose(code, `app-server socket closed (code ${String(code)})`);
    });
  });
}

function openSpawnChannel(
  plan: Extract<CodexAttachPlan, { mode: 'spawn' }>,
  handlers: CodexChannelHandlers,
  opts: OpenOptions,
  deps: ChannelDeps,
): CodexChannel {
  const [bin, ...args] = plan.argv;
  const child = deps.spawn(bin ?? 'codex', args, { cwd: opts.cwd, env: opts.env ?? process.env });
  const buffer = new LineBuffer();

  child.stdout.setEncoding?.('utf8');
  child.stdout.on('data', (chunk: string | Buffer) => {
    const { frames, junk } = buffer.push(typeof chunk === 'string' ? chunk : chunk.toString('utf8'));
    for (const f of frames) handlers.onFrame(f);
    for (const j of junk) handlers.onJunk?.(j);
  });

  // stderr is never protocol, but it MUST be drained: the child is spawned with
  // stderr piped, and codex writes WARNING lines there — an unread pipe blocks
  // the engine outright once the ~64 KiB buffer fills, a hang with no symptom.
  // The lines themselves go to onJunk (never onFrame, however JSON-shaped a log
  // line looks): engine complaints belong somewhere other than the floor.
  let errTail = '';
  const relayErrText = (text: string): void => {
    const pieces = (errTail + text).split('\n');
    errTail = pieces.pop() ?? '';
    for (const line of pieces) {
      const t = line.trim();
      if (t !== '') handlers.onJunk?.(t);
    }
  };
  child.stderr?.setEncoding?.('utf8');
  child.stderr?.on('data', (chunk: string | Buffer) => {
    relayErrText(typeof chunk === 'string' ? chunk : chunk.toString('utf8'));
  });

  child.on('exit', (code) => {
    const { frames, junk } = buffer.flush();
    for (const f of frames) handlers.onFrame(f);
    for (const j of junk) handlers.onJunk?.(j);
    if (errTail.trim() !== '') handlers.onJunk?.(errTail.trim());
    errTail = '';
    handlers.onClose({ code, reason: `codex app-server exited (code ${String(code)})` });
  });

  return {
    mode: 'spawn',
    reason: plan.reason,
    send(frame) {
      child.stdin.write(JSON.stringify(frame) + '\n');
    },
    close() {
      // Ending stdin is what retires a session; the kill is the backstop.
      try {
        child.stdin.end();
      } catch {
        /* already closed */
      }
      child.kill();
    },
  };
}

function textOf(data: unknown): string {
  if (typeof data === 'string') return data;
  if (data instanceof Uint8Array) return Buffer.from(data).toString('utf8');
  if (Array.isArray(data)) return data.map((d) => textOf(d)).join('');
  return String(data);
}

function defaultDeps(): ChannelDeps {
  return {
    async connect(url, options) {
      // Loaded lazily: `ws` is only needed for the daemon rung, and the common
      // install has no daemon rung at all. `ws` is external in esbuild.mjs, so
      // this stays a REAL dynamic import and the result is node's
      // cjs-module-lexer namespace rather than the raw `module.exports` — the
      // interop ssh2mod.ts documents at length. `ws` exports the class both as
      // `module.exports` and as `.WebSocket`, so normalize across all three
      // shapes rather than betting on one.
      const m = (await import('ws')) as unknown as {
        WebSocket?: WsCtor;
        default?: WsCtor & { WebSocket?: WsCtor };
      };
      const ctor = m.WebSocket ?? m.default?.WebSocket ?? m.default;
      if (typeof ctor !== 'function') {
        throw new Error('the `ws` module did not export a WebSocket constructor');
      }
      return new ctor(url, options);
    },
    spawn(bin, args, o) {
      return nodeSpawn(bin, args, { cwd: o.cwd, env: o.env, stdio: ['pipe', 'pipe', 'pipe'] }) as SpawnedProcess;
    },
    startDaemon(argv, o) {
      return new Promise<void>((resolve, reject) => {
        const [bin, ...rest] = argv;
        const p = nodeSpawn(bin ?? 'codex', rest, { env: o.env, stdio: ['ignore', 'pipe', 'pipe'] });
        let err = '';
        p.stderr?.setEncoding('utf8');
        p.stderr?.on('data', (d: string) => {
          err += d;
        });
        p.on('error', reject);
        p.on('exit', (code) => {
          if (code === 0) resolve();
          else reject(new Error(err.trim() || `\`codex app-server daemon start\` exited ${String(code)}`));
        });
      });
    },
  };
}
