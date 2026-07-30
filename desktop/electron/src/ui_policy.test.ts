/// Tests for the desktop UI policy table + focus projection (D1 —
/// docs/plans/desktop-ui-context-and-pointing.md §3.0/§3.2, plan §5):
/// every declared surface projects to exactly its allowlist (or degrades to
/// existence-only), an undeclared surface fails, content fields never
/// survive projection — plus the raw-focus assembly and the ≥500 ms
/// throttle/coalescing sender. Run with `node --test` (Node strips the type
/// annotations); imports the dependency-free renderer module directly.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  assembleRawFocus,
  createFocusSender,
  projectFocus,
  uiPolicyFor,
  UI_POLICY,
  type RawFocus,
  type UiFocusSnapshot,
} from '../../src/state/ui_policy.ts';

// Mirror of the workbench's surface set — desktop/src/state/workbench.ts
// JOBS + SETTINGS_JOB (:36-64), plus the two pseudo-surfaces the plan names
// (kimi-web panel, vault). workbench.ts itself is NOT imported: it is a
// zustand store that reads localStorage at module scope, and this suite must
// stay electron- AND renderer-free. Adding a job to workbench.ts without a
// policy row (and vice versa) fails the matrix test below — keep this list
// in sync.
const KNOWN_SURFACES = [
  'fleet',
  'projects',
  'read',
  'author',
  'debug',
  'compare',
  'replay',
  'record',
  'terminal',
  'settings',
  'kimiweb',
  'vault',
] as const;

/// A raw focus state with EVERY block populated — the upper bound of what
/// assembly may produce. The projection must narrow it to exactly the
/// allowlists.
const FULL_RAW: RawFocus = {
  surface: 'read',
  captured_at: '2026-07-30T00:00:00.000Z',
  agent: { id: 'ag_1', handle: 'kimi-1', session_id: 'ses_1' },
  project: { project_id: 'pr_1', task_id: 'tk_1' },
  tabs: [
    { kind: 'web', title: 'a paper', url: 'https://arxiv.org/abs/2401.00001', path: '/p/a.pdf' },
    { kind: 'pdf', title: 'local', path: '/p/b.pdf' },
  ],
  tab: { kind: 'web', title: 'a paper', url: 'https://arxiv.org/abs/2401.00001' },
  document: { id: 'doc1', title: 'notes' },
  inspect_tabs: [{ kind: 'code', path: 'src/foo.ts' }, { kind: 'log' }],
  inspect: { path: 'src/foo.ts', selection: [42, 58] },
  compare: { left: 'src/a.ts', right: 'src/b.ts' },
  replay: { dataset_id: 'ds_1', episode_id: 'ep_1', cursor: 1234 },
  record: { dataset_id: 'ds_9' },
  terminal: { pane_id: '%12', agent_id: 'ag_1' },
  kimiweb: { url: 'http://127.0.0.1:17331/' },
};

/// Every block with EVERY field populated — the vocabulary fixture: the
/// allowlists may only name sub-fields that exist here (which is what keeps
/// the dot-path strings honest against the RawFocus type).
const FULLEST_RAW: RawFocus = {
  surface: 'read',
  captured_at: 't0',
  agent: { id: 'a', handle: 'h', session_id: 's' },
  project: { project_id: 'p', task_id: 't' },
  tabs: [{ kind: 'k', title: 't', url: 'u', path: 'p' }],
  tab: { kind: 'k', title: 't', url: 'u', path: 'p' },
  document: { id: 'd', title: 't' },
  inspect_tabs: [{ kind: 'k', path: 'p' }],
  inspect: { path: 'p', selection: [1, 2] },
  compare: { left: 'l', right: 'r' },
  replay: { dataset_id: 'd', episode_id: 'e', cursor: 1 },
  record: { dataset_id: 'd' },
  terminal: { pane_id: 'p', agent_id: 'a' },
  kimiweb: { url: 'u' },
};

// ── The matrix ───────────────────────────────────────────────────────────────

