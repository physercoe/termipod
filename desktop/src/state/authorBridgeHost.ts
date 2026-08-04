/// The renderer half of the `author_*` bridge — the wiring (coworking A2;
/// ADR-064).
///
/// Deliberately thin. The request order that makes an agent write safe lives
/// in `executeAuthorRequest` (authorBridge.ts), where `node --test` can drive
/// it; this module only binds the three real stores to it and answers main.
import { listen, invoke } from '../bridge';
import { isShell } from '../platform';
import {
  asAuthorRequest,
  AUTHOR_REQUEST_EVENT,
  AUTHOR_RESULT_COMMAND,
  executeAuthorRequest,
  type AuthorIO,
  type AuthorResult,
} from './authorBridge';
import { useAgentEdits } from './agentEdits';
import { useDocuments } from './documents';
import { renderFigure } from './figures';
import { liveApply } from './liveApply';
import { hasLiveRender } from './liveRender';
import { renderDocument } from './renderDocHost';

/// Read the stores at CALL time, never at module load: an `author_apply` that
/// parked behind an approval card comes back to a workbench the user has been
/// using, and a snapshot taken when the app booted would write against a
/// document list that no longer exists.
function io(): AuthorIO {
  const { docs, activeId } = useDocuments.getState();
  return {
    docs,
    activeId,
    liveApply,
    record: (docId, edit) => useAgentEdits.getState().record(docId, edit),
    update: (docId, body) => useDocuments.getState().update(docId, { body }),
    now: () => Date.now(),
    // B5's dry run. The same function the figure editor renders with, so an
    // agent's body is judged by exactly what the user's pane will run — not by
    // a second, kinder validator that agrees with it until it does not.
    renderFigure,
    // `author_render`. `hasLiveRender` is read separately from the render itself
    // so the executor can tell "no editor is open" from "the editor could not
    // draw this" — two facts with different recoveries.
    hasLiveRender,
    renderDocument,
  };
}

/// Subscribe to main's author-bridge channel (called once from main.tsx). A
/// no-op outside the Electron shell: a browser build has no bridge server, so
/// no agent can reach these documents at all.
export function initAuthorBridge(): void {
  if (!isShell()) return;
  void listen<unknown>(AUTHOR_REQUEST_EVENT, (event) => {
    const req = asAuthorRequest(event.payload);
    // No usable correlation id means there is nowhere to reply; main's own
    // deadline is what ends that call.
    if (req === null) return;
    // The executor awaits B5's dry-run render, so the reply is sent from a
    // continuation. `catch` covers both halves — a synchronous throw and a
    // rejected promise — because a store or parser that throws is an INTERNAL
    // refusal, never a silent hang: main would otherwise park until its
    // deadline and report a timeout, which reads to the agent as "the desktop
    // is busy" when the truth is "this request cannot be served".
    void executeAuthorRequest(req, io())
      .catch(
        (e: unknown): AuthorResult => ({
          ok: false,
          code: 'INTERNAL',
          message: e instanceof Error ? e.message : String(e),
        }),
      )
      .then((result) => invoke(AUTHOR_RESULT_COMMAND, { id: req.id, result }));
  });
}
