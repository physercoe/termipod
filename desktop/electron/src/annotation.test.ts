/// Tests for the D2 annotation overlay core (docs/plans/desktop-ui-context-
/// and-pointing.md §3.4/§3.5, plan §5): drag → rect normalization, the
/// rect→guest-region capture mapping (intersection pick + translated coords),
/// the sensitive-surface refusal (refused only when ENTIRELY inside a
/// capture:refuse region), the ≤1568px downscale fit, the temp-file naming
/// contract, and the kimi composer injection sequence against a fake CDP
/// sender (mirroring browserbridge.test.ts's fake backend) — including the
/// clipboard-fallback decision when no file input resolves. Plus the
/// companion chip → postAgentInput payload shape via attach.ts's compose().
/// Run with `node --test` (Node strips the type annotations).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  annotationTempPath,
  fitWithin,
  injectImageIntoComposer,
  injectNoteIntoComposer,
  isAnnotationTempFile,
  KIMI_FILE_INPUT_SELECTORS,
  KIMI_TEXT_INPUT_SELECTORS,
  MAX_IMAGE_EDGE,
  normalizeDrag,
  pickCaptureTarget,
  rectFullyInside,
  rectIntersection,
  refusedSurface,
  toIntRect,
  type AnnotRect,
  type GuestRegion,
} from './annotation.ts';
import { compose, type Pending } from '../../src/ui/attach.ts';

const GUEST: GuestRegion = { id: 7, rect: { x: 400, y: 100, width: 600, height: 500 } };

// ── Drag → rect ──────────────────────────────────────────────────────────────

test('normalizeDrag: any drag direction normalizes to a positive rect', () => {
  assert.deepEqual(normalizeDrag(10, 20, 110, 70), { x: 10, y: 20, width: 100, height: 50 });
  assert.deepEqual(normalizeDrag(110, 70, 10, 20), { x: 10, y: 20, width: 100, height: 50 });
  assert.deepEqual(normalizeDrag(110, 20, 10, 70), { x: 10, y: 20, width: 100, height: 50 });
});

test('rectIntersection / rectFullyInside basics', () => {
  const a: AnnotRect = { x: 0, y: 0, width: 100, height: 100 };
  assert.deepEqual(rectIntersection(a, { x: 50, y: 50, width: 100, height: 100 }), { x: 50, y: 50, width: 50, height: 50 });
  assert.equal(rectIntersection(a, { x: 200, y: 0, width: 10, height: 10 }), null);
  // Touching edges do not intersect.
  assert.equal(rectIntersection(a, { x: 100, y: 0, width: 10, height: 10 }), null);
  assert.equal(rectFullyInside({ x: 10, y: 10, width: 20, height: 20 }, a), true);
  assert.equal(rectFullyInside({ x: 0, y: 0, width: 100, height: 100 }, a), true, 'inclusive edges');
  assert.equal(rectFullyInside({ x: 90, y: 90, width: 20, height: 20 }, a), false);
});

// ── Rect → capture target mapping ────────────────────────────────────────────

test('pickCaptureTarget: rect fully inside a guest captures the guest, translated', () => {
  const sel: AnnotRect = { x: 450, y: 150, width: 200, height: 100 };
  const t = pickCaptureTarget(sel, [GUEST]);
  assert.deepEqual(t, { kind: 'guest', id: 7, rect: { x: 50, y: 50, width: 200, height: 100 } });
});

test('pickCaptureTarget: a rect straddling the guest edge clips to the guest part', () => {
  // Starts inside the guest, runs past its right edge (guest ends at x=1000).
  const sel: AnnotRect = { x: 900, y: 150, width: 300, height: 100 };
  const t = pickCaptureTarget(sel, [GUEST]);
  assert.deepEqual(t, { kind: 'guest', id: 7, rect: { x: 500, y: 50, width: 100, height: 100 } });
});

