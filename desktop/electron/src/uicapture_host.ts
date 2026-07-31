/// Gated desktop screenshot — Electron main-process half (D3 —
/// docs/plans/desktop-ui-context-and-pointing.md §3.3). The policy (which
/// surfaces refuse pixels, how a decision reads, what the card says) is the
/// electron-free uicapture.ts; this module owns the live halves:
///
///   - the CAPTURE: `capturePage` on the shell window or on one `<webview>`
///     guest, downscaled to the browser_screenshot discipline (PNG, ≤1568px
///     max edge) and returned as base64 — never written to disk, never
///     cached: the image exists for one tool result (ADR-062 D-7);
///   - the APPROVAL: a hub `desktop_action` attention_item raised per call,
///     polled until it resolves. The LOCAL leg raises it here; a hub-relayed
///     call arrives pre-approved (the hub raises the same card kind before it
///     routes — D5) and must not raise a second one.
///
/// Fail-closed everywhere: no hub to ask, no answer in the window, an
/// unreadable resolution, an unknown surface on screen — all deny. The tool
/// registration + audit live on the bridge server (browserbridge.ts); this
/// module plugs itself in through setUiCaptureProvider so the import edge
/// stays one-directional.
import { webContents, type BrowserWindow, type NativeImage, type WebContents } from 'electron';
import { fitWithin, MAX_IMAGE_EDGE } from './annotation';
import { currentHubContext, setUiCaptureProvider } from './browserbridge_host';
import { currentFocusSnapshot, isUiSharingEnabled } from './desktopui';
import { keychainGetLocal } from './ipc/keychain';
import {
  captureApprovalCard,
  captureDenialMessage,
  captureRefusal,
  readCaptureDecision,
  surfaceForPartition,
  visibleSurfaces,
  UI_CAPTURE_APPROVAL_TIMEOUT_MS,
  UI_CAPTURE_POLL_MS,
  type CaptureOutcome,
} from './uicapture';
import type { UiCaptureRequest, UiCaptureResult } from './browserbridge';

// ── The shell window ─────────────────────────────────────────────────────────

/// The app's own window, registered by main.ts at creation (and cleared on
/// close). Deliberately NOT discovered via `BrowserWindow.getAllWindows()`:
/// the kimi-web detached window is a BrowserWindow too, and "capture whatever
/// window we found first" is exactly the kind of ambient targeting a
/// screenshot tool must not have.
let shellWindow: BrowserWindow | null = null;

export function setShellWindow(win: BrowserWindow | null): void {
  shellWindow = win;
}

/// The shell's webContents, for main→renderer pushes (D6's highlight orders).
/// Null when the window is closed — a push with nowhere to land is a refusal,
/// not a crash.
export function shellWebContents(): WebContents | null {
  return shellWindow !== null && !shellWindow.isDestroyed() ? shellWindow.webContents : null;
}

// ── Guest resolution ─────────────────────────────────────────────────────────

/// The tabId was already validated against the bridge's live registry
/// (`requireTarget` in browserbridge.ts, so a guessed id can never name the
/// app:// shell); this only re-resolves it to a live webContents.
function liveGuest(tabId: number): WebContents | null {
  const wc = webContents.fromId(tabId);
  if (wc === undefined || wc.isDestroyed() || wc.getType() !== 'webview') return null;
  return wc;
}

// ── The hub approval round trip ──────────────────────────────────────────────

interface HubLeg {
  baseUrl: string;
  teamId: string;
  token: string;
}

/// The hub identity + bearer for the approval card, or null when the desktop
/// is signed out. Same sourcing as the audit mirror: non-secret context pushed
/// by the renderer, the token read from the main-process keychain at use time
/// (never over IPC).
async function hubLeg(): Promise<HubLeg | null> {
  const ctx = currentHubContext();
  if (ctx === null) return null;
  const token = await keychainGetLocal(`hub_token_${ctx.profileId}`);
  if (token === null || token === '') return null;
  return { baseUrl: ctx.baseUrl, teamId: ctx.teamId, token };
}

function teamUrl(leg: HubLeg, suffix: string): string {
  return `${leg.baseUrl}/v1/teams/${encodeURIComponent(leg.teamId)}${suffix}`;
}

/// Raise the per-call card. Returns its id, or null when the hub refused it —
/// which denies the capture (we never fall back to capturing unasked).
async function raiseCard(leg: HubLeg, summary: string, payload: Record<string, unknown>, agentHandle: string): Promise<string | null> {
  try {
    const res = await fetch(teamUrl(leg, '/attention'), {
      method: 'POST',
      headers: { 'content-type': 'application/json', authorization: `Bearer ${leg.token}` },
      body: JSON.stringify({
        scope_kind: 'team',
        scope_id: leg.teamId,
        kind: 'desktop_action',
        summary,
        severity: 'minor',
        actor_handle: agentHandle,
        pending_payload: payload,
      }),
      signal: AbortSignal.timeout(10_000),
    });
    if (!res.ok) return null;
    const body = (await res.json()) as { id?: unknown };
    return typeof body.id === 'string' && body.id !== '' ? body.id : null;
  } catch {
    return null;
  }
}

