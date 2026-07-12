import { CONTACT, getJson } from './http';
import type { JournalMetrics, ResourceLink, WorkLink } from '../state/library';

/// The library **scraper** — given whatever identifiers an item already has
/// (DOI / arXiv id / OpenAlex id / title), it pulls the rich metadata the plain
/// inspector doesn't cover: the reference list, who cites it, journal-level
/// metrics (an IF-like signal), open-access status, topics, and code/data links.
///
/// The backbone is OpenAlex — free, keyless, CORS-free through the Rust
/// `hub_request` transport (see ./http.ts), and the one source that exposes the
/// full citation graph plus per-source `summary_stats`. Code/data links are
/// extracted from the abstract + landing metadata rather than a code index:
/// Papers-with-Code's API was retired (its `/api/v1/` now serves HTML), so there
/// is no keyless code-index left. Figures need PDF/HTML parsing and are out of
/// scope here (they belong with the pdf.js reader).

export interface ScrapeSeed {
  doi?: string;
  arxivId?: string;
  openAlexId?: string;
  title?: string;
  url?: string;
  abstract?: string;
}

/// A patch of enrichment fields to merge into a Reference. Bibliographic fields
/// (title/authors/…) are included so a brand-new item can be built from just an
/// identifier; the caller backfills those only when empty.
export interface ScrapePatch {
  title?: string;
  authors?: string[];
  year?: number;
  venue?: string;
  doi?: string;
  abstract?: string;
  pdfUrl?: string;
  arxivId?: string;
  referenceCount?: number;
  citedByCount?: number;
  references?: WorkLink[];
  citations?: WorkLink[];
  journal?: JournalMetrics;
  openAccess?: { status?: string; oaUrl?: string; isOa?: boolean };
  topics?: string[];
  resourceLinks?: ResourceLink[];
  detailsAdd?: Record<string, string>;
  enrichedAt: number;
  enrichSource: string;
}

export interface ScrapeResult {
  patch: ScrapePatch | null;
  found: string[]; // human labels of what was populated, for the toast
  identifier?: string; // what resolved the work (for the "not found" message)
}

// How many cited-/citing-works we fetch titles for. These are *samples* of the
// full graph (the true totals live in cited_by_count / referenced_works.length,
// shown as the metric card + the "of N" list count); OpenAlex allows per-page up
// to 200, so 50 is a generous, cheap sample.
const REF_SAMPLE = 50; // cap on cited-works we fetch titles for
const CITE_SAMPLE = 50; // cap on citing-works we fetch (top-cited first)

function asObj(v: unknown): Record<string, unknown> {
  return v !== null && typeof v === 'object' ? (v as Record<string, unknown>) : {};
}
function asStr(v: unknown): string | undefined {
  return typeof v === 'string' && v !== '' ? v : undefined;
}
function asNum(v: unknown): number | undefined {
  return typeof v === 'number' ? v : undefined;
}

// The bare `Wxxxx` tail of an OpenAlex id URL (used to build cites: filters).
function shortId(id: string | undefined): string | undefined {
  if (id === undefined) return undefined;
  const m = id.match(/([WwSsAa]\d+)\/?$/);
  return m !== null ? m[1] : undefined;
}

