import { create } from 'zustand';
import { HubClient } from '../hub/client';
import { emptyConfig, type HubConfig } from '../hub/config';
import { clearCache } from './queryClient';
import { getActiveProfile, getProfile, getToken, setActiveProfileId, setToken, upsertProfile } from './profiles';

/// The active hub session (parity Phase 3a). A session is bound from a hub
/// profile: non-secret fields (baseUrl/teamId) come from the profile store, the
/// token from the OS keychain. Switching profiles re-binds the client and drops
/// the query cache (queryClient.ts) so one hub's snapshot never bleeds into
/// another's. `init()` auto-binds the active profile on launch.

interface SessionState {
  config: HubConfig;
  client: HubClient | null;
  activeProfileId: string | null;
  /** Bind a client to a config (internal). */
  bind: (config: HubConfig, profileId: string | null) => void;
  /** Create/update a profile, store its token, make it active, and connect. */
  connectProfile: (input: {
    id?: string;
    name: string;
    baseUrl: string;
    teamId: string;
    token: string;
  }) => Promise<void>;
  /** Switch to an existing profile (token from the keychain). */
  switchProfile: (id: string) => Promise<void>;
  disconnect: () => void;
  init: () => Promise<void>;
}

export const useSession = create<SessionState>((set, get) => ({
  config: emptyConfig,
  client: null,
  activeProfileId: null,

  bind: (config, profileId) => set({ config, client: new HubClient(config), activeProfileId: profileId }),

  connectProfile: async ({ id, name, baseUrl, teamId, token }) => {
    const profile = upsertProfile({ id, name, baseUrl, teamId });
    await setToken(profile.id, token);
    setActiveProfileId(profile.id);
    clearCache();
    get().bind({ baseUrl, teamId, token }, profile.id);
  },

  switchProfile: async (profileId) => {
    if (profileId === get().activeProfileId) return;
    const profile = getProfile(profileId);
    if (profile === undefined) return;
    const token = (await getToken(profileId)) ?? '';
    setActiveProfileId(profileId);
    clearCache();
    get().bind({ baseUrl: profile.baseUrl, teamId: profile.teamId, token }, profileId);
  },

  disconnect: () => {
    setActiveProfileId(null);
    clearCache();
    set({ client: null, activeProfileId: null, config: emptyConfig });
  },

  init: async () => {
    if (get().client !== null) return;
    const profile = getActiveProfile();
    if (profile === undefined) return;
    const token = await getToken(profile.id);
    if (token === null || token === '') return;
    get().bind({ baseUrl: profile.baseUrl, teamId: profile.teamId, token }, profile.id);
  },
}));
