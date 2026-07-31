/// Tests for the gated-screenshot core (D3 — plan §3.3, ADR-062 D-3/D-4).
/// The three rules that make a screenshot safe are all decided in uicapture.ts,
/// so they are all provable here without Electron: refusal by table, per-call
/// approval (never a session grant), and fail-closed decision reading.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { UI_POLICY, uiPolicyFor } from '../../src/state/ui_policy.ts';
import {
  captureApprovalCard,
  captureDenialMessage,
  captureRefusal,
  parseScreenshotArgs,
  readCaptureDecision,
  surfaceForPartition,
  visibleSurfaces,
} from './uicapture.ts';

// ── What is on screen ────────────────────────────────────────────────────────

test('visibleSurfaces reads both panes, and answers "unknown" rather than "empty"', () => {
  assert.deepEqual(visibleSurfaces({ surface: 'read', captured_at: 'x' }), ['read']);
  assert.deepEqual(
    visibleSurfaces({ surface: 'read', secondary: { surface: 'debug' }, active_pane: 'secondary', captured_at: 'x' }),
    ['read', 'debug'],
  );
  // No snapshot yet, or one without a usable surface: null, NOT [] — the
  // caller must be able to tell "nothing sensitive on screen" from "we have no
  // idea what is on screen", because those get opposite answers.
  assert.equal(visibleSurfaces(null), null);
  assert.equal(visibleSurfaces({ captured_at: 'x' }), null);
  assert.equal(visibleSurfaces({ surface: '', captured_at: 'x' }), null);
  // A malformed secondary is ignored rather than fabricating a pane.
  assert.deepEqual(visibleSurfaces({ surface: 'read', secondary: { nope: 1 } }), ['read']);
});

// ── Refusal by table ─────────────────────────────────────────────────────────

test('captureRefusal: the policy table decides, and refuses what it does not declare', () => {
  assert.equal(captureRefusal(['read']), null);
  assert.equal(captureRefusal(['read', 'debug', 'replay']), null);
  // A pixel capture of settings shows its VALUES even though its snapshot row
  // emits only the surface id — the columns are independent (ADR-062 D-3).
  assert.equal(captureRefusal(['settings']), 'settings');
  assert.equal(captureRefusal(['vault']), 'vault');
  // The gate covers BOTH panes: a split cannot launder the vault past it by
  // putting it in the half the user is not focused on.
  assert.equal(captureRefusal(['read', 'vault']), 'vault');
  // Default-correct for the unknown: a surface with no row is not capturable.
  assert.equal(captureRefusal(['surface-added-next-quarter']), 'surface-added-next-quarter');
});

test('the sensitive surfaces are exactly the ones that refuse pixels', () => {
  const refusing = Object.entries(UI_POLICY)
    .filter(([, row]) => row.capture === 'refuse')
    .map(([surface]) => surface)
    .sort();
  // Stated as an invariant, not as a snapshot of today's table: adding a row
  // that refuses capture is a deliberate privacy decision and should have to
  // touch this line.
  assert.deepEqual(refusing, ['settings', 'vault']);
});

test('surfaceForPartition maps every guest kind onto a real policy row', () => {
  assert.equal(surfaceForPartition('persist:webtab'), 'read');
  assert.equal(surfaceForPartition('kimiweb'), 'kimiweb');
  assert.equal(surfaceForPartition('rerunweb'), 'replay');
  // An unlisted partition opts IN by naming its surface — it never inherits
  // one (the webtab_policy discipline).
  assert.equal(surfaceForPartition('persist:something-new'), null);
  assert.equal(surfaceForPartition(null), null);
  // Every mapping must land on a declared row, or the guest leg would refuse
  // for a reason nobody intended.
  for (const partition of ['persist:webtab', 'kimiweb', 'rerunweb']) {
    const surface = surfaceForPartition(partition);
    assert.ok(surface !== null && uiPolicyFor(surface) !== null, partition);
  }
});

// ── Arguments ────────────────────────────────────────────────────────────────

