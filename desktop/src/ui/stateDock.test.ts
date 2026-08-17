/// P2 state-dock checks (agent-transcript-redesign §6 P2):
///  - name matching (bare / case-insensitive / mcp__<server>__<name> suffix,
///    with near-miss exclusions) for the shell + sub-agent baselines.
///  - per-call status via the lineage maps (running / done / error).
///  - todos: newest plan event wins, done/total counting, no-plan → no chip.
///  - chip visibility rules (Tasks/Sub-agents only while running; Todos once
///    any plan exists).
///  - lens invariance: the derivation reads only plan / tool_call kinds, so
///    dropping unrelated kinds (what any lens does) cannot move the counts.
///  - list ordering: running first, then settled newest-first, capped at 20.
/// Run with `node --test src/ui/stateDock.test.ts` from `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  deriveStateDock,
  isShellTaskName,
  subagentDetail,
  isSubagentName,
  MAX_DOCK_CALLS,
  toolCallKeyArg,
  visibleDockChips,
  type StateDockModel,
} from './stateDock.ts';
import type { Entity } from '../hub/types';
import type { FeedEvent } from './EventCard';

let n = 0;
function ev(kind: string, payload: Record<string, unknown>): FeedEvent {
  n += 1;
  return { id: `e${n}`, seq: n, ord: n, kind, producer: 'agent', payload };
}
function call(id: string, name: string, extra: Record<string, unknown> = {}): FeedEvent {
  // Event id keyed on the tool-call id so tests can assert row order.
  n += 1;
  return { id: `c-${id}`, seq: n, ord: n, kind: 'tool_call', producer: 'agent', payload: { id, name, ...extra } };
}
function plan(entries: Record<string, unknown>[]): FeedEvent {
  return ev('plan', { entries });
}
function result(id: string, isError = false): FeedEvent {
  return ev('tool_result', { tool_use_id: id, is_error: isError, content: '…' });
}
function update(id: string, status: string): FeedEvent {
  return ev('tool_call_update', { toolCallId: id, status });
}

/// The same joins AgentTranscript's useToolMaps performs, so the derivation
/// sees the lineage maps it gets in production.
function mapsFor(events: FeedEvent[]): {
  nameById: Map<string, string>;
  resultById: Map<string, Entity>;
  updateById: Map<string, Entity>;
} {
  const nameById = new Map<string, string>();
  const resultById = new Map<string, Entity>();
  const updateById = new Map<string, Entity>();
  for (const e of events) {
    if (e.kind === 'tool_call') {
      const id = e.payload['id'];
      const name = e.payload['name'];
      if (typeof id === 'string' && typeof name === 'string') nameById.set(id, name);
    } else if (e.kind === 'tool_result') {
      const id = e.payload['tool_use_id'];
      if (typeof id === 'string') resultById.set(id, e.payload);
    } else if (e.kind === 'tool_call_update') {
      const id = e.payload['toolCallId'] ?? e.payload['tool_call_id'];
      if (id !== undefined && id !== null) updateById.set(String(id), e.payload);
    }
  }
  return { nameById, resultById, updateById };
}
function derive(events: FeedEvent[]): StateDockModel {
  return deriveStateDock(events, mapsFor(events));
}

// --- name matching ------------------------------------------------------------

test('name matching: bare, case-insensitive, and mcp-suffixed names hit', () => {
  for (const name of ['bash', 'Bash', 'BASH', 'shell', 'Shell', 'exec', 'exec_command', 'run_shell_command', 'execute_command']) {
    assert.equal(isShellTaskName(name), true, name);
  }
  assert.equal(isShellTaskName('mcp__termipod__bash'), true);
  assert.equal(isShellTaskName('mcp__local__EXEC_COMMAND'), true);
  for (const name of ['agent', 'Agent', 'AGENT', 'task', 'Task']) {
    assert.equal(isSubagentName(name), true, name);
  }
  assert.equal(isSubagentName('mcp__crew__task'), true);
  assert.equal(isSubagentName('mcp__crew__AGENT'), true);
});

test('name matching: near-miss names stay out', () => {
  // Exact-set / `__`-suffixed matching only — no substring hits: `tasklist`,
  // `mybash`, `execute` (not the wired `execute_command`), `subagent`, and
  // unrelated tools are NOT dock tasks.
  for (const name of ['tasklist', 'TaskList', 'mybash', 'bashful', 'execute', 'subagent', 'agents', 'Edit', 'mcp__x__mytask']) {
    assert.equal(isShellTaskName(name), false, name);
    assert.equal(isSubagentName(name), false, name);
  }
});

// --- status derivation via the lineage maps ------------------------------------

