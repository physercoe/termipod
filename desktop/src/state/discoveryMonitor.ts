import { create } from 'zustand';
import {
  enrichWithUnpaywall,
  loadGoogleScholarCitations,
  loadOpenAlexCitations,
  sourceById,
  type DiscoveryPaper,
  type DiscoverySourceId,
} from '../discovery';
import { fetchDiscoveryFeed } from '../discovery/rss';
import { useDiscoveryHistory } from './discoveryHistory';
import type { DiscoveryQuerySpec } from './discoveryHistoryCore';
import {
  mergeTargetResults,
  targetDue,
  type DiscoveryCadence,
  type DiscoverySubscription,
  type DiscoverySubscriptionKind,
  type DiscoveryTargetRun,
  type DiscoveryUpdate,
} from './discoveryMonitorCore';

const LS_KEY = 'termipod.discover.monitor.v1';

interface PersistedDiscoveryMonitor {
  version: 1;
  subscriptions: DiscoverySubscription[];
  updates: DiscoveryUpdate[];
  runs: Record<string, DiscoveryTargetRun>;
  lastRefreshAt?: number;
}

interface AddSubscription {
  kind: DiscoverySubscriptionKind;
  label: string;
  value: string;
  sourceId?: DiscoverySourceId;
  cadence: DiscoveryCadence;
  referenceId?: string;
}

interface DiscoveryMonitorState {
  subscriptions: DiscoverySubscription[];
  updates: DiscoveryUpdate[];
  runs: Record<string, DiscoveryTargetRun>;
  lastRefreshAt?: number;
  refreshing: boolean;
  addSubscription: (input: AddSubscription) => string;
  removeSubscription: (id: string) => void;
  setSubscriptionCadence: (id: string, cadence: DiscoveryCadence) => void;
  markRead: (id: string) => void;
  markAllRead: () => void;
  removeUpdate: (id: string) => void;
  clearRead: () => void;
}

const KINDS = new Set<DiscoverySubscriptionKind>(['author', 'journal', 'topic', 'citation', 'rss']);
const CADENCES = new Set<DiscoveryCadence>(['daily', 'weekly', 'monthly']);
const SOURCES = new Set<DiscoverySourceId>([
  'openalex', 'semanticscholar', 'google-scholar', 'crossref', 'arxiv', 'pubmed', 'core',
]);

function isSubscription(value: unknown): value is DiscoverySubscription {
  if (value === null || typeof value !== 'object') return false;
  const row = value as Record<string, unknown>;
  return typeof row.id === 'string' &&
    typeof row.kind === 'string' && KINDS.has(row.kind as DiscoverySubscriptionKind) &&
    typeof row.label === 'string' && typeof row.value === 'string' &&
    typeof row.sourceId === 'string' && SOURCES.has(row.sourceId as DiscoverySourceId) &&
    typeof row.cadence === 'string' && CADENCES.has(row.cadence as DiscoveryCadence) &&
    typeof row.createdAt === 'number' &&
    (row.referenceId === undefined || typeof row.referenceId === 'string');
}

function isPaper(value: unknown): value is DiscoveryPaper {
  if (value === null || typeof value !== 'object') return false;
  const row = value as Record<string, unknown>;
  return typeof row.paperId === 'string' && typeof row.title === 'string' && Array.isArray(row.authors);
}

function isUpdate(value: unknown): value is DiscoveryUpdate {
  if (value === null || typeof value !== 'object') return false;
  const row = value as Record<string, unknown>;
  return typeof row.id === 'string' &&
    (row.originType === 'saved-search' || row.originType === 'subscription') &&
    typeof row.originId === 'string' && typeof row.originLabel === 'string' &&
    typeof row.arrivedAt === 'number' && isPaper(row.paper) &&
    (row.readAt === undefined || typeof row.readAt === 'number');
}

function load(): Pick<DiscoveryMonitorState, 'subscriptions' | 'updates' | 'runs' | 'lastRefreshAt'> {
  try {
    const parsed = JSON.parse(localStorage.getItem(LS_KEY) ?? '') as Partial<PersistedDiscoveryMonitor>;
    const subscriptions = Array.isArray(parsed.subscriptions) ? parsed.subscriptions.filter(isSubscription) : [];
    const updates = Array.isArray(parsed.updates) ? parsed.updates.filter(isUpdate).slice(0, 500) : [];
    const runs: Record<string, DiscoveryTargetRun> = {};
    if (parsed.runs !== null && typeof parsed.runs === 'object') {
      for (const [key, run] of Object.entries(parsed.runs)) {
        if (run !== null && typeof run === 'object' && typeof run.lastRunAt === 'number' && Array.isArray(run.seen)) {
          runs[key] = {
            lastRunAt: run.lastRunAt,
            lastError: typeof run.lastError === 'string' ? run.lastError : undefined,
            seen: run.seen.filter((entry): entry is string => typeof entry === 'string').slice(0, 250),
          };
        }
      }
    }
    return {
      subscriptions,
      updates,
      runs,
      lastRefreshAt: typeof parsed.lastRefreshAt === 'number' ? parsed.lastRefreshAt : undefined,
    };
  } catch {
    return { subscriptions: [], updates: [], runs: {}, lastRefreshAt: undefined };
  }
}

