import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  buildRecommendationSeed,
  mergeTargetResults,
  paperFingerprint,
  targetDue,
} from './discoveryMonitorCore.ts';

test('target cadence is due initially and again only after its interval', () => {
  assert.equal(targetDue('daily', undefined, 1), true);
  assert.equal(targetDue('daily', { lastRunAt: 100, seen: [] }, 100 + 86_399_999), false);
  assert.equal(targetDue('daily', { lastRunAt: 100, seen: [] }, 100 + 86_400_000), true);
  assert.equal(targetDue('weekly', { lastRunAt: 100, seen: [], lastError: 'offline' }, 100 + 899_999), false);
  assert.equal(targetDue('weekly', { lastRunAt: 100, seen: [], lastError: 'offline' }, 100 + 900_000), true);
});

test('updates dedupe by DOI and remember earlier target results', () => {
  const papers = [
    { paperId: 'one', doi: '10.1/ABC', title: 'First', authors: [] },
    { paperId: 'two', doi: '10.1/abc', title: 'Duplicate', authors: [] },
  ];
  assert.equal(paperFingerprint(papers[0]!), 'doi:10.1/abc');
  const first = mergeTargetResults([], undefined, papers, {
    originType: 'subscription', originId: 'sub', originLabel: 'Topic',
  }, 10, ['u1', 'u2']);
  assert.equal(first.added, 1);
  const second = mergeTargetResults(first.updates, first.run, papers, {
    originType: 'subscription', originId: 'sub', originLabel: 'Topic',
  }, 20, ['u3', 'u4']);
  assert.equal(second.added, 0);
});

test('recommendations favor curated topics and tags from one collection', () => {
  const seed = buildRecommendationSeed('c1', [{ id: 'c1', name: 'Reading' }], [{
    id: 'r1', type: 'article', title: 'Graph neural networks for molecules', authors: [], tags: ['drug discovery'],
    topics: ['Graph learning'], collectionIds: ['c1'], notes: '', addedAt: 1, rating: 5,
  }]);
  assert.ok(seed !== null);
  assert.equal(seed.collection.name, 'Reading');
  assert.ok(seed.query.includes('graph'));
  assert.ok(seed.query.includes('learning'));
  assert.equal(seed.references.length, 1);
});
