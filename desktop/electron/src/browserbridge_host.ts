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
///   - W3 REMOTE DRIVING: while the bridge is enabled AND the separate
///     Remote-driving consent is on AND a hub context is set, the desktop
///     registers as a hub `hosts` row (capabilities.browser_bridge),
///     heartbeats every 10s, and long-polls the hub's A2A reverse tunnel —
///     incoming browser.invoke envelopes dispatch into the electron-free
///     core in-process and their results POST back. Settings → Remote
///     driving holds the consent toggle (default off), lists the remote
///     sessions (folded from the audit ring — hub-leg READS included) and
///     can revoke one (per-run set covering reads AND actions + best-effort
///     hub grant clear).
///
/// Off by default: no toggle, no server, no discovery file, no injection.
import { app, webContents, type WebContents } from 'electron';
import os from 'node:os';
import path from 'node:path';
import {
  ACTION_TOOLS,
  BridgeAuditRing,
  BridgeError,
  dispatchHubInvoke,
  foldRemoteSessions,
  mintToken,
  pruneSnapshotRefs,
  READ_TOOLS,
  removeBridgeDiscovery,
  shouldMirrorAudit,
  TUNNEL_KINDS,
  startBridgeServer,
  stripFragment,
  writeBridgeDiscovery,
  type BridgeAuditEntry,
  type BridgeBackend,
  type BridgeRemoteSession,
  type BridgeServer,
  type BridgeTarget,
  type HubInvokePayload,
  type HubInvokeResult,
  type McpServerDeps,
  type TunnelClass,
  type UiCaptureRequest,
  type UiCaptureResult,
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
  wc.once('destroyed', () => {
    attached.delete(wc.id);
    // Snapshot @eN refs are minted per tab; a destroyed tab's map would sit
    // in the core's ref store forever (every snapshot went through this
    // attach path, so this hook sees every tab that ever minted refs).
    pruneSnapshotRefs(wc.id);
  });
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
  // Hub-leg reads are ring-only (Settings visibility, no hub mirror — the
  // hub routed the call and already knows); everything else mirrors as W2.
  if (shouldMirrorAudit(entry)) void postBridgeAudit(entry);
}

