import assert from 'node:assert/strict';
import { test } from 'node:test';
import { isAllowedForgeUrl } from './forgepolicy.ts';

test('forge URL policy: https is always allowed', () => {
  assert.equal(isAllowedForgeUrl('https://api.github.com/repos/a/b', undefined), true);
  assert.equal(isAllowedForgeUrl('https://huggingface.co/a/b/resolve/x/y', '1'), true);
});

test('forge URL policy: plain http is rejected outside the e2e harness', () => {
  assert.equal(isAllowedForgeUrl('http://127.0.0.1:8080/repos/a/b', undefined), false);
  assert.equal(isAllowedForgeUrl('http://localhost:8080/x', ''), false);
  assert.equal(isAllowedForgeUrl('http://[::1]:8080/x', '0'), false);
});

test('forge URL policy: loopback http is allowed only under TERMIPOD_E2E=1', () => {
  assert.equal(isAllowedForgeUrl('http://127.0.0.1:8080/repos/a/b', '1'), true);
  assert.equal(isAllowedForgeUrl('http://localhost:8080/x', '1'), true);
  assert.equal(isAllowedForgeUrl('http://[::1]:8080/x', '1'), true);
  // Non-loopback http stays rejected even under the harness.
  assert.equal(isAllowedForgeUrl('http://example.com/x', '1'), false);
  assert.equal(isAllowedForgeUrl('http://127.0.0.1.evil.com/x', '1'), false);
  assert.equal(isAllowedForgeUrl('ftp://127.0.0.1/x', '1'), false);
});
