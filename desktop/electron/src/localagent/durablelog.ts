/// The on-disk half of a local session's transcript (vision-parity L3b).
///
/// L3a kept a session's events in main's heap, which meant quitting the app
/// ended the session and everything it had said. This puts the same rows in an
/// append-only JSONL file so a session can be read back after a restart.
///
/// **Why the file is the only source of the transcript.** Measured against
/// claude-code 2.1.220: `--resume <id>` under the M2 pipe restores the *model's*
/// context — a codeword set before the restart came back correctly — but emits
/// **no replay frames at all** (0 user frames, 0 assistant frames from the
/// prior turns). The engine remembers; it does not re-narrate. So native resume
/// alone would hand the director a blank transcript backed by an agent that
/// secretly knows things, which is strictly worse than a cold start: you cannot
/// see what it is about to act on. The engine restores the memory, this file
/// restores the view, and rebind needs both halves or neither is worth doing.
///
/// **The layering.** `SessionLog` (log.ts) stays the pure in-memory window with
/// the cursor semantics the renderer already speaks. This wraps it: appends go
/// to the window *and* the file; reads are served from the window. So the disk
/// is durability, not scrollback — a cursor older than the retained window still
/// gets `resyncRequired`, exactly as before, even though the bytes exist on
/// disk. Serving arbitrary history from the file is a real feature and a
/// different one (it needs an index; a 1.8 GB transcript is not hypothetical —
/// that is the size of a real claude session file on this machine).
///
/// **Still no epoch, and now for a stated reason rather than an absence of
/// one.** L3a omitted the `{seq, epoch}` cursor's epoch half because the log
/// lived in main's heap, so the two sides of the comparison could never differ,
/// and it recorded that an on-disk log was what would make epoch meaningful.
/// Building it, that turns out to be conditional: an epoch is only needed if
/// numbering can restart under an id a client already holds a cursor for. This
/// module makes that unreachable by construction — `seq` is adopted from the
/// file (`SessionLog.restore`), never re-assigned, and a session whose event
/// file is missing is REFUSED rather than reopened empty at seq 1. Under those
/// two rules an epoch would still be a comparison whose sides cannot differ.
/// The condition that changes the answer is a client holding a cursor across a
/// service restart — which is L3c's loopback socket, not this wedge.

import { appendFileSync, existsSync, mkdirSync, openSync, closeSync, readSync, fstatSync, renameSync, writeFileSync } from 'node:fs';
import path from 'node:path';

import { DEFAULT_CAPACITY, SessionLog, type LocalAgentEvent, type LogPage } from './log.ts';

/// How much of the tail to read back at load. The window we serve is bounded by
/// event count, but the file is bounded by nothing, so the reader takes bytes
/// from the end rather than parsing from the start — a session that ran all week
/// must not cost a full-file parse to reopen.
///
/// Sized to comfortably hold `DEFAULT_CAPACITY` ordinary rows while staying a
/// bounded read. When it does not (a few enormous tool results), fewer events
/// come back and the log says so via its normal `resyncRequired` path.
export const DEFAULT_TAIL_BYTES = 8 * 1024 * 1024;

/// Rewrite the file down to the retained window once it passes this. Without
/// it, a long-running session grows without bound on the director's disk — the
/// engine's own session files reach gigabytes, and ours has no more right to.
export const DEFAULT_MAX_FILE_BYTES = 32 * 1024 * 1024;

export const EVENTS_FILENAME = 'events.jsonl';

export interface ParsedChunk {
  events: LocalAgentEvent[];
  /// A trailing fragment with no newline — the write that was in flight when
  /// the process died. Expected on an unclean exit, not an error.
  tornTail: boolean;
  /// Lines that were complete but did not parse, or parsed to something that is
  /// not an event row. Distinct from `tornTail` because a torn tail is normal
  /// and this is corruption.
  droppedInvalid: number;
  /// The leading fragment discarded because the read started mid-file.
  droppedLeadingPartial: boolean;
}