function persist(state: Pick<DiscoveryMonitorState, 'subscriptions' | 'updates' | 'runs' | 'lastRefreshAt'>): void {
  try {
    const payload: PersistedDiscoveryMonitor = {
      version: 1,
      subscriptions: state.subscriptions,
      updates: state.updates,
      runs: state.runs,
      lastRefreshAt: state.lastRefreshAt,
    };
    localStorage.setItem(LS_KEY, JSON.stringify(payload));
  } catch (error) {
    console.error(`[discovery] failed to persist "${LS_KEY}"`, error);
  }
}

function commit(partial: Partial<DiscoveryMonitorState>): void {
  useDiscoveryMonitor.setState(partial);
  const state = useDiscoveryMonitor.getState();
  persist(state);
}

function newId(prefix: string): string {
  return `${prefix}${crypto.randomUUID()}`;
}

export const useDiscoveryMonitor = create<DiscoveryMonitorState>((set, get) => ({
  ...load(),
  refreshing: false,
  addSubscription: (input) => {
    const id = newId('subscription');
    const subscription: DiscoverySubscription = {
      id,
      kind: input.kind,
      label: input.label.trim() || input.value.trim(),
      value: input.value.trim(),
      sourceId: input.sourceId ?? 'openalex',
      cadence: input.cadence,
      createdAt: Date.now(),
      referenceId: input.referenceId,
    };
    const subscriptions = [subscription, ...get().subscriptions];
    set({ subscriptions });
    persist({ ...get(), subscriptions });
    return id;
  },
  removeSubscription: (id) => {
    const subscriptions = get().subscriptions.filter((entry) => entry.id !== id);
    const runs = { ...get().runs };
    delete runs[`subscription:${id}`];
    set({ subscriptions, runs });
    persist({ ...get(), subscriptions, runs });
  },
  setSubscriptionCadence: (id, cadence) => {
    const subscriptions = get().subscriptions.map((entry) => entry.id === id ? { ...entry, cadence } : entry);
    set({ subscriptions });
    persist({ ...get(), subscriptions });
  },
  markRead: (id) => {
    const updates = get().updates.map((entry) => entry.id === id && entry.readAt === undefined ? { ...entry, readAt: Date.now() } : entry);
    set({ updates });
    persist({ ...get(), updates });
  },
  markAllRead: () => {
    const now = Date.now();
    const updates = get().updates.map((entry) => entry.readAt === undefined ? { ...entry, readAt: now } : entry);
    set({ updates });
    persist({ ...get(), updates });
  },
  removeUpdate: (id) => {
    const updates = get().updates.filter((entry) => entry.id !== id);
    set({ updates });
    persist({ ...get(), updates });
  },
  clearRead: () => {
    const updates = get().updates.filter((entry) => entry.readAt === undefined);
    set({ updates });
    persist({ ...get(), updates });
  },
}));

function filterSpecResults(papers: DiscoveryPaper[], spec: DiscoveryQuerySpec): DiscoveryPaper[] {
  const author = spec.authorFilter.trim().toLocaleLowerCase();
  const from = spec.yearFrom === '' ? undefined : Number(spec.yearFrom);
  const to = spec.yearTo === '' ? undefined : Number(spec.yearTo);
  const filtered = papers.filter((paper) =>
    (author === '' || paper.authors.some((name) => name.toLocaleLowerCase().includes(author))) &&
    (from === undefined || !Number.isFinite(from) || (paper.year !== undefined && paper.year >= from)) &&
    (to === undefined || !Number.isFinite(to) || (paper.year !== undefined && paper.year <= to)),
  );
  if (spec.sort === 'relevance') return filtered;
  return [...filtered].sort((a, b) => {
    if (spec.sort === 'title') return a.title.localeCompare(b.title);
    if (spec.sort === 'citations') return (b.citationCount ?? -1) - (a.citationCount ?? -1);
    if (a.year === undefined) return b.year === undefined ? 0 : 1;
    if (b.year === undefined) return -1;
    return spec.sort === 'newest' ? b.year - a.year : a.year - b.year;
  });
}

