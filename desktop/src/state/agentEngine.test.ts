/// Run locally: `node --test src/state/agentEngine.test.ts` from `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { agentEngine } from './agentEngine.ts';

test('the engine is backend.kind, not the agent kind', () => {
  // The case the field exists for: a steward's own kind names its template,
  // and reading it as the engine disables every engine-gated affordance for
  // exactly the agents the Companion is built around.
  assert.equal(agentEngine({ kind: 'steward.general', backend: { kind: 'claude-code' } }), 'claude-code');
  assert.equal(agentEngine({ kind: 'codex', backend: { kind: 'codex' } }), 'codex');
});

test('an unknown engine reads as undefined rather than a guess', () => {
  assert.equal(agentEngine(undefined), undefined);
  assert.equal(agentEngine({ kind: 'claude-code' }), undefined);
  assert.equal(agentEngine({ backend: {} }), undefined);
  assert.equal(agentEngine({ backend: { kind: '' } }), undefined);
  // A backend that isn't an object at all (an older row, a partial fetch).
  assert.equal(agentEngine({ backend: 'claude-code' }), undefined);
});
