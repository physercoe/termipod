/// The per-session append-only event log (vision-parity L3, discussion §9).
///
/// A session's transcript lives here, in Electron main, for as long as the
/// service holds the session — which is longer than any renderer holds a view
/// of it. That asymmetry is the whole reason this module exists: the Companion
/// can close, reopen, or switch agents mid-turn, and what it reattaches to has
/// to be a log it can replay, not a stream it missed.
///
/// **The shape is the hub's, on purpose.** `followAgent` (state/agentSource.ts)
/// backfills with `history()`, sorts by `seq`, then subscribes from the last
/// `seq` it saw — one orchestration for both producers. So a local event is an
/// `agent_events` row in every field the renderer reads (`toFeedEvent`:
/// id / seq / session_ordinal / kind / producer / ts / payload), and the folds,
/// the lenses and the Composer never learn which source they are reading. Plan
/// L1 promised the renderer would be untouched; this is what pays for it.
///
/// **`seq` is per SESSION, not per child.** The hub's is per agent, and it says
/// so — `session_ordinal` exists precisely because `seq` collides across a
/// resumed session's agents (ADR-042). Locally we get to choose, and choosing
/// the session means the two numbers coincide honestly rather than by luck:
/// when L3b respawns a child to rebind after an app restart, the counter keeps
/// climbing instead of restarting inside a transcript the reader thinks is
/// continuous.
///
/// **No epoch, deliberately — see `since()`.** The discussion's cursor is
/// `{seq, epoch}`, and this ships only the `seq` half.
///
/// Pure: no Electron, no child processes, no clock of its own (the caller
/// stamps `ts`). `node --test` runs it directly.

/// One row of a session's transcript, in the hub's `agent_events` wire shape.
export interface LocalAgentEvent {
  id: string;
  seq: number;
  /// Equal to `seq` for a local session — one dense counter per session, so
  /// the session-scoped navigation anchor and the cursor are the same number.
  /// Both are published because the renderer reads both.
  session_ordinal: number;
  ts: string;
  kind: string;
  producer: string;
  payload: Record<string, unknown>;
}

/// A backfill page plus where to resume from.
export interface LogPage {
  events: LocalAgentEvent[];
  /// The `seq` a subscriber should resume after. Equal to the last event's
  /// `seq`, or the log's high-water mark when the page is empty — resuming
  /// from 0 after an empty page would replay a transcript the caller just
  /// chose not to fetch.
  cursor: number;
  /// The requested cursor pointed into history this log no longer holds, so
  /// the page is a fresh snapshot rather than a continuation. A caller that
  /// ignores this renders a transcript with a silent hole in it.
  resyncRequired: boolean;
}

/// How many events one session retains. Beyond this the oldest are dropped and
/// a cursor that old gets `resyncRequired`.
///
/// The number is a memory bound, not a product decision: a long claude session
/// is thousands of events, several of which carry whole tool results, and this
/// log is per session in a process that also runs the UI. The hub's transcript
/// is the durable one — a local session that has outgrown this cap has scrolled
/// far past what the dock shows anyway.
export const DEFAULT_CAPACITY = 5000;

export class SessionLog {
  #events: LocalAgentEvent[] = [];
  #nextSeq = 1;
  /// The `seq` of the oldest event still retained. Starts at 1 (nothing
  /// dropped yet) and only ever rises, which is what makes the gap test in
  /// `since()` a comparison rather than a search.
  #oldestSeq = 1;
  readonly #capacity: number;

  constructor(capacity: number = DEFAULT_CAPACITY) {
    // A non-positive capacity would evict every event as it arrives, turning
    // the log into a silent black hole. Treat it as a caller error rather than
    // clamping to something arbitrary.
    if (!Number.isInteger(capacity) || capacity < 1) {
      throw new Error(`SessionLog capacity must be a positive integer, got ${String(capacity)}`);
    }
    this.#capacity = capacity;
  }

