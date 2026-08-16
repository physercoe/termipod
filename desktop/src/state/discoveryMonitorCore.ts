import type { DiscoveryPaper, DiscoverySourceId } from '../discovery/types.ts';
import type { Collection, Reference } from './library.ts';

export type DiscoveryCadence = 'daily' | 'weekly' | 'monthly';
export type DiscoverySubscriptionKind =
  | 'author'
  | 'journal'
  | 'topic'
  | 'citation'
  | 'rss'
  | 'bluesky-author'
  | 'bluesky-feed'
  | 'bluesky-query'
  | 'mastodon-author'
  | 'mastodon-tag'
  | 'youtube-channel'
  | 'x-author'
  | 'x-query';

export type DiscoverySubscriptionGroup = 'research' | 'social' | 'monitors' | 'feeds';

export interface DiscoveryMonitorFilters {
  include?: string;
  exclude?: string;
  language?: string;
  excludeReposts?: boolean;
  minEngagement?: number;
}

export interface DiscoverySubscription {
  id: string;
  kind: DiscoverySubscriptionKind;
  label: string;
  value: string;
  sourceId?: DiscoverySourceId;
  cadence: DiscoveryCadence;
  createdAt: number;
  referenceId?: string;
  filters?: DiscoveryMonitorFilters;
  paused?: boolean;
}

export interface DiscoveryTargetRun {
  lastRunAt: number;
  lastError?: string;
  seen: string[];
}

export interface DiscoverySocialPost {
  id: string;
  platform: 'bluesky' | 'mastodon' | 'youtube' | 'x';
  author: string;
  handle?: string;
  text: string;
  title?: string;
  url: string;
  publishedAt?: number;
  language?: string;
  avatarUrl?: string;
  tags?: string[];
  engagement?: {
    likes?: number;
    reposts?: number;
    replies?: number;
  };
}

interface DiscoveryUpdateBase {
  id: string;
  originType: 'saved-search' | 'subscription';
  originId: string;
  originLabel: string;
  arrivedAt: number;
  readAt?: number;
}

export type DiscoveryUpdate = DiscoveryUpdateBase & (
  | { paper: DiscoveryPaper; social?: never }
  | { paper?: never; social: DiscoverySocialPost }
);

export type DiscoveryResult = { paper: DiscoveryPaper } | { social: DiscoverySocialPost };

export const MAX_UPDATES = 500;
export const MAX_SEEN_PER_TARGET = 250;

function compactPaper(paper: DiscoveryPaper): DiscoveryPaper {
  return {
    ...paper,
    authors: paper.authors.slice(0, 20),
    abstract: paper.abstract?.slice(0, 4_000),
    tldr: paper.tldr?.slice(0, 1_000),
  };
}

function compactSocial(post: DiscoverySocialPost): DiscoverySocialPost {
  return {
    ...post,
    author: post.author.slice(0, 240),
    handle: post.handle?.slice(0, 240),
    title: post.title?.slice(0, 500),
    text: post.text.slice(0, 8_000),
    tags: post.tags?.slice(0, 30),
  };
}

const CADENCE_MS: Record<DiscoveryCadence, number> = {
  daily: 24 * 60 * 60 * 1000,
  weekly: 7 * 24 * 60 * 60 * 1000,
  monthly: 30 * 24 * 60 * 60 * 1000,
};

export function targetDue(cadence: DiscoveryCadence, run: DiscoveryTargetRun | undefined, now: number): boolean {
  if (run === undefined) return true;
  const retryAfter = run.lastError === undefined ? CADENCE_MS[cadence] : 15 * 60 * 1000;
  return now - run.lastRunAt >= retryAfter;
}

export function paperFingerprint(paper: Pick<DiscoveryPaper, 'paperId' | 'doi' | 'title' | 'year'>): string {
  const doi = paper.doi?.trim().toLocaleLowerCase();
  if (doi !== undefined && doi !== '') return `doi:${doi}`;
  const nativeId = paper.paperId.trim().toLocaleLowerCase();
  if (nativeId !== '') return `id:${nativeId}`;
  return `title:${paper.title.trim().replace(/\s+/g, ' ').toLocaleLowerCase()}|${paper.year ?? ''}`;
}

