/// Tests for the desktop steward-spawn contract (state/stewardSpawn.ts) —
/// the handle taxonomy mirrors lib/services/steward_handle.dart, so these
/// pins keep the two ends of the convention from drifting apart.
/// Run locally: `node --test src/state/stewardSpawn.test.ts` from `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  defaultStewardName,
  isStewardHandle,
  normalizeStewardHandle,
  parseBackendKind,
  stewardTemplatePicks,
  suggestedNameFor,
  validateStewardName,
} from './stewardSpawn.ts';

test('isStewardHandle: steward / *-steward only — @steward and project stewards excluded', () => {
  assert.equal(isStewardHandle('steward'), true);
  assert.equal(isStewardHandle('research-steward'), true);
  assert.equal(isStewardHandle('@steward'), false);
  assert.equal(isStewardHandle('@steward.abcd1234'), false);
  assert.equal(isStewardHandle('worker-bee'), false);
  assert.equal(isStewardHandle(''), false);
});

test('normalizeStewardHandle: bare name gains -steward; steward and suffixed forms pass through', () => {
  assert.equal(normalizeStewardHandle('research'), 'research-steward');
  assert.equal(normalizeStewardHandle('steward'), 'steward');
  assert.equal(normalizeStewardHandle('infra-steward'), 'infra-steward');
  assert.equal(normalizeStewardHandle('  research  '), 'research-steward');
  assert.equal(normalizeStewardHandle(''), '');
});

test('validateStewardName: shape checked post-normalization', () => {
  assert.equal(validateStewardName('steward'), null);
  assert.equal(validateStewardName('research'), null);
  assert.equal(validateStewardName('infra-east'), null);
  assert.equal(validateStewardName(''), 'required');
  assert.equal(validateStewardName('   '), 'required');
  assert.equal(validateStewardName('Steward'), 'shape');
  assert.equal(validateStewardName('my steward'), 'shape');
  assert.equal(validateStewardName('9lives'), 'shape');
});

test('defaultStewardName: steward when free, empty when a live steward owns it', () => {
  assert.equal(defaultStewardName(new Set()), 'steward');
  assert.equal(defaultStewardName(new Set(['research-steward'])), 'steward');
  assert.equal(defaultStewardName(new Set(['steward'])), '');
});

test('stewardTemplatePicks: steward*.yaml minus the general-concierge singleton, sorted, with a fallback', () => {
  assert.deepEqual(
    stewardTemplatePicks(['worker.v1.yaml', 'steward.v1.yaml', 'steward.research.v1.yaml', 'steward.general.v1.yaml']),
    ['steward.research.v1.yaml', 'steward.v1.yaml'],
  );
  // Nothing listed (listing not loaded / stripped team): the shipped default.
  assert.deepEqual(stewardTemplatePicks([]), ['steward.v1.yaml']);
  assert.deepEqual(stewardTemplatePicks(['steward.general.v1.yaml']), ['steward.v1.yaml']);
});

test('suggestedNameFor: domain templates seed the name; off-convention gives nothing', () => {
  assert.equal(suggestedNameFor('steward.research.v1.yaml'), 'research');
  assert.equal(suggestedNameFor('steward.infra-east.v2.yaml'), 'infra-east');
  assert.equal(suggestedNameFor('steward.v1.yaml'), '');
  assert.equal(suggestedNameFor('steward.general.v1.yaml'), 'general');
});

test('parseBackendKind: reads backend.kind, never bleeds across blocks', () => {
  const yaml = ['persona:', '  kind: steward.v1', 'backend:', '  cmd: claude', '  kind: claude-code', 'limits:', '  kind: nope', ''].join(
    '\n',
  );
  assert.equal(parseBackendKind(yaml), 'claude-code');
  // The persona block's kind must not win, and a missing backend block is null.
  assert.equal(parseBackendKind('persona:\n  kind: steward.v1\n'), null);
  assert.equal(parseBackendKind(''), null);
});