test('status derivation: results and updates resolve running / done / error', () => {
  const events = [
    call('a', 'Bash', { input: { command: 'sleep 5' } }), // no lineage → running
    call('b', 'bash', { input: { command: 'ls' } }),
    result('b'), // → done
    call('c', 'mcp__os__exec', { input: { command: 'false' } }),
    result('c', true), // → error
    call('d', 'Agent', { input: { prompt: 'scan the repo' } }),
    update('d', 'completed'), // → done (latest update wins)
    call('e', 'task'),
    update('e', 'failed'), // → error
  ];
  const m = derive(events);
  assert.equal(m.shellRunning, 1);
  assert.deepEqual(
    m.shellCalls.map((c) => [c.id, c.status]),
    [
      ['c-a', 'running'],
      ['c-c', 'error'],
      ['c-b', 'done'],
    ],
  );
  assert.equal(m.subagentRunning, 0);
  assert.deepEqual(
    m.subagentCalls.map((c) => [c.id, c.status]),
    [
      ['c-e', 'error'],
      ['c-d', 'done'],
    ],
  );
  // Rows carry the name as wired + the key argument.
  assert.equal(m.shellCalls[0].name, 'Bash');
  assert.equal(m.shellCalls[0].arg, 'sleep 5');
  assert.equal(m.subagentCalls[1].arg, 'scan the repo');
});

test('a tool_call without a payload name falls back to the lineage nameById', () => {
  const noName = ev('tool_call', { id: 'a', input: { command: 'ls' } });
  const maps = mapsFor([noName]);
  maps.nameById.set('a', 'bash'); // backfilled from the session's lineage
  assert.equal(deriveStateDock([noName], maps).shellRunning, 1);
  assert.equal(deriveStateDock([noName], mapsFor([noName])).shellRunning, 0); // unnamed → uncounted
});

// --- todos ---------------------------------------------------------------------

test('todos: the newest plan event wins; done/total counts completed entries', () => {
  const events = [
    plan([{ content: 'old', status: 'pending' }]),
    plan([
      { content: 'one', status: 'completed' },
      { content: 'two', status: 'in_progress' },
      { content: 'three', status: 'pending' },
      { content: 'four', status: 'done' }, // tolerated as completed (planMark parity)
    ]),
  ];
  const m = derive(events);
  assert.deepEqual({ done: m.todos?.done, total: m.todos?.total }, { done: 2, total: 4 });
  assert.deepEqual(
    m.todos?.items.map((i) => [i.content, i.status]),
    [
      ['one', 'completed'],
      ['two', 'in_progress'],
      ['three', 'pending'],
      ['four', 'done'],
    ],
  );
});

test('todos: no plan event → no todos snapshot (no chip)', () => {
  const m = derive([call('a', 'Bash')]);
  assert.equal(m.todos, undefined);
  assert.deepEqual(visibleDockChips(m), ['tasks']);
});

test('todos: a plan with a missing/non-array entries payload snapshots empty', () => {
  const m = derive([ev('plan', { note: 'no entries here' })]);
  assert.deepEqual({ done: m.todos?.done, total: m.todos?.total }, { done: 0, total: 0 });
  assert.deepEqual(visibleDockChips(m), ['todos']); // a plan event exists — chip shows
});

// --- chip visibility -------------------------------------------------------------

test('chip visibility: Tasks/Sub-agents only while running; Todos once a plan exists', () => {
  // Nothing dock-worthy → no chips (the dock hides entirely).
  assert.deepEqual(visibleDockChips(derive([ev('text', { text: 'hi' })])), []);
  // A RUNNING shell call earns the Tasks chip.
  assert.deepEqual(visibleDockChips(derive([call('a', 'Bash')])), ['tasks']);
  // Settled shell calls alone do not (chips track live state).
  assert.deepEqual(visibleDockChips(derive([call('a', 'Bash'), result('a')])), []);
  // Running sub-agent, same rule.
  assert.deepEqual(visibleDockChips(derive([call('a', 'agent')])), ['subagents']);
  assert.deepEqual(visibleDockChips(derive([call('a', 'mcp__crew__Task'), result('a')])), []);
  // A plan earns the Todos chip even with nothing running.
  assert.deepEqual(visibleDockChips(derive([plan([{ content: 'x', status: 'pending' }])])), ['todos']);
  // All three, in dock order.
  const all = derive([plan([]), call('a', 'Bash'), call('b', 'Task')]);
  assert.deepEqual(visibleDockChips(all), ['tasks', 'subagents', 'todos']);
});

// --- lens invariance --------------------------------------------------------------

