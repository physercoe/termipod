/// Pure list-shaping for the Admin → Teams tab, extracted into a
/// non-widget seam so it can be unit-tested without a hub client (the
/// agent_feed / WS2 controller-seam precedent). The widget owns the
/// network calls; this owns the ordering and the active-team mark.
library;

/// Orders teams for display: the caller's currently-active team first,
/// then the rest alphabetically by name (case-insensitive), falling back
/// to id. Input rows are `{id, name, created_at}` JSON maps; the returned
/// list is a new list (the caller's is never mutated).
List<Map<String, dynamic>> sortTeamsForDisplay(
  List<Map<String, dynamic>> teams,
  String activeTeamId,
) {
  final out = List<Map<String, dynamic>>.from(teams);
  out.sort((a, b) {
    final aActive = (a['id'] ?? '') == activeTeamId;
    final bActive = (b['id'] ?? '') == activeTeamId;
    if (aActive != bActive) return aActive ? -1 : 1;
    final an = _displayKey(a);
    final bn = _displayKey(b);
    final c = an.compareTo(bn);
    if (c != 0) return c;
    return (a['id'] ?? '').toString().compareTo((b['id'] ?? '').toString());
  });
  return out;
}

/// Sort key: lowercased display name, falling back to the id when a team
/// has no name set.
String _displayKey(Map<String, dynamic> team) {
  final name = (team['name'] ?? '').toString().trim();
  final key = name.isNotEmpty ? name : (team['id'] ?? '').toString();
  return key.toLowerCase();
}

/// True when [team] is the hub-profile's currently-bound team.
bool isActiveTeam(Map<String, dynamic> team, String activeTeamId) =>
    (team['id'] ?? '') == activeTeamId;
