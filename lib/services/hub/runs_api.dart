import 'hub_read_through.dart';
import 'hub_transport.dart';

/// Runs and schedules (blueprint §6.3 / §6.5). Schedules trigger a plan
/// from a template (they never spawn agents directly); runs are the hub's
/// lightweight record of a compute activity — name/status/metric URIs,
/// plus the metric/image/histogram/sweep digests the host-runner pushes.
/// Wedge W10 of `docs/plans/hub-client-split.md`.
class RunsApi {
  final HubTransport _t;
  RunsApi(this._t);

  // ---- schedules (team-scoped, blueprint §6.3) ----

  Future<List<Map<String, dynamic>>> listSchedules({String? projectId}) =>
      _t.listJson(
        '/v1/teams/${_t.cfg.teamId}/schedules',
        query: projectId == null ? null : {'project': projectId},
      );

  /// Read-through variant of [listSchedules]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listSchedulesCached({
    String? projectId,
  }) {
    final q = projectId == null ? null : {'project': projectId};
    return readThrough<List<Map<String, dynamic>>>(
      cache: _t.snapshotCache,
      hubKey: _t.cacheHubKey,
      endpoint: buildEndpointKey('/v1/teams/${_t.cfg.teamId}/schedules', q),
      fetch: () => listSchedules(projectId: projectId),
      decode: _t.decodeListMaps,
    );
  }

  Future<Map<String, dynamic>> createSchedule({
    required String projectId,
    required String templateId,
    required String triggerKind, // 'cron' | 'manual' | 'on_create'
    String? cronExpr,
    Map<String, dynamic>? parameters,
    bool? enabled,
  }) async {
    final body = <String, dynamic>{
      'project_id': projectId,
      'template_id': templateId,
      'trigger_kind': triggerKind,
    };
    if (cronExpr != null && cronExpr.isNotEmpty) body['cron_expr'] = cronExpr;
    if (parameters != null) body['parameters_json'] = parameters;
    if (enabled != null) body['enabled'] = enabled;
    final out = await _t.post('/v1/teams/${_t.cfg.teamId}/schedules', body);
    return (out as Map).cast<String, dynamic>();
  }

  /// PATCHes an existing schedule. Only cron schedules accept [cronExpr].
  Future<void> patchSchedule(
    String id, {
    bool? enabled,
    String? cronExpr,
    Map<String, dynamic>? parameters,
  }) async {
    final body = <String, dynamic>{};
    if (enabled != null) body['enabled'] = enabled;
    if (cronExpr != null) body['cron_expr'] = cronExpr;
    if (parameters != null) body['parameters_json'] = parameters;
    await _t.patch('/v1/teams/${_t.cfg.teamId}/schedules/$id', body);
  }

  Future<void> deleteSchedule(String id) =>
      _t.delete('/v1/teams/${_t.cfg.teamId}/schedules/$id');

  /// Manually fires a schedule, creating a plan row. Works for any
  /// trigger_kind. Returns the new plan id.
  Future<String> runSchedule(String id) async {
    final out = await _t.post(
      '/v1/teams/${_t.cfg.teamId}/schedules/$id/run',
      const <String, dynamic>{},
    );
    return ((out as Map).cast<String, dynamic>()['plan_id'] ?? '').toString();
  }

  // ---- runs (blueprint §6.5) ----

  Future<List<Map<String, dynamic>>> listRuns({
    String? projectId,
    String? status,
    int? limit,
  }) async {
    final q = <String, String>{};
    if (projectId != null && projectId.isNotEmpty) q['project'] = projectId;
    if (status != null && status.isNotEmpty) q['status'] = _runStatusToServer(status);
    if (limit != null) q['limit'] = '$limit';
    final rows = await _t.listJson(
      '/v1/teams/${_t.cfg.teamId}/runs',
      query: q.isEmpty ? null : q,
    );
    return [for (final r in rows) _runRowToUI(r)];
  }

  /// Read-through variant of [listRuns] that serves the last cached
  /// snapshot if the network is down. Cache is keyed on the full URL
  /// (path + sorted query), so different filters get independent rows.
  Future<CachedResponse<List<Map<String, dynamic>>>> listRunsCached({
    String? projectId,
    String? status,
    int? limit,
  }) {
    final q = <String, String>{};
    if (projectId != null && projectId.isNotEmpty) q['project'] = projectId;
    if (status != null && status.isNotEmpty) {
      q['status'] = _runStatusToServer(status);
    }
    if (limit != null) q['limit'] = '$limit';
    return readThrough<List<Map<String, dynamic>>>(
      cache: _t.snapshotCache,
      hubKey: _t.cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${_t.cfg.teamId}/runs',
        q.isEmpty ? null : q,
      ),
      fetch: () => listRuns(
        projectId: projectId,
        status: status,
        limit: limit,
      ),
      decode: _t.decodeListMaps,
    );
  }

  Future<Map<String, dynamic>> getRun(String runId) async {
    final out = await _t.get('/v1/teams/${_t.cfg.teamId}/runs/$runId');
    return _runRowToUI((out as Map).cast<String, dynamic>());
  }

  /// Read-through variant of [getRun]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<Map<String, dynamic>>> getRunCached(
    String runId,
  ) =>
      readThrough<Map<String, dynamic>>(
        cache: _t.snapshotCache,
        hubKey: _t.cacheHubKey,
        endpoint: '/v1/teams/${_t.cfg.teamId}/runs/$runId',
        fetch: () => getRun(runId),
        decode: _t.decodeMap,
      );

  Future<Map<String, dynamic>> createRun({
    required String projectId,
    required String kind, // e.g. 'train', 'eval', 'notebook'
    String? agentId,
    String? parentRunId,
    String? name,
    Map<String, dynamic>? metadata,
  }) async {
    final body = <String, dynamic>{'project_id': projectId, 'kind': kind};
    if (agentId != null) body['agent_id'] = agentId;
    if (parentRunId != null) body['parent_run_id'] = parentRunId;
    if (name != null) body['name'] = name;
    if (metadata != null) body['metadata_json'] = metadata;
    final out = await _t.post('/v1/teams/${_t.cfg.teamId}/runs', body);
    await _t.invalidate('/v1/teams/${_t.cfg.teamId}/runs');
    return (out as Map).cast<String, dynamic>();
  }

  Future<void> completeRun(
    String runId, {
    required String status, // 'succeeded' | 'failed' | 'cancelled'
    String? summary,
  }) async {
    // Server vocabulary uses 'completed'; the rest of the mobile UI says
    // 'succeeded'. Translate at the boundary.
    final body = <String, dynamic>{'status': _runStatusToServer(status)};
    if (summary != null) body['summary'] = summary;
    await _t.post('/v1/teams/${_t.cfg.teamId}/runs/$runId/complete', body);
    await _t.invalidate('/v1/teams/${_t.cfg.teamId}/runs');
  }

  // UI run-status 'succeeded' ↔ server 'completed'. All other values pass
  // through unchanged.
  String _runStatusToServer(String uiStatus) =>
      uiStatus == 'succeeded' ? 'completed' : uiStatus;

  Map<String, dynamic> _runRowToUI(Map<String, dynamic> row) {
    if (row['status'] == 'completed') {
      return {...row, 'status': 'succeeded'};
    }
    return row;
  }

  Future<void> attachRunMetricURI(
    String runId, {
    required String kind, // e.g. 'tensorboard', 'wandb'
    required String uri,
  }) async {
    await _t.post(
      '/v1/teams/${_t.cfg.teamId}/runs/$runId/metric_uri',
      {'kind': kind, 'uri': uri},
    );
  }

  /// Pulls the run's metric digests — one row per metric name, each with
  /// a downsampled [[step, value], ...] points array. The host-runner
  /// metric poller (trackio/wandb/tensorboard) writes these; the mobile
  /// app renders them as inline sparklines. Bulk time-series stay on the
  /// host per blueprint §4.
  Future<List<Map<String, dynamic>>> getRunMetrics(String runId) =>
      _t.listJson('/v1/teams/${_t.cfg.teamId}/runs/$runId/metrics');

  /// Read-through variant of [getRunMetrics]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> getRunMetricsCached(
    String runId,
  ) =>
      readThrough<List<Map<String, dynamic>>>(
        cache: _t.snapshotCache,
        hubKey: _t.cacheHubKey,
        endpoint: '/v1/teams/${_t.cfg.teamId}/runs/$runId/metrics',
        fetch: () => getRunMetrics(runId),
        decode: _t.decodeListMaps,
      );

  /// Lists a run's image-panel entries — the wandb "Images" equivalent.
  /// Each row carries a `metric_name` + `step` + `blob_sha`. The mobile UI
  /// groups by metric_name and fetches frame bytes lazily via
  /// [HubClient.downloadBlob] as the slider is scrubbed.
  Future<List<Map<String, dynamic>>> getRunImages(
    String runId, {
    String? metric,
  }) =>
      _t.listJson(
        '/v1/teams/${_t.cfg.teamId}/runs/$runId/images',
        query: metric == null ? null : {'metric': metric},
      );

  /// Read-through variant of [getRunImages]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> getRunImagesCached(
    String runId, {
    String? metric,
  }) {
    final q = metric == null ? null : {'metric': metric};
    return readThrough<List<Map<String, dynamic>>>(
      cache: _t.snapshotCache,
      hubKey: _t.cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${_t.cfg.teamId}/runs/$runId/images',
        q,
      ),
      fetch: () => getRunImages(runId, metric: metric),
      decode: _t.decodeListMaps,
    );
  }

  /// Per-project sweep summary — one row per run in this project, each
  /// carrying {run_id, status, config_json (string), final_metrics
  /// (map name→last value), created_at}. Feeds the cross-run scatter
  /// panel (wandb "parallel coords" archetype) on the Project detail
  /// screen. Avoids the N+1 fan-out that listRuns + getRunMetrics would
  /// require.
  Future<List<Map<String, dynamic>>> getProjectSweepSummary(
          String projectId) =>
      _t.listJson(
        '/v1/teams/${_t.cfg.teamId}/projects/$projectId/sweep-summary',
      );

  /// Read-through variant of [getProjectSweepSummary]; see [listRunsCached]
  /// for the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>>
      getProjectSweepSummaryCached(String projectId) =>
          readThrough<List<Map<String, dynamic>>>(
            cache: _t.snapshotCache,
            hubKey: _t.cacheHubKey,
            endpoint:
                '/v1/teams/${_t.cfg.teamId}/projects/$projectId/sweep-summary',
            fetch: () => getProjectSweepSummary(projectId),
            decode: _t.decodeListMaps,
          );

  /// Lists a run's histogram entries — the wandb "Distributions" panel.
  /// Each row is `{name, step, buckets: {edges, counts}, updated_at}`.
  /// The mobile widget groups by metric_name and renders a scrubber
  /// across steps so the distribution shift over training is visible.
  Future<List<Map<String, dynamic>>> getRunHistograms(
    String runId, {
    String? metric,
  }) =>
      _t.listJson(
        '/v1/teams/${_t.cfg.teamId}/runs/$runId/histograms',
        query: metric == null ? null : {'metric': metric},
      );

  /// Read-through variant of [getRunHistograms]; see [listRunsCached] for the
  /// offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> getRunHistogramsCached(
    String runId, {
    String? metric,
  }) {
    final q = metric == null ? null : {'metric': metric};
    return readThrough<List<Map<String, dynamic>>>(
      cache: _t.snapshotCache,
      hubKey: _t.cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${_t.cfg.teamId}/runs/$runId/histograms',
        q,
      ),
      fetch: () => getRunHistograms(runId, metric: metric),
      decode: _t.decodeListMaps,
    );
  }

  /// Upserts histogram digests for a run. Body shape:
  ///   [{"name":..., "step":N, "buckets":{"edges":[...],"counts":[...]}}]
  /// Rows are keyed by (run, metric_name, step) — PUTs for the same
  /// triple replace the stored buckets. Used by host-runners that
  /// forward binned tensors from trackio/wandb. Not called from the
  /// mobile UI today; exposed so tests (and future import flows) can
  /// populate histograms without hitting the database directly.
  Future<void> putRunHistograms(
    String runId,
    List<Map<String, dynamic>> histograms,
  ) =>
      _t.put(
        '/v1/teams/${_t.cfg.teamId}/runs/$runId/histograms',
        {'histograms': histograms},
      );
}
