/// Stream-folding parity port (agent-transcript-redesign §6 P1) of mobile's
/// `collapseStreamingPartials` (lib/widgets/transcript/feed_reducer.dart:1236).
/// The Dart reducer is the byte-reference; this module must stay behaviour-
/// identical to it. Pure + import-free at runtime (the FeedEvent import is
/// type-only) so it runs under `node --test` like the other src leaf modules.
///
// Codex emits item/agentMessage/delta as a stream of small chunks while a turn
// is generating. The driver throttles + buffers them into `kind=text,
// partial: true` events that share a message_id; each carries the full
// accumulated text so far (not a delta). The final item/completed produces a
// normal `kind=text` event with the same message_id and no partial flag.
//
// Collapse rule: walk events in order. The first partial for a message_id
// opens a chain — its index in the rendered list is remembered. Subsequent
// events (partial OR final) for the same chain replace the chain entry
// instead of appending. An event with no partial flag and no preceding
// partial chain (claude's case) appends normally — we only redirect events
// whose message_id is already a known chain root, so claude's per-block text
// events with the same message_id keep stacking the way they do today.
import type { FeedEvent } from './EventCard';

/// The kinds that fold. `text` + `thought` are mobile's original allowlist
/// (gemini-cli streams thought chunks the same way it streams text —
/// incremental session/update frames the driver accumulates and re-emits with
/// a shared message_id + partial:true; without `thought` they stack as N
/// redundant cards each carrying the cumulative text so far). `plan` is the
/// P1 addition (mobile is updated in parallel): the hub stamps every ACP
/// plan update with a stable per-turn message_id + partial:true
/// (driver_acp.go plan arm), so folding them here is what turns N todo
/// snapshot cards into ONE card that updates in place (plan goal G3).
const FOLD_KINDS = new Set(['text', 'thought', 'plan']);

/// Fold streaming partial chains in [events], returning a new list. Chains
/// are keyed by `kind:message_id` — namespaced by kind so a `text` and a
/// `thought` event that happen to share a message_id (e.g. when the engine
/// reuses turn-local ids across kinds) don't fold into each other. Events
/// without a message_id, or with no preceding partial, pass through
/// unchanged.
export function collapseStreamingPartials(events: FeedEvent[]): FeedEvent[] {
  const out: FeedEvent[] = [];
  const chainIdx = new Map<string, number>();
  for (const e of events) {
    if (!FOLD_KINDS.has(e.kind)) {
      out.push(e);
      continue;
    }
    const p = e.payload;
    // Mobile coerces with toString() — mirror it (a non-string message_id
    // still keys a chain rather than silently passing through).
    const rawMid = p['message_id'];
    const mid = rawMid === null || rawMid === undefined ? '' : String(rawMid);
    // `partial` arrives as a real bool from most drivers, as "true" from
    // frame-profile evaluators that only emit strings.
    const pv = p['partial'];
    const isPartial = pv === true || pv === 'true';
    if (mid === '') {
      out.push(e);
      continue;
    }
    const chainKey = `${e.kind}:${mid}`;
    const existing = chainIdx.get(chainKey);
    if (existing !== undefined) {
      // We're in a streaming chain for this kind+message_id — every
      // subsequent event (partial or final) replaces the entry.
      out[existing] = e;
    } else if (isPartial) {
      // First partial for this kind+message_id opens a chain.
      chainIdx.set(chainKey, out.length);
      out.push(e);
    } else {
      // Regular event with no preceding partial — claude's shape; append
      // without opening a chain.
      out.push(e);
    }
  }
  return out;
}
