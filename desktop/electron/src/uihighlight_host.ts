/// Agent pointing — Electron main-process half (D6 —
/// docs/plans/desktop-ui-context-and-pointing.md §3.4b). The rules live in the
/// electron-free uihighlight.ts; this module owns the two live parts:
///
///   - the per-agent rate-limit store (in-memory, per app run — a hub restart
///     or an app restart forgiving an agent is fine: the limit exists to stop
///     a loop, not to punish);
///   - delivery: the order is pushed to the renderer over the shell's event
///     channel, where AgentHighlightOverlay paints it and lets it expire.
///
/// Nothing here is stored and nothing is echoed back to the agent beyond
/// "drawn, for N seconds" — ADR-062 D-7: UI state is host-local and ephemeral.
import { setUiHighlightProvider } from './browserbridge_host';
import { isUiSharingEnabled } from './desktopui';
import { emit } from './events';
import { shellWebContents } from './uicapture_host';
import { decideHighlight, pruneHighlightHistory } from './uihighlight';
import type { UiHighlightRequest, UiHighlightResult } from './browserbridge';

/// The renderer event name. One channel, one payload shape (a HighlightOrder).
export const HIGHLIGHT_EVENT = 'desktopui_highlight';

/// agentId → recent highlight timestamps. Bounded by the prune on every use,
/// so a chatty agent cannot grow it either.
const history = new Map<string, number[]>();

let seq = 0;

async function highlight(req: UiHighlightRequest): Promise<UiHighlightResult> {
  if (!isUiSharingEnabled()) {
    return { ok: false, code: 'UI_UNAVAILABLE', message: 'UI context sharing is off on the desktop (Settings → Assistant)' };
  }
  const wc = shellWebContents();
  if (wc === null) {
    return { ok: false, code: 'NO_WINDOW', message: 'the desktop window is not open' };
  }
  const now = Date.now();
  const key = req.agentId !== '' ? req.agentId : 'unknown';
  const recent = pruneHighlightHistory(history.get(key) ?? [], now);
  seq += 1;
  const decision = decideHighlight({
    ref: req.ref,
    note: req.note,
    ttlMs: req.ttlMs,
    agentId: req.agentId,
    agentHandle: req.agentHandle,
    now,
    recent,
    id: `hl-${String(now)}-${String(seq)}`,
    iso: new Date(now).toISOString(),
  });
  if (!decision.ok) {
    // A refusal does NOT consume budget: a refused surface is the policy
    // table talking, and an unparseable ref is a mistake, neither of which is
    // the abuse the limit exists for.
    history.set(key, recent);
    return { ok: false, code: decision.code, message: decision.message };
  }
  history.set(key, [...recent, now]);
  emit(wc, HIGHLIGHT_EVENT, decision.order);
  return { ok: true, surface: decision.order.ref.surface, ttl_ms: decision.order.ttl_ms };
}

setUiHighlightProvider(highlight);
