import { loadJson, newId, saveJson, secretDelete, secretGet, secretSet } from './persist';

/// Hub profiles (parity Phase 3a) — the desktop analogue of the mobile
/// HubProfile list (lib/services/hub/hub_profiles.dart). Each profile's
/// non-secret fields (name, baseUrl, teamId) live in localStorage; its bearer
/// token lives in the OS keychain under `hub_token_<id>`. An active-profile
/// pointer drives which one the app binds on launch and shows in the switcher.

export interface HubProfile {
  id: string;
  name: string;
  baseUrl: string;
  teamId: string;
}

const LIST_KEY = 'hub_profiles';
const ACTIVE_KEY = 'hub_active_profile';

function tokenKey(id: string): string {
  return `hub_token_${id}`;
}

export function listProfiles(): HubProfile[] {
  return loadJson<HubProfile[]>(LIST_KEY, []);
}

export function getProfile(id: string): HubProfile | undefined {
  return listProfiles().find((p) => p.id === id);
}

export function getActiveProfileId(): string | null {
  return localStorage.getItem(ACTIVE_KEY);
}

export function setActiveProfileId(id: string | null): void {
  if (id !== null) localStorage.setItem(ACTIVE_KEY, id);
  else localStorage.removeItem(ACTIVE_KEY);
}

export function getActiveProfile(): HubProfile | undefined {
  const id = getActiveProfileId();
  return id !== null ? getProfile(id) : undefined;
}

/** Create or update a profile's metadata; returns the stored record. */
export function upsertProfile(input: { id?: string; name: string; baseUrl: string; teamId: string }): HubProfile {
  const list = listProfiles();
  const id = input.id ?? newId();
  const profile: HubProfile = { id, name: input.name, baseUrl: input.baseUrl, teamId: input.teamId };
  const next = list.some((p) => p.id === id) ? list.map((p) => (p.id === id ? profile : p)) : [...list, profile];
  saveJson(LIST_KEY, next);
  return profile;
}

export async function removeProfile(id: string): Promise<void> {
  saveJson(LIST_KEY, listProfiles().filter((p) => p.id !== id));
  if (getActiveProfileId() === id) setActiveProfileId(null);
  await secretDelete(tokenKey(id));
}

export function getToken(id: string): Promise<string | null> {
  return secretGet(tokenKey(id));
}

export function setToken(id: string, token: string): Promise<void> {
  return secretSet(tokenKey(id), token);
}
