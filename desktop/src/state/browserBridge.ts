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
///
/// W3: with the bridge on AND a hub context pushed, main registers as a hub
/// host and long-polls the reverse tunnel so REMOTE agents can drive the
/// browser (approval-gated hub-side). The Settings "Remote driving" block
/// reads those sessions via `refreshRemoteSessions` and revokes one via
/// `revokeRemote`.

const LS_KEY = 'termipod.browserBridge.enabled';
/// W3: the SEPARATE remote-driving consent (default off). The main toggle's
/// consent is "agents on this machine"; exposing the tabs to every agent in
/// the team through the hub is a different sentence, so it never rides the
/// main toggle silently.
const LS_REMOTE_KEY = 'termipod.browserBridge.remote';

function loadFlag(key: string): boolean {
  try {
    return localStorage.getItem(key) === '1';
  } catch {
    return false;
  }
}

function persistFlag(key: string, v: boolean): void {
  try {
    localStorage.setItem(key, v ? '1' : '0');
  } catch {
    /* private mode — the toggle still works for the session */
  }
}

const loadEnabled = (): boolean => loadFlag(LS_KEY);
const persistEnabled = (v: boolean): void => {
  persistFlag(LS_KEY, v);
};

/// One row of the main-side action audit ring (mirrors BridgeAuditEntry in
/// electron/src/browserbridge.ts). Args are redacted main-side.
export interface BridgeActionRow {
  ts: string;
  tool: string;
  agent_id: string;
  /// W3: 'local' (same-host spawn) or 'hub' (remote agent via the hub tunnel).
  via: 'local' | 'hub';
  agent_handle?: string;
  tab_id: number | null;
  url: string | null;
  partition: string | null;
  args: Record<string, unknown>;
  ok: boolean;
  error: string | null;
  hub?: 'ok' | 'failed' | 'skipped';
}

/// One row of the W3 "Remote driving" view (mirrors BridgeRemoteSession in
/// electron/src/browserbridge.ts): a remote agent that drove this desktop's
/// browser via the hub this app run.
export interface BridgeRemoteSessionRow {
  agent_id: string;
  agent_handle?: string;
  last_tool: string;
  last_ts: string;
  revoked: boolean;
}

interface BrowserBridgeState {
  /// The user's setting (persisted). Main may still be catching up — `running`
  /// is the server's actual state.
  enabled: boolean;
  /// W3: the separate remote-driving consent (persisted, default off) —
  /// gates the hub relay main-side; without it the desktop never registers
  /// a hosts row, so remote agents can't see it, read it, or drive it.
  remoteEnabled: boolean;
  /// The main-process MCP server is up (from browserbridge_status).
  running: boolean;
  /// The last-50 action ring (browserbridge_audit_tail), oldest-first.
  audit: BridgeActionRow[];
  /// W3: remote hub-driven sessions (browserbridge_remote_sessions),
  /// most-recent-first.
  remoteSessions: BridgeRemoteSessionRow[];
  setEnabled: (v: boolean) => void;
  setRemoteEnabled: (v: boolean) => void;
  refreshStatus: () => Promise<void>;
  refreshAudit: () => Promise<void>;
  refreshRemoteSessions: () => Promise<void>;
  revokeRemote: (agentId: string) => void;
}

export const useBrowserBridge = create<BrowserBridgeState>((set, get) => ({
  enabled: loadEnabled(),
  remoteEnabled: loadFlag(LS_REMOTE_KEY),
  running: false,
  audit: [],
  remoteSessions: [],
  setRemoteEnabled: (v) => {
    // Optimistic: main's browserbridge_set_remote only flips a flag and
    // reconciles the relay — there is no bind step to fail like the server.
    persistFlag(LS_REMOTE_KEY, v);
    set({ remoteEnabled: v });
    if (!isShell()) return;
    void invoke('browserbridge_set_remote', { enabled: v }).catch(() => undefined);
  },
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
  refreshRemoteSessions: async () => {
    if (!isShell()) return;
    try {
      const r = await invoke<{ sessions: BridgeRemoteSessionRow[] }>('browserbridge_remote_sessions');
      set({ remoteSessions: r.sessions });
    } catch {
      /* older main without W3 handlers — leave the list as-is */
    }
  },
  revokeRemote: (agentId) => {
    // Confirm-less: mark the row revoked immediately; the IPC result only
    // reports the hub-side grant clear, which the row doesn't display.
    set({ remoteSessions: get().remoteSessions.map((s) => (s.agent_id === agentId ? { ...s, revoked: true } : s)) });
    if (!isShell()) return;
    void invoke('browserbridge_revoke_remote', { agent_id: agentId }).catch(() => undefined);
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
  // W3: the remote-driving consent rides the same boot push. Only when on —
  // off is the main-side default; and only when the bridge itself is on
  // (the guard above), since the relay needs both.
  if (loadFlag(LS_REMOTE_KEY)) {
    void invoke('browserbridge_set_remote', { enabled: true }).catch(() => undefined);
  }
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