test('pickCaptureTarget: a shell-only rect stays a shell capture, unchanged', () => {
  const sel: AnnotRect = { x: 10, y: 10, width: 200, height: 100 };
  assert.deepEqual(pickCaptureTarget(sel, [GUEST]), { kind: 'shell', rect: sel });
  assert.deepEqual(pickCaptureTarget(sel, []), { kind: 'shell', rect: sel });
});

test('pickCaptureTarget: overlapping two guests picks the larger intersection', () => {
  const dock: GuestRegion = { id: 9, rect: { x: 0, y: 400, width: 1280, height: 320 } };
  // Intersects GUEST (200x180 = 36000) and the dock (200x200 = 40000) — the
  // dock wins, and the rect translates into ITS coordinates.
  const sel: AnnotRect = { x: 800, y: 420, width: 200, height: 200 };
  const t = pickCaptureTarget(sel, [GUEST, dock]);
  assert.deepEqual(t, { kind: 'guest', id: 9, rect: { x: 800, y: 20, width: 200, height: 200 } });
});

test('toIntRect: floors the origin and ceils the far edge (covers the float rect)', () => {
  assert.deepEqual(toIntRect({ x: 10.2, y: 20.6, width: 100.5, height: 50.1 }), { x: 10, y: 20, width: 101, height: 51 });
  assert.deepEqual(toIntRect({ x: 0, y: 0, width: 100, height: 50 }), { x: 0, y: 0, width: 100, height: 50 });
});

// ── Sensitive-surface refusal (plan §3.4: refused only when ENTIRELY inside) ──

test('refusedSurface: a rect fully inside a capture:refuse region is refused', () => {
  const settingsPane = { surface: 'settings', rect: { x: 0, y: 0, width: 800, height: 600 } };
  assert.equal(refusedSurface({ x: 100, y: 100, width: 200, height: 100 }, [settingsPane]), 'settings');
});

test('refusedSurface: partial overlap with a refuse region is ALLOWED', () => {
  const settingsPane = { surface: 'settings', rect: { x: 0, y: 0, width: 800, height: 600 } };
  // Pokes past the pane's bottom edge → not entirely inside → allowed.
  assert.equal(refusedSurface({ x: 100, y: 550, width: 200, height: 100 }, [settingsPane]), null);
});

test('refusedSurface: vault refuses; allow surfaces and unknown surfaces never do', () => {
  const rect: AnnotRect = { x: 10, y: 10, width: 50, height: 50 };
  assert.equal(refusedSurface(rect, [{ surface: 'vault', rect: { x: 0, y: 0, width: 800, height: 600 } }]), 'vault');
  assert.equal(refusedSurface(rect, [{ surface: 'read', rect: { x: 0, y: 0, width: 800, height: 600 } }]), null);
  assert.equal(refusedSurface(rect, [{ surface: 'no-such-surface', rect: { x: 0, y: 0, width: 800, height: 600 } }]), null);
  assert.equal(refusedSurface(rect, []), null);
});

// ── Downscale fit ────────────────────────────────────────────────────────────

test('fitWithin: caps the longest edge at the max, preserving aspect', () => {
  assert.deepEqual(fitWithin(3136, 1568), { width: MAX_IMAGE_EDGE, height: 784 });
  const tall = fitWithin(800, 3200);
  assert.equal(tall.height, MAX_IMAGE_EDGE);
  assert.equal(tall.width, 392);
  assert.ok(Math.abs(tall.width / tall.height - 800 / 3200) < 0.01, 'aspect preserved');
});

test('fitWithin: never upscales; small images pass through', () => {
  assert.deepEqual(fitWithin(800, 600), { width: 800, height: 600 });
  assert.deepEqual(fitWithin(MAX_IMAGE_EDGE, 100), { width: MAX_IMAGE_EDGE, height: 100 });
});

// ── Temp-file contract ───────────────────────────────────────────────────────

