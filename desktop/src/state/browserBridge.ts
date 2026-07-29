import { create } from 'zustand';
import { invoke } from '../bridge';
import { isShell } from '../platform';
import { useSession } from './session';

/// Agent browser bridge toggle (plan W1+W2 — docs/plans/desktop-agent-browser-bridge.md).
/// Whether MCP agents spawned on this host may drive the desktop's embedded
/// browser tabs through the main process's loopback MCP server. OFF BY
/// DEFAULT: no toggle, no server, no discovery file, no hostrunner injection.
///
/// The flag persists renderer-side in localStorage (device-local, like the
/// assistant dock side) and is PUSHED to the main process at boot
/// (`syncBrowserBridgeToMain`, main.tsx) and on every change — the same
/// pattern as the guest-menu label push: the renderer is the settings store,
/// main is the authority that runs the server + discovery file.
///
/// W2: the renderer also pushes the hub session's NON-secret identity
/// (`browserbridge_hub_context`) so the main-side audit mirror can post
/// action calls as hub agent_events — the bearer never crosses IPC (main
/// reads its own keychain). The Settings "recent bridge actions" view reads
/// the main-side ring via `refreshAudit`.

const LS_KEY = 'termipod.browserBridge.enabled';

function loadEnabled(): boolean {
  try {
    return localStorage.getItem(LS_KEY) === '1';
  } catch {
    return false;
  }
}

function persistEnabled(v: boolean): void {
  try {
    localStorage.setItem(LS_KEY, v ? '1' : '0');
  } catch {
    /* private mode — the toggle still works for the session */
  }
}

/// One row of the main-side action audit ring (mirrors BridgeAuditEntry in
/// electron/src/browserbridge.ts). Args are redacted main-side.
export interface BridgeActionRow {
  ts: string;
  tool: string;
  agent_id: string;
  tab_id: number | null;
  url: string | null;
  partition: string | null;
  args: Record<string, unknown>;
  ok: boolean;
  error: string | null;
  hub?: 'ok' | 'failed' | 'skipped';
}

interface BrowserBridgeState {
  /// The user's setting (persisted). Main may still be catching up — `running`
  /// is the server's actual state.
  enabled: boolean;
  /// The main-process MCP server is up (from browserbridge_status).
  running: boolean;
  /// The last-50 action ring (browserbridge_audit_tail), oldest-first.
  audit: BridgeActionRow[];
  setEnabled: (v: boolean) => void;
  refreshStatus: () => Promise<void>;
  refreshAudit: () => Promise<void>;
}

export const useBrowserBridge = create<BrowserBridgeState>((set) => ({
  enabled: loadEnabled(),
  running: false,
  audit: [],
  setEnabled: (v) => {
    persistEnabled(v);
    set({ enabled: v });
    if (!isShell()) return;
    invoke('browserbridge_set_enabled', { enabled: v })
      .then(() => {
        void useBrowserBridge.getState().refreshStatus();
      })
      .catch(() => {
        // Main refused (server failed to bind) — roll the setting back so the
        // UI never shows enabled over a dead bridge.
        persistEnabled(!v);
        set({ enabled: !v, running: false });
      });
  },
  refreshStatus: async () => {
    if (!isShell()) return;
    try {
      const s = await invoke<{ running: boolean }>('browserbridge_status');
      set({ running: s.running });
    } catch {
      /* pre-shell or handler absent (older main) — leave running as-is */
    }
  },
  refreshAudit: async () => {
    if (!isShell()) return;
    try {
      const r = await invoke<{ entries: BridgeActionRow[] }>('browserbridge_audit_tail');
      set({ audit: r.entries });
    } catch {
      /* older main without W2 handlers — leave the list as-is */
    }
  },
}));

/// Boot push (called from main.tsx): hand the persisted toggle to the main
/// process. Only pushes when ON — off is the default main-side, and skipping
/// the call keeps boot work at zero for the default path.
export function syncBrowserBridgeToMain(): void {
  if (!isShell() || !loadEnabled()) return;
  void invoke('browserbridge_set_enabled', { enabled: true })
    .then(() => useBrowserBridge.getState().refreshStatus())
    .catch(() => undefined);
}

/// Push the current hub session's non-secret identity (baseUrl/teamId/
/// profileId) to main so the W2 audit mirror can post action calls as hub
/// agent_events. Null when signed out — the mirror marks entries 'skipped'.
/// The bearer never crosses IPC; main reads `hub_token_<profileId>` from its
/// own keychain store at post time.
export function pushBridgeHubContext(): void {
  if (!isShell()) return;
  const { config, activeProfileId } = useSession.getState();
  const context =
    config.baseUrl !== '' && config.teamId !== '' && activeProfileId !== null
      ? { baseUrl: config.baseUrl, teamId: config.teamId, profileId: activeProfileId }
      : null;
  void invoke('browserbridge_hub_context', { context }).catch(() => undefined);
}

// Re-push on profile connect/switch/disconnect. The key dedupe keeps the
// subscribe cheap (it fires for every session-state change, most irrelevant).
let lastHubCtxKey = '';
useSession.subscribe((s) => {
  const key = `${s.config.baseUrl}|${s.config.teamId}|${s.activeProfileId ?? ''}`;
  if (key === lastHubCtxKey) return;
  lastHubCtxKey = key;
  pushBridgeHubContext();
});
