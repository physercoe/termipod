import { create } from 'zustand';
import type { DiscoveryPaper, DiscoverySourceId } from '../discovery';
import type { DiscoveryQuerySpec } from './discoveryHistoryCore';

/// The Read surface's Discover pane state, lifted out of the component so it
/// survives an unmount — the pane unmounts when the user switches to the Library
/// mode or opens a reader/web tab, and a fresh mount would otherwise drop the
/// last search. Results remain in-memory because they can be large, while the
/// source and PDF-enrichment preference are small device-local preferences.
/// Busy/error/key-prompt state remains transient in the component.

interface DiscoverySearchState {
  query: string;
  results: DiscoveryPaper[];
  authorFilter: string;
  yearFrom: string;
  yearTo: string;
  sort: DiscoverySort;
  sourceId: DiscoverySourceId;
  findPdfs: boolean;
  runRequest: number;
  setQuery: (q: string) => void;
  setResults: (r: DiscoveryPaper[]) => void;
  setAuthorFilter: (author: string) => void;
  setYearFrom: (year: string) => void;
  setYearTo: (year: string) => void;
  setSort: (sort: DiscoverySort) => void;
  setSourceId: (source: DiscoverySourceId) => void;
  setFindPdfs: (find: boolean) => void;
  restoreAndRun: (spec: DiscoveryQuerySpec) => void;
  clearFilters: () => void;
}

export type DiscoverySort = 'relevance' | 'newest' | 'oldest' | 'citations' | 'title';

const SOURCE_LS = 'termipod.discover.source';
const FIND_PDFS_LS = 'termipod.discover.findPdfs';
const SOURCES = new Set<DiscoverySourceId>([
  'openalex',
  'semanticscholar',
  'google-scholar',
  'crossref',
  'arxiv',
  'pubmed',
  'core',
]);

function loadSource(): DiscoverySourceId {
  try {
    const value = localStorage.getItem(SOURCE_LS);
    return value !== null && SOURCES.has(value as DiscoverySourceId) ? (value as DiscoverySourceId) : 'openalex';
  } catch {
    return 'openalex';
  }
}

function loadFindPdfs(): boolean {
  try {
    return localStorage.getItem(FIND_PDFS_LS) !== 'false';
  } catch {
    return true;
  }
}

function persist(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch {
    /* device-local preference; ignore storage denial */
  }
}

export const useDiscoverySearch = create<DiscoverySearchState>((set) => ({
  query: '',
  results: [],
  authorFilter: '',
  yearFrom: '',
  yearTo: '',
  sort: 'relevance',
  sourceId: loadSource(),
  findPdfs: loadFindPdfs(),
  runRequest: 0,
  setQuery: (q) => set({ query: q }),
  setResults: (r) => set({ results: r }),
  setAuthorFilter: (author) => set({ authorFilter: author }),
  setYearFrom: (year) => set({ yearFrom: year }),
  setYearTo: (year) => set({ yearTo: year }),
  setSort: (sort) => set({ sort }),
  setSourceId: (sourceId) => {
    set({ sourceId });
    persist(SOURCE_LS, sourceId);
  },
  setFindPdfs: (findPdfs) => {
    set({ findPdfs });
    persist(FIND_PDFS_LS, String(findPdfs));
  },
  restoreAndRun: (spec) => {
    set((state) => ({
      query: spec.query,
      authorFilter: spec.authorFilter,
      yearFrom: spec.yearFrom,
      yearTo: spec.yearTo,
      sort: spec.sort,
      sourceId: spec.sourceId,
      findPdfs: spec.findPdfs,
      results: [],
      runRequest: state.runRequest + 1,
    }));
    persist(SOURCE_LS, spec.sourceId);
    persist(FIND_PDFS_LS, String(spec.findPdfs));
  },
  clearFilters: () => set({ authorFilter: '', yearFrom: '', yearTo: '' }),
}));
