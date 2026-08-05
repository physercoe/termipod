/// R3 — turn footers + context dividers. Run locally:
/// `node --test src/ui/turnMarkers.test.ts` from `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  contextDivider,
  fmtCost,
  fmtDuration,
  isContextBoundarySystem,
  turnFooter,
} from './turnMarkers.ts';

test('turn footer reads only what the engine reported', () => {
  const f = turnFooter({ status: 'success', duration_ms: 4312, message_count: 7, cost_usd: 0.0123 });
  assert.equal(f.durationMs, 4312);
  assert.equal(f.messages, 7);
  assert.equal(f.costUsd, 0.0123);
  assert.equal(f.failed, false);

  // codex reports no cost, and until E2 reported no duration either. Absent
  // stays absent — a footer showing "0ms · $0.00" would be a claim.
  const sparse = turnFooter({ status: 'completed' });
  assert.equal(sparse.durationMs, undefined);
  assert.equal(sparse.costUsd, undefined);
  assert.equal(sparse.messages, undefined);
});

test('a failed turn says so; an unreported status is not a failure', () => {
  assert.equal(turnFooter({ status: 'error' }).failed, true);
  assert.equal(turnFooter({ status: 'cancelled' }).failed, true);
  assert.equal(turnFooter({ status: 'success' }).failed, false);
  assert.equal(turnFooter({ status: 'completed' }).failed, false);
  assert.equal(turnFooter({ stop_reason: 'end_turn' }).failed, false);
  // Nothing reported is not a failure — most turn.result rows on some engines
  // carry no status at all, and painting those red would make every turn look
  // broken.
  assert.equal(turnFooter({}).failed, false);
  assert.equal(turnFooter({ status: '' }).failed, false);
});

test('the hub input-route markers become dividers', () => {
  assert.deepEqual(contextDivider('context.compacted', { verb: 'compact' }), {
    verb: 'compact',
    engineVerb: 'compact',
  });
  assert.equal(contextDivider('context.cleared', {})?.verb, 'clear');
  assert.equal(contextDivider('context.rewound', {})?.verb, 'rewind');
  // gemini calls it `compress` but the KIND is still `context.compacted`, so
  // the label follows the kind and the engine's own word rides along for the
  // hover — one vocabulary, no per-engine reading.
  const gemini = contextDivider('context.compacted', { verb: 'compress' });
  assert.equal(gemini?.verb, 'compact');
  assert.equal(gemini?.engineVerb, 'compress');
});

test("claude's compact_boundary is a divider with no token delta", () => {
  const d = contextDivider('system', { subtype: 'compact_boundary' });
  assert.equal(d?.verb, 'compact');
  assert.equal(d?.tokensBefore, undefined);
  assert.equal(d?.tokensAfter, undefined);
});

test("kimi's compaction is the one producer that reports the delta", () => {
  const d = contextDivider('system', {
    subtype: 'compaction',
    tokens_before: 120_000,
    tokens_after: 18_000,
    summary: 'condensed the refactor discussion',
  });
  assert.equal(d?.verb, 'compact');
  assert.equal(d?.tokensBefore, 120_000);
  assert.equal(d?.tokensAfter, 18_000);
  assert.equal(d?.summary, 'condensed the refactor discussion');
});

test('an ordinary system event is not a boundary', () => {
  // The rule this draws across the transcript means "the agent forgot
  // something here". A generic notice must never draw it.
  assert.equal(contextDivider('system', {}), undefined);
  assert.equal(contextDivider('system', { subtype: 'task_started', task_id: 't1' }), undefined);
  assert.equal(contextDivider('system', { text: 'hello' }), undefined);
  assert.equal(contextDivider('text', { subtype: 'compact_boundary' }), undefined);
  assert.equal(contextDivider('turn.result', {}), undefined);
});

test('isContextBoundarySystem only answers for system events', () => {
  assert.equal(isContextBoundarySystem('system', { subtype: 'compaction' }), true);
  assert.equal(isContextBoundarySystem('system', { subtype: 'task_started' }), false);
  // The `context.*` kinds are dividers too, but they are not `system` and are
  // not verbose-gated, so the feed-lens exemption must not claim them.
  assert.equal(isContextBoundarySystem('context.compacted', {}), false);
});

test('duration formats at the scale a reader thinks in', () => {
  assert.equal(fmtDuration(0), '0ms');
  assert.equal(fmtDuration(999), '999ms');
  assert.equal(fmtDuration(1000), '1s');
  assert.equal(fmtDuration(4312), '4.3s');
  assert.equal(fmtDuration(59_999), '59.9s');
  assert.equal(fmtDuration(60_000), '1m 0s');
  assert.equal(fmtDuration(3_600_000), '1h 0m');
  assert.equal(fmtDuration(-1), '');
});

test('sub-dollar turns keep their digits', () => {
  // Rounding to cents prints $0.00 for most turns, which reads as free.
  assert.equal(fmtCost(0.0123), '$0.0123');
  assert.equal(fmtCost(0), '$0.0000');
  assert.equal(fmtCost(1.5), '$1.50');
  assert.equal(fmtCost(12.345), '$12.35');
});
