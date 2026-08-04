/// The `author_*` bridge — Electron main-process half (coworking A2/A3;
/// ADR-064). The judgement lives in the electron-free `author.ts`; this module
/// owns the three live parts:
///
///   - the RENDERER ROUND TRIP. The documents, their parsers and their editors
///     are all in the renderer, and main has no `DOMParser` — so a request
///     goes down the `bridge:event` channel with a correlation id and parks on
///     the renderer's `desktopui_author_result` reply. The subscription is
///     checked BEFORE parking: a push with nobody listening would otherwise
///     burn the whole deadline and report a timeout, which reads to the agent
///     as "the desktop is busy" rather than "no workbench is up";
///   - the APPROVAL. `author_apply` names the document on a hub
///     `desktop_action` card and parks on the answer. "Allow this document for
///     this session" is a lease held HERE, per (agent, document);
///   - the ORDER. Resolve the target first, card second, write third. The card
///     has to name the document it is about, and the document is a fact only
///     the renderer holds — so the pre-flight `resolve` returns the target's
///     identity WITHOUT its body: naming a document must not disclose it.
///
/// Fail-closed everywhere: sharing off, no window, no listener, no hub to ask,
/// no answer in the window, an unreadable reply — all refuse, and none of them
/// write.
import { setAuthorBridgeProvider, currentHubContext } from './browserbridge_host';
import { isUiSharingEnabled } from './desktopui';
import { emit, hasSubscriber } from './events';
import { keychainGetLocal } from './ipc/keychain';
import { shellWebContents } from './uicapture_host';
import { approveViaCardWithOption, type HubLeg } from './uicapture_hub';
import {
  applyResultText,
  authorApprovalCard,
  authorDenialMessage,
  authorLeases,
  documentIndexText,
  type AuthorApplyState,
} from './author';
import type { AuthorBridgeRequest, AuthorBridgeResult } from './browserbridge';
import type { Handler } from './ipc/dispatch';
import { diagramOpsBytes, type DiagramOperation } from '../../src/state/drawioOps.ts';

/// Main → renderer requests; renderer → main replies. Mirrors
/// `src/state/authorBridge.ts`, which is the other end of both.
export const AUTHOR_REQUEST_EVENT = 'desktopui_author_request';

/// How long the renderer has to answer one request. Generous for a store read
/// plus a parse, short enough that a wedged renderer does not hold the agent's
/// tool call open. The APPROVAL wait is not inside this window — it happens
/// between two renderer calls, on the card's own (much longer) deadline.
const RENDERER_TIMEOUT_MS = 15_000;

interface Pending {
  resolve: (value: unknown) => void;
  timer: ReturnType<typeof setTimeout>;
}

const pending = new Map<string, Pending>();
let seq = 0;

/// The renderer's reply. Deliberately tolerant about the RESULT (the renderer
/// is the authority on its own shape and this module only forwards it) and
/// strict about the ID: an unknown id is a reply to a request that already
/// timed out, and resolving nothing is the correct response to it.
export const authorHostHandlers: Record<string, Handler> = {
  desktopui_author_result: (args): { ok: boolean } => {
    const id = typeof args.id === 'string' ? args.id : '';
    const p = pending.get(id);
    if (p === undefined) return { ok: false };
    pending.delete(id);
    clearTimeout(p.timer);
    p.resolve(args.result);
    return { ok: true };
  },
};

interface RendererRequest {
  op: 'read' | 'resolve' | 'apply' | 'render';
  documentId: string | null;
  mode?: string;
  body?: string;
  operations?: readonly DiagramOperation[];
  format?: string;
  reason?: string;
  by?: string;
}

type RendererReply = Record<string, unknown> | null;

/// One round trip. `null` means the renderer never answered — the caller turns
/// that into a refusal, never into a "maybe it worked".
async function askRenderer(req: RendererRequest): Promise<RendererReply> {
  const wc = shellWebContents();
  if (wc === null) return null;
  if (!hasSubscriber(wc, AUTHOR_REQUEST_EVENT)) return null;
  seq += 1;
  const id = `ab-${String(Date.now())}-${String(seq)}`;
  const answer = new Promise<unknown>((resolve) => {
    const timer = setTimeout(() => {
      pending.delete(id);
      resolve(null);
    }, RENDERER_TIMEOUT_MS);
    pending.set(id, { resolve, timer });
  });
  emit(wc, AUTHOR_REQUEST_EVENT, {
    id,
    op: req.op,
    document_id: req.documentId,
    mode: req.mode ?? 'replace',
    body: req.body ?? '',
    operations: req.operations ?? [],
    format: req.format ?? 'svg',
    reason: req.reason ?? '',
    by: req.by ?? '',
  });
  const value = await answer;
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? (value as Record<string, unknown>) : null;
}

