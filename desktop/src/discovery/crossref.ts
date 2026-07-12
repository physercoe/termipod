import { CONTACT, getJson } from './http';
import type { DiscoveryPaper } from './types';

/// Crossref — 150M+ DOI-registered records; the metadata backbone. No key
/// (mailto → polite pool). Abstracts, when present, are JATS XML, stripped here.

function first(v: unknown): string | undefined {
  if (Array.isArray(v) && typeof v[0] === 'string') return v[0];
  return typeof v === 'string' ? v : undefined;
}

function stripJats(s: unknown): string | undefined {
  if (typeof s !== 'string' || s === '') return undefined;
  const t = s
    .replace(/<[^>]+>/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();
  return t === '' ? undefined : t;
}

function yearOf(it: Record<string, unknown>): number | undefined {
  for (const k of ['issued', 'published', 'published-print', 'published-online']) {
    const dp = (it[k] as Record<string, unknown> | undefined)?.['date-parts'];
    if (Array.isArray(dp) && Array.isArray(dp[0]) && typeof dp[0][0] === 'number') return dp[0][0];
  }
  return undefined;
}

function mapItem(it: Record<string, unknown>): DiscoveryPaper {
  const authors = (Array.isArray(it.author) ? it.author : [])
    .map((a) => {
      const o = a as Record<string, unknown>;
      const name = [o.given, o.family].filter((x) => typeof x === 'string').join(' ').trim();
      return name !== '' ? name : typeof o.name === 'string' ? o.name : '';
    })
    .filter((n) => n !== '');
  return {
    paperId: typeof it.DOI === 'string' ? it.DOI : '',
    title: first(it.title) ?? '',
    authors,
    year: yearOf(it),
    venue: first(it['container-title']),
    abstract: stripJats(it.abstract),
    citationCount: typeof it['is-referenced-by-count'] === 'number' ? it['is-referenced-by-count'] : undefined,
    doi: typeof it.DOI === 'string' ? it.DOI : undefined,
    url: typeof it.URL === 'string' ? it.URL : undefined,
  };
}

export async function searchCrossref(query: string, limit: number): Promise<DiscoveryPaper[]> {
  const select = 'DOI,title,author,issued,published,container-title,URL,is-referenced-by-count,abstract';
  const url = `https://api.crossref.org/works?query=${encodeURIComponent(query)}&rows=${limit}&select=${select}&mailto=${CONTACT}`;
  const j = (await getJson(url)) as Record<string, unknown>;
  const items = ((j.message as Record<string, unknown> | undefined)?.items ?? []) as unknown[];
  return items.map((it) => mapItem(it as Record<string, unknown>)).filter((p) => p.title !== '');
}