test('ui_policy: every known surface has a row, and every row names a known surface', () => {
  for (const surface of KNOWN_SURFACES) {
    const row = uiPolicyFor(surface);
    assert.ok(row !== null, `surface '${surface}' has no ui_policy row — add one (the row IS the privacy review)`);
    for (const bit of [row.capture, row.highlight]) {
      assert.ok(bit === 'allow' || bit === 'refuse', `surface '${surface}' has an invalid policy bit`);
    }
  }
  for (const key of Object.keys(UI_POLICY)) {
    assert.ok((KNOWN_SURFACES as readonly string[]).includes(key), `ui_policy row '${key}' is not a known surface — stale row?`);
  }
});

test('ui_policy: allowlist paths name real RawFocus blocks and sub-fields', () => {
  const rawBlocks = FULLEST_RAW as unknown as Record<string, unknown>;
  for (const [surface, row] of Object.entries(UI_POLICY)) {
    for (const p of row.snapshot) {
      const dot = p.indexOf('.');
      assert.ok(dot > 0, `${surface}: allowlist entry '${p}' must be block.field`);
      const [block, sub] = [p.slice(0, dot), p.slice(dot + 1)];
      const val = rawBlocks[block];
      assert.ok(val !== undefined, `${surface}: allowlist block '${block}' is not a RawFocus field`);
      if (Array.isArray(val)) {
        assert.ok(
          val.some((item) => typeof item === 'object' && item !== null && sub in (item as Record<string, unknown>)),
          `${surface}: '${sub}' is not a field of any '${block}' item`,
        );
      } else {
        assert.ok(typeof val === 'object' && val !== null && sub in (val as Record<string, unknown>), `${surface}: '${sub}' is not a field of '${block}'`);
      }
    }
  }
});

test('ui_policy: vault refuses everything; settings refuses capture', () => {
  assert.deepEqual(uiPolicyFor('vault'), { snapshot: [], capture: 'refuse', highlight: 'refuse' });
  const settings = uiPolicyFor('settings');
  assert.ok(settings !== null && settings.snapshot.length === 0 && settings.capture === 'refuse');
});

// ── Projection ───────────────────────────────────────────────────────────────

/// The set of (block, sub-field) pairs the whole table allows — the ceiling
/// any projection may emit.
function allowedPairs(): Map<string, Set<string>> {
  const out = new Map<string, Set<string>>();
  for (const row of Object.values(UI_POLICY)) {
    for (const p of row.snapshot) {
      const dot = p.indexOf('.');
      const [block, sub] = [p.slice(0, dot), p.slice(dot + 1)];
      const subs = out.get(block) ?? new Set<string>();
      subs.add(sub);
      out.set(block, subs);
    }
  }
  return out;
}

test('projectFocus: every surface with an allowlist projects to exactly that allowlist', () => {
  const allowed = allowedPairs();
  for (const surface of KNOWN_SURFACES) {
    const row = uiPolicyFor(surface);
    assert.ok(row !== null);
    if (row.snapshot.length === 0) continue; // degradation covered below
    const snap = projectFocus({ ...FULL_RAW, surface });
    // surface + captured_at, always.
    assert.equal(snap.surface, surface);
    assert.equal(snap.captured_at, FULL_RAW.captured_at);
    for (const [key, val] of Object.entries(snap)) {
      if (key === 'surface' || key === 'captured_at') continue;
      // Every emitted block/sub-field must be allowlisted for SOME row...
      const subs = allowed.get(key);
      assert.ok(subs !== undefined, `${surface}: emitted non-allowlisted block '${key}'`);
      const items = Array.isArray(val) ? (val as Array<Record<string, unknown>>) : [val as Record<string, unknown>];
      for (const item of items) {
        for (const sub of Object.keys(item)) {
          assert.ok(subs.has(sub), `${surface}: emitted non-allowlisted field '${key}.${sub}'`);
        }
      }
      // ...and every allowlisted value the raw state HAD must be present
      // (projection narrows, it never drops an allowed field).
      const rawVal = (FULL_RAW as unknown as Record<string, unknown>)[key];
      const rawItems = Array.isArray(rawVal) ? (rawVal as Array<Record<string, unknown>>) : [rawVal as Record<string, unknown>];
      assert.equal(items.length, rawItems.length, `${surface}: '${key}' item count changed`);
      rawItems.forEach((rawItem, i) => {
        for (const [sub, v] of Object.entries(rawItem)) {
          if (!subs.has(sub)) continue;
          assert.deepEqual((items[i] as Record<string, unknown>)[sub], v, `${surface}: lost allowed field '${key}.${sub}'`);
        }
      });
    }
  }
});

