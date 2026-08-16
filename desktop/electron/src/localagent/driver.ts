/// What every local engine driver has in common (vision-parity L4c).
///
/// L3a had one driver, so its vocabulary lived in `claudewire.ts` — the posture,
/// the input kinds, the event shape. L4c adds a second engine, and a type named
/// after claude that codex has to import is a name that lies to the next reader.
/// So the engine-neutral half moves here and the two wires keep only what is
/// genuinely theirs: `claudewire.ts` builds stream-json frames and `--tools`
/// argv, `codexwire.ts` builds JSON-RPC params.
///
/// Nothing here spawns, connects, or imports `electron` — `service.ts` owns one
/// of these per session and cannot tell which engine is behind it.

// ── Tool posture ─────────────────────────────────────────────────────────────

/// How much of the director's machine a local session may touch.
///
/// **The name is the promise; each driver has to MEASURE that it keeps it.**
/// The two engines enforce it by completely different means — claude by which
/// tools exist (`--tools`), codex by an OS sandbox (`sandbox` +
/// `approvalPolicy`) — so the same posture is one contract with two proofs.
/// Both proofs are in the wire modules beside the mapping they justify, and
/// both were run against a live engine rather than read off documentation.
export type ToolPosture = 'converse' | 'read_local' | 'unrestricted';

/// The default. Reading the workdir is what makes a co-working Companion
/// useful; writing, executing and reaching the network are what make an
/// unattended one dangerous, and none of the three are here.
export const DEFAULT_TOOL_POSTURE: ToolPosture = 'read_local';

export function isToolPosture(v: unknown): v is ToolPosture {
  return v === 'converse' || v === 'read_local' || v === 'unrestricted';
}

// ── Input ────────────────────────────────────────────────────────────────────

/// A binary attachment, already base64 and without a `data:` prefix.
export interface AttachmentInput {
  mime: string;
  data: string;
  filename?: string;
}

export interface InputPayload {
  body?: string;
  images?: AttachmentInput[];
  pdfs?: AttachmentInput[];
  request_id?: string;
  decision?: string;
  note?: string;
  reason?: string;
}

/// The input kinds a local driver accepts. A deliberate subset of the hub
/// driver's: `attention_reply` and `attach` are hub concepts (an attention
/// table, a document entity) that a local session has none of, so they are
/// absent rather than stubbed (D-4).
export type InputKind = 'text' | 'approval' | 'answer' | 'cancel';

// ── Output ───────────────────────────────────────────────────────────────────

/// An event on its way to the session log — kind/producer/payload, before the
/// log assigns it a `seq`.
export interface DriverEvent {
  kind: string;
  producer: string;
  payload: Record<string, unknown>;
}

// ── The driver itself ────────────────────────────────────────────────────────

/// One engine session, as `service.ts` uses it.
///
/// `start()` returns void on both engines even though codex's opening move is a
/// multi-step async handshake. That is deliberate: the service's job is to have
/// a session that exists immediately and a transcript that explains itself, and
/// an async `start` would either make `create()` async (changing the IPC
/// contract for every caller) or leave a window where a session exists with no
/// driver. The codex driver therefore queues input until its thread is open and
/// reports a failed handshake as an `error` row — the same place a failed
/// claude launch already reports.
export interface LocalDriver {
  /// False once the engine is gone, whether we stopped it or it died.
  readonly running: boolean;
  /// The posture this session actually launched with, recorded on the
  /// lifecycle event so a transcript states what the agent was allowed to do
  /// rather than leaving a reader to infer it.
  readonly posture: ToolPosture;
  start(): void;
  input(kind: InputKind, payload: InputPayload): void;
  stop(): void;
}

/// What a driver needs from the session that owns it.
export interface DriverHooks {
  onEvent: (ev: DriverEvent) => void;
  /// The engine is gone. `service.ts` marks the session stopped and flushes.
  onExit?: (code: number | null) => void;
}
