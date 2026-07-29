import { create } from 'zustand';
import { invoke } from '../bridge';
import { isShell } from '../platform';

/// Agent browser bridge toggle (plan W1 — docs/plans/desktop-agent-browser-bridge.md).
/// Whether MCP agents spawned on this host may drive the desktop's embedded
/// browser tabs through the main process's loopback MCP server. OFF BY
/// DEFAULT: no toggle, no server, no discovery file, no hostrunner injection.
///
/// The flag persists renderer-side in localStorage (device-local, like the
/// assistant dock side) and is PUSHED to the main process at boot
/// (`syncBrowserBridgeToMain`, main.tsx) and on every change — the same
/// pattern as the guest-menu label push: the renderer is the settings store,
/// main is the authority that runs the server + discovery file.

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

interface BrowserBridgeState {
  /// The user's setting (persisted). Main may still be catching up — `running`
  /// is the server's actual state.
  enabled: boolean;
  /// The main-process MCP server is up (from browserbridge_status).
  running: boolean;
  setEnabled: (v: boolean) => void;
  refreshStatus: () => Promise<void>;
}

export const useBrowserBridge = create<BrowserBridgeState>((set) => ({
  enabled: loadEnabled(),
  running: false,
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