async function runSavedSearch(spec: DiscoveryQuerySpec): Promise<DiscoveryPaper[]> {
  const source = sourceById(spec.sourceId);
  let papers: DiscoveryPaper[] = (await source.search(spec.query, 25)).map((paper) => ({ ...paper, source: source.id }));
  if (spec.findPdfs) papers = await enrichWithUnpaywall(papers);
  return filterSpecResults(papers, spec);
}

async function runSubscription(subscription: DiscoverySubscription): Promise<DiscoveryPaper[]> {
  if (subscription.kind === 'rss') {
    return (await fetchDiscoveryFeed(subscription.value)).papers;
  }
  if (subscription.kind === 'citation') {
    if (subscription.sourceId === 'google-scholar') {
      return (await loadGoogleScholarCitations(subscription.value, 0, 25)).papers.map((paper) => ({
        ...paper,
        source: 'google-scholar' as const,
      }));
    }
    return (await loadOpenAlexCitations(subscription.value, 25)).map((paper) => ({ ...paper, source: 'openalex' as const }));
  }
  const source = sourceById(subscription.sourceId);
  let papers = (await source.search(subscription.value, 25)).map((paper) => ({ ...paper, source: source.id }));
  const needle = subscription.value.toLocaleLowerCase();
  if (subscription.kind === 'author') {
    papers = papers.filter((paper) => paper.authors.some((author) => author.toLocaleLowerCase().includes(needle)));
  } else if (subscription.kind === 'journal') {
    papers = papers.filter((paper) => paper.venue?.toLocaleLowerCase().includes(needle) === true);
  }
  return papers;
}

let refreshPromise: Promise<number> | null = null;

export function refreshDiscoveryTargets(options: { force?: boolean; targetKey?: string } = {}): Promise<number> {
  if (refreshPromise !== null) return refreshPromise.then(() => refreshDiscoveryTargets(options));
  refreshPromise = (async () => {
    const now = Date.now();
    commit({ refreshing: true });
    let totalAdded = 0;
    const saved = useDiscoveryHistory.getState().saved.filter((entry) => entry.schedule !== undefined);
    const subscriptions = useDiscoveryMonitor.getState().subscriptions;
    const targets: Array<{
      key: string;
      cadence: DiscoveryCadence;
      origin: Pick<DiscoveryUpdate, 'originType' | 'originId' | 'originLabel'>;
      run: () => Promise<DiscoveryPaper[]>;
    }> = [
      ...saved.map((entry) => ({
        key: `saved-search:${entry.id}`,
        cadence: entry.schedule!,
        origin: { originType: 'saved-search' as const, originId: entry.id, originLabel: entry.name },
        run: () => runSavedSearch(entry),
      })),
      ...subscriptions.map((entry) => ({
        key: `subscription:${entry.id}`,
        cadence: entry.cadence,
        origin: { originType: 'subscription' as const, originId: entry.id, originLabel: entry.label },
        run: () => runSubscription(entry),
      })),
    ];
    for (const target of targets) {
      if (options.targetKey !== undefined && target.key !== options.targetKey) continue;
      const state = useDiscoveryMonitor.getState();
      const previousRun = state.runs[target.key];
      if (options.force !== true && !targetDue(target.cadence, previousRun, now)) continue;
      try {
        const papers = await target.run();
        const merged = mergeTargetResults(
          state.updates,
          previousRun,
          papers,
          target.origin,
          Date.now(),
          papers.map(() => newId('update')),
        );
        totalAdded += merged.added;
        commit({
          updates: merged.updates,
          runs: { ...useDiscoveryMonitor.getState().runs, [target.key]: merged.run },
        });
      } catch (error) {
        const latest = useDiscoveryMonitor.getState();
        commit({
          runs: {
            ...latest.runs,
            [target.key]: {
              lastRunAt: Date.now(),
              lastError: error instanceof Error ? error.message : String(error),
              seen: previousRun?.seen ?? [],
            },
          },
        });
      }
    }
    commit({ refreshing: false, lastRefreshAt: Date.now() });
    return totalAdded;
  })().finally(() => {
    refreshPromise = null;
    if (useDiscoveryMonitor.getState().refreshing) commit({ refreshing: false });
  });
  return refreshPromise;
}

export function startDiscoveryScheduler(): () => void {
  const refresh = (): void => {
    void refreshDiscoveryTargets();
  };
  refresh();
  const timer = window.setInterval(refresh, 5 * 60 * 1000);
  window.addEventListener('focus', refresh);
  return () => {
    window.clearInterval(timer);
    window.removeEventListener('focus', refresh);
  };
}
