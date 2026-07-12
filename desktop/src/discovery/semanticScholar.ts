import { getJson, lsGet } from './http';
import type { DiscoveryPaper } from './types';

/// Semantic Scholar — 200M+ papers with the distinctive **TLDR** one-line
/// summaries and a citation graph; strong CS/AI coverage. The keyless
/// `/paper/search` endpoint shares one small global bucket (429s constantly), so
/// the shared http layer retries; an optional user key (`x-api-key`, stored under
/// `S2_KEY`) grants a dedicated quota.

export const S2_KEY = 'termipod.s2.apiKey';

const BASE = 'https://api.semanticscholar.org/graph/v1';
const FIELDS = 'title,abstract,year,venue,authors,externalIds,tldr,citationCount,openAccessPdf,url';

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

export async function searchSemanticScholar(query: string, limit: number): Promise<DiscoveryPaper[]> {
  const key = lsGet(S2_KEY);
  const headers: Record<string, string> = key !== '' ? { 'x-api-key': key } : {};
  const attempts = key !== '' ? 2 : 5; // keyless shares a saturated bucket → retry more
  const url = `${BASE}/paper/search?query=${encodeURIComponent(query)}&limit=${limit}&fields=${FIELDS}`;
  const json = (await getJson(url, headers, attempts)) as Record<string, unknown>;
  const data = json.data;
  if (!Array.isArray(data)) return [];
  return data.map(normalize).filter((p): p is DiscoveryPaper => p !== null);
}
