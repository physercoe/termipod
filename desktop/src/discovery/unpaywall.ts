import { CONTACT, getJson } from './http';
import type { DiscoveryPaper } from './types';

/// Unpaywall — NOT a search engine: a DOI → open-access-PDF lookup. Used to
/// *enrich* results from the other sources: for a paper that has a DOI but no
/// PDF link, fill in the free full-text location when one exists.

export async function unpaywallPdf(doi: string): Promise<string | undefined> {
  if (doi === '') return undefined;
  try {
    const j = (await getJson(
      `https://api.unpaywall.org/v2/${encodeURIComponent(doi)}?email=${CONTACT}`,
      {},
      2,
    )) as Record<string, unknown>;
    const best = (j.best_oa_location ?? undefined) as Record<string, unknown> | undefined;
    if (best === undefined) return undefined;
    const pdf = typeof best.url_for_pdf === 'string' ? best.url_for_pdf : undefined;
    return pdf ?? (typeof best.url === 'string' ? best.url : undefined);
  } catch {
    return undefined;
  }
}

/// Backfill `pdfUrl` on results that have a DOI but no PDF, capped so a page of
/// results doesn't fan out into dozens of lookups. Mutates and returns `papers`.
export async function enrichWithUnpaywall(papers: DiscoveryPaper[], cap = 12): Promise<DiscoveryPaper[]> {
  const targets = papers.filter((p) => p.pdfUrl === undefined && p.doi !== undefined && p.doi !== '').slice(0, cap);
  await Promise.all(
    targets.map(async (p) => {
      const pdf = await unpaywallPdf(p.doi ?? '');
      if (pdf !== undefined) p.pdfUrl = pdf;
    }),
  );
  return papers;
}