/// D2 user→agent pointing (annotation_host.ts): a capture or kimi-composer
/// attach lands in the SAME ring (the Settings "recent bridge actions" view),
/// stamped `agent_id: 'user'` — the actor is the user's own gesture, so there
/// is no calling agent's stream to mirror to and the entry is ring-only. The
/// args carry the rect + target kind, never the image.
export function recordUserOverlayAudit(entry: Omit<BridgeAuditEntry, 'agent_id' | 'via'>): void {
  auditRing.push({ ...entry, agent_id: 'user', via: 'local' });
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
              via: entry.via,
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

// ── D1 desktop UI focus provider ─────────────────────────────────────────────
// desktopui.ts (the focus cache + sharing gate) plugs itself in here so this
// module never imports it back — the import edge stays one-directional
// (desktopui → browserbridge_host for stdioBridgePath).

export interface UiFocusProvider {
  /// The renderer's UI context sharing toggle — gates the tool's catalog
  /// presence on the bridge server.
  available: () => boolean;
  /// The last pushed (policy-projected) focus snapshot, or null.
  snapshot: () => Record<string, unknown> | null;
}

let uiFocusProvider: UiFocusProvider | null = null;

export function setUiFocusProvider(p: UiFocusProvider): void {
  uiFocusProvider = p;
}

/// D3: the gated screenshot provider (uicapture_host.ts), plugged in the same
/// way and for the same reason — it needs the hub context and the focus cache
/// this module and desktopui.ts own, so it registers instead of being imported.
type UiCaptureProvider = (req: UiCaptureRequest) => Promise<UiCaptureResult>;

let uiCaptureProvider: UiCaptureProvider | null = null;

export function setUiCaptureProvider(p: UiCaptureProvider): void {
  uiCaptureProvider = p;
}

/// The hub identity the audit mirror and the D3 approval card both post as.
/// Non-secret: the bearer is fetched from the keychain per use, never held
/// here (and never handed across IPC).
export function currentHubContext(): { baseUrl: string; teamId: string; profileId: string } | null {
  return hubContext;
}

// ── Enable/disable lifecycle ─────────────────────────────────────────────────

let server: BridgeServer | null = null;
/// The deps the HTTP server AND the W3 hub dispatch share — same backend,
/// same audit hook (recordAction → ring + hub mirror). Set at enable,
/// cleared at disable; the tunnel dispatch refuses while null.
let mcpDeps: McpServerDeps | null = null;
let enabled = false;

async function enable(): Promise<void> {
  if (server !== null) return;
  const token = mintToken();
  // W2: a second bearer for action scope. Minted (and discarded) with the
  // read token; it leaves the machine only through the 0o600 discovery file,
  // and the hostrunner hands it to opted-in spawns alone.
  const actionToken = mintToken();
  const version = app.getVersion();
  mcpDeps = {
    backend,
    serverInfo: { name: 'termipod-browser', version },
    onAction: recordAction,
    // D1: read live at call time — a toggle flip mid-run shows/hides
    // ui_get_focus on the next tools/list without a server restart.
    uiFocusAvailable: () => uiFocusProvider?.available() ?? false,
    getUiFocus: () => uiFocusProvider?.snapshot() ?? null,
    // D3: read through the holder on every call, so a provider registered
    // after the server started (module init order) is still reachable.
    captureUi: (req) =>
      uiCaptureProvider !== null
        ? uiCaptureProvider(req)
        : Promise.resolve<UiCaptureResult>({ ok: false, code: 'UI_UNAVAILABLE', message: 'the capture provider is not wired' }),
  };
  server = await startBridgeServer({ ...mcpDeps, token, actionToken });
  writeBridgeDiscovery(os.homedir(), {
    url: `http://127.0.0.1:${String(server.port)}/mcp`,
    token,
    action_token: actionToken,
    pid: process.pid,
    started_at: new Date().toISOString(),
    app_version: version,
    bridge_path: stdioBridgePath(),
  });
  // W3: register + heartbeat + tunnel poll when a hub session is around.
  syncHubRelay();
}

async function disable(): Promise<void> {
  const s = server;
  server = null;
  mcpDeps = null;
  stopHubRelay();
  removeBridgeDiscovery(os.homedir());
  if (s !== null) await s.close();
}

// ── W3 hub relay (remote driving through the reverse tunnel) ─────────────────
// While the bridge is enabled AND the Remote-driving consent is on AND a hub
// context is set, the desktop is a hub
// `hosts` row advertising capabilities.browser_bridge: it registers (upsert
// on (team,name) — safe to re-post on every enable), heartbeats every 10s
// (the hub sweep flips silent hosts offline; 10s matches the hostrunner
// cadence), and long-polls the A2A reverse tunnel for browser.invoke
// envelopes from remote agents. The bearer is the signed-in user's hub token
// read from the main-process keychain at registration time — never IPC. All
// of it dies on disable / hub-context change / quit; a hub or network outage
// just backs the poll off.

/// One live relay: the registration id, the heartbeat timer, and the abort
/// handle the poll loop + in-flight dispatch share.
interface HubRelay {
  ctx: BridgeHubContext;
  hostId: string;
  token: string;
  abort: AbortController;
  heartbeat: ReturnType<typeof setInterval>;
}

let hubRelay: HubRelay | null = null;
/// Bumped on every stop/start so a superseded async start abandons its
/// result instead of clobbering the newer relay.
let hubRelayGen = 0;

const TUNNEL_WAIT_MS = 25000;

function hubRelayKey(ctx: BridgeHubContext): string {
  return `${ctx.baseUrl}|${ctx.teamId}|${ctx.profileId}`;
}

/// W3 consent gate: remote driving is its OWN opt-in (Settings → Remote
/// driving, default off, renderer-persisted like the main toggle). The W1
/// toggle's consent sentence is "agents spawned on this machine may drive
/// my tabs"; exposing the same tabs to every agent in the team is a
/// different sentence and gets its own switch — reads route with no
/// approval card, so the exposure must never ride the local toggle
/// silently.
let remoteEnabled = false;

/// (Re)concile the relay with the current enabled + remote-consent +
/// hub-context state: start when all three are present, restart when the
/// profile changed, stop otherwise. Fire-and-forget — a registration
/// failure retries on the next sync, and the poll loop backs off on its
/// own.
function syncHubRelay(): void {
  const want = enabled && remoteEnabled && hubContext !== null ? hubContext : null;
  if (want !== null && hubRelay !== null && hubRelayKey(hubRelay.ctx) === hubRelayKey(want)) return;
  stopHubRelay();
  if (want !== null) void startHubRelay(want);
}

function stopHubRelay(): void {
  hubRelayGen += 1;
  const r = hubRelay;
  hubRelay = null;
  if (r === null) return;
  clearInterval(r.heartbeat);
  r.abort.abort();
}

async function startHubRelay(ctx: BridgeHubContext): Promise<void> {
  const gen = ++hubRelayGen;
  const token = await keychainGetLocal(`hub_token_${ctx.profileId}`);
  if (gen !== hubRelayGen) return;
  if (token === null || token === '') return; // signed out — local-relay-only mode
  try {
    const res = await fetch(`${ctx.baseUrl}/v1/teams/${encodeURIComponent(ctx.teamId)}/hosts`, {
      method: 'POST',
      headers: { 'content-type': 'application/json', authorization: `Bearer ${token}` },
      body: JSON.stringify({ name: hostRegistrationName(), capabilities: hostCapabilities() }),
      signal: AbortSignal.timeout(10000),
    });
    if (!res.ok) return;
    const body = (await res.json()) as { id?: unknown };
    if (typeof body.id !== 'string' || body.id === '' || gen !== hubRelayGen) return;
    const abort = new AbortController();
    const relay: HubRelay = {
      ctx,
      hostId: body.id,
      token,
      abort,
      heartbeat: setInterval(() => {
        void postHubHeartbeat(relay);
      }, 10000),
    };
    hubRelay = relay;
    void hubTunnelLoop(relay);
  } catch {
    /* hub unreachable — the next sync (context push / enable) retries */
  }
}

/// One heartbeat. Best-effort: a missed beat flips the host offline at the
/// hub's sweep and the next success flips it back.
async function postHubHeartbeat(relay: HubRelay): Promise<void> {
  try {
    await fetch(
      `${relay.ctx.baseUrl}/v1/teams/${encodeURIComponent(relay.ctx.teamId)}/hosts/${encodeURIComponent(relay.hostId)}/heartbeat`,
      { method: 'POST', headers: { authorization: `Bearer ${relay.token}` }, signal: AbortSignal.timeout(5000) },
    );
  } catch {
    /* best-effort */
  }
}

function hostRegistrationName(): string {
  return `desktop-${os.hostname()}`;
}

/// What this desktop advertises to the hub. `desktop_ui` (D5) tracks the
/// UI-context-sharing toggle, so a remote agent's hosts_list answer says
/// truthfully whether this desktop will talk about its own screen — the
/// toggle is not a refusal the agent discovers only on call.
function hostCapabilities(): Record<string, unknown> {
  return {
    browser_bridge: true,
    desktop_ui: uiFocusProvider?.available() ?? false,
    app_version: app.getVersion(),
    tools: READ_TOOLS.length + ACTION_TOOLS.length,
  };
}

/// Re-post the registration so a mid-run toggle flip is reflected in what the
/// hub advertises (the POST upserts on (team, name), which is why W3 could
/// re-post on every enable). Best-effort and idempotent: a failure just
/// leaves the previous capabilities until the next sync.
export function refreshHostCapabilities(): void {
  const relay = hubRelay;
  if (relay === null) return;
  void fetch(`${relay.ctx.baseUrl}/v1/teams/${encodeURIComponent(relay.ctx.teamId)}/hosts`, {
    method: 'POST',
    headers: { 'content-type': 'application/json', authorization: `Bearer ${relay.token}` },
    body: JSON.stringify({ name: hostRegistrationName(), capabilities: hostCapabilities() }),
    signal: AbortSignal.timeout(10000),
  }).catch(() => undefined);
}

/// Abortable sleep for the poll backoff.
function sleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const t = setTimeout(done, ms);
    function done(): void {
      signal.removeEventListener('abort', done);
      clearTimeout(t);
      resolve();
    }
    signal.addEventListener('abort', done, { once: true });
  });
}

