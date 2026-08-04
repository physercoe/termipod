import type { InputAttachments } from '../hub/client';
import type { SseHandle, SseOptions } from '../hub/sse';
import { num, type Entity } from '../hub/types.ts';

/// **AgentEventSource** — the one seam between the Companion and whatever is
/// producing the typed `agent_events` vocabulary (vision-parity L1; plan D-7
/// "hub-optional"). Today there is exactly one implementation, the hub SDK;
/// lane L3/L4 add a desktop-local driver behind the same shape, and the
/// renderer, the folds and the Composer never learn which one they are reading.
///
/// The contract is deliberately narrow — it is the *transport*, not the
/// feature set:
///
///   - `history` + `subscribe` are the primitive pair because both producers
///     have exactly it. The hub serves a backfill page plus an SSE tail; the
///     local service (plan L3) keeps an append-only log with snapshot + cursor
///     semantics, which is the same pair by another name. Orchestrating them
///     (sort, cursor, dedupe) is shared logic, so it lives in `followAgent`
///     below rather than being written once per source.
///   - `send` / `approve` / `answer` are one channel hub-side (`POST
///     /agents/{id}/input`, three `kind`s) and one channel locally too, so they
///     are core: a producer that can *block* an agent must be able to unblock
///     it. Plan L4 says as much — a local codex driver surfaces its parked
///     approvals as R1 cards with no attention table involved.
///   - `attention` is OPTIONAL, and that is the whole point of D-4. The
///     attention table is a hub concept (a durable cross-agent queue with its
///     own `POST /attention/{id}/decide`); a local source has none, so the
///     capability is *absent* rather than stubbed, and the surfaces that read
///     it check for presence rather than for `kind === 'hub'`.
///
/// Note the plan's L1 line names "attention resolve" as the fourth verb to
/// extract. It was written before R1 existed, when resolving an attention item
/// was the only way to unblock a blocked agent. R1 added the direct input
/// verbs, and those are the ones that generalise — so the extraction split in
/// two: `approve`/`answer` core, attention optional.
///
/// Pure and bridge-free (type-only imports of the SDK) so `node --test` pins
/// the contract; the React binding is `useAgentSource.ts`.

/// Which producer is behind a source. Not a switch the renderer should read —
/// check for the capability you need instead — but it names sources in logs
/// and in the picker.
export type SourceKind = 'hub' | 'local';

/// The semantic decision set the hub validates against when the agent offered
/// no option ids of its own (`handlers_agent_input.go`, `case "approval"`).
export type ApprovalDecision = 'approve' | 'allow' | 'deny' | 'cancel';

/// Resolving a parked attention item. Hub-only: present on a hub source,
/// absent on a local one (D-4 — degrade to absence, never to a stub that
/// silently no-ops).
export interface AttentionCapability {
  resolve(itemId: string, decision: string): Promise<void>;
}

export interface AgentEventSource {
  readonly kind: SourceKind;
  /// The newest `tail` events for one agent, in whatever order the producer
  /// returns them — `followAgent` is what orders them.
  history(agentId: string, opts?: { tail?: number }): Promise<Entity[]>;
  /// Live tail from `opts.since` (a `seq` cursor).
  subscribe(agentId: string, opts: SseOptions): SseHandle;
  /// Director text plus optional multimodal attachments.
  send(agentId: string, body: string, att?: InputAttachments): Promise<void>;
  /// Resolve an `approval_request` the agent is blocked on. `decision` is
  /// typed as a plain string, not `ApprovalDecision`: when the agent offered
  /// its own option set, `optionId` is the source of truth and the decision
  /// beside it carries the same id (see `approvalWire` in ui/approvalRequest).
  approve(agentId: string, requestId: string, decision: string, optionId?: string): Promise<void>;
  /// Answer a tool question. `body` is the chosen option's LABEL — the hub
  /// carved `answer` off `approval` so the agent receives the option text.
  answer(agentId: string, requestId: string, body: string): Promise<void>;
  readonly attention?: AttentionCapability;
}

/// The slice of the hub SDK the adapter consumes. Structural on purpose: the
/// contract test drives it with a fake, and nothing in this module depends on
/// the concrete `HubClient` (which pulls in the bridge and cannot load outside
/// a browser).
export interface HubAgentBackend {
  listAgentEvents(id: string, opts?: { tail?: number }): Promise<Entity[]>;
  streamAgent(id: string, opts: SseOptions): SseHandle;
  postAgentInput(id: string, body: string, att?: InputAttachments, raw?: boolean): Promise<unknown>;
  approveAgentInput(id: string, requestId: string, decision: ApprovalDecision, optionId?: string): Promise<unknown>;
  answerAgentInput(id: string, requestId: string, body: string): Promise<unknown>;
  decideAttention(id: string, decision: string, extra?: Record<string, unknown>): Promise<unknown>;
}

