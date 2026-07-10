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
/// permissive CORS headers). No API key, no new Rust code.

const BASE = 'https://api.semanticscholar.org/graph/v1';
const FIELDS = 'title,abstract,year,venue,authors,externalIds,tldr,citationCount,openAccessPdf,url';

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

async function getJson(url: string): Promise<unknown> {
  if (isTauri()) {
    const res = await invoke<RawResponse>('hub_request', {
      req: { method: 'GET', url, headers: { accept: 'application/json' }, body: null },
    });
    if (res.status < 200 || res.status >= 300) {
      throw new Error(res.status === 429 ? 'rate-limited' : `HTTP ${res.status}`);
    }
    return JSON.parse(res.body);
  }
  const res = await fetch(url, { headers: { accept: 'application/json' } });
  if (!res.ok) throw new Error(res.status === 429 ? 'rate-limited' : `HTTP ${res.status}`);
  return res.json();
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
