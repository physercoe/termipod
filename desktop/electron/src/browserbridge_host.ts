/// Agent browser bridge — Electron host wiring (plan W1; the electron-free
/// core, incl. the security rationale, is browserbridge.ts).
///
/// This module owns the live halves the core abstracts away:
///   - the target registry: `webContents.getAllWebContents()` filtered to
///     `<webview>` guests in an allowlisted partition whose policy marks it
///     bridge-capable (webtab_policy.ts `bridge`) — the app:// shell and any
///     non-allowlisted partition are unreachable BY CONSTRUCTION, because the
///     registry is the only way a tabId becomes a debugger target;
///   - CDP transport: one lazily-attached `webContents.debugger` session per
///     target, with a clear DEBUGGER_BUSY error instead of stealing when
///     devtools (or the user) already holds it;
///   - the enable/disable lifecycle: Settings → Assistant toggle → start the
///     loopback MCP server + write the discovery file the hostrunner reads at
///     spawn time; off (or quit) → server down + discovery file gone.
///
/// Off by default: no toggle, no server, no discovery file, no injection.
import { app, webContents, type WebContents } from 'electron';
import os from 'node:os';
import path from 'node:path';
import {
  BridgeError,
  mintToken,
  removeBridgeDiscovery,
  startBridgeServer,
  stripFragment,
  writeBridgeDiscovery,
  type BridgeBackend,
  type BridgeServer,
  type BridgeTarget,
} from './browserbridge';
import { policyForGuest } from './webtab';
import type { Handler } from './ipc/dispatch';

/// The stdio relay the hostrunner points engine MCP configs at. Packaged:
/// shipped as an electron-builder extraResource beside dist/; dev: the source
/// copy under desktop/electron/resources/.
export function stdioBridgePath(): string {
  return app.isPackaged
    ? path.join(process.resourcesPath, 'browser_bridge_stdio.mjs')
    : path.join(__dirname, '..', 'resources', 'browser_bridge_stdio.mjs');
}

// ── Registry + CDP backend ───────────────────────────────────────────────────

/// The live guest registry. Recomputed on every call — guests come and go
/// with renderer tabs, and a stale entry is a TARGET_GONE lying in wait (or
/// worse, a recycled id pointing at the wrong webContents).
function listTargets(): BridgeTarget[] {
  const out: BridgeTarget[] = [];
  for (const wc of webContents.getAllWebContents()) {
    if (wc.getType() !== 'webview') continue;
    const policy = policyForGuest(wc);
    if (policy === null || policy.bridge === 'none') continue;
    out.push({
      tabId: wc.id,
      url: stripFragment(wc.getURL()),
      title: wc.getTitle(),
      partition: policy.partition,
      bridge: policy.bridge,
    });
  }
  return out;
}

/// Lazily-attached debugger sessions, keyed by tabId. One per target; torn
/// down when the guest is destroyed or detaches (devtools close).
const attached = new Set<number>();

/// Resolve a tabId to a live, allowlisted guest — the SCOPE CHECK. The id
/// must be in the current registry; without this, `webContents.fromId` would
/// happily return the app:// shell to a caller that guessed its id.
function resolveGuest(tabId: number): WebContents {
  if (!listTargets().some((t) => t.tabId === tabId)) {
    throw new BridgeError('TARGET_GONE', `tab ${String(tabId)} is not a bridgeable guest tab`);
  }
  const wc = webContents.fromId(tabId);
  if (wc === undefined || wc.isDestroyed()) {
    attached.delete(tabId);
    throw new BridgeError('TARGET_GONE', `tab ${String(tabId)} was destroyed`);
  }
  return wc;
}

function ensureDebugger(wc: WebContents): void {
  if (attached.has(wc.id) && wc.debugger.isAttached()) return;
  try {
    wc.debugger.attach('1.3');
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e);
    // Another client (devtools, the user) holds the one allowed debugger
    // session — surface a clear error, never steal it.
    throw new BridgeError(
      msg.includes('Another debugger') || msg.includes('already attached') ? 'DEBUGGER_BUSY' : 'ATTACH_FAILED',
      msg,
    );
  }
  attached.add(wc.id);
  // once(): a re-attach after detach would otherwise stack duplicate listeners.
  wc.debugger.once('detach', () => attached.delete(wc.id));
  wc.once('destroyed', () => attached.delete(wc.id));
}

const backend: BridgeBackend = {
  listTargets,
  sendCommand: async (tabId, method, params) => {
    const wc = resolveGuest(tabId);
    ensureDebugger(wc);
    try {
      return await wc.debugger.sendCommand(method, params ?? {});
    } catch (e) {
      if (wc.isDestroyed()) {
        attached.delete(tabId);
        throw new BridgeError('TARGET_GONE', `tab ${String(tabId)} was destroyed mid-command`);
      }
      throw e;
    }
  },
};

// ── Enable/disable lifecycle ─────────────────────────────────────────────────

let server: BridgeServer | null = null;
let enabled = false;

async function enable(): Promise<void> {
  if (server !== null) return;
  const token = mintToken();
  const version = app.getVersion();
  server = await startBridgeServer({
    backend,
    token,
    serverInfo: { name: 'termipod-browser', version },
  });
  writeBridgeDiscovery(os.homedir(), {
    url: `http://127.0.0.1:${String(server.port)}/mcp`,
    token,
    pid: process.pid,
    started_at: new Date().toISOString(),
    app_version: version,
    bridge_path: stdioBridgePath(),
  });
}

async function disable(): Promise<void> {
  const s = server;
  server = null;
  removeBridgeDiscovery(os.homedir());
  if (s !== null) await s.close();
}

/// The Settings toggle's reach. Idempotent; failures roll the flag back so
/// the UI can't show enabled over a dead server.
export async function setBrowserBridgeEnabled(next: boolean): Promise<void> {
  enabled = next;
  try {
    if (next) await enable();
    else await disable();
  } catch (e) {
    enabled = server !== null;
    throw e;
  }
}

/// before-quit (main.ts, next to disposeKimiWeb): stop accepting + delete the
/// discovery file so the hostrunner's next spawn stops injecting. Sync on
/// purpose — the process is going down; in-flight requests die with it.
export function disposeBrowserBridge(): void {
  enabled = false;
  removeBridgeDiscovery(os.homedir());
  const s = server;
  server = null;
  if (s !== null) void s.close();
}

export const browserBridgeHandlers: Record<string, Handler> = {
  /// Settings → Assistant "agent browser bridge" toggle. On: start the
  /// loopback MCP server + write ~/.termipod/browser-bridge.json. Off: tear
  /// both down. Default off — nothing runs until the user opts in.
  browserbridge_set_enabled: async (args): Promise<{ enabled: boolean; running: boolean }> => {
    await setBrowserBridgeEnabled(args.enabled === true);
    return { enabled, running: server !== null };
  },
  /// Toggle state for the Settings page. NEVER returns the token or the
  /// discovery contents — those cross only via the 0o600 file.
  browserbridge_status: async (): Promise<{ enabled: boolean; running: boolean; port: number | null }> => ({
    enabled,
    running: server !== null,
    port: server?.port ?? null,
  }),
};
