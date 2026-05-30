import 'dart:convert';
import 'dart:io';

import 'hub_read_through.dart';
import 'hub_transport.dart';

/// Hub-level ("the hub itself", not a project/agent/session entity)
/// endpoints: probe + self-stats, the scope-parameterized insights
/// aggregator, owner-only token management, and the hub-wide governance
/// config (`roles.yaml`). Wedge W2 of `docs/plans/hub-client-split.md`.
class SystemApi {
  final HubTransport _t;
  SystemApi(this._t);

  // ---- info / probe ----

  /// Probe the hub. Doesn't require a token, so we use it from the bootstrap
  /// wizard to validate the URL before the user pastes the token.
  Future<Map<String, dynamic>> getInfo() async {
    final out = await _t.get('/v1/_info', auth: false);
    return (out as Map).cast<String, dynamic>();
  }

  /// Probe with auth — fails fast if the token is wrong. Uses /hosts because
  /// it's cheap and always exists for a valid team.
  Future<void> verifyAuth() async {
    await _t.get('/v1/teams/${_t.cfg.teamId}/hosts');
  }

  /// Hub-self capacity report — machine, DB, and live counts. Backs the
  /// Hub group on the Hosts tab + the Hub Detail screen (ADR-022 D2 /
  /// insights-phase-1.md W1). Authed but not team-scoped: the hub box
  /// is shared across teams, so the endpoint sits at /v1/hub/stats
  /// rather than /v1/teams/{team}/hosts/...
  Future<Map<String, dynamic>> getHubStats() async {
    final out = await _t.get('/v1/hub/stats');
    return (out as Map).cast<String, dynamic>();
  }

  /// Scope-parameterized insights aggregator (ADR-022 D3,
  /// insights-phase-1 W2 + insights-phase-2 W1). Returns Tier-1
  /// dimensions — spend / latency / errors / concurrency — summed
  /// across `agent_events` filtered by the requested scope (project /
  /// team / agent / engine / host) and the optional time range. The
  /// hub caches the response with a 30s TTL keyed on
  /// (scope_kind, scope_id, since, until); the panel layers a
  /// snapshot cache on top per ADR-006.
  ///
  /// Exactly one of the *Id / engine params must be set — the hub
  /// 400s otherwise. We intentionally don't enumerate `InsightsScope`
  /// here; Dart-side it's the `InsightsScope` value object in
  /// providers/insights_provider.dart that builds the q-param.
  Future<Map<String, dynamic>> getInsights({
    String? projectId,
    String? teamId,
    String? agentId,
    String? engine,
    String? hostId,
    bool stewardOnly = false,
    DateTime? since,
    DateTime? until,
  }) async {
    final q = _insightsScopeQuery(
      projectId: projectId,
      teamId: teamId,
      agentId: agentId,
      engine: engine,
      hostId: hostId,
    );
    if (stewardOnly) q['kind'] = 'steward';
    if (since != null) q['since'] = since.toUtc().toIso8601String();
    if (until != null) q['until'] = until.toUtc().toIso8601String();
    final out = await _t.get('/v1/insights', query: q);
    return (out as Map).cast<String, dynamic>();
  }

  /// Read-through variant of [getInsights]; same offline-fallback
  /// contract as [HubClient.listHostsCached]. The endpoint key folds the
  /// scope pair + since + until into the cache row so reads at different
  /// scopes don't shadow each other.
  Future<CachedResponse<Map<String, dynamic>>> getInsightsCached({
    String? projectId,
    String? teamId,
    String? agentId,
    String? engine,
    String? hostId,
    bool stewardOnly = false,
    DateTime? since,
    DateTime? until,
  }) {
    final q = _insightsScopeQuery(
      projectId: projectId,
      teamId: teamId,
      agentId: agentId,
      engine: engine,
      hostId: hostId,
    );
    if (stewardOnly) q['kind'] = 'steward';
    if (since != null) q['since'] = since.toUtc().toIso8601String();
    if (until != null) q['until'] = until.toUtc().toIso8601String();
    return readThrough<Map<String, dynamic>>(
      cache: _t.snapshotCache,
      hubKey: _t.cacheHubKey,
      endpoint: buildEndpointKey('/v1/insights', q),
      fetch: () => getInsights(
        projectId: projectId,
        teamId: teamId,
        agentId: agentId,
        engine: engine,
        hostId: hostId,
        stewardOnly: stewardOnly,
        since: since,
        until: until,
      ),
      decode: _t.decodeMap,
    );
  }

