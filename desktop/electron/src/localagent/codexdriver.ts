/// One codex session over the app-server protocol (vision-parity **L4c**).
///
/// L4a chose the rung, L4b opened the byte path, and this is what finally
/// speaks: a JSON-RPC client that turns `turn/start` into a transcript and a
/// director's click into a JSON-RPC response. It is the codex counterpart of
/// `claudechild.ts`, and `service.ts` holds either behind `LocalDriver` without
/// knowing which.
///
/// ## What is NOT here, deliberately
///
///  - **A second frame vocabulary.** Notifications go through the L2
///    interpreter with the hub's own `codex` frame profile, which ships to the
///    desktop in `agent_families.generated.json`. `thread/started` →
///    `session.init`, `item/completed` → `text` / `tool_result`, and so on are
///    *data*, already written and already drift-tested against the hub.
///  - **An attention table.** A local session has no hub, so a parked approval
///    surfaces as an `approval_request` event and is answered by R1's inline
///    cards — which is precisely what R1's own comment said would happen.
///  - **A written `.codex/config.toml`.** `thread/start` takes a `config` map,
///    so a session's overrides never become a file in the director's project.
///
/// ## Two things the transcript has to be honest about
///
///  1. **Which rung opened.** "shared with your codex TUI" and "dies with this
///     window" are different promises, so the channel's own sentence is
///     recorded as a `system` row rather than being inferred from silence.
///  2. **What the agent may actually do.** The lifecycle row carries the
///     posture AND the sandbox/approval policy it lowered to, because a name
///     is not a boundary — see `codexPosture` for the probe that measured it.
///
/// ## Async start behind a synchronous door
///
/// Opening the channel and running `initialize → thread/start` is a multi-step
/// async handshake, but `LocalDriver.start()` returns void: the service must be
/// able to hand back a session descriptor immediately. So input sent before the
/// thread exists is QUEUED and flushed when it opens, and a handshake that
/// fails reports an `error` row plus `lifecycle{phase:stopped}` — the same
/// place a failed claude launch already reports, rather than an exception
/// thrown somewhere that cannot say which session it was about.

import fs from 'node:fs';
import { applyProfile } from '../frameprofile/translate.ts';
import type { EmittedEvent } from '../frameprofile/types.ts';
import {
  codexBinary,
  managedCodexPath,
  codexHome,
  planCodexAttach,
  type CodexAttachPlan,
} from './codexattach.ts';
import {
  openCodexChannel,
  type ChannelDeps,
  type CodexChannel,
  type CodexFrame,
} from './codexchannel.ts';
import {
  AGENT_MESSAGE_DELTA,
  COMMAND_OUTPUT_DELTA,
  CODEX_CLIENT_INFO,
  MAX_STREAMED_OUTPUT_BYTES,
  askEventPayload,
  askResult,
  buildTurnInput,
  codexPosture,
  isDeltaMethod,
  parseServerAsk,
  refuseResult,
  threadResumeParams,
  threadStartParams,
  trimToTail,
  type CodexAsk,
} from './codexwire.ts';
import {
  DEFAULT_TOOL_POSTURE,
  type DriverEvent,
  type InputKind,
  type InputPayload,
  type LocalDriver,
  type ToolPosture,
} from './driver.ts';
import type { Family } from './families.ts';

export interface CodexDriverOptions {
  family: Family;
  cwd: string;
  posture?: ToolPosture;
  model?: string;
  /// Base environment. Supplied by the caller rather than read from the global
  /// so a test can drive a hermetic one.
  env: NodeJS.ProcessEnv;
  homeDir: string;
  /// An existing codex thread to reattach to. Absent for a fresh session.
  ///
  /// This is a THREAD id, not an argv token. N1's table says so itself: the
  /// codex family's mechanism is `appserver_thread_resume`, and its note
  /// records that the `codex resume <id>` CLI row is "the spawn-per-session
  /// fallback rung … not what the hub drives today". So the plan's line about
  /// taking resume argv from the N1 table does not describe this rung — the
  /// table it points at is the one that says to use an RPC instead.
  resumeThreadId?: string;
  /// Per-thread codex config overrides, merged over `config.toml` by codex.
  config?: Record<string, unknown>;
  /// Pre-resolved rung. Injected by tests; production computes it.
  plan?: CodexAttachPlan;
  channelDeps?: ChannelDeps;
  /// Injected so a test can open a fake channel.
  openChannel?: typeof openCodexChannel;
  exists?: (p: string) => boolean;
  now?: () => Date;
  /// Throttle for streaming deltas. Negative disables streaming entirely.
  flushIntervalMs?: number;
  onEvent: (ev: DriverEvent) => void;
  onExit?: (code: number | null) => void;
}