/// Poll one card until it resolves or the window closes. A transport error is
/// NOT a decision — it just costs one poll interval, and the deadline still
/// governs.
async function awaitCard(leg: HubLeg, id: string, deadline: number, now: () => number, sleep: (ms: number) => Promise<void>): Promise<CaptureOutcome> {
  for (;;) {
    if (now() >= deadline) return 'pending';
    await sleep(UI_CAPTURE_POLL_MS);
    let outcome: CaptureOutcome = 'pending';
    try {
      const res = await fetch(teamUrl(leg, `/attention/${encodeURIComponent(id)}`), {
        headers: { authorization: `Bearer ${leg.token}` },
        signal: AbortSignal.timeout(10_000),
      });
      if (res.ok) outcome = readCaptureDecision(await res.json());
    } catch {
      /* transport hiccup — try again inside the deadline */
    }
    if (outcome !== 'pending') return outcome;
  }
}

/// Best-effort tidy-up so an unanswered card does not loiter in the inbox
/// after the agent gave up. `desktop_action` owes no agent reply, so /resolve
/// (the dismiss path) is the right verb.
async function dismissCard(leg: HubLeg, id: string): Promise<void> {
  try {
    await fetch(teamUrl(leg, `/attention/${encodeURIComponent(id)}/resolve`), {
      method: 'POST',
      headers: { 'content-type': 'application/json', authorization: `Bearer ${leg.token}` },
      body: JSON.stringify({ resolved_by: 'desktop' }),
      signal: AbortSignal.timeout(5_000),
    });
  } catch {
    /* the row stays open; the director can dismiss it by hand */
  }
}

function sleepMs(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

// ── The capture ──────────────────────────────────────────────────────────────

function toPngBase64(source: NativeImage): { data_b64: string; width: number; height: number } {
  const size = source.getSize();
  const fit = fitWithin(size.width, size.height, MAX_IMAGE_EDGE);
  const image = fit.width !== size.width || fit.height !== size.height ? source.resize({ ...fit, quality: 'good' }) : source;
  return { data_b64: image.toPNG().toString('base64'), width: fit.width, height: fit.height };
}

function refused(surface: string): UiCaptureResult {
  return {
    ok: false,
    code: 'CAPTURE_REFUSED',
    message:
      surface === ''
        ? 'the desktop has not published what is on screen yet — a capture of an unclassified screen is refused'
        : `surface '${surface}' refuses pixel capture by policy (ui_policy.ts capture column) — ask for structure instead (ui_get_focus)`,
  };
}

async function capture(req: UiCaptureRequest): Promise<UiCaptureResult> {
  if (!isUiSharingEnabled()) {
    return { ok: false, code: 'UI_UNAVAILABLE', message: 'UI context sharing is off on the desktop (Settings → Assistant)' };
  }

  // 1. The table decides what may be captured at all — before any approval is
  //    asked for, so a refused surface never costs the user a card.
  const scope: 'window' | 'tab' = req.tabId === null ? 'window' : 'tab';
  let surfaces: string[];
  if (scope === 'tab') {
    const surface = surfaceForPartition(req.partition);
    if (surface === null) return refused(req.partition ?? '');
    surfaces = [surface];
  } else {
    const onScreen = visibleSurfaces(currentFocusSnapshot());
    if (onScreen === null) return refused('');
    surfaces = onScreen;
  }
  const blocked = captureRefusal(surfaces);
  if (blocked !== null) return refused(blocked);

  // 2. Per-call approval. A hub-relayed call already carries one (D5).
  if (req.via !== 'hub') {
    const leg = await hubLeg();
    if (leg === null) return { ok: false, code: 'UI_APPROVAL_UNAVAILABLE', message: captureDenialMessage('unavailable') };
    const card = captureApprovalCard({
      agentId: req.agentId,
      agentHandle: req.agentHandle,
      scope,
      surfaces,
      url: req.url,
    });
    const id = await raiseCard(leg, card.summary, card.payload, req.agentHandle);
    if (id === null) {
      return { ok: false, code: 'UI_APPROVAL_UNAVAILABLE', message: captureDenialMessage('unavailable') };
    }
    const outcome = await awaitCard(leg, id, Date.now() + UI_CAPTURE_APPROVAL_TIMEOUT_MS, () => Date.now(), sleepMs);
    if (outcome !== 'approve') {
      if (outcome === 'pending') void dismissCard(leg, id);
      return { ok: false, code: 'CAPTURE_DENIED', message: captureDenialMessage(outcome === 'pending' ? 'timeout' : 'denied') };
    }
  }

  // 3. Pixels. Re-checked after the park: the user may have moved to the vault
  //    while the card sat open, and the approval was for what was on screen
  //    when it was raised.
  if (scope === 'window') {
    const after = visibleSurfaces(currentFocusSnapshot());
    if (after === null) return refused('');
    const moved = captureRefusal(after);
    if (moved !== null) return refused(moved);
  }

  try {
    if (scope === 'tab' && req.tabId !== null) {
      const guest = liveGuest(req.tabId);
      if (guest === null) return { ok: false, code: 'TARGET_GONE', message: `tab ${String(req.tabId)} was destroyed` };
      return { ok: true, ...toPngBase64(await guest.capturePage()) };
    }
    if (shellWindow === null || shellWindow.isDestroyed()) {
      return { ok: false, code: 'NO_WINDOW', message: 'the desktop window is not open' };
    }
    return { ok: true, ...toPngBase64(await shellWindow.webContents.capturePage()) };
  } catch (e) {
    return { ok: false, code: 'CAPTURE_FAILED', message: e instanceof Error ? e.message : String(e) };
  }
}

setUiCaptureProvider(capture);