  // _insightsScopeQuery builds the {scope_param: id} singleton map.
  // Exactly one of the params must be non-empty — the hub enforces
  // this too, but failing fast on the client surfaces caller bugs
  // synchronously instead of as a 400 round-trip.
  Map<String, String> _insightsScopeQuery({
    String? projectId,
    String? teamId,
    String? agentId,
    String? engine,
    String? hostId,
  }) {
    final entries = <String, String>{};
    if (projectId != null && projectId.isNotEmpty) entries['project_id'] = projectId;
    if (teamId != null && teamId.isNotEmpty) entries['team_id'] = teamId;
    if (agentId != null && agentId.isNotEmpty) entries['agent_id'] = agentId;
    if (engine != null && engine.isNotEmpty) entries['engine'] = engine;
    if (hostId != null && hostId.isNotEmpty) entries['host_id'] = hostId;
    if (entries.length != 1) {
      throw ArgumentError(
          'getInsights requires exactly one of projectId/teamId/agentId/engine/hostId; got $entries');
    }
    return entries;
  }

  // ---- tokens (owner-only) ----

  /// Lists all tokens for the team (metadata only, no plaintext). Requires
  /// the caller to hold an owner-kind token; non-owners get 403.
  Future<List<Map<String, dynamic>>> listTokens() =>
      _t.listJson('/v1/teams/${_t.cfg.teamId}/tokens');

  /// Issues a new token and returns the plaintext exactly once. Treat the
  /// response's `plaintext` field as write-once: show it to the user, then
  /// drop it from memory — the hub never stores it.
  Future<Map<String, dynamic>> issueToken({
    String kind = 'user',
    String role = 'principal',
    String? handle,
    String? expiresAt,
  }) async {
    final body = <String, dynamic>{'kind': kind, 'role': role};
    if (handle != null && handle.isNotEmpty) body['handle'] = handle;
    if (expiresAt != null && expiresAt.isNotEmpty) {
      body['expires_at'] = expiresAt;
    }
    final out = await _t.post('/v1/teams/${_t.cfg.teamId}/tokens', body);
    return (out as Map).cast<String, dynamic>();
  }

  Future<void> revokeToken(String id) async {
    await _t.post(
      '/v1/teams/${_t.cfg.teamId}/tokens/$id/revoke',
      const <String, dynamic>{},
    );
  }

  // ---- hub-wide governance config (owner-only) ----

  /// Fetches the active operation-scope manifest (`roles.yaml`).
  /// Returns the on-disk overlay if present; otherwise returns the
  /// embedded built-in so the editor always has a starting point.
  /// Requires an owner-kind token; non-owners get 403.
  Future<String> getHubRolesConfig() async {
    final req = await _t.open('GET', '/v1/hub/config/roles');
    req.headers.set(HttpHeaders.acceptHeader, 'application/yaml');
    final resp = await req.close();
    final body = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, body);
    }
    return body;
  }

  /// Writes `roles.yaml` atomically: server validates by parsing the
  /// supplied YAML, snapshots any prior overlay to `roles.yaml.bak`,
  /// writes the new body, and hot-reloads the manifest. Parse failure
  /// surfaces as HubApiError(400) without touching disk. Hot-reload
  /// failure rolls back from the `.bak`. Returns the canonical body
  /// the hub is now serving.
  Future<String> putHubRolesConfig(String yaml) async {
    final req = await _t.open('PUT', '/v1/hub/config/roles');
    req.headers.contentType =
        ContentType('application', 'yaml', charset: 'utf-8');
    req.add(utf8.encode(yaml));
    final resp = await req.close();
    final body = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, body);
    }
    return body;
  }

  /// Removes the on-disk `roles.yaml` overlay and hot-reloads back to
  /// the embedded default. Returns the embedded body. Idempotent —
  /// succeeds whether or not an overlay file currently exists.
  Future<String> resetHubRolesConfig() async {
    final req = await _t.open('DELETE', '/v1/hub/config/roles');
    final resp = await req.close();
    final body = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, body);
    }
    return body;
  }
}
