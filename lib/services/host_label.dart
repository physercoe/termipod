/// Resolves a raw `host_id` into a human-friendly label by looking up
/// the host's display name in the cached hosts list (typically
/// `ref.read(hubProvider).value?.hosts`).
///
/// Returns:
///   - The host's `name` field when found and non-empty.
///   - The first 8 chars of the id with a leading `host:` tag when the
///     id resolves to a host with no name.
///   - `null` when [hostId] is empty or the id isn't in [hosts] at all
///     (caller decides whether to render anything).
///
/// Surfaces that need the name even when the host record is missing
/// (e.g. a stale agent pointing at a deleted host) can fall back to
/// the raw id themselves; this helper deliberately doesn't invent a
/// label, so a missing host row stays visible to the operator.
String? hostLabel(List<Map<String, dynamic>> hosts, String? hostId) {
  if (hostId == null || hostId.isEmpty) return null;
  for (final h in hosts) {
    if ((h['id'] ?? '').toString() != hostId) continue;
    final name = (h['name'] ?? '').toString().trim();
    if (name.isNotEmpty) return name;
    return 'host:${hostId.length > 8 ? hostId.substring(0, 8) : hostId}';
  }
  return null;
}
