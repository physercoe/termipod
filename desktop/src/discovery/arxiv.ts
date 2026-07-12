import { httpGet } from './http';
import type { DiscoveryPaper } from './types';

/// arXiv — preprints (CS / physics / math / stats). The API returns Atom XML
/// (not JSON), parsed here with the webview's DOMParser.

function text(el: Element, tag: string): string | undefined {
  const n = el.getElementsByTagName(tag)[0];
  const t = n?.textContent?.replace(/\s+/g, ' ').trim();
  return t === '' ? undefined : t;
}

function mapEntry(e: Element): DiscoveryPaper {
  const idUrl = text(e, 'id') ?? '';
  const m = idUrl.match(/abs\/([^/]+?)(v\d+)?$/);
  const arxivId = m ? m[1] : undefined;
  const links = Array.from(e.getElementsByTagName('link'));
  const pdf = links.find((l) => l.getAttribute('title') === 'pdf')?.getAttribute('href') ?? undefined;
  const authors = Array.from(e.getElementsByTagName('author'))
    .map((a) => a.getElementsByTagName('name')[0]?.textContent?.trim())
    .filter((n): n is string => typeof n === 'string' && n !== '');
  const published = text(e, 'published');
  const doiEl = e.getElementsByTagName('arxiv:doi')[0]?.textContent?.trim();
  return {
    paperId: idUrl,
    title: text(e, 'title') ?? '',
    authors,
    year: published !== undefined ? parseInt(published.slice(0, 4), 10) || undefined : undefined,
    abstract: text(e, 'summary'),
    doi: doiEl !== undefined && doiEl !== '' ? doiEl : undefined,
    arxivId,
    pdfUrl: pdf,
    url: idUrl,
  };
}

export async function searchArxiv(query: string, limit: number): Promise<DiscoveryPaper[]> {
  const url = `https://export.arxiv.org/api/query?search_query=${encodeURIComponent(`all:${query}`)}&start=0&max_results=${limit}`;
  const xml = await httpGet(url, { accept: 'application/atom+xml' }, 3);
  const doc = new DOMParser().parseFromString(xml, 'application/xml');
  const entries = Array.from(doc.getElementsByTagName('entry'));
  return entries.map(mapEntry).filter((p) => p.title !== '');
}
