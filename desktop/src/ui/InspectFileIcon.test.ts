import test from 'node:test';
import assert from 'node:assert/strict';
import { inspectFileVisual } from './inspectFileVisual.ts';

test('inspect file visuals distinguish common code families', () => {
  assert.deepEqual(inspectFileVisual('client.ts'), { icon: 'code', tone: 'blue' });
  assert.deepEqual(inspectFileVisual('component.jsx'), { icon: 'code', tone: 'yellow' });
  assert.deepEqual(inspectFileVisual('main.rs'), { icon: 'code', tone: 'orange' });
  assert.deepEqual(inspectFileVisual('styles.scss'), { icon: 'code', tone: 'violet' });
});

test('inspect file visuals recognize semantic filenames and data formats', () => {
  assert.deepEqual(inspectFileVisual('/repo/README.md'), { icon: 'book', tone: 'blue' });
  assert.deepEqual(inspectFileVisual('Dockerfile'), { icon: 'terminal', tone: 'cyan' });
  assert.deepEqual(inspectFileVisual('package-lock.json'), { icon: 'lock', tone: 'yellow' });
  assert.deepEqual(inspectFileVisual('results.parquet'), { icon: 'table', tone: 'cyan' });
});

test('inspect file visuals keep unknown files quiet', () => {
  assert.deepEqual(inspectFileVisual('NOTICE'), { icon: 'file-text', tone: 'neutral' });
});
