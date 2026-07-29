/// Agent browser bridge — Electron host wiring (plan W1+W2; the electron-free
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
///     spawn time; off (or quit) → server down + discovery file gone. Two
///     bearers are minted per run (read + action); the action token reaches
///     only spawns whose spec opts in (`browser_bridge: true`, plan W2);
///   - the W2 audit trail: every action call lands in an in-memory ring (the
///     Settings "recent bridge actions" view) and is mirrored best-effort to
///     the hub as a `browser_bridge` agent_events row on the CALLING agent's
///     stream (producer 'system') — the agent id arrives per request via the
///     relay's x-tp-agent-id header, the hub context (baseUrl/teamId/profile)
///     is pushed renderer-side, and the bearer is read from the main-process
///     keychain store at post time (never via IPC);
///   - the "borrowed tab": the renderer reports the visible browser guest's
///     webContents id (`browserbridge_set_active_guest`) so W2's
///     `browser_find_tab {active:true}` resolves the tab the user is watching.
///
/// Off by default: no toggle, no server, no discovery file, no injection.
import { app, webContents, type WebContents } from 'electron';
import os from 'node:os';
import path from 'node:path';
import {
  BridgeAuditRing,
  BridgeError,
  mintToken,
  removeBridgeDiscovery,
  startBridgeServer,
  stripFragment,
  writeBridgeDiscovery,
  type BridgeAuditEntry,
  type BridgeBackend,
  type BridgeServer,
  type BridgeTarget,
} from './browserbridge';
import { policyForGuest } from './webtab';
import { keychainGetLocal } from './ipc/keychain';
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

/// The guest the user is currently viewing, reported renderer-side
/// (`browserbridge_set_active_guest`). Validated against the live registry on
/// every read — a destroyed tab never stays "active".
let activeGuestId: number | null = null;

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
  activeTabId: () => {
    if (activeGuestId !== null && listTargets().some((t) => t.tabId === activeGuestId)) return activeGuestId;
    return null;
  },
};

// ── Audit trail (W2) ─────────────────────────────────────────────────────────

/// The Settings "recent bridge actions" view's data: last 50 action calls,
/// in-memory only. Filled by the core's onAction hook.
const auditRing = new BridgeAuditRing(50);

/// The hub identity the audit mirror posts as: non-secret fields pushed by
/// the renderer (its session store owns them), the bearer read from the
/// main-process keychain at post time. In-memory only; cleared on disable.
interface BridgeHubContext {
  baseUrl: string;
  teamId: string;
  profileId: string;
}
let hubContext: BridgeHubContext | null = null;

function recordAction(entry: BridgeAuditEntry): void {
  auditRing.push(entry);
  void postBridgeAudit(entry);
}

/// Best-effort mirror of one action call onto the CALLING agent's hub event
/// stream (kind `browser_bridge`, producer 'system' — the desktop bridge is
/// neither the agent nor the user). Never blocks or fails the tool call: no
/// hub context, an unknown agent, a missing token, or a network error just
/// marks the ring entry. The payload carries the redacted args — typed text
/// was cut by the core before this point.
async function postBridgeAudit(entry: BridgeAuditEntry): Promise<void> {
  const ctx = hubContext;
  if (ctx === null || entry.agent_id === 'unknown') {
    entry.hub = 'skipped';
    return;
  }
  try {
    const token = await keychainGetLocal(`hub_token_${ctx.profileId}`);
    if (token === null || token === '') {
      entry.hub = 'skipped';
      return;
    }
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 4000);
    try {
      const res = await fetch(
        `${ctx.baseUrl}/v1/teams/${encodeURIComponent(ctx.teamId)}/agents/${encodeURIComponent(entry.agent_id)}/events`,
        {
          method: 'POST',
          headers: { 'content-type': 'application/json', authorization: `Bearer ${token}` },
          body: JSON.stringify({
            kind: 'browser_bridge',
            producer: 'system',
            payload: {
              ts: entry.ts,
              tool: entry.tool,
              agent_id: entry.agent_id,
              tab_id: entry.tab_id,
              url: entry.url,
              partition: entry.partition,
              args: entry.args,
              ok: entry.ok,
              error: entry.error,
            },
          }),
          signal: controller.signal,
        },
      );
      entry.hub = res.ok ? 'ok' : 'failed';
    } finally {
      clearTimeout(timer);
    }
  } catch {
    entry.hub = 'failed';
  }
}

// ── Enable/disable lifecycle ─────────────────────────────────────────────────

let server: BridgeServer | null = null;
let enabled = false;

async function enable(): Promise<void> {
  if (server !== null) return;
  const token = mintToken();
  // W2: a second bearer for action scope. Minted (and discarded) with the
  // read token; it leaves the machine only through the 0o600 discovery file,
  // and the hostrunner hands it to opted-in spawns alone.
  const actionToken = mintToken();
  const version = app.getVersion();
  server = await startBridgeServer({
    backend,
    token,
    actionToken,
    serverInfo: { name: 'termipod-browser', version },
    onAction: recordAction,
  });
  writeBridgeDiscovery(os.homedir(), {
    url: `http://127.0.0.1:${String(server.port)}/mcp`,
    token,
    action_token: actionToken,
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
  /// The renderer pushes its hub session's non-secret fields (baseUrl/teamId/
  /// profileId) so the audit mirror can post agent_events as the signed-in
  /// user. The bearer never crosses IPC — it's read from the main-process
  /// keychain (`hub_token_<profileId>`) at post time. Null clears (logout /
  /// disconnect). Validated as plain strings; bogus values just make the
  /// mirror fail (marked on the ring entry).
  browserbridge_hub_context: async (args): Promise<{ ok: boolean }> => {
    if (args.context === null) {
      hubContext = null;
      return { ok: true };
    }
    const c = args.context as Record<string, unknown> | undefined;
    if (
      c !== undefined &&
      typeof c.baseUrl === 'string' &&
      typeof c.teamId === 'string' &&
      typeof c.profileId === 'string' &&
      c.baseUrl.startsWith('http')
    ) {
      hubContext = { baseUrl: c.baseUrl.replace(/\/+$/, ''), teamId: c.teamId, profileId: c.profileId };
      return { ok: true };
    }
    return { ok: false };
  },
  /// The renderer reports the browser guest the user is currently viewing
  /// (its webContents id) — W2's `browser_find_tab {active:true}`. A stale or
  /// foreign id is rejected; the registry is the only source of valid ids.
  browserbridge_set_active_guest: async (args): Promise<{ ok: boolean }> => {
    const id = args.id;
    if (typeof id !== 'number' || !Number.isInteger(id)) return { ok: false };
    activeGuestId = listTargets().some((t) => t.tabId === id) ? id : null;
    return { ok: activeGuestId !== null };
  },
  /// The Settings "recent bridge actions" view (W2): the in-memory ring,
  /// oldest-first. Entries' `hub` field fills in asynchronously as the mirror
  /// post resolves (skipped/ok/failed).
  browserbridge_audit_tail: async (): Promise<{ entries: BridgeAuditEntry[] }> => ({ entries: auditRing.list() }),
};
