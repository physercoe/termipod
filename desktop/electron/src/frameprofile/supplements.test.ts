/// The driver-side supplements. Run with `node --test`.
///
/// These exist because a profile renames fields but never values (the grammar
/// has no comparisons by design), so an engine whose enum spelling differs from
/// the vocabulary's needs one line of code — on BOTH sides of the parity
/// boundary, or the hub and the Companion produce different transcripts from
/// identical rules.
import { test } from 'node:test';
import assert from 'node:assert/strict';

import { canonicalPlanStatus, canonicalizePlanEntries } from './supplements.ts';

test('codex plan statuses land in the vocabulary spelling', () => {
  // codex-cli 0.133.0's TurnPlanStepStatus is {pending, inProgress, completed}.
  // Two of the three already agree with ACP's, which got to `plan` first.
  assert.equal(canonicalPlanStatus('inProgress'), 'in_progress');
  assert.equal(canonicalPlanStatus('pending'), 'pending');
  assert.equal(canonicalPlanStatus('completed'), 'completed');
});

test('an unrecognized status passes through rather than being rewritten', () => {
  // A future codex status this build has never seen is data we should not
  // invent a mapping for. Both clients read an unknown status as "not started",
  // which is the safe read; guessing would not be.
  assert.equal(canonicalPlanStatus('blocked'), 'blocked');
  assert.equal(canonicalPlanStatus(''), '');
  assert.equal(canonicalPlanStatus('in_progress'), 'in_progress');
});

test('the rename is what stops a running step rendering as unstarted', () => {
  // This payload is exactly what the profile's payload_lists projection
  // produces — see the `payload-lists/projects-elements-in-order` case in the
  // shared translate fixture, whose recorded output still says `inProgress`.
  // The projection renames the FIELD (`step` → `content`) and stops there; the
  // value is this function's job, and without it every codex turn would have
  // shown its running step as not yet started, with nothing reporting an error.
  const payload: Record<string, unknown> = {
    entries: [
      { content: 'one', status: 'completed' },
      { content: 'two', status: 'inProgress' },
      { content: 'three', status: 'pending' },
    ],
  };
  canonicalizePlanEntries(payload);
  assert.deepStrictEqual(payload.entries, [
    { content: 'one', status: 'completed' },
    { content: 'two', status: 'in_progress' },
    { content: 'three', status: 'pending' },
  ]);
});

test('a payload without usable entries is left exactly as it was', () => {
  // Mirrors the Go driver: entries that aren't objects, and statuses that
  // aren't strings, are a profile bug to surface rather than one to coerce.
  const odd: Record<string, unknown> = {
    entries: [{ content: 'one' }, 'scalar', null, [1], { status: 7 }],
  };
  const before = JSON.stringify(odd);
  canonicalizePlanEntries(odd);
  assert.equal(JSON.stringify(odd), before);

  for (const payload of [null, undefined, {}, { entries: 'not-a-list' }]) {
    assert.doesNotThrow(() => canonicalizePlanEntries(payload));
  }
});
