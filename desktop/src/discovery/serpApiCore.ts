import type { DiscoveryPaper } from './types.ts';

const ENDPOINT = 'https://serpapi.com/search.json';

function object(raw: unknown): Record<string, unknown> | null {
  return raw !== null && typeof raw === 'object' ? (raw as Record<string, unknown>) : null;
}

function text(raw: unknown): string | undefined {
  return typeof raw === 'string' && raw.trim() !== '' ? raw.trim() : undefined;
}

function publicationYear(summary: string | undefined): number | undefined {
  if (summary === undefined) return undefined;
  const years = summary.match(/\b(?:18|19|20)\d{2}\b/g);
  if (years === null) return undefined;
  const year = Number(years.at(-1));
  return Number.isFinite(year) ? year : undefined;
}

function publicationVenue(summary: string | undefined): string | undefined {
  if (summary === undefined) return undefined;
  // Scholar summaries normally read "authors - venue, year - publisher".
  const middle = summary.split(' - ')[1]?.trim();
  if (middle === undefined || middle === '') return undefined;
  const venue = middle.replace(/,?\s*\b(?:18|19|20)\d{2}\b.*$/, '').trim();
  return venue !== '' ? venue : undefined;
}

function publicationAuthors(summary: string | undefined): string[] {
  if (summary === undefined) return [];
  const authorText = summary.split(' - ')[0]?.trim() ?? '';
  if (authorText === '') return [];
  return authorText.split(',').map((name) => name.trim()).filter((name) => name !== '');
}

function doiFromUrl(url: string | undefined): string | undefined {
  if (url === undefined) return undefined;
  const m = url.match(/(?:doi\.org\/|\/doi\/(?:abs\/|full\/)?)(10\.\d{4,9}\/[\w.()/:;-]+)/i);
  return m?.[1]?.replace(/[?#].*$/, '');
}

/// Normalize one SerpAPI Google Scholar organic result. Kept in this pure module
/// so the response contract can be pinned without spending API quota in tests.
export function normalizeSerpApiPaper(raw: unknown): DiscoveryPaper | null {
  const row = object(raw);
  if (row === null) return null;
  const title = text(row.title);
  if (title === undefined) return null;
  const publication = object(row.publication_info);
  const summary = text(publication?.summary);
  const listedAuthors = Array.isArray(publication?.authors)
    ? publication.authors
        .map((author) => text(object(author)?.name))
        .filter((name): name is string => name !== undefined)
    : [];
  const authors = listedAuthors.length > 0 ? listedAuthors : publicationAuthors(summary);
  const link = text(row.link);
  const resources = Array.isArray(row.resources) ? row.resources : [];
  const pdf = resources
    .map(object)
    .find((resource) => resource !== null && text(resource.file_format)?.toUpperCase() === 'PDF');
  const citedBy = object(object(row.inline_links)?.cited_by ?? row.cited_by);
  const resultId = text(row.result_id);

  return {
    paperId: resultId ?? link ?? title,
    title,
    authors,
    year: publicationYear(summary),
    venue: publicationVenue(summary),
    abstract: text(row.snippet),
    citationCount: typeof citedBy?.total === 'number' ? citedBy.total : undefined,
    doi: doiFromUrl(link),
    pdfUrl: text(pdf?.link) ?? (link?.toLowerCase().endsWith('.pdf') === true ? link : undefined),
    url: link,
  };
}

export function serpApiSearchUrl(query: string, limit: number, apiKey: string): string {
  const params = new URLSearchParams({
    engine: 'google_scholar',
    q: query,
    api_key: apiKey,
    hl: 'en',
    num: String(Math.max(1, Math.min(20, Math.trunc(limit)))),
  });
  return `${ENDPOINT}?${params.toString()}`;
}
