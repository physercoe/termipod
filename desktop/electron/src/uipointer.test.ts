/// Tests for element-resolved pointing (D4 —
/// docs/plans/desktop-ui-context-and-pointing.md §3.4 step 5). The whole CDP
/// sequence runs against a fake sender, the same seam annotation.test.ts uses
/// for the kimi injection — so the hit-test → backendNodeId → @eN → role/name
/// chain is provable without a browser. `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { POINTER_INTERACTIVE_SELECTOR, clipName, pointerHitScript, resolvePointer } from './uipointer.ts';
import { registerAnnotationRef } from './browserbridge.ts';
import type { CdpSend } from './annotation.ts';

/// A page with one button (backendNodeId 42) that the AX tree names, plus a
/// static-text node that mints no ref.
const AX_NODES = [
  { nodeId: '1', role: { value: 'WebArea' }, name: { value: 'deploys' }, childIds: ['2', '3'] },
  { nodeId: '2', role: { value: 'button' }, name: { value: 'Deploy' }, backendDOMNodeId: 42 },
  { nodeId: '3', role: { value: 'StaticText' }, name: { value: 'last run 3m ago' }, backendDOMNodeId: 43 },
];

interface FakeOpts {
  /// The object the hit-test resolves to; null models "nothing under there".
  hitObjectId?: string | null;
  backendNodeId?: number;
  nodeName?: string;
  /// Fail this method to model a page that answers nothing useful.
  failMethod?: string;
  partialNodes?: unknown[];
}

function fakeSend(opts: FakeOpts = {}): CdpSend & { calls: string[] } {
  const calls: string[] = [];
  const send = async (method: string, params?: Record<string, unknown>): Promise<unknown> => {
    calls.push(method);
    if (method === opts.failMethod) throw new Error(`${method} unavailable`);
    switch (method) {
      case 'DOM.getDocument':
        return { root: { nodeId: 1 } };
      case 'Runtime.evaluate':
        return opts.hitObjectId === null ? { result: { subtype: 'null' } } : { result: { objectId: opts.hitObjectId ?? 'obj-1' } };
      case 'DOM.requestNode':
        return { nodeId: 77 };
      case 'DOM.describeNode':
        return { node: { backendNodeId: opts.backendNodeId ?? 42, nodeName: opts.nodeName ?? 'BUTTON' } };
      case 'Runtime.releaseObject':
        return {};
      case 'Accessibility.getFullAXTree':
        return { nodes: AX_NODES };
      case 'Accessibility.getPartialAXTree': {
        if (opts.partialNodes !== undefined) return { nodes: opts.partialNodes };
        const backend = params?.backendNodeId;
        return { nodes: AX_NODES.filter((n) => n.backendDOMNodeId === backend) };
      }
      default:
        throw new Error(`unexpected CDP method ${method}`);
    }
  };
  return Object.assign(send, { calls });
}

// ── The hit-test expression ──────────────────────────────────────────────────

