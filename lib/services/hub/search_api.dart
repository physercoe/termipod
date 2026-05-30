import 'hub_read_through.dart';
import 'hub_transport.dart';

/// Full-text event search and the team audit-event feed (both read-only
/// query surfaces over the hub's event/audit tables). Wedge W3 of
/// `docs/plans/hub-client-split.md`.
class SearchApi {
  final HubTransport _t;
  SearchApi(this._t);

  /// Full-text search over event parts. Returns matching events newest-
  /// first, each with `id`, `received_ts`, `channel_id`, `type`, `from_id`,
  /// and `parts` (the original event parts JSON). The hub uses SQLite
  /// FTS5; the `q` string accepts FTS5 match syntax.
  Future<List<Map<String, dynamic>>> searchEvents(
    String q, {
    int? limit,
  }) {
    final query = <String, String>{'q': q};
    if (limit != null) query['limit'] = '$limit';
    return _t.listJson('/v1/search', query: query);
  }

  /// Lists audit events for the configured team, newest first. Each row
  /// has `id`, `ts`, `actor_kind`, `actor_handle`, `action`, `target_kind`,
  /// `target_id`, `summary`, and optional `meta`.
  ///
  /// [action] filters to an exact action string (e.g. `agent.spawn`).
  /// [since] is an ISO-8601 UTC timestamp lower bound. [limit] is clamped
  /// to 500 by the server. [projectId] scopes to one project (W2 Activity
  /// feed): includes rows whose target is the project plus rows whose
  /// meta carries a matching `project_id`.
  Future<List<Map<String, dynamic>>> listAuditEvents({
    String? action,
    String? since,
    String? projectId,
    int? limit,
  }) {
    final query = <String, String>{};
    if (action != null && action.isNotEmpty) query['action'] = action;
    if (since != null && since.isNotEmpty) query['since'] = since;
    if (projectId != null && projectId.isNotEmpty) {
      query['project_id'] = projectId;
    }
    if (limit != null) query['limit'] = '$limit';
    return _t.listJson(
      '/v1/teams/${_t.cfg.teamId}/audit',
      query: query.isEmpty ? null : query,
    );
  }

  /// Read-through variant of [listAuditEvents]; see [HubClient.listRunsCached]
  /// for the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listAuditEventsCached({
    String? action,
    String? since,
    String? projectId,
    int? limit,
  }) {
    final query = <String, String>{};
    if (action != null && action.isNotEmpty) query['action'] = action;
    if (since != null && since.isNotEmpty) query['since'] = since;
    if (projectId != null && projectId.isNotEmpty) {
      query['project_id'] = projectId;
    }
    if (limit != null) query['limit'] = '$limit';
    return readThrough<List<Map<String, dynamic>>>(
      cache: _t.snapshotCache,
      hubKey: _t.cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${_t.cfg.teamId}/audit',
        query.isEmpty ? null : query,
      ),
      fetch: () => listAuditEvents(
        action: action,
        since: since,
        projectId: projectId,
        limit: limit,
      ),
      decode: _t.decodeListMaps,
    );
  }
}
