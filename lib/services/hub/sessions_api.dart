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

  /// Per-session run digest — the canonical summary the analysis surface
  /// reads (ADR-038 §5): the ts-ordered rollup of the session's agents'
  /// digests. Shape: `{session_id, agent_ids, event_count, turn_count,
  /// duration_ms, cost_usd, error_count, errors, tool_total, tool_failed,
  /// tools, by_model, latency:{p50_ms,p95_ms,samples,...}, outcome,
  /// watermark_seq, first_ts, last_ts}`. The hub lazily (re)computes it on
  /// read, so it is current for live runs too (label it "as of last_ts").
  ///
  /// Returns null on any error so the report card self-gates blank rather
  /// than stalling the analysis view.
  Future<Map<String, dynamic>?> getSessionDigest(String id) async {
    try {
      final out =
          await _t.get('/v1/teams/${_t.cfg.teamId}/sessions/$id/digest');
      if (out is Map) return out.cast<String, dynamic>();
      return null;
    } catch (_) {
      return null;
    }
  }

  /// Read-through variant of [getSessionDigest] — keeps the last digest
  /// visible offline (the analysis surface is read-mostly; a stale report
  /// card beats a blank one). Mirrors [listSessionsCached].
  Future<CachedResponse<Map<String, dynamic>>> getSessionDigestCached(
      String id) {
    return readThrough<Map<String, dynamic>>(
      cache: _t.snapshotCache,
      hubKey: _t.cacheHubKey,
      endpoint: '/v1/teams/${_t.cfg.teamId}/sessions/$id/digest',
      fetch: () async {
        final out =
            await _t.get('/v1/teams/${_t.cfg.teamId}/sessions/$id/digest');
        return (out as Map).cast<String, dynamic>();
      },
      decode: (raw) => (raw as Map).cast<String, dynamic>(),
    );
  }

  /// Per-session turn index (ADR-038 §3 / plan P2) — the ts-ordered union of
  /// the session's agents' turns, the digest-backed "Turns" structure index
  /// the analysis surface lists and jumps from. Each row:
  /// `{agent_id, turn_id, idx, start_seq, start_ts, end_seq, end_ts,
  /// duration_ms, status, open, cost_usd, in_tokens, out_tokens, tool_count,
  /// tool_failed, error_count}`. `start_seq` is the jump anchor. The hub lazily
  /// backfills the index on read. Returns an empty list on any error so the
  /// section self-gates rather than stalling the view.
  Future<List<Map<String, dynamic>>> getSessionTurns(String id) async {
    try {
      final out =
          await _t.get('/v1/teams/${_t.cfg.teamId}/sessions/$id/turns');
      if (out is Map && out['turns'] is List) {
        return (out['turns'] as List)
            .whereType<Map>()
            .map((e) => e.cast<String, dynamic>())
            .toList();
      }
      return const [];
    } catch (_) {
      return const [];
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

  /// Archives a session (was: closeSession). Posts to the canonical
  /// /archive endpoint (ADR-009); the legacy /close alias was retired
  /// in WS1.2.
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