test('the hit script rounds coordinates and climbs to the interactive ancestor', () => {
  const script = pointerHitScript(120.4, 88.6);
  assert.match(script, /elementFromPoint\(120,89\)/);
  // The user drags over a button's LABEL and means the button.
  assert.match(script, /closest\(/);
  assert.ok(script.includes(JSON.stringify(POINTER_INTERACTIVE_SELECTOR)));
  // No interpolation seam: the selector is JSON-quoted and the coordinates are
  // numbers, so nothing string-shaped from a caller reaches the evaluator.
  assert.ok(!script.includes('${'));
});

// ── The happy path ───────────────────────────────────────────────────────────

test('resolvePointer names the element and hands back the node to register', async () => {
  const send = fakeSend();
  const out = await resolvePointer(send, 3, { x: 100, y: 50 }, { actionable: true });
  assert.ok(out !== null);
  // No ref here: the CALLER registers the node with the bridge (merge, never
  // clobbering an agent-held snapshot map) and fills in the returned name.
  assert.deepEqual(out.pointer, { tab_id: 3, role: 'button', name: 'Deploy', actionable: true });
  // Interactivity was decided by the same compaction browser_snapshot uses.
  assert.equal(out.refBackendNodeId, 42);
  // The object handle is always released, even on the happy path.
  assert.ok(send.calls.includes('Runtime.releaseObject'));
});

test('a read-only partition is reported as such, not silently promised', async () => {
  const out = await resolvePointer(fakeSend(), 9, { x: 1, y: 1 }, { actionable: false });
  assert.equal(out?.pointer.actionable, false);
  assert.equal(out?.refBackendNodeId, 42, 'a ref is still useful as a reference');
});

// ── Honest degradation ───────────────────────────────────────────────────────

test('nothing under the point → no pointer, not a fabricated one', async () => {
  assert.equal(await resolvePointer(fakeSend({ hitObjectId: null }), 3, { x: 0, y: 0 }, { actionable: true }), null);
});

test('a non-interactive element resolves without a node to register', async () => {
  // backendNodeId 43 is the static text: the AX tree names it, but
  // compactAxTree mints refs for interactive roles only — and a ref-less
  // resolution must not touch the tab's ref map at all.
  const out = await resolvePointer(fakeSend({ backendNodeId: 43, nodeName: 'SPAN' }), 3, { x: 0, y: 0 }, { actionable: true });
  assert.equal(out?.refBackendNodeId, null);
  assert.equal(out?.pointer.ref, undefined);
  assert.equal(out?.pointer.role, 'statictext');
  assert.equal(out?.pointer.name, 'last run 3m ago');
});

test('no accessibility node → the DOM tag name, never an invented role', async () => {
  const out = await resolvePointer(fakeSend({ backendNodeId: 99, nodeName: 'CANVAS', partialNodes: [] }), 3, { x: 0, y: 0 }, {
    actionable: true,
  });
  assert.equal(out?.pointer.role, 'canvas');
  assert.equal(out?.pointer.name, undefined);
  assert.equal(out?.refBackendNodeId, null);
});

test('a failing accessibility call degrades to the tag name instead of throwing', async () => {
  const out = await resolvePointer(fakeSend({ failMethod: 'Accessibility.getPartialAXTree' }), 3, { x: 0, y: 0 }, {
    actionable: true,
  });
  // The full tree still decided interactivity; only role/name fell back.
  assert.equal(out?.refBackendNodeId, 42);
  assert.equal(out?.pointer.role, 'button');
});

test('a CDP failure propagates — the HOST decides a pointer is optional, not this', async () => {
  const send = fakeSend({ failMethod: 'DOM.describeNode' });
  await assert.rejects(() => resolvePointer(send, 3, { x: 0, y: 0 }, { actionable: true }), /DOM.describeNode unavailable/);
  // …and the object handle is still released on the way out.
  assert.ok(send.calls.includes('Runtime.releaseObject'));
});

// ── Names are references, not content ────────────────────────────────────────

test('accessible names are flattened and clipped', () => {
  assert.equal(clipName('  Deploy   to\nprod '), 'Deploy to prod');
  const long = 'x'.repeat(200);
  const clipped = clipName(long);
  assert.equal(clipped.length, 80);
  assert.ok(clipped.endsWith('…'));
});

test('a paragraph-length accessible name never rides along whole', async () => {
  const wordy = [{ role: { value: 'button' }, name: { value: 'y'.repeat(500) }, backendDOMNodeId: 42, nodeId: '2' }];
  const out = await resolvePointer(fakeSend({ partialNodes: wordy }), 3, { x: 0, y: 0 }, { actionable: true });
  assert.ok((out?.pointer.name ?? '').length <= 80, 'the pointer is a reference, not a content channel');
});

// ── Registering the ref: merge, never clobber ────────────────────────────────

test('registerAnnotationRef merges into the tab map — earlier refs survive later registrations', () => {
  // Distinct tab ids per test run: the registry is module-global.
  const tab = 91_001;
  const first = registerAnnotationRef(tab, 42);
  assert.match(first, /^@a\d+$/, 'annotation refs live in their own namespace, never colliding with @eN');
  // A second element on the same tab mints a NEW ref…
  const second = registerAnnotationRef(tab, 43);
  assert.notEqual(second, first);
  // …and the first entry is still there: re-registering node 42 answers the
  // ref the agent already holds instead of renumbering it. (This is the M2
  // clobber regression: a whole-map replacement would have dropped it.)
  assert.equal(registerAnnotationRef(tab, 42), first);
  assert.equal(registerAnnotationRef(tab, 43), second);
});

test('registerAnnotationRef keeps tabs independent', () => {
  const a = registerAnnotationRef(91_002, 42);
  const b = registerAnnotationRef(91_003, 42);
  assert.notEqual(a, b, 'same node id on different tabs is two different registrations');
});
