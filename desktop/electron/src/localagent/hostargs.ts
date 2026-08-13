/// Argument readers for the local-agent IPC surface (vision-parity L3a).
///
/// Split out of `host.ts` because that file imports `electron` and therefore
/// cannot be loaded by `node --test` — the same pure-module/thin-host split the
/// rest of `electron/src` uses. Everything here arrives from the renderer as
/// `unknown`, so this is the boundary that gives it a type.

import { isToolPosture, type InputKind, type InputPayload, type ToolPosture } from './claudewire.ts';

export function requireString(args: Record<string, unknown>, key: string): string {
  const v = args[key];
  if (typeof v !== 'string' || v.trim() === '') {
    throw new Error(`local agent: ${key} is required`);
  }
  return v;
}

export function optionalString(args: Record<string, unknown>, key: string): string | undefined {
  const v = args[key];
  return typeof v === 'string' && v.trim() !== '' ? v : undefined;
}

/// A cursor, refused rather than coerced.
///
/// A NaN reaching `SessionLog.since` is handled — it resyncs — but a cursor
/// that silently became a full transcript replay is a bug wearing a correct
/// answer's clothes. Refuse it where it entered.
///
/// The narrow accept-list is the point, and it is not theoretical: a blanket
/// `Number(v)` turns **null into 0** and `''` into 0, so a renderer bug that
/// dropped the cursor would resume from the beginning of the transcript and
/// look, from every side, like it had worked.
export function requireCursor(args: Record<string, unknown>): number {
  const v = args.cursor;
  let n = Number.NaN;
  if (typeof v === 'number') n = v;
  else if (typeof v === 'string' && v.trim() !== '') n = Number(v);
  if (!Number.isFinite(n)) throw new Error('local agent: cursor must be a number');
  return n;
}

/// `tail` is optional; a nonsense value means "no tail" rather than an error,
/// because the caller asking for a page size is a hint, not a contract.
export function readTail(args: Record<string, unknown>): number | undefined {
  const v = args.tail;
  return typeof v === 'number' && Number.isFinite(v) ? Math.max(0, Math.trunc(v)) : undefined;
}

const INPUT_KINDS = new Set<string>(['text', 'approval', 'answer', 'cancel']);

export function requireInputKind(args: Record<string, unknown>): InputKind {
  const v = args.kind;
  if (typeof v !== 'string' || !INPUT_KINDS.has(v)) {
    throw new Error(`local agent: unsupported input kind ${String(v)}`);
  }
  return v as InputKind;
}

export function readPosture(args: Record<string, unknown>): ToolPosture | undefined {
  if (args.posture === undefined) return undefined;
  if (!isToolPosture(args.posture)) {
    throw new Error(`local agent: unknown tool posture ${String(args.posture)}`);
  }
  return args.posture;
}

/// Read the input payload, keeping ONLY the fields the wire builder knows.
///
/// An allowlist rather than a spread: passing the renderer's object through
/// would let it set keys the frame builder happens to read later, and "the
/// renderer is ours" is exactly the assumption that stops being true when
/// something else gets to talk to the bridge.
export function readInputPayload(args: Record<string, unknown>): InputPayload {
  const raw = (args.payload !== null && typeof args.payload === 'object' ? args.payload : {}) as Record<string, unknown>;
  const out: InputPayload = {};
  for (const key of ['body', 'request_id', 'decision', 'note', 'reason'] as const) {
    const v = raw[key];
    if (typeof v === 'string') out[key] = v;
  }
  for (const key of ['images', 'pdfs'] as const) {
    const v = raw[key];
    if (!Array.isArray(v)) continue;
    const list: { mime: string; data: string; filename?: string }[] = [];
    for (const entry of v) {
      if (entry === null || typeof entry !== 'object' || Array.isArray(entry)) continue;
      const att = entry as Record<string, unknown>;
      // Both halves are required: an attachment with no data is an empty block
      // the engine rejects, and one with no mime is one it cannot decode.
      if (typeof att.mime !== 'string' || typeof att.data !== 'string') continue;
      list.push({
        mime: att.mime,
        data: att.data,
        ...(typeof att.filename === 'string' ? { filename: att.filename } : {}),
      });
    }
    if (list.length > 0) out[key] = list;
  }
  return out;
}