/// The tunnel envelope JSON (hub tunnelRequest), narrowed to what W3 reads.
interface TunnelEnvelope {
  req_id?: string;
  kind?: string;
  payload?: unknown;
}

/// The reverse-tunnel poll: long-poll /a2a/tunnel/next, dispatch
/// browser.invoke envelopes in-process, POST the {ok,result|error} body back
/// base64'd. 204 → reconnect immediately; transport/5xx → backoff 1s
/// doubling to 15s. Runs until the relay's abort fires (disable / context
/// change / quit).
async function hubTunnelLoop(relay: HubRelay): Promise<void> {
  const base = `${relay.ctx.baseUrl}/v1/teams/${encodeURIComponent(relay.ctx.teamId)}/hosts/${encodeURIComponent(relay.hostId)}`;
  let backoff = 1000;
  while (!relay.abort.signal.aborted) {
    let env: TunnelEnvelope;
    try {
      const res = await fetch(`${base}/a2a/tunnel/next?wait_ms=${String(TUNNEL_WAIT_MS)}`, {
        headers: { authorization: `Bearer ${relay.token}` },
        signal: relay.abort.signal,
      });
      if (relay.abort.signal.aborted) return;
      if (res.status === 204) {
        backoff = 1000;
        continue;
      }
      if (!res.ok) throw new Error(`tunnel next: HTTP ${String(res.status)}`);
      env = (await res.json()) as TunnelEnvelope;
      backoff = 1000;
    } catch {
      if (relay.abort.signal.aborted) return;
      await sleep(backoff, relay.abort.signal);
      backoff = Math.min(backoff * 2, 15000);
      continue;
    }
    const reqId = typeof env.req_id === 'string' ? env.req_id : '';
    if (reqId === '') continue; // malformed envelope — nothing to answer to
    // One at a time, inline: the hub's enqueueAndWait already serializes per
    // desktop, and a slow navigate mustn't fan out into parallel CDP work. A
    // bad envelope answers {ok:false} — the loop never dies on dispatch.
    const out = await answerTunnelEnvelope(env);
    try {
      await fetch(`${base}/a2a/tunnel/responses`, {
        method: 'POST',
        headers: { 'content-type': 'application/json', authorization: `Bearer ${relay.token}` },
        body: JSON.stringify({ req_id: reqId, status: 200, body_b64: Buffer.from(JSON.stringify(out), 'utf8').toString('base64') }),
        signal: relay.abort.signal,
      });
    } catch {
      /* the hub-side waiter times out (60s) — nothing to retry against */
    }
  }
}

