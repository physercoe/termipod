import 'hub_read_through.dart';
import 'hub_transport.dart';

/// Plans and plan steps (blueprint §6.2) — the structured execution graph
/// a schedule/template instantiates: list/get (live + cached), create,
/// update, and per-step list/create/update. Wedge W13 of
/// `docs/plans/hub-client-split.md`.
class PlansApi {
  final HubTransport _t;
  PlansApi(this._t);

  Future<List<Map<String, dynamic>>> listPlans({
    String? projectId,
    String? status,
  }) {
    final q = <String, String>{};
    if (projectId != null) q['project'] = projectId;
    if (status != null) q['status'] = status;
    return _t.listJson(
      '/v1/teams/${_t.cfg.teamId}/plans',
      query: q.isEmpty ? null : q,
    );
  }

  /// Read-through variant of [listPlans]; see [HubClient.listRunsCached]
  /// for the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listPlansCached({
    String? projectId,
    String? status,
  }) {
    final q = <String, String>{};
    if (projectId != null) q['project'] = projectId;
    if (status != null) q['status'] = status;
    return readThrough<List<Map<String, dynamic>>>(
      cache: _t.snapshotCache,
      hubKey: _t.cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${_t.cfg.teamId}/plans',
        q.isEmpty ? null : q,
      ),
      fetch: () => listPlans(projectId: projectId, status: status),
      decode: _t.decodeListMaps,
    );
  }

  Future<Map<String, dynamic>> getPlan(String planId) async {
    final out = await _t.get('/v1/teams/${_t.cfg.teamId}/plans/$planId');
    return (out as Map).cast<String, dynamic>();
  }

  /// Read-through variant of [getPlan]; see [HubClient.listRunsCached] for
  /// the offline-fallback contract.
  Future<CachedResponse<Map<String, dynamic>>> getPlanCached(
    String planId,
  ) =>
      readThrough<Map<String, dynamic>>(
        cache: _t.snapshotCache,
        hubKey: _t.cacheHubKey,
        endpoint: '/v1/teams/${_t.cfg.teamId}/plans/$planId',
        fetch: () => getPlan(planId),
        decode: _t.decodeMap,
      );

  Future<Map<String, dynamic>> createPlan({
    required String projectId,
    String? templateId,
    int? version,
    Map<String, dynamic>? spec,
  }) async {
    final body = <String, dynamic>{'project_id': projectId};
    if (templateId != null) body['template_id'] = templateId;
    if (version != null) body['version'] = version;
    if (spec != null) body['spec_json'] = spec;
    final out = await _t.post('/v1/teams/${_t.cfg.teamId}/plans', body);
    return (out as Map).cast<String, dynamic>();
  }

  Future<void> updatePlan(
    String planId, {
    String? status, // blueprint §6.2 lifecycle values
    Map<String, dynamic>? spec,
  }) async {
    final body = <String, dynamic>{};
    if (status != null) body['status'] = status;
    if (spec != null) body['spec_json'] = spec;
    await _t.patch('/v1/teams/${_t.cfg.teamId}/plans/$planId', body);
  }

  Future<List<Map<String, dynamic>>> listPlanSteps(String planId) =>
      _t.listJson('/v1/teams/${_t.cfg.teamId}/plans/$planId/steps');

  /// Read-through variant of [listPlanSteps]; see [HubClient.listRunsCached]
  /// for the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listPlanStepsCached(
    String planId,
  ) =>
      readThrough<List<Map<String, dynamic>>>(
        cache: _t.snapshotCache,
        hubKey: _t.cacheHubKey,
        endpoint: '/v1/teams/${_t.cfg.teamId}/plans/$planId/steps',
        fetch: () => listPlanSteps(planId),
        decode: _t.decodeListMaps,
      );

  Future<Map<String, dynamic>> createPlanStep(
    String planId, {
    required int phaseIdx,
    required int stepIdx,
    required String kind, // 'agent_spawn' | 'llm_call' | 'shell' | 'mcp_call' | 'human_decision'
    Map<String, dynamic>? spec,
  }) async {
    final body = <String, dynamic>{
      'phase_idx': phaseIdx,
      'step_idx': stepIdx,
      'kind': kind,
    };
    if (spec != null) body['spec_json'] = spec;
    final out = await _t.post(
      '/v1/teams/${_t.cfg.teamId}/plans/$planId/steps',
      body,
    );
    return (out as Map).cast<String, dynamic>();
  }

  Future<void> updatePlanStep(
    String planId,
    String stepId, {
    String? status,
    String? agentId,
    Map<String, dynamic>? inputRefs,
    Map<String, dynamic>? outputRefs,
  }) async {
    final body = <String, dynamic>{};
    if (status != null) body['status'] = status;
    if (agentId != null) body['agent_id'] = agentId;
    if (inputRefs != null) body['input_refs_json'] = inputRefs;
    if (outputRefs != null) body['output_refs_json'] = outputRefs;
    await _t.patch(
      '/v1/teams/${_t.cfg.teamId}/plans/$planId/steps/$stepId',
      body,
    );
  }
}
