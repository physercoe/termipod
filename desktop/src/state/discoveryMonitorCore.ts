import type { DiscoveryPaper, DiscoverySourceId } from '../discovery/types.ts';
import type { Collection, Reference } from './library.ts';

export type DiscoveryCadence = 'daily' | 'weekly' | 'monthly';
export type DiscoverySubscriptionKind = 'author' | 'journal' | 'topic' | 'citation' | 'rss';

export interface DiscoverySubscription {
  id: string;
  kind: DiscoverySubscriptionKind;
  label: string;
  value: string;
  sourceId: DiscoverySourceId;
  cadence: DiscoveryCadence;
  createdAt: number;
  referenceId?: string;
}

export interface DiscoveryTargetRun {
  lastRunAt: number;
  lastError?: string;
  seen: string[];
}

export interface DiscoveryUpdate {
  id: string;
  originType: 'saved-search' | 'subscription';
  originId: string;
  originLabel: string;
  paper: DiscoveryPaper;
  arrivedAt: number;
  readAt?: number;
}

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

export function mergeTargetResults(
  existingUpdates: DiscoveryUpdate[],
  previousRun: DiscoveryTargetRun | undefined,
  papers: DiscoveryPaper[],
  origin: Pick<DiscoveryUpdate, 'originType' | 'originId' | 'originLabel'>,
  now: number,
  ids: string[],
): { updates: DiscoveryUpdate[]; run: DiscoveryTargetRun; added: number } {
  const oldSeen = new Set(previousRun?.seen ?? []);
  const batchSeen = new Set<string>();
  const additions: DiscoveryUpdate[] = [];
  for (let index = 0; index < papers.length; index += 1) {
    const paper = papers[index]!;
    const fingerprint = paperFingerprint(paper);
    if (batchSeen.has(fingerprint)) continue;
    batchSeen.add(fingerprint);
    if (oldSeen.has(fingerprint)) continue;
    additions.push({
      id: ids[index] ?? `${origin.originId}:${fingerprint}`,
      ...origin,
      paper: compactPaper(paper),
      arrivedAt: now,
    });
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