/// Route one envelope. Two kinds are ours (D5) — browser.invoke drives the
/// embedded tabs, desktop.invoke describes/captures the desktop's own UI —
/// and they share this dispatcher; anything else gets the contract's
/// unknown_kind error rather than killing the loop.
async function answerTunnelEnvelope(env: TunnelEnvelope): Promise<HubInvokeResult> {
  const cls = (Object.keys(TUNNEL_KINDS) as TunnelClass[]).find((k) => TUNNEL_KINDS[k] === env.kind);
  if (cls === undefined) return { ok: false, error: 'unknown_kind' };
  const deps = mcpDeps;
  if (deps === null) return { ok: false, error: 'bridge not running' };
  const p = (env.payload !== null && typeof env.payload === 'object' ? env.payload : {}) as Record<string, unknown>;
  const payload: HubInvokePayload = {
    tool: typeof p.tool === 'string' ? p.tool : '',
    args: p.args !== null && typeof p.args === 'object' && !Array.isArray(p.args) ? (p.args as Record<string, unknown>) : {},
    agent_id: typeof p.agent_id === 'string' ? p.agent_id : '',
    ...(typeof p.agent_handle === 'string' && p.agent_handle !== '' ? { agent_handle: p.agent_handle } : {}),
  };
  return dispatchHubInvoke(deps, payload, revokedAgents, cls);
}

