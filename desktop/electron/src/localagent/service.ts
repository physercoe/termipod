/// The local agent service (vision-parity L3a, plan D-8).
///
/// One registry of live sessions in Electron main. Each session owns an engine
/// child and an append-only log; the renderer is a *client* of this, not its
/// owner, which is the whole point of the service topology — a session outlives
/// the view of it, so closing the Companion, switching agents, or reopening the
/// dock replays from the log instead of losing the turn.
///
/// **What this is not, yet.** D-8 asks for a session that outlives the *app*,
/// reached over a loopback WebSocket so other clients can attach. This ships
/// the service and its client-outliving half; the socket and the across-restart
/// half ride L3b, because both need the log to be on disk first and that is one
/// wedge's worth of work on its own. The shape here is chosen so that lands as
/// an addition rather than a rewrite: nothing below assumes its caller is the
/// renderer, and the log is already cursor-addressed.
///
/// Transport-agnostic on purpose — no `electron` import. `host.ts` is what
/// turns this into IPC handlers, and `service.test.ts` drives it directly.

import { randomUUID } from 'node:crypto';
import { ClaudeChild, type DriverEvent, type SpawnFn } from './claudechild.ts';
import { DEFAULT_TOOL_POSTURE, resolveConfigHome, type InputKind, type InputPayload, type ToolPosture } from './claudewire.ts';
import { familyByName, supportsLocalDriving, type Family } from './families.ts';
import { SessionLog, type LocalAgentEvent, type LogPage } from './log.ts';

/// What a client needs to know about a session without reading its transcript.
export interface SessionDescriptor {
  id: string;
  family: string;
  cwd: string;
  posture: ToolPosture;
  model?: string;
  status: 'running' | 'stopped';
  created_at: string;
  /// The engine's own session id, learned from its `session.init` frame. Absent
  /// until the child emits one. This is the handle a native resume needs
  /// (N1's recipe table), so L3b's rebind reads it from here.
  engine_session_id?: string;
}

export interface CreateOptions {
  family?: string;
  cwd: string;
  /// Omitted means `DEFAULT_TOOL_POSTURE`. A caller wanting `unrestricted` has
  /// to say so — see `claudewire.ts` for what that costs.
  posture?: ToolPosture;
  model?: string;
  /// Per-session override of claude's config root.
  configHome?: string;
}

export interface ServiceOptions {
  families: readonly Family[];
  env: NodeJS.ProcessEnv;
  homeDir: string;
  spawnFn?: SpawnFn;
  now?: () => Date;
  logCapacity?: number;
}

/// A live event, already assigned its `seq` by the session's log.
export type SessionListener = (sessionId: string, event: LocalAgentEvent) => void;

interface Session {
  desc: SessionDescriptor;
  log: SessionLog;
  child: ClaudeChild;
}

export class LocalAgentService {
  readonly #opts: ServiceOptions;
  readonly #sessions = new Map<string, Session>();
  readonly #listeners = new Set<SessionListener>();

  constructor(opts: ServiceOptions) {
    this.#opts = opts;
  }

  /// Families this build can drive locally. The dock's picker offers these and
  /// nothing else, so an engine we have no driver for is absent rather than
  /// offered-then-failing.
  ///
  /// The families themselves, not just their names: the composer's F3 gate
  /// resolves prompt modalities out of the registry, and a local session has no
  /// hub to ask for it.
  localFamilies(): Family[] {
    return this.#opts.families.filter((f) => supportsLocalDriving(f));
  }