test('parseScreenshotArgs: absent tabId means the window; junk is refused', () => {
  assert.deepEqual(parseScreenshotArgs({}), { tabId: null });
  assert.deepEqual(parseScreenshotArgs({ tabId: null }), { tabId: null });
  assert.deepEqual(parseScreenshotArgs({ tabId: 7 }), { tabId: 7 });
  assert.ok('error' in parseScreenshotArgs({ tabId: '7' }));
  assert.ok('error' in parseScreenshotArgs({ tabId: 1.5 }));
});

// ── The approval card ────────────────────────────────────────────────────────

test('the approval card names what was asked for and carries no pixels', () => {
  const card = captureApprovalCard({
    agentId: 'ag_1',
    agentHandle: 'kimi-1',
    scope: 'window',
    surfaces: ['read', 'debug'],
    url: null,
  });
  assert.match(card.summary, /kimi-1/);
  assert.match(card.summary, /read \+ debug/);
  assert.equal(card.payload.tool, 'ui_screenshot');
  assert.deepEqual(card.payload.surfaces, ['read', 'debug']);
  // §3.3: screenshots never get a standing grant, so the card says so in the
  // payload as well as by omitting the session option in the UI.
  assert.equal(card.payload.session_grant, false);
  // The payload is a REFERENCE to what was requested — the image does not
  // exist yet, and nothing content-shaped may ride along (ADR-062 D-2).
  for (const key of Object.keys(card.payload)) {
    assert.ok(!['image', 'data', 'data_b64', 'png', 'preview'].includes(key), key);
  }
});

test('the card falls back to the agent id, then to a neutral subject', () => {
  const byId = captureApprovalCard({ agentId: 'ag_1', agentHandle: '', scope: 'window', surfaces: ['read'], url: null });
  assert.match(byId.summary, /^ag_1 wants/);
  const anon = captureApprovalCard({ agentId: '', agentHandle: '', scope: 'tab', surfaces: ['read'], url: 'https://a.b/c' });
  assert.match(anon.summary, /^An agent wants/);
  assert.match(anon.summary, /https:\/\/a\.b\/c/);
});

// ── Fail-closed decisions ────────────────────────────────────────────────────

test('readCaptureDecision: only an explicit trailing approve approves', () => {
  assert.equal(readCaptureDecision({ status: 'open', decisions: [] }), 'pending');
  // Quorum not yet met: an approve on a still-open row is not a decision.
  assert.equal(readCaptureDecision({ status: 'open', decisions: [{ decision: 'approve' }] }), 'pending');
  assert.equal(readCaptureDecision({ status: 'resolved', decisions: [{ decision: 'approve' }] }), 'approve');
  assert.equal(readCaptureDecision({ status: 'resolved', decisions: [{ decision: 'reject' }] }), 'deny');
  // Dismissed through /resolve — resolved with no decision at all. Denies.
  assert.equal(readCaptureDecision({ status: 'resolved' }), 'deny');
  assert.equal(readCaptureDecision({ status: 'resolved', decisions: [] }), 'deny');
  // The LAST decision wins (an override after a reject, say).
  assert.equal(readCaptureDecision({ status: 'resolved', decisions: [{ decision: 'reject' }, { decision: 'approve' }] }), 'approve');
  assert.equal(readCaptureDecision({ status: 'resolved', decisions: [{ decision: 'approve' }, { decision: 'reject' }] }), 'deny');
  // Shapes we cannot read: an unparseable resolved row denies; a body we
  // cannot read at all is not evidence of anything, so it keeps waiting.
  assert.equal(readCaptureDecision({ status: 'resolved', decisions: 'nope' }), 'deny');
  assert.equal(readCaptureDecision({ status: 'resolved', decisions: [42] }), 'deny');
  assert.equal(readCaptureDecision(null), 'pending');
  assert.equal(readCaptureDecision('nope'), 'pending');
});

test('denial messages point the agent at the cheaper representation', () => {
  assert.match(captureDenialMessage('denied'), /denied/);
  assert.match(captureDenialMessage('timeout'), /approval window/);
  assert.match(captureDenialMessage('unavailable'), /ui_get_focus/);
});
