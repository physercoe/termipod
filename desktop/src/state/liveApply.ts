/// The **live-apply registry** — how an agent's `author_apply` reaches the
/// editor a user is looking at, instead of only the store behind it (coworking
/// lane B; ADR-064).
///
/// Writing a document's `body` is not enough for every kind. A vendor editor
/// owns its own live state once mounted: the draw.io iframe loads the XML on
/// `init` and then streams changes back, and the canvas board holds React Flow
/// nodes/edges built from one parse. Setting `body` under either leaves the
/// screen showing the pre-write document while the store holds the new one —
/// so the user is told nothing happened and the agent is told it did.
///
/// Each editor that can take a live write registers here while it is mounted.
/// `author_apply` (lane A) tries `liveApply` first and reports which rung it
/// landed on — the A4 degrade ladder:
///
///   - `applied_live`       a target was registered and accepted the body
///   - `applied_store_only` no target (the doc is not open, or its kind has no
///                          adapter yet) — the store took it, the screen did
///                          not
///   - `rejected`           a target refused: the body did not parse, or the
///                          editor is read-only
///
/// Keyed by document id, single target per document: a document is open in at
/// most one editor (the Author surface mounts one, keyed on `active.id`).
/// Registering a second replaces the first, which is what a remount does — the
/// unregister returned to the FIRST mount must therefore not evict the second.
///
/// Deliberately a plain module-level Map, not a store: nothing renders from it,
/// and a zustand subscription would re-render the Author surface on every
/// editor mount for no visible reason. Pure, so `node --test` covers the
/// eviction rules that are easy to get subtly wrong.

export type ApplyOutcome = 'applied_live' | 'rejected';

/// Apply a body to a live editor. Returns `rejected` when the editor will not
/// take it — an unparseable body or a read-only board. Throwing is also
/// treated as a rejection: an adapter that blows up must never be reported to
/// the agent as a successful write.
export type LiveApplyFn = (body: string) => ApplyOutcome;

const targets = new Map<string, LiveApplyFn>();

/// Register `fn` as the live target for `docId` while the editor is mounted.
/// Returns the unregister — call it from the effect cleanup.
///
/// The unregister is identity-checked: React can mount the next editor before
/// unmounting the previous one, so a cleanup that deleted the key
/// unconditionally would evict the target that just replaced it and silently
/// downgrade the next `author_apply` to `applied_store_only`.
export function registerLiveApply(docId: string, fn: LiveApplyFn): () => void {
  targets.set(docId, fn);
  return () => {
    if (targets.get(docId) === fn) targets.delete(docId);
  };
}

/// Is a live target registered for this document? Lane A's dry-run check —
/// it decides which result state to report before attempting the write.
export function hasLiveApply(docId: string): boolean {
  return targets.has(docId);
}

/// Apply to the live editor, or report that there is none. Never throws: an
/// adapter that does is a rejection, because the alternative is telling the
/// agent a write landed when the editor is in an unknown state.
export function liveApply(docId: string, body: string): ApplyOutcome | 'no_target' {
  const fn = targets.get(docId);
  if (fn === undefined) return 'no_target';
  try {
    return fn(body);
  } catch {
    return 'rejected';
  }
}

/// Test seam — drops every target. Never called by the app: a registration
/// outlives nothing but its editor's mount.
export function resetLiveApply(): void {
  targets.clear();
}
