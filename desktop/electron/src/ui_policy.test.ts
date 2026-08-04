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
  compare: { left: 'run_a', right: 'run_b' },
  replay: { dataset_id: 'ds_1', episode_id: 'ep_1', cursor: 1234 },
  record: { record_id: 'rec_9' },
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
  record: { record_id: 'r' },
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

// ── Coverage: allowlisted ⇒ assemblable (coworking lane G) ───────────────────
//
// The suite above proves the projection narrows correctly, but every one of
// its inputs is a HAND-WRITTEN RawFocus — so a field the table reserves and
// `assembleRawFocus` never produces passes every test while being permanently
// invisible to agents. That is exactly what lane G found: `tabs.*`, `tab.*`,
// `inspect.selection`, `replay.episode_id` and `replay.cursor` had been
// allowlisted since D1 and assembled by nothing.
//
// This test closes the class. It drives the assembler with a maximal
// FocusSources and asserts every allowlisted path comes out — except the ones
// named below, which must state WHY. A gap is allowed; a silent one is not.

/// Allowlisted paths `assembleRawFocus` cannot yet produce, each with the
/// reason and the wedge that closes it. Removing an entry without adding the
/// assembly fails the test; adding one without a reason fails review.
const DECLARED_GAPS: Readonly<Record<string, string>> = {
  // Lane G's own remaining wedge. G5 renamed the field (the row reserved
  // `record.dataset_id`, describing an episode-recording surface that was
  // never built); populating it is B2's, because the Record surface still
  // holds device-local drafts and a draft id dereferences to nothing — a UIRef
  // whose entity is not agent-addressable is not a join key (ADR-062 D-2).
  'record.record_id': 'B2 — Record still writes device-local drafts; their ids resolve through no tool',
  // Gaps lane G did not enumerate, found by this test. Each is a real hole in
  // what an agent can learn, tracked for a follow-up wedge.
  'agent.session_id': 'focus.fleet.selection carries {type,id,name} only — no session id reaches the assembler',
  'project.task_id': 'focus.projects.selection names a project or a host, never a task',
  'terminal.agent_id': 'useTerminals knows the pane, not which agent owns it',
  'kimiweb.url': 'the kimi panel is main-side; its URL never reaches the renderer publisher',
  // Reserved on purpose, with nothing truthful to fill it — see
  // FocusSources.readTabs. Not a follow-up: a Read tab has no local path.
  'tabs.path': 'a Read tab addresses bytes by reference id, not a filesystem path',
  'tab.path': 'a Read tab addresses bytes by reference id, not a filesystem path',
};

/// Every source populated — the upper bound of what assembly can be handed.
const FULL_SOURCES = {
  job: 'read',
  fleetSelection: { type: 'agent', id: 'ag_1', name: 'kimi-1' },
  projectSelection: { type: 'project', id: 'pr_1' },
  activeDocument: { id: 'doc1', title: 'notes' },
  readTabs: [{ kind: 'pdf', title: 'a paper', url: 'https://arxiv.org/abs/2401.00001' }],
  readActive: { kind: 'pdf', title: 'a paper', url: 'https://arxiv.org/abs/2401.00001' },
  inspectTabs: [{ kind: 'code', path: 'src/foo.ts' }],
  inspectActive: { path: 'src/foo.ts' },
  inspectSelection: [42, 58] as [number, number],
  compareSelected: ['run_a', 'run_b'],
  compareBaseline: 'run_a',
  replayDatasetId: 'ds_1',
  replayEpisodeId: '7',
  replayCursor: 12.5,
  terminalPaneId: '%12',
  capturedAt: 't0',
};

test('every allowlisted field is assemblable, or is a DECLARED gap', () => {
  const raw = assembleRawFocus(FULL_SOURCES) as unknown as Record<string, unknown>;
  const missing: string[] = [];
  for (const row of Object.values(UI_POLICY)) {
    for (const p of row.snapshot) {
      const dot = p.indexOf('.');
      const [block, sub] = [p.slice(0, dot), p.slice(dot + 1)];
      const val = raw[block];
      const items = Array.isArray(val) ? (val as Array<Record<string, unknown>>) : val !== undefined ? [val as Record<string, unknown>] : [];
      const present = items.some((item) => item[sub] !== undefined);
      if (present === (DECLARED_GAPS[p] !== undefined)) {
        missing.push(
          present
            ? `${p} is now assembled — delete its DECLARED_GAPS entry`
            : `${p} is allowlisted but assembleRawFocus never produces it (add the assembly, or a DECLARED_GAPS entry saying why not)`,
        );
      }
    }
  }
  assert.deepEqual(missing, [], missing.join('\n'));
});

