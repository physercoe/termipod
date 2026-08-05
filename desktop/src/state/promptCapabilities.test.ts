/// F3 — the composer's capability gate. Run locally:
/// `node --test src/state/promptCapabilities.test.ts` from `desktop/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  anyAttachable,
  attachAllowed,
  drivingModeOf,
  NO_PROMPT_CAPABILITIES,
  promptCapabilities,
} from './promptCapabilities.ts';

/// The shipped registry's shape, trimmed to what the gate reads.
const FAMILIES = [
  {
    family: 'claude-code',
    prompt_image: { M1: true, M2: true, M4: false },
    prompt_pdf: { M1: true, M2: true, M4: false },
  },
  {
    family: 'gemini-cli',
    prompt_image: { M1: true, M2: false, M4: false },
    prompt_pdf: { M1: true, M2: false, M4: false },
    prompt_audio: { M1: true, M2: false, M4: false },
    prompt_video: { M1: true, M2: false, M4: false },
  },
  { family: 'codex' },
];

test('flags are per MODE, not per engine', () => {
  // The whole reason the registry keys these by mode: the same engine accepts
  // an image on one wire and cannot on another.
  assert.deepEqual(promptCapabilities('claude-code', 'M2', FAMILIES), {
    image: true,
    pdf: true,
    audio: false,
    video: false,
  });
  assert.deepEqual(promptCapabilities('claude-code', 'M4', FAMILIES), NO_PROMPT_CAPABILITIES);
  assert.deepEqual(promptCapabilities('gemini-cli', 'M1', FAMILIES), {
    image: true,
    pdf: true,
    audio: true,
    video: true,
  });
  assert.deepEqual(promptCapabilities('gemini-cli', 'M2', FAMILIES), NO_PROMPT_CAPABILITIES);
});

test('an undeclared flag, an unlisted family and an unknown engine all mean no', () => {
  // codex declares no modality maps at all.
  assert.deepEqual(promptCapabilities('codex', 'M2', FAMILIES), NO_PROMPT_CAPABILITIES);
  assert.deepEqual(promptCapabilities('kimi-code-ts', 'M1', FAMILIES), NO_PROMPT_CAPABILITIES);
  assert.deepEqual(promptCapabilities(undefined, 'M2', FAMILIES), NO_PROMPT_CAPABILITIES);
  assert.deepEqual(promptCapabilities('', 'M2', FAMILIES), NO_PROMPT_CAPABILITIES);
  // Registry not loaded yet — the composer must not assume a capability it
  // hasn't been told about.
  assert.deepEqual(promptCapabilities('claude-code', 'M2', []), NO_PROMPT_CAPABILITIES);
});

test('a non-boolean flag value is not truthy-coerced', () => {
  // The map is `map[string]bool` hub-side, but the client reads untyped JSON;
  // a string "true" would sail through a truthiness check and claim a channel
  // the engine never declared.
  const odd = [{ family: 'x', prompt_image: { M2: 'true' }, prompt_pdf: { M2: 1 } }];
  assert.deepEqual(promptCapabilities('x', 'M2', odd), NO_PROMPT_CAPABILITIES);
});

test('the driving mode prefers the resolved field and defaults to M4', () => {
  assert.equal(drivingModeOf({ mode: 'M2', driving_mode: 'M4' }), 'M2');
  assert.equal(drivingModeOf({ driving_mode: 'M1' }), 'M1');
  // M4 is the safe default precisely because every modality flag is false
  // there — an unresolved agent grants nothing.
  assert.equal(drivingModeOf({}), 'M4');
  assert.equal(drivingModeOf({ mode: '' }), 'M4');
  assert.equal(drivingModeOf(undefined), 'M4');
});

test('text attachments need no engine capability', () => {
  // They are inlined into the body as a fenced block, so they ride the
  // ordinary text channel every engine has.
  assert.equal(attachAllowed('text', NO_PROMPT_CAPABILITIES), true);
  assert.equal(attachAllowed('image', NO_PROMPT_CAPABILITIES), false);
  const caps = { image: true, pdf: false, audio: false, video: false };
  assert.equal(attachAllowed('image', caps), true);
  assert.equal(attachAllowed('pdf', caps), false);
  assert.equal(attachAllowed('audio', caps), false);
  assert.equal(attachAllowed('video', caps), false);
});

test('anyAttachable is what decides the button exists', () => {
  assert.equal(anyAttachable(NO_PROMPT_CAPABILITIES), false);
  assert.equal(anyAttachable({ image: false, pdf: false, audio: false, video: true }), true);
  assert.equal(anyAttachable(promptCapabilities('claude-code', 'M2', FAMILIES)), true);
  assert.equal(anyAttachable(promptCapabilities('claude-code', 'M4', FAMILIES)), false);
});
