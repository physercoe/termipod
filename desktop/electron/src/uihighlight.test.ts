/// Tests for agent pointing (D6 — docs/plans/desktop-ui-context-and-pointing.md
/// §3.4b, ADR-062 D-5). `ui_highlight` is the weakest capability in the plan —
/// it draws a glow and expires — so what needs proving is not that it works
/// but that it cannot become attention spam or fake UI: the policy bit binds,
/// the attribution is never empty, the TTL is ours, and the rate limit counts
/// abuse rather than refusals. `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  clampHighlightTtl,
  clipHighlightNote,
  decideHighlight,
  pruneHighlightHistory,
  HIGHLIGHT_NOTE_MAX,
  HIGHLIGHT_RATE_LIMIT,
  HIGHLIGHT_RATE_WINDOW_MS,
  HIGHLIGHT_TTL_DEFAULT_MS,
  HIGHLIGHT_TTL_MAX_MS,
  type HighlightInput,
} from './uihighlight.ts';

const NOW = 1_800_000_000_000;

function input(over: Partial<HighlightInput> = {}): HighlightInput {
  return {
    ref: 'ui://replay?dataset_id=ds_1&episode_id=ep_2&cursor=1234',
    note: 'this episode',
    ttlMs: null,
    agentId: 'ag_1',
    agentHandle: 'kimi-1',
    now: NOW,
    recent: [],
    id: 'hl-1',
    iso: '2026-07-31T00:00:00.000Z',
    ...over,
  };
}

// ── The policy bit binds ─────────────────────────────────────────────────────

test('the highlight column decides which surfaces may be drawn over', () => {
  const ok = decideHighlight(input());
  assert.equal(ok.ok, true);
  if (ok.ok) assert.equal(ok.order.ref.surface, 'replay');

  // Vault refuses everything, always (ADR-062 D-3).
  const vault = decideHighlight(input({ ref: 'ui://vault' }));
  assert.equal(vault.ok, false);
  if (!vault.ok) assert.equal(vault.code, 'HIGHLIGHT_REFUSED');

  // Settings allows highlight even though it refuses CAPTURE — the columns
  // are independent because the sensitivities are.
  assert.equal(decideHighlight(input({ ref: 'ui://settings' })).ok, true);

  // A surface with no row is not annotatable: the table is the allowlist.
  const unknown = decideHighlight(input({ ref: 'ui://surface-from-the-future' }));
  assert.equal(unknown.ok, false);
  if (!unknown.ok) assert.equal(unknown.code, 'UNKNOWN_SURFACE');
});

test('both UIRef spellings resolve to the same thing', () => {
  const uri = decideHighlight(input({ ref: 'ui://replay?dataset_id=ds_1' }));
  const json = decideHighlight(input({ ref: { surface: 'replay', entity: { dataset_id: 'ds_1' } } }));
  assert.equal(uri.ok && json.ok, true);
  if (uri.ok && json.ok) assert.deepEqual(uri.order.ref, json.order.ref);
});

test('an unparseable ref is refused with an actionable message', () => {
  for (const bad of [null, 42, 'not a ref', {}, { surface: 'Replay!' }, []]) {
    const out = decideHighlight(input({ ref: bad }));
    assert.equal(out.ok, false, JSON.stringify(bad));
    if (!out.ok) {
      assert.equal(out.code, 'INVALID_REF');
      // The message must show the shape, not just name it.
      assert.match(out.message, /ui:\/\//);
    }
  }
});

// ── Attribution, TTL, note ───────────────────────────────────────────────────

test('a highlight is never unattributed', () => {
  const handle = decideHighlight(input());
  assert.equal(handle.ok && handle.order.by, 'kimi-1');
  const byId = decideHighlight(input({ agentHandle: '' }));
  assert.equal(byId.ok && byId.order.by, 'ag_1');
  // Even a caller with no identity at all gets a subject — an anonymous glow
  // is the fake-UI failure mode the plan's risk section names.
  const anon = decideHighlight(input({ agentHandle: '', agentId: '' }));
  assert.equal(anon.ok && anon.order.by, 'an agent');
});

test('the TTL is ours, not the caller argument', () => {
  assert.equal(clampHighlightTtl(null), HIGHLIGHT_TTL_DEFAULT_MS);
  assert.equal(clampHighlightTtl(2000), 2000);
  // An agent cannot pin a marker to the screen.
  assert.equal(clampHighlightTtl(60 * 60 * 1000), HIGHLIGHT_TTL_MAX_MS);
  // …nor make one flash by for a frame.
  assert.equal(clampHighlightTtl(1), 500);
  const out = decideHighlight(input({ ttlMs: 999_999 }));
  assert.equal(out.ok && out.order.ttl_ms, HIGHLIGHT_TTL_MAX_MS);
});

test('the note is a caption, not a message channel', () => {
  assert.equal(clipHighlightNote('  look   at\nthis '), 'look at this');
  const long = clipHighlightNote('y'.repeat(500));
  assert.equal(long.length, HIGHLIGHT_NOTE_MAX);
  assert.ok(long.endsWith('…'));
  const out = decideHighlight(input({ note: 'z'.repeat(1000) }));
  assert.ok(out.ok && out.order.note.length <= HIGHLIGHT_NOTE_MAX);
});

// ── Rate limiting ────────────────────────────────────────────────────────────

test('an agent cannot paper the screen', () => {
  const recent = Array.from({ length: HIGHLIGHT_RATE_LIMIT }, (_, i) => NOW - i * 1000);
  const out = decideHighlight(input({ recent }));
  assert.equal(out.ok, false);
  if (!out.ok) {
    assert.equal(out.code, 'HIGHLIGHT_RATE_LIMITED');
    // The refusal suggests the alternative rather than just saying no.
    assert.match(out.message, /words/);
  }

  // One short of the limit still passes…
  assert.equal(decideHighlight(input({ recent: recent.slice(1) })).ok, true);
  // …and the window slides: old timestamps do not count.
  const stale = recent.map((t) => t - HIGHLIGHT_RATE_WINDOW_MS);
  assert.equal(decideHighlight(input({ recent: stale })).ok, true);
});

test('history pruning keeps the store bounded without a sweeper', () => {
  const recent = [NOW - HIGHLIGHT_RATE_WINDOW_MS * 2, NOW - 1000, NOW];
  assert.deepEqual(pruneHighlightHistory(recent, NOW), [NOW - 1000, NOW]);
  assert.deepEqual(pruneHighlightHistory([], NOW), []);
});

test('a refusal does not consume the budget', () => {
  // The limit exists to stop a loop, not to punish a mistake: an agent that
  // mistypes a ref five times must still be able to point once it gets it
  // right. (The host enforces this by writing back the PRUNED history on a
  // refusal and the appended one only on success — this test pins the
  // decision half: a refusal returns before the counter is read.)
  const recent = Array.from({ length: HIGHLIGHT_RATE_LIMIT - 1 }, () => NOW);
  const bad = decideHighlight(input({ ref: 'nonsense', recent }));
  assert.equal(bad.ok, false);
  if (!bad.ok) assert.equal(bad.code, 'INVALID_REF', 'the ref check must precede the rate check');
  const refused = decideHighlight(input({ ref: 'ui://vault', recent }));
  if (!refused.ok) assert.equal(refused.code, 'HIGHLIGHT_REFUSED', 'policy refusal must precede the rate check');
});
