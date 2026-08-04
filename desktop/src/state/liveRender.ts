/// The **live-render registry** — how `author_render` reaches an editor that is
/// the only thing able to draw its own document (coworking W2; ADR-064).
///
/// Sibling of `liveApply.ts`, and deliberately a separate registry rather than a
/// second method on the same one. The two answer different questions and a
/// document can support either without the other: `figure` renders from its
/// body and takes no live write beyond a store update, while `canvas` takes a
/// live write (B2) and cannot be rendered at all. Folding them together would
/// make every adapter declare a capability it does not have.
///
/// Only `diagram` registers today. draw.io holds the model inside an iframe, and
/// the only renderer for an mxGraph model is draw.io itself — so an agent asking
/// for a picture of a closed diagram gets a refusal that says so, not a blank
/// page. Every other renderable kind goes through `doc.body` and never comes
/// here.
///
/// Async where `liveApply` is sync: an export is a postMessage round trip, not a
/// function call. That difference is why the timeout lives in the ADAPTER rather
/// than here — the registry does not know what a slow answer means for a given
/// editor, and a deadline invented at this level would be a guess applied to
/// every future one.

/// Render the document to SVG source. Resolves with the markup, or rejects with
/// a message the agent is shown. SVG only: the one rasterizer in the app turns
/// SVG into PNG, so an adapter that also had a PNG path would be a second answer
/// to the same question that could disagree with the first.
export type LiveRenderFn = () => Promise<string>;

const targets = new Map<string, LiveRenderFn>();

/// Register `fn` as the live render target for `docId` while the editor is
/// mounted. Returns the unregister — call it from the effect cleanup.
///
/// Identity-checked on the way out for the same reason `liveApply` is: React can
/// mount the next editor before unmounting the previous one, and a cleanup that
/// deleted the key unconditionally would evict the target that just replaced it.
export function registerLiveRender(docId: string, fn: LiveRenderFn): () => void {
  targets.set(docId, fn);
  return () => {
    if (targets.get(docId) === fn) targets.delete(docId);
  };
}

export function hasLiveRender(docId: string): boolean {
  return targets.has(docId);
}

/// Ask the mounted editor for this document's SVG. `null` means no editor is
/// registered — the document is not open, which is a different answer from "the
/// export failed" and gets a different refusal.
///
/// A throw from the adapter is NOT swallowed: unlike a live apply, where the
/// safe reading of an exception is "the write did not land", a failed render has
/// no destructive outcome to guard against and its message is the most useful
/// thing we can give the agent.
export async function liveRender(docId: string): Promise<string | null> {
  const fn = targets.get(docId);
  if (fn === undefined) return null;
  return fn();
}

/// Test seam — drops every target. Never called by the app.
export function resetLiveRender(): void {
  targets.clear();
}