/// W3: agent ids the user revoked from Settings → Remote driving. In-memory,
/// per app run — a restart re-allows (the longer-lived half is the hub-side
/// session grant, cleared best-effort by browserbridge_revoke_remote; that
/// dies with the hub process anyway).
const revokedAgents = new Set<string>();

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
  stopHubRelay();
  mcpDeps = null;
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
  /// W3 consent gate: remote driving (hub relay) is its own opt-in, pushed
  /// by the renderer like the main toggle (persisted there, default off).
  /// Off tears the relay down; the hosts row goes offline at the hub sweep.
  browserbridge_set_remote: async (args): Promise<{ remote: boolean }> => {
    remoteEnabled = args.enabled === true;
    syncHubRelay();
    return { remote: remoteEnabled };
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
      syncHubRelay(); // context gone — the W3 relay stops
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
      syncHubRelay(); // start (or restart against the new profile) when enabled
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
  /// W3: the Settings "Remote driving" view — one row per remote agent that
  /// ran an action call via the hub this app run, folded from the audit ring
  /// (electron-free foldRemoteSessions), most-recent-first.
  browserbridge_remote_sessions: async (): Promise<{ sessions: BridgeRemoteSession[] }> => ({
    sessions: foldRemoteSessions(auditRing.list(), revokedAgents),
  }),
  /// W3: revoke one remote agent. The local effect is immediate — the
  /// dispatch gate refuses its next action call ("revoked by user on
  /// desktop"); the hub-side session grant is cleared best-effort (same
  /// posture as the audit poster). No un-revoke: the set dies with the app
  /// run, the hub grant with the hub process.
  browserbridge_revoke_remote: async (args): Promise<{ ok: boolean; hub: 'ok' | 'failed' | 'skipped' }> => {
    const agentId = typeof args.agent_id === 'string' && args.agent_id !== '' ? args.agent_id : null;
    if (agentId === null) return { ok: false, hub: 'skipped' };
    revokedAgents.add(agentId);
    const ctx = hubContext;
    const hostId = hubRelay?.hostId ?? null;
    if (ctx === null || hostId === null) return { ok: true, hub: 'skipped' };
    try {
      const token = await keychainGetLocal(`hub_token_${ctx.profileId}`);
      if (token === null || token === '') return { ok: true, hub: 'skipped' };
      const res = await fetch(
        `${ctx.baseUrl}/v1/teams/${encodeURIComponent(ctx.teamId)}/hosts/${encodeURIComponent(hostId)}/browserbridge/revoke`,
        {
          method: 'POST',
          headers: { 'content-type': 'application/json', authorization: `Bearer ${token}` },
          body: JSON.stringify({ agent_id: agentId }),
          signal: AbortSignal.timeout(4000),
        },
      );
      return { ok: true, hub: res.ok ? 'ok' : 'failed' };
    } catch {
      return { ok: true, hub: 'failed' };
    }
  },
};
