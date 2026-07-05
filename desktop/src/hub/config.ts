/// Connection config for a hub. Mirrors HubConfig in hub_client.dart.
export interface HubConfig {
  /** e.g. https://hub.example.com (trailing slash tolerated). */
  baseUrl: string;
  teamId: string;
  token: string;
}

export const emptyConfig: HubConfig = { baseUrl: '', teamId: '', token: '' };

export function configComplete(c: HubConfig): boolean {
  return c.baseUrl.trim() !== '' && c.teamId.trim() !== '' && c.token.trim() !== '';
}
