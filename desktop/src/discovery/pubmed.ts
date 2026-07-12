import { getJson } from './http';
import type { DiscoveryPaper } from './types';

/// PubMed — biomedical / life sciences via NCBI E-utilities. Two calls: esearch
/// (query → PMIDs) then esummary (PMIDs → metadata). Abstracts aren't in
/// esummary (they need efetch), so results carry title/authors/year/venue/DOI.

const EUTILS = 'https://eutils.ncbi.nlm.nih.gov/entrez/eutils';

function mapSummary(o: Record<string, unknown> | undefined): DiscoveryPaper | null {
  if (o === undefined) return null;
  const uid = typeof o.uid === 'string' ? o.uid : '';
  const ids = Array.isArray(o.articleids) ? o.articleids : [];
  const doi = ids
    .map((a) => a as Record<string, unknown>)
    .find((a) => a.idtype === 'doi' && typeof a.value === 'string')?.value as string | undefined;
  const yearMatch = typeof o.pubdate === 'string' ? o.pubdate.match(/\d{4}/) : null;
  const authors = (Array.isArray(o.authors) ? o.authors : [])
    .map((a) => (a as Record<string, unknown>).name)
    .filter((n): n is string => typeof n === 'string');
  const title = typeof o.title === 'string' ? o.title : '';
  return {
    paperId: uid !== '' ? uid : (doi ?? ''),
    title,
    authors,
    year: yearMatch ? parseInt(yearMatch[0], 10) : undefined,
    venue: typeof o.fulljournalname === 'string' ? o.fulljournalname : typeof o.source === 'string' ? o.source : undefined,
    doi,
    url: doi !== undefined ? `https://doi.org/${doi}` : uid !== '' ? `https://pubmed.ncbi.nlm.nih.gov/${uid}/` : undefined,
  };
}

export async function searchPubmed(query: string, limit: number): Promise<DiscoveryPaper[]> {
  const es = (await getJson(
    `${EUTILS}/esearch.fcgi?db=pubmed&term=${encodeURIComponent(query)}&retmax=${limit}&retmode=json`,
  )) as Record<string, unknown>;
  const ids = ((es.esearchresult as Record<string, unknown> | undefined)?.idlist ?? []) as string[];
  if (ids.length === 0) return [];
  const su = (await getJson(`${EUTILS}/esummary.fcgi?db=pubmed&id=${ids.join(',')}&retmode=json`)) as Record<
    string,
    unknown
  >;
  const result = (su.result ?? {}) as Record<string, unknown>;
  const uids = Array.isArray(result.uids) ? (result.uids as string[]) : ids;
  return uids
    .map((uid) => mapSummary(result[uid] as Record<string, unknown> | undefined))
    .filter((p): p is DiscoveryPaper => p !== null && p.title !== '');
}
