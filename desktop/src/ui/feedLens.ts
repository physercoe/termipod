import { bool, str, type Entity } from '../hub/types';
import { callToolId, type FeedEvent } from './EventCard';

/// The transcript "lens" — a family filter over the flat event feed, a
/// byte-for-byte port of mobile's `FeedLens` (feed_reducer.dart:948) and its
/// kind sets. Used by the live-feed filter (all five) and Insight mode.
export type FeedLens = 'all' | 'text' | 'turns' | 'tools' | 'errors';
export const FEED_LENSES: FeedLens[] = ['all', 'text', 'turns', 'tools', 'errors'];

// feed_reducer.dart:952 / :961 / :991
const TEXT_KINDS = new Set(['text', 'thought', 'input.text']);
const TURN_KINDS = new Set(['input.text', 'input.cancel', 'input.approval', 'input.attention_reply', 'system']);
const TOOL_KINDS = new Set(['tool_call', 'tool_result', 'tool_call_update']);

/// Mirror of mobile `agentEventIsError` (feed_reducer.dart:1006): a bare error
/// event, a failed `tool_result`, or a `tool_call` whose paired result failed.
export function eventIsError(ev: FeedEvent, resultById: Map<string, Entity>): boolean {
  if (ev.kind === 'error') return true;
  if (ev.kind === 'tool_result') return bool(ev.payload, 'is_error') === true;
  if (ev.kind === 'tool_call') {
    const id = callToolId(ev.payload);
    const r = id !== undefined ? resultById.get(id) : undefined;
    return r !== undefined && bool(r, 'is_error') === true;
  }
  return false;
}

/// Mirror of mobile `agentEventMatchesLens` (feed_reducer.dart:1162).
export function matchesLens(ev: FeedEvent, lens: FeedLens, resultById: Map<string, Entity>): boolean {
  switch (lens) {
    case 'all':
      return true;
    case 'text':
      return TEXT_KINDS.has(ev.kind);
    case 'turns':
      return TURN_KINDS.has(ev.kind);
    case 'tools':
      return TOOL_KINDS.has(ev.kind);
    case 'errors':
      return eventIsError(ev, resultById);
  }
}

/// Feed-noise model (parity — mobile feed_reducer.dart `isHiddenInFeed` /
/// `isVerboseOnly`). The live feed defaults to a clean reading view; a "verbose"
/// toggle reveals the low-signal rows. Two tiers:
///  - ALWAYS_HIDDEN — pure telemetry, never shown in either mode.
///  - VERBOSE_ONLY  — lifecycle chatter, shown only when verbose is on.
/// The user's own input (`input.*`) is NEVER hidden.
const ALWAYS_HIDDEN_KINDS = new Set([
  'usage',
  'rate_limit',
  'turn.result',
  'tool_call_update',
  'status_line',
]);
const VERBOSE_ONLY_KINDS = new Set(['lifecycle', 'completion', 'system', 'thought']);
// MCP permission-gate calls are prompts, not real tool work — noise in the feed.
const GATE_TOOL_NAMES = new Set(['permission_prompt', 'request_select', 'request_approval']);

/// Whether an event is suppressed from the live feed. `verbose=true` reveals the
/// VERBOSE_ONLY tier; ALWAYS_HIDDEN telemetry and gate prompts stay hidden.
export function isHiddenInFeed(ev: FeedEvent, verbose: boolean): boolean {
  if (ev.kind.startsWith('input.')) return false; // user input always visible
  if (ALWAYS_HIDDEN_KINDS.has(ev.kind)) return true;
  if (ev.kind === 'tool_call') {
    const name = str(ev.payload, 'name');
    if (name !== undefined && GATE_TOOL_NAMES.has(name)) return true;
  }
  if (!verbose && VERBOSE_ONLY_KINDS.has(ev.kind)) return true;
  return false;
}

/// A short human label for an error row in the Insight navigator — the failing
/// tool's name where we can find it, else the kind.
export function errorLabel(ev: FeedEvent, nameById: Map<string, string>): string {
  if (ev.kind === 'tool_call') {
    const id = callToolId(ev.payload);
    const name = id !== undefined ? nameById.get(id) : undefined;
    return name ?? 'tool';
  }
  if (ev.kind === 'tool_result') return 'tool result';
  return 'error';
}
