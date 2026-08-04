/// The `author_*` consent policy — electron-free, so `node --test` (and CI,
/// which runs this package's suite) pins the parts that decide whether an
/// agent may write into the user's document (coworking A3/A4; ADR-064).
///
/// The Electron halves live in `author_host.ts`: the renderer round trip, the
/// keychain-backed hub leg, the provider registration. What is here is the
/// judgement:
///
///   - the CARD an `author_apply` raises, and the two answers it offers —
///     *allow once* and *allow this document for this session*;
///   - the LEASE that the second answer creates, keyed per (agent, document)
///     because that is the sentence the user agreed to. A per-agent lease
///     would let "yes, edit my draft" become "yes, edit anything I open";
///   - the WORDING an agent sees on each outcome, including the honest
///     `applied_store_only` sentence — a write that reached the store but not
///     the mounted editor must not be reported as one the user can see.
///
/// The hub leg is deliberately NOT lease-bearing. A hub-relayed action is
/// approval-gated hub-side, but that grant is per (class, agent, desktop) —
/// it has no document in it, so it cannot stand for a per-document decision
/// (`desktopUIGrantable` refuses `author_apply` a session grant for exactly
/// this reason). Remote applies are carded every time, by the hub, naming the
/// document in the card's args.

/// How the write landed. The A4 ladder, with the two rungs W1 can actually
/// distinguish:
///
///   - `applied_live`       the user is looking at the new document: either an
///                          adapter took it (draw.io, canvas) or the kind's
///                          editor re-renders from `body` (markdown, figure);
///   - `applied_store_only` the document holds it, the mounted editor does not
///                          (table until B4, excalidraw until B3, or the doc
///                          is not open at all).
///
/// The plan's third rung, `applied_via_remount`, is not implemented and not
/// reported: nothing here remounts an editor, and a state nobody produces is
/// a promise in a tool description rather than a result.
export type AuthorApplyState = 'applied_live' | 'applied_store_only';

// ── The session lease ────────────────────────────────────────────────────────

/// Grants an `author_apply` may ride instead of raising a card, keyed per
/// (agent, document). In memory for the app run only — a lease is a
/// convenience within one sitting, never a durable permission, so restarting
/// the desktop asks again.
export class AuthorLeaseStore {
  private readonly held = new Set<string>();

  private static key(agentId: string, documentId: string): string {
    // NUL separates: it cannot occur in either id, so no pair of distinct
    // (agent, document) can collide into one key.
    return `${agentId}\u0000${documentId}`;
  }

  /// An empty agent id never holds a lease. Ad-hoc callers arrive without one
  /// (the audit row writes 'unknown'), and letting them share a single
  /// anonymous lease would make one user's "allow this session" answer apply
  /// to every unidentified caller after it.
  has(agentId: string, documentId: string): boolean {
    if (agentId === '' || documentId === '') return false;
    return this.held.has(AuthorLeaseStore.key(agentId, documentId));
  }

  grant(agentId: string, documentId: string): void {
    if (agentId === '' || documentId === '') return;
    this.held.add(AuthorLeaseStore.key(agentId, documentId));
  }

  /// Drop every lease this agent holds — Settings → Remote driving "Revoke",
  /// which must mean the agent no longer touches this desktop at all.
  revokeAgent(agentId: string): void {
    const prefix = `${agentId}\u0000`;
    for (const k of this.held) {
      if (k.startsWith(prefix)) this.held.delete(k);
    }
  }

  /// Drop everything — the sharing toggle went off. The toggle is the consent
  /// that every desktop-UI capability hangs from, so turning it off cannot
  /// leave standing grants behind for the next time it goes on.
  clear(): void {
    this.held.clear();
  }

  size(): number {
    return this.held.size;
  }
}

/// The app's leases. A module singleton rather than a field on the bridge deps
/// because two modules that must not import `author_host.ts` have to clear it:
/// the sharing toggle (`desktopui.ts`) and the remote-driving revoke
/// (`browserbridge_host.ts`). Both import this file, which imports nothing.
export const authorLeases = new AuthorLeaseStore();

// ── The card ─────────────────────────────────────────────────────────────────

export interface AuthorApprovalRequest {
  agentId: string;
  agentHandle: string;
  documentId: string;
  title: string;
  kind: string;
  mode: string;
  reason: string;
  /// Size of the body about to be committed — the one number that tells a user
  /// "this is a tweak" from "this replaces the document".
  bytes: number;
  /// How many structured edits, for `mode:'ops'` (D1). Zero for the whole-body
  /// modes. A count and a byte size answer different questions here: "12 edits"
  /// is what the user is agreeing to, and 400 bytes of op payload would read as
  /// a trivial change.
  operations?: number;
}