function stripDoi(d: unknown): string | undefined {
  const s = asStr(d);
  return s === undefined ? undefined : s.replace(/^https?:\/\/(dx\.)?doi\.org\//i, '');
}

function abstractFromInverted(inv: unknown): string | undefined {
  const o = asObj(inv);
  if (Object.keys(o).length === 0) return undefined;
  const words: string[] = [];
  for (const [word, positions] of Object.entries(o)) {
    if (Array.isArray(positions)) for (const p of positions as number[]) words[p] = word;
  }
  const s = words.join(' ').replace(/\s+/g, ' ').trim();
  return s === '' ? undefined : s;
}

function workLink(w: Record<string, unknown>): WorkLink {
  return {
    id: asStr(w.id),
    title: asStr(w.title) ?? asStr(w.display_name) ?? '(untitled)',
    year: asNum(w.publication_year),
    doi: stripDoi(w.doi),
  };
}

// ---- Resource-link extraction ----------------------------------------------

// Host → resource kind. Anything not listed is ignored (papers cite countless
// URLs; only these hosts are reliable code/data signals).
const HOST_KIND: { re: RegExp; kind: ResourceLink['kind'] }[] = [
  { re: /(^|\.)github\.com$/i, kind: 'code' },
  { re: /(^|\.)gitlab\.com$/i, kind: 'code' },
  { re: /(^|\.)bitbucket\.org$/i, kind: 'code' },
  { re: /(^|\.)sourceforge\.net$/i, kind: 'code' },
  { re: /(^|\.)zenodo\.org$/i, kind: 'data' },
  { re: /(^|\.)osf\.io$/i, kind: 'data' },
  { re: /(^|\.)figshare\.com$/i, kind: 'data' },
  { re: /(^|\.)datadryad\.org$/i, kind: 'data' },
  { re: /(^|\.)kaggle\.com$/i, kind: 'data' },
];

function classifyHost(host: string, path: string): ResourceLink['kind'] | undefined {
  if (/(^|\.)huggingface\.co$/i.test(host)) return /^\/datasets\//i.test(path) ? 'data' : 'model';
  for (const h of HOST_KIND) if (h.re.test(host)) return h.kind;
  return undefined;
}

/// Pull code/data/model links out of free text (abstract, landing metadata).
function extractResourceLinks(...texts: (string | undefined)[]): ResourceLink[] {
  const out = new Map<string, ResourceLink>();
  const urlRe = /https?:\/\/[^\s)<>"'\]]+/g;
  for (const text of texts) {
    if (text === undefined) continue;
    for (const raw of text.match(urlRe) ?? []) {
      const url = raw.replace(/[.,;:]+$/, ''); // trailing sentence punctuation
      let host = '';
      let path = '';
      try {
        const u = new URL(url);
        host = u.host;
        path = u.pathname;
      } catch {
        continue;
      }
      const kind = classifyHost(host, path);
      if (kind === undefined || out.has(url)) continue;
      out.set(url, { url, kind, host: host.replace(/^www\./, '') });
    }
  }
  return [...out.values()].slice(0, 12);
}

// ---- OpenAlex work resolution ----------------------------------------------

async function tryGet(url: string): Promise<Record<string, unknown> | null> {
  try {
    const j = asObj(await getJson(url));
    return Object.keys(j).length > 0 ? j : null;
  } catch {
    return null;
  }
}

async function resolveWork(seed: ScrapeSeed): Promise<Record<string, unknown> | null> {
  const mail = `mailto=${CONTACT}`;
  if (seed.openAlexId !== undefined) {
    const w = await tryGet(`https://api.openalex.org/works/${encodeURIComponent(seed.openAlexId)}?${mail}`);
    if (w !== null) return w;
  }
  if (seed.doi !== undefined && seed.doi !== '') {
    const w = await tryGet(`https://api.openalex.org/works/doi:${encodeURIComponent(seed.doi)}?${mail}`);
    if (w !== null) return w;
  }
  if (seed.arxivId !== undefined && seed.arxivId !== '') {
    // arXiv works carry the DOI 10.48550/arXiv.<id>; try that, then a search.
    const w = await tryGet(
      `https://api.openalex.org/works/doi:${encodeURIComponent(`10.48550/arXiv.${seed.arxivId}`)}?${mail}`,
    );
    if (w !== null) return w;
  }
  const title = seed.title;
  if (title !== undefined && title.trim() !== '') {
    const j = await tryGet(
      `https://api.openalex.org/works?search=${encodeURIComponent(title)}&per-page=1&${mail}`,
    );
    const results = j !== null && Array.isArray(j.results) ? (j.results as unknown[]) : [];
    if (results.length > 0) return asObj(results[0]);
  }
  return null;
}

async function fetchWorkLinks(ids: string[]): Promise<WorkLink[]> {
  const shorts = ids.map(shortId).filter((x): x is string => x !== undefined);
  if (shorts.length === 0) return [];
  const filter = `openalex_id:${shorts.join('|')}`;
  const url = `https://api.openalex.org/works?filter=${encodeURIComponent(filter)}&select=id,title,display_name,publication_year,doi,cited_by_count&per-page=${shorts.length}&mailto=${CONTACT}`;
  const j = await tryGet(url);
  const results = j !== null && Array.isArray(j.results) ? (j.results as unknown[]) : [];
  // Preserve the order the source listed its references in.
  const byId = new Map(results.map((r) => [asStr(asObj(r).id) ?? '', asObj(r)] as const));
  return ids
    .map((id) => byId.get(id))
    .filter((w): w is Record<string, unknown> => w !== undefined)
    .map(workLink);
}

async function fetchCitations(workId: string): Promise<WorkLink[]> {
  const short = shortId(workId);
  if (short === undefined) return [];
  const url = `https://api.openalex.org/works?filter=${encodeURIComponent(`cites:${short}`)}&sort=cited_by_count:desc&select=id,title,display_name,publication_year,doi&per-page=${CITE_SAMPLE}&mailto=${CONTACT}`;
  const j = await tryGet(url);
  const results = j !== null && Array.isArray(j.results) ? (j.results as unknown[]) : [];
  return results.map((r) => workLink(asObj(r)));
}

async function fetchJournal(sourceId: string | undefined): Promise<JournalMetrics | undefined> {
  if (sourceId === undefined) return undefined;
  const s = await tryGet(`https://api.openalex.org/sources/${encodeURIComponent(sourceId)}?mailto=${CONTACT}`);
  if (s === null) return undefined;
  const stats = asObj(s.summary_stats);
  const issn = Array.isArray(s.issn) ? (s.issn as unknown[]).filter((x): x is string => typeof x === 'string') : undefined;
  return {
    name: asStr(s.display_name),
    issn,
    twoYearMeanCitedness: asNum(stats['2yr_mean_citedness']),
    hIndex: asNum(stats.h_index),
    i10Index: asNum(stats.i10_index),
    worksCount: asNum(s.works_count),
    isOa: typeof s.is_oa === 'boolean' ? s.is_oa : undefined,
  };
}

/// Scrape rich metadata for a seed. Resolves the work on OpenAlex, then fans out
/// (references, citations, journal) concurrently. Returns `patch: null` when the
/// work can't be resolved at all.
export async function scrapeMetadata(seed: ScrapeSeed): Promise<ScrapeResult> {
  const work = await resolveWork(seed);
  if (work === null) return { patch: null, found: [] };

  const loc = asObj(work.primary_location);
  const source = asObj(loc.source);
  const oa = asObj(work.open_access);
  const ids = asObj(work.ids);
  const biblio = asObj(work.biblio);
  const referenced = Array.isArray(work.referenced_works)
    ? (work.referenced_works as unknown[]).filter((x): x is string => typeof x === 'string')
    : [];
  const abstract = abstractFromInverted(work.abstract_inverted_index) ?? seed.abstract;

  const authorships = Array.isArray(work.authorships) ? work.authorships : [];
  const authors = authorships
    .map((a) => asStr(asObj(asObj(a).author).display_name))
    .filter((n): n is string => n !== undefined);

  const topics = (Array.isArray(work.topics) ? work.topics : [])
    .map((tp) => asStr(asObj(tp).display_name))
    .filter((n): n is string => n !== undefined)
    .slice(0, 6);

  // Fan out the three graph queries; each degrades to [] / undefined on failure.
  const [references, citations, journal] = await Promise.all([
    fetchWorkLinks(referenced.slice(0, REF_SAMPLE)),
    fetchCitations(asStr(work.id) ?? ''),
    fetchJournal(shortId(asStr(source.id))),
  ]);

  const detailsAdd: Record<string, string> = {};
  const vol = asStr(biblio.volume);
  const issue = asStr(biblio.issue);
  const fp = asStr(biblio.first_page);
  const lp = asStr(biblio.last_page);
  if (vol !== undefined) detailsAdd.volume = vol;
  if (issue !== undefined) detailsAdd.issue = issue;
  if (fp !== undefined) detailsAdd.pages = lp !== undefined && lp !== fp ? `${fp}–${lp}` : fp;

  const pdfUrl = asStr(oa.oa_url) ?? asStr(loc.pdf_url);
  const resourceLinks = extractResourceLinks(abstract, asStr(loc.landing_page_url));

  const found: string[] = [];
  if (references.length > 0) found.push('references');
  if (citations.length > 0 || asNum(work.cited_by_count) !== undefined) found.push('citations');
  if (journal?.twoYearMeanCitedness !== undefined) found.push('metrics');
  if (resourceLinks.length > 0) found.push('code/data');

  const patch: ScrapePatch = {
    title: asStr(work.title) ?? asStr(work.display_name),
    authors: authors.length > 0 ? authors : undefined,
    year: asNum(work.publication_year),
    venue: asStr(source.display_name),
    doi: stripDoi(work.doi),
    abstract,
    pdfUrl,
    arxivId: asStr(ids.arxiv)?.replace(/^.*abs\//, ''),
    referenceCount: referenced.length > 0 ? referenced.length : undefined,
    citedByCount: asNum(work.cited_by_count),
    references: references.length > 0 ? references : undefined,
    citations: citations.length > 0 ? citations : undefined,
    journal,
    openAccess: {
      status: asStr(oa.oa_status),
      oaUrl: asStr(oa.oa_url),
      isOa: typeof oa.is_oa === 'boolean' ? oa.is_oa : undefined,
    },
    topics: topics.length > 0 ? topics : undefined,
    resourceLinks: resourceLinks.length > 0 ? resourceLinks : undefined,
    detailsAdd: Object.keys(detailsAdd).length > 0 ? detailsAdd : undefined,
    enrichedAt: Date.now(),
    enrichSource: 'OpenAlex',
  };
  return { patch, found, identifier: asStr(work.id) };
}

// ---- Identifier detection (for "add by identifier") ------------------------

/// Recognize a pasted DOI / arXiv id / OpenAlex id / URL so the Discover panel
/// can offer a one-click "add by identifier" instead of a keyword search.
export function detectIdentifier(raw: string): ScrapeSeed | null {
  const s = raw.trim();
  if (s === '') return null;
  const doiInUrl = s.match(/doi\.org\/(10\.\d{4,9}\/\S+)$/i);
  if (doiInUrl !== null) return { doi: doiInUrl[1] };
  if (/^10\.\d{4,9}\/\S+$/.test(s)) return { doi: s };
  const arxivUrl = s.match(/arxiv\.org\/(?:abs|pdf)\/(\d{4}\.\d{4,5})(v\d+)?/i);
  if (arxivUrl !== null) return { arxivId: arxivUrl[1] };
  if (/^\d{4}\.\d{4,5}(v\d+)?$/.test(s)) return { arxivId: s.replace(/v\d+$/, '') };
  if (/^https?:\/\/openalex\.org\/W\d+$/i.test(s) || /^W\d+$/.test(s)) {
    return { openAlexId: s.replace(/^https?:\/\/openalex\.org\//i, '') };
  }
  return null;
}