test('lens invariance: dropping unrelated kinds cannot move the counts', () => {
  const full = [
    ev('text', { text: 'working…' }),
    call('a', 'Bash', { input: { command: 'make' } }),
    ev('usage', { input_tokens: 10 }),
    plan([
      { content: 'one', status: 'completed' },
      { content: 'two', status: 'pending' },
    ]),
    call('b', 'Agent'),
    ev('tool_call', { id: 'x', name: 'Edit' }), // a non-dock tool — never counted
    result('b'),
  ];
  const fromFull = derive(full);
  // The dock reads the FULL session list in production; any subset that
  // keeps the plan + dock tool_calls and their lineage (what a lens can
  // never guarantee, which is why the dock is not lens-fed) must yield the
  // identical model — the derivation only reads those kinds.
  const subset = full.filter(
    (e) => e.kind === 'tool_call' || e.kind === 'plan' || e.kind === 'tool_result' || e.kind === 'tool_call_update',
  );
  assert.deepEqual(derive(subset), fromFull);
  // And the full-list model is the session state as designed:
  assert.equal(fromFull.shellRunning, 1);
  assert.equal(fromFull.subagentRunning, 0); // b settled → no Sub-agents chip
  assert.equal(fromFull.subagentCalls.length, 1); // …but it still lists, settled
  assert.deepEqual({ done: fromFull.todos?.done, total: fromFull.todos?.total }, { done: 1, total: 2 });
});

// --- ordering + cap -----------------------------------------------------------------

test('ordering: running first (feed order), then settled newest-first, capped at 20', () => {
  const events: FeedEvent[] = [];
  for (const id of ['a', 'b', 'c']) events.push(call(id, 'Bash')); // 3 running
  for (let i = 0; i < 25; i += 1) {
    const id = `s${String(i).padStart(2, '0')}`;
    events.push(call(id, 'bash'));
    events.push(result(id)); // 25 settled, s00 oldest … s24 newest
  }
  const m = derive(events);
  assert.equal(m.shellRunning, 3); // the true running count, never capped
  assert.equal(m.shellCalls.length, MAX_DOCK_CALLS);
  assert.deepEqual(
    m.shellCalls.map((c) => c.id),
    ['c-a', 'c-b', 'c-c', ...Array.from({ length: 17 }, (_, i) => `c-s${String(24 - i).padStart(2, '0')}`)],
  );
  // Past the cap even when everything runs: the first 20 running survive.
  const many: FeedEvent[] = [];
  for (let i = 0; i < MAX_DOCK_CALLS + 2; i += 1) many.push(call(`r${i}`, 'exec'));
  const m2 = derive(many);
  assert.equal(m2.shellRunning, MAX_DOCK_CALLS + 2);
  assert.equal(m2.shellCalls.length, MAX_DOCK_CALLS);
  assert.equal(m2.shellCalls[0].id, 'c-r0');
});

// --- key argument ---------------------------------------------------------------------

test('toolCallKeyArg: command/prompt-class keys win; first string value is the fallback', () => {
  assert.equal(toolCallKeyArg({ command: 'ls -la', path: '/x' }), 'ls -la');
  assert.equal(toolCallKeyArg({ prompt: 'scan the repo' }), 'scan the repo');
  assert.equal(toolCallKeyArg({ description: 'd', task: 't' }), 'd');
  assert.equal(toolCallKeyArg({ other: 'first' }), 'first');
  assert.equal(toolCallKeyArg({ count: 3 }), undefined);
  assert.equal(toolCallKeyArg(undefined), undefined);
  assert.equal(toolCallKeyArg('nope'), undefined);
});

// --- R5: one sub-agent's own activity -----------------------------------------

/// Frames shaped like the real thing. The field names come from a MEASURED
/// claude-code 2.1.220 run under `--print --output-format stream-json` (a real
/// Agent call), translated by the frame profile: a sub-agent's own events carry
/// `parent_tool_use_id` + `subagent: true`, and the task_* system frames carry
/// the same id as `tool_use_id`.
function subEvent(kind: string, parentId: string, payload: Record<string, unknown>): FeedEvent {
  return ev(kind, { ...payload, parent_tool_use_id: parentId, subagent: true, subagent_type: 'general-purpose' });
}

function detailFor(events: FeedEvent[], toolId: string) {
  const model = derive(events);
  const row = model.subagentCalls.find((c) => c.toolId === toolId);
  assert.ok(row !== undefined, `no dock row for ${toolId}`);
  return subagentDetail(events, row, mapsFor(events));
}

