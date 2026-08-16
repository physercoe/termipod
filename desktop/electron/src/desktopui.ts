/// Desktop UI context — Electron main-process half (D1 —
/// docs/plans/desktop-ui-context-and-pointing.md §3.1/§3.2).
///
/// Three jobs:
///   - the FOCUS CACHE: the renderer publisher (state/uiContext.ts) pushes its
///     projected, policy-filtered snapshot over `desktopui_focus`; main holds
///     the last one and the bridge server serves it as `ui_get_focus` /
///     `ui://focus`. The cache never blocks a reader — before the first push
///     the tool answers an empty `{surface: null, captured_at: null}`;
///   - the SHARING GATE: the renderer's toggle (`desktopui_set_enabled`)
///     decides whether the tool/resource appears in the bridge's catalogs at
///     all (plan §3.2 layer 1: no toggle, no publisher, no tool);
///   - the user-level `~/.kimi-code/mcp.json` lifecycle: toggle-on (and the
///     per-start boot push, which re-fires set_enabled) refreshes the stable
///     relay copy and re-merges the additive `termipod-desktop` entry;
///     toggle-off removes just that entry. All file mechanics live in the
///     electron-free usermcp.ts.
///
/// D1 shipped LOCAL-ONLY; since D5 this module also re-posts the `desktop_ui`
/// capability to the hub when the toggle flips (refreshHostCapabilities), so
/// `hosts_list` answers truthfully — the tunnel routing itself lives in
/// browserbridge_host.ts.
import os from 'node:os';
import { authorLeases } from './author';
import { installStableRelay, mergeSharingEntries, removeSharingEntries, type ReseedResult } from './usermcp';
import { refreshHostCapabilities, setUiFocusProvider, stdioBridgePath } from './browserbridge_host';
import type { Handler } from './ipc/dispatch';

/// Whether the user turned UI context sharing on (Settings → Assistant).
/// Gates the tool's catalog presence AND the mcp.json entry — and, since the
/// renderer only publishes while on, the freshness of the cache. D2's
/// annotation handlers gate on the same flag (one toggle, no new one).
let sharingEnabled = false;

/// The D2 annotation orchestration (annotation_host.ts) gates every capture +
/// injection on the same toggle — off, no "Ask agent" button renderer-side and
/// no handler cooperation main-side.
export function isUiSharingEnabled(): boolean {
  return sharingEnabled;
}

/// The last pushed snapshot, verbatim. The renderer projects through
/// ui_policy.ts BEFORE pushing (ADR-062 D-1: the projection is the only view
/// that crosses IPC), so main's job is just to hold and serve it.
let focusCache: Record<string, unknown> | null = null;

/// D3's capture path reads the same cache to learn which surfaces are on
/// screen — the refusal decision must be made from the SAME view of the
/// screen the agent can already see, not from a second source of truth.
export function currentFocusSnapshot(): Record<string, unknown> | null {
  return sharingEnabled ? focusCache : null;
}

/// A runaway publisher can't fill memory: snapshots are ref-sized (a handful
/// of ids/paths); anything bigger is a bug, not a bigger allowance.
const MAX_SNAPSHOT_BYTES = 16384;

/// Reconcile every engine's user-level MCP entry + the stable relay copy with
/// the toggle. Never throws into the toggle path — a filesystem hiccup is
/// reported per engine, not fatal, and one engine's failure never stops another.
function reconcileSharing(next: boolean): ReseedResult | 'failed' {
  // e2e hermeticity (the Playwright suites set TERMIPOD_E2E): the reconcile
  // writes the REAL user configs — ~/.kimi-code/mcp.json, ~/.claude.json,
  // ~/.codex/config.toml — and ~/.termipod/bridge. An e2e instance must never
  // touch the user's files (the same gate pattern as the keychain migration
  // skip), and .claude.json especially is live account state. The sharing gate
  // itself still flips, so the overlay/capture paths run for real.
  if (process.env.TERMIPOD_E2E !== undefined) return { kimi: 'noop', claude: 'noop', codex: 'noop' };
  const home = os.homedir();
  try {
    if (next) {
      installStableRelay(home, stdioBridgePath());
      return mergeSharingEntries(home);
    }
    return removeSharingEntries(home);
  } catch (e) {
    console.warn('desktopui: user-MCP reconcile failed:', e instanceof Error ? e.message : e);
    return 'failed';
  }
}

export const desktopuiHandlers: Record<string, Handler> = {
  /// Settings → Assistant "UI context sharing" toggle (default off). On:
  /// publish the tool on the bridge + install the user-level mcp.json entry
  /// (also the per-app-start refresh — the renderer re-pushes at boot). Off:
  /// hide the tool, drop the cache, remove the entry.
  desktopui_set_enabled: async (args): Promise<{ enabled: boolean; mcp: ReseedResult | 'failed' }> => {
    sharingEnabled = args.enabled === true;
    if (!sharingEnabled) focusCache = null;
    // Coworking A3: toggle-off revokes every standing author lease. The toggle
    // is the consent the whole desktop-UI capability set hangs from, so it must
    // not leave "allow this document for this session" grants behind for the
    // next time it goes on — the user turned the capability off, not paused it.
    if (!sharingEnabled) authorLeases.clear();
    const mcp = reconcileSharing(sharingEnabled);
    // D5: `desktop_ui` in the hub host row tracks this toggle, so a remote
    // agent's hosts_list answer says truthfully whether this desktop will
    // talk about its own screen. No-op unless a hub relay is live.
    refreshHostCapabilities();
    return { enabled: sharingEnabled, mcp };
  },
  /// The renderer's focus push. Validated defensively (shape + size) — the
  /// content itself is the renderer's own UI state, already projected through
  /// the policy table renderer-side.
  desktopui_focus: async (args): Promise<{ ok: boolean }> => {
    // A push racing toggle-off must not refill the just-dropped cache — the
    // renderer only publishes while on, but the gate belongs on both sides.
    if (!sharingEnabled) return { ok: false };
    const s = args.snapshot;
    if (s === null || typeof s !== 'object' || Array.isArray(s)) return { ok: false };
    const surface = (s as Record<string, unknown>).surface;
    if (typeof surface !== 'string' || surface === '' || surface.length > 64) return { ok: false };
    let bytes = 0;
    try {
      bytes = JSON.stringify(s).length;
    } catch {
      return { ok: false };
    }
    if (bytes > MAX_SNAPSHOT_BYTES) return { ok: false };
    focusCache = s as Record<string, unknown>;
    return { ok: true };
  },
  /// Toggle state for the renderer. Never returns the snapshot itself — the
  /// renderer already has it; this answers "is the loop live".
  desktopui_status: async (): Promise<{ enabled: boolean; cached: boolean }> => ({
    enabled: sharingEnabled,
    cached: focusCache !== null,
  }),
};

// Feed the bridge server's `ui_get_focus` tool + `ui://focus` resource. The
// provider reads live flags, so a toggle flip mid-run shows/hides the tool on
// the next tools/list without a server restart.
setUiFocusProvider({
  available: () => sharingEnabled,
  snapshot: () => focusCache,
});