test('assembleRawFocus: lane G fields land in their blocks', () => {
  const raw = assembleRawFocus(FULL_SOURCES);
  assert.deepEqual(raw.tabs, [{ kind: 'pdf', title: 'a paper', url: 'https://arxiv.org/abs/2401.00001' }]);
  assert.deepEqual(raw.tab, { kind: 'pdf', title: 'a paper', url: 'https://arxiv.org/abs/2401.00001' });
  assert.deepEqual(raw.inspect, { path: 'src/foo.ts', selection: [42, 58] });
  assert.deepEqual(raw.replay, { dataset_id: 'ds_1', episode_id: '7', cursor: 12.5 });
  assert.deepEqual(raw.compare, { left: 'run_a', right: 'run_b' });
});

// ── G4: the wall's N-run selection through a two-field block ─────────────────

test('assembleRawFocus: the baseline is the LEFT of the pair', () => {
  // Pick order puts run_b first; the baseline is run_a. Every delta on the
  // wall reads "other minus baseline", so the pair must read that way too —
  // publishing screen order here would invert the direction of every number an
  // agent then computes.
  const raw = assembleRawFocus({ ...FULL_SOURCES, compareSelected: ['run_b', 'run_a'], compareBaseline: 'run_a' });
  assert.deepEqual(raw.compare, { left: 'run_a', right: 'run_b' });
});

test('assembleRawFocus: with no baseline the pair keeps pick order', () => {
  const raw = assembleRawFocus({ ...FULL_SOURCES, compareSelected: ['run_b', 'run_a'], compareBaseline: null });
  assert.deepEqual(raw.compare, { left: 'run_b', right: 'run_a' });
});

test('assembleRawFocus: a wall that is not a pair publishes no compare block', () => {
  // Three runs is the one that matters: {left, right} would be two thirds of
  // the truth with nothing saying so, and an agent cannot tell a truncated
  // pair from a real one. Zero and one have no comparison to name.
  for (const selected of [[], ['run_a'], ['run_a', 'run_b', 'run_c']]) {
    const raw = assembleRawFocus({ ...FULL_SOURCES, compareSelected: selected, compareBaseline: 'run_a' });
    assert.equal(raw.compare, undefined, `${selected.length} selected run(s) must publish nothing`);
  }
  // And a pre-G4 caller that passes no wall state at all is unchanged.
  const { compareSelected: _s, compareBaseline: _b, ...withoutWall } = FULL_SOURCES;
  assert.equal(assembleRawFocus(withoutWall).compare, undefined);
});

test('assembleRawFocus: a Read tab set with nothing open publishes tabs but no tab', () => {
  // The library view is not a tab. Publishing one would tell an agent the user
  // is reading a document when they are looking at the shelf.
  const raw = assembleRawFocus({ ...FULL_SOURCES, readActive: null });
  assert.equal(raw.tabs?.length, 1);
  assert.equal(raw.tab, undefined);
});

test('assembleRawFocus: a caret with no selection publishes path alone', () => {
  const raw = assembleRawFocus({ ...FULL_SOURCES, inspectSelection: null });
  assert.deepEqual(raw.inspect, { path: 'src/foo.ts' });
});

test('assembleRawFocus: cursor 0 is a position, not an absence', () => {
  // `if (cursor)` would drop the start of the episode — the one cursor value a
  // user reaches by rewinding.
  const raw = assembleRawFocus({ ...FULL_SOURCES, replayCursor: 0 });
  assert.equal(raw.replay?.cursor, 0);
});

test('assembleRawFocus: an episode with no dataset publishes nothing', () => {
  // The player cannot be on screen without a dataset; a bare episode_id would
  // be an id an agent has no way to resolve.
  const raw = assembleRawFocus({ ...FULL_SOURCES, replayDatasetId: null });
  assert.equal(raw.replay, undefined);
});

