/// Excalidraw **scene bodies** — the one place that decides whether a stored
/// `.excalidraw` body is a scene, and what an editor should load from it
/// (coworking lane B3; ADR-064).
///
/// Dependency-free on purpose. `@excalidraw/excalidraw` is a lazy chunk worth
/// hundreds of KB, and this module is imported by `node --test`, so the shapes
/// below are structural and `ExcalidrawEditor` casts them to the vendor's types
/// at its one call site.
///
/// **Strict, unlike the reader it replaces.** `ExcalidrawEditor.toInitialData`
/// accepted any JSON and coerced a missing `elements` to `[]` — the
/// lenient-for-humans reading `parseTable` had before A5, with the same
/// consequence. `kindForFile` routes a `.excalidraw` extension to this editor
/// without consulting the content (`documents.ts:122-124`), so a file whose
/// bytes we cannot read opened as a BLANK canvas, and the editor's first
/// `onChange` serialized that blank scene back over the user's drawing. A body
/// that is not a scene is now refused, and the editor opens read-only rather
/// than empty.

export interface ExcalidrawScene {
  /// Scene elements, verbatim. Not validated element-by-element: the vendor
  /// owns that grammar and re-validates on load, and a check here would be a
  /// second opinion that drifts from theirs.
  elements: unknown[];
  appState: Record<string, unknown>;
  /// Embedded binary files (images), keyed by file id. Absent is different
  /// from empty only to the vendor's `addFiles`, which we skip entirely when
  /// there is nothing to add.
  files: Record<string, unknown> | undefined;
}

/// What opening a body means, as three distinct answers rather than one
/// nullable scene. `blank` and `unreadable` both yield no scene, and treating
/// them alike is precisely the bug above: one is a new document, the other is
/// a document we must not overwrite.
export type SceneOpen =
  | { state: 'blank' }
  | { state: 'scene'; scene: ExcalidrawScene }
  | { state: 'unreadable' };

function isRecord(v: unknown): v is Record<string, unknown> {
  return v !== null && typeof v === 'object' && !Array.isArray(v);
}

/// Parse a body into a loadable scene, or null when it is not one.
///
/// An empty body returns null too — it is not a scene — so callers that need
/// to tell "new document" from "unreadable" use `openScene` instead of
/// re-deriving the distinction from a null.
export function parseExcalidrawScene(body: string): ExcalidrawScene | null {
  if (body.trim() === '') return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(body);
  } catch {
    return null;
  }
  if (!isRecord(parsed)) return null;
  // `type: 'excalidraw'` plus an element array is the ecosystem's own
  // discriminator — the pair every `.excalidraw` writer emits. JSON that
  // merely happens to carry an `elements` array is some other document.
  if (parsed.type !== 'excalidraw' || !Array.isArray(parsed.elements)) return null;
  return {
    elements: parsed.elements,
    // `serializeAsJSON` already strips runtime-only appState, so whatever
    // survives a round-trip is safe to restore verbatim. A non-object one is
    // dropped rather than refused: appState is presentation, and losing a
    // scroll position must not cost the user their drawing.
    appState: isRecord(parsed.appState) ? parsed.appState : {},
    files: isRecord(parsed.files) ? parsed.files : undefined,
  };
}

/// The editor's three-way open decision, kept here so a test drives it rather
/// than a screen this host does not have.
export function openScene(body: string): SceneOpen {
  if (body.trim() === '') return { state: 'blank' };
  const scene = parseExcalidrawScene(body);
  return scene === null ? { state: 'unreadable' } : { state: 'scene', scene };
}
