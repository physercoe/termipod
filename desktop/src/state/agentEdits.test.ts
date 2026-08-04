import { test, beforeEach } from 'node:test';
import assert from 'node:assert/strict';
import { agentEditCount, agentEditTitle, latestAgentEdit, pushCapped, useAgentEdits, type AgentEdit } from './agentEdits.ts';

/// The agent-edit ring (coworking B6). Its whole job is to be there when a user
/// wants an agent's write taken back, so every test below is a way of losing
/// that: dropping the wrong end at the cap, leaving a chip up over an empty
/// stack, or reverting one document with another's body.

function edit(before: string, at = 1): AgentEdit {
  return { before, by: 'kimi-1', at };
}

beforeEach(() => useAgentEdits.setState({ byDoc: {} }));

test('the cap drops the OLDEST edit, never the newest', () => {
  // The newest is the one the user is looking at and most likely to reject —
  // evicting from the wrong end makes exactly that one unrevertible.
  const stack = Array.from({ length: 3 }, (_, i) => edit(`v${i}`));
  const out = pushCapped(stack, edit('v3'), 3);
  assert.deepEqual(
    out.map((e) => e.before),
    ['v1', 'v2', 'v3'],
  );
});

test('under the cap nothing is dropped', () => {
  const out = pushCapped([edit('a')], edit('b'), 5);
  assert.equal(out.length, 2);
});

test('record then revert returns the body from BEFORE the write', () => {
  const s = useAgentEdits.getState();
  s.record('doc1', edit('original'));
  assert.equal(useAgentEdits.getState().revert('doc1'), 'original');
});

test('reverts unwind newest-first', () => {
  const s = useAgentEdits.getState();
  s.record('doc1', edit('v0'));
  s.record('doc1', edit('v1'));
  assert.equal(useAgentEdits.getState().revert('doc1'), 'v1');
  assert.equal(useAgentEdits.getState().revert('doc1'), 'v0');
});

test('reverting an untouched document returns null rather than a wrong body', () => {
  assert.equal(useAgentEdits.getState().revert('nope'), null);
});

test('an emptied stack becomes absent, so the chip cannot linger over nothing', () => {
  const s = useAgentEdits.getState();
  s.record('doc1', edit('v0'));
  useAgentEdits.getState().revert('doc1');
  const { byDoc } = useAgentEdits.getState();
  assert.equal(byDoc.doc1, undefined, 'an empty array would still read as "this doc has agent edits"');
  assert.equal(latestAgentEdit(byDoc, 'doc1'), undefined);
  assert.equal(agentEditCount(byDoc, 'doc1'), 0);
});

test('documents are independent — one revert never reaches another', () => {
  const s = useAgentEdits.getState();
  s.record('doc1', edit('one'));
  s.record('doc2', edit('two'));
  assert.equal(useAgentEdits.getState().revert('doc1'), 'one');
  assert.equal(agentEditCount(useAgentEdits.getState().byDoc, 'doc2'), 1);
});

test('clear drops a closed document entirely', () => {
  const s = useAgentEdits.getState();
  s.record('doc1', edit('one'));
  useAgentEdits.getState().clear('doc1');
  assert.equal(agentEditCount(useAgentEdits.getState().byDoc, 'doc1'), 0);
  assert.equal(useAgentEdits.getState().revert('doc1'), null);
});

test('latestAgentEdit reports the newest attribution, for the chip', () => {
  const s = useAgentEdits.getState();
  s.record('doc1', { before: 'v0', by: 'kimi-1', at: 10 });
  s.record('doc1', { before: 'v1', by: 'codex-2', reason: 'renamed the box', at: 20 });
  const latest = latestAgentEdit(useAgentEdits.getState().byDoc, 'doc1');
  assert.equal(latest?.by, 'codex-2');
  assert.equal(latest?.reason, 'renamed the box');
  assert.equal(latest?.at, 20);
});

// ── the chip's tooltip ───────────────────────────────────────────────────────

const T: Record<string, string> = {
  'author.agentEditedBy': 'Edited by {agent} — click to undo',
  'author.agentEditCount': '{n} agent edits to undo',
};
const t = (k: string): string => T[k] ?? k;

test('the tooltip names the agent, and stays silent about a count of one', () => {
  assert.equal(agentEditTitle(edit('v'), 1, t), 'Edited by kimi-1 — click to undo');
});

test('the tooltip reports how many reverts remain once there is more than one', () => {
  assert.match(agentEditTitle(edit('v'), 3, t), /\(3 agent edits to undo\)/);
});

test("the agent's reason goes last so a long one cannot push the attribution out of view", () => {
  const out = agentEditTitle({ before: 'v', by: 'codex-2', reason: 'renamed the box', at: 1 }, 1, t);
  assert.match(out, /^Edited by codex-2 .* — renamed the box$/);
});

test('an empty reason does not leave a dangling dash', () => {
  assert.equal(agentEditTitle({ before: 'v', by: 'k', reason: '', at: 1 }, 1, t), 'Edited by k — click to undo');
});

test('no edit means no tooltip', () => {
  assert.equal(agentEditTitle(undefined, 0, t), '');
});

test('recording does not mutate the previous array', () => {
  // The store publishes `byDoc` to React; mutating in place would leave the
  // chip stale because the reference never changed.
  const s = useAgentEdits.getState();
  s.record('doc1', edit('v0'));
  const before = useAgentEdits.getState().byDoc.doc1;
  useAgentEdits.getState().record('doc1', edit('v1'));
  assert.equal(before.length, 1, 'the array handed out earlier must not have grown');
  assert.notEqual(useAgentEdits.getState().byDoc.doc1, before);
});
