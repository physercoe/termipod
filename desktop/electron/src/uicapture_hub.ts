/// The hub approval round trip for ui_screenshot (D3 —
/// docs/plans/desktop-ui-context-and-pointing.md §3.3), electron-free so
/// `node --test` can drive the raise → poll → dismiss sequencing against a
/// mocked fetch. The policy (card shape, decision reading, denial wording)
/// stays in uicapture.ts; the Electron halves (capturePage, the keychain
/// token) stay in uicapture_host.ts.
import {
  readCaptureDecision,
  UI_CAPTURE_APPROVAL_TIMEOUT_MS,
  UI_CAPTURE_POLL_MS,
  type CaptureOutcome,
} from './uicapture.ts';

export interface HubLeg {
  baseUrl: string;
  teamId: string;
  token: string;
}

export type FetchLike = (url: string, init?: RequestInit) => Promise<Response>;

export function teamUrl(leg: HubLeg, suffix: string): string {
  return `${leg.baseUrl}/v1/teams/${encodeURIComponent(leg.teamId)}${suffix}`;
}

/// Raise the per-call card. Returns its id, or null when the hub refused it —
/// which denies the capture (we never fall back to capturing unasked).
///
/// `actor_handle` names the requesting agent, but the hub honours the body
/// field only when the caller's token carries no handle of its own
/// (host-runner tokens — handleCreateAttention). This desktop's token belongs
/// to the signed-in user, so the ROW's actor stays the user — deliberately:
/// letting a user token attribute rows to arbitrary agents would be a
/// spoofing lever. The agent is named authoritatively in the summary and in
/// payload.agent_id.
export async function raiseCard(
  leg: HubLeg,
  summary: string,
  payload: Record<string, unknown>,
  agentHandle: string,
  fetchFn: FetchLike = fetch,
): Promise<string | null> {
  try {
    const res = await fetchFn(teamUrl(leg, '/attention'), {
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
export async function awaitCard(
  leg: HubLeg,
  id: string,
  deadline: number,
  now: () => number,
  sleep: (ms: number) => Promise<void>,
  fetchFn: FetchLike = fetch,
): Promise<CaptureOutcome> {
  for (;;) {
    if (now() >= deadline) return 'pending';
    await sleep(UI_CAPTURE_POLL_MS);
    let outcome: CaptureOutcome = 'pending';
    try {
      const res = await fetchFn(teamUrl(leg, `/attention/${encodeURIComponent(id)}`), {
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
///
/// The body deliberately names NO `resolved_by`: the hub column REFERENCES
/// agents(id) (NULLIF('') → NULL is the human/system-dismiss convention in
/// handleResolveAttention), and any non-agent value FK-violates — a 500 this
/// catch would swallow, leaving the card open forever. Pinned by test.
export async function dismissCard(leg: HubLeg, id: string, fetchFn: FetchLike = fetch): Promise<void> {
  try {
    await fetchFn(teamUrl(leg, `/attention/${encodeURIComponent(id)}/resolve`), {
      method: 'POST',
      headers: { 'content-type': 'application/json', authorization: `Bearer ${leg.token}` },
      body: JSON.stringify({}),
      signal: AbortSignal.timeout(5_000),
    });
  } catch {
    /* the row stays open; the director can dismiss it by hand */
  }
}

/// How one local capture's approval leg ends. `raise_failed` is a reachable
/// hub that refused/errored the card — a different diagnosis than "signed
/// out", which the caller checks before ever building a card.
export type ApprovalVerdict = 'approve' | 'denied' | 'timeout' | 'raise_failed';

export interface ApprovalDeps {
  now?: () => number;
  sleep?: (ms: number) => Promise<void>;
  fetchFn?: FetchLike;
  timeoutMs?: number;
}

function defaultSleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

/// The whole approval leg: raise the card, park on it, tidy up on timeout.
/// Every seam is injectable so the sequencing — including the FK-sensitive
/// dismiss body — is provable without Electron or a hub.
export async function approveViaCard(
  leg: HubLeg,
  card: { summary: string; payload: Record<string, unknown> },
  agentHandle: string,
  deps: ApprovalDeps = {},
): Promise<ApprovalVerdict> {
  const now = deps.now ?? (() => Date.now());
  const sleep = deps.sleep ?? defaultSleep;
  const fetchFn = deps.fetchFn ?? fetch;
  const id = await raiseCard(leg, card.summary, card.payload, agentHandle, fetchFn);
  if (id === null) return 'raise_failed';
  const outcome = await awaitCard(leg, id, now() + (deps.timeoutMs ?? UI_CAPTURE_APPROVAL_TIMEOUT_MS), now, sleep, fetchFn);
  if (outcome === 'approve') return 'approve';
  if (outcome === 'pending') {
    void dismissCard(leg, id, fetchFn);
    return 'timeout';
  }
  return 'denied';
}