  /// Rebuild a log from rows that already carry their `seq` — the durable log
  /// reading a session back off disk after an app restart (L3b).
  ///
  /// Numbering is ADOPTED, never re-assigned. That is the whole point: a
  /// reloaded transcript keeps the `seq` values its events were written with,
  /// so a `seq` means the same event before and after the restart. Re-running
  /// them through `append()` would renumber from 1 and quietly turn every
  /// recorded cursor into a pointer at the wrong row.
  ///
  /// `rows` must be ascending and dense in `seq`; the caller (`durablelog.ts`)
  /// is what reads them off disk and is where a gap is detected, because it is
  /// the only layer that can tell "the file was truncated" from "these rows
  /// were passed in wrong".
  static restore(rows: readonly LocalAgentEvent[], capacity: number = DEFAULT_CAPACITY): SessionLog {
    const log = new SessionLog(capacity);
    if (rows.length === 0) return log;
    const kept = rows.length > capacity ? rows.slice(rows.length - capacity) : [...rows];
    log.#events = kept.map((r) => ({ ...r }));
    log.#oldestSeq = kept[0].seq;
    log.#nextSeq = kept[kept.length - 1].seq + 1;
    return log;
  }

  /// The highest `seq` assigned so far; 0 before the first append.
  get highWater(): number {
    return this.#nextSeq - 1;
  }

  get size(): number {
    return this.#events.length;
  }

  /// Append one event, assigning it the next `seq`. Returns the stored row so
  /// the caller can hand the very same object to live subscribers — a
  /// subscriber and a later `since()` reader must not see two different
  /// numberings of the same event.
  append(ev: { id: string; ts: string; kind: string; producer: string; payload: Record<string, unknown> }): LocalAgentEvent {
    const seq = this.#nextSeq++;
    const row: LocalAgentEvent = {
      id: ev.id,
      seq,
      session_ordinal: seq,
      ts: ev.ts,
      kind: ev.kind,
      producer: ev.producer,
      payload: ev.payload,
    };
    this.#events.push(row);
    if (this.#events.length > this.#capacity) {
      const dropped = this.#events.length - this.#capacity;
      this.#events.splice(0, dropped);
      // The oldest RETAINED seq, read off the array rather than computed from
      // the drop count — they agree today, and reading the array keeps them
      // agreeing if eviction ever grows a second trigger.
      this.#oldestSeq = this.#events[0].seq;
    }
    return row;
  }

  /// The newest `n` events (all of them when `n` is omitted).
  ///
  /// Ascending by `seq`, which `sortBySeq` on the renderer side then leaves
  /// alone. The hub returns its tail page newest-first; matching that would
  /// mean two orderings for one contract, and the ordering that survives is
  /// the one the consumer wants.
  tail(n?: number): LogPage {
    const events = n === undefined ? [...this.#events] : this.#events.slice(Math.max(0, this.#events.length - Math.max(0, n)));
    return {
      events,
      cursor: events.length > 0 ? events[events.length - 1].seq : this.highWater,
      // A tail IS a fresh snapshot — there is no prior position it could have
      // failed to continue from.
      resyncRequired: false,
    };
  }

  /// Everything after `cursor`.
  ///
  /// `resyncRequired` is set when `cursor` names a position older than what is
  /// retained, i.e. the events between `cursor` and `#oldestSeq` have been
  /// evicted. The caller then gets the full retained window instead of a page
  /// with a hole in it, and knows to replace its view rather than append.
  ///
  /// **Why no epoch.** Discussion §9 credits kap-server with a `{seq, epoch}`
  /// cursor, and epoch is what tells a client its cursor belongs to a previous
  /// incarnation of the session. This log has exactly one incarnation: it lives
  /// in main's heap, so anything that ends it also ends the renderer holding
  /// the cursor. Shipping the field now would mean a comparison whose two sides
  /// can never differ — a branch no test could reach except by fabricating an
  /// input production cannot produce. It arrives with the on-disk log in L3b,
  /// where a cursor really can outlive the log it came from.
  since(cursor: number): LogPage {
    if (!Number.isFinite(cursor) || cursor < 0) {
      // Not a position this log ever issued. Treat it as "start over" rather
      // than guessing, and say so.
      return { ...this.tail(), resyncRequired: true };
    }
    if (cursor >= this.highWater) {
      // Caller is current (or ahead of us, which a future log with persistence
      // could produce). Nothing to send, and its cursor stands.
      return { events: [], cursor: Math.max(cursor, this.highWater), resyncRequired: false };
    }
    // Serving from `cursor` requires the event at cursor+1 to still be here.
    if (cursor + 1 < this.#oldestSeq) {
      return { ...this.tail(), resyncRequired: true };
    }
    const events = this.#events.filter((e) => e.seq > cursor);
    return {
      events,
      cursor: events.length > 0 ? events[events.length - 1].seq : this.highWater,
      resyncRequired: false,
    };
  }
}