function str(v: unknown, fallback = ''): string {
  return typeof v === 'string' && v !== '' ? v : fallback;
}

/// Turn a renderer refusal into ours. The renderer's codes are the specific
/// ones (INVALID_DIAGRAM, DOCUMENT_GONE, APPLY_REJECTED) and are passed
/// through: the agent's next move depends on WHICH refusal this was.
function rendererRefusal(reply: RendererReply, fallbackCode: string, fallbackMessage: string): AuthorBridgeResult {
  if (reply === null) return { ok: false, code: fallbackCode, message: fallbackMessage };
  return {
    ok: false,
    code: str(reply.code, fallbackCode),
    message: str(reply.message, fallbackMessage),
  };
}

const NO_RENDERER = 'the desktop workbench is not answering — its window may be closed or still starting';

/// The hub identity + bearer for the approval card, or null when signed out.
/// Same sourcing as the capture path: non-secret context from the renderer,
/// the token read from the main-process keychain at use time.
async function hubLeg(): Promise<HubLeg | null> {
  const ctx = currentHubContext();
  if (ctx === null) return null;
  const token = await keychainGetLocal(`hub_token_${ctx.profileId}`);
  if (token === null || token === '') return null;
  return { baseUrl: ctx.baseUrl, teamId: ctx.teamId, token };
}

async function read(req: AuthorBridgeRequest): Promise<AuthorBridgeResult> {
  const reply = await askRenderer({ op: 'read', documentId: req.documentId });
  if (reply === null || reply.ok !== true) return rendererRefusal(reply, 'AUTHOR_UNAVAILABLE', NO_RENDERER);
  const doc = (reply.document ?? {}) as Record<string, unknown>;
  const list = Array.isArray(reply.documents) ? (reply.documents as Record<string, unknown>[]) : [];
  const index = documentIndexText(
    list.map((d) => ({ id: str(d.id), kind: str(d.kind), title: str(d.title), active: d.active === true })),
  );
  // The body is returned verbatim inside a JSON envelope so an agent can round
  // -trip it through author_apply without re-escaping; the index rides
  // alongside as text because it is the part a human reads in a transcript.
  return {
    ok: true,
    text: `${JSON.stringify(doc, null, 2)}\n\nOpen documents:\n${index}`,
  };
}

/// `author_render`. No card and no lease: this returns a picture of ONE
/// document, drawn from that document, and the agent could already have the same
/// document's source from `author_read` under the same toggle. It is a read that
/// happens to answer in pixels — not a screenshot, which is a frame of the
/// user's whole screen and is carded every single time (ADR-062 D-4).
///
/// One round trip, not two: unlike `apply` there is nothing to ask the user
/// between resolving the target and doing the work, so the renderer resolves and
/// draws in the same call.
async function render(req: AuthorBridgeRequest): Promise<AuthorBridgeResult> {
  const reply = await askRenderer({ op: 'render', documentId: req.documentId, format: req.format });
  if (reply === null || reply.ok !== true) return rendererRefusal(reply, 'AUTHOR_UNAVAILABLE', NO_RENDERER);
  const image = (reply.image ?? {}) as Record<string, unknown>;
  const base64 = str(image.base64);
  const mimeType = str(image.mimeType);
  if (base64 === '' || mimeType === '') {
    // A reply shaped like a success with no picture in it. Refusing beats
    // forwarding an empty image block, which a client renders as a broken
    // graphic and an agent reads as "the document is blank".
    //
    // Not unit-tested here — this module imports Electron and `node --test`
    // cannot load it. The same shape IS pinned one layer out
    // (`authortools.test.ts`, "a provider that answers ok with no image"),
    // which is the leg an agent actually reaches; this is the second wall.
    return { ok: false, code: 'RENDER_FAILED', message: 'the desktop answered without an image' };
  }
  return { ok: true, text: str(reply.text, 'rendered'), image: { base64, mimeType } };
}

