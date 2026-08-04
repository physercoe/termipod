/// Per-kind body rules for an **agent** write into an Author document
/// (coworking lane A; ADR-064 D5). Pure, so `node --test` can pin the one
/// property that matters: a body we cannot read is REFUSED, never absorbed.
///
/// Three questions live here, and nowhere else:
///
///   1. **May this body replace that document's?** Every structured kind has a
///      parser, and every one of those parsers has a lenient path for the
///      HUMAN case — `parseTable` seeds a blank grid, `parseCanvas` opens
///      read-only, an unwrapped `mxGraphModel` is wrapped for you. Lenient is
///      right for a person opening a file and wrong for an agent committing
///      one: the human sees the blank grid, an agent is told "applied". So the
///      agent path re-asks each parser and treats every degraded answer as a
///      refusal.
///   2. **What does `mode` compose?** `replace` is the body; `append` is a
///      prose operation and exists for prose kinds only.
///   3. **Does a store write reach the screen?** Some editors re-render from
///      `doc.body` on every change and some own their live state after mount.
///      That difference is the whole reason lane B's live-apply registry
///      exists, and it is what A4's `applied_live` / `applied_store_only`
///      ladder reports. Getting it wrong here makes the tool lie about what
///      the user can see, so each answer below cites the code that decides it.
import { parseCanvas } from './canvas.ts';
import { applyDiagramOperations, diagramOpsSummary, type DiagramOperation } from './drawioOps.ts';
import { prepareDiagramBody } from './drawioXml.ts';
import { parseExcalidrawScene } from './excalidrawScene.ts';
import { parseTable } from './table.ts';
import type { DocKind } from './documents.ts';

/// The write modes. `replace` and `append` shipped with lane A; `ops` —
/// ID-addressed structured edits against the diagram the user already has — is
/// lane D1, and today means draw.io only (`supportsOps`). Canvas ops are D2.
export type AuthorApplyMode = 'replace' | 'append' | 'ops';

/// `note` carries what an agent could not work out from the body alone. Today
/// that is the op batch's summary, and specifically its CASCADE: deleting one
/// box can remove six cells, and the composed body does not say which.
export type BodyCheck = { ok: true; body: string; note?: string } | { ok: false; code: string; message: string };

/// A `.json` blob is an Excalidraw scene if its top-level `type` is
/// `"excalidraw"` (the ecosystem-standard discriminator) and it carries an
/// element array. Asked three times now — to classify a file being opened, to
/// refuse an agent's malformed scene, and (B3) to decide whether the editor may
/// write back over the file it opened — so the rule itself lives in
/// `excalidrawScene.ts` and this is the boolean face of it. Re-stating the
/// checks here instead would be two predicates that agree until one is edited.
export function isExcalidrawBody(content: string): boolean {
  return parseExcalidrawScene(content) !== null;
}

/// Whether an editor of this kind re-renders from `doc.body`, so writing the
/// store IS what the user sees. Verified per kind, not assumed:
///
///   - `markdown` — `ui/MarkdownEditor.tsx` diffs an external `value` change
///     into the CodeMirror doc (its own comment names the agent-insert case);
///   - `figure` — `surfaces/FigureEditor.tsx` does the same for the source
///     pane and re-renders the preview from a debounced `doc.body`;
///   - `table` — `ui/TableEditor.tsx` parsed `value` in a `useState`
///     INITIALIZER only, so a store write never reached the mounted grid. B4
///     added the `useEffect([value])` reconcile (with an echo guard, so the
///     editor does not re-parse its own writes), which is what moved this kind
///     from `false` to `true`;
///   - `excalidraw` — holds the scene in the vendor component after mount, so
///     B3 registers a live-apply target that calls `updateScene`;
///   - `diagram` / `canvas` — own their live state too, which is exactly why
///     lane B1/B2 register live-apply targets for them. A registered target
///     answers `applied_live` on its own, so all three are `false` here.
export function rendersFromBody(kind: DocKind): boolean {
  return kind === 'markdown' || kind === 'figure' || kind === 'table';
}

/// Whether `append` means anything for this kind. Appending to a structured
/// body would produce a document that no longer parses, so the mode is refused
/// rather than silently reinterpreted as `replace`.
export function supportsAppend(kind: DocKind): boolean {
  return kind === 'markdown';
}

/// Whether `ops` means anything for this kind. Diagram only: the op grammar is
/// draw.io's cell model (`{operation, cell_id, new_xml}` over `mxCell` ids), and
/// there is no kind-neutral reading of it. Canvas gets its own node/edge ops in
/// D2; the two structured drawing kinds that stay replace-only are named in the
/// tool description (D3) rather than left for an agent to discover by refusal.
export function supportsOps(kind: DocKind): boolean {
  return kind === 'diagram';
}