export function socialFingerprint(post: Pick<DiscoverySocialPost, 'platform' | 'id' | 'url'>): string {
  const id = post.id.trim().toLocaleLowerCase();
  if (id !== '') return `social:${post.platform}:${id}`;
  return `social:${post.platform}:${post.url.trim().toLocaleLowerCase()}`;
}

export function resultFingerprint(result: DiscoveryResult): string {
  return 'paper' in result ? paperFingerprint(result.paper) : socialFingerprint(result.social);
}

export function updateFingerprint(update: DiscoveryUpdate): string {
  return update.paper !== undefined ? paperFingerprint(update.paper) : socialFingerprint(update.social);
}

export function mergeTargetItems(
  existingUpdates: DiscoveryUpdate[],
  previousRun: DiscoveryTargetRun | undefined,
  results: DiscoveryResult[],
  origin: Pick<DiscoveryUpdate, 'originType' | 'originId' | 'originLabel'>,
  now: number,
  ids: string[],
): { updates: DiscoveryUpdate[]; run: DiscoveryTargetRun; added: number } {
  const oldSeen = new Set(previousRun?.seen ?? []);
  const batchSeen = new Set<string>();
  const additions: DiscoveryUpdate[] = [];
  for (let index = 0; index < results.length; index += 1) {
    const result = results[index]!;
    const fingerprint = resultFingerprint(result);
    if (batchSeen.has(fingerprint)) continue;
    batchSeen.add(fingerprint);
    if (oldSeen.has(fingerprint)) continue;
    const base = {
      id: ids[index] ?? `${origin.originId}:${fingerprint}`,
      ...origin,
      arrivedAt: now,
    };
    additions.push('paper' in result
      ? { ...base, paper: compactPaper(result.paper) }
      : { ...base, social: compactSocial(result.social) });
  }
  const seen = [...batchSeen, ...(previousRun?.seen ?? [])].filter(
    (value, index, all) => all.indexOf(value) === index,
  ).slice(0, MAX_SEEN_PER_TARGET);
  return {
    updates: [...additions, ...existingUpdates].slice(0, MAX_UPDATES),
    run: { lastRunAt: now, seen },
    added: additions.length,
  };
}

export function mergeTargetResults(
  existingUpdates: DiscoveryUpdate[],
  previousRun: DiscoveryTargetRun | undefined,
  papers: DiscoveryPaper[],
  origin: Pick<DiscoveryUpdate, 'originType' | 'originId' | 'originLabel'>,
  now: number,
  ids: string[],
): { updates: DiscoveryUpdate[]; run: DiscoveryTargetRun; added: number } {
  return mergeTargetItems(existingUpdates, previousRun, papers.map((paper) => ({ paper })), origin, now, ids);
}

const STOP_WORDS = new Set([
  'about', 'after', 'among', 'based', 'between', 'from', 'into', 'model', 'models', 'paper', 'study',
  'their', 'these', 'this', 'through', 'toward', 'using', 'with', 'without',
]);

function words(value: string): string[] {
  return value
    .toLocaleLowerCase()
    .replace(/[^\p{L}\p{N}-]+/gu, ' ')
    .split(/\s+/)
    .filter((word) => word.length >= 3 && !STOP_WORDS.has(word));
}

export interface DiscoveryTrend {
  term: string;
  evidenceIds: string[];
  recentCount: number;
  previousCount: number;
  velocity: number;
  authors: number;
  sources: number;
  days: number[];
  confidence: 'low' | 'medium' | 'high';
}

function updateWords(update: DiscoveryUpdate): string[] {
  if (update.paper !== undefined) {
    return words(`${update.paper.title} ${update.paper.abstract ?? ''} ${(update.paper.authors ?? []).join(' ')} ${update.paper.venue ?? ''}`);
  }
  return words(`${update.social.title ?? ''} ${update.social.text} ${(update.social.tags ?? []).join(' ')}`);
}