test('projectFocus: read surface emits the §3.2 example shape', () => {
  const snap = projectFocus({ ...FULL_RAW, surface: 'read' });
  assert.deepEqual(snap.tab, { kind: 'web', title: 'a paper', url: 'https://arxiv.org/abs/2401.00001' });
  assert.deepEqual(snap.agent, { id: 'ag_1', handle: 'kimi-1', session_id: 'ses_1' });
  assert.deepEqual(snap.inspect, { path: 'src/foo.ts', selection: [42, 58] });
  assert.deepEqual(snap.terminal, { pane_id: '%12', agent_id: 'ag_1' });
});

test('projectFocus: settings, vault and unknown surfaces degrade to existence only', () => {
  for (const surface of ['settings', 'vault', 'nosuch']) {
    const snap = projectFocus({ ...FULL_RAW, surface });
    assert.deepEqual(snap, { surface, captured_at: FULL_RAW.captured_at }, `'${surface}' must emit existence only`);
  }
});

test('projectFocus: content fields never survive — secret-free by construction', () => {
  // A raw state poisoned with content fields (cast: assembly would never
  // produce these, but the table — not the pipeline — must be the guard).
  const poisoned = {
    ...FULL_RAW,
    agent: { id: 'ag_1', handle: 'kimi-1', messages: 'SECRET CHAT' },
    document: { id: 'doc1', title: 'notes', body: 'SECRET DOC' },
    tabs: [{ kind: 'web', title: 't', url: 'https://a.b/', content: 'SECRET PAGE' }],
    inspect: { path: 'src/foo.ts', fileText: 'SECRET CODE' },
  } as unknown as RawFocus;
  const snap = projectFocus({ ...poisoned, surface: 'read' }) as unknown as Record<string, unknown>;
  const flat = JSON.stringify(snap);
  assert.ok(!flat.includes('SECRET'), `projection leaked a content field: ${flat}`);
  assert.deepEqual(snap.agent, { id: 'ag_1', handle: 'kimi-1' });
  assert.deepEqual(snap.document, { id: 'doc1', title: 'notes' });
  assert.deepEqual(snap.tabs, [{ kind: 'web', title: 't', url: 'https://a.b/' }]);
  assert.deepEqual(snap.inspect, { path: 'src/foo.ts' });
});

test('projectFocus: blocks absent from the raw state emit nothing', () => {
  const snap = projectFocus({ surface: 'fleet', captured_at: 't0' });
  assert.deepEqual(snap, { surface: 'fleet', captured_at: 't0' });
});

// ── Assembly ─────────────────────────────────────────────────────────────────

test('assembleRawFocus: store slices land in the right blocks; non-matching selections drop', () => {
  const raw = assembleRawFocus({
    job: 'debug',
    fleetSelection: { type: 'agent', id: 'ag_1', name: 'kimi-1' },
    projectSelection: { type: 'host', id: 'h_1' }, // not a project → no block
    activeDocument: { id: 'doc1', title: 'notes' },
    inspectTabs: [{ kind: 'code', path: 'src/foo.ts' }, { kind: 'log' }],
    inspectActive: { path: 'src/foo.ts' },
    replayDatasetId: 'ds_1',
    terminalPaneId: '%12',
    capturedAt: 't0',
  });
  assert.deepEqual(raw, {
    surface: 'debug',
    captured_at: 't0',
    agent: { id: 'ag_1', handle: 'kimi-1' },
    document: { id: 'doc1', title: 'notes' },
    inspect_tabs: [{ kind: 'code', path: 'src/foo.ts' }, { kind: 'log' }],
    inspect: { path: 'src/foo.ts' },
    replay: { dataset_id: 'ds_1' },
    terminal: { pane_id: '%12' },
  });
});

test('assembleRawFocus: sparse sources produce a bare snapshot', () => {
  const raw = assembleRawFocus({
    job: 'settings',
    fleetSelection: null,
    projectSelection: null,
    activeDocument: null,
    inspectTabs: [],
    inspectActive: null,
    replayDatasetId: null,
    terminalPaneId: null,
    capturedAt: 't0',
  });
  assert.deepEqual(raw, { surface: 'settings', captured_at: 't0' });
});

