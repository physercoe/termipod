import { getSerpApiKey } from '../state/discoverySecrets';
import { invoke } from '../bridge';
import { isShell } from '../platform';
import { proxyForConnection } from '../state/proxy';
import { normalizeSerpApiCitationPage, normalizeSerpApiPaper } from './serpApiCore';
import type { DiscoveryPaper, ScholarCitationPage } from './types';

export { normalizeSerpApiCitationPage, normalizeSerpApiPaper, serpApiCitationsUrl, serpApiSearchUrl } from './serpApiCore';

/// Google Scholar results through SerpAPI. The credential is read on demand
/// from Vault → TermiPod, so replacing it takes effect on the next search.
export async function searchGoogleScholar(query: string, limit: number): Promise<DiscoveryPaper[]> {
  const key = await getSerpApiKey();
  if (key === '') throw new Error('needs-key');
  // SerpAPI intentionally does not allow browser-origin requests. Route through
  // the native shell so the key never appears in a renderer fetch URL and the
  // app's Discovery proxy setting is applied.
  if (!isShell()) throw new Error('serpapi-shell-required');
  const json = await invoke<Record<string, unknown>>('serpapi_search', {
    query,
    limit,
    apiKey: key,
    proxy: proxyForConnection('discovery') ?? null,
  });
  if (typeof json.error === 'string' && json.error !== '') throw new Error('serpapi-error');
  const results = json.organic_results;
  if (!Array.isArray(results)) return [];
  return results.map(normalizeSerpApiPaper).filter((paper): paper is DiscoveryPaper => paper !== null);
}

/// Fetch one page of papers that cite a Scholar result. This intentionally
/// requires an explicit Cite-tab action because every page consumes one SerpAPI
/// query. The key remains in the native request and is never persisted in the
/// reference record or exposed in a renderer URL.
export async function loadGoogleScholarCitations(
  citesId: string,
  start = 0,
  limit = 20,
): Promise<ScholarCitationPage> {
  const key = await getSerpApiKey();
  if (key === '') throw new Error('needs-key');
  if (!isShell()) throw new Error('serpapi-shell-required');
  const json = await invoke<Record<string, unknown>>('serpapi_citations', {
    citesId,
    start,
    limit,
    apiKey: key,
    proxy: proxyForConnection('discovery') ?? null,
  });
  if (typeof json.error === 'string' && json.error !== '') throw new Error('serpapi-error');
  return normalizeSerpApiCitationPage(json, limit, start);
}