/// Builds transparent local trend signals from the monitoring inbox. A trend is
/// acceleration plus independent evidence, not simply the most popular post.
export function buildDiscoveryTrends(updates: DiscoveryUpdate[], now: number): DiscoveryTrend[] {
  const dayMs = 24 * 60 * 60 * 1000;
  const recentCutoff = now - dayMs;
  const historyCutoff = now - 7 * dayMs;
  const rows = new Map<string, {
    evidenceIds: string[];
    recent: number;
    previous: number;
    authors: Set<string>;
    sources: Set<string>;
    days: number[];
  }>();
  for (const update of updates) {
    const timestamp = update.social?.publishedAt ?? update.arrivedAt;
    if (timestamp < historyCutoff || timestamp > now + dayMs) continue;
    const author = update.paper !== undefined ? update.paper.authors[0] ?? '' : update.social.author;
    const source = update.paper?.source ?? update.social?.platform ?? update.originLabel;
    const dayIndex = Math.max(0, Math.min(6, 6 - Math.floor((now - timestamp) / dayMs)));
    for (const term of new Set(updateWords(update))) {
      if (term.length < 4 || /^\d+$/.test(term)) continue;
      const row = rows.get(term) ?? {
        evidenceIds: [], recent: 0, previous: 0, authors: new Set<string>(), sources: new Set<string>(), days: Array(7).fill(0) as number[],
      };
      row.evidenceIds.push(update.id);
      if (timestamp >= recentCutoff) row.recent += 1;
      else row.previous += 1;
      if (author !== '') row.authors.add(author.toLocaleLowerCase());
      row.sources.add(String(source));
      row.days[dayIndex] = (row.days[dayIndex] ?? 0) + 1;
      rows.set(term, row);
    }
  }
  return [...rows.entries()]
    .filter(([, row]) => row.evidenceIds.length >= 2 && row.recent >= 1)
    .map(([term, row]) => {
      const baseline = row.previous / 6;
      const velocity = (row.recent + 0.5) / (baseline + 0.5);
      const independent = Math.min(row.authors.size, row.sources.size + 1);
      const confidence: DiscoveryTrend['confidence'] = row.evidenceIds.length >= 6 && independent >= 3
        ? 'high'
        : row.evidenceIds.length >= 3 && independent >= 2 ? 'medium' : 'low';
      return {
        term,
        evidenceIds: row.evidenceIds,
        recentCount: row.recent,
        previousCount: row.previous,
        velocity,
        authors: row.authors.size,
        sources: row.sources.size,
        days: row.days,
        confidence,
      };
    })
    .sort((a, b) => (b.velocity * Math.log2(b.evidenceIds.length + 1)) - (a.velocity * Math.log2(a.evidenceIds.length + 1)))
    .slice(0, 30);
}

export interface RecommendationSeed {
  collection: Collection;
  references: Reference[];
  query: string;
  terms: string[];
}

export function buildRecommendationSeed(
  collectionId: string,
  collections: Collection[],
  references: Reference[],
): RecommendationSeed | null {
  const collection = collections.find((candidate) => candidate.id === collectionId);
  if (collection === undefined) return null;
  const members = references.filter((reference) => reference.collectionIds.includes(collectionId));
  if (members.length === 0) return { collection, references: [], query: '', terms: [] };
  const scored = new Map<string, number>();
  const add = (value: string, weight: number): void => {
    for (const word of words(value)) scored.set(word, (scored.get(word) ?? 0) + weight);
  };
  for (const reference of members) {
    for (const topic of reference.topics ?? []) add(topic, 5);
    for (const tag of reference.tags) add(tag, 4);
    if (reference.venue !== undefined) add(reference.venue, 2);
    add(reference.title, reference.rating !== undefined ? 1 + reference.rating / 2 : 1);
  }
  const terms = [...scored]
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .slice(0, 6)
    .map(([term]) => term);
  return { collection, references: members, terms, query: terms.join(' ') };
}
