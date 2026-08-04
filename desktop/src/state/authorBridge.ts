/// The renderer half of the `author_*` bridge — the pure part (coworking A2;
/// ADR-064).
///
/// Main cannot answer `author_read` or `author_apply` on its own: the
/// documents live in a renderer store, the validators need a DOM (`DOMParser`
/// is what catches a self-closing nested `mxCell` the regex sweep cannot see),
/// and the live editors are React components. So main pushes a request down
/// the existing `bridge:event` channel with a correlation id and parks on the
/// renderer's `desktopui_author_result` reply.
///
/// This module owns the parts that are decidable without a store, so
/// `node --test` covers them:
///
///   - NARROWING the pushed payload. Main is our own process, but these
///     arguments came from an agent, and a renderer store takes nothing on
///     trust from an IPC boundary an agent's input reached (the
///     `agentHighlight.ts` discipline);
///   - SHAPING the answer — the document index and the per-document summary,
///     which must carry ids and metadata and never a body it was not asked
///     for;
///   - the LADDER: turning a `liveApply` outcome plus the document's kind into
///     the state A4 reports.
///
/// `authorBridgeHost.ts` does the store work and the wiring.
import { composeAuthorBody, rendersFromBody, validateAuthorBody, type AuthorApplyMode } from './authorBody.ts';
import type { ApplyOutcome } from './liveApply.ts';
import type { AgentEdit } from './agentEdits.ts';
import type { Doc, DocKind } from './documents.ts';
import type { FigureSpec } from './figures.ts';

/// The renderer event main pushes requests on, and the bridge command the
/// renderer answers with. One channel each way, correlated by `id`.
export const AUTHOR_REQUEST_EVENT = 'desktopui_author_request';
export const AUTHOR_RESULT_COMMAND = 'desktopui_author_result';

/// `resolve` is main's pre-flight: it needs the target document's identity to
/// put on the approval card BEFORE the body is applied, and it deliberately
/// does not return the body — naming a document must not disclose it.
export type AuthorOp = 'read' | 'resolve' | 'apply';

export interface AuthorRequest {
  id: string;
  op: AuthorOp;
  /// null = whichever document is active in Author.
  documentId: string | null;
  mode: AuthorApplyMode;
  body: string;
  reason: string;
  /// The agent's handle as the tool call carried it — display text for the
  /// agent-edit chip, never trusted as an identity.
  by: string;
}

export interface AuthorDocLine {
  id: string;
  kind: DocKind;
  title: string;
  file_path: string | null;
  updated_at: string;
  active: boolean;
}

export type AuthorResult =
  | { ok: true; op: 'resolve'; document: AuthorDocLine }
  | { ok: true; op: 'read'; document: AuthorDocLine & { spec: string | null; body: string }; documents: AuthorDocLine[] }
  | { ok: true; op: 'apply'; document: AuthorDocLine; state: 'applied_live' | 'applied_store_only'; bytes: number }
  | { ok: false; code: string; message: string };

/// The largest body an agent may commit in one call. A document is prose or a
/// diagram, not a dataset; a megabyte arriving here is a runaway loop or a
/// paste of something that does not belong in a document, and either way the
/// refusal is cheaper than the store write.
export const AUTHOR_BODY_MAX = 512 * 1024;

function str(v: unknown): string {
  return typeof v === 'string' ? v : '';
}

/// Narrow one pushed request. Returns null when the payload is not a request
/// we can answer — the host drops it rather than replying, because a payload
/// with no usable `id` has nowhere to reply TO.
export function asAuthorRequest(value: unknown): AuthorRequest | null {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return null;
  const v = value as Record<string, unknown>;
  const id = str(v.id);
  const op = str(v.op);
  if (id === '' || (op !== 'read' && op !== 'resolve' && op !== 'apply')) return null;
  const documentId = str(v.document_id);
  const mode = v.mode === 'append' ? 'append' : 'replace';
  return {
    id,
    op,
    documentId: documentId === '' ? null : documentId,
    mode,
    body: str(v.body),
    reason: str(v.reason),
    by: str(v.by),
  };
}