test('annotationTempPath + isAnnotationTempFile: prefix, .png, no traversal', () => {
  const p = annotationTempPath('/tmp', '1716000000000-3');
  assert.equal(p, '/tmp/termipod-annot-1716000000000-3.png');
  assert.equal(isAnnotationTempFile('/tmp', p), true);
  assert.equal(isAnnotationTempFile('/tmp', '/tmp/termipod-annot-x.png'), true);
  assert.equal(isAnnotationTempFile('/tmp', '/tmp/other.png'), false);
  assert.equal(isAnnotationTempFile('/tmp', '/tmp/termipod-annot-../../etc/passwd.png'), false);
  assert.equal(isAnnotationTempFile('/tmp', '/tmp/termipod-annot-x.jpg'), false);
  // A stamp's unsafe characters are stripped at naming time.
  assert.equal(annotationTempPath('/tmp', 'a/b\\c:d'), '/tmp/termipod-annot-abcd.png');
});

// ── kimi composer injection (fake CDP, browserbridge.test.ts style) ──────────

interface FakeCall {
  method: string;
  params?: Record<string, unknown>;
}

/// A fake CDP sender: `nodeIds` answers the FIRST matching selector with
/// those node ids (empty = no composer input); records every call.
function fakeCdp(nodeIds: number[]): { send: (m: string, p?: Record<string, unknown>) => Promise<unknown>; calls: FakeCall[] } {
  const calls: FakeCall[] = [];
  return {
    calls,
    send: async (method: string, params?: Record<string, unknown>) => {
      calls.push({ method, params });
      if (method === 'DOM.getDocument') return { root: { nodeId: 1 } };
      if (method === 'DOM.querySelectorAll') return { nodeIds };
      if (method === 'DOM.resolveNode') return { object: { objectId: 'obj-1' } };
      if (method === 'DOM.setFileInputFiles') return {};
      if (method === 'Runtime.callFunctionOn') return {};
      if (method === 'Runtime.releaseObject') return {};
      if (method === 'DOM.focus') return {};
      if (method === 'Input.insertText') return {};
      throw new Error(`unexpected ${method}`);
    },
  };
}

