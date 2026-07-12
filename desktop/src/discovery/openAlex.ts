import { CONTACT, getJson } from './http';
import type { DiscoveryPaper } from './types';

/// OpenAlex — 250M+ works, fully open, no key. The most generous free source
/// (~100k calls/day vs Semantic Scholar's tiny shared bucket), so it's the
/// default. Abstracts arrive as an inverted index and are reconstructed here.

function abstractFromInverted(inv: unknown): string | undefined {
  if (inv === null || typeof inv !== 'object') return undefined;
  const words: string[] = [];
  for (const [word, positions] of Object.entries(inv as Record<string, number[]>)) {
    if (Array.isArray(positions)) for (const p of positions) words[p] = word;
  }
  const s = words.join(' ').replace(/\s+/g, ' ').trim();
  return s === '' ? undefined : s;
}

function stripDoi(d: unknown): string | undefined {
  if (typeof d !== 'string' || d === '') return undefined;
  return d.replace(/^https?:\/\/(dx\.)?doi\.org\//i, '');
}

function mapWork(w: Record<string, unknown>): DiscoveryPaper {
  const loc = (w.primary_location ?? {}) as Record<string, unknown>;
  const source = (loc.source ?? {}) as Record<string, unknown>;
  const oa = (w.open_access ?? {}) as Record<string, unknown>;
  const ids = (w.ids ?? {}) as Record<string, unknown>;
  const authorships = Array.isArray(w.authorships) ? w.authorships : [];
  const authors = authorships
    .map((a) => ((a as Record<string, unknown>).author as Record<string, unknown> | undefined)?.display_name)
    .filter((n): n is string => typeof n === 'string');
  const pdf =
    (typeof oa.oa_url === 'string' ? oa.oa_url : undefined) ??
    (typeof loc.pdf_url === 'string' ? loc.pdf_url : undefined);
  return {
    paperId: typeof w.id === 'string' ? w.id : '',
    title: typeof w.title === 'string' ? w.title : typeof w.display_name === 'string' ? w.display_name : '',
    authors,
    year: typeof w.publication_year === 'number' ? w.publication_year : undefined,
    venue: typeof source.display_name === 'string' ? source.display_name : undefined,
    abstract: abstractFromInverted(w.abstract_inverted_index),
    citationCount: typeof w.cited_by_count === 'number' ? w.cited_by_count : undefined,
    doi: stripDoi(w.doi),
    arxivId: typeof ids.arxiv === 'string' ? ids.arxiv.replace(/^.*abs\//, '') : undefined,
    pdfUrl: pdf,
    url: typeof w.id === 'string' ? w.id : undefined,
  };
}

export async function searchOpenAlex(query: string, limit: number): Promise<DiscoveryPaper[]> {
  const url = `https://api.openalex.org/works?search=${encodeURIComponent(query)}&per-page=${limit}&mailto=${CONTACT}`;
  const j = (await getJson(url)) as Record<string, unknown>;
  const results = Array.isArray(j.results) ? j.results : [];
  return results.map((w) => mapWork(w as Record<string, unknown>)).filter((p) => p.title !== '');
}
