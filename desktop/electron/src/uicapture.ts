/// Gated desktop screenshot — electron-free core (D3 —
/// docs/plans/desktop-ui-context-and-pointing.md §3.3, ADR-062 D-4).
///
/// The visual representation of the desktop UI entity: pixels for the residue
/// structure cannot answer (a rendering bug, a layout question). It is the
/// most sensitive artifact the app can emit — a frame of everything the user
/// sees — so three rules bind every call, and all three are decided here:
///
///   1. REFUSAL BY TABLE. A capture is refused outright when any surface on
///      screen carries `capture: 'refuse'` in ui_policy.ts (vault, settings —
///      a pixel capture of settings shows its VALUES even though its snapshot
///      row emits only the surface id). An undeclared surface refuses too, and
///      so does an unknown screen (no focus snapshot yet): we refuse what we
///      cannot classify rather than capture it (ADR-062 D-3).
///   2. PER-CALL APPROVAL, ALWAYS. Never a session grant — §3.3 is explicit
///      that screenshots get no standing consent. The card is a hub
///      attention_item of kind `desktop_action`, so the decision lands in the
///      director's inbox on every client, and the local leg raises it itself
///      (the hub leg's card is raised hub-side — D5).
///   3. FAIL CLOSED. No decision inside the window, a dismissed row, a
///      resolution we cannot read: all deny.
///
/// The live halves (capturePage, the hub round trip) are uicapture_host.ts;
/// everything below is pure so `node --test` covers the policy without
/// Electron.
import { uiPolicyFor } from '../../src/state/ui_policy.ts';
import { KIMIWEB_PARTITION, RERUNWEB_PARTITION, WEBTAB_PARTITION } from './webtab_policy.ts';

// ── The refusal rule ─────────────────────────────────────────────────────────

/// Which ui_policy row governs pixels of a `<webview>` guest. A guest is not a
/// workbench job, but it is painted inside one, and the capture question is
/// the same question — so it is answered by the same table rather than by a
/// second rule living in the capture path (ADR-062 D-3: nothing else in the
/// pipeline makes policy decisions).
///
/// An unlisted partition maps to no row and therefore refuses: a future guest
/// kind opts in by naming its surface here, exactly as `webtab_policy` makes a
/// new partition state its `bridge` capability instead of inheriting one.
export function surfaceForPartition(partition: string | null): string | null {
  switch (partition) {
    case WEBTAB_PARTITION:
      return 'read';
    case KIMIWEB_PARTITION:
      return 'kimiweb';
    case RERUNWEB_PARTITION:
      return 'replay';
    default:
      return null;
  }
}

/// The surfaces a full-window capture would expose: the primary pane plus the
/// pinned one when a split is on screen. Read from the SAME projected focus
/// snapshot the agent can already see (desktopui.ts's cache) — no second
/// source of truth about what is on screen.
///
/// Returns null when there is no usable snapshot: the answer is "unknown",
/// which the caller must treat as refusal, not as "nothing sensitive".
///
/// INVARIANT this rests on: when the ACTIVE pane's row has an empty allowlist
/// (settings, vault), projectFocus rule 1 degrades the snapshot to
/// existence-only and DROPS `secondary` — so a hypothetical split with a
/// refusing surface active in the pinned pane would report only the primary
/// here, and "a split refuses if either pane refuses" would not hold. That
/// state is unreachable today because every `capture: 'refuse'` row is also
/// `splitEligible: false` (workbench.ts) — a refusing surface can only ever
/// be the primary, which rule 1 still reports as `surface`. If a refusing
/// surface ever becomes split-eligible, this helper must read the raw
/// (unprojected) focus state instead of the projection.
export function visibleSurfaces(snapshot: Record<string, unknown> | null): string[] | null {
  if (snapshot === null) return null;
  const primary = snapshot.surface;
  if (typeof primary !== 'string' || primary === '') return null;
  const out = [primary];
  const secondary = snapshot.secondary;
  if (secondary !== null && typeof secondary === 'object' && !Array.isArray(secondary)) {
    const s = (secondary as Record<string, unknown>).surface;
    if (typeof s === 'string' && s !== '') out.push(s);
  }
  return out;
}

/// The first surface that refuses pixels, or null when every one of them
/// allows. A surface with NO policy row refuses — the table is the allowlist,
/// so an undeclared surface is never capturable (the same posture the
/// projection takes in ui_policy.ts rule 1).
export function captureRefusal(surfaces: readonly string[]): string | null {
  for (const surface of surfaces) {
    if (uiPolicyFor(surface)?.capture !== 'allow') return surface;
  }
  return null;
}

// ── Arguments ────────────────────────────────────────────────────────────────

