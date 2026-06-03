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
      // The worker process ended (via Stop or Archive). "terminated" is DB
      // jargon; "ended" reads cleanly and doesn't falsely claim archived.
      return 'ended';
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
