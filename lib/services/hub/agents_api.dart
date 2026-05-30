import 'dart:convert';

import 'hub_read_through.dart';
import 'hub_transport.dart';

/// Agents — the spawned engine instances and everything around their
/// lifecycle: listing (live + cached), spawn, steward ensure, terminate/
/// rename/archive/pause/resume, pane capture, journal read/append, and
/// the per-agent event queue (the P1.7 AG-UI broker: post/list/cache).
/// SSE streaming of those events lives in `EventsApi` (W4). Wedge W9 of
/// `docs/plans/hub-client-split.md`.
class AgentsApi {
  final HubTransport _t;
  AgentsApi(this._t);

  // ---- collections ----

  Future<List<Map<String, dynamic>>> listAgents({
    bool includeArchived = false,
    // Default hides terminated/failed/crashed rows so long-running teams
    // don't accumulate clutter in the agent list (v1.0.606). Pass true
    // for surfaces that need historical agents — Budget rollups need
    // them for accurate spend, the Archived screen needs them because
    // archived rows are usually terminal.
    bool includeTerminated = false,
    String? projectId,
  }) {
    final q = <String, String>{};
    if (includeArchived) q['include_archived'] = '1';
    if (includeTerminated) q['include_terminated'] = '1';
    if (projectId != null && projectId.isNotEmpty) {
      q['project_id'] = projectId;
    }
    return _t.listJson(
      '/v1/teams/${_t.cfg.teamId}/agents',
      query: q.isEmpty ? null : q,
    );
  }

