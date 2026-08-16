/// Main-process transport for keyed discovery providers. SerpAPI deliberately
/// rejects browser-origin requests (CORS), so the renderer cannot call it like
/// the public OpenAlex/Crossref APIs. Keep the endpoint fixed here, enforce a
/// response cap/timeout, and use the app's proxy-aware Node transport.
import { serpApiCitationsUrl, serpApiSearchUrl } from '../../../src/discovery/serpApiCore.ts';
import type { Handler } from './dispatch';
import { proxyFetch } from './net.ts';
import { isIP } from 'node:net';
import { lookup } from 'node:dns/promises';

const RESPONSE_CAP = 5 * 1024 * 1024;
const TIMEOUT_MS = 45_000;
const FEED_RESPONSE_CAP = 3 * 1024 * 1024;

function privateAddress(address: string): boolean {
  const normalized = address.toLocaleLowerCase();
  if (normalized === '::1' || normalized === '::' || normalized.startsWith('fe80:') || normalized.startsWith('fc') || normalized.startsWith('fd')) return true;
  const parts = normalized.split('.').map(Number);
  if (parts.length !== 4 || parts.some((part) => !Number.isInteger(part))) return false;
  return parts[0] === 10 ||
    parts[0] === 127 ||
    parts[0] === 0 ||
    (parts[0] === 169 && parts[1] === 254) ||
    (parts[0] === 172 && (parts[1] ?? 0) >= 16 && (parts[1] ?? 0) <= 31) ||
    (parts[0] === 192 && parts[1] === 168) ||
    (parts[0] === 100 && (parts[1] ?? 0) >= 64 && (parts[1] ?? 0) <= 127) ||
    parts[0] >= 224;
}

async function publicFeedUrl(raw: string): Promise<string> {
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    throw new Error('discovery_fetch_feed: valid URL is required');
  }
  if (url.protocol !== 'https:' && url.protocol !== 'http:') throw new Error('discovery_fetch_feed: only HTTP(S) is allowed');
  const hostname = url.hostname.toLocaleLowerCase();
  if (hostname === 'localhost' || hostname.endsWith('.localhost') || hostname.endsWith('.local')) {
    throw new Error('discovery_fetch_feed: private hosts are not allowed');
  }
  if (isIP(hostname) !== 0) {
    if (privateAddress(hostname)) throw new Error('discovery_fetch_feed: private hosts are not allowed');
  } else {
    const addresses = await lookup(hostname, { all: true });
    if (addresses.length === 0 || addresses.some(({ address }) => privateAddress(address))) {
      throw new Error('discovery_fetch_feed: private hosts are not allowed');
    }
  }
  url.hash = '';
  return url.toString();
}

async function fetchPublicFeed(rawUrl: string, proxy: string | null): Promise<{ response: Response; url: string }> {
  let url = rawUrl;
  for (let redirects = 0; redirects <= 5; redirects += 1) {
    url = await publicFeedUrl(url);
    const response = await proxyFetch(url, {
      method: 'GET',
      headers: { Accept: 'application/atom+xml, application/rss+xml, application/xml, text/xml, */*;q=0.2' },
      redirect: 'manual',
      signal: AbortSignal.timeout(TIMEOUT_MS),
    }, proxy);
    if (response.status < 300 || response.status >= 400) return { response, url };
    const location = response.headers.get('location');
    await response.body?.cancel().catch(() => undefined);
    if (location === null) throw new Error('discovery_fetch_feed: redirect has no location');
    url = new URL(location, url).toString();
  }
  throw new Error('discovery_fetch_feed: too many redirects');
}

async function fetchSerpApiJson(url: string, proxy: string | null, operation: string): Promise<unknown> {
  const res = await proxyFetch(
    url,
    {
      method: 'GET',
      headers: { Accept: 'application/json' },
      redirect: 'follow',
      signal: AbortSignal.timeout(TIMEOUT_MS),
    },
    proxy,
  );
  const declared = Number(res.headers.get('content-length') ?? '0');
  if (Number.isFinite(declared) && declared > RESPONSE_CAP) {
    await res.body?.cancel().catch(() => undefined);
    throw new Error(`${operation}: response exceeds 5 MB`);
  }
  const bytes = new Uint8Array(await res.arrayBuffer());
  if (bytes.byteLength > RESPONSE_CAP) throw new Error(`${operation}: response exceeds 5 MB`);
  if (!res.ok) throw new Error(`${operation}: HTTP ${res.status}`);
  try {
    return JSON.parse(new TextDecoder().decode(bytes)) as unknown;
  } catch {
    throw new Error(`${operation}: invalid JSON response`);
  }
}

export const discoveryHandlers: Record<string, Handler> = {
  discovery_fetch_feed: async (args): Promise<unknown> => {
    const rawUrl = typeof args.url === 'string' ? args.url.trim() : '';
    const proxy = typeof args.proxy === 'string' && args.proxy !== '' ? args.proxy : null;
    const { response: res, url } = await fetchPublicFeed(rawUrl, proxy);
    const declared = Number(res.headers.get('content-length') ?? '0');
    if (Number.isFinite(declared) && declared > FEED_RESPONSE_CAP) {
      await res.body?.cancel().catch(() => undefined);
      throw new Error('discovery_fetch_feed: response exceeds 3 MB');
    }
    const bytes = new Uint8Array(await res.arrayBuffer());
    if (bytes.byteLength > FEED_RESPONSE_CAP) throw new Error('discovery_fetch_feed: response exceeds 3 MB');
    if (!res.ok) throw new Error(`discovery_fetch_feed: HTTP ${res.status}`);
    return { url: res.url || url, text: new TextDecoder().decode(bytes) };
  },
  serpapi_search: async (args): Promise<unknown> => {
    const query = typeof args.query === 'string' ? args.query.trim() : '';
    const apiKey = typeof args.apiKey === 'string' ? args.apiKey.trim() : '';
    const limit = typeof args.limit === 'number' ? args.limit : 20;
    const proxy = typeof args.proxy === 'string' && args.proxy !== '' ? args.proxy : null;
    if (query === '') throw new Error('serpapi_search: query is required');
    if (apiKey === '') throw new Error('serpapi_search: API key is required');

    return fetchSerpApiJson(serpApiSearchUrl(query, limit, apiKey), proxy, 'serpapi_search');
  },
  serpapi_citations: async (args): Promise<unknown> => {
    const citesId = typeof args.citesId === 'string' ? args.citesId.trim() : '';
    const apiKey = typeof args.apiKey === 'string' ? args.apiKey.trim() : '';
    const limit = typeof args.limit === 'number' ? args.limit : 20;
    const start = typeof args.start === 'number' ? args.start : 0;
    const proxy = typeof args.proxy === 'string' && args.proxy !== '' ? args.proxy : null;
    if (citesId === '' || !/^[\w-]+$/.test(citesId)) throw new Error('serpapi_citations: valid cites id is required');
    if (apiKey === '') throw new Error('serpapi_citations: API key is required');
    return fetchSerpApiJson(
      serpApiCitationsUrl(citesId, limit, start, apiKey),
      proxy,
      'serpapi_citations',
    );
  },
};
