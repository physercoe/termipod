import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  normalizeUiFontScale,
  scaledUiFontSizes,
  uiFontStack,
} from './appearance.ts';

test('font scale clamps and snaps to the supported five-percent ladder', () => {
  assert.equal(normalizeUiFontScale(0.1), 0.8);
  assert.equal(normalizeUiFontScale(2), 1.3);
  assert.equal(normalizeUiFontScale(1.07), 1.05);
  assert.equal(normalizeUiFontScale('1.18'), 1.2);
  assert.equal(normalizeUiFontScale('not-a-number'), 1);
});

test('font scale updates every semantic typography token', () => {
  assert.deepEqual(scaledUiFontSizes(1.2), {
    '--font-size-label': '13.20px',
    '--font-size-caption': '14.40px',
    '--font-size-body-small': '15.60px',
    '--font-size-body': '16.80px',
    '--font-size-subtitle': '19.20px',
    '--font-size-title': '21.60px',
    '--font-size-title-large': '24px',
  });
});

test('font choices preserve explicit fallback stacks', () => {
  assert.match(uiFontStack('inter'), /Inter Variable/);
  assert.match(uiFontStack('system'), /^system-ui/);
  assert.match(uiFontStack('mono'), /JetBrains Mono Variable/);
});
