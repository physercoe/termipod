/// One claude session as a child process (vision-parity L3a).
///
/// The port of `StdioDriver` (driver_stdio.go) to Electron main: own the child,
/// read its line-JSON stdout, translate each frame through the L2 interpreter,
/// and write user frames back on a stdin that stays open.
///
/// **stdin stays open, and that is the whole topology.** `--print` reads its
/// prompt from stdin and, with `--input-format stream-json`, keeps reading
/// after the first turn: probed against claude-code 2.1.220, a child fed two
/// prompts on one held-open stdin answered both and reported the same
/// `session_id` for each. That is what makes a *session* possible here rather
/// than a sequence of one-shot invocations, and it is why `stop()` ending stdin
/// is what retires a session.
///
/// The process is injected (`SpawnFn`) so the frame path can be tested without
/// an engine on the box — the same reason the Go driver takes a `ProcSpawner`.

import { spawn as nodeSpawn } from 'node:child_process';
import { applyProfile } from '../frameprofile/translate.ts';
import type { EmittedEvent } from '../frameprofile/types.ts';
import type { Family } from './families.ts';
import {
  buildInputFrame,
  buildLaunchArgs,
  ContextWindows,
  DEFAULT_TOOL_POSTURE,
  TurnClock,
  type InputKind,
  type InputPayload,
  type ToolPosture,
} from './claudewire.ts';

/// The child's streams, narrowed to what this module uses. Structural so a
/// test can supply plain Node streams.
export interface SpawnedChild {
  stdout: NodeJS.ReadableStream;
  stderr: NodeJS.ReadableStream | null;
  stdin: NodeJS.WritableStream;
  kill(signal?: NodeJS.Signals): void;
  on(event: 'exit', cb: (code: number | null, signal: NodeJS.Signals | null) => void): void;
}

export type SpawnFn = (
  bin: string,
  args: string[],
  opts: { cwd: string; env: NodeJS.ProcessEnv },
) => SpawnedChild;

/// An event on its way to the session log — kind/producer/payload, before the
/// log assigns it a `seq`.
export interface DriverEvent {
  kind: string;
  producer: string;
  payload: Record<string, unknown>;
}

export interface ClaudeChildOptions {
  family: Family;
  cwd: string;
  posture?: ToolPosture;
  model?: string;
  /// Resolved by `resolveConfigHome`. Passed to the child explicitly rather
  /// than inherited, so the root we believe it is using is the root it uses.
  configHome: string;
  /// Base environment for the child. The caller supplies it (main's
  /// `process.env`) rather than this module reading the global, so a test can
  /// drive a hermetic one.
  env: NodeJS.ProcessEnv;
  spawnFn?: SpawnFn;
  now?: () => Date;
  /// Pin the engine's session id rather than learning it from the init frame.
  /// See `LaunchOptions.sessionId` — this is what gives a session a resume
  /// handle before its first frame arrives.
  sessionId?: string;
  /// Resume tokens from the N1 recipe table. Present only on a rebind.
  resumeTokens?: readonly string[];
  onEvent: (ev: DriverEvent) => void;
  onExit?: (code: number | null, signal: NodeJS.Signals | null) => void;
}

/// Frames above this are refused rather than buffered. Tool results can be
/// large; 1 MiB matches the Go scanner's cap and keeps a malformed stream from
/// growing main's heap without bound.
export const MAX_FRAME_BYTES = 1 << 20;

export class ClaudeChild {
  readonly #opts: ClaudeChildOptions;
  readonly #turns = new TurnClock();
  readonly #windows = new ContextWindows();
  #child: SpawnedChild | null = null;
  #buffer = '';
  #overflowed = false;
  #started = false;
  #stopped = false;

  constructor(opts: ClaudeChildOptions) {
    this.#opts = opts;
  }

  get running(): boolean {
    return this.#child !== null && !this.#stopped;
  }

  /// The posture this session actually launched with. Recorded on the
  /// lifecycle event so a transcript states what the agent was allowed to do
  /// rather than leaving a reader to infer it.
  get posture(): ToolPosture {
    return this.#opts.posture ?? DEFAULT_TOOL_POSTURE;
  }