  /// Read-through variant of [listAgents]; see [HubClient.listRunsCached]
  /// for the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listAgentsCached({
    bool includeArchived = false,
    bool includeTerminated = false,
    String? projectId,
  }) {
    final q = <String, String>{};
    if (includeArchived) q['include_archived'] = '1';
    if (includeTerminated) q['include_terminated'] = '1';
    if (projectId != null && projectId.isNotEmpty) {
      q['project_id'] = projectId;
    }
    final query = q.isEmpty ? null : q;
    return readThrough<List<Map<String, dynamic>>>(
      cache: _t.snapshotCache,
      hubKey: _t.cacheHubKey,
      endpoint: buildEndpointKey('/v1/teams/${_t.cfg.teamId}/agents', query),
      fetch: () => listAgents(
        includeArchived: includeArchived,
        includeTerminated: includeTerminated,
        projectId: projectId,
      ),
      decode: _t.decodeListMaps,
    );
  }

  /// Single-agent fetch. Includes `spawn_spec_yaml` + `spawn_authority`
  /// pulled from the agent_spawns join when the agent was created via
  /// /spawn. Agents created by other means (hand-crafted inserts) simply
  /// won't carry those fields.
  Future<Map<String, dynamic>> getAgent(String agentId) async {
    final out = await _t.get('/v1/teams/${_t.cfg.teamId}/agents/$agentId');
    return (out as Map).cast<String, dynamic>();
  }

  /// Read-through variant of [getAgent]; see [HubClient.listRunsCached]
  /// for the offline-fallback contract.
  Future<CachedResponse<Map<String, dynamic>>> getAgentCached(
    String agentId,
  ) =>
      readThrough<Map<String, dynamic>>(
        cache: _t.snapshotCache,
        hubKey: _t.cacheHubKey,
        endpoint: '/v1/teams/${_t.cfg.teamId}/agents/$agentId',
        fetch: () => getAgent(agentId),
        decode: _t.decodeMap,
      );

  /// Parent→child spawn edges. Each row has `parent_agent_id`,
  /// `child_agent_id`, `handle`, `kind`, `status`, plus the original
  /// spawn metadata. Used to render the agent org chart.
  Future<List<Map<String, dynamic>>> listSpawns() =>
      _t.listJson('/v1/teams/${_t.cfg.teamId}/agents/spawns');

  /// Read-through variant of [listSpawns]; see [HubClient.listRunsCached]
  /// for the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listSpawnsCached() =>
      readThrough<List<Map<String, dynamic>>>(
        cache: _t.snapshotCache,
        hubKey: _t.cacheHubKey,
        endpoint: '/v1/teams/${_t.cfg.teamId}/agents/spawns',
        fetch: listSpawns,
        decode: _t.decodeListMaps,
      );

  // ---- spawn ----

  /// Spawns a new agent on the given host using the provided YAML spec
  /// body. Returns either the spawned agent (`status: spawned`) or an
  /// approval handle (`status: pending_approval` + `attention_id`) when
  /// policy gates the action.
  Future<Map<String, dynamic>> spawnAgent({
    required String childHandle,
    required String kind,
    required String spawnSpecYaml,
    String? hostId,
    String? parentAgentId,
    String? personaSeed,
    String? permissionMode,
    String? sessionId,
    bool autoOpenSession = false,
  }) async {
    final body = <String, dynamic>{
      'child_handle': childHandle,
      'kind': kind,
      'spawn_spec_yaml': spawnSpecYaml,
    };
    if (hostId != null && hostId.isNotEmpty) body['host_id'] = hostId;
    if (parentAgentId != null && parentAgentId.isNotEmpty) {
      body['parent_agent_id'] = parentAgentId;
    }
    if (personaSeed != null && personaSeed.trim().isNotEmpty) {
      body['persona_seed'] = personaSeed.trim();
    }
    if (permissionMode != null && permissionMode.isNotEmpty) {
      body['permission_mode'] = permissionMode;
    }
    // sessionId attaches the spawn to an existing session — used by
    // the "Replace steward" / "Switch engine" flow on the project
    // page so the new agent inherits the session's transcript while
    // the underlying claude/codex/etc. process is restarted with
    // the operator's new engine/model picks.
    if (sessionId != null && sessionId.isNotEmpty) {
      body['session_id'] = sessionId;
    }
    // auto_open_session is the multi-steward UX invariant ("every live
    // steward has a session"). When set + sessionId empty, the hub
    // opens a session pointing at the new agent inside the same tx so
    // the spawn is atomic. Ignored when sessionId is set (the swap
    // path already updates the named session in-tx).
    if (autoOpenSession) {
      body['auto_open_session'] = true;
    }
    final out = await _t.post('/v1/teams/${_t.cfg.teamId}/agents/spawn', body);
    return (out as Map).cast<String, dynamic>();
  }

  // ---- general steward (singleton, W4) ----

  /// Ensures the team-scoped general steward (`steward.general.v1`,
  /// handle `@steward`) is running, spawning it on first call. The
  /// endpoint is idempotent: subsequent calls return the existing
  /// agent's id. Returns the server envelope verbatim — `agent_id`,
  /// `status`, `already_running`, plus `spawn_id` on first spawn.
  ///
  /// Surface: tap on the home-tab persistent steward card. The card
  /// avoids manual spawn-sheet UX for the always-on concierge — there
  /// is exactly one general steward per team, archived only by
  /// explicit director action.
  Future<Map<String, dynamic>> ensureGeneralSteward({String? hostId}) async {
    final body = <String, dynamic>{};
    if (hostId != null && hostId.isNotEmpty) {
      body['host_id'] = hostId;
    }
    final out = await _t.post(
      '/v1/teams/${_t.cfg.teamId}/steward.general/ensure',
      body,
    );
    return (out as Map).cast<String, dynamic>();
  }

  /// Ensures the named project's steward (per ADR-025) is running,
  /// spawning one if none exists. Idempotent. Returns the server
  /// envelope — `agent_id`, `project_id`, `status`, `already_running`,
  /// plus `spawn_id` on first spawn.
  ///
  /// `hostId` pins the spawn to a specific host; empty falls back to
  /// the hub's pickFirstHost. `permissionMode` ("skip" / "prompt")
  /// chooses the template's permission flag at spawn time; empty
  /// defaults to "skip" (matches the demo bootstrap).
  Future<Map<String, dynamic>> ensureProjectSteward({
    required String projectId,
    String? hostId,
    String? permissionMode,
  }) async {
    final body = <String, dynamic>{};
    if (hostId != null && hostId.isNotEmpty) {
      body['host_id'] = hostId;
    }
    if (permissionMode != null && permissionMode.isNotEmpty) {
      body['permission_mode'] = permissionMode;
    }
    final out = await _t.post(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/steward/ensure',
      body,
    );
    return (out as Map).cast<String, dynamic>();
  }

  // ---- agent lifecycle ----

  /// Terminates an agent by patching status=terminated. The host-runner
  /// will pick up the kill on its next poll.
  Future<void> terminateAgent(String agentId) async {
    await _t.patch('/v1/teams/${_t.cfg.teamId}/agents/$agentId',
        {'status': 'terminated'});
  }

  /// Renames an agent (handle field). Used by the multi-steward UX to
  /// label stewards (research-steward, infra-steward, …). Server
  /// enforces the live-handle uniqueness — collisions surface as 409
  /// HubApiError so the caller can show "handle already in use".
  Future<void> renameAgent(String agentId, String newHandle) async {
    await _t.patch('/v1/teams/${_t.cfg.teamId}/agents/$agentId',
        {'handle': newHandle});
  }

  /// Soft-archives a terminated agent so it drops out of the live list.
  /// The row stays in the DB so audit history continues to resolve.
  /// Hub refuses with 409 if the agent is still live.
  Future<void> archiveAgent(String agentId) async {
    await _t.delete('/v1/teams/${_t.cfg.teamId}/agents/$agentId');
  }

  /// Enqueues a SIGSTOP against the agent's pane process group. Returns
  /// the command id so callers can poll status if needed.
  Future<Map<String, dynamic>> pauseAgent(String agentId) async {
    final out = await _t.post(
        '/v1/teams/${_t.cfg.teamId}/agents/$agentId/pause', const <String, dynamic>{});
    return (out as Map).cast<String, dynamic>();
  }

  Future<Map<String, dynamic>> resumeAgent(String agentId) async {
    final out = await _t.post(
        '/v1/teams/${_t.cfg.teamId}/agents/$agentId/resume', const <String, dynamic>{});
    return (out as Map).cast<String, dynamic>();
  }

  /// Returns the most recent pane capture for this agent. Pass
  /// `refresh: true` to also enqueue a fresh capture; the current call
  /// still returns the previous cached capture — fetch again after a
  /// beat to see the new one.
  Future<Map<String, dynamic>> getAgentPane(
    String agentId, {
    bool refresh = false,
  }) async {
    final out = await _t.get(
      '/v1/teams/${_t.cfg.teamId}/agents/$agentId/pane',
      query: refresh ? {'refresh': '1'} : null,
    );
    return (out as Map).cast<String, dynamic>();
  }

  /// Reads the agent's markdown journal. Returns the raw markdown text;
  /// an empty string means the journal file hasn't been written yet.
  Future<String> readAgentJournal(String agentId) async {
    final req = await _t.open(
        'GET', '/v1/teams/${_t.cfg.teamId}/agents/$agentId/journal');
    final resp = await req.close();
    final body = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, body);
    }
    return body;
  }

  /// Appends a markdown note to the agent's journal. The hub prepends a
  /// UTC timestamp header unless [header] is supplied.
  Future<void> appendAgentJournal(
    String agentId,
    String entry, {
    String? header,
  }) async {
    final body = <String, dynamic>{'entry': entry};
    if (header != null && header.isNotEmpty) body['header'] = header;
    await _t.post('/v1/teams/${_t.cfg.teamId}/agents/$agentId/journal', body);
  }

  // ---- agent events (P1.7 AG-UI broker) ----

  /// Appends an event to the agent's per-agent queue. Producer defaults to
  /// `agent`; use `user` for approvals/input and `system` for status blips.
  /// Returns {id, seq, ts} so callers can order locally without re-listing.
  Future<Map<String, dynamic>> postAgentEvent(
    String agentId, {
    required String kind,
    String? producer,
    Map<String, dynamic>? payload,
  }) async {
    final body = <String, dynamic>{'kind': kind};
    if (producer != null && producer.isNotEmpty) body['producer'] = producer;
    if (payload != null) body['payload'] = payload;
    final out =
        await _t.post('/v1/teams/${_t.cfg.teamId}/agents/$agentId/events', body);
    return (out as Map).cast<String, dynamic>();
  }

  /// Posts structured user input to an agent (P1.8). Lands in
  /// agent_events as producer='user' with kind='input.<kind>'; driver
  /// dispatch is the hub's job downstream. Returns {id, seq, ts}.
  /// Post a user-side input event to an agent. `kind` selects the
  /// shape — `text` / `approval` / `answer` / `cancel` / `attach` —
  /// and the relevant fields ride alongside. `answer` is for inline
  /// replies to tool questions (e.g. AskUserQuestion); pass the
  /// originating tool_call id as [requestId] and the chosen reply as
  /// [body].
  Future<Map<String, dynamic>> postAgentInput(
    String agentId, {
    required String kind,
    String? body,
    String? decision,
    String? requestId,
    String? optionId,
    String? note,
    String? reason,
    String? documentId,
    // ADR-021 W2.1 — set_mode / set_model picker payload. The hub's
    // input handler routes by family.runtime_mode_switch[driving_mode]:
    // rpc/per_turn_argv land as input.* events the driver picks up;
    // respawn triggers respawn-with-mutated-spec. Mobile sends one
    // shape; the wire path varies per engine.
    String? modeId,
    String? modelId,
    // ADR-021 W4.1 — image content blocks alongside body. Each entry
    // is `{mime_type, data}` where data is base64. The hub validates
    // (mime allowlist, ≤5 MiB decoded, ≤3 images) and persists onto
    // payload_json["images"]; per-driver shape mapping lands in
    // W4.2-W4.5. UI surface (composer attach branch) lands in W4.6.
    List<Map<String, String>>? images,
    // artifact-type-registry W7.2 — non-image multimodal attachments.
    // PDFs are cross-engine (Claude document, Codex file_data, Gemini
    // inline_data); audio/video are Gemini-only. Each entry is
    // `{mime_type, data, filename}` with data base64.
    List<Map<String, String>>? pdfs,
    List<Map<String, String>>? audios,
    List<Map<String, String>>? videos,
    // v1.0.707 polish — when true and kind=='text', the hub bypasses
    // the principal-directive envelope wrap so the engine receives
    // the body verbatim. Used by mobile for engine-control slash
    // commands (/clear, /compact, /model …) — wrapping those in
    // "[directive from the principal]\n…\n\nReply in this chat…"
    // turns them into prose the engine ignores. The shape gate is
    // mobile-side (see ComposeBar's isSlashCommandBody); hub honours
    // the flag regardless. Ignored for non-text kinds.
    bool? raw,
  }) async {
    final req = <String, dynamic>{'kind': kind};
    if (body != null) req['body'] = body;
    if (decision != null) req['decision'] = decision;
    if (requestId != null) req['request_id'] = requestId;
    if (optionId != null) req['option_id'] = optionId;
    if (note != null) req['note'] = note;
    if (reason != null) req['reason'] = reason;
    if (documentId != null) req['document_id'] = documentId;
    if (modeId != null) req['mode_id'] = modeId;
    if (modelId != null) req['model_id'] = modelId;
    if (images != null && images.isNotEmpty) req['images'] = images;
    if (pdfs != null && pdfs.isNotEmpty) req['pdfs'] = pdfs;
    if (audios != null && audios.isNotEmpty) req['audios'] = audios;
    if (videos != null && videos.isNotEmpty) req['videos'] = videos;
    if (raw == true) req['raw'] = true;
    final out =
        await _t.post('/v1/teams/${_t.cfg.teamId}/agents/$agentId/input', req);
    return (out as Map).cast<String, dynamic>();
  }

  /// Backfill events by monotonic seq. Modes (mutually exclusive,
  /// resolved server-side in the order before > tail > since):
  ///   - [since] (exclusive, seq > since, ASC) — incremental tail used
  ///     by SSE reconnect; default behavior when none of the others is set.
  ///   - [tail] true — newest [limit] events, DESC. Used by AgentFeed's
  ///     cold open so a long transcript shows the most recent turns
  ///     instead of the oldest.
  ///   - [before] (exclusive, seq < before, DESC) — load-older paging
  ///     trigger when the user scrolls past the top of the loaded set.
  /// `sessionId` scopes the result to one session — the new-session flow
  /// keeps the same agentId, so without this filter a fresh chat would
  /// replay the prior session's transcript.
  Future<List<Map<String, dynamic>>> listAgentEvents(
    String agentId, {
    int? since,
    int? before,
    String? beforeTs,
    bool tail = false,
    int? limit,
    String? sessionId,
  }) {
    final q = <String, String>{};
    // Cursor precedence on the server is before_ts > before > tail > since.
    // before_ts is the session-scoped variant — seq is per-agent so it
    // can't order events across the agents that one resumed session
    // spans, but ts can.
    if (beforeTs != null && beforeTs.isNotEmpty) {
      q['before_ts'] = beforeTs;
    } else if (before != null) {
      q['before'] = '$before';
    } else if (tail) {
      q['tail'] = 'true';
    } else if (since != null) {
      q['since'] = '$since';
    }
    if (limit != null) q['limit'] = '$limit';
    if (sessionId != null && sessionId.isNotEmpty) q['session'] = sessionId;
    return _t.listJson(
      '/v1/teams/${_t.cfg.teamId}/agents/$agentId/events',
      query: q.isEmpty ? null : q,
    );
  }

  /// Read-through variant of [listAgentEvents]; see
  /// [HubClient.listAttentionCached] for the offline-fallback contract.
  /// Sessions are the one surface where dropping the transcript on a
  /// flaky network hurts most — opening an existing session offline now
  /// shows the last-seen transcript with an "Offline · last updated X"
  /// hint, instead of an empty error card.
  Future<CachedResponse<List<Map<String, dynamic>>>> listAgentEventsCached(
    String agentId, {
    int? since,
    bool tail = false,
    int? limit,
    String? sessionId,
  }) {
    final q = <String, String>{};
    if (tail) {
      q['tail'] = 'true';
    } else if (since != null) {
      q['since'] = '$since';
    }
    if (limit != null) q['limit'] = '$limit';
    if (sessionId != null && sessionId.isNotEmpty) q['session'] = sessionId;
    return readThrough<List<Map<String, dynamic>>>(
      cache: _t.snapshotCache,
      hubKey: _t.cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${_t.cfg.teamId}/agents/$agentId/events',
        q.isEmpty ? null : q,
      ),
      fetch: () => listAgentEvents(
        agentId,
        since: since,
        tail: tail,
        limit: limit,
        sessionId: sessionId,
      ),
      decode: _t.decodeListMaps,
    );
  }

  /// Cache-only read of agent events for cold-open render-first-paint.
  /// Returns null when no snapshot exists; otherwise serves the cached
  /// rows immediately without waiting on a network round-trip. Pair
  /// with [listAgentEventsCached] kicked off in parallel (and ignored
  /// on success — SSE with `since=<maxSeq>` does the actual delta
  /// catch-up) so the cache stays warm for next cold-open.
  ///
  /// ADR-006: cache-first beats network-first-with-fallback. The
  /// blocking `await fetch()` in [readThrough] is the wait users feel
  /// when opening a session; this method skips it.
  Future<CachedResponse<List<Map<String, dynamic>>>?>
      listAgentEventsCacheOnly(
    String agentId, {
    int? since,
    bool tail = false,
    int? limit,
    String? sessionId,
  }) async {
    final cache = _t.snapshotCache;
    if (cache == null) return null;
    final q = <String, String>{};
    if (tail) {
      q['tail'] = 'true';
    } else if (since != null) {
      q['since'] = '$since';
    }
    if (limit != null) q['limit'] = '$limit';
    if (sessionId != null && sessionId.isNotEmpty) q['session'] = sessionId;
    final endpoint = buildEndpointKey(
      '/v1/teams/${_t.cfg.teamId}/agents/$agentId/events',
      q.isEmpty ? null : q,
    );
    final snap = await cache.get(_t.cacheHubKey, endpoint);
    if (snap == null) return null;
    return CachedResponse<List<Map<String, dynamic>>>(
      _t.decodeListMaps(snap.body),
      snap.fetchedAt,
    );
  }
}
