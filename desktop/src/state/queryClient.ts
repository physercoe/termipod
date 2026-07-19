import { QueryClient } from '@tanstack/react-query';
import type { PersistedClient, Persister } from '@tanstack/react-query-persist-client';
import { HubApiError } from '../hub/errors';

/// Cache-first / offline foundation (parity Phase 3b, ADR-006). The TanStack
/// QueryClient's cache is persisted to localStorage, so on reload the last-known
/// fleet/projects/etc. render instantly and — when the hub is unreachable — the
/// surfaces keep showing the last successful data instead of blanking. Only
/// successful query results are persisted (a 4xx/error is never written as
/// data, matching the mobile "4xx never cached" rule). Query keys already carry
/// the team id; switching profiles calls clearCache() so one hub's data never
/// bleeds into another's.

const CACHE_KEY = 'termipod.qcache';
const WEEK_MS = 7 * 24 * 60 * 60 * 1000;

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 5000,
      // gcTime must outlive the persister maxAge or entries are dropped before
      // they can be restored — hold a week to match the mobile snapshot TTL.
      gcTime: WEEK_MS,
      refetchOnWindowFocus: false,
      // Retry once for transient failures (network/5xx), but never for a
      // deterministic 4xx — a 400/401/403/404 will answer identically on a
      // retry, so retrying just doubles latency before the error surfaces.
      retry: (failureCount, error) => {
        if (error instanceof HubApiError && error.status >= 400 && error.status < 500) return false;
        return failureCount < 1;
      },
    },
  },
});

export const localStoragePersister: Persister = {
  persistClient: async (client: PersistedClient) => {
    try {
      localStorage.setItem(CACHE_KEY, JSON.stringify(client));
    } catch {
      /* quota / serialization — skip persisting this tick */
    }
  },
  restoreClient: async () => {
    try {
      const raw = localStorage.getItem(CACHE_KEY);
      return raw !== null ? (JSON.parse(raw) as PersistedClient) : undefined;
    } catch {
      return undefined;
    }
  },
  removeClient: async () => {
    localStorage.removeItem(CACHE_KEY);
  },
};

export const persistMaxAge = WEEK_MS;

/// Drop all cached query data (in-memory + persisted). Called on profile switch
/// and disconnect so a different hub/team starts clean, and from the cache
/// settings "Clear cache" action.
export function clearCache(): void {
  queryClient.clear();
  void localStoragePersister.removeClient();
}

/// Approximate size of the persisted cache, for the settings display.
export function cacheSizeBytes(): number {
  try {
    return localStorage.getItem(CACHE_KEY)?.length ?? 0;
  } catch {
    return 0;
  }
}