test('a dock row carries the TOOL id, not just its event id', () => {
  // The join key. `id` is the event row (`c-t1` here); `toolId` is what a
  // sub-agent's events name as their parent. Asserting only on `id` would pass
  // with the two confused, and the panel would then be empty for every agent.
  const events = [call('t1', 'Agent', { input: { description: 'go look' } })];
  const row = derive(events).subagentCalls[0];
  assert.equal(row.toolId, 't1');
  assert.notEqual(row.id, row.toolId);
});

test('a sub-agent detail collects only ITS events, not a sibling’s', () => {
  // The separating input: two sub-agents running at once. A filter that merely
  // asked "is this event a subagent's?" (the `subagent:true` boolean alone)
  // would put both agents' work under each row and read as one agent doing
  // twice the work.
  const events = [
    call('t1', 'Agent', { input: { prompt: 'first job', subagent_type: 'general-purpose' } }),
    call('t2', 'Agent', { input: { prompt: 'second job' } }),
    subEvent('tool_call', 't1', { id: 'inner-1', name: 'Bash', input: { command: 'ls /one' } }),
    subEvent('tool_call', 't2', { id: 'inner-2', name: 'Bash', input: { command: 'ls /two' } }),
    subEvent('text', 't1', { text: 'first is done' }),
  ];
  const d1 = detailFor(events, 't1');
  assert.equal(d1.prompt, 'first job');
  assert.equal(d1.subagentType, 'general-purpose');
  assert.deepEqual(d1.lines.map((l) => l.detail), ['ls /one', 'first is done']);

  const d2 = detailFor(events, 't2');
  assert.deepEqual(d2.lines.map((l) => l.detail), ['ls /two']);
  assert.equal(d2.prompt, 'second job');
});

test('task_progress joins on tool_use_id and the newest one is what it is doing now', () => {
  const events = [
    call('t1', 'Agent', { input: { description: 'dig' } }),
    ev('system', { subtype: 'task_progress', tool_use_id: 't1', description: 'Running Read' }),
    ev('system', { subtype: 'task_progress', tool_use_id: 't1', description: 'Running Grep' }),
  ];
  const d = detailFor(events, 't1');
  assert.equal(d.lastActivity, 'Running Grep', 'forward scan — newest wins');
  assert.equal(d.lines.length, 2, 'both progress lines are the activity stream');
});

test('the spawning call is the header, never a line in its own stream', () => {
  const events = [call('t1', 'Agent', { input: { prompt: 'p' } }), subEvent('text', 't1', { text: 'x' })];
  const d = detailFor(events, 't1');
  assert.equal(d.lines.length, 1);
  assert.equal(d.lines[0].detail, 'x');
});

test('an engine that reports no provenance says so; a quiet sub-agent does not', () => {
  // Two empty lists, two different sentences. codex/gemini publish no
  // per-sub-agent correlation at all, so the panel must not imply the agent
  // has simply been idle.
  const silent = [call('t1', 'Agent', { input: { prompt: 'ask codex' } })];
  const d = detailFor(silent, 't1');
  assert.equal(d.lines.length, 0);
  assert.equal(d.reported, false, 'no event in the session carries provenance');
  assert.equal(d.prompt, 'ask codex', 'the request is still worth showing');

  // Same shape, but another sub-agent in the session HAS reported — so the
  // engine does publish provenance and this one is merely quiet.
  const quiet = [
    call('t1', 'Agent', { input: { prompt: 'not started' } }),
    call('t2', 'Agent', { input: { prompt: 'busy' } }),
    subEvent('tool_call', 't2', { id: 'i', name: 'Bash', input: { command: 'ls' } }),
  ];
  assert.equal(detailFor(quiet, 't1').reported, true);
});

test('a tool_result does not double the row its call already shows', () => {
  const events = [
    call('t1', 'Agent', { input: { prompt: 'p' } }),
    subEvent('tool_call', 't1', { id: 'inner', name: 'Bash', input: { command: 'ls' } }),
    ev('tool_result', { tool_use_id: 'inner', parent_tool_use_id: 't1', subagent: true, content: 'ok' }),
  ];
  const d = detailFor(events, 't1');
  assert.deepEqual(d.lines.map((l) => l.kind), ['tool_call']);
  assert.equal(d.lines[0].status, 'done', 'the result resolves the call’s status instead');
});

test('kimi’s agent-id provenance is accepted as the same join', () => {
  const events = [
    call('agent-9', 'task', { input: { prompt: 'kimi job' } }),
    ev('text', { text: 'sub report', subagent: true, kimi_agent_id: 'agent-9' }),
  ];
  const d = detailFor(events, 'agent-9');
  assert.deepEqual(d.lines.map((l) => l.detail), ['sub report']);
  assert.equal(d.reported, true);
});