export function docLine(doc: Doc, activeId: string | null): AuthorDocLine {
  return {
    id: doc.id,
    kind: doc.kind,
    title: doc.title,
    file_path: doc.filePath ?? null,
    updated_at: new Date(doc.updatedAt).toISOString(),
    active: doc.id === activeId,
  };
}

/// Pick the document a request addresses: the named one, or the active one
/// when the agent omitted the id. Absence is an error with the id in it — an
/// agent that names a document that was closed since it read the index needs
/// to know which one, not that "a document" was missing.
export function resolveTarget(
  docs: readonly Doc[],
  activeId: string | null,
  documentId: string | null,
): { ok: true; doc: Doc } | { ok: false; code: string; message: string } {
  if (documentId !== null) {
    const found = docs.find((d) => d.id === documentId);
    if (found === undefined) {
      return {
        ok: false,
        code: 'DOCUMENT_GONE',
        message: `no open document with id '${documentId}' — call author_read with no document_id for the current list`,
      };
    }
    return { ok: true, doc: found };
  }
  const active = docs.find((d) => d.id === activeId);
  if (active === undefined) {
    return {
      ok: false,
      code: 'NO_ACTIVE_DOCUMENT',
      message: 'no document is open in Author — ask the user to open or create one, or name a document_id',
    };
  }
  return { ok: true, doc: active };
}

/// A4's ladder. A registered live target that took the body is the strong
/// answer; without one, the kind decides — an editor that re-renders from
/// `body` shows the write as soon as the store has it, and an editor that owns
/// its live state does not.
///
/// `no_target` also covers the ordinary case of a document that is not the one
/// on screen, where `rendersFromBody` is the right answer anyway: opening that
/// tab reads the new body.
export function applyStateFor(outcome: ApplyOutcome | 'no_target', kind: DocKind): 'applied_live' | 'applied_store_only' {
  if (outcome === 'applied_live') return 'applied_live';
  // A refusal is never a landing, whatever the kind re-renders from. The
  // executor turns `rejected` into a refusal before this function is reached,
  // so this arm is defensive — but it must stay true by construction rather
  // than by the accident that no body-reactive kind had a live target: B4 made
  // `table` reactive, and without this line a rejected table write would have
  // started answering `applied_live`.
  if (outcome === 'rejected') return 'applied_store_only';
  return rendersFromBody(kind) ? 'applied_live' : 'applied_store_only';
}

/// Everything the executor touches, injected. The three stores it needs
/// (`useDocuments`, `useAgentEdits`, the live-apply registry) all reach
/// `localStorage` or a mounted React tree, and the ORDER in which they are
/// touched is this wedge's safety property — so the order is written where a
/// test can drive it, and `authorBridgeHost.ts` binds the real ones.
export interface AuthorIO {
  docs: readonly Doc[];
  activeId: string | null;
  liveApply: (docId: string, body: string) => ApplyOutcome | 'no_target';
  record: (docId: string, edit: AgentEdit) => void;
  update: (docId: string, body: string) => void;
  now: () => number;
  /// B5's dry run: render a figure body and throw the result away. Injected
  /// rather than imported because it reaches a lazily-loaded renderer library
  /// (mermaid, graphviz-wasm, vega) and a DOM — neither of which belongs in a
  /// module `node --test` drives. Resolving means the source renders; throwing
  /// is the refusal, and the thrown message is what the agent is told.
  renderFigure: (spec: FigureSpec, src: string) => Promise<unknown>;
}

function refuse(code: string, message: string): AuthorResult {
  return { ok: false, code, message };
}

/// Turn a renderer throw into the sentence the agent gets back (B5).
///
/// `FigureRenderError` carries an optional source `line`, and it is read
/// STRUCTURALLY rather than by importing the class: `state/figures.ts` builds a
/// registry of renderers that reach `document` and lazily import megabytes of
/// vendor code, and pulling it in here for one `instanceof` would drag all of
/// that into a module whose whole point is being drivable by `node --test`.
/// A duck-typed number is the same answer at a fraction of the coupling.
export function figureRefusal(spec: string, e: unknown): string {
  const msg = e instanceof Error ? e.message : String(e);
  const line = (e as { line?: unknown }).line;
  const where = typeof line === 'number' ? ` (line ${String(line)})` : '';
  return `the ${spec} renderer could not render this source${where}: ${msg}. The document is unchanged`;
}

