import { invoke } from '@tauri-apps/api/core';
import { isTauri } from '../platform';

/// Literature discovery via the **Semantic Scholar Graph API** — the open
/// discovery backbone the landscape doc (`research-tooling-landscape.md` §3.1)
/// says to INTEGRATE. Keyless/free at our scale; returns the fields that make
/// Semantic Scholar's reader compelling: the **TLDR** one-line summary, abstract,
/// citation count, DOI/arXiv ids, and an open-access PDF link.
///
/// The call is routed through the Rust core's `hub_request` command (reqwest) so
/// it is CORS-free and works inside the sandboxed webview, exactly as the hub SDK
/// transport does; the plain-browser build falls back to `fetch` (the API sends
/// permissive CORS headers).
///
/// **Rate limits.** The keyless `/paper/search` endpoint shares ONE small global
/// bucket across all anonymous users, so it returns 429 (`TooManyRequests`) most
/// of the time — hence the constant "rate-limited" the director saw. Two
/// mitigations: (1) automatic **retry with backoff** on 429 (a single request
/// succeeds often enough that a few retries usually land); (2) an optional, free
/// **API key** (`x-api-key`) the user pastes in — a keyed request gets a dedicated
/// quota, so it retries less and rarely 429s. The key is the user's own (stored
/// device-local); we don't ship a shared one (it would be abused/leaked).

const BASE = 'https://api.semanticscholar.org/graph/v1';
const FIELDS = 'title,abstract,year,venue,authors,externalIds,tldr,citationCount,openAccessPdf,url';
const KEY_LS = 'termipod.s2.apiKey';

export function getApiKey(): string {
  try {
    return localStorage.getItem(KEY_LS) ?? '';
  } catch {
    return '';
  }
}
export function setApiKey(key: string): void {
  try {
    if (key.trim() === '') localStorage.removeItem(KEY_LS);
    else localStorage.setItem(KEY_LS, key.trim());
  } catch {
    /* ignore */
  }
}

function delay(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

export interface DiscoveryPaper {
  paperId: string;
  title: string;
  authors: string[];
  year?: number;
  venue?: string;
  abstract?: string;
  tldr?: string;
  citationCount?: number;
  doi?: string;
  arxivId?: string;
  pdfUrl?: string;
  url?: string;
}

interface RawResponse {
  status: number;
  body: string;
}

async function requestOnce(url: string, headers: Record<string, string>): Promise<RawResponse> {
  if (isTauri()) {
    return invoke<RawResponse>('hub_request', { req: { method: 'GET', url, headers, body: null } });
  }
  const res = await fetch(url, { headers });
  return { status: res.status, body: await res.text() };
}

async function getJson(url: string): Promise<unknown> {
  const headers: Record<string, string> = { accept: 'application/json' };
  const key = getApiKey();
  if (key !== '') headers['x-api-key'] = key;
  // Keyless calls share a saturated global bucket → retry the 429s with backoff;
  // a keyed call has its own quota so it needs far fewer attempts.
  const maxAttempts = key !== '' ? 2 : 5;
  let lastStatus = 0;
  for (let attempt = 0; attempt < maxAttempts; attempt += 1) {
    if (attempt > 0) await delay(500 * attempt + 300); // 800ms, 1.3s, 1.8s, 2.3s
    const res = await requestOnce(url, headers);
    if (res.status >= 200 && res.status < 300) return JSON.parse(res.body);
    lastStatus = res.status;
    if (res.status !== 429) break; // only the shared-pool 429 is worth retrying
  }
  throw new Error(lastStatus === 429 ? 'rate-limited' : `HTTP ${lastStatus}`);
}

function normalize(raw: unknown): DiscoveryPaper | null {
  if (raw === null || typeof raw !== 'object') return null;
  const o = raw as Record<string, unknown>;
  const title = typeof o.title === 'string' ? o.title : '';
  if (title === '') return null;
  const authors = Array.isArray(o.authors)
    ? o.authors
        .map((a) => (a !== null && typeof a === 'object' ? (a as Record<string, unknown>).name : undefined))
        .filter((n): n is string => typeof n === 'string')
    : [];
  const ext = (o.externalIds ?? {}) as Record<string, unknown>;
  const tldrObj = o.tldr as Record<string, unknown> | null | undefined;
  const oa = o.openAccessPdf as Record<string, unknown> | null | undefined;
  return {
    paperId: typeof o.paperId === 'string' ? o.paperId : '',
    title,
    authors,
    year: typeof o.year === 'number' ? o.year : undefined,
    venue: typeof o.venue === 'string' && o.venue !== '' ? o.venue : undefined,
    abstract: typeof o.abstract === 'string' ? o.abstract : undefined,
    tldr: tldrObj !== null && typeof tldrObj === 'object' && typeof tldrObj.text === 'string' ? tldrObj.text : undefined,
    citationCount: typeof o.citationCount === 'number' ? o.citationCount : undefined,
    doi: typeof ext.DOI === 'string' ? ext.DOI : undefined,
    arxivId: typeof ext.ArXiv === 'string' ? ext.ArXiv : undefined,
    pdfUrl: oa !== null && typeof oa === 'object' && typeof oa.url === 'string' ? oa.url : undefined,
    url: typeof o.url === 'string' ? o.url : undefined,
  };
}

/** Search papers by free-text query. Returns up to `limit` normalized results. */
export async function searchPapers(query: string, limit = 20): Promise<DiscoveryPaper[]> {
  const q = query.trim();
  if (q === '') return [];
  const url = `${BASE}/paper/search?query=${encodeURIComponent(q)}&limit=${limit}&fields=${FIELDS}`;
  const json = await getJson(url);
  const data = json !== null && typeof json === 'object' ? (json as Record<string, unknown>).data : undefined;
  if (!Array.isArray(data)) return [];
  return data.map(normalize).filter((p): p is DiscoveryPaper => p !== null);
}
