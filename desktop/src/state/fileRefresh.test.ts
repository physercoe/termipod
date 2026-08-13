import assert from 'node:assert/strict';
import test from 'node:test';
import { fingerprintBody } from './fileRefresh.ts';

test('fingerprintBody is stable for identical file content', () => {
  assert.equal(fingerprintBody('# report\n'), fingerprintBody('# report\n'));
});

test('fingerprintBody detects same-length and line-ending changes', () => {
  assert.notEqual(fingerprintBody('alpha'), fingerprintBody('alpHa'));
  assert.notEqual(fingerprintBody('a\nb\n'), fingerprintBody('a\r\nb\r\n'));
});

test('fingerprintBody includes content length', () => {
  assert.notEqual(fingerprintBody(''), fingerprintBody('\0'));
});
