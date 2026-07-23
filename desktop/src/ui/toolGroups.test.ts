/// P1 tool-lineage + grouping checks (agent-transcript-redesign §6 P1):
///  - the tool_call_update visibility rule (mobile feed_reducer.dart:902-924):
///    visible non-gated parent → hidden; gated parent / no parent → shown.
///  - the MCP gate-name model (bare + mcp__<server>__<name> suffix; the
///    request_help asymmetry between the two rule sets).
///  - run grouping (kimi-web tool-stack): ≥2 consecutive tool_calls → one
///    group; lone standalone; break on non-tool row and turn_id change.
///  - per-call status + aggregate (running > error > done).
/// The frontend package has no CI test runner; run locally with
/// `node --test src/ui/toolGroups.test.ts` from `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  aggregateToolStatus,
  callToolId,
  groupToolCalls,
  isGateToolCallName,
  isGatedToolName,
  isToolCallUpdateHidden,
  toolCallUpdateParentId,
  toolDiffStats,
  toolStatusOf,
} from './toolGroups.ts';
import type { Entity } from '../hub/types';
import type { FeedEvent } from './EventCard';

let n = 0;
function ev(kind: string, payload: Record<string, unknown>): FeedEvent {
  n += 1;
  return { id: `e${n}`, seq: n, ord: n, kind, producer: 'agent', payload };
}
function call(id: string, extra: Record<string, unknown> = {}): FeedEvent {
  return ev('tool_call', { id, name: 'Bash', ...extra });
}

// --- tool_call_update visibility (the feedLens parity rule) -----------------

test('tool_call_update: a visible non-gated parent hides the update', () => {
  const toolNames = new Map([['t1', 'Bash']]);
  assert.equal(isToolCallUpdateHidden({ toolCallId: 't1', status: 'completed' }, toolNames), true);
});

test('tool_call_update: a gated parent (bare or mcp-suffixed) leaves the update standalone', () => {
  const toolNames = new Map([
    ['g1', 'request_approval'],
    ['g2', 'mcp__termipod__request_select'],
    ['g3', 'mcp__termipod__permission_prompt'],
    ['g4', 'request_decision'], // stale-template spelling hides the parent too
    ['g5', 'request_help'], // gated as a parent even though not a tool_call gate
  ]);
  for (const id of ['g1', 'g2', 'g3', 'g4', 'g5']) {
    assert.equal(isToolCallUpdateHidden({ toolCallId: id }, toolNames), false, id);
  }
});

test('tool_call_update: no parent in scope (or no id) leaves the update standalone', () => {
  const toolNames = new Map([['t1', 'Bash']]);
  assert.equal(isToolCallUpdateHidden({ toolCallId: 'nope' }, toolNames), false);
  assert.equal(isToolCallUpdateHidden({ status: 'completed' }, toolNames), false);
  assert.equal(isToolCallUpdateHidden({ tool_call_id: 't1', status: 'completed' }, toolNames), true); // snake_case id works
});

// --- gate names ---------------------------------------------------------------

test('gate names match bare and as mcp__<server>__<name> suffix', () => {
  for (const name of ['permission_prompt', 'request_select', 'request_decision', 'request_approval']) {
    assert.equal(isGateToolCallName(name), true, name);
    assert.equal(isGateToolCallName(`mcp__termipod__${name}`), true, `mcp__termipod__${name}`);
  }
  assert.equal(isGateToolCallName('Bash'), false);
  assert.equal(isGateToolCallName('mcp__termipod__Bash'), false);
  // The asymmetry (mobile _kGateToolNames vs the tool_call gate set):
  // request_help gates an update's PARENT but does not hide its own tool_call.
  assert.equal(isGateToolCallName('request_help'), false);
  assert.equal(isGatedToolName('request_help'), true);
  assert.equal(isGatedToolName('mcp__termipod__request_help'), true);
});

// --- run grouping -------------------------------------------------------------

test('≥2 consecutive tool_calls group into one row; a lone call stays standalone', () => {
  const rows = groupToolCalls([call('a'), call('b'), call('c')]);
  assert.equal(rows.length, 1);
  assert.equal(rows[0].events.length, 3);
  assert.equal(rows[0].key, `grp:${rows[0].events[0].id}`);

  const lone = groupToolCalls([call('a')]);
  assert.equal(lone.length, 1);
  assert.equal(lone[0].events.length, 1);
  assert.equal(lone[0].key, lone[0].events[0].id);
});

