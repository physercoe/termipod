/// #374 — the stats strip must ignore kimi M4 subagent-flagged terminal and
/// usage frames (the desktop mirror of mobile's feed_telemetry guard). Run
/// locally: `node --test src/state/transcriptStats.test.ts` from `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compactCommandFor, contextFill, foldTranscriptStats, type StatsEvent } from './transcriptStats.ts';

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

/// R2 — context-window fill. The fold has to tell two different quantities
/// apart under one event kind, and getting it wrong is invisible: the ring
/// still draws, just against the wrong denominator.

test('codex cumulative usage: fill is the LAST turn, not the session total', () => {
  const s = foldTranscriptStats([
    ev('usage', {
      cumulative: 'true',
      engine: 'codex',
      context_window: 258_400,
      total_tokens: 169_000,
      last_total_tokens: 19_986,
    }),
  ]);
  assert.equal(s.contextWindow, 258_400);
  // Reading `total_tokens` here is the v1.0.712 mobile regression: a session
  // whose real fill was ~19K showed 169K because every turn re-sends the
  // conversation.
  assert.equal(s.contextUsed, 19_986);
});

test('codex usage without last_total_tokens falls back to the cumulative total', () => {
  const s = foldTranscriptStats([
    ev('usage', { cumulative: true, context_window: 258_400, total_tokens: 12_000 }),
  ]);
  assert.equal(s.contextUsed, 12_000);
});

test('claude per-message usage: fill is the prompt, cache included', () => {
  const s = foldTranscriptStats([
    ev('usage', { model: 'claude-opus-5', input_tokens: 1_200, cache_read: 40_000, cache_create: 800, context_window: 200_000 }),
  ]);
  assert.equal(s.contextWindow, 200_000);
  // The number claude's own /context reports — anything less understates the
  // fill by the entire cached prefix.
  assert.equal(s.contextUsed, 42_000);
});

test('claude raw stream-json cache key spellings are accepted too', () => {
  const s = foldTranscriptStats([
    ev('usage', { input_tokens: 100, cache_read_input_tokens: 900, cache_creation_input_tokens: 50 }),
  ]);
  assert.equal(s.contextUsed, 1_050);
});

test('capacity falls back to the dominant model on turn.result.by_model', () => {
  const s = foldTranscriptStats([
    ev('turn.result', {
      status: 'success',
      by_model: {
        'claude-haiku-4-5': { output: 12, context_window: 100_000 },
        'claude-opus-5': { output: 4_000, context_window: 200_000 },
      },
    }),
  ]);
  // The subagent model produced almost nothing; the session's model is the one
  // that did the work.
  assert.equal(s.contextWindow, 200_000);
});

test('a by_model tie resolves stably to wire order', () => {
  // Before any model has produced output there is no dominant one, so the pick
  // is arbitrary — the requirement is that it not change between renders of
  // the same event, which is what a strict `>` gives.
  const s = foldTranscriptStats([
    ev('turn.result', {
      status: 'success',
      by_model: { first: { output: 0, context_window: 111 }, second: { output: 0, context_window: 222 } },
    }),
  ]);
  assert.equal(s.contextWindow, 111);
});

test('a usage-reported window wins over the by_model fallback', () => {
  const s = foldTranscriptStats([
    ev('usage', { input_tokens: 10, context_window: 200_000 }),
    ev('turn.result', { status: 'success', by_model: { m: { output: 1, context_window: 999 } } }),
  ]);
  assert.equal(s.contextWindow, 200_000);
});

test('antigravity status_line is the only token source for its engine', () => {
  const s = foldTranscriptStats([
    ev('status_line', {
      context_window: {
        context_window_size: 1_000_000,
        current_usage: { input_tokens: 5_000, cache_read_input_tokens: 20_000, cache_creation_input_tokens: 0 },
      },
    }),
  ]);
  assert.equal(s.contextWindow, 1_000_000);
  assert.equal(s.contextUsed, 25_000);
});

test('subagent usage does not move the context fill', () => {
  const s = foldTranscriptStats([
    ev('usage', { input_tokens: 1_000, context_window: 200_000 }),
    ev('usage', { input_tokens: 190_000, context_window: 200_000, subagent: true }),
  ]);
  assert.equal(s.contextUsed, 1_000);
});

test('cost sums turn.result and stays absent when no engine reports it', () => {
  const withCost = foldTranscriptStats([
    ev('turn.result', { status: 'success', cost_usd: 0.012 }),
    ev('turn.result', { status: 'success', cost_usd: 0.008 }),
    ev('turn.result', { status: 'success', cost_usd: 0.5, subagent: true }),
  ]);
  assert.ok(withCost.costUsd !== undefined && Math.abs(withCost.costUsd - 0.02) < 1e-9);
  // codex ships no cost_usd; absence must stay absence rather than becoming a
  // confident $0.00 (D-4 — the imputed figure lives on the digest, labeled).
  const noCost = foldTranscriptStats([ev('turn.result', { status: 'success' })]);
  assert.equal(noCost.costUsd, undefined);
});

test('contextFill needs both halves and bands at mobile thresholds', () => {
  assert.equal(contextFill({ inTok: 0, outTok: 0, turns: 0 }), undefined);
  // Capacity with no reading would draw an empty ring, which reads as 0% full
  // rather than "not reported".
  assert.equal(contextFill({ inTok: 0, outTok: 0, turns: 0, contextWindow: 1000 }), undefined);
  assert.equal(contextFill({ inTok: 0, outTok: 0, turns: 0, contextUsed: 500 }), undefined);

  const at = (used: number): string | undefined =>
    contextFill({ inTok: 0, outTok: 0, turns: 0, contextWindow: 1000, contextUsed: used })?.band;
  assert.equal(at(0), 'ok');
  assert.equal(at(699), 'ok');
  assert.equal(at(700), 'warn');
  assert.equal(at(899), 'warn');
  assert.equal(at(900), 'high');
});

test('an over-full reading clamps the arc but keeps the raw numbers', () => {
  const f = contextFill({ inTok: 0, outTok: 0, turns: 0, contextWindow: 1000, contextUsed: 4000 });
  assert.equal(f?.pct, 1);
  assert.equal(f?.used, 4000);
  assert.equal(f?.band, 'high');
});

test('compactCommandFor tracks the hub table and refuses to guess', () => {
  assert.equal(compactCommandFor('claude-code'), '/compact');
  assert.equal(compactCommandFor('gemini-cli'), '/compress');
  // codex has no entry in server/context_mutation.go, so the shortcut would
  // send text the hub does not record and the engine may ignore.
  assert.equal(compactCommandFor('codex'), undefined);
  assert.equal(compactCommandFor(undefined), undefined);
  // A steward's agent kind is its template, never an engine — passing one in
  // must not accidentally match a prefix.
  assert.equal(compactCommandFor('steward.general'), undefined);
});
