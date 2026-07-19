import { defaultShouldDehydrateQuery, QueryClient, type Query } from '@tanstack/react-query';
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

// Throttle snapshot writes: the provider calls persistClient on every cache
// mutation, and a burst of query settles (a fleet refresh) would otherwise
// JSON.stringify + write the whole snapshot many times in a frame (#311). Write
// at most once per second, trailing (so the latest state still lands).
let persistTimer: ReturnType<typeof setTimeout> | undefined;
let pendingClient: PersistedClient | null = null;
function flushPersist(): void {
  if (pendingClient === null) return;
  try {
    localStorage.setItem(CACHE_KEY, JSON.stringify(pendingClient));
  } catch {
    /* quota / serialization — skip persisting this tick */
  }
  pendingClient = null;
}

export const localStoragePersister: Persister = {
  persistClient: async (client: PersistedClient) => {
    pendingClient = client;
    if (persistTimer === undefined) {
      persistTimer = setTimeout(() => {
        persistTimer = undefined;
        flushPersist();
      }, 1000);
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
    // Cancel a pending throttled write so it can't resurrect the snapshot after
    // a clear (profile switch / disconnect).
    if (persistTimer !== undefined) {
      clearTimeout(persistTimer);
      persistTimer = undefined;
    }
    pendingClient = null;
    localStorage.removeItem(CACHE_KEY);
  },
};

export const persistMaxAge = WEEK_MS;

// Query families whose payloads are large (raw blob bytes, full document/
// transcript bodies) and not worth serializing into localStorage on every cache
// write — persisting them bloats the snapshot and each JSON.stringify is a
// main-thread stall (#311). The cache-first snapshot only needs the light
// list/overview metadata that fills the shell on reload.
const NO_PERSIST_KEYS = new Set([
  'blob',
  'document',
  'documents',
  'project-doc',
  'project-docs',
  'agent-turns',
  'agent-digest',
  'insights',
  'audit',
]);

/// Decides which queries get written to the persisted snapshot: the TanStack
/// default (successful only) minus the heavy payload families above.
export function shouldPersistQuery(query: Query): boolean {
  if (!defaultShouldDehydrateQuery(query)) return false;
  const head = query.queryKey[0];
  return !(typeof head === 'string' && NO_PERSIST_KEYS.has(head));
}

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