const DEFAULT_FLUSH_MS = 200;

interface Parked {
  jsonrpcId: number | string;
  ask: CodexAsk;
}

interface StreamBuffer {
  text: string;
  timer: ReturnType<typeof setTimeout> | null;
}

export class CodexDriver implements LocalDriver {
  readonly #opts: CodexDriverOptions;
  readonly #pending = new Map<number, (frame: CodexFrame) => void>();
  readonly #parked = new Map<string, Parked>();
  readonly #messageBuffers = new Map<string, StreamBuffer>();
  readonly #outputBuffers = new Map<string, StreamBuffer>();
  /// Text inputs that arrived before the thread was open.
  readonly #queue: InputPayload[] = [];
  #channel: CodexChannel | null = null;
  #nextId = 0;
  /// Bumped on every channel open so a card left on screen across a rebind
  /// cannot answer a request that merely reached the same JSON-RPC id.
  #epoch = 0;
  #threadId = '';
  #turnId = '';
  #started = false;
  #stopped = false;
  #ready = false;

  constructor(opts: CodexDriverOptions) {
    this.#opts = opts;
  }

  get running(): boolean {
    return this.#started && !this.#stopped;
  }

  get posture(): ToolPosture {
    return this.#opts.posture ?? DEFAULT_TOOL_POSTURE;
  }

  /// The codex thread this session is bound to — the handle `thread/resume`
  /// takes. Empty until the handshake completes.
  get threadId(): string {
    return this.#threadId;
  }

  start(): void {
    if (this.#started) return;
    this.#started = true;

    const posture = codexPosture(this.posture);
    this.#emit('lifecycle', 'system', {
      phase: 'started',
      mode: 'M2',
      source: 'local',
      engine: 'codex-app-server',
      tool_posture: this.posture,
      // What the posture actually LOWERED to. A reader should not have to know
      // our mapping table to know whether this agent can write to their disk.
      sandbox: posture.sandbox,
      approval_policy: posture.approvalPolicy,
      ...(posture.note !== undefined ? { posture_note: posture.note } : {}),
      cwd: this.#opts.cwd,
      resumed: this.#opts.resumeThreadId !== undefined && this.#opts.resumeThreadId !== '',
    });