test('assembleRawFocus: lane G sources are optional — a pre-G caller still assembles', () => {
  const { readTabs, readActive, inspectSelection, replayEpisodeId, replayCursor, ...preG } = FULL_SOURCES;
  void readTabs, readActive, inspectSelection, replayEpisodeId, replayCursor;
  const raw = assembleRawFocus(preG);
  assert.equal(raw.tabs, undefined);
  assert.equal(raw.tab, undefined);
  assert.deepEqual(raw.inspect, { path: 'src/foo.ts' });
  assert.deepEqual(raw.replay, { dataset_id: 'ds_1' });
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

// ── Split panes (desktop-shell-split-pane.md §3.4 / §5) ──────────────────────
// A visible split adds `secondary` + `active_pane`; one pane has neither
// (absent means no split — no `"secondary": null` noise). The pinned pane is
// served under ITS OWN row, and the gate follows FOCUS, not position.

test('projectFocus: a visible split carries the pinned pane and the focus attribution', () => {
  const raw: RawFocus = {
    surface: 'author',
    captured_at: 'now',
    document: { id: 'd1', title: 'Draft' },
    secondary: { surface: 'compare' },
    active_pane: 'primary',
  };
  const out = projectFocus(raw);
  assert.equal(out.surface, 'author');
  assert.equal(out.active_pane, 'primary');
  assert.deepEqual(out.secondary, { surface: 'compare' });
  assert.deepEqual(out.document, { id: 'd1', title: 'Draft' });
});

test('projectFocus: a single pane emits neither field — absent means no split', () => {
  const out = projectFocus({ surface: 'author', captured_at: 'now', document: { id: 'd1', title: 'Draft' } });
  assert.equal('secondary' in out, false);
  assert.equal('active_pane' in out, false);
});

test('projectFocus: the pinned pane is served under its OWN row', () => {
  // `replay` pinned beside `author`: the replay block reaches the pane object
  // through replay's row, and nothing outside that row does.
  const out = projectFocus({
    surface: 'author',
    captured_at: 'now',
    document: { id: 'd1', title: 'Draft' },
    replay: { dataset_id: 'ds1' },
    secondary: { surface: 'replay' },
    active_pane: 'secondary',
  });
  assert.deepEqual(out.secondary, { surface: 'replay', replay: { dataset_id: 'ds1' } });
  // Pinning can never publish more than opening would: the pane object carries
  // no block the pinned surface's row does not allowlist.
  assert.equal('document' in (out.secondary as Record<string, unknown>), false);
});

test('projectFocus: an undeclared pinned surface is not published at all', () => {
  const out = projectFocus({
    surface: 'author',
    captured_at: 'now',
    secondary: { surface: 'not-a-surface' },
    active_pane: 'primary',
  });
  assert.equal('secondary' in out, false);
  assert.equal('active_pane' in out, false);
});

test('projectFocus: the gate follows FOCUS across the split, not position', () => {
  // The vault pane is the one the user is in, so the WHOLE answer degrades —
  // including the other pane's blocks, which position-based gating would leak.
  const raw: RawFocus = {
    surface: 'author',
    captured_at: 'now',
    document: { id: 'd1', title: 'Draft' },
    secondary: { surface: 'vault' },
    active_pane: 'secondary',
  };
  assert.deepEqual(projectFocus(raw), { surface: 'author', captured_at: 'now' });
  // …and with focus back on the author pane the same raw state projects fully.
  const focused = projectFocus({ ...raw, active_pane: 'primary' });
  assert.deepEqual(focused.document, { id: 'd1', title: 'Draft' });
  // A vault pane still names itself (a surface id is not a secret — the same
  // thing D1 already publishes when the vault is the only surface), but its
  // empty row means it contributes no fields.
  assert.deepEqual(focused.secondary, { surface: 'vault' });
});

test('assembleRawFocus: the split fields are set only when a pane is pinned', () => {
  const base = {
    fleetSelection: null,
    projectSelection: null,
    activeDocument: null,
    inspectTabs: [],
    inspectActive: null,
    replayDatasetId: null,
    terminalPaneId: null,
    capturedAt: 'now',
  };
  // Absent (every pre-S3 caller) and explicit-null both read as "no split".
  assert.deepEqual(assembleRawFocus({ ...base, job: 'read' }), { surface: 'read', captured_at: 'now' });
  assert.deepEqual(assembleRawFocus({ ...base, job: 'read', secondaryJob: null }), {
    surface: 'read',
    captured_at: 'now',
  });
  assert.deepEqual(assembleRawFocus({ ...base, job: 'read', secondaryJob: 'compare', activePane: 'secondary' }), {
    surface: 'read',
    captured_at: 'now',
    secondary: { surface: 'compare' },
    active_pane: 'secondary',
  });
});

// ── Coworking H — the navigate column ───────────────────────────────────────

test('navigate is declared exactly where an agent may bring the user, and NOWHERE else', () => {
  // The invariant ADR-064 §12 states, made executable: terminal, settings and
  // the vault are unreachable by `desktop_open`. This suite runs in CI, so a
  // future edit that adds `navigate: 'allow'` to one of them fails on the PR
  // that does it rather than at review.
  const navigable = Object.entries(UI_POLICY)
    .filter(([, row]) => row.navigate === 'allow')
    .map(([surface]) => surface)
    .sort();
  assert.deepEqual(navigable, ['author', 'compare', 'debug', 'fleet', 'projects', 'read', 'record', 'replay']);

  for (const surface of ['terminal', 'settings', 'kimiweb', 'vault']) {
    // Absent, not false. `capture: 'refuse'` is a bit a careless edit flips;
    // a missing field has to be added on purpose.
    assert.equal(
      Object.prototype.hasOwnProperty.call(UI_POLICY[surface], 'navigate'),
      false,
      `${surface} must have no navigate column at all`,
    );
  }
});

test('a surface that refuses PIXELS is never navigable — the weaker bit cannot outrank the stronger', () => {
  // Not a rule the type system can state, and the pairing is the point: if a
  // surface is too sensitive to photograph, dropping the user into it with an
  // agent watching the transcript is not a lesser act.
  for (const [surface, row] of Object.entries(UI_POLICY)) {
    if (row.capture === 'refuse') {
      assert.notEqual(row.navigate, 'allow', `${surface} refuses capture but allows navigate`);
    }
  }
});
