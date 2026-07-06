import { bool, type Entity } from '../hub/types';
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