test('kimi injection: file set on the resolved input, then input+change dispatched, object released', async () => {
  const { send, calls } = fakeCdp([42]);
  const res = await injectImageIntoComposer(send, '/tmp/termipod-annot-1.png');
  assert.equal(res, 'attached');
  const set = calls.find((c) => c.method === 'DOM.setFileInputFiles');
  assert.deepEqual(set?.params?.files, ['/tmp/termipod-annot-1.png']);
  assert.equal(set?.params?.objectId, 'obj-1');
  // The SPA's attach pipeline listens for change — CDP fires no DOM events,
  // so the dispatch must follow the set.
  const dispatch = calls.find((c) => c.method === 'Runtime.callFunctionOn');
  const fn = String(dispatch?.params?.functionDeclaration ?? '');
  assert.match(fn, /dispatchEvent\(new Event\("input"/);
  assert.match(fn, /dispatchEvent\(new Event\("change"/);
  assert.ok(
    calls.findIndex((c) => c.method === 'DOM.setFileInputFiles') < calls.findIndex((c) => c.method === 'Runtime.callFunctionOn'),
    'set before dispatch',
  );
  assert.ok(calls.some((c) => c.method === 'Runtime.releaseObject'), 'object released');
});

test('kimi injection: selector candidates are tried in order until one matches', async () => {
  const calls: string[] = [];
  const send = async (method: string, params?: Record<string, unknown>): Promise<unknown> => {
    if (method === 'DOM.getDocument') return { root: { nodeId: 1 } };
    if (method === 'DOM.querySelectorAll') {
      calls.push(String(params?.selector));
      // Only the LAST candidate matches.
      return { nodeIds: params?.selector === KIMI_FILE_INPUT_SELECTORS[KIMI_FILE_INPUT_SELECTORS.length - 1] ? [5] : [] };
    }
    if (method === 'DOM.resolveNode') return { object: { objectId: 'o' } };
    return {};
  };
  const res = await injectImageIntoComposer(send, '/tmp/termipod-annot-1.png');
  assert.equal(res, 'attached');
  assert.deepEqual(calls, [...KIMI_FILE_INPUT_SELECTORS]);
});

test('kimi injection: no matching selector → no-input (the clipboard-fallback cue), never a set', async () => {
  const { send, calls } = fakeCdp([]);
  const res = await injectImageIntoComposer(send, '/tmp/termipod-annot-1.png');
  assert.equal(res, 'no-input');
  assert.ok(!calls.some((c) => c.method === 'DOM.setFileInputFiles'), 'nothing set without an input');
});

test('kimi injection: an unresolvable node → no-input', async () => {
  const send = async (method: string): Promise<unknown> => {
    if (method === 'DOM.getDocument') return { root: { nodeId: 1 } };
    if (method === 'DOM.querySelectorAll') return { nodeIds: [9] };
    if (method === 'DOM.resolveNode') return {};
    return {};
  };
  assert.equal(await injectImageIntoComposer(send, '/tmp/x.png'), 'no-input');
});

// ── kimi composer NOTE injection (D4 — the pointer must ride this path too) ──

test('kimi note: focus the composer text box, then insert via the input pipeline', async () => {
  const { send, calls } = fakeCdp([7]);
  const res = await injectNoteIntoComposer(send, 'why is this red?\nPointing at a button…');
  assert.equal(res, 'injected');
  const focus = calls.findIndex((c) => c.method === 'DOM.focus');
  const insert = calls.findIndex((c) => c.method === 'Input.insertText');
  assert.ok(focus >= 0 && insert > focus, 'focus before insert');
  assert.equal(calls[focus].params?.nodeId, 7);
  // Input.insertText rides the browser's own input pipeline (React's value
  // tracker included) — never a .value= assignment the SPA can't see.
  assert.equal(calls[insert].params?.text, 'why is this red?\nPointing at a button…');
});

test('kimi note: textarea, then contenteditable — first match wins', async () => {
  const tried: string[] = [];
  const send = async (method: string, params?: Record<string, unknown>): Promise<unknown> => {
    if (method === 'DOM.getDocument') return { root: { nodeId: 1 } };
    if (method === 'DOM.querySelectorAll') {
      tried.push(String(params?.selector));
      return { nodeIds: params?.selector === KIMI_TEXT_INPUT_SELECTORS[KIMI_TEXT_INPUT_SELECTORS.length - 1] ? [5] : [] };
    }
    return {};
  };
  assert.equal(await injectNoteIntoComposer(send, 'note'), 'injected');
  assert.deepEqual(tried, [...KIMI_TEXT_INPUT_SELECTORS]);
});

test('kimi note: no text box → no-input, and nothing inserted', async () => {
  const { send, calls } = fakeCdp([]);
  assert.equal(await injectNoteIntoComposer(send, 'note'), 'no-input');
  assert.ok(!calls.some((c) => c.method === 'Input.insertText'));
});

// ── Companion chip → postAgentInput payload shape ────────────────────────────

test('compose: a staged annotation image rides att.images as raw base64 with the note as body', () => {
  const staged: Pending[] = [
    {
      id: 'att1',
      kind: 'image',
      name: 'annotation-1568x784.png',
      mime: 'image/png',
      size: 12345,
      data: 'aGVsbG8=',
      preview: 'data:image/png;base64,cHJldmlldw==',
    },
  ];
  const { body, att } = compose('look at this panel', staged);
  assert.equal(body, 'look at this panel');
  assert.deepEqual(att.images, [{ mime_type: 'image/png', data: 'aGVsbG8=' }]);
  assert.equal(att.pdfs, undefined);
  assert.equal(att.audios, undefined);
  assert.equal(att.videos, undefined);
  // The hub wants RAW base64 (no data: prefix) — attach.ts's contract.
  assert.ok(!att.images[0].data.startsWith('data:'));
});
