import assert from 'node:assert/strict';
import { test } from 'node:test';
import { discoveryHandlers } from './discovery.ts';

test('SerpAPI transport fixes the Scholar endpoint and returns structured JSON', async () => {
  const originalFetch = globalThis.fetch;
  let requested = '';
  globalThis.fetch = (async (input) => {
    requested = String(input);
    return new Response(JSON.stringify({ organic_results: [{ title: 'Paper' }] }), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    });
  }) as typeof fetch;
  try {
    const out = await discoveryHandlers.serpapi_search(
      { query: 'graph neural networks', apiKey: 'test-secret', limit: 25, proxy: null },
      {} as never,
    );
    assert.deepEqual(out, { organic_results: [{ title: 'Paper' }] });
    const url = new URL(requested);
    assert.equal(url.origin + url.pathname, 'https://serpapi.com/search.json');
    assert.equal(url.searchParams.get('engine'), 'google_scholar');
    assert.equal(url.searchParams.get('q'), 'graph neural networks');
    assert.equal(url.searchParams.get('api_key'), 'test-secret');
    assert.equal(url.searchParams.get('num'), '20');
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test('SerpAPI transport does not echo a rejected credential in its error', async () => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = (async () =>
    new Response('{"error":"Invalid API key: top-secret"}', {
      status: 401,
      headers: { 'content-type': 'application/json' },
    })) as typeof fetch;
  try {
    await assert.rejects(
      async () =>
        discoveryHandlers.serpapi_search(
          { query: 'paper', apiKey: 'top-secret', limit: 10, proxy: null },
          {} as never,
        ),
      (error: unknown) => error instanceof Error && error.message === 'serpapi_search: HTTP 401',
    );
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test('SerpAPI citations transport sends a fixed cites query with pagination', async () => {
  const originalFetch = globalThis.fetch;
  let requested = '';
  globalThis.fetch = (async (input) => {
    requested = String(input);
    return new Response(JSON.stringify({ organic_results: [{ title: 'Citing paper' }] }), { status: 200 });
  }) as typeof fetch;
  try {
    await discoveryHandlers.serpapi_citations(
      { citesId: 'abc_123', apiKey: 'test-secret', limit: 20, start: 40, proxy: null },
      {} as never,
    );
    const url = new URL(requested);
    assert.equal(url.origin + url.pathname, 'https://serpapi.com/search.json');
    assert.equal(url.searchParams.get('cites'), 'abc_123');
    assert.equal(url.searchParams.get('start'), '40');
    assert.equal(url.searchParams.get('api_key'), 'test-secret');
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test('SerpAPI citations transport rejects malformed ids before network access', async () => {
  await assert.rejects(
    async () =>
      discoveryHandlers.serpapi_citations(
        { citesId: 'https://evil.test/', apiKey: 'secret', limit: 20, start: 0, proxy: null },
        {} as never,
      ),
    /valid cites id is required/,
  );
});
