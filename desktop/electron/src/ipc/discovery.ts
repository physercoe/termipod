/// Main-process transport for keyed discovery providers. SerpAPI deliberately
/// rejects browser-origin requests (CORS), so the renderer cannot call it like
/// the public OpenAlex/Crossref APIs. Keep the endpoint fixed here, enforce a
/// response cap/timeout, and use the app's proxy-aware Node transport.
import { serpApiCitationsUrl, serpApiSearchUrl } from '../../../src/discovery/serpApiCore.ts';
import type { Handler } from './dispatch';
import { proxyFetch } from './net.ts';

const RESPONSE_CAP = 5 * 1024 * 1024;
const TIMEOUT_MS = 45_000;

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
