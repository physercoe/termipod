import assert from 'node:assert/strict';
import { test } from 'node:test';
import { normalizeSerpApiPaper, serpApiSearchUrl } from './serpApiCore.ts';

test('maps Google Scholar organic metadata into a discovery paper', () => {
  const paper = normalizeSerpApiPaper({
    title: 'Attention Is All You Need',
    result_id: 'scholar-result-id',
    link: 'https://doi.org/10.5555/3295222.3295349',
    snippet: 'A transformer architecture.',
    publication_info: {
      summary: 'A Vaswani, N Shazeer - Advances in neural information processing systems, 2017 - proceedings.neurips.cc',
      authors: [{ name: 'A Vaswani' }, { name: 'N Shazeer' }],
    },
    inline_links: { cited_by: { total: 123456 } },
    resources: [{ file_format: 'PDF', link: 'https://example.test/paper.pdf' }],
  });

  assert.deepEqual(paper, {
    paperId: 'scholar-result-id',
    title: 'Attention Is All You Need',
    authors: ['A Vaswani', 'N Shazeer'],
    year: 2017,
    venue: 'Advances in neural information processing systems',
    abstract: 'A transformer architecture.',
    citationCount: 123456,
    doi: '10.5555/3295222.3295349',
    pdfUrl: 'https://example.test/paper.pdf',
    url: 'https://doi.org/10.5555/3295222.3295349',
  });
});

test('rejects title-less rows and tolerates sparse Scholar results', () => {
  assert.equal(normalizeSerpApiPaper({ snippet: 'missing title' }), null);
  assert.deepEqual(normalizeSerpApiPaper({ title: 'Sparse result' }), {
    paperId: 'Sparse result',
    title: 'Sparse result',
    authors: [],
    year: undefined,
    venue: undefined,
    abstract: undefined,
    citationCount: undefined,
    doi: undefined,
    pdfUrl: undefined,
    url: undefined,
  });
});

test('falls back to the Scholar summary when structured authors are absent', () => {
  const paper = normalizeSerpApiPaper({
    title: 'Summary-only authors',
    publication_info: { summary: 'A Author, B Researcher - Journal of Tests, 2024 - example.test' },
  });
  assert.deepEqual(paper?.authors, ['A Author', 'B Researcher']);
  assert.equal(paper?.year, 2024);
  assert.equal(paper?.venue, 'Journal of Tests');
});

test('builds a Google Scholar request, encodes the key, and caps a page at 20', () => {
  const url = new URL(serpApiSearchUrl('graph neural networks', 25, 'secret +/='));
  assert.equal(url.origin + url.pathname, 'https://serpapi.com/search.json');
  assert.equal(url.searchParams.get('engine'), 'google_scholar');
  assert.equal(url.searchParams.get('q'), 'graph neural networks');
  assert.equal(url.searchParams.get('api_key'), 'secret +/=');
  assert.equal(url.searchParams.get('num'), '20');
});
