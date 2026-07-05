import { create } from 'zustand';
import { HubClient } from '../hub/client';
import { emptyConfig, type HubConfig } from '../hub/config';

const LS_KEY = 'termipod.hub';

// Persist non-secret connection fields (baseUrl, teamId) so a reconnect only
// needs the token re-pasted. The token stays in memory only (browser build).
function loadPersisted(): Pick<HubConfig, 'baseUrl' | 'teamId'> {
  try {
    const raw = localStorage.getItem(LS_KEY);
    if (raw) {
      const p = JSON.parse(raw) as Partial<HubConfig>;
      return { baseUrl: p.baseUrl ?? '', teamId: p.teamId ?? '' };
    }
  } catch {
    /* ignore */
  }
  return { baseUrl: '', teamId: '' };
}

interface SessionState {
  config: HubConfig;
  client: HubClient | null;
  connect: (config: HubConfig) => void;
  disconnect: () => void;
}

export const useSession = create<SessionState>((set) => ({
  config: { ...emptyConfig, ...loadPersisted() },
  client: null,
  connect: (config) => {
    try {
      localStorage.setItem(LS_KEY, JSON.stringify({ baseUrl: config.baseUrl, teamId: config.teamId }));
    } catch {
      /* ignore */
    }
    set({ config, client: new HubClient(config) });
  },
  disconnect: () => set({ client: null }),
}));
