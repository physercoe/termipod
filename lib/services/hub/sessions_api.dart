import 'hub_read_through.dart';
import 'hub_transport.dart';

/// Sessions — the durable conversational frame around a steward (or any
/// agent): a transcript that survives a host-runner restart because
/// session_id is stamped on agent_events independent of the agent process.
/// Listing (live + cached), open/rename/archive/resume/fork/delete, the
/// per-session imputed cost, and transcript search. Wedge W8 of
/// `docs/plans/hub-client-split.md`.
class SessionsApi {
  final HubTransport _t;
  SessionsApi(this._t);

  Future<List<Map<String, dynamic>>> listSessions({String? status}) =>
      _t.listJson(
        '/v1/teams/${_t.cfg.teamId}/sessions',
        query: status == null ? null : {'status': status},
      );

  /// Read-through variant of [listSessions]; see
  /// [HubClient.listAttentionCached] for the offline-fallback contract.
  /// Sessions are the navigational primitive of the Stewards page —
  /// without this, an airplane-mode open would show "no stewards" even
  /// when prior fetches populated the cache.
  Future<CachedResponse<List<Map<String, dynamic>>>> listSessionsCached({
    String? status,
  }) {
    final q = status == null ? null : {'status': status};
    return readThrough<List<Map<String, dynamic>>>(
      cache: _t.snapshotCache,
      hubKey: _t.cacheHubKey,
      endpoint:
          buildEndpointKey('/v1/teams/${_t.cfg.teamId}/sessions', q),
      fetch: () => listSessions(status: status),
      decode: _t.decodeListMaps,
    );
  }

  Future<Map<String, dynamic>> getSession(String id) async {
    final out = await _t.get('/v1/teams/${_t.cfg.teamId}/sessions/$id');
    return (out as Map).cast<String, dynamic>();
  }

  /// Per-session imputed cost breakdown (ADR-036 D8 chip 2 + tooltip).
  /// Returns `{session_id, total_usd, breakdown_by_model, tokens_by_model,
  /// missing_models, snapshot_date, origin, imputed}`. The `imputed: true`
  /// flag is always set and carries the subscription-disclaimer semantics
  /// (subscription users aren't actually billed per-token; the numbers
  /// are estimates against the public API rate sheet).
  ///
  /// Returns null on any error so the chip self-gates blank rather than
  /// stalling the UI (the parent GET inlines `session_cost_usd_imputed`
  /// for first-paint; this endpoint serves only the tooltip detail).
  Future<Map<String, dynamic>?> getSessionCost(String id) async {
    try {
      final out = await _t.get('/v1/teams/${_t.cfg.teamId}/sessions/$id/cost');
      if (out is Map) return out.cast<String, dynamic>();
      return null;
    } catch (_) {
      return null;
    }
  }

  /// Opens a new session. Most callers will pass `agentId` to attach
  /// the session to an existing steward; `worktreePath` and
  /// `spawnSpecYaml` are needed when the resume flow (W2-S3) wants to
  /// respawn cleanly without the user re-picking engine/scope.
  Future<Map<String, dynamic>> openSession({
    String? title,
    String? scopeKind,
    String? scopeId,
    String? agentId,
    String? worktreePath,
    String? spawnSpecYaml,
  }) async {
    final body = <String, dynamic>{};
    if (title != null && title.isNotEmpty) body['title'] = title;
    if (scopeKind != null && scopeKind.isNotEmpty) body['scope_kind'] = scopeKind;
    if (scopeId != null && scopeId.isNotEmpty) body['scope_id'] = scopeId;
    if (agentId != null && agentId.isNotEmpty) body['agent_id'] = agentId;
    if (worktreePath != null && worktreePath.isNotEmpty) {
      body['worktree_path'] = worktreePath;
    }
    if (spawnSpecYaml != null && spawnSpecYaml.isNotEmpty) {
      body['spawn_spec_yaml'] = spawnSpecYaml;
    }
    final out = await _t.post('/v1/teams/${_t.cfg.teamId}/sessions', body);
    return (out as Map).cast<String, dynamic>();
  }

  /// Renames a session. Empty title clears it back to the default
  /// "(untitled session)" rendering on mobile.
  Future<void> renameSession(String id, String title) async {
    await _t.patch('/v1/teams/${_t.cfg.teamId}/sessions/$id', {'title': title});
  }

  /// Archives a session (was: closeSession). The hub's /close endpoint
  /// is kept as an alias by ADR-009 for one release; this calls
  /// /archive directly.
  Future<void> archiveSession(String id) async {
    // post requires a non-null body; an empty map is the canonical
    // payload for endpoints that take none.
    await _t.post(
        '/v1/teams/${_t.cfg.teamId}/sessions/$id/archive', const <String, dynamic>{});
  }

  /// Resumes a paused session: spawns a new agent with the same
  /// handle/kind/host as the session's prior current_agent_id,
  /// reusing the worktree_path and spawn_spec_yaml captured at open
  /// time. Returns `{session_id, new_agent_id, prior_agent_id, spawn_id}`.
  Future<Map<String, dynamic>> resumeSession(String id) async {
    final out = await _t.post(
        '/v1/teams/${_t.cfg.teamId}/sessions/$id/resume',
        const <String, dynamic>{});
    return (out as Map).cast<String, dynamic>();
  }

  /// Phase 1.5c — full-text search across session transcripts.
  /// Returns a list of result rows shaped:
  ///   { event_id, session_id, scope_kind, scope_id, session_title,
  ///     seq, ts, kind, snippet }
  /// `query` is an FTS5 MATCH expression. `limit` defaults to 50.
  Future<List<Map<String, dynamic>>> searchSessions(
    String query, {
    int? limit,
  }) async {
    final q = <String, String>{'q': query};
    if (limit != null) q['limit'] = '$limit';
    final out = await _t.get(
      '/v1/teams/${_t.cfg.teamId}/sessions/search',
      query: q,
    );
    return (out as List).cast<Map<String, dynamic>>();
  }

  /// Forks an archived session into a new active one (ADR-009 D4).
  /// The new session copies scope from the source and attaches to
  /// the team's live steward (or [agentId] if provided). Returns
  /// `{session_id, source_session_id, agent_id, scope_kind,
  /// scope_id, title}`.
  Future<Map<String, dynamic>> forkSession(
    String id, {
    String? agentId,
    String? title,
  }) async {
    final body = <String, dynamic>{};
    if (agentId != null && agentId.isNotEmpty) body['agent_id'] = agentId;
    if (title != null && title.isNotEmpty) body['title'] = title;
    final out = await _t.post(
        '/v1/teams/${_t.cfg.teamId}/sessions/$id/fork', body);
    return (out as Map).cast<String, dynamic>();
  }

  /// Soft-deletes an archived session and clears its session_id from
  /// transcript / audit / attention rows. Hub refuses with 409 if
  /// the session is still active or paused (archive it first).
  /// Idempotent: deleting an already-deleted session returns 204.
  Future<void> deleteSession(String id) async {
    await _t.delete('/v1/teams/${_t.cfg.teamId}/sessions/$id');
  }
}
