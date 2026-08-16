import { create } from 'zustand';
import type { DiscoverySourceId } from '../discovery/types';
import type { DiscoverySort } from './discoverySearch';
import type { DiscoveryCadence } from './discoveryMonitorCore';
import {
  discoveryQueryKey,
  upsertRecentSearch,
  upsertSavedSearch,
  type DiscoveryQuerySpec,
  type RecentDiscoverySearch,
  type SavedDiscoverySearch,
} from './discoveryHistoryCore';

const LS_KEY = 'termipod.discover.history.v1';

interface PersistedDiscoveryHistory {
  version: 1;
  recent: RecentDiscoverySearch[];
  saved: SavedDiscoverySearch[];
}

interface DiscoveryHistoryState {
  recent: RecentDiscoverySearch[];
  saved: SavedDiscoverySearch[];
  record: (spec: DiscoveryQuerySpec, resultCount: number) => void;
  save: (spec: DiscoveryQuerySpec, name?: string) => string;
  removeRecent: (id: string) => void;
  removeSaved: (id: string) => void;
  setSavedSchedule: (id: string, cadence: DiscoveryCadence | undefined) => void;
  clearRecent: () => void;
}

const SOURCES = new Set<DiscoverySourceId>([
  'openalex',
  'semanticscholar',
  'google-scholar',
  'crossref',
  'arxiv',
  'pubmed',
  'core',
]);
const SORTS = new Set<DiscoverySort>(['relevance', 'newest', 'oldest', 'citations', 'title']);
const CADENCES = new Set<DiscoveryCadence>(['daily', 'weekly', 'monthly']);

function isSpec(value: unknown): value is DiscoveryQuerySpec {
  if (value === null || typeof value !== 'object') return false;
  const row = value as Record<string, unknown>;
  return (
    typeof row.query === 'string' &&
    typeof row.sourceId === 'string' &&
    SOURCES.has(row.sourceId as DiscoverySourceId) &&
    typeof row.authorFilter === 'string' &&
    typeof row.yearFrom === 'string' &&
    typeof row.yearTo === 'string' &&
    typeof row.sort === 'string' &&
    SORTS.has(row.sort as DiscoverySort) &&
    typeof row.findPdfs === 'boolean'
  );
}

function load(): Pick<DiscoveryHistoryState, 'recent' | 'saved'> {
  try {
    const parsed = JSON.parse(localStorage.getItem(LS_KEY) ?? '') as Partial<PersistedDiscoveryHistory>;
    const recent = Array.isArray(parsed.recent)
      ? parsed.recent.filter(
          (entry): entry is RecentDiscoverySearch =>
            isSpec(entry) && typeof entry.id === 'string' && typeof entry.ranAt === 'number' && typeof entry.resultCount === 'number',
        )
      : [];
    const saved = Array.isArray(parsed.saved)
      ? parsed.saved.filter(
          (entry): entry is SavedDiscoverySearch =>
            isSpec(entry) &&
            typeof entry.id === 'string' &&
            typeof entry.name === 'string' &&
            typeof entry.savedAt === 'number' &&
            (entry.schedule === undefined ||
              (typeof entry.schedule === 'string' && CADENCES.has(entry.schedule as DiscoveryCadence))),
        )
      : [];
    return { recent: recent.slice(0, 20), saved };
  } catch {
    return { recent: [], saved: [] };
  }
}

function persist(recent: RecentDiscoverySearch[], saved: SavedDiscoverySearch[]): void {
  try {
    const payload: PersistedDiscoveryHistory = { version: 1, recent, saved };
    localStorage.setItem(LS_KEY, JSON.stringify(payload));
  } catch (error) {
    console.error(`[discovery] failed to persist "${LS_KEY}"`, error);
  }
}

function id(prefix: string): string {
  return `${prefix}${crypto.randomUUID()}`;
}

export { discoveryQueryKey };
export type { DiscoveryQuerySpec, RecentDiscoverySearch, SavedDiscoverySearch };

export const useDiscoveryHistory = create<DiscoveryHistoryState>((set, get) => ({
  ...load(),
  record: (spec, resultCount) => {
    const recent = upsertRecentSearch(get().recent, spec, resultCount, Date.now(), id('search'));
    set({ recent });
    persist(recent, get().saved);
  },
  save: (spec, name) => {
    const before = get().saved;
    const saved = upsertSavedSearch(before, spec, Date.now(), id('saved-search'), name);
    set({ saved });
    persist(get().recent, saved);
    return saved.find((entry) => discoveryQueryKey(entry) === discoveryQueryKey(spec))?.id ?? '';
  },
  removeRecent: (searchId) => {
    const recent = get().recent.filter((entry) => entry.id !== searchId);
    set({ recent });
    persist(recent, get().saved);
  },
  removeSaved: (searchId) => {
    const saved = get().saved.filter((entry) => entry.id !== searchId);
    set({ saved });
    persist(get().recent, saved);
  },
  setSavedSchedule: (searchId, schedule) => {
    const saved = get().saved.map((entry) => (entry.id === searchId ? { ...entry, schedule } : entry));
    set({ saved });
    persist(get().recent, saved);
  },
  clearRecent: () => {
    set({ recent: [] });
    persist([], get().saved);
  },
}));
