import type { Entity } from '../hub/types';

/// The transcript header's persistent stats fold (#332) — model, latest token
/// snapshot, turn count, elapsed wall-time — extracted from AgentTranscript so
/// the subagent guard is unit-testable (the desktop mirror of mobile's
/// `FeedTelemetry`).
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
}

function str(e: Entity, k: string): string | undefined {
  const v = e[k];
  return typeof v === 'string' && v !== '' ? v : undefined;
}
function num(e: Entity, k: string): number | undefined {
  const v = e[k];
  return typeof v === 'number' && Number.isFinite(v) ? v : undefined;
}

export function foldTranscriptStats(feed: readonly StatsEvent[]): TranscriptStats {
  let model: string | undefined;
  let inTok = 0;
  let outTok = 0;
  let turns = 0;
  let firstTs: number | undefined;
  let lastTs: number | undefined;
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
    } else if (ev.kind === 'turn.result' && !subagent) turns += 1;
  }
  const elapsed = firstTs !== undefined && lastTs !== undefined && lastTs > firstTs ? lastTs - firstTs : undefined;
  return { model, inTok, outTok, turns, elapsed };
}
