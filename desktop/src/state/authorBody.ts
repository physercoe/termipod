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
import { prepareDiagramBody } from './drawioXml.ts';
import { parseTable } from './table.ts';
import type { DocKind } from './documents.ts';

/// The write modes lane A ships. `ops` (structured edits against an existing
/// diagram/canvas) is lane D — W2 — and is deliberately absent rather than
/// accepted-and-ignored.
export type AuthorApplyMode = 'replace' | 'append';

export type BodyCheck = { ok: true; body: string } | { ok: false; code: string; message: string };

/// A `.json` blob is an Excalidraw scene if its top-level `type` is
/// `"excalidraw"` (the ecosystem-standard discriminator) and it carries an
/// element array. Lives here rather than in `documents.ts` (its original home)
/// because it is now asked twice — once to classify a file being opened, once
/// to refuse an agent's malformed scene — and one predicate with two callers
/// beats two predicates that can drift.
export function isExcalidrawBody(content: string): boolean {
  try {
    const d = JSON.parse(content) as { type?: unknown; elements?: unknown };
    return d.type === 'excalidraw' && Array.isArray(d.elements);
  } catch {
    return false;
  }
}

/// Whether an editor of this kind re-renders from `doc.body`, so writing the
/// store IS what the user sees. Verified per kind, not assumed:
///
///   - `markdown` — `ui/MarkdownEditor.tsx` diffs an external `value` change
///     into the CodeMirror doc (its own comment names the agent-insert case);
///   - `figure` — `surfaces/FigureEditor.tsx` does the same for the source
///     pane and re-renders the preview from a debounced `doc.body`;
///   - `table` — `ui/TableEditor.tsx` parses `value` in a `useState`
///     INITIALIZER and has no effect on it: a store write does not reach the
///     mounted grid (lane B4 adds the reconcile);
///   - `excalidraw` — holds the scene in the vendor component after mount
///     (lane B3 adds `updateScene`);
///   - `diagram` / `canvas` — own their live state too, which is exactly why
///     lane B1/B2 register live-apply targets for them. A registered target
///     answers `applied_live` on its own, so they are `false` here.
export function rendersFromBody(kind: DocKind): boolean {
  return kind === 'markdown' || kind === 'figure';
}

/// Whether `append` means anything for this kind. Appending to a structured
/// body would produce a document that no longer parses, so the mode is refused
/// rather than silently reinterpreted as `replace`.
export function supportsAppend(kind: DocKind): boolean {
  return kind === 'markdown';
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
      // + a worker. Lane B5 adds the dry-run render; until then a figure body
      // is accepted and its error surfaces in the preview, where a human sees
      // it — the failure mode is a visible render error, not a lost document.
      return { ok: true, body };
  }
}

/// Compose the body to commit from the mode. Separated from validation because
/// `append` must be validated on the RESULT, not on the fragment: a fragment
/// can be perfectly good markdown and still produce a document that is not.
export function composeAuthorBody(kind: DocKind, mode: AuthorApplyMode, current: string, incoming: string): BodyCheck {
  if (mode === 'replace') return { ok: true, body: incoming };
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
