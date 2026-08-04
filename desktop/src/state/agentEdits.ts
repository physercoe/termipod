import { create } from 'zustand';

/// The **agent-edit ring** — what makes an `author_apply` reversible and
/// attributable (coworking B6; ADR-064's "landing live, undoable, attributed,
/// revertible").
///
/// Before every agent write, the document's current body is pushed here with
/// who asked for the change and why. The Author tab strip shows a chip while a
/// document has agent edits on it, and one click puts the last one back.
///
/// **Deliberately not `createHistory`** (`state/canvas.ts`), which the plan
/// names as the thing to reuse. That primitive has a `future` stack and a
/// `redo`, and redo is exactly wrong here: after a user reverts an agent's
/// write, "redo" would re-apply it — reinstating a change they just rejected,
/// one keystroke from the button that rejected it. What is shared with the
/// canvas is the *shape* (a bounded stack of serialized bodies), not the
/// implementation, and the six lines of capping are cheaper than a redo path
/// that must never be reachable.
///
/// It is also NOT the vendor editors' undo. Cmd+Z inside the canvas or draw.io
/// still walks their own history — B2 pushes to it before applying for exactly
/// that reason. This ring is the document-level answer for kinds whose editor
/// owns its undo stack privately (diagram, excalidraw), where the user has no
/// other way back.

/// How many agent edits per document are reversible. A bound, not a policy:
/// the ring holds whole document bodies, and an agent in a loop would otherwise
/// grow it without limit.
const CAP = 20;

export interface AgentEdit {
  /// The document body BEFORE the agent's write — what revert restores.
  before: string;
  /// The agent's handle, as the tool call carried it. Shown to the user, so it
  /// is display text and never trusted as an id.
  by: string;
  /// The `reason` the agent supplied, if any.
  reason?: string;
  /// Epoch ms, supplied by the caller — the store mints no time of its own so
  /// its behaviour is testable without a clock.
  at: number;
}

interface AgentEditsState {
  /// Per document, oldest first. Absent = no agent has written to it.
  byDoc: Record<string, AgentEdit[]>;
  record: (docId: string, edit: AgentEdit) => void;
  /// Pop the newest edit and return the body to restore, or null when there is
  /// nothing to revert. The caller writes it back — this store owns the
  /// history, not the document.
  revert: (docId: string) => string | null;
  /// Drop a document's history (it was closed).
  clear: (docId: string) => void;
}

/// Append with the cap applied. Pure so the eviction end is pinned by a test:
/// dropping the NEWEST instead of the oldest would silently make the most
/// recent agent edit the one you cannot undo.
export function pushCapped(prev: readonly AgentEdit[], edit: AgentEdit, cap = CAP): AgentEdit[] {
  const next = [...prev, edit];
  return next.length > cap ? next.slice(next.length - cap) : next;
}

export const useAgentEdits = create<AgentEditsState>((set, get) => ({
  byDoc: {},
  record: (docId, edit) => set({ byDoc: { ...get().byDoc, [docId]: pushCapped(get().byDoc[docId] ?? [], edit) } }),
  revert: (docId) => {
    const stack = get().byDoc[docId] ?? [];
    const top = stack[stack.length - 1];
    if (top === undefined) return null;
    const byDoc = { ...get().byDoc };
    // An emptied stack deletes its key rather than holding `[]`, so
    // `latestAgentEdit` and the chip read absence the same way whether the doc
    // was never written to or has been fully reverted.
    if (stack.length === 1) delete byDoc[docId];
    else byDoc[docId] = stack.slice(0, -1);
    set({ byDoc });
    return top.before;
  },
  clear: (docId) => {
    const byDoc = { ...get().byDoc };
    delete byDoc[docId];
    set({ byDoc });
  },
}));

/// The newest agent edit on a document, or undefined. Drives the tab chip.
export function latestAgentEdit(byDoc: Record<string, AgentEdit[]>, docId: string): AgentEdit | undefined {
  const stack = byDoc[docId];
  return stack === undefined || stack.length === 0 ? undefined : stack[stack.length - 1];
}

/// How many agent edits are still reversible on a document.
export function agentEditCount(byDoc: Record<string, AgentEdit[]>, docId: string): number {
  return byDoc[docId]?.length ?? 0;
}

/// The chip's tooltip: who wrote, why, and how many reverts are left. Pure and
/// formatter-injected so the wording is pinned by a test rather than eyeballed
/// on a screen this host does not have.
export function agentEditTitle(edit: AgentEdit | undefined, count: number, t: (k: string) => string): string {
  if (edit === undefined) return '';
  const who = t('author.agentEditedBy').replace('{agent}', edit.by);
  const rest = count > 1 ? ` (${t('author.agentEditCount').replace('{n}', String(count))})` : '';
  // The reason is agent-authored text, so it goes last: a long one truncating
  // in the tooltip must not push the attribution out of view.
  return edit.reason !== undefined && edit.reason !== '' ? `${who}${rest} — ${edit.reason}` : `${who}${rest}`;
}