/// The `desktop_action` card an `author_apply` parks on. `session_grant: true`
/// is what makes the client render the second button; the summary names the
/// DOCUMENT because that is the scope the second button grants.
export function authorApprovalCard(req: AuthorApprovalRequest): { summary: string; payload: Record<string, unknown> } {
  const who = req.agentHandle !== '' ? req.agentHandle : req.agentId !== '' ? req.agentId : 'An agent';
  const ops = req.operations ?? 0;
  const what = req.mode === 'append' ? 'append to' : req.mode === 'ops' ? 'edit' : 'rewrite';
  // What the user is agreeing to, in the mode's own terms. "Rewrite (12000
  // bytes)" and "edit (3 changes)" are the two very different things
  // `author_apply` can mean, and a card that reports bytes for both makes an op
  // batch look like the smaller change when it may not be.
  const size = req.mode === 'ops' ? `${String(ops)} change${ops === 1 ? '' : 's'}` : `${String(req.bytes)} bytes`;
  const title = req.title !== '' ? req.title : 'an untitled document';
  return {
    summary: `${who} wants to ${what} your ${req.kind} document “${title}” (${size})`,
    payload: {
      tool: 'author_apply',
      document_id: req.documentId,
      title: req.title,
      kind: req.kind,
      mode: req.mode,
      bytes: req.bytes,
      ...(req.mode === 'ops' ? { operations: ops } : {}),
      // Agent-authored free text. Kept because it is the most useful thing on
      // the card, clipped because a card is one line and an agent's `reason`
      // is not bounded by anything upstream.
      reason: req.reason.length > 200 ? `${req.reason.slice(0, 200)}…` : req.reason,
      agent_id: req.agentId,
      // The grant this card can create: THIS document, this session. The
      // payload states it so a client renders the button it can honour rather
      // than inferring one from the kind.
      session_grant: true,
      session_grant_scope: 'document',
    },
  };
}

/// The refusal sentence, by cause. Here (not in the host) so both legs read
/// identically and the wording is pinned by a test rather than eyeballed.
export function authorDenialMessage(cause: 'denied' | 'timeout' | 'unavailable' | 'raise_failed'): string {
  switch (cause) {
    case 'denied':
      return 'the desktop user declined this edit';
    case 'timeout':
      return 'no decision on the edit request within the approval window — denied';
    case 'unavailable':
      return (
        'this desktop cannot ask for edit approval — it is not signed in to a hub, and author_apply is ' +
        'approved by the user, always. Use author_read to look, and hand the change to the user as text'
      );
    case 'raise_failed':
      return 'the hub did not accept the approval card for this edit — denied (transient hub error; retrying may work)';
  }
}

// ── What the agent is told ───────────────────────────────────────────────────

export interface AuthorApplyOutcome {
  documentId: string;
  title: string;
  kind: string;
  state: AuthorApplyState;
  bytes: number;
  /// What the renderer knows and the byte count cannot say — for D1, which
  /// cells the batch touched and which the CASCADE took. An agent that deletes
  /// one box and reports "removed the box" is not lying on purpose when five
  /// edges went with it; it simply was not told.
  note?: string;
}

/// The `author_apply` answer. It says what the USER can see, not merely what
/// the tool did: `applied_store_only` is a success that the person on the
/// other side may not have noticed yet, and an agent that reports "done" from
/// it without saying so is the exact dishonesty A4 exists to prevent.
export function applyResultText(out: AuthorApplyOutcome): string {
  const what = out.note !== undefined && out.note !== '' ? `${out.note} — ${String(out.bytes)} bytes` : `${String(out.bytes)} bytes`;
  const head = `${out.state}: ${what} written to ${out.kind} document “${out.title}” (${out.documentId})`;
  if (out.state === 'applied_live') {
    return `${head}. The user is looking at the new version; their undo (Cmd+Z, or the agent-edit chip on the document tab) puts the old one back.`;
  }
  return (
    `${head}. The document now holds it, but the editor on screen still shows the previous version — ` +
    `this kind's editor does not yet take a live write, so tell the user to reopen the tab to see it. ` +
    `The agent-edit chip on the document tab reverts it.`
  );
}

/// One line per open document for the `author_read` answer's index. Bounded by
/// how many tabs a person keeps open, so it is not windowed.
export interface AuthorDocLine {
  id: string;
  kind: string;
  title: string;
  active: boolean;
}

export function documentIndexText(docs: readonly AuthorDocLine[]): string {
  if (docs.length === 0) return '(no documents are open in Author)';
  return docs.map((d) => `- ${d.id} [${d.kind}]${d.active ? ' (active)' : ''} ${d.title}`).join('\n');
}
