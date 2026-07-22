import { create } from 'zustand';
import type { DiscoveryPaper } from '../discovery';

/// The Read surface's Discover pane state, lifted out of the component so it
/// survives an unmount — the pane unmounts when the user switches to the Library
/// mode or opens a reader/web tab, and a fresh mount would otherwise drop the
/// last search. In-memory only (results can be large; this is session state, not
/// something to persist to disk): the query + its results come back exactly as
/// left when the pane remounts. `sourceId` keeps its own localStorage
/// persistence in the component; the transient bits (busy/error/showKey) reset.

interface DiscoverySearchState {
  query: string;
  results: DiscoveryPaper[];
  setQuery: (q: string) => void;
  setResults: (r: DiscoveryPaper[]) => void;
}

export const useDiscoverySearch = create<DiscoverySearchState>((set) => ({
  query: '',
  results: [],
  setQuery: (q) => set({ query: q }),
  setResults: (r) => set({ results: r }),
}));