function refuse(code: string, message: string): BodyCheck {
  return { ok: false, code, message };
}

/// Validate (and, for a diagram, normalize) a body an agent wants to commit.
///
/// An empty body is refused for every structured kind. `parseTable('')` and
/// `seedBody` both treat empty as "a new document", which is the right reading
/// of a human creating a file and the wrong reading of an agent write — a
/// zero-length commit would blank the document and report success.
export function validateAuthorBody(kind: DocKind, body: string): BodyCheck {
  if (kind === 'markdown') return { ok: true, body };
  if (body.trim() === '') {
    return refuse('EMPTY_BODY', `an empty body cannot replace a ${kind} document — send the full ${kind} source`);
  }
  switch (kind) {
    case 'diagram': {
      // prepareDiagramBody wraps a bare mxGraphModel in the <mxfile> scaffold
      // and THEN validates, so the answer is the body to store, not the input.
      const prepared = prepareDiagramBody(body);
      if (prepared.xml === null) return refuse('INVALID_DIAGRAM', prepared.error);
      return { ok: true, body: prepared.xml };
    }
    case 'canvas': {
      if (parseCanvas(body).readOnly === true) {
        return refuse(
          'INVALID_CANVAS',
          'this is not a JSON Canvas document — expected {"nodes":[…],"edges":[…]} (jsoncanvas.org 1.0)',
        );
      }
      return { ok: true, body };
    }
    case 'table': {
      // The name-column label only seeds a NEW table; validation never reaches
      // it, because a body that would need seeding is the refusal case.
      if (parseTable(body, 'Name').readOnly === true) {
        return refuse('INVALID_TABLE', 'this is not a table document — expected {"columns":[…],"rows":[…]} as JSON');
      }
      return { ok: true, body };
    }
    case 'excalidraw': {
      if (!isExcalidrawBody(body)) {
        return refuse('INVALID_SCENE', 'this is not an Excalidraw scene — expected {"type":"excalidraw","elements":[…]}');
      }
      return { ok: true, body };
    }
    case 'figure':
      // A figure body is renderer source (mermaid/dot/vega-lite); the renderer
      // is the only thing that can judge it, and it lives behind a lazy import
      // + a DOM. So this function — pure, synchronous, `node --test`-drivable —
      // is the wrong place, and B5 puts the judgement where it can be made: an
      // awaited dry-run render inside `executeAuthorRequest`, step 4. The
      // non-empty check above still applies, and is all that can be decided
      // here.
      return { ok: true, body };
  }
}

/// What an `author_apply` wants written, before any of it is judged.
export interface AuthorWrite {
  mode: AuthorApplyMode;
  body: string;
  operations: readonly DiagramOperation[];
}

/// Compose the body to commit from the mode. Separated from validation because
/// neither composing mode can be judged on its input: an `append` fragment can
/// be perfectly good markdown and still produce a document that is not, and an
/// `ops` batch produces a body no argument contained. Both are validated on the
/// RESULT, which is what `executeAuthorRequest` does next.
export function composeAuthorBody(kind: DocKind, current: string, write: AuthorWrite): BodyCheck {
  const { mode, body: incoming } = write;
  if (mode === 'replace') return { ok: true, body: incoming };
  if (mode === 'ops') {
    if (!supportsOps(kind)) {
      return refuse(
        'MODE_UNSUPPORTED',
        `mode 'ops' addresses draw.io cells by id and this is a ${kind} document — send mode 'replace' with the whole document`,
      );
    }
    const done = applyDiagramOperations(current, write.operations);
    // Every refusal here leaves `current` untouched by construction: the op
    // engine returns a new string or a message, and never edits in place.
    if (!done.ok) return refuse('INVALID_OPERATIONS', done.message);
    return { ok: true, body: done.result.xml, note: diagramOpsSummary(done.result) };
  }
  if (!supportsAppend(kind)) {
    return refuse(
      'MODE_UNSUPPORTED',
      `mode 'append' is only meaningful for a markdown document — a ${kind} body is structured, so send mode 'replace' with the whole document`,
    );
  }
  if (current.trim() === '') return { ok: true, body: incoming };
  // One blank line between what was there and what arrived: markdown treats a
  // single newline as a soft wrap, so appending without it would fuse the
  // agent's first line onto the user's last paragraph.
  const sep = current.endsWith('\n\n') ? '' : current.endsWith('\n') ? '\n' : '\n\n';
  return { ok: true, body: `${current}${sep}${incoming}` };
}
