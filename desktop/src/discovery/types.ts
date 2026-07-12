/// A normalized paper across every discovery source (Semantic Scholar, OpenAlex,
/// Crossref, arXiv, PubMed, CORE). Each source maps its own response into this
/// shape so the Read/Discover UI is source-agnostic.
export interface DiscoveryPaper {
  paperId: string; // source-native id (S2 paperId / DOI / OpenAlex id / arXiv url / PMID) — dedupes imports
  title: string;
  authors: string[];
  year?: number;
  venue?: string;
  abstract?: string;
  tldr?: string; // Semantic Scholar one-line summary
  citationCount?: number;
  doi?: string;
  arxivId?: string;
  pdfUrl?: string; // open-access PDF link
  url?: string;
}

/// One searchable literature source. `keyKey`/`keyUrl` are set when the source
/// needs a user-supplied API key (stored device-local under `keyKey`).
export interface SearchSource {
  id: string;
  label: string;
  note?: string; // short descriptor shown under the picker
  keyKey?: string; // localStorage key holding the user's API key, if required
  keyUrl?: string; // where to get a free key
  search: (query: string, limit: number) => Promise<DiscoveryPaper[]>;
}
