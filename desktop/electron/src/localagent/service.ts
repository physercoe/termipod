/// The local agent service (vision-parity L3a; durable half L3b, plan D-8).
///
/// One registry of sessions in Electron main. Each session owns a transcript on
/// disk and, when it is running, an engine child. The renderer is a *client* of
/// this, not its owner — which is the point of the service topology: a session
/// outlives the view of it, and since L3b it outlives the app.
///
/// **The two halves of surviving a restart, and why neither works alone.**
/// Measured against claude-code 2.1.220 rather than assumed:
///
///   - `--resume <id>` under the M2 pipe restores the ENGINE's memory. A
///     codeword set before the restart came back correctly, and the child kept
///     the same session id rather than forking a new one.
///   - It emits NO replay. Zero frames from the prior turns. The engine
///     remembers; it does not re-narrate.
///
/// So resume alone would give the director a blank transcript backed by an
/// agent that secretly knows things — strictly worse than a cold start, because
/// you cannot see what it is about to act on. `durablelog.ts` restores the view,
/// resume restores the memory, and a rebind does both or it is not worth doing.
///
/// **Rebind is lazy.** A reloaded session comes back `stopped` with its
/// transcript readable, and respawns on the first input. Eagerly respawning
/// every session at boot would start N engine children — real processes, real
/// tokens, possibly a rate limit — because the app was opened, which is not
/// something the director asked for.
///
/// Transport-agnostic on purpose — no `electron` import. `host.ts` turns this
/// into IPC handlers, and `service.test.ts` drives it directly.

import { randomUUID } from 'node:crypto';
import { ClaudeChild, type SpawnFn } from './claudechild.ts';
import { resolveConfigHome } from './claudewire.ts';
import type { CodexAttachPlan } from './codexattach.ts';
import type { ChannelDeps, openCodexChannel } from './codexchannel.ts';
import { CodexDriver } from './codexdriver.ts';
import {
  DEFAULT_TOOL_POSTURE,
  type DriverEvent,
  type InputKind,
  type InputPayload,
  type LocalDriver,
  type ToolPosture,
} from './driver.ts';
import { DurableSessionLog } from './durablelog.ts';
import { familyByName, localEngine, supportsLocalDriving, type Family } from './families.ts';
import { type LocalAgentEvent, type LogPage } from './log.ts';
import { newSessionID, resumeSplice, type ResumeTable } from './resumerecipes.ts';
import {
  listSessionMetas,
  removeSessionDir,
  sessionPaths,
  writeSessionMeta,
  type PersistedSession,
} from './store.ts';

/// What a client needs to know about a session without reading its transcript.
export interface SessionDescriptor {
  id: string;
  family: string;
  cwd: string;
  posture: ToolPosture;
  model?: string;
  status: 'running' | 'stopped';
  created_at: string;
  /// The engine's own session id — the handle a reattach is keyed on.
  ///
  /// **How it arrives differs by engine, and that is a measured difference,
  /// not a style choice.** claude honours `--session-id <uuid>`, so since L3b
  /// its handle is ASSIGNED at create time and exists from the moment the
  /// session does — closing the window where a child that died before its
  /// first frame left an unreattachable transcript. codex mints its own thread
  /// id and takes no equivalent flag, so a codex session has no handle until
  /// `thread/started` reports one. Assigning a UUID anyway would write down a
  /// thread id codex has never heard of, and a later `thread/resume` would
  /// fail against it.
  ///
  /// The engine's own report wins on both paths: it is what a reattach will
  /// look up.
  engine_session_id?: string;
  /// True for a session read back off disk that has not been rebound yet. The
  /// transcript is readable; the engine is not attached.
  restored?: boolean;
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
  /// Where session directories live — the app's userData root.
  dataDir: string;
  /// The N1 resume table, for rebinding.
  resumeTable: ResumeTable;
  /// `process.platform`; injected so a test can ask what Windows would do.
  platform?: string;
  spawnFn?: SpawnFn;
  now?: () => Date;
  logCapacity?: number;
  /// Overrides for the codex rung, injected so a test can drive a codex
  /// session with no app-server on the box. Production leaves them absent and
  /// the driver resolves the rung itself.
  codex?: {
    plan?: CodexAttachPlan;
    deps?: ChannelDeps;
    openChannel?: typeof openCodexChannel;
    flushIntervalMs?: number;
    /// Per-thread codex config overrides (`thread/start.config`).
    config?: Record<string, unknown>;
    exists?: (p: string) => boolean;
  };
}

