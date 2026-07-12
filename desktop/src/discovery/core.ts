import { getJson, lsGet } from './http';
import type { DiscoveryPaper } from './types';

/// CORE — 300M+ open-access papers with full text. Requires a free API key
/// (register at core.ac.uk); `search` throws `needs-key` when none is set so the
/// UI can prompt for one.

export const CORE_KEY = 'termipod.core.apiKey';

function mapWork(w: Record<string, unknown>): DiscoveryPaper {
  const authors = (Array.isArray(w.authors) ? w.authors : [])
    .map((a) => (typeof a === 'string' ? a : (a as Record<string, unknown>).name))
    .filter((n): n is string => typeof n === 'string');
  return {
    paperId: typeof w.id === 'string' || typeof w.id === 'number' ? String(w.id) : typeof w.doi === 'string' ? w.doi : '',
    title: typeof w.title === 'string' ? w.title : '',
    authors,
    year: typeof w.yearPublished === 'number' ? w.yearPublished : undefined,
    venue: typeof w.publisher === 'string' ? w.publisher : undefined,
    abstract: typeof w.abstract === 'string' ? w.abstract : undefined,
    doi: typeof w.doi === 'string' ? w.doi : undefined,
    arxivId: typeof w.arxivId === 'string' ? w.arxivId : undefined,
    pdfUrl: typeof w.downloadUrl === 'string' ? w.downloadUrl : undefined,
    url: typeof w.doi === 'string' ? `https://doi.org/${w.doi}` : undefined,
  };
}

export async function searchCore(query: string, limit: number): Promise<DiscoveryPaper[]> {
  const key = lsGet(CORE_KEY);
  if (key === '') throw new Error('needs-key');
  const url = `https://api.core.ac.uk/v3/search/works?q=${encodeURIComponent(query)}&limit=${limit}`;
  const j = (await getJson(url, { authorization: `Bearer ${key}` }, 2)) as Record<string, unknown>;
  const results = Array.isArray(j.results) ? j.results : [];
  return results.map((w) => mapWork(w as Record<string, unknown>)).filter((p) => p.title !== '');
}