/// Serve one request. The order below IS the contract:
///
///   1. resolve the target — a document that is gone fails before anything;
///   2. cap the size;
///   3. compose (`replace` / `append`);
///   4. validate the RESULT through the kind's parser, and for a figure also
///      through its RENDERER (B5) — a figure body is source for mermaid or
///      graphviz, and no parser we own can judge it;
///   5. offer it to the mounted editor;
///   6. record the pre-write body, then write the store.
///
/// Step 6 holds the only mutations, and every refusal above it leaves the
/// document byte-identical — including a live editor's rejection, which is why
/// the store write comes AFTER the editor gets its say rather than before.
///
/// Async only because of step 4's dry run. Everything else here is synchronous,
/// and no `await` sits between the editor call and the store write: those two
/// must not be separated by a suspension point that lets a user edit land in
/// between them.
export async function executeAuthorRequest(req: AuthorRequest, io: AuthorIO): Promise<AuthorResult> {
  const target = resolveTarget(io.docs, io.activeId, req.documentId);
  if (!target.ok) return refuse(target.code, target.message);
  const doc = target.doc;
  const line = docLine(doc, io.activeId);

  if (req.op === 'resolve') return { ok: true, op: 'resolve', document: line };
  if (req.op === 'read') {
    return {
      ok: true,
      op: 'read',
      document: { ...line, spec: doc.spec ?? null, body: doc.body },
      documents: io.docs.map((d) => docLine(d, io.activeId)),
    };
  }

  // The size gate is first among the write checks: a body over the cap is
  // refused without composing, validating or parsing it, so a runaway agent
  // costs one comparison.
  if (req.body.length > AUTHOR_BODY_MAX) {
    return refuse(
      'BODY_TOO_LARGE',
      `body is ${String(req.body.length)} bytes; the cap is ${String(AUTHOR_BODY_MAX)} — a document is prose or a drawing, not a dataset`,
    );
  }
  const composed = composeAuthorBody(doc.kind, req.mode, doc.body, req.body);
  if (!composed.ok) return refuse(composed.code, composed.message);
  const checked = validateAuthorBody(doc.kind, composed.body);
  if (!checked.ok) return refuse(checked.code, checked.message);
  const next = checked.body;

  // Nothing changed: report it rather than pushing a no-op onto the revert
  // ring, which would spend the user's undo budget on writes that did nothing.
  // Ahead of the dry run on purpose — re-rendering a body the document already
  // has cannot change the answer, and for mermaid or vega it is not cheap.
  if (next === doc.body) {
    return { ok: true, op: 'apply', document: line, state: 'applied_live', bytes: next.length };
  }

  // B5: the figure dry run. Every other kind has a parser we own, and
  // `validateAuthorBody` has already asked it; a figure body is source for a
  // third-party renderer, so the only honest judge is the renderer itself.
  // Render it once and discard the SVG — what matters is whether it throws.
  //
  // Without this the failure mode was quiet in the worst way: the write landed,
  // the tool said `applied_live`, and the user's figure pane switched to an
  // error strip over a document they did not break. A refusal instead keeps the
  // document intact and hands the agent the renderer's own diagnosis, which is
  // what lets it fix the source and retry.
  //
  // A figure doc with no `spec` has no renderer to ask, so it is accepted —
  // absence of a judge is not a verdict.
  if (doc.kind === 'figure' && doc.spec !== undefined) {
    try {
      await io.renderFigure(doc.spec, next);
    } catch (e) {
      return refuse('INVALID_FIGURE', figureRefusal(doc.spec, e));
    }
  }

  const outcome = io.liveApply(doc.id, next);
  if (outcome === 'rejected') {
    return refuse(
      'APPLY_REJECTED',
      `the ${doc.kind} editor refused this body — it is open read-only, or the editor could not load it. The document is unchanged`,
    );
  }
  io.record(doc.id, {
    before: doc.body,
    by: req.by !== '' ? req.by : 'an agent',
    ...(req.reason !== '' ? { reason: req.reason } : {}),
    at: io.now(),
  });
  io.update(doc.id, next);
  return { ok: true, op: 'apply', document: line, state: applyStateFor(outcome, doc.kind), bytes: next.length };
}