/// Turn a chunk of the events file into rows.
///
/// Pure, and separated from the I/O on purpose: recovery is the part with the
/// interesting cases (torn tail, mid-file start, a corrupt line in the middle)
/// and none of them should need a filesystem to test.
///
/// `startedMidFile` tells it the first line is probably a fragment — true for a
/// bounded tail read, false when the whole file was read.
export function parseLogChunk(text: string, startedMidFile: boolean): ParsedChunk {
  const out: ParsedChunk = { events: [], tornTail: false, droppedInvalid: 0, droppedLeadingPartial: false };
  if (text === '') return out;

  const endsClean = text.endsWith('\n');
  const lines = text.split('\n');
  // split() on a clean end leaves a trailing '' — drop it. On a torn end the
  // final element is the fragment, which we drop and flag.
  if (endsClean) {
    lines.pop();
  } else if (lines.length > 0) {
    lines.pop();
    out.tornTail = true;
  }

  let start = 0;
  if (startedMidFile && lines.length > 0) {
    // The first line began before our read window. It may look like valid JSON
    // and still be a fragment, so it is dropped on position rather than on
    // parse success — trusting parse here is how half a tool result becomes a
    // plausible-looking event.
    start = 1;
    out.droppedLeadingPartial = true;
  }

  for (let i = start; i < lines.length; i += 1) {
    const line = lines[i];
    if (line.trim() === '') continue;
    const row = parseEventLine(line);
    if (row === null) {
      out.droppedInvalid += 1;
      continue;
    }
    out.events.push(row);
  }
  return out;
}

function parseEventLine(line: string): LocalAgentEvent | null {
  let raw: unknown;
  try {
    raw = JSON.parse(line);
  } catch {
    return null;
  }
  if (typeof raw !== 'object' || raw === null) return null;
  const o = raw as Record<string, unknown>;
  if (typeof o.id !== 'string') return null;
  if (typeof o.seq !== 'number' || !Number.isInteger(o.seq) || o.seq < 1) return null;
  if (typeof o.ts !== 'string' || typeof o.kind !== 'string' || typeof o.producer !== 'string') return null;
  if (typeof o.payload !== 'object' || o.payload === null || Array.isArray(o.payload)) return null;
  const ordinal = typeof o.session_ordinal === 'number' ? o.session_ordinal : o.seq;
  return {
    id: o.id,
    seq: o.seq,
    session_ordinal: ordinal,
    ts: o.ts,
    kind: o.kind,
    producer: o.producer,
    payload: o.payload as Record<string, unknown>,
  };
}

/// Keep the longest ascending, dense run ending at the newest row.
///
/// A gap means the middle of the file is missing or corrupt. Serving across it
/// would produce a transcript with an invisible hole — the renderer sorts by
/// `seq` and would render the two sides as one continuous conversation. Taking
/// the newest dense run instead means the reader loses old context it can see
/// it lost (the window simply starts later), rather than reading a conversation
/// that never happened.
export function newestDenseRun(rows: readonly LocalAgentEvent[]): LocalAgentEvent[] {
  if (rows.length === 0) return [];
  const sorted = [...rows].sort((a, b) => a.seq - b.seq);
  let start = sorted.length - 1;
  while (start > 0 && sorted[start - 1].seq === sorted[start].seq - 1) start -= 1;
  return sorted.slice(start);
}

export interface DurableLogOptions {
  capacity?: number;
  tailBytes?: number;
  maxFileBytes?: number;
}

export interface LoadReport {
  events: number;
  tornTail: boolean;
  droppedInvalid: number;
  /// Rows read but discarded because they sat before a gap.
  droppedBeforeGap: number;
}

