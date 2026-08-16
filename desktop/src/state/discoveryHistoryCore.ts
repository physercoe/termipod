import type { DiscoverySourceId } from '../discovery/types.ts';
import type { DiscoverySort } from './discoverySearch.ts';

export interface DiscoveryQuerySpec {
  query: string;
  sourceId: DiscoverySourceId;
  authorFilter: string;
  yearFrom: string;
  yearTo: string;
  sort: DiscoverySort;
  findPdfs: boolean;
}

export interface RecentDiscoverySearch extends DiscoveryQuerySpec {
  id: string;
  ranAt: number;
  resultCount: number;
}

export interface SavedDiscoverySearch extends DiscoveryQuerySpec {
  id: string;
  name: string;
  savedAt: number;
}

export const MAX_RECENT_SEARCHES = 20;

export function normalizeDiscoveryQuery(query: string): string {
  return query.trim().replace(/\s+/g, ' ');
}

export function discoveryQueryKey(spec: DiscoveryQuerySpec): string {
  return JSON.stringify([
    normalizeDiscoveryQuery(spec.query).toLocaleLowerCase(),
    spec.sourceId,
    spec.authorFilter.trim().toLocaleLowerCase(),
    spec.yearFrom.trim(),
    spec.yearTo.trim(),
    spec.sort,
    spec.findPdfs,
  ]);
}

export function upsertRecentSearch(
  existing: RecentDiscoverySearch[],
  spec: DiscoveryQuerySpec,
  resultCount: number,
  ranAt: number,
  id: string,
): RecentDiscoverySearch[] {
  const normalized = { ...spec, query: normalizeDiscoveryQuery(spec.query) };
  const key = discoveryQueryKey(normalized);
  const previous = existing.find((entry) => discoveryQueryKey(entry) === key);
  const entry: RecentDiscoverySearch = {
    ...normalized,
    id: previous?.id ?? id,
    ranAt,
    resultCount: Math.max(0, Math.trunc(resultCount)),
  };
  return [entry, ...existing.filter((item) => discoveryQueryKey(item) !== key)]
    .sort((a, b) => b.ranAt - a.ranAt)
    .slice(0, MAX_RECENT_SEARCHES);
}

export function upsertSavedSearch(
  existing: SavedDiscoverySearch[],
  spec: DiscoveryQuerySpec,
  savedAt: number,
  id: string,
  name?: string,
): SavedDiscoverySearch[] {
  const normalized = { ...spec, query: normalizeDiscoveryQuery(spec.query) };
  const key = discoveryQueryKey(normalized);
  const previous = existing.find((entry) => discoveryQueryKey(entry) === key);
  const entry: SavedDiscoverySearch = {
    ...normalized,
    id: previous?.id ?? id,
    name: name?.trim() || previous?.name || normalized.query,
    savedAt: previous?.savedAt ?? savedAt,
  };
  return [entry, ...existing.filter((item) => discoveryQueryKey(item) !== key)].sort((a, b) => b.savedAt - a.savedAt);
}
