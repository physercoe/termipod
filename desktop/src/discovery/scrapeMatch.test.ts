import assert from 'node:assert/strict';
import { test } from 'node:test';
import { isLikelySameWork } from './scrapeMatch.ts';

test('accepts punctuation and small subtitle differences for the same work', () => {
  assert.equal(
    isLikelySameWork(
      { title: 'Attention Is All You Need', year: 2017, authors: ['A. Vaswani'] },
      { title: 'Attention is all you need.', year: 2017, authors: ['Ashish Vaswani'] },
    ),
    true,
  );
});

test('rejects OpenAlex first-hit results with a different title, year, or first author', () => {
  assert.equal(
    isLikelySameWork({ title: 'Graph learning for molecules', year: 2024 }, { title: 'Graph learning for traffic', year: 2024 }),
    false,
  );
  assert.equal(
    isLikelySameWork({ title: 'A compact research title', year: 2024 }, { title: 'A compact research title', year: 2018 }),
    false,
  );
  assert.equal(
    isLikelySameWork(
      { title: 'A compact research title', authors: ['Alice Smith'] },
      { title: 'A compact research title', authors: ['Alice Jones'] },
    ),
    false,
  );
});
