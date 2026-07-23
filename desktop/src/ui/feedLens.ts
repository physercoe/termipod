import { bool, str, type Entity } from '../hub/types';
import type { FeedEvent } from './EventCard';
import { callToolId, isGateToolCallName, isToolCallUpdateHidden } from './toolGroups';

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
  'turn.start', // turn boundary marker — pure telemetry, like turn.result
  'status_line',
]);
const VERBOSE_ONLY_KINDS = new Set([
  'lifecycle',
  'completion',
  'system',
  'session.init', // "session <model> · N tools" — low signal, reveal on Details
  'thought',
  'thinking',
  'reasoning',
]);
// MCP gate names + suffix matching live in toolGroups.ts (mobile parity —
// feed_reducer.dart:877-890 + `isGatedToolName`): permission_prompt /
// request_select / request_decision / request_approval hide their tool_call
// (bare name or `mcp__<server>__<name>` suffix), because the inline
// attention/approval card already represents the same gesture.

/// Whether an event is suppressed from the live feed. `verbose=true` reveals the
/// VERBOSE_ONLY tier; ALWAYS_HIDDEN telemetry and gate prompts stay hidden.
/// [toolNames] is the in-scope tool_call-id → name map (useToolMaps nameById);
/// it drives the tool_call_update rule (mobile feed_reducer.dart:902-924):
/// an update is hidden iff its toolCallId has a visible, NON-gated parent
/// tool_call — it folds into the parent card's status pill. Updates for gated
/// tools (whose parent is hidden by the gate rule) and orphan updates (no
/// parent in scope) render standalone. Like mobile, this rule is
/// verbose-INDEPENDENT: a foldable update stays hidden in verbose mode, and a
/// standalone update shows even with verbose off.
export function isHiddenInFeed(ev: FeedEvent, verbose: boolean, toolNames?: Map<string, string>): boolean {
  if (ev.kind.startsWith('input.')) return false; // user input always visible
  if (ALWAYS_HIDDEN_KINDS.has(ev.kind)) return true;
  if (ev.kind === 'tool_call') {
    const name = str(ev.payload, 'name');
    if (name !== undefined && isGateToolCallName(name)) return true;
  }
  if (ev.kind === 'tool_call_update' && toolNames !== undefined && isToolCallUpdateHidden(ev.payload, toolNames)) {
    return true;
  }
  if (!verbose && VERBOSE_ONLY_KINDS.has(ev.kind)) return true;
  return false;
}

/// Turn-active kinds — the allowlist that decisively signals the agent is
/// mid-turn (mobile `kAgentTurnActiveKinds`, feed_reducer.dart:145).
const TURN_ACTIVE_KINDS = new Set(['text', 'tool_call', 'tool_call_update', 'thought', 'plan']);

/// True when the latest non-user event signals an in-flight turn (mobile
/// `agentIsBusy`, feed_reducer.dart:711). Walks newest-first: `turn.result` /
/// `completion` / `session.init` and `exited`/`stopped` lifecycle short-circuit
/// to idle; only TURN_ACTIVE_KINDS decisively signal busy; everything else is
/// no-signal and the walk continues. Default = idle.
///
/// This is the composer's Stop-vs-Send signal — NOT the agent's lifecycle status
/// (`agents.status`), which reads `running` for a live-but-idle agent and so
/// would show Stop almost always. Only an actively-generating agent is "busy".
export function agentIsBusy(feed: FeedEvent[]): boolean {
  for (let i = feed.length - 1; i >= 0; i--) {
    const ev = feed[i];
    if (ev.producer === 'user') continue; // user inputs don't move the state
    const kind = ev.kind;
    if (kind === 'turn.result' || kind === 'completion' || kind === 'session.init') return false;
    if (kind === 'lifecycle') {
      const phase = str(ev.payload, 'phase');
      if (phase === 'exited' || phase === 'stopped') return false;
      continue; // other lifecycle phases are ambiguous — keep scanning
    }
    if (TURN_ACTIVE_KINDS.has(kind)) return true;
  }
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
