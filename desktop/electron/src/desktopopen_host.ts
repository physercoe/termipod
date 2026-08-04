/// `desktop_open` — Electron main-process half (coworking lane H). The rules
/// live in the electron-free `desktopopen.ts`; this module owns the live parts:
///
///   - the per-agent rate-limit store (in-memory, per app run — the limit exists
///     to stop a loop, not to punish, so a restart forgiving an agent is fine);
///   - the ROUND TRIP. Unlike a highlight, which is fire-and-forget because the
///     answer is always "drawn", a navigation has to report how far the
///     reference actually resolved — and only the renderer knows, because only
///     the renderer has the stores. So the order goes down the event channel
///     with an id and this parks on the reply.
///
/// Fail-closed: sharing off, no window, no listener, no answer in the window —
/// all refuse, and a refusal here means nothing on the user's screen moved.
import { setDesktopOpenProvider } from './browserbridge_host';
import { isUiSharingEnabled } from './desktopui';
import { emit, hasSubscriber } from './events';
import { shellWebContents } from './uicapture_host';
import { decideOpen, openResultText, openUnresolvedMessage, pruneOpenHistory } from './desktopopen';
import type { DesktopOpenRequest, DesktopOpenResult } from './browserbridge';
import type { Handler } from './ipc/dispatch';

export const NAVIGATE_EVENT = 'desktopui_open';

/// How long the renderer has to perform one navigation. A focus is a few store
/// writes; anything past this is a wedged renderer, and the agent is told the
/// desktop did not answer rather than left hanging.
const RENDERER_TIMEOUT_MS = 8_000;

interface Pending {
  resolve: (depth: string) => void;
  timer: ReturnType<typeof setTimeout>;
}

const pending = new Map<string, Pending>();
const history = new Map<string, number[]>();
let seq = 0;

/// The renderer's reply. Strict about the id — an unknown one is a reply to a
/// request that already timed out, and resolving nothing is correct for it.
export const desktopOpenHandlers: Record<string, Handler> = {
  desktopui_open_result: (args): { ok: boolean } => {
    const id = typeof args.id === 'string' ? args.id : '';
    const p = pending.get(id);
    if (p === undefined) return { ok: false };
    pending.delete(id);
    clearTimeout(p.timer);
    p.resolve(typeof args.depth === 'string' ? args.depth : 'unknown');
    return { ok: true };
  },
};

async function open(req: DesktopOpenRequest): Promise<DesktopOpenResult> {
  if (!isUiSharingEnabled()) {
    return { ok: false, code: 'UI_UNAVAILABLE', message: 'UI context sharing is off on the desktop (Settings → Assistant)' };
  }
  const wc = shellWebContents();
  if (wc === null) return { ok: false, code: 'NO_WINDOW', message: 'the desktop window is not open' };
  // Checked BEFORE deciding: a push nobody is listening for would burn the whole
  // deadline and report a timeout, which reads as "the desktop is busy" rather
  // than "no workbench is up".
  if (!hasSubscriber(wc, NAVIGATE_EVENT)) {
    return { ok: false, code: 'NO_WINDOW', message: 'the desktop workbench is not answering — its window may be closed or still starting' };
  }

  const now = Date.now();
  const key = req.agentId !== '' ? req.agentId : 'unknown';
  const recent = pruneOpenHistory(history.get(key) ?? [], now);
  seq += 1;
  const id = `nav-${String(now)}-${String(seq)}`;
  const decision = decideOpen({
    ref: req.ref,
    note: req.note,
    agentId: req.agentId,
    agentHandle: req.agentHandle,
    now,
    recent,
    id,
    iso: new Date(now).toISOString(),
  });
  if (!decision.ok) {
    // A refusal does not consume budget — a refused surface is the policy table
    // talking and an unparseable ref is a mistake, neither of which is the abuse
    // the limit exists for.
    history.set(key, recent);
    return { ok: false, code: decision.code, message: decision.message };
  }

  const depth = await new Promise<string>((resolve) => {
    const timer = setTimeout(() => {
      pending.delete(id);
      resolve('timeout');
    }, RENDERER_TIMEOUT_MS);
    pending.set(id, { resolve, timer });
    emit(wc, NAVIGATE_EVENT, decision.order);
  });

  if (depth === 'timeout') {
    // Budget spent: the order WAS pushed, so the screen may well have moved.
    // Charging for it is the safe direction — the alternative lets a timing-out
    // desktop be navigated without limit.
    history.set(key, [...recent, now]);
    return { ok: false, code: 'NAVIGATE_TIMEOUT', message: 'the desktop did not confirm the navigation — the user may or may not have been moved' };
  }
  if (depth !== 'entity' && depth !== 'surface') {
    // The renderer resolved nothing, so nothing moved and nothing is charged.
    history.set(key, recent);
    return { ok: false, code: 'NAVIGATE_UNRESOLVED', message: openUnresolvedMessage(decision.order.ref) };
  }
  history.set(key, [...recent, now]);
  return { ok: true, surface: decision.order.ref.surface, depth, text: openResultText(decision.order.ref, depth) };
}

setDesktopOpenProvider(open);
