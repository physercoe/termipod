import type { Entity } from '../hub/types';

/// The transcript header's persistent stats fold (#332) — model, latest token
/// snapshot, turn count, elapsed wall-time, context-window fill and cost —
/// extracted from AgentTranscript so the subagent guard is unit-testable (the
/// desktop mirror of mobile's `FeedTelemetry`).
///
/// kimi M4 wire-tail stamps subagent emissions with `subagent: true`. Their
/// `turn.result` / `usage` frames meter the subagent's inner loop, not the
/// session's turn — counting them inflates the turns count, and a subagent's
/// latest-wins usage snapshot clobbers the main agent's token numbers (#374).
/// Current mappers no longer emit subagent turn.result, but rows stored by
/// pre-fix hubs (and live rows from an old hub in a mixed-version fleet) still
/// carry them, so the fold skips them here too. Timestamps are NOT skipped —
/// subagent activity is real session wall-time.

export interface StatsEvent {
  kind: string;
  ts?: string;
  payload: Entity;
}

export interface TranscriptStats {
  model?: string;
  inTok: number;
  outTok: number;
  turns: number;
  elapsed?: number;
  /// Context-window capacity for the session's model, in tokens — whatever the
  /// engine last reported. Absent when no producer reported one, and the ring
  /// then suppresses itself rather than dividing by a guess (R2/D-4).
  contextWindow?: number;
  /// Tokens the model will start the NEXT turn from — the fill, not the
  /// session's cumulative spend. Absent when no producer reported it.
  contextUsed?: number;
  /// Running sum of `turn.result.cost_usd`, present only where the engine
  /// actually reports cost (claude does, codex does not). Never imputed here:
  /// the sanctioned approximation lives on the digest and is labeled there.
  costUsd?: number;
}

function str(e: Entity, k: string): string | undefined {
  const v = e[k];
  return typeof v === 'string' && v !== '' ? v : undefined;
}
function num(e: Entity, k: string): number | undefined {
  const v = e[k];
  return typeof v === 'number' && Number.isFinite(v) ? v : undefined;
}
function obj(e: Entity, k: string): Entity | undefined {
  const v = e[k];
  return v !== null && typeof v === 'object' && !Array.isArray(v) ? (v as Entity) : undefined;
}

/// True for codex's session-cumulative `usage` event, false for claude's
/// per-message one. The hub's codex frame profile stamps `cumulative` — as the
/// STRING "true", because the expression grammar has only string literals — so
/// both spellings are accepted (mobile `isCumulativeUsage`).
///
/// The distinction is load-bearing, not cosmetic: codex's numbers grow across
/// the whole session and claude's describe one API call, so reading a codex
/// event as a claude one reports a context fill several times the real value.
/// That exact bug shipped on mobile (a session at ~19K tokens showing 169K).
function isCumulativeUsage(p: Entity): boolean {
  const v = p['cumulative'];
  if (typeof v === 'boolean') return v;
  return typeof v === 'string' && v.toLowerCase() === 'true';
}

/// The claude-shaped prompt size for one API call: what the model was handed,
/// including the cached prefix. This is the number claude's own `/context`
/// reports, so the ring agrees with the engine's own view.
///
/// Both key spellings are accepted because both are on the wire: the M4 mapper
/// emits `cache_read` / `cache_create`, raw stream-json carries
/// `cache_read_input_tokens` / `cache_creation_input_tokens`.
function promptTokens(p: Entity): number | undefined {
  const input = num(p, 'input_tokens');
  if (input === undefined) return undefined;
  const cacheRead = num(p, 'cache_read') ?? num(p, 'cache_read_input_tokens') ?? 0;
  const cacheCreate = num(p, 'cache_create') ?? num(p, 'cache_creation_input_tokens') ?? 0;
  return input + cacheRead + cacheCreate;
}

/// The per-model capacity claude reports on `turn.result.by_model`. Claude M2
/// and M4 both carry the window there rather than on `usage`, and a turn can
/// name several models (a Haiku subagent alongside the main model), so the
/// dominant one — most output tokens — is the session's model. Mirrors mobile's
/// `feed_telemetry` dominant-model pick.
///
/// An exact tie means no model did more of the work, so no pick is more
/// correct than another; the strict `>` resolves it to the first in wire order.
/// What the ring actually needs from a tie is only that the answer not change
/// between renders of the same event — a capacity that flips would make the
/// fill jump with no new data behind it.
function dominantContextWindow(byModel: Entity): number | undefined {
  let best: number | undefined;
  let bestOutput = -1;
  for (const raw of Object.values(byModel)) {
    if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) continue;
    const entry = raw as Entity;
    const window = num(entry, 'context_window');
    if (window === undefined || window <= 0) continue;
    const output = num(entry, 'output') ?? num(entry, 'output_tokens') ?? 0;
    if (output > bestOutput) {
      best = window;
      bestOutput = output;
    }
  }
  return best;
}

