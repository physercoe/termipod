import { secretGet } from './persist';

/// Fixed consolidated-keychain slot managed by Settings → Vault → TermiPod.
/// Keeping this out of localStorage prevents the SerpAPI credential from being
/// exposed alongside ordinary renderer preferences.
export const SERPAPI_KEY = 'termipod.discovery.serpapi.api_key';
export const X_BEARER_TOKEN = 'termipod.discovery.x.bearer_token';

export async function getSerpApiKey(): Promise<string> {
  return (await secretGet(SERPAPI_KEY))?.trim() ?? '';
}

export async function getXBearerToken(): Promise<string> {
  return (await secretGet(X_BEARER_TOKEN))?.trim() ?? '';
}
