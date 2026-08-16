/// A normalized paper across every discovery source (Semantic Scholar, OpenAlex,
/// Google Scholar/SerpAPI, Crossref, arXiv, PubMed, CORE). Each source maps its
/// own response into this shape so the Read/Discover UI is source-agnostic.
export interface DiscoveryPaper {
  paperId: string; // source-native id (S2 paperId / DOI / OpenAlex id / arXiv url / PMID) — dedupes imports
  title: string;
  authors: string[];
  year?: number;
  publishedAt?: number; // precise feed timestamp when a provider exposes one
  venue?: string;
  abstract?: string;
  tldr?: string; // Semantic Scholar one-line summary
  citationCount?: number;
  doi?: string;
  arxivId?: string;
  pdfUrl?: string; // open-access PDF link
  url?: string;
  source?: DiscoverySourceId;
  // Google Scholar exposes provider-specific graph/navigation metadata that
  // cannot be represented by the generic citationCount alone. Keep it with the
  // result so an imported paper can offer detailed citing works in the Cite tab.
  scholar?: ScholarResultMetadata;
}

export type DiscoverySourceId =
  | 'openalex'
  | 'semanticscholar'
  | 'google-scholar'
  | 'crossref'
  | 'arxiv'
  | 'pubmed'
  | 'core';

export interface ScholarResultMetadata {
  resultId?: string;
  citedByCount?: number;
  citesId?: string;
  citedByUrl?: string;
  relatedUrl?: string;
  versionsCount?: number;
  versionsUrl?: string;
  cachedUrl?: string;
}

export interface ScholarCitationYear {
  year: number;
  citations: number;
}

export interface ScholarCitationPage {
  papers: DiscoveryPaper[];
  citationsPerYear: ScholarCitationYear[];
  totalResults?: number;
  hasMore: boolean;
}

/// One searchable literature source. `keyKey`/`keyUrl` are set when the source
/// needs a user-supplied API key (stored device-local under `keyKey`).
export interface SearchSource {
  id: DiscoverySourceId;
  label: string;
  note?: string; // short descriptor shown under the picker
  keyKey?: string; // localStorage key holding the user's API key, if required
  keyUrl?: string; // where to get a free key
  keyManagedInVault?: boolean; // fixed keychain slot in Settings → Vault → TermiPod
  search: (query: string, limit: number) => Promise<DiscoveryPaper[]>;
}