  list(): SessionDescriptor[] {
    return [...this.#sessions.values()].map((s) => ({ ...s.desc }));
  }

  get(id: string): SessionDescriptor | undefined {
    const s = this.#sessions.get(id);
    return s === undefined ? undefined : { ...s.desc };
  }

  create(opts: CreateOptions): SessionDescriptor {
    const familyName = opts.family ?? 'claude-code';
    const family = familyByName(this.#opts.families, familyName);
    if (!supportsLocalDriving(family)) {
      throw new Error(`local agent service cannot drive family ${familyName}`);
    }
    if (opts.cwd.trim() === '') {
      throw new Error('local agent service: cwd is required');
    }

    const id = `local-${randomUUID()}`;
    const posture = opts.posture ?? DEFAULT_TOOL_POSTURE;
    const log = new SessionLog(this.#opts.logCapacity);
    const desc: SessionDescriptor = {
      id,
      family: familyName,
      cwd: opts.cwd,
      posture,
      model: opts.model,
      status: 'running',
      created_at: this.#nowIso(),
    };

    const child = new ClaudeChild({
      family: family as Family,
      cwd: opts.cwd,
      posture,
      model: opts.model,
      configHome: resolveConfigHome(opts.configHome, this.#opts.env, this.#opts.homeDir),
      env: this.#opts.env,
      spawnFn: this.#opts.spawnFn,
      now: this.#opts.now,
      onEvent: (ev) => this.#record(id, ev),
      onExit: () => {
        const s = this.#sessions.get(id);
        if (s !== undefined) s.desc.status = 'stopped';
      },
    });

    this.#sessions.set(id, { desc, log, child });
    // After the map entry exists: `start()` emits its lifecycle event
    // synchronously, and #record has to find the session to log it into.
    child.start();
    return { ...desc };
  }

  history(id: string, tail?: number): LogPage {
    return this.#require(id).log.tail(tail);
  }

  since(id: string, cursor: number): LogPage {
    return this.#require(id).log.since(cursor);
  }

  input(id: string, kind: InputKind, payload: InputPayload): void {
    this.#require(id).child.input(kind, payload);
  }

  stop(id: string): void {
    const s = this.#sessions.get(id);
    if (s === undefined) return;
    s.child.stop();
    s.desc.status = 'stopped';
  }

  /// Stop every session. Called on app quit, beside the other hosts'
  /// `disposeAll` — a child left running after the window closes is a claude
  /// process nobody can see and nobody will reap.
  disposeAll(): void {
    for (const id of [...this.#sessions.keys()]) this.stop(id);
  }

  /// Forget a stopped session. The log goes with it, so a client still holding
  /// a cursor gets a plain "no such session" rather than a silent empty page.
  forget(id: string): boolean {
    const s = this.#sessions.get(id);
    if (s === undefined) return false;
    if (s.desc.status === 'running') {
      throw new Error(`session ${id} is still running; stop it first`);
    }
    return this.#sessions.delete(id);
  }

  subscribe(listener: SessionListener): () => void {
    this.#listeners.add(listener);
    return () => this.#listeners.delete(listener);
  }

  // ── internals ──────────────────────────────────────────────────────────────

  #record(sessionId: string, ev: DriverEvent): void {
    const s = this.#sessions.get(sessionId);
    if (s === undefined) return;

    const row = s.log.append({
      id: randomUUID(),
      ts: this.#nowIso(),
      kind: ev.kind,
      producer: ev.producer,
      payload: ev.payload,
    });

    // The engine's own session id arrives once, on its init frame. Capturing
    // it here rather than making a client parse the transcript for it is what
    // lets a later rebind (L3b) look up a resume recipe by handle.
    if (ev.kind === 'session.init' && s.desc.engine_session_id === undefined) {
      const engineId = ev.payload.session_id;
      if (typeof engineId === 'string' && engineId !== '') s.desc.engine_session_id = engineId;
    }

    for (const listener of this.#listeners) {
      try {
        listener(sessionId, row);
      } catch {
        // One bad subscriber must not stop the others, and must not unwind
        // into the child's stdout handler — the transcript is already written.
      }
    }
  }

  #require(id: string): Session {
    const s = this.#sessions.get(id);
    if (s === undefined) throw new Error(`no local session ${id}`);
    return s;
  }

  #nowIso(): string {
    return (this.#opts.now?.() ?? new Date()).toISOString();
  }
}
