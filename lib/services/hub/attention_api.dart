import 'hub_read_through.dart';
import 'hub_transport.dart';

/// Attention items — the director's queue of things needing a decision.
/// Listing (live + cached), the decide/resolve actions, and the
/// originating-context fetch behind the approval-detail screen. Wedge W5
/// of `docs/plans/hub-client-split.md`.
class AttentionApi {
  final HubTransport _t;
  AttentionApi(this._t);

  Future<List<Map<String, dynamic>>> listAttention({
    String? status,
    bool includeEscalated = false,
  }) =>
      _t.listJson(
        '/v1/teams/${_t.cfg.teamId}/attention',
        query: _attentionQuery(status, includeEscalated),
      );

  static Map<String, String>? _attentionQuery(String? status, bool includeEscalated) {
    if (status == null && !includeEscalated) return null;
    final q = <String, String>{};
    if (status != null) q['status'] = status;
    if (includeEscalated) q['include_escalated'] = 'true';
    return q;
  }

  Future<CachedResponse<List<Map<String, dynamic>>>> listAttentionCached({
    String? status,
    bool includeEscalated = false,
  }) {
    final q = _attentionQuery(status, includeEscalated);
    return readThrough<List<Map<String, dynamic>>>(
      cache: _t.snapshotCache,
      hubKey: _t.cacheHubKey,
      endpoint: buildEndpointKey('/v1/teams/${_t.cfg.teamId}/attention', q),
      fetch: () => listAttention(status: status, includeEscalated: includeEscalated),
      decode: _t.decodeListMaps,
    );
  }

  Future<Map<String, dynamic>> decideAttention(
    String id, {
    required String decision,
    String? by,
    String? reason,
    String? optionId,
    /// Free-text reply for kind='help_request' attentions (request_help
    /// MCP tool). Required when decision='approve' on a help_request.
    String? body,
    /// ADR-030 W9 principal-override flag. When `true`, the hub re-enters
    /// the dispatcher with a Rollback call against the original Apply.
    /// The hub's validation accepts `decision='override'` only when
    /// paired with `override=true`; it also accepts `decision='approve'`
    /// + `override=true` as a synonym, but mobile passes
    /// `decision='override'` explicitly so the decisions_json audit
    /// trail reads honestly.
    bool override = false,
  }) async {
    final req = <String, dynamic>{'decision': decision};
    if (by != null && by.isNotEmpty) req['by'] = by;
    if (reason != null && reason.isNotEmpty) req['reason'] = reason;
    if (optionId != null && optionId.isNotEmpty) req['option_id'] = optionId;
    if (body != null && body.isNotEmpty) req['body'] = body;
    if (override) req['override'] = true;
    final out = await _t.post('/v1/teams/${_t.cfg.teamId}/attention/$id/decide', req);
    return (out as Map).cast<String, dynamic>();
  }

  /// Fetches the originating context for an attention item — the
  /// session it was raised from, the agent that raised it, and the
  /// last 10 transcript turns leading up to the request. Drives the
  /// approval-detail screen's transcript block + "Open in chat" jump.
  /// Returns shape `{attention_id, session_id?, agent_id?, agent_handle?, events: [...]}`
  /// where `events` is newest-first (seq DESC). Empty `events` means
  /// the attention was system-originated or pre-dates the
  /// session_id population (v1.0.336).
  Future<Map<String, dynamic>> getAttentionContext(String id) async {
    final out = await _t.get('/v1/teams/${_t.cfg.teamId}/attention/$id/context');
    return (out as Map).cast<String, dynamic>();
  }

  Future<Map<String, dynamic>> resolveAttention(
    String id, {
    String? by,
    String? reason,
  }) async {
    final body = <String, dynamic>{};
    if (by != null && by.isNotEmpty) body['by'] = by;
    if (reason != null && reason.isNotEmpty) body['reason'] = reason;
    final out = await _t.post('/v1/teams/${_t.cfg.teamId}/attention/$id/resolve', body);
    return (out as Map).cast<String, dynamic>();
  }
}