/// A live event, already assigned its `seq` by the session's log.
export type SessionListener = (sessionId: string, event: LocalAgentEvent) => void;

/// What `reload()` found on disk.
export interface ReloadReport {
  restored: string[];
  /// Sessions whose descriptor read but whose transcript did not. Reported
  /// rather than silently reopened at seq 1 — see `durablelog.ts` on why that
  /// refusal is what keeps the cursor honest without an epoch.
  unreadable: string[];
  /// Directories whose descriptor itself was unreadable.
  skipped: string[];
}

interface Session {
  desc: SessionDescriptor;
  log: DurableSessionLog;
  driver: LocalDriver | null;
  /// The resolved claude config root, kept so a rebind spawns against the same
  /// one the session was created against. Unused by codex, which is configured
  /// per thread through `thread/start.config` rather than by a config root.
  configHome: string;
  /// Whether the CURRENT driver has emitted its init frame yet. Reset on every
  /// spawn — see `#record` for the distinction this draws.
  initSeen: boolean;
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

  /// Read persisted sessions back off disk. Call once, at service construction.
  ///
  /// Restored sessions arrive `stopped` — every session in a file was written
  /// by a process that is no longer running, so a persisted `running` would
  /// describe a child that died with its parent.
  reload(): ReloadReport {
    const listing = listSessionMetas(this.#opts.dataDir);
    const report: ReloadReport = { restored: [], unreadable: [], skipped: listing.skipped };

    for (const meta of listing.sessions) {
      if (this.#sessions.has(meta.id)) continue;
      const { dir } = sessionPaths(this.#opts.dataDir, meta.id);
      let log: DurableSessionLog;
      try {
        ({ log } = DurableSessionLog.open(dir, { capacity: this.#opts.logCapacity }));
      } catch {
        report.unreadable.push(meta.id);
        continue;
      }
      this.#sessions.set(meta.id, {
        desc: {
          id: meta.id,
          family: meta.family,
          cwd: meta.cwd,
          posture: meta.posture,
          model: meta.model,
          status: 'stopped',
          created_at: meta.created_at,
          engine_session_id: meta.engine_session_id,
          restored: true,
        },
        log,
        driver: null,
        configHome: meta.config_home ?? resolveConfigHome(undefined, this.#opts.env, this.#opts.homeDir),
        initSeen: false,
      });
      report.restored.push(meta.id);
    }
    return report;
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
    const engine = localEngine(family);
    if (engine === null) {
      throw new Error(`local agent service cannot drive family ${familyName}`);
    }
    if (opts.cwd.trim() === '') {
      throw new Error('local agent service: cwd is required');
    }

    const id = `local-${randomUUID()}`;
    const posture = opts.posture ?? DEFAULT_TOOL_POSTURE;
    const configHome = resolveConfigHome(opts.configHome, this.#opts.env, this.#opts.homeDir);
    const desc: SessionDescriptor = {
      id,
      family: familyName,
      cwd: opts.cwd,
      posture,
      model: opts.model,
      status: 'running',
      created_at: this.#nowIso(),
      // claude's handle is assigned rather than awaited (`--session-id`,
      // probed on 2.1.220); codex mints its own thread id and takes no such
      // flag, so its handle stays absent until `thread/started` reports one.
      // See `SessionDescriptor.engine_session_id`.
      ...(engine === 'claude-code' ? { engine_session_id: randomUUID() } : {}),
    };

    const { dir } = sessionPaths(this.#opts.dataDir, id);
    const log = DurableSessionLog.create(dir, { capacity: this.#opts.logCapacity });
    // Written BEFORE the driver starts: a descriptor that appeared only after
    // a successful launch would leave a crashed first launch with a transcript
    // nobody can attribute.
    this.#persist(desc, configHome);

    this.#sessions.set(id, { desc, log, driver: null, configHome, initSeen: false });
    this.#spawn(id, undefined);
    return { ...desc };
  }

  history(id: string, tail?: number): LogPage {
    return this.#require(id).log.tail(tail);
  }

  since(id: string, cursor: number): LogPage {
    return this.#require(id).log.since(cursor);
  }

  /// Send input, rebinding a restored session first.
  ///
  /// The rebind is here rather than in an explicit verb because this is the
  /// moment the director actually needs the engine. A separate `resume()` the
  /// UI had to remember to call would be one more way to end up typing into a
  /// session that is not listening.
  input(id: string, kind: InputKind, payload: InputPayload): void {
    const s = this.#require(id);
    if (s.driver === null || !s.driver.running) this.rebind(id);
    const driver = this.#require(id).driver;
    if (driver === null) throw new Error(`local session ${id} is not running`);
    // Record the director's own turn BEFORE sending it. Two reasons, and the
    // first is why this exists at all:
    //
    //   - The transcript is otherwise half a conversation. Everything else in
    //     this log is translated from an engine frame, and the engine does not
    //     echo the prompt back — so without this row the Companion shows the
    //     agent's replies and none of the questions. L3a did not notice because
    //     nothing re-read the log; making it durable is what made the gap
    //     visible, and it was already wrong for anyone who closed the dock and
    //     reopened it.
    //   - `input.<kind>` with producer `user` is the hub's own shape
    //     (handlers_agent_input.go), which the feed lens and EventCard already
    //     render as a user card. Inventing a local-only kind would have been a
    //     second vocabulary for the same thing.
    //
    // Before rather than after, because the prompt is the cause of the turn the
    // child is about to open, and a reader sorting by seq should see it that way.
    this.#record(id, { kind: `input.${kind}`, producer: 'user', payload: inputEventPayload(payload) });
    driver.input(kind, payload);
  }

  /// Restart a stopped session's engine, reattached to its conversation.
  ///
  /// Throws when there is no handle to resume against. It does NOT throw when
  /// the engine rejects the handle: claude exits 1 with `No conversation found
  /// with session ID`, which arrives as an `error` row and a
  /// `lifecycle{expected:false}` in the transcript. That is the honest place
  /// for it — the reader sees the session failed to reattach, rather than an
  /// exception surfacing somewhere that cannot say which session it was about.
  ///
  /// **The mechanism is per family, and the N1 table is what says which.**
  /// claude reattaches by argv (`--resume <id>` spliced from the recipe);
  /// codex reattaches by RPC (`thread/resume`), which is exactly what that
  /// table's codex row declares — `mechanism: appserver_thread_resume`, with a
  /// note recording that the `codex resume <id>` CLI row is a different rung.
  /// So the argv is built here only for the families whose mechanism IS argv,
  /// and the rest hand their handle to a driver that knows what to do with it.
  rebind(id: string): SessionDescriptor {
    const s = this.#require(id);
    if (s.driver !== null && s.driver.running) return { ...s.desc };

    const handle = s.desc.engine_session_id;
    if (handle === undefined || handle === '') {
      throw new Error(`local session ${id} has no engine session id to resume`);
    }
    // Screened before it is used, on both paths: the handle arrives from the
    // engine and is read back off disk, so neither its length nor its bytes
    // are ours to assume.
    if (newSessionID(handle) === null) {
      throw new Error(`local session ${id} has an unusable engine session id`);
    }

    this.#spawn(id, handle);
    return { ...this.#require(id).desc };
  }

  stop(id: string): void {
    const s = this.#sessions.get(id);
    if (s === undefined) return;
    s.driver?.stop();
    s.desc.status = 'stopped';
    // Flush before the process can go away. The batching in `durablelog` is a
    // performance choice, not a durability one, and this is where that has to
    // be true.
    s.log.flush();
  }

  /// Stop every session. Called on app quit, beside the other hosts'
  /// `disposeAll` — a child left running after the window closes is an engine
  /// process nobody can see and nobody will reap. (On codex's daemon rung this
  /// closes our socket only: that rung exists so the thread outlives us.)
  disposeAll(): void {
    for (const id of [...this.#sessions.keys()]) this.stop(id);
  }

  /// Forget a stopped session — from the registry and from disk.
  ///
  /// Deleting the directory is the point: `forget` is how the director says
  /// "this is over", and leaving the transcript behind would mean it silently
  /// reappears at the next launch.
  forget(id: string): boolean {
    const s = this.#sessions.get(id);
    if (s === undefined) return false;
    if (s.desc.status === 'running' && s.driver !== null && s.driver.running) {
      throw new Error(`session ${id} is still running; stop it first`);
    }
    s.log.close();
    this.#sessions.delete(id);
    removeSessionDir(this.#opts.dataDir, id);
    return true;
  }

  subscribe(listener: SessionListener): () => void {
    this.#listeners.add(listener);
    return () => this.#listeners.delete(listener);
  }

  // ── internals ──────────────────────────────────────────────────────────────

  /// Start (or restart) a session's engine.
  ///
  /// `handle` absent means a fresh conversation; present means a rebind
  /// against the engine's own session id.
  #spawn(id: string, handle: string | undefined): void {
    const s = this.#require(id);
    const family = familyByName(this.#opts.families, s.desc.family);
    const engine = localEngine(family);
    if (engine === null) {
      throw new Error(`local agent service cannot drive family ${s.desc.family}`);
    }
    const onExit = (): void => {
      const cur = this.#sessions.get(id);
      if (cur !== undefined) {
        cur.desc.status = 'stopped';
        cur.log.flush();
      }
    };

    const driver =
      engine === 'codex'
        ? this.#codexDriver(id, family as Family, s, handle, onExit)
        : this.#claudeDriver(id, family as Family, s, handle, onExit);

    s.driver = driver;
    s.desc.status = 'running';
    s.desc.restored = false;
    s.initSeen = false;
    // After the field is set: `start()` emits its lifecycle event
    // synchronously, and #record has to find the session to log it into.
    driver.start();
  }

  #claudeDriver(
    id: string,
    family: Family,
    s: Session,
    handle: string | undefined,
    onExit: () => void,
  ): LocalDriver {
    let resumeTokens: string[] | undefined;
    if (handle !== undefined) {
      const ref = newSessionID(handle);
      // `rebind` already screened it; this narrows the type without a cast,
      // and would catch an internal caller that skipped the check.
      if (ref === null) throw new Error(`local session ${id} has an unusable engine session id`);
      const splice = resumeSplice(
        this.#opts.resumeTable,
        s.desc.family,
        ref,
        this.#opts.platform ?? process.platform,
      );
      if (!splice.ok) throw new Error(`local session ${id} cannot resume: ${splice.error}`);
      resumeTokens = splice.tokens;
    }
    return new ClaudeChild({
      family,
      cwd: s.desc.cwd,
      posture: s.desc.posture,
      model: s.desc.model,
      configHome: s.configHome,
      env: this.#opts.env,
      spawnFn: this.#opts.spawnFn,
      now: this.#opts.now,
      // `--resume` names an existing conversation and `--session-id` names a
      // new one, so exactly one of them is ever passed.
      sessionId: handle === undefined ? s.desc.engine_session_id : undefined,
      resumeTokens,
      onEvent: (ev) => this.#record(id, ev),
      onExit,
    });
  }

  #codexDriver(
    id: string,
    family: Family,
    s: Session,
    handle: string | undefined,
    onExit: () => void,
  ): LocalDriver {
    const codex = this.#opts.codex ?? {};
    return new CodexDriver({
      family,
      cwd: s.desc.cwd,
      posture: s.desc.posture,
      model: s.desc.model,
      env: this.#opts.env,
      homeDir: this.#opts.homeDir,
      ...(handle !== undefined ? { resumeThreadId: handle } : {}),
      ...(codex.config !== undefined ? { config: codex.config } : {}),
      ...(codex.plan !== undefined ? { plan: codex.plan } : {}),
      ...(codex.deps !== undefined ? { channelDeps: codex.deps } : {}),
      ...(codex.openChannel !== undefined ? { openChannel: codex.openChannel } : {}),
      ...(codex.flushIntervalMs !== undefined ? { flushIntervalMs: codex.flushIntervalMs } : {}),
      ...(codex.exists !== undefined ? { exists: codex.exists } : {}),
      now: this.#opts.now,
      onEvent: (ev) => this.#record(id, ev),
      onExit,
    });
  }

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

    // The engine's own id, taken from the FIRST init frame of each child and
    // never a later one. Two different things emit `session.init` and they need
    // opposite treatment:
    //
    //   - The first, right after a spawn, reports the id this child actually
    //     has. We passed `--session-id`, but the engine is authoritative — it
    //     is what `--resume` will look up — so if a build ever ignored the flag
    //     we would otherwise persist a handle that resolves to nothing.
    //   - A later one, mid-session, is claude re-initialising (a `/clear`).
    //     That id belongs to a conversation this transcript is not a record of,
    //     and adopting it would point a rebind at the wrong history.
    //
    // Hence the per-child flag rather than "capture once per session": a rebind
    // spawns a new child, whose first init is again the authoritative one.
    if (ev.kind === 'session.init' && !s.initSeen) {
      s.initSeen = true;
      const engineId = ev.payload.session_id;
      if (typeof engineId === 'string' && engineId !== '' && engineId !== s.desc.engine_session_id) {
        s.desc.engine_session_id = engineId;
        this.#persist(s.desc, s.configHome);
      }
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

  #persist(desc: SessionDescriptor, configHome: string): void {
    const meta: PersistedSession = {
      id: desc.id,
      family: desc.family,
      cwd: desc.cwd,
      posture: desc.posture,
      created_at: desc.created_at,
      config_home: configHome,
    };
    if (desc.model !== undefined && desc.model !== '') meta.model = desc.model;
    if (desc.engine_session_id !== undefined && desc.engine_session_id !== '') {
      meta.engine_session_id = desc.engine_session_id;
    }
    writeSessionMeta(this.#opts.dataDir, meta);
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

/// The payload for a director-side input row, in the hub's field names.
///
/// `body` rather than `text` because that is what `handlers_agent_input.go`
/// writes; `EventCard` reads `text ?? body`, so either renders, and matching
/// the hub is what keeps one vocabulary across both producers.
///
/// Attachments cross as `images` with `mime_type`/`data` — the shape
/// `InputImages` already reads, so a pasted screenshot appears in a local
/// transcript the same way it does in a hub one. PDFs are carried but not
/// rendered inline by any card today; they are recorded so the transcript does
/// not silently omit that something was attached.
function inputEventPayload(payload: InputPayload): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  if (payload.body !== undefined && payload.body !== '') out.body = payload.body;
  if (payload.images !== undefined && payload.images.length > 0) {
    out.images = payload.images.map((a) => ({
      mime_type: a.mime,
      data: a.data,
      ...(a.filename !== undefined ? { filename: a.filename } : {}),
    }));
  }
  if (payload.pdfs !== undefined && payload.pdfs.length > 0) {
    out.pdfs = payload.pdfs.map((a) => ({
      mime_type: a.mime,
      data: a.data,
      ...(a.filename !== undefined ? { filename: a.filename } : {}),
    }));
  }
  if (payload.request_id !== undefined) out.request_id = payload.request_id;
  if (payload.decision !== undefined) out.decision = payload.decision;
  if (payload.note !== undefined) out.note = payload.note;
  if (payload.reason !== undefined) out.reason = payload.reason;
  return out;
}