/// A session log that survives the process.
export class DurableSessionLog {
  readonly dir: string;
  readonly #file: string;
  readonly #maxFileBytes: number;
  /// The size that triggers the next compaction. Normally `#maxFileBytes`, but
  /// raised past it when a compaction cannot get below the cap — see `compact`.
  #compactAt: number;
  #log: SessionLog;
  #bytes = 0;
  #closed = false;
  /// Rows appended but not yet on disk. See `#scheduleFlush` for why writes are
  /// batched rather than streamed or written one at a time.
  #pending: string[] = [];
  #flushTimer: ReturnType<typeof setTimeout> | null = null;

  private constructor(dir: string, log: SessionLog, bytes: number, opts: DurableLogOptions) {
    this.dir = dir;
    this.#file = path.join(dir, EVENTS_FILENAME);
    this.#maxFileBytes = opts.maxFileBytes ?? DEFAULT_MAX_FILE_BYTES;
    this.#compactAt = this.#bytes > this.#maxFileBytes ? this.#bytes * 2 : this.#maxFileBytes;
    this.#log = log;
    this.#bytes = bytes;
  }

  /// Create a fresh log, making its directory.
  static create(dir: string, opts: DurableLogOptions = {}): DurableSessionLog {
    mkdirSync(dir, { recursive: true });
    return new DurableSessionLog(dir, new SessionLog(opts.capacity ?? DEFAULT_CAPACITY), 0, opts);
  }

  /// Reopen an existing log, reading back the tail.
  ///
  /// Throws when the events file is absent. That is the refusal the epoch note
  /// at the top depends on: reopening empty would restart `seq` at 1 under an id
  /// whose events are numbered in the hundreds, and every stale cursor would
  /// then point at a real-looking row that is the wrong event. A caller that
  /// wants a fresh log for this id must say so by calling `create`.
  static open(dir: string, opts: DurableLogOptions = {}): { log: DurableSessionLog; report: LoadReport } {
    const file = path.join(dir, EVENTS_FILENAME);
    if (!existsSync(file)) {
      throw new Error(`local session log missing at ${file}`);
    }
    const tailBytes = opts.tailBytes ?? DEFAULT_TAIL_BYTES;
    const { text, startedMidFile, size } = readTail(file, tailBytes);
    const chunk = parseLogChunk(text, startedMidFile);
    const dense = newestDenseRun(chunk.events);
    const log = SessionLog.restore(dense, opts.capacity ?? DEFAULT_CAPACITY);
    return {
      log: new DurableSessionLog(dir, log, size, opts),
      report: {
        events: dense.length,
        tornTail: chunk.tornTail,
        droppedInvalid: chunk.droppedInvalid,
        droppedBeforeGap: chunk.events.length - dense.length,
      },
    };
  }

  get highWater(): number {
    return this.#log.highWater;
  }

  get size(): number {
    return this.#log.size;
  }

  /// Bytes the events file occupies, as this object believes them to be. Used
  /// by the compaction check and asserted in tests; not read by the service.
  get fileBytes(): number {
    return this.#bytes;
  }

