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

async function fetchPublicFeed(
  rawUrl: string,
  proxy: string | null,
  headers: Record<string, string> = { Accept: 'application/atom+xml, application/rss+xml, application/xml, text/xml, */*;q=0.2' },
): Promise<{ response: Response; url: string }> {
  let url = rawUrl;
  for (let redirects = 0; redirects <= 5; redirects += 1) {
    url = await publicFeedUrl(url);
    const requestOrigin = new URL(url).origin;
    const response = await proxyFetch(url, {
      method: 'GET',
      headers,
      redirect: 'manual',
      signal: AbortSignal.timeout(TIMEOUT_MS),
    }, proxy);
    if (response.status < 300 || response.status >= 400) return { response, url };
    const location = response.headers.get('location');
    await response.body?.cancel().catch(() => undefined);
    if (location === null) throw new Error('discovery_fetch_feed: redirect has no location');
    const redirected = new URL(location, url);
    if (headers.Authorization !== undefined && redirected.origin !== requestOrigin) {
      throw new Error('discovery_social_fetch: credentialed cross-origin redirect refused');
    }
    url = redirected.toString();
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

export async function fetchPublicJson(
  rawUrl: string,
  proxy: string | null,
  operation: string,
  headers: Record<string, string> = {},
): Promise<unknown> {
  const { response: res } = await fetchPublicFeed(rawUrl, proxy, {
    Accept: 'application/json',
    ...headers,
  });
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

function mastodonTarget(raw: string, kind: 'author' | 'tag'): { origin: string; value: string } {
  const value = raw.trim();
  try {
    const url = new URL(value);
    if (url.protocol !== 'https:' && url.protocol !== 'http:') throw new Error('protocol');
    const parts = url.pathname.split('/').filter(Boolean);
    if (kind === 'author') {
      const handle = parts.find((part) => part.startsWith('@'))?.slice(1);
      if (handle !== undefined && handle !== '') return { origin: url.origin, value: handle };
    } else {
      const tagIndex = parts.findIndex((part) => part === 'tags');
      const tag = tagIndex >= 0 ? parts[tagIndex + 1] : undefined;
      if (tag !== undefined && tag !== '') return { origin: url.origin, value: tag.replace(/^#/, '') };
    }
  } catch {
    // Fall through to the compact user@instance / tag@instance syntax.
  }
  const compact = value.replace(/^[@#]/, '');
  const split = compact.lastIndexOf('@');
  if (split <= 0 || split === compact.length - 1) {
    throw new Error(`discovery_social_fetch: Mastodon ${kind} must include its instance`);
  }
  return { origin: `https://${compact.slice(split + 1)}`, value: compact.slice(0, split) };
}

export function socialUrl(provider: string, value: string, language: string, excludeReposts: boolean): URL | null {
  if (provider === 'bluesky-author') {
    const url = new URL('https://public.api.bsky.app/xrpc/app.bsky.feed.getAuthorFeed');
    url.searchParams.set('actor', value.replace(/^@/, ''));
    url.searchParams.set('filter', excludeReposts ? 'posts_no_replies' : 'posts_with_replies');
    url.searchParams.set('limit', '50');
    return url;
  }
  if (provider === 'bluesky-feed') {
    const url = new URL('https://public.api.bsky.app/xrpc/app.bsky.feed.getFeed');
    url.searchParams.set('feed', value);
    url.searchParams.set('limit', '50');
    return url;
  }
  if (provider === 'bluesky-query') {
    const url = new URL('https://public.api.bsky.app/xrpc/app.bsky.feed.searchPosts');
    url.searchParams.set('q', value);
    if (language !== '') url.searchParams.set('lang', language);
    url.searchParams.set('limit', '50');
    return url;
  }
  if (provider === 'x-author' || provider === 'x-query') {
    const url = new URL('https://api.x.com/2/tweets/search/recent');
    const base = provider === 'x-author' ? `from:${value.replace(/^@/, '')}` : value;
    const parts = [base];
    if (excludeReposts) parts.push('-is:retweet');
    if (language !== '') parts.push(`lang:${language}`);
    url.searchParams.set('query', parts.join(' '));
    url.searchParams.set('max_results', '50');
    url.searchParams.set('expansions', 'author_id');
    url.searchParams.set('tweet.fields', 'author_id,created_at,lang,public_metrics,entities,referenced_tweets');
    url.searchParams.set('user.fields', 'id,name,username,profile_image_url');
    return url;
  }
  return null;
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
  discovery_social_fetch: async (args): Promise<unknown> => {
    const provider = typeof args.provider === 'string' ? args.provider : '';
    const value = typeof args.value === 'string' ? args.value.trim() : '';
    const language = typeof args.language === 'string' ? args.language.trim().toLocaleLowerCase() : '';
    const excludeReposts = args.excludeReposts !== false;
    const proxy = typeof args.proxy === 'string' && args.proxy !== '' ? args.proxy : null;
    const apiKey = typeof args.apiKey === 'string' ? args.apiKey.trim() : '';
    if (value === '') throw new Error('discovery_social_fetch: target is required');

    const url = socialUrl(provider, value, language, excludeReposts);
    if (url !== null) {
      const headers: Record<string, string> = {};
      if (provider.startsWith('x-')) {
        if (apiKey === '') throw new Error('discovery_social_fetch: X bearer token is required');
        headers.Authorization = `Bearer ${apiKey}`;
      }
      return fetchPublicJson(url.toString(), proxy, 'discovery_social_fetch', headers);
    }

    if (provider === 'mastodon-author') {
      const target = mastodonTarget(value, 'author');
      const lookupUrl = new URL('/api/v1/accounts/lookup', target.origin);
      lookupUrl.searchParams.set('acct', target.value);
      const account = await fetchPublicJson(lookupUrl.toString(), proxy, 'discovery_social_fetch') as Record<string, unknown>;
      const id = typeof account.id === 'string' ? account.id : '';
      if (id === '') throw new Error('discovery_social_fetch: Mastodon account not found');
      const statusesUrl = new URL(`/api/v1/accounts/${encodeURIComponent(id)}/statuses`, target.origin);
      statusesUrl.searchParams.set('limit', '40');
      statusesUrl.searchParams.set('exclude_reblogs', excludeReposts ? 'true' : 'false');
      statusesUrl.searchParams.set('exclude_replies', 'true');
      const statuses = await fetchPublicJson(statusesUrl.toString(), proxy, 'discovery_social_fetch');
      return { account, statuses };
    }
    if (provider === 'mastodon-tag') {
      const target = mastodonTarget(value, 'tag');
      const timelineUrl = new URL(`/api/v1/timelines/tag/${encodeURIComponent(target.value)}`, target.origin);
      timelineUrl.searchParams.set('limit', '40');
      const statuses = await fetchPublicJson(timelineUrl.toString(), proxy, 'discovery_social_fetch');
      return { tag: target.value, statuses };
    }
    throw new Error('discovery_social_fetch: unsupported provider');
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
