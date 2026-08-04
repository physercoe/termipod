/// `desktop_open` — the navigate class's decisions (coworking lane H,
/// ADR-064 §6). The counterweights to the one desktop-UI capability that moves
/// the user's screen, each pinned here: the policy column, the rate limit, the
/// attribution, and the honesty of what the agent is told afterwards. `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  clipOpenNote,
  decideOpen,
  OPEN_NOTE_MAX,
  OPEN_RATE_LIMIT,
  OPEN_RATE_WINDOW_MS,
  openResultText,
  openUnresolvedMessage,
  pruneOpenHistory,
} from './desktopopen.ts';
import { UI_POLICY } from '../../src/state/ui_policy.ts';

function input(over: Partial<Parameters<typeof decideOpen>[0]> = {}): Parameters<typeof decideOpen>[0] {
  return {
    ref: 'ui://replay?dataset_id=ds_1',
    note: '',
    agentId: 'ag_1',
    agentHandle: 'kimi-1',
    now: 1_700_000_000_000,
    recent: [],
    id: 'nav-1',
    iso: '2026-08-04T00:00:00.000Z',
    ...over,
  };
}

test('the navigate column is OPTIONAL, and its absence is what refuses a surface', () => {
  // The whole point of the shape (ADR-064 §12): terminal, settings, kimiweb and
  // vault are unreachable by having no column at all, not by a false bit that a
  // careless edit flips true. A row type of `'allow' | 'refuse'` would make the
  // dangerous edit a one-character change.
  for (const surface of ['terminal', 'settings', 'kimiweb', 'vault']) {
    assert.equal(
      Object.prototype.hasOwnProperty.call(UI_POLICY[surface], 'navigate'),
      false,
      `${surface} must not declare a navigate column at all`,
    );
    const out = decideOpen(input({ ref: `ui://${surface}` }));
    assert.equal(out.ok, false, surface);
    if (!out.ok) assert.equal(out.code, 'NAVIGATE_REFUSED', surface);
  }
  // …and the local-work surfaces do allow it, or the verb has nothing to open.
  for (const surface of ['fleet', 'projects', 'read', 'author', 'debug', 'compare', 'replay', 'record']) {
    assert.equal(UI_POLICY[surface].navigate, 'allow', surface);
    assert.equal(decideOpen(input({ ref: `ui://${surface}` })).ok, true, surface);
  }
});

test('the refusal says CANNOT, not "not allowed" — there is no setting that changes it', () => {
  const out = decideOpen(input({ ref: 'ui://settings' }));
  assert.equal(out.ok, false);
  if (out.ok) return;
  // An agent told "refused" retries; one told "cannot" moves on.
  assert.match(out.message, /cannot be opened by an agent/);
  assert.match(out.message, /ui_policy/);
});

test('an unparseable ref and an undeclared surface are different refusals', () => {
  const bad = decideOpen(input({ ref: 42 }));
  assert.equal(bad.ok, false);
  if (!bad.ok) {
    assert.equal(bad.code, 'INVALID_REF');
    // The message shows both spellings, because an agent that got the shape
    // wrong needs an example more than a diagnosis.
    assert.match(bad.message, /ui:\/\/replay/);
  }
  const unknown = decideOpen(input({ ref: 'ui://nonsense' }));
  assert.equal(unknown.ok, false);
  if (!unknown.ok) assert.equal(unknown.code, 'UNKNOWN_SURFACE');
});

test('the rate limit is TIGHTER than a highlight’s, and refusals do not spend it', () => {
  // A highlight is a glow the user can ignore; a navigation takes the screen.
  assert.ok(OPEN_RATE_LIMIT < 6, 'a navigation budget must be smaller than the highlight budget');
  const now = 1_700_000_000_000;
  const full = Array.from({ length: OPEN_RATE_LIMIT }, (_, i) => now - i * 1000);
  const limited = decideOpen(input({ now, recent: full }));
  assert.equal(limited.ok, false);
  if (!limited.ok) {
    assert.equal(limited.code, 'NAVIGATE_RATE_LIMITED');
    // Names the alternative rather than just saying no.
    assert.match(limited.message, /ui_highlight/);
  }
  // Timestamps outside the window do not count — the budget is per minute, not
  // per session.
  const stale = full.map((t) => t - OPEN_RATE_WINDOW_MS - 1);
  assert.equal(decideOpen(input({ now, recent: stale })).ok, true);
  assert.deepEqual(pruneOpenHistory(stale, now), []);
});

test('the order is parse → policy → rate, so a refused surface never spends budget', () => {
  const now = 1_700_000_000_000;
  const full = Array.from({ length: OPEN_RATE_LIMIT }, () => now);
  // Over budget AND refused by policy: the POLICY answer must win, because the
  // caller needs to learn the surface is unreachable rather than that it should
  // wait a minute and try the same impossible thing again.
  const out = decideOpen(input({ ref: 'ui://vault', now, recent: full }));
  assert.equal(out.ok, false);
  if (!out.ok) assert.equal(out.code, 'NAVIGATE_REFUSED');
});

test('attribution is never empty, whatever the caller sent', () => {
  const named = decideOpen(input());
  assert.equal(named.ok && named.order.by, 'kimi-1');
  const idOnly = decideOpen(input({ agentHandle: '' }));
  assert.equal(idOnly.ok && idOnly.order.by, 'ag_1');
  // An unattributed banner is the failure mode attribution exists to prevent:
  // a tab that changes with nobody's name on it reads as the app misbehaving.
  const anon = decideOpen(input({ agentHandle: '', agentId: '' }));
  assert.equal(anon.ok && anon.order.by, 'an agent');
});

test('the note is a caption, not a message channel', () => {
  const long = decideOpen(input({ note: 'x'.repeat(OPEN_NOTE_MAX + 50) }));
  assert.equal(long.ok && long.order.note.length, OPEN_NOTE_MAX);
  assert.equal(clipOpenNote('  a \n  b  '), 'a b');
});

test('the result sentence carries the DEPTH, and tells the agent not to overclaim', () => {
  const ref = { surface: 'replay', params: { dataset_id: 'ds_1', episode_id: '3' } };
  const entity = openResultText(ref, 'entity');
  assert.match(entity, /ds_1|replay/);
  assert.match(entity, /undo/);
  const surface = openResultText(ref, 'surface');
  // The honesty rule, executable: "I put you in Replay" and "I opened that
  // episode" are different sentences.
  assert.match(surface, /could not open/);
  assert.match(surface, /do not claim you opened it/);
  assert.match(openUnresolvedMessage(ref), /nothing on the user's screen changed/);
});