// ── Throttle / coalescing sender ─────────────────────────────────────────────

/// A deterministic clock + timer queue: advance(ms) fires everything due.
function fakeTiming(): {
  now: () => number;
  advance: (ms: number) => void;
  setTimeout: (fn: () => void, ms: number) => unknown;
  clearTimeout: (h: unknown) => void;
} {
  let t = 0;
  let nextId = 1;
  const timers: Array<{ at: number; fn: () => void; id: number }> = [];
  return {
    now: () => t,
    advance: (ms) => {
      t += ms;
      for (;;) {
        const due = timers.filter((x) => x.at <= t).sort((a, b) => a.at - b.at);
        if (due.length === 0) return;
        for (const x of due) {
          timers.splice(timers.indexOf(x), 1);
          x.fn();
        }
      }
    },
    setTimeout: (fn, ms) => {
      const id = nextId++;
      timers.push({ at: t + ms, fn, id });
      return id;
    },
    clearTimeout: (h) => {
      const i = timers.findIndex((x) => x.id === h);
      if (i >= 0) timers.splice(i, 1);
    },
  };
}

const snap = (surface: string): UiFocusSnapshot => ({ surface, captured_at: 't0' });

test('focus sender: first push is immediate; in-window pushes coalesce to one trailing latest', () => {
  const timing = fakeTiming();
  const sent: UiFocusSnapshot[] = [];
  const s = createFocusSender(500, (x) => sent.push(x), timing);
  s.push(snap('read'));
  assert.deepEqual(sent.map((x) => x.surface), ['read']);
  // Three pushes inside the window — only the latest survives.
  s.push(snap('debug'));
  s.push(snap('replay'));
  s.push(snap('terminal'));
  assert.deepEqual(sent.map((x) => x.surface), ['read'], 'in-window pushes must not send immediately');
  timing.advance(499);
  assert.equal(sent.length, 1);
  timing.advance(1); // 500 ms after the first send
  assert.deepEqual(sent.map((x) => x.surface), ['read', 'terminal']);
});

test('focus sender: a push after the window sends immediately again', () => {
  const timing = fakeTiming();
  const sent: UiFocusSnapshot[] = [];
  const s = createFocusSender(500, (x) => sent.push(x), timing);
  s.push(snap('read'));
  timing.advance(500);
  s.push(snap('debug'));
  assert.deepEqual(sent.map((x) => x.surface), ['read', 'debug']);
});

test('focus sender: an identical snapshot is dropped before arming any timer', () => {
  const timing = fakeTiming();
  const sent: UiFocusSnapshot[] = [];
  const s = createFocusSender(500, (x) => sent.push(x), timing);
  s.push(snap('read'));
  s.push(snap('read')); // same shape — deduped
  s.push({ surface: 'read', captured_at: 't0' }); // deep-equal — deduped too
  timing.advance(1000);
  assert.equal(sent.length, 1);
});

test('focus sender: fresh captured_at alone is not a change — dedupe survives production timestamps', () => {
  const timing = fakeTiming();
  const sent: UiFocusSnapshot[] = [];
  const s = createFocusSender(500, (x) => sent.push(x), timing);
  s.push({ surface: 'read', captured_at: 't0' });
  timing.advance(600); // outside the throttle window — only the key comparison can drop the next push
  s.push({ surface: 'read', captured_at: 't1' }); // same content, new stamp (the assembly mints one per store tick)
  assert.equal(sent.length, 1, 'a captured_at-only difference must dedupe');
  s.push({ surface: 'debug', captured_at: 't2' }); // a real change still sends, carrying its fresh stamp
  assert.deepEqual(sent.map((x) => x.captured_at), ['t0', 't2']);
});

test('focus sender: cancel drops the pending trailing send', () => {
  const timing = fakeTiming();
  const sent: UiFocusSnapshot[] = [];
  const s = createFocusSender(500, (x) => sent.push(x), timing);
  s.push(snap('read'));
  s.push(snap('debug'));
  s.cancel();
  timing.advance(1000);
  assert.deepEqual(sent.map((x) => x.surface), ['read']);
});