test('the run breaks at any non-tool_call row', () => {
  const rows = groupToolCalls([call('a'), ev('text', { text: 'hi' }), call('b'), call('c')]);
  assert.equal(rows.length, 3);
  assert.deepEqual(
    rows.map((r) => r.events.length),
    [1, 1, 2],
  );
});

test('the run breaks at a turn_id change when both neighbours carry one', () => {
  const rows = groupToolCalls([
    call('a', { turn_id: 't1' }),
    call('b', { turn_id: 't1' }),
    call('c', { turn_id: 't2' }),
    call('d', { turn_id: 't2' }),
  ]);
  assert.equal(rows.length, 2);
  assert.deepEqual(
    rows.map((r) => r.events.length),
    [2, 2],
  );
  // A call without a turn stamp joins the run (it can't be proven a new turn).
  const noStamp = groupToolCalls([call('a', { turn_id: 't1' }), call('b'), call('c', { turn_id: 't1' })]);
  assert.equal(noStamp.length, 1);
  assert.equal(noStamp[0].events.length, 3);
});

// --- per-call status + aggregate ----------------------------------------------

test('per-call status: result resolves failed/completed, else pending → running', () => {
  const results = new Map<string, Entity>([
    ['bad', { is_error: true }],
    ['good', { is_error: false }],
  ]);
  const updates = new Map<string, Entity>();
  assert.equal(toolStatusOf(call('bad'), results, updates), 'error');
  assert.equal(toolStatusOf(call('good'), results, updates), 'done');
  assert.equal(toolStatusOf(call('fresh'), results, updates), 'running');
});

test('per-call status: the latest update status wins over result + call status (mobile order)', () => {
  const results = new Map<string, Entity>([['x', { is_error: true }]]);
  const updates = new Map<string, Entity>([
    ['x', { status: 'in_progress' }],
    ['dead', { status: 'failed' }],
    ['fin', { status: 'completed' }],
  ]);
  // Mobile: a non-empty update status IS the status — an in-flight update
  // outranks even an error result (event_card.dart:386-389).
  assert.equal(toolStatusOf(call('x'), results, updates), 'running');
  assert.equal(toolStatusOf(call('dead'), new Map(), updates), 'error');
  assert.equal(toolStatusOf(call('fin'), new Map(), updates), 'done');
  // The call's own status field is the fallback when no update exists.
  assert.equal(toolStatusOf(call('y', { status: 'failed' }), new Map(), new Map()), 'error');
});

test('per-call status: ACP-style toolCallId ids pair across maps', () => {
  const updates = new Map<string, Entity>([['acp-1', { status: 'completed' }]]);
  const c = ev('tool_call', { toolCallId: 'acp-1', name: 'Edit' });
  assert.equal(callToolId(c.payload), 'acp-1');
  assert.equal(toolStatusOf(c, new Map(), updates), 'done');
});

test('aggregate: running > error > done', () => {
  assert.equal(aggregateToolStatus(['error', 'running']), 'running');
  assert.equal(aggregateToolStatus(['done', 'error']), 'error');
  assert.equal(aggregateToolStatus(['done', 'done']), 'done');
  assert.equal(aggregateToolStatus([]), 'done');
});

// --- misc lineage helpers -------------------------------------------------------

test('toolCallUpdateParentId: toolCallId wins, tool_call_id falls back, values coerce', () => {
  assert.equal(toolCallUpdateParentId({ toolCallId: 'a', tool_call_id: 'b' }), 'a');
  assert.equal(toolCallUpdateParentId({ tool_call_id: 'b' }), 'b');
  assert.equal(toolCallUpdateParentId({ toolCallId: 7 }), '7');
  assert.equal(toolCallUpdateParentId({}), undefined);
  assert.equal(toolCallUpdateParentId({ toolCallId: '' }), undefined);
});

test('toolDiffStats: ACP diff content blocks sum to a +A −R readout', () => {
  const c = {
    content: [
      { type: 'diff', path: 'a.ts', oldText: 'x\ny', newText: 'x\nz\nw' },
      { type: 'content', content: { type: 'text', text: 'ignored' } },
    ],
  };
  assert.deepEqual(toolDiffStats(c), { added: 3, removed: 2 });
  const upd = { content: [{ type: 'diff', path: 'b.ts', oldText: '', newText: 'new' }] };
  assert.deepEqual(toolDiffStats({}, upd), { added: 1, removed: 0 });
  assert.equal(toolDiffStats({}), undefined);
  assert.equal(toolDiffStats({ content: 'not-an-array' }), undefined);
});
