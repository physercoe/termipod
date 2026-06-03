// Human-friendly labels for the raw agent lifecycle status. The hub stores the
// DB jargon ('terminated', 'pending', …); surfaces should show a word that
// matches the Stop/Archive action vocabulary instead of leaking the column
// value onto a row. One place so the wording can't fork across screens.

/// Friendly label for a raw agent `status`. Unknown values pass through.
String agentStatusLabel(String status) {
  switch (status) {
    case 'running':
      return 'running';
    case 'idle':
      return 'idle';
    case 'pending':
      return 'starting';
    case 'paused':
      return 'paused';
    case 'terminated':
      // The worker process was killed (via Stop or Archive). 'terminated' is DB
      // jargon; the glossary's principal-facing words are Stop/Archive. With no
      // session context the conservative reading is the permanent one, so this
      // bare label is 'archived' — callers that know the session fate use
      // [agentStatusLabelResumable] to show 'stopped' (resumable) instead.
      return 'archived';
    case 'failed':
      return 'failed';
    case 'crashed':
      return 'crashed';
    case 'archived':
      return 'archived';
    default:
      return status;
  }
}

/// True for the error end-states that shouldn't appear in a switch-to-analyse
/// roster (a crash/failure isn't a run you'd want to open). A clean finish
/// ('terminated') is reviewable and is NOT excluded.
bool agentIsCrashedOrFailed(String status) =>
    status == 'crashed' || status == 'failed';

/// Whether a *terminal* agent's session can be resumed. Stop and Archive BOTH
/// leave `agents.status = 'terminated'` (handlers_agents.go) — the fate that
/// distinguishes them lives on the SESSION: Stop pauses it (resumable via
/// Resume session), Archive archives it (permanent, fork-only — Resume 409s).
/// So the agent row alone can't say "stopped" vs "ended"; callers resolve the
/// session status and pass it through [agentResumability].
enum AgentResumability {
  /// Session is paused — Resume session respawns into it (keeps history).
  resumable,

  /// Session is archived/gone — permanent, Resume would 409. Fork-only.
  permanent,

  /// No session info to hand (cold list) — fall back to the legacy wording
  /// and let the hub 409 if a resume is attempted.
  unknown,
}

/// Map a session `status` to the resumability it implies for the agent that
/// fronted it. See [AgentResumability].
AgentResumability agentResumability(String sessionStatus) {
  switch (sessionStatus) {
    case 'paused':
    case 'interrupted':
      return AgentResumability.resumable;
    case 'archived':
    case 'deleted':
      return AgentResumability.permanent;
    default:
      return AgentResumability.unknown;
  }
}

/// Friendly label for an agent row that folds in the session's fate, using the
/// glossary's principal-facing lifecycle words (Stop / Archive) so a
/// `terminated` row reads as the *resumability* the user cares about rather
/// than the ambiguous "ended": a Stop (session paused) shows "stopped"
/// (resumable); an Archive (session archived) — or an unknown session, read
/// conservatively as permanent — shows "archived". Every non-terminated status
/// defers to [agentStatusLabel].
String agentStatusLabelResumable(String status, AgentResumability r) {
  if (status == 'terminated' && r == AgentResumability.resumable) {
    return 'stopped';
  }
  return agentStatusLabel(status);
}