/// The hub SDK as an event source — the first implementation (plan L1).
export function hubAgentSource(client: HubAgentBackend): AgentEventSource {
  return {
    kind: 'hub',
    history: (agentId, opts) => client.listAgentEvents(agentId, opts),
    subscribe: (agentId, opts) => client.streamAgent(agentId, opts),
    send: async (agentId, body, att) => {
      await client.postAgentInput(agentId, body, att);
    },
    approve: async (agentId, requestId, decision, optionId) => {
      // The SDK's narrow union is the *semantic* set. When the agent offered
      // its own option ids the hub reads `option_id` as authoritative and
      // ignores the decision beside it, so an ACP id ("proceed_once")
      // legitimately rides this field. The widening is admitted here, once,
      // instead of as a cast at every card.
      await client.approveAgentInput(agentId, requestId, decision as ApprovalDecision, optionId);
    },
    answer: async (agentId, requestId, body) => {
      await client.answerAgentInput(agentId, requestId, body);
    },
    attention: {
      resolve: async (itemId, decision) => {
        await client.decideAttention(itemId, decision);
      },
    },
  };
}

/// Ascending by `seq`. The backfill page comes back newest-first when `tail`
/// is set (the hub gates newest-first on `tail=true` + a limit), so the feed
/// has to reorder it before rendering. Copies — the caller's array is the
/// producer's and may be reused.
export function sortBySeq(events: readonly Entity[]): Entity[] {
  return [...events].sort((a, b) => (num(a, 'seq') ?? 0) - (num(b, 'seq') ?? 0));
}

/// Append one live event unless its `seq` is already in view. A reconnect
/// resumes from the last cursor the stream saw, which can overlap what the
/// backfill already delivered — without this the same turn renders twice.
/// Returns the SAME array on a duplicate so a React setState skips the render.
/// An event with no `seq` is always appended: there is nothing to dedupe on,
/// and dropping it would lose a real event.
export function appendEvent(prev: readonly Entity[], ev: Entity): Entity[] {
  const s = num(ev, 'seq');
  if (s !== undefined && prev.some((p) => num(p, 'seq') === s)) return prev as Entity[];
  return [...prev, ev];
}

export interface FollowHandle {
  close(): void;
}

export interface FollowOptions {
  /// How many events to backfill. Omit for the producer's default.
  tail?: number;
  /// The ordered backfill page, once. Replaces the view.
  onBackfill: (events: Entity[]) => void;
  /// One live event. Feed it through `appendEvent` — the cursor can overlap
  /// the backfill.
  onEvent: (event: Entity) => void;
  onError: (err: unknown) => void;
}

/// Backfill, then tail from where the backfill ended. Both sources need
/// exactly this sequence, so it is written once here rather than in each
/// implementation — and once here it can be tested, which the version that
/// lived inside a `useEffect` could not be.
///
/// `close()` is safe at any point, including before the backfill resolves: a
/// late resolution neither renders nor opens a stream. That is not
/// hypothetical — switching agents while a slow backfill is in flight is the
/// ordinary case, and without the guard the previous agent's page lands in the
/// new agent's feed.
export function followAgent(source: AgentEventSource, agentId: string, opts: FollowOptions): FollowHandle {
  let closed = false;
  let handle: SseHandle | null = null;

  void (async () => {
    try {
      const initial = await source.history(agentId, opts.tail !== undefined ? { tail: opts.tail } : undefined);
      if (closed) return;
      const ordered = sortBySeq(initial);
      opts.onBackfill(ordered);
      const last = ordered.length > 0 ? num(ordered[ordered.length - 1], 'seq') : undefined;
      handle = source.subscribe(agentId, {
        since: last !== undefined ? String(last) : undefined,
        onEvent: (e) => {
          if (!closed) opts.onEvent(e as Entity);
        },
        onError: (err) => {
          if (!closed) opts.onError(err);
        },
      });
      // Closed while `subscribe` was being set up — close what we just opened.
      if (closed) handle.close();
    } catch (err) {
      if (!closed) opts.onError(err);
    }
  })();

  return {
    close(): void {
      closed = true;
      handle?.close();
    },
  };
}
