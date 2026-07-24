/// #374 — the stats strip must ignore kimi M4 subagent-flagged terminal and
/// usage frames (the desktop mirror of mobile's feed_telemetry guard). Run
/// locally: `node --test src/state/transcriptStats.test.ts` from `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { foldTranscriptStats, type StatsEvent } from './transcriptStats.ts';

function ev(kind: string, payload: Record<string, unknown>, ts?: string): StatsEvent {
  return { kind, payload, ts };
}

test('subagent turn.result does not inflate the turn count', () => {
  const s = foldTranscriptStats([
    ev('turn.result', { reason: 'end_of_turn', status: 'success' }),
    ev('turn.result', { reason: 'end_of_turn', status: 'success', subagent: true, kimi_agent_id: 'agent-9' }),
  ]);
  assert.equal(s.turns, 1);
});

test('subagent usage does not clobber the main agent snapshot', () => {
  const s = foldTranscriptStats([
    ev('usage', { model: 'kimi-k2', input_tokens: 100, output_tokens: 10 }),
    ev('usage', { input_tokens: 999999, output_tokens: 88, subagent: true }),
  ]);
  assert.equal(s.model, 'kimi-k2');
  assert.equal(s.inTok, 100);
  assert.equal(s.outTok, 10);
});

test('main-agent events still fold; subagent rows still count toward elapsed', () => {
  const s = foldTranscriptStats([
    ev('session.init', { model: 'kimi-k2' }, '2026-06-02T00:00:00Z'),
    ev('turn.result', { status: 'success' }, '2026-06-02T00:00:10Z'),
    ev('usage', { input_tokens: 50, output_tokens: 5, subagent: true }, '2026-06-02T00:00:30Z'),
  ]);
  assert.equal(s.model, 'kimi-k2');
  assert.equal(s.inTok, 0);
  assert.equal(s.turns, 1);
  // Subagent rows are excluded from accounting but not from wall-time.
  assert.equal(s.elapsed, 30_000);
});