  start(): void {
    if (this.#started) return;
    this.#started = true;

    const resumeTokens = this.#opts.resumeTokens ?? [];
    const resumed = resumeTokens.length > 0;
    const args = buildLaunchArgs(this.#opts.family, {
      posture: this.posture,
      model: this.#opts.model,
      sessionId: this.#opts.sessionId,
      resumeTokens,
    });
    const spawnFn = this.#opts.spawnFn ?? defaultSpawn;
    const child = spawnFn(this.#opts.family.bin, args, {
      cwd: this.#opts.cwd,
      env: { ...this.#opts.env, CLAUDE_CONFIG_DIR: this.#opts.configHome },
    });
    this.#child = child;

    this.#emit('lifecycle', 'system', {
      phase: 'started',
      mode: 'M2',
      source: 'local',
      tool_posture: this.posture,
      cwd: this.#opts.cwd,
      // A rebind is a new process continuing an old conversation, and the
      // transcript has to say so: without this the reader sees a second
      // `started` in the middle of one conversation with no explanation, and
      // cannot tell a resumed session from a session that restarted cold —
      // which is exactly the difference that decides whether the agent still
      // knows what was said above.
      resumed,
    });

    child.stdout.setEncoding?.('utf-8');
    child.stdout.on('data', (chunk: string | Buffer) => {
      this.#consume(typeof chunk === 'string' ? chunk : chunk.toString('utf-8'));
    });

    // stderr is NOT parsed as frames. claude writes auth diagnostics and
    // warnings there, and feeding them to the JSON path would turn every one
    // into a `raw` transcript row. They ride a distinct kind so a reader can
    // see them without them pretending to be agent output.
    child.stderr?.setEncoding?.('utf-8');
    child.stderr?.on('data', (chunk: string | Buffer) => {
      const text = (typeof chunk === 'string' ? chunk : chunk.toString('utf-8')).trim();
      if (text !== '') this.#emit('error', 'system', { text, stream: 'stderr' });
    });

    child.on('exit', (code, signal) => {
      // A child that exits on its own (crash, auth failure, engine bug) is not
      // a stop() we performed, and the transcript has to distinguish them —
      // otherwise a session that died looks exactly like one the user closed.
      const expected = this.#stopped;
      this.#stopped = true;
      this.#emit('lifecycle', 'system', {
        phase: 'stopped',
        mode: 'M2',
        source: 'local',
        exit_code: code,
        signal,
        expected,
      });
      this.#opts.onExit?.(code, signal);
    });
  }

  /// Send one user-side input. `text` opens a turn; control inputs continue
  /// the one in flight.
  input(kind: InputKind, payload: InputPayload): void {
    const child = this.#child;
    if (child === null) throw new Error('local claude: session not started');
    if (this.#stopped) throw new Error('local claude: session has stopped');

    // Built before the turn marker so a malformed payload throws without
    // leaving a turn open that no reply will ever close.
    const frame = buildInputFrame(kind, payload);

    if (kind === 'text') {
      this.#emit('turn.start', 'agent', { turn_id: this.#turns.next(), ts: this.#nowIso() });
    }
    child.stdin.write(frame);
  }

  /// Retire the session: end stdin so the child drains its current turn, then
  /// signal it.
  ///
  /// Ending stdin first is the graceful half — the child treats EOF as "no
  /// more prompts" and exits after finishing what it has. The signal is the
  /// backstop for a child that does not.
  stop(signal: NodeJS.Signals = 'SIGTERM'): void {
    const child = this.#child;
    if (child === null || this.#stopped) return;
    this.#stopped = true;
    try {
      child.stdin.end();
    } catch {
      // A already-closed stdin is not a failure to report; the kill below is
      // what actually retires the process.
    }
    child.kill(signal);
  }

  // ── Frame path ─────────────────────────────────────────────────────────────

  #consume(chunk: string): void {
    this.#buffer += chunk;
    for (;;) {
      const nl = this.#buffer.indexOf('\n');
      if (nl < 0) break;
      const line = this.#buffer.slice(0, nl);
      this.#buffer = this.#buffer.slice(nl + 1);
      // A line that arrived only because an oversized one was discarded is
      // the REMAINDER of that oversized line, not a frame. Drop it and clear
      // the flag; the alternative is parsing a fragment as if it were whole.
      if (this.#overflowed) {
        this.#overflowed = false;
        continue;
      }
      this.#handleLine(line);
    }
    if (this.#buffer.length > MAX_FRAME_BYTES) {
      this.#emit('error', 'system', {
        text: `dropped a stream-json frame over ${MAX_FRAME_BYTES} bytes`,
        stream: 'stdout',
      });
      this.#buffer = '';
      this.#overflowed = true;
    }
  }

  #handleLine(line: string): void {
    const trimmed = line.trim();
    if (trimmed === '') return;
    let frame: unknown;
    try {
      frame = JSON.parse(trimmed);
    } catch {
      // Pass it through rather than dropping bytes: the Go driver's rule, and
      // for the same reason — a transcript that silently omits what it could
      // not parse is one nobody can debug.
      this.#emit('raw', 'agent', { text: trimmed });
      return;
    }
    if (frame === null || typeof frame !== 'object' || Array.isArray(frame)) {
      this.#emit('raw', 'agent', { text: trimmed });
      return;
    }
    const events: EmittedEvent[] = applyProfile(frame as Record<string, unknown>, this.#opts.family.frame_profile);
    for (const ev of events) {
      this.#emit(ev.kind, ev.producer, ev.payload ?? {});
    }
  }

  #emit(kind: string, producer: string, payload: Record<string, unknown>): void {
    this.#windows.apply(kind, payload);
    this.#opts.onEvent({ kind, producer, payload });
  }

  #nowIso(): string {
    return (this.#opts.now?.() ?? new Date()).toISOString();
  }
}

const defaultSpawn: SpawnFn = (bin, args, opts) =>
  nodeSpawn(bin, args, {
    cwd: opts.cwd,
    env: opts.env,
    stdio: ['pipe', 'pipe', 'pipe'],
  }) as unknown as SpawnedChild;
