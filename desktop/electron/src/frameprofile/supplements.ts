/// Driver-side supplements — the imperative bits a frame profile cannot
/// express, ported alongside the interpreter (vision-parity D-7).
///
/// A profile renames **fields**; it never renames **values**, because the
/// expression grammar has no comparisons or ternaries by design
/// (`docs/reference/frame-profiles.md` §3). So every engine whose enum spelling
/// differs from the vocabulary's needs one line of code, and that line has to
/// exist on both sides of the parity boundary or the two produce different
/// transcripts from identical rules.
///
/// **What is here and what is not.** Only the *pure* supplements live in this
/// module — the ones that are a function of their input alone. Codex's other
/// two, `finishPlanEvent`'s chain root and the turn clock, are per-session
/// mutable state (a message id held across a turn's N plan snapshots, a start
/// timestamp keyed to a turn id). They belong to whatever owns the session, so
/// they land with the codex driver in L4 rather than here, where there is no
/// session to hold them.
///
/// Go counterparts: `hub/internal/hostrunner/driver_appserver.go`.

/// Map codex's `TurnPlanStepStatus` spelling onto the vocabulary's.
///
/// The enum is `{pending, inProgress, completed}` (codex-cli 0.133.0
/// app-server schema); ACP got to `plan` first and spells the middle state
/// `in_progress`, which is what both clients' renderers and the Todos rollup
/// match on. Two of the three already agree, so this is one rename — but the
/// one that would fail silently: an unrecognized status renders as an unstarted
/// step, so a running step would have shown as not-yet-started on every codex
/// turn.
///
/// Unknown values pass through untouched. A future codex status this build has
/// never seen is data we shouldn't rewrite; the clients treat it as "not
/// started", which is the safe read.
export function canonicalPlanStatus(status: string): string {
  if (status === 'inProgress') return 'in_progress';
  return status;
}

/// Apply `canonicalPlanStatus` to every entry of a `plan` payload the profile
/// built, in place, and return it.
///
/// This is the half of the Go driver's `finishPlanEvent` that carries no
/// session state. The caller — a driver, in L4 — still owes the payload its
/// `message_id` and `partial` chain root; without those a turn's N plan
/// snapshots render as N cards instead of one that updates.
///
/// Entries that aren't objects, and statuses that aren't strings, are left
/// alone rather than coerced: a projection that produced something unexpected
/// is a profile bug to surface, not one to paper over here.
export function canonicalizePlanEntries(
  payload: Record<string, unknown> | null | undefined,
): Record<string, unknown> | null | undefined {
  const entries = payload?.entries;
  if (!Array.isArray(entries)) return payload;
  for (const raw of entries) {
    if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) continue;
    const entry = raw as Record<string, unknown>;
    if (typeof entry.status === 'string') {
      entry.status = canonicalPlanStatus(entry.status);
    }
  }
  return payload;
}
