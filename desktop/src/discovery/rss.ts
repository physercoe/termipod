import { invoke, isShell } from '../bridge';
import { proxyForConnection } from '../state/proxy';
import type { DiscoveryPaper } from './types';

interface FeedPayload {
  url: string;
  text: string;
}

export interface ParsedDiscoveryFeed {
  title: string;
  url: string;
  papers: DiscoveryPaper[];
}

function child(element: Element, localName: string): Element | undefined {
  return [...element.children].find((candidate) => candidate.localName.toLocaleLowerCase() === localName);
}

function children(element: Element, localName: string): Element[] {
  return [...element.children].filter((candidate) => candidate.localName.toLocaleLowerCase() === localName);
}

function text(element: Element, ...names: string[]): string | undefined {
  for (const name of names) {
    const value = child(element, name)?.textContent?.replace(/\s+/g, ' ').trim();
    if (value !== undefined && value !== '') return value;
  }
  return undefined;
}

function plainText(value: string | undefined): string | undefined {
  if (value === undefined || value === '') return undefined;
  const parsed = new DOMParser().parseFromString(value, 'text/html').body.textContent?.replace(/\s+/g, ' ').trim();
  return parsed === undefined || parsed === '' ? undefined : parsed;
}

function year(value: string | undefined): number | undefined {
  if (value === undefined) return undefined;
  const timestamp = Date.parse(value);
  return Number.isFinite(timestamp) ? new Date(timestamp).getUTCFullYear() : undefined;
}

function atomLink(entry: Element): string | undefined {
  const links = children(entry, 'link');
  const preferred = links.find((link) => (link.getAttribute('rel') ?? 'alternate') === 'alternate') ?? links[0];
  return preferred?.getAttribute('href') ?? preferred?.textContent?.trim() ?? undefined;
}

function rssAuthors(entry: Element): string[] {
  const values = [text(entry, 'author'), text(entry, 'creator')].filter((value): value is string => value !== undefined);
  return values.flatMap((value) => value.split(/\s*[,;]\s*/)).filter(Boolean);
}

function atomAuthors(entry: Element): string[] {
  return children(entry, 'author')
    .map((author) => text(author, 'name') ?? author.textContent?.trim() ?? '')
    .filter(Boolean);
}

export function parseDiscoveryFeed(xml: string, feedUrl: string): ParsedDiscoveryFeed {
  const doc = new DOMParser().parseFromString(xml, 'application/xml');
  if (doc.querySelector('parsererror') !== null) throw new Error('Invalid RSS or Atom feed');
  const root = doc.documentElement;
  const atom = root.localName.toLocaleLowerCase() === 'feed';
  const container = atom ? root : child(root, 'channel') ?? root;
  const feedTitle = text(container, 'title') ?? new URL(feedUrl).hostname;
  const entries = atom ? children(container, 'entry') : children(container, 'item');
  const papers = entries.map((entry): DiscoveryPaper | null => {
    const title = text(entry, 'title') ?? '';
    if (title === '') return null;
    const url = atom ? atomLink(entry) : text(entry, 'link');
    const nativeId = text(entry, 'id', 'guid') ?? url ?? title;
    const published = text(entry, 'published', 'updated', 'pubdate', 'date');
    const summary = plainText(text(entry, 'summary', 'description', 'content'));
    return {
      paperId: `rss:${nativeId}`,
      title,
      authors: atom ? atomAuthors(entry) : rssAuthors(entry),
      year: year(published),
      venue: feedTitle,
      abstract: summary,
      url,
    };
  }).filter((paper): paper is DiscoveryPaper => paper !== null);
  return { title: feedTitle, url: feedUrl, papers };
}

export async function fetchDiscoveryFeed(url: string): Promise<ParsedDiscoveryFeed> {
  let payload: FeedPayload;
  if (isShell()) {
    payload = await invoke<FeedPayload>('discovery_fetch_feed', {
      url,
      proxy: proxyForConnection('discovery') ?? null,
    });
  } else {
    const response = await fetch(url, {
      headers: { Accept: 'application/atom+xml, application/rss+xml, application/xml, text/xml' },
      signal: AbortSignal.timeout(45_000),
    });
    if (!response.ok) throw new Error(`RSS: HTTP ${response.status}`);
    payload = { url: response.url || url, text: await response.text() };
  }
  return parseDiscoveryFeed(payload.text, payload.url);
}

