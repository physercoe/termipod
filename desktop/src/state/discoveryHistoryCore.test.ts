import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  discoveryQueryKey,
  MAX_RECENT_SEARCHES,
  upsertRecentSearch,
  upsertSavedSearch,
  type DiscoveryQuerySpec,
} from './discoveryHistoryCore.ts';

const base: DiscoveryQuerySpec = {
  query: 'graph learning',
  sourceId: 'openalex',
  authorFilter: '',
  yearFrom: '',
  yearTo: '',
  sort: 'relevance',
  findPdfs: true,
};

test('query identity includes provider and filters but normalizes whitespace and case', () => {
  assert.equal(discoveryQueryKey(base), discoveryQueryKey({ ...base, query: '  Graph   Learning ' }));
  assert.notEqual(discoveryQueryKey(base), discoveryQueryKey({ ...base, sourceId: 'google-scholar' }));
  assert.notEqual(discoveryQueryKey(base), discoveryQueryKey({ ...base, yearFrom: '2024' }));
});

test('recent searches deduplicate, move to the top, and stay bounded', () => {
  let recent = upsertRecentSearch([], base, 4, 10, 'one');
  recent = upsertRecentSearch(recent, { ...base, query: ' Graph  Learning ' }, 9, 20, 'two');
  assert.equal(recent.length, 1);
  assert.equal(recent[0]?.id, 'one');
  assert.equal(recent[0]?.resultCount, 9);
  assert.equal(recent[0]?.ranAt, 20);
  for (let i = 0; i < MAX_RECENT_SEARCHES + 5; i += 1) {
    recent = upsertRecentSearch(recent, { ...base, query: `query ${i}` }, i, 30 + i, `id-${i}`);
  }
  assert.equal(recent.length, MAX_RECENT_SEARCHES);
  assert.equal(recent[0]?.query, `query ${MAX_RECENT_SEARCHES + 4}`);
});

test('saving the same search is idempotent and preserves its chosen name', () => {
  let saved = upsertSavedSearch([], base, 10, 'saved-one', 'My topic');
  saved = [{ ...saved[0]!, schedule: 'weekly' }];
  saved = upsertSavedSearch(saved, { ...base, query: 'GRAPH LEARNING' }, 20, 'saved-two');
  assert.equal(saved.length, 1);
  assert.equal(saved[0]?.id, 'saved-one');
  assert.equal(saved[0]?.name, 'My topic');
  assert.equal(saved[0]?.savedAt, 10);
  assert.equal(saved[0]?.schedule, 'weekly');
});
