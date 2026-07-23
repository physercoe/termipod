/// The per-event fold both transcript surfaces need (ADR-040 substrate): it
/// turns the flat event list into the lineage maps a card renders from and the
/// lens predicates read. Pure over an event list — no widget, no mode, so it is
/// shared by `LiveFeed` and `InsightTranscript` alike and is unit-testable
/// without a widget tree.
///
/// Lifted verbatim from `_AgentFeedState.build()` (the first scan over
/// `_events`); the behaviour is byte-identical, only its home changed.
library;

/// The call-side tool id of a `tool_call` payload: prefer `tool_use_id`
/// (what the claude-code log-tail mapper writes — mapper.go `tool_use` arm),
/// then `id` (the live stdio / ACP drivers), then `toolCallId`. Desktop
/// parity: toolGroups.ts `callToolId`. Results key on `tool_use_id` and
/// updates on `toolCallId` — the SAME underlying id under different names —
/// so without the first arm a log-tailed claude-code session never pairs its
/// calls: names, folded status, error classification and group rows all miss.
String callToolIdOf(Map<dynamic, dynamic> payload) =>
    (payload['tool_use_id'] ?? payload['id'] ?? payload['toolCallId'] ?? '')
        .toString();

class FoldMaps {
  /// tool_call `id` → tool name, so a tool_result card can show
  /// "tool: git_log" in its header instead of a bare id. Only `tool_call`
  /// events with both an id and a name contribute.
  final Map<String, String> toolNames;

  /// request_id → decision, for approval requests the user has already
  /// answered (an `input.approval`): the matching approval_request card then
  /// renders resolved instead of offering the buttons again.
  final Map<String, String> resolvedApprovals;

  /// toolCallId → the latest `tool_call_update` payload, folded into the parent
  /// tool_call card. Individual update events are hidden from the feed —
  /// rendering every progress tick floods the list.
  final Map<String, Map<String, dynamic>> toolUpdates;

  /// tool_use_id → the full `tool_result` event row (so a card can surface its
  /// ts too). The tool_call card pulls its matching result from here; bare
  /// tool_result cards drop out of the feed because the lineage now lives
  /// inside one card per call.
  final Map<String, Map<String, dynamic>> toolResults;

  const FoldMaps({
    required this.toolNames,
    required this.resolvedApprovals,
    required this.toolUpdates,
    required this.toolResults,
  });

  /// Single forward scan over [events] (the loaded window). Cheap — the feed is
  /// O(dozens) of events — so callers may rebuild it each frame.
  factory FoldMaps.fromEvents(List<Map<String, dynamic>> events) {
    final toolNames = <String, String>{};
    final resolvedApprovals = <String, String>{};
    final toolUpdates = <String, Map<String, dynamic>>{};
    final toolResults = <String, Map<String, dynamic>>{};
    for (final e in events) {
      final kind = (e['kind'] ?? '').toString();
      final p = e['payload'];
      if (p is! Map) continue;
      if (kind == 'tool_call') {
        final id = callToolIdOf(p);
        final name = p['name']?.toString() ?? '';
        if (id.isNotEmpty && name.isNotEmpty) toolNames[id] = name;
      } else if (kind == 'tool_call_update') {
        final id = (p['toolCallId'] ?? p['tool_call_id'] ?? '').toString();
        if (id.isNotEmpty) toolUpdates[id] = p.cast<String, dynamic>();
      } else if (kind == 'tool_result') {
        final id = p['tool_use_id']?.toString() ?? '';
        if (id.isNotEmpty) toolResults[id] = e.cast<String, dynamic>();
      } else if (kind == 'input.approval') {
        final rid = p['request_id']?.toString() ?? '';
        final dec = p['decision']?.toString() ?? '';
        if (rid.isNotEmpty) resolvedApprovals[rid] = dec;
      }
    }
    return FoldMaps(
      toolNames: toolNames,
      resolvedApprovals: resolvedApprovals,
      toolUpdates: toolUpdates,
      toolResults: toolResults,
    );
  }
}