    void this.#open().catch((err: unknown) => {
      this.#fail(err instanceof Error ? err.message : String(err));
    });
  }

  input(kind: InputKind, payload: InputPayload): void {
    if (!this.#started) throw new Error('local codex: session not started');
    if (this.#stopped) throw new Error('local codex: session has stopped');

    switch (kind) {
      case 'text': {
        // Built before anything is queued or sent so a payload codex cannot
        // carry (a PDF) throws at the call site instead of failing later, out
        // of sight, against a turn the director thinks is running.
        const input = buildTurnInput(payload);
        if (!this.#ready) {
          this.#queue.push(payload);
          return;
        }
        void this.#startTurn(input);
        return;
      }
      case 'approval':
      case 'answer': {
        const requestId = payload.request_id ?? '';
        const choice = kind === 'approval' ? payload.decision ?? '' : payload.body ?? '';
        if (requestId === '' || choice === '') {
          throw new Error(`local codex: ${kind} missing request_id/${kind === 'approval' ? 'decision' : 'body'}`);
        }
        this.#answerParked(requestId, choice);
        return;
      }
      case 'cancel': {
        void this.#cancel(payload.reason ?? '');
        return;
      }
    }
  }

  stop(): void {
    if (!this.#started || this.#stopped) return;
    this.#stopped = true;
    this.#clearTimers();
    // Refuse anything still parked BEFORE the socket goes: an unanswered
    // JSON-RPC request leaves the thread waiting on a client that no longer
    // exists, and on the daemon rung that thread outlives us.
    this.#refuseAllParked('the Companion closed this session');
    try {
      this.#channel?.close();
    } catch {
      // An already-closed channel is not a failure to report.
    }
    this.#channel = null;
    this.#emit('lifecycle', 'system', {
      phase: 'stopped',
      mode: 'M2',
      source: 'local',
      engine: 'codex-app-server',
      expected: true,
    });
    this.#opts.onExit?.(null);
  }

  // ── Opening ────────────────────────────────────────────────────────────────

  async #open(): Promise<void> {
    const plan = this.#opts.plan ?? this.#planRung();
    const open = this.#opts.openChannel ?? openCodexChannel;
    const channel = await open(
      plan,
      {
        onFrame: (frame) => this.#onFrame(frame),
        onClose: (info) => this.#onClose(info),
        onJunk: (line) => this.#emit('error', 'system', { text: line, stream: 'stderr' }),
      },
      { cwd: this.#opts.cwd, env: this.#opts.env },
      this.#opts.channelDeps,
    );
    if (this.#stopped) {
      channel.close();
      return;
    }
    this.#channel = channel;
    this.#epoch += 1;
    // The rung is a fact about what this session IS, so it is a row, not a log
    // line: on `daemon` the thread survives the app and shows up in the codex
    // TUI; on `spawn` it dies with this window.
    this.#emit('system', 'system', {
      kind: 'codex_channel',
      channel: channel.mode,
      reason: channel.reason,
    });
    await this.#handshake();
  }

  #planRung(): CodexAttachPlan {
    const exists = this.#opts.exists ?? fs.existsSync;
    const home = this.#opts.homeDir;
    return planCodexAttach(
      home,
      {
        managedInstallPresent: exists(managedCodexPath(codexHome(home, this.#opts.env))),
        bin: codexBinary(home, this.#opts.env, exists),
      },
      this.#opts.env,
    );
  }

  async #handshake(): Promise<void> {
    await this.#call('initialize', {
      clientInfo: { ...CODEX_CLIENT_INFO },
      capabilities: { experimentalApi: false },
    });
    // The protocol's one client notification. Send-and-forget: app-server
    // treats it as confirmation and answers nothing.
    this.#notify('initialized', null);

    const threadOpts = {
      cwd: this.#opts.cwd,
      posture: this.posture,
      ...(this.#opts.model !== undefined ? { model: this.#opts.model } : {}),
      ...(this.#opts.config !== undefined ? { config: this.#opts.config } : {}),
    };
    const resume = this.#opts.resumeThreadId ?? '';
    const result = resume !== ''
      ? await this.#call('thread/resume', threadResumeParams(resume, threadOpts))
      : await this.#call('thread/start', threadStartParams(threadOpts));

    const thread = asRecord(result.thread);
    const id = thread === undefined ? '' : asString(thread.id);
    if (id === '') {
      throw new Error('codex app-server returned no thread id');
    }
    this.#threadId = id;

    if (resume !== '') {
      // A fresh start gets its `session.init` from the profile's
      // `thread/started` rule. A RESUME emits no such notification (measured:
      // the only notifications after `thread/resume` are configWarning,
      // remoteControl/status, thread/status, tokenUsage and goal/cleared), so
      // without this a reattached session would have no init row and the
      // service would have nothing to re-confirm its handle from.
      this.#emit('session.init', 'agent', {
        session_id: id,
        engine: 'codex',
        model: asString(result.model),
        cwd: asString(result.cwd) || this.#opts.cwd,
        resumed: true,
      });
    }

    this.#ready = true;
    const queued = this.#queue.splice(0, this.#queue.length);
    for (const payload of queued) {
      await this.#startTurn(buildTurnInput(payload));
    }
  }

  #fail(message: string): void {
    if (this.#stopped) return;
    this.#stopped = true;
    this.#clearTimers();
    this.#emit('error', 'system', { text: message, stream: 'handshake' });
    this.#emit('lifecycle', 'system', {
      phase: 'stopped',
      mode: 'M2',
      source: 'local',
      engine: 'codex-app-server',
      expected: false,
    });
    this.#opts.onExit?.(null);
  }

  // ── Turns ──────────────────────────────────────────────────────────────────

  async #startTurn(input: Record<string, unknown>[]): Promise<void> {
    try {
      const result = await this.#call('turn/start', { threadId: this.#threadId, input });
      const turn = asRecord(result.turn);
      if (turn !== undefined) this.#turnId = asString(turn.id);
    } catch (err) {
      this.#emit('error', 'system', {
        text: err instanceof Error ? err.message : String(err),
        stream: 'turn',
      });
    }
  }

  async #cancel(reason: string): Promise<void> {
    // Unblock anything parked first. `turn/interrupt` aborts in-flight tool
    // calls, but a parked JSON-RPC id stays open until we write a response —
    // so an interrupt with a gate still held leaves the next turn stuck behind
    // it.
    this.#refuseAllParked(reason !== '' ? reason : 'the director cancelled this turn');
    if (this.#threadId === '' || this.#turnId === '') return;
    try {
      // codex requires BOTH ids; without either it answers -32600 "missing
      // field".
      await this.#call('turn/interrupt', { threadId: this.#threadId, turnId: this.#turnId });
    } catch (err) {
      this.#emit('error', 'system', {
        text: err instanceof Error ? err.message : String(err),
        stream: 'cancel',
      });
    }
  }

  // ── Parked requests ────────────────────────────────────────────────────────

  #park(jsonrpcId: number | string, method: string, params: unknown): void {
    const requestId = `codex-${String(this.#epoch)}-${String(jsonrpcId)}`;
    const ask = parseServerAsk(method, params, requestId);

    if (ask.form === 'unsupported') {
      // Answered immediately, never parked: a card the director cannot answer
      // would hold the engine open forever, which is a stub wearing a card's
      // clothes (D-4).
      this.#respond(jsonrpcId, refuseResult(method));
      this.#emit('system', 'system', {
        kind: 'codex_request_refused',
        method,
        reason: ask.note ?? 'unbridged',
        summary: ask.summary,
      });
      return;
    }

    this.#parked.set(requestId, { jsonrpcId, ask });
    this.#emit('approval_request', 'agent', askEventPayload(ask));
  }

  #answerParked(requestId: string, choice: string): void {
    const parked = this.#parked.get(requestId);
    if (parked === undefined) {
      // The connection restarted, or the request was already answered. Report
      // it rather than throwing: the card is gone either way, and an exception
      // here would surface on a surface that cannot say which session it was.
      this.#emit('system', 'system', {
        kind: 'codex_answer_unmatched',
        request_id: requestId,
        choice,
      });
      return;
    }
    this.#parked.delete(requestId);
    this.#respond(parked.jsonrpcId, askResult(parked.ask, choice));
  }

  #refuseAllParked(reason: string): void {
    for (const [requestId, parked] of [...this.#parked]) {
      this.#parked.delete(requestId);
      this.#respond(parked.jsonrpcId, refuseResult(parked.ask.method));
      this.#emit('system', 'system', {
        kind: 'codex_request_refused',
        method: parked.ask.method,
        reason,
      });
    }
  }

  // ── Frames ─────────────────────────────────────────────────────────────────

  #onFrame(frame: CodexFrame): void {
    const id = frame.id;
    const method = asString(frame.method);

    if (id !== undefined && method === '') {
      const waiter = typeof id === 'number' ? this.#pending.get(id) : undefined;
      if (waiter !== undefined) {
        this.#pending.delete(id as number);
        waiter(frame);
      }
      return;
    }
    if (id !== undefined && method !== '') {
      this.#park(id as number | string, method, frame.params);
      return;
    }
    if (method === '') return;
    this.#onNotification(method, frame);
  }

  #onNotification(method: string, frame: CodexFrame): void {
    if (isDeltaMethod(method)) {
      if (method === AGENT_MESSAGE_DELTA) this.#onDelta(this.#messageBuffers, frame, false);
      else if (method === COMMAND_OUTPUT_DELTA) this.#onDelta(this.#outputBuffers, frame, true);
      // Every other delta stays dropped: reasoning text and raw reasoning
      // content are internal monologue the vocabulary does not surface.
      return;
    }

    const params = asRecord(frame.params);
    if (method === 'item/completed' && params !== undefined) {
      const item = asRecord(params.item);
      if (item !== undefined) {
        const itemId = asString(item.id);
        const type = asString(item.type);
        // The profile is about to post the authoritative row for this item, so
        // stop streaming partials for it. Without this the last partial can
        // land AFTER the final and pin the card at a stale value.
        if (itemId !== '') {
          if (type === 'agentMessage') this.#finalize(this.#messageBuffers, itemId);
          else if (type === 'commandExecution') this.#finalize(this.#outputBuffers, itemId);
        }
      }
    }
    if (params !== undefined) {
      if (method === 'turn/started') {
        const turn = asRecord(params.turn);
        if (turn !== undefined) this.#turnId = asString(turn.id) || this.#turnId;
      } else if (method === 'turn/completed' || method === 'turn/failed') {
        this.#turnId = '';
      }
    }

    const events: EmittedEvent[] = applyProfile(frame, this.#opts.family.frame_profile);
    for (const ev of events) this.#emit(ev.kind, ev.producer, ev.payload ?? {});
  }

  #onClose(info: { code: number | null; reason: string }): void {
    if (this.#stopped) return;
    this.#stopped = true;
    this.#clearTimers();
    this.#channel = null;
    // Fail everything still waiting: a caller blocked on a response that can
    // no longer arrive would hang until the process ended.
    for (const [id, waiter] of [...this.#pending]) {
      this.#pending.delete(id);
      waiter({ id, error: { code: -1, message: info.reason } });
    }
    this.#emit('lifecycle', 'system', {
      phase: 'stopped',
      mode: 'M2',
      source: 'local',
      engine: 'codex-app-server',
      exit_code: info.code,
      reason: info.reason,
      // We did not ask for this — `stop()` sets `#stopped` before closing, so
      // reaching here means the engine or the socket went on its own.
      expected: false,
    });
    this.#opts.onExit?.(info.code);
  }

  // ── Streaming deltas ───────────────────────────────────────────────────────

  #onDelta(buffers: Map<string, StreamBuffer>, frame: CodexFrame, bounded: boolean): void {
    const flushMs = this.#opts.flushIntervalMs ?? DEFAULT_FLUSH_MS;
    if (flushMs < 0) return;
    const params = asRecord(frame.params);
    if (params === undefined) return;
    const itemId = asString(params.itemId);
    const delta = asString(params.delta);
    if (itemId === '' || delta === '') return;

    let buf = buffers.get(itemId);
    if (buf === undefined) {
      buf = { text: '', timer: null };
      buffers.set(itemId, buf);
    }
    buf.text = bounded ? trimToTail(buf.text + delta, MAX_STREAMED_OUTPUT_BYTES) : buf.text + delta;
    // A THROTTLE, not a debounce: the timer is only armed when there is none,
    // so a fast stream flushes every `flushMs` instead of never.
    if (buf.timer === null) {
      buf.timer = setTimeout(() => this.#flush(buffers, itemId, bounded), flushMs);
    }
  }

  #flush(buffers: Map<string, StreamBuffer>, itemId: string, bounded: boolean): void {
    const buf = buffers.get(itemId);
    if (buf === undefined) return;
    buf.timer = null;
    if (buf.text === '') return;
    if (bounded) {
      // `toolCallId` (not `tool_use_id`) is the key BOTH clients fold a running
      // tool card on, and the payload carries NO `status`: the latest update's
      // status wins over the one derived from the paired tool_result, so a
      // trailing "in_progress" would pin the card at running forever.
      this.#emit('tool_call_update', 'agent', {
        toolCallId: itemId,
        content: [{ type: 'content', content: { type: 'text', text: buf.text } }],
        partial: true,
      });
    } else {
      this.#emit('text', 'agent', { text: buf.text, message_id: itemId, partial: true });
    }
  }

  #finalize(buffers: Map<string, StreamBuffer>, itemId: string): void {
    const buf = buffers.get(itemId);
    if (buf === undefined) return;
    if (buf.timer !== null) clearTimeout(buf.timer);
    buffers.delete(itemId);
  }

  #clearTimers(): void {
    for (const buffers of [this.#messageBuffers, this.#outputBuffers]) {
      for (const buf of buffers.values()) {
        if (buf.timer !== null) clearTimeout(buf.timer);
      }
      buffers.clear();
    }
  }

  // ── JSON-RPC plumbing ──────────────────────────────────────────────────────

  #call(method: string, params: unknown): Promise<Record<string, unknown>> {
    const channel = this.#channel;
    if (channel === null) return Promise.reject(new Error(`local codex: channel is closed (${method})`));
    const id = ++this.#nextId;
    return new Promise<Record<string, unknown>>((resolve, reject) => {
      this.#pending.set(id, (frame) => {
        const err = asRecord(frame.error);
        if (err !== undefined) {
          reject(new Error(`codex ${method}: ${asString(err.message) || JSON.stringify(err)}`));
          return;
        }
        resolve(asRecord(frame.result) ?? {});
      });
      try {
        channel.send({ jsonrpc: '2.0', id, method, params });
      } catch (err) {
        this.#pending.delete(id);
        reject(err instanceof Error ? err : new Error(String(err)));
      }
    });
  }

  #notify(method: string, params: unknown): void {
    this.#channel?.send({ jsonrpc: '2.0', method, params });
  }

  #respond(id: number | string, result: Record<string, unknown>): void {
    this.#channel?.send({ jsonrpc: '2.0', id, result });
  }

  #emit(kind: string, producer: string, payload: Record<string, unknown>): void {
    this.#opts.onEvent({ kind, producer, payload });
  }
}

function asRecord(v: unknown): Record<string, unknown> | undefined {
  return v !== null && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : undefined;
}

function asString(v: unknown): string {
  return typeof v === 'string' ? v : '';
}
