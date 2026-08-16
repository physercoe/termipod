import assert from 'node:assert/strict';
import { test } from 'node:test';
import { discoveryHandlers, fetchPublicJson, socialUrl } from './discovery.ts';

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

test('RSS transport accepts a public HTTP feed and returns bounded text', async () => {
  const originalFetch = globalThis.fetch;
  let requested = '';
  globalThis.fetch = (async (input) => {
    requested = String(input);
    return new Response('<rss><channel><title>Research</title></channel></rss>', { status: 200 });
  }) as typeof fetch;
  try {
    const out = await discoveryHandlers.discovery_fetch_feed(
      { url: 'https://93.184.216.34/research.xml', proxy: null },
      {} as never,
    );
    assert.equal(requested, 'https://93.184.216.34/research.xml');
    assert.deepEqual(out, {
      url: 'https://93.184.216.34/research.xml',
      text: '<rss><channel><title>Research</title></channel></rss>',
    });
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test('RSS transport refuses loopback and private-network URLs before fetch', async () => {
  let fetched = false;
  const originalFetch = globalThis.fetch;
  globalThis.fetch = (async () => {
    fetched = true;
    return new Response('unexpected');
  }) as typeof fetch;
  try {
    for (const url of ['http://127.0.0.1/feed', 'http://10.0.0.2/rss', 'file:///tmp/feed.xml']) {
      await assert.rejects(
        async () => discoveryHandlers.discovery_fetch_feed({ url, proxy: null }, {} as never),
        /not allowed|only HTTP/,
      );
    }
    assert.equal(fetched, false);
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test('RSS transport revalidates redirects so a public feed cannot bounce into loopback', async () => {
  const originalFetch = globalThis.fetch;
  let requests = 0;
  globalThis.fetch = (async () => {
    requests += 1;
    return new Response(null, { status: 302, headers: { location: 'http://127.0.0.1/private-feed' } });
  }) as typeof fetch;
  try {
    await assert.rejects(
      async () => discoveryHandlers.discovery_fetch_feed({ url: 'https://93.184.216.34/feed', proxy: null }, {} as never),
      /private hosts are not allowed/,
    );
    assert.equal(requests, 1);
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test('social connector URLs keep X credentials out of the query and apply monitor filters', () => {
  const x = socialUrl('x-author', '@researcher', 'en', true);
  assert.ok(x !== null);
  assert.equal(x.origin + x.pathname, 'https://api.x.com/2/tweets/search/recent');
  assert.equal(x.searchParams.get('query'), 'from:researcher -is:retweet lang:en');
  assert.equal(x.searchParams.has('api_key'), false);

  const bluesky = socialUrl('bluesky-query', 'agent memory', 'zh', true);
  assert.ok(bluesky !== null);
  assert.equal(bluesky.searchParams.get('q'), 'agent memory');
  assert.equal(bluesky.searchParams.get('lang'), 'zh');
});

test('credentialed social fetch refuses a cross-origin redirect before leaking the bearer token', async () => {
  const originalFetch = globalThis.fetch;
  let requests = 0;
  globalThis.fetch = (async () => {
    requests += 1;
    return new Response(null, { status: 302, headers: { location: 'https://198.51.100.1/steal' } });
  }) as typeof fetch;
  try {
    await assert.rejects(
      async () => fetchPublicJson('https://93.184.216.34/posts', null, 'social', { Authorization: 'Bearer secret' }),
      /cross-origin redirect refused/,
    );
    assert.equal(requests, 1);
  } finally {
    globalThis.fetch = originalFetch;
  }
});
