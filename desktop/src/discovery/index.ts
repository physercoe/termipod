import { searchArxiv } from './arxiv';
import { CORE_KEY, searchCore } from './core';
import { searchCrossref } from './crossref';
import { searchOpenAlex } from './openAlex';
import { searchPubmed } from './pubmed';
import { S2_KEY, searchSemanticScholar } from './semanticScholar';
import type { SearchSource } from './types';

export type { DiscoveryPaper, SearchSource } from './types';
export { lsGet, lsSet } from './http';
export { enrichWithUnpaywall } from './unpaywall';

/// The discovery source registry — the single source of truth the Read/Discover
/// picker renders. OpenAlex is first (free, keyless, most generous → the default);
/// Unpaywall isn't here because it's an enrichment (DOI → PDF), not a search.
export const SOURCES: SearchSource[] = [
  { id: 'openalex', label: 'OpenAlex', note: '250M+ · free, no key', search: searchOpenAlex },
  {
    id: 'semanticscholar',
    label: 'Semantic Scholar',
    note: 'TLDR summaries · CS/AI',
    keyKey: S2_KEY,
    keyUrl: 'https://www.semanticscholar.org/product/api#api-key',
    search: searchSemanticScholar,
  },
  { id: 'crossref', label: 'Crossref', note: 'DOI metadata · 150M+', search: searchCrossref },
  { id: 'arxiv', label: 'arXiv', note: 'preprints · CS/physics/math', search: searchArxiv },
  { id: 'pubmed', label: 'PubMed', note: 'biomedical / life sci', search: searchPubmed },
  {
    id: 'core',
    label: 'CORE',
    note: 'open-access full text · key',
    keyKey: CORE_KEY,
    keyUrl: 'https://core.ac.uk/services/api',
    search: searchCore,
  },
];

export function sourceById(id: string): SearchSource {
  return SOURCES.find((s) => s.id === id) ?? SOURCES[0];
}
