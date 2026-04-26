// Liveness classifier for the team steward agent.
//
// A wedged claude process keeps `status='running'` on the hub but stops
// emitting `agent_events`, so a binary present/absent signal can show
// green forever on a dead steward. The hub now exposes
// `last_event_at` (MAX(ts) over agent_events for that agent); we
// classify the chip state on `(status, age(last_event_at))`.
//
// Thresholds are placeholders pending real-session telemetry.

enum StewardLiveness {
  /// status=running, last event within [healthyWindow]. Green.
  healthy,

  /// status=running, last event 2–10 min old. Amber — might still be
  /// alive, might be wedged.
  idle,

  /// status=running, last event >10 min old. Red — almost certainly
  /// broken; user should recreate via 2b.
  stuck,

  /// status=pending — host-runner hasn't picked up the spawn yet. Grey.
  starting,

  /// No agent with handle=='steward', or it's terminated/failed/archived.
  /// Grey "No steward" — tap to spawn a new one.
  none,
}

/// Threshold below which a `running` steward is considered healthy.
const Duration healthyWindow = Duration(minutes: 2);

/// Threshold above which a `running` steward is considered stuck.
///
/// 30 min is a band-aid: the real signal we want is "wedged vs.
/// idle-waiting-for-the-user", which requires correlating
/// `last_event_at` with the last user input. Without that, the only
/// safe move is to keep the bar high enough that an idle steward
/// (sitting on the user's last reply with nothing to do) doesn't get
/// flagged red after a coffee break. Revisit when we track per-session
/// last-input-time.
const Duration stuckWindow = Duration(minutes: 30);

/// Classifies team-level steward liveness from the agents-list payload.
///
/// `agents` is the `hub_state.agents` list — each row is the JSON map
/// returned by `GET /v1/teams/{team}/agents`. A team can have multiple
/// agents but only one is `handle == 'steward'`.
StewardLiveness stewardLiveness(
  List<Map<String, dynamic>> agents, {
  DateTime? now,
}) {
  final clock = now ?? DateTime.now().toUtc();
  for (final a in agents) {
    if ((a['handle'] ?? '').toString() != 'steward') continue;
    final status = (a['status'] ?? '').toString();
    if (status == 'pending') return StewardLiveness.starting;
    if (status != 'running') continue; // terminated/failed/archived → none
    final raw = (a['last_event_at'] ?? '').toString();
    if (raw.isEmpty) {
      // Running but the hub has never seen an event from it. Treat as
      // starting — the agent is between status='running' and its first
      // session.init / text frame.
      return StewardLiveness.starting;
    }
    final ts = DateTime.tryParse(raw);
    if (ts == null) return StewardLiveness.idle; // unparseable → cautious
    final age = clock.difference(ts.toUtc());
    if (age <= healthyWindow) return StewardLiveness.healthy;
    if (age >= stuckWindow) return StewardLiveness.stuck;
    return StewardLiveness.idle;
  }
  return StewardLiveness.none;
}