/// `ui_screenshot` takes at most a tabId: absent means the desktop window
/// itself, present means that `<webview>` guest (validated against the live
/// registry host-side — an id alone never reaches the app:// shell).
export interface UiScreenshotArgs {
  tabId: number | null;
}

export function parseScreenshotArgs(args: Record<string, unknown>): UiScreenshotArgs | { error: string } {
  const raw = args.tabId;
  if (raw === undefined || raw === null) return { tabId: null };
  if (typeof raw !== 'number' || !Number.isInteger(raw)) {
    return { error: 'tabId must be an integer from browser_list_tabs (omit it to capture the desktop window)' };
  }
  return { tabId: raw };
}

// ── The approval card ────────────────────────────────────────────────────────

/// How long the tool parks on the card before failing closed. Deliberately
/// SHORTER than the hub's 10-minute browser_action window: this leg is a
/// local MCP tool call held open over stdio, and an agent client that gives up
/// mid-park would leave the user approving a call nobody is waiting for. Two
/// minutes is long enough to notice the card, short enough that a stale one is
/// rare — and a denial is retryable, unlike a capture.
export const UI_CAPTURE_APPROVAL_TIMEOUT_MS = 120_000;

/// How often the local leg re-reads the card while parked.
export const UI_CAPTURE_POLL_MS = 1500;

export interface CaptureApprovalRequest {
  agentId: string;
  agentHandle: string;
  /// 'window' (the whole desktop) or 'tab' (one embedded guest).
  scope: 'window' | 'tab';
  /// The surfaces on screen, for the window scope; the guest URL for a tab.
  surfaces: readonly string[];
  url: string | null;
}

/// The card's human sentence + its machine payload. The payload carries a
/// UIRef-shaped description of WHAT was asked for — surfaces, tab, url — and
/// never a pixel: the approver decides from the reference, and the image only
/// exists after they say yes.
export function captureApprovalCard(req: CaptureApprovalRequest): { summary: string; payload: Record<string, unknown> } {
  const who = req.agentHandle !== '' ? req.agentHandle : req.agentId !== '' ? req.agentId : 'An agent';
  const what =
    req.scope === 'tab'
      ? `the embedded tab ${req.url ?? ''}`.trimEnd()
      : req.surfaces.length > 0
        ? `the desktop window (${req.surfaces.join(' + ')})`
        : 'the desktop window';
  return {
    summary: `${who} wants a screenshot of ${what}`,
    payload: {
      tool: 'ui_screenshot',
      scope: req.scope,
      surfaces: [...req.surfaces],
      url: req.url,
      agent_id: req.agentId,
      // Pinned in the payload so the card can never be re-read as a standing
      // grant: screenshots are per-call, always (plan §3.3).
      session_grant: false,
    },
  };
}

// ── Reading the decision back ────────────────────────────────────────────────

export type CaptureOutcome = 'approve' | 'deny' | 'pending';

/// Interpret one `GET /attention/{id}` row. `open` means keep waiting;
/// anything else resolves, and only an explicit trailing `approve` decision
/// approves — a row dismissed through /resolve (no decision at all), a reject,
/// or a shape we cannot parse all deny.
export function readCaptureDecision(row: unknown): CaptureOutcome {
  if (row === null || typeof row !== 'object' || Array.isArray(row)) return 'pending';
  const r = row as Record<string, unknown>;
  const status = typeof r.status === 'string' ? r.status : 'open';
  if (status === 'open') return 'pending';
  const decisions = r.decisions;
  if (!Array.isArray(decisions) || decisions.length === 0) return 'deny';
  const last = decisions[decisions.length - 1];
  if (last === null || typeof last !== 'object') return 'deny';
  const decision = (last as Record<string, unknown>).decision;
  return decision === 'approve' ? 'approve' : 'deny';
}

/// The denial sentence an agent sees, by cause. Kept here (not in the host) so
/// the wording is testable and identical on both legs.
export function captureDenialMessage(cause: 'denied' | 'timeout' | 'unavailable' | 'raise_failed'): string {
  switch (cause) {
    case 'denied':
      return 'the desktop user denied this screenshot';
    case 'timeout':
      return 'no decision on the screenshot request within the approval window — denied';
    case 'unavailable':
      return (
        'this desktop cannot ask for screenshot approval — it is not signed in to a hub, ' +
        'and ui_screenshot is per-call approved, always. Use ui_get_focus for structure instead'
      );
    case 'raise_failed':
      // Distinct from 'unavailable': the desktop IS signed in, but the hub
      // refused or errored the card — a retry may succeed, signing in won't.
      return 'the hub did not accept the approval card for this screenshot — denied (transient hub error; retrying may work)';
  }
}