export function foldTranscriptStats(feed: readonly StatsEvent[]): TranscriptStats {
  let model: string | undefined;
  let inTok = 0;
  let outTok = 0;
  let turns = 0;
  let firstTs: number | undefined;
  let lastTs: number | undefined;
  let contextWindow: number | undefined;
  let contextUsed: number | undefined;
  let costUsd: number | undefined;
  for (const ev of feed) {
    if (ev.ts !== undefined) {
      const ts = Date.parse(ev.ts);
      if (!Number.isNaN(ts)) {
        if (firstTs === undefined) firstTs = ts;
        lastTs = ts;
      }
    }
    const subagent = ev.payload['subagent'] === true;
    if (ev.kind === 'session.init') model = str(ev.payload, 'model') ?? model;
    else if (ev.kind === 'usage' && !subagent) {
      model = str(ev.payload, 'model') ?? model;
      inTok = num(ev.payload, 'input_tokens') ?? inTok;
      outTok = num(ev.payload, 'output_tokens') ?? outTok;
      const window = num(ev.payload, 'context_window');
      if (window !== undefined && window > 0) contextWindow = window;
      // Two different quantities arrive under one kind — see
      // isCumulativeUsage. Codex reports the whole session, so the fill is its
      // LAST turn's total (what the next turn starts from); the cumulative
      // total is the legacy fallback for rows written before the hub emitted
      // `last_total_tokens`. Claude reports one call, so the fill is that
      // call's prompt.
      const used = isCumulativeUsage(ev.payload)
        ? num(ev.payload, 'last_total_tokens') ?? num(ev.payload, 'total_tokens')
        : promptTokens(ev.payload);
      if (used !== undefined && used > 0) contextUsed = used;
    } else if (ev.kind === 'turn.result' && !subagent) {
      turns += 1;
      const cost = num(ev.payload, 'cost_usd');
      if (cost !== undefined) costUsd = (costUsd ?? 0) + cost;
      const byModel = obj(ev.payload, 'by_model');
      if (byModel !== undefined && contextWindow === undefined) {
        contextWindow = dominantContextWindow(byModel) ?? contextWindow;
      }
    } else if (ev.kind === 'status_line' && !subagent) {
      // antigravity's M4 tap ships tokens ONLY here — its transcript carries
      // none — under a nested block whose field names match claude's `usage`.
      // Latest-wins, same as every other producer.
      const cw = obj(ev.payload, 'context_window');
      if (cw !== undefined) {
        const size = num(cw, 'context_window_size');
        if (size !== undefined && size > 0) contextWindow = size;
        const current = obj(cw, 'current_usage');
        if (current !== undefined) {
          const used = promptTokens(current);
          if (used !== undefined && used > 0) contextUsed = used;
        }
      }
    }
  }
  const elapsed = firstTs !== undefined && lastTs !== undefined && lastTs > firstTs ? lastTs - firstTs : undefined;
  return { model, inTok, outTok, turns, elapsed, contextWindow, contextUsed, costUsd };
}

/// Fill bands. The numbers are mobile's (`telemetry_strip.dart`) so the same
/// session reads the same on both clients — past ~90% the next big response
/// spills, which is the moment to compact or branch.
export type ContextBand = 'ok' | 'warn' | 'high';

export interface ContextFill {
  window: number;
  used: number;
  /// 0..1, clamped. A producer can report a used count above the window (a
  /// stale window from an earlier model, say); clamping keeps the ring from
  /// drawing past full, and the raw numbers stay readable beside it.
  pct: number;
  band: ContextBand;
}

/// The ring's input, or undefined when there is nothing honest to draw.
///
/// Capacity alone is not enough: without a used count the ring would render an
/// empty circle, which reads as "0% full" rather than "unknown". Both numbers
/// or nothing.
export function contextFill(stats: TranscriptStats): ContextFill | undefined {
  const { contextWindow: window, contextUsed: used } = stats;
  if (window === undefined || window <= 0 || used === undefined || used < 0) return undefined;
  const pct = Math.min(1, used / window);
  return { window, used, pct, band: pct >= 0.9 ? 'high' : pct >= 0.7 ? 'warn' : 'ok' };
}

/// The slash command that compacts this engine's context, or undefined where
/// the hub has no mapping for the engine. Takes the ENGINE FAMILY
/// (`agentEngine`), not the agent's kind — a steward's kind is its template.
///
/// Deliberately the same table the hub's `detectContextMutation` keys on
/// (`server/context_mutation.go`): those are the engines whose command the hub
/// recognizes and records as a `context.compacted` marker. Offering it for an
/// engine outside that set would send a line of text that may do nothing and
/// would leave no mark in the transcript either way — a button that might not
/// be a button. Codex is outside it today, so codex sessions get the ring
/// without the shortcut (D-4), and the answer changes here the same day it
/// changes in the Go table.
export function compactCommandFor(engine: string | undefined): string | undefined {
  switch (engine) {
    case 'claude-code':
      return '/compact';
    case 'gemini-cli':
      return '/compress';
    default:
      return undefined;
  }
}