  append(ev: { id: string; ts: string; kind: string; producer: string; payload: Record<string, unknown> }): LocalAgentEvent {
    const row = this.#log.append(ev);
    if (!this.#closed) {
      const line = JSON.stringify(row) + '\n';
      this.#pending.push(line);
      this.#bytes += Buffer.byteLength(line, 'utf-8');
      this.#scheduleFlush();
      if (this.#bytes > this.#compactAt) this.compact();
    }
    return row;
  }

  /// Write pending rows to disk now.
  ///
  /// Synchronous by design. The alternative — a `WriteStream` — flushes on its
  /// own schedule, which means `close()` can return before the bytes exist and
  /// app-quit silently drops the end of the transcript. The cost is bounded:
  /// one `appendFileSync` per batch, not per event.
  flush(): void {
    if (this.#flushTimer !== null) {
      clearTimeout(this.#flushTimer);
      this.#flushTimer = null;
    }
    if (this.#pending.length === 0) return;
    const body = this.#pending.join('');
    this.#pending = [];
    try {
      mkdirSync(this.dir, { recursive: true });
      appendFileSync(this.#file, body, 'utf-8');
    } catch {
      // Disk full, permissions, a directory someone deleted underneath us. This
      // must not take down the engine child or the window: the in-memory log
      // stays authoritative for this run and durability is what was lost. Going
      // closed rather than retrying forever means we stop pretending to persist.
      this.#closed = true;
    }
  }

  tail(n?: number): LogPage {
    return this.#log.tail(n);
  }

  since(cursor: number): LogPage {
    return this.#log.since(cursor);
  }

  /// Rewrite the file to hold only the retained window.
  ///
  /// Written to a sibling and renamed, so a crash mid-compaction leaves the
  /// previous file intact rather than a half-written one. Events keep their
  /// `seq`, so this drops old rows exactly as the in-memory window already had
  /// — the file stops holding history we were never going to serve.
  compact(): void {
    // Pending rows are part of the window, so they are dropped rather than
    // flushed — the rewrite below already contains them. Flushing first would
    // append them and then immediately overwrite the file that holds them.
    if (this.#flushTimer !== null) {
      clearTimeout(this.#flushTimer);
      this.#flushTimer = null;
    }
    this.#pending = [];

    const page = this.#log.tail();
    const body = page.events.map((e) => JSON.stringify(e) + '\n').join('');
    const tmp = this.#file + '.compact';
    try {
      mkdirSync(this.dir, { recursive: true });
      writeFileSync(tmp, body, 'utf-8');
      renameSync(tmp, this.#file);
      this.#bytes = Buffer.byteLength(body, 'utf-8');
      // When the retained window itself serializes past the cap (a 5000-event
      // window holding a few enormous tool results), no compaction can get the
      // file below `#maxFileBytes` — and re-triggering on the cap would mean a
      // full synchronous rewrite of a 32 MB+ file on EVERY append, in the
      // process that also draws the UI. Requiring the file to double instead
      // amortises the rewrite to O(1) per byte appended. When the window fits
      // under the cap the trigger stays the cap, exactly as before.
      this.#compactAt = this.#bytes > this.#maxFileBytes ? this.#bytes * 2 : this.#maxFileBytes;
    } catch {
      this.#closed = true;
    }
  }

  /// Flush and stop persisting. Appends after this are kept in memory only — a
  /// stopped session still answers `history()` for as long as the service holds
  /// it, it just no longer grows the file.
  close(): void {
    this.flush();
    this.#closed = true;
  }

  /// Batch within a tick rather than writing per event.
  ///
  /// A turn arrives as a burst — text deltas, tool calls, usage, the result —
  /// and writing each one separately would be that many syscalls on the process
  /// that also draws the UI. Deferring to a timer coalesces the burst; every
  /// path that matters for durability (`close`, `compact`, quit) flushes
  /// explicitly rather than waiting for it.
  #scheduleFlush(): void {
    if (this.#flushTimer !== null) return;
    this.#flushTimer = setTimeout(() => {
      this.#flushTimer = null;
      this.flush();
    }, 0);
    // Do not hold the event loop open for a pending transcript write.
    this.#flushTimer.unref?.();
  }
}

/// Read at most `maxBytes` from the end of a file.
///
/// Synchronous and low-level because it runs once per session at startup, on a
/// path where an async read would just be a promise nobody awaits differently.
function readTail(file: string, maxBytes: number): { text: string; startedMidFile: boolean; size: number } {
  const fd = openSync(file, 'r');
  try {
    const size = fstatSync(fd).size;
    const want = Math.min(size, maxBytes);
    const start = size - want;
    const buf = Buffer.allocUnsafe(want);
    let read = 0;
    while (read < want) {
      const n = readSync(fd, buf, read, want - read, start + read);
      if (n <= 0) break;
      read += n;
    }
    return {
      // A bounded read can split a multi-byte character at the start; that
      // lands inside the leading fragment we drop by position anyway.
      text: buf.subarray(0, read).toString('utf-8'),
      startedMidFile: start > 0,
      size,
    };
  } finally {
    closeSync(fd);
  }
}