async function apply(req: AuthorBridgeRequest): Promise<AuthorBridgeResult> {
  // 1. Who is this about? The card cannot say "your diagram Foo" until the
  //    renderer has resolved the target, and an agent that named a closed
  //    document must learn that BEFORE the user is asked anything.
  const resolved = await askRenderer({ op: 'resolve', documentId: req.documentId });
  if (resolved === null || resolved.ok !== true) return rendererRefusal(resolved, 'AUTHOR_UNAVAILABLE', NO_RENDERER);
  const target = (resolved.document ?? {}) as Record<string, unknown>;
  const documentId = str(target.id);
  if (documentId === '') return { ok: false, code: 'AUTHOR_UNAVAILABLE', message: NO_RENDERER };
  const title = str(target.title);
  const kind = str(target.kind, 'document');

  // 2. Consent. A hub-relayed call was carded hub-side before it was routed
  //    (and `author_apply` can never ride a hub session grant — see
  //    desktopUIGrantable), so a second card here would ask the same question
  //    twice. A local call is ours to ask, unless this agent already holds a
  //    lease on THIS document.
  if (req.via !== 'hub' && !authorLeases.has(req.agentId, documentId)) {
    const leg = await hubLeg();
    if (leg === null) return { ok: false, code: 'AUTHOR_APPROVAL_UNAVAILABLE', message: authorDenialMessage('unavailable') };
    const card = authorApprovalCard({
      agentId: req.agentId,
      agentHandle: req.agentHandle,
      documentId,
      title,
      kind,
      mode: req.mode,
      reason: req.reason,
      bytes: req.mode === 'ops' ? diagramOpsBytes(req.operations) : req.body.length,
      operations: req.operations.length,
    });
    const verdict = await approveViaCardWithOption(leg, card, req.agentHandle);
    if (verdict.verdict !== 'approve') {
      if (verdict.verdict === 'raise_failed') {
        return { ok: false, code: 'AUTHOR_APPROVAL_UNAVAILABLE', message: authorDenialMessage('raise_failed') };
      }
      return { ok: false, code: 'AUTHOR_DENIED', message: authorDenialMessage(verdict.verdict) };
    }
    // "Allow this document for this session" — the scope the card offered, and
    // the only one it may create.
    if (verdict.optionId === 'session') authorLeases.grant(req.agentId, documentId);
  }

  // 3. The write. The renderer re-resolves by id rather than trusting the one
  //    we just looked at: the user may have closed the document while the card
  //    sat open, and a write to a document that is gone must fail, not land
  //    somewhere else.
  const done = await askRenderer({
    op: 'apply',
    documentId,
    mode: req.mode,
    body: req.body,
    operations: req.operations,
    reason: req.reason,
    by: req.agentHandle !== '' ? req.agentHandle : req.agentId,
  });
  if (done === null || done.ok !== true) return rendererRefusal(done, 'AUTHOR_UNAVAILABLE', NO_RENDERER);
  const applied = (done.document ?? {}) as Record<string, unknown>;
  const state: AuthorApplyState = done.state === 'applied_live' ? 'applied_live' : 'applied_store_only';
  return {
    ok: true,
    text: applyResultText({
      documentId: str(applied.id, documentId),
      title: str(applied.title, title),
      kind: str(applied.kind, kind),
      state,
      bytes: typeof done.bytes === 'number' ? done.bytes : req.body.length,
      ...(typeof done.note === 'string' && done.note !== '' ? { note: done.note } : {}),
    }),
  };
}

async function authorBridge(req: AuthorBridgeRequest): Promise<AuthorBridgeResult> {
  // The sharing toggle governs the whole desktop-UI capability set, reads
  // included: an agent that can read the user's documents is describing their
  // screen, which is the thing the toggle is a yes/no about.
  if (!isUiSharingEnabled()) {
    return { ok: false, code: 'UI_UNAVAILABLE', message: 'UI context sharing is off on the desktop (Settings → Assistant)' };
  }
  if (req.op === 'read') return read(req);
  return req.op === 'render' ? render(req) : apply(req);
}

setAuthorBridgeProvider(authorBridge);
