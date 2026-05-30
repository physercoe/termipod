import 'hub_read_through.dart';
import 'hub_transport.dart';

/// Project work-products (A3 §4/§5/§9, blueprint §6.6/§6.8): deliverables
/// (+ ratify/unratify/send-back), acceptance criteria (+ mark/waive), the
/// project overview snippet, document version history, content-addressed
/// artifacts, and reviews. Wedge W12 of `docs/plans/hub-client-split.md`.
class DeliverablesApi {
  final HubTransport _t;
  DeliverablesApi(this._t);

  // ---- deliverables + components + project overview ----

  Future<List<Map<String, dynamic>>> listDeliverables({
    required String projectId,
    String? phase,
    String? state,
    bool includeComponents = false,
  }) async {
    final q = <String, String>{};
    if (phase != null && phase.isNotEmpty) q['phase'] = phase;
    if (state != null && state.isNotEmpty) q['state'] = state;
    if (includeComponents) q['include'] = 'components';
    final out = await _t.get(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/deliverables',
      query: q.isEmpty ? null : q,
    );
    final m = (out as Map).cast<String, dynamic>();
    final items = (m['items'] as List? ?? const [])
        .map((e) => (e as Map).cast<String, dynamic>())
        .toList();
    return items;
  }

  /// Read-through variant of [listDeliverables]; see
  /// [HubClient.listRunsCached] for the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listDeliverablesCached({
    required String projectId,
    String? phase,
    String? state,
    bool includeComponents = false,
  }) {
    final q = <String, String>{};
    if (phase != null && phase.isNotEmpty) q['phase'] = phase;
    if (state != null && state.isNotEmpty) q['state'] = state;
    if (includeComponents) q['include'] = 'components';
    return readThrough<List<Map<String, dynamic>>>(
      cache: _t.snapshotCache,
      hubKey: _t.cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${_t.cfg.teamId}/projects/$projectId/deliverables',
        q.isEmpty ? null : q,
      ),
      fetch: () => listDeliverables(
        projectId: projectId,
        phase: phase,
        state: state,
        includeComponents: includeComponents,
      ),
      decode: _t.decodeListMaps,
    );
  }

  Future<Map<String, dynamic>> getDeliverable({
    required String projectId,
    required String deliverableId,
  }) async {
    final out = await _t.get(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/deliverables/$deliverableId',
    );
    return (out as Map).cast<String, dynamic>();
  }

  /// Read-through variant of [getDeliverable]; see [HubClient.listRunsCached]
  /// for the offline-fallback contract. The structured deliverable viewer
  /// uses this so directors can re-open a deliverable without network.
  Future<CachedResponse<Map<String, dynamic>>> getDeliverableCached({
    required String projectId,
    required String deliverableId,
  }) =>
      readThrough<Map<String, dynamic>>(
        cache: _t.snapshotCache,
        hubKey: _t.cacheHubKey,
        endpoint:
            '/v1/teams/${_t.cfg.teamId}/projects/$projectId/deliverables/$deliverableId',
        fetch: () => getDeliverable(
          projectId: projectId,
          deliverableId: deliverableId,
        ),
        decode: _t.decodeMap,
      );

  Future<Map<String, dynamic>> ratifyDeliverable({
    required String projectId,
    required String deliverableId,
    String? rationale,
  }) async {
    final out = await _t.post(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/deliverables/$deliverableId/ratify',
      {if (rationale != null && rationale.isNotEmpty) 'rationale': rationale},
    );
    await _invalidateProjectDeliverable(projectId, deliverableId);
    return (out as Map).cast<String, dynamic>();
  }

  Future<Map<String, dynamic>> unratifyDeliverable({
    required String projectId,
    required String deliverableId,
    String? reason,
  }) async {
    final out = await _t.post(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/deliverables/$deliverableId/unratify',
      {if (reason != null && reason.isNotEmpty) 'rationale': reason},
    );
    await _invalidateProjectDeliverable(projectId, deliverableId);
    return (out as Map).cast<String, dynamic>();
  }

  /// ADR-020 W2 — POST /deliverables/{id}/send-back. Returns the
  /// updated deliverable wrapped with the new attention_item_id so the
  /// caller can show "Note sent — attention item created" with a
  /// deep-link if it wants. 409 when state is `ratified`; 422 when an
  /// annotation_id doesn't belong to one of this deliverable's docs.
  Future<Map<String, dynamic>> sendBackDeliverable({
    required String projectId,
    required String deliverableId,
    required String note,
    List<String> annotationIds = const [],
  }) async {
    final out = await _t.post(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/deliverables/$deliverableId/send-back',
      {
        'note': note,
        if (annotationIds.isNotEmpty) 'annotation_ids': annotationIds,
      },
    );
    await _invalidateProjectDeliverable(projectId, deliverableId);
    return (out as Map).cast<String, dynamic>();
  }

  /// Drop every cache row touched by a deliverable mutation: the
  /// deliverable itself, the project's deliverable list (any phase /
  /// state filter), the criteria list (ratification mutates criterion
  /// gates as a cascade), and the project overview snippet.
  Future<void> _invalidateProjectDeliverable(
    String projectId,
    String deliverableId,
  ) async {
    await _t.invalidate(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/deliverables/$deliverableId',
    );
    await _t.invalidate(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/deliverables',
    );
    await _t.invalidate(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/criteria',
    );
    await _t.invalidate(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/overview',
    );
  }

  Future<Map<String, dynamic>> getProjectOverview(String projectId) async {
    final out = await _t.get(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/overview',
    );
    return (out as Map).cast<String, dynamic>();
  }

  Future<CachedResponse<Map<String, dynamic>>> getProjectOverviewCached(
    String projectId,
  ) =>
      readThrough<Map<String, dynamic>>(
        cache: _t.snapshotCache,
        hubKey: _t.cacheHubKey,
        endpoint:
            '/v1/teams/${_t.cfg.teamId}/projects/$projectId/overview',
        fetch: () => getProjectOverview(projectId),
        decode: _t.decodeMap,
      );

  Future<List<Map<String, dynamic>>> listProjectCriteria({
    required String projectId,
    String? phase,
    String? deliverableId,
  }) async {
    final q = <String, String>{};
    if (phase != null && phase.isNotEmpty) q['phase'] = phase;
    if (deliverableId != null && deliverableId.isNotEmpty) {
      q['deliverable_id'] = deliverableId;
    }
    final out = await _t.get(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/criteria',
      query: q.isEmpty ? null : q,
    );
    final m = (out as Map).cast<String, dynamic>();
    return (m['items'] as List? ?? const [])
        .map((e) => (e as Map).cast<String, dynamic>())
        .toList();
  }

  /// Read-through variant of [listProjectCriteria]; see
  /// [HubClient.listRunsCached] for the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listProjectCriteriaCached({
    required String projectId,
    String? phase,
    String? deliverableId,
  }) {
    final q = <String, String>{};
    if (phase != null && phase.isNotEmpty) q['phase'] = phase;
    if (deliverableId != null && deliverableId.isNotEmpty) {
      q['deliverable_id'] = deliverableId;
    }
    return readThrough<List<Map<String, dynamic>>>(
      cache: _t.snapshotCache,
      hubKey: _t.cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${_t.cfg.teamId}/projects/$projectId/criteria',
        q.isEmpty ? null : q,
      ),
      fetch: () => listProjectCriteria(
        projectId: projectId,
        phase: phase,
        deliverableId: deliverableId,
      ),
      decode: _t.decodeListMaps,
    );
  }

  Future<Map<String, dynamic>> createCriterion({
    required String projectId,
    required String phase,
    required String kind,
    Map<String, dynamic>? body,
    String? deliverableId,
    bool? required,
    int? ord,
  }) async {
    final payload = <String, dynamic>{
      'phase': phase,
      'kind': kind,
      if (body != null) 'body': body,
      if (deliverableId != null && deliverableId.isNotEmpty)
        'deliverable_id': deliverableId,
      if (required != null) 'required': required,
      if (ord != null) 'ord': ord,
    };
    final out = await _t.post(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/criteria',
      payload,
    );
    await _t.invalidate(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/overview',
    );
    return (out as Map).cast<String, dynamic>();
  }

  Future<Map<String, dynamic>> markCriterionMet({
    required String projectId,
    required String criterionId,
    String? evidenceRef,
    String? rationale,
  }) =>
      _markCriterion(projectId, criterionId, 'mark-met', evidenceRef, rationale);

  Future<Map<String, dynamic>> markCriterionFailed({
    required String projectId,
    required String criterionId,
    String? reason,
  }) =>
      _markCriterion(projectId, criterionId, 'mark-failed', null, reason);

  Future<Map<String, dynamic>> waiveCriterion({
    required String projectId,
    required String criterionId,
    String? reason,
  }) =>
      _markCriterion(projectId, criterionId, 'waive', null, reason);

  Future<Map<String, dynamic>> _markCriterion(
    String projectId,
    String criterionId,
    String action,
    String? evidenceRef,
    String? note,
  ) async {
    final payload = <String, dynamic>{};
    if (evidenceRef != null && evidenceRef.isNotEmpty) {
      payload['evidence_ref'] = evidenceRef;
    }
    if (note != null && note.isNotEmpty) {
      // mark-met treats it as rationale; mark-failed/waive treat it as
      // reason. The hub accepts both; sending both keeps shapes simple.
      payload['rationale'] = note;
      payload['reason'] = note;
    }
    final out = await _t.post(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/criteria/$criterionId/$action',
      payload,
    );
    // Criterion mutations can cascade into deliverable.ratified gate
    // state, so drop the project's deliverable + criteria + overview
    // caches as a unit.
    await _t.invalidate(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/deliverables',
    );
    await _t.invalidate(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/criteria',
    );
    await _t.invalidate(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/overview',
    );
    return (out as Map).cast<String, dynamic>();
  }

  Future<List<Map<String, dynamic>>> listDocumentVersions(String docId) =>
      _t.listJson('/v1/teams/${_t.cfg.teamId}/documents/$docId/versions');

  /// Read-through variant of [listDocumentVersions]; see
  /// [HubClient.listRunsCached] for the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listDocumentVersionsCached(
    String docId,
  ) =>
      readThrough<List<Map<String, dynamic>>>(
        cache: _t.snapshotCache,
        hubKey: _t.cacheHubKey,
        endpoint: '/v1/teams/${_t.cfg.teamId}/documents/$docId/versions',
        fetch: () => listDocumentVersions(docId),
        decode: _t.decodeListMaps,
      );

  /// Lists artifacts at team scope. Optional filters: [projectId], [runId],
  /// [kind]. Newest first. See blueprint §6.6 — artifacts are the
  /// content-addressed output surface for runs and standalone uploads.
  Future<List<Map<String, dynamic>>> listArtifacts({
    String? projectId,
    String? runId,
    String? kind,
  }) {
    final q = <String, String>{};
    if (projectId != null) q['project'] = projectId;
    if (runId != null) q['run'] = runId;
    if (kind != null) q['kind'] = kind;
    return _t.listJson(
      '/v1/teams/${_t.cfg.teamId}/artifacts',
      query: q.isEmpty ? null : q,
    );
  }

  Future<Map<String, dynamic>> getArtifact(String artifactId) async {
    final out = await _t.get('/v1/teams/${_t.cfg.teamId}/artifacts/$artifactId');
    return (out as Map).cast<String, dynamic>();
  }

  /// Read-through variant of [listArtifacts]; see [HubClient.listRunsCached]
  /// for the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listArtifactsCached({
    String? projectId,
    String? runId,
    String? kind,
  }) {
    final q = <String, String>{};
    if (projectId != null) q['project'] = projectId;
    if (runId != null) q['run'] = runId;
    if (kind != null) q['kind'] = kind;
    return readThrough<List<Map<String, dynamic>>>(
      cache: _t.snapshotCache,
      hubKey: _t.cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${_t.cfg.teamId}/artifacts',
        q.isEmpty ? null : q,
      ),
      fetch: () => listArtifacts(
        projectId: projectId,
        runId: runId,
        kind: kind,
      ),
      decode: _t.decodeListMaps,
    );
  }

  /// Read-through variant of [getArtifact]; see [HubClient.listRunsCached]
  /// for the offline-fallback contract.
  Future<CachedResponse<Map<String, dynamic>>> getArtifactCached(
    String artifactId,
  ) =>
      readThrough<Map<String, dynamic>>(
        cache: _t.snapshotCache,
        hubKey: _t.cacheHubKey,
        endpoint: '/v1/teams/${_t.cfg.teamId}/artifacts/$artifactId',
        fetch: () => getArtifact(artifactId),
        decode: _t.decodeMap,
      );

  Future<List<Map<String, dynamic>>> listReviews({
    String? projectId,
    String? status, // UI value: 'pending' | 'approved' | 'rejected' | 'needs_changes'
  }) async {
    final q = <String, String>{};
    if (projectId != null) q['project'] = projectId;
    if (status != null) {
      // Backend reads `state` and expects 'request_changes', not 'needs_changes'.
      q['state'] = _reviewStateToServer(status);
    }
    final rows = await _t.listJson(
      '/v1/teams/${_t.cfg.teamId}/reviews',
      query: q.isEmpty ? null : q,
    );
    return [for (final r in rows) _reviewRowToUI(r)];
  }

  /// Read-through variant of [listReviews]; see [HubClient.listRunsCached]
  /// for the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listReviewsCached({
    String? projectId,
    String? status,
  }) {
    final q = <String, String>{};
    if (projectId != null) q['project'] = projectId;
    if (status != null) q['state'] = _reviewStateToServer(status);
    return readThrough<List<Map<String, dynamic>>>(
      cache: _t.snapshotCache,
      hubKey: _t.cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${_t.cfg.teamId}/reviews',
        q.isEmpty ? null : q,
      ),
      fetch: () => listReviews(projectId: projectId, status: status),
      decode: _t.decodeListMaps,
    );
  }

  Future<Map<String, dynamic>> getReview(String reviewId) async {
    final out = await _t.get('/v1/teams/${_t.cfg.teamId}/reviews/$reviewId');
    return _reviewRowToUI((out as Map).cast<String, dynamic>());
  }

  /// Read-through variant of [getReview]; see [HubClient.listRunsCached]
  /// for the offline-fallback contract.
  Future<CachedResponse<Map<String, dynamic>>> getReviewCached(
    String reviewId,
  ) =>
      readThrough<Map<String, dynamic>>(
        cache: _t.snapshotCache,
        hubKey: _t.cacheHubKey,
        endpoint: '/v1/teams/${_t.cfg.teamId}/reviews/$reviewId',
        fetch: () => getReview(reviewId),
        decode: _t.decodeMap,
      );

  // Review state lives in the UI as 'needs_changes' but the backend column
  // holds 'request_changes'. Translate at the client boundary so callers
  // don't need to know the difference.
  String _reviewStateToServer(String uiState) =>
      uiState == 'needs_changes' ? 'request_changes' : uiState;

  Map<String, dynamic> _reviewRowToUI(Map<String, dynamic> row) {
    if (row['state'] == 'request_changes') {
      return {...row, 'state': 'needs_changes'};
    }
    return row;
  }

  Future<Map<String, dynamic>> createReview({
    required String projectId,
    required String targetKind, // 'document' | 'artifact'
    required String targetId,
    String? note,
  }) async {
    final body = <String, dynamic>{
      'project_id': projectId,
      'target_kind': targetKind,
      'target_id': targetId,
    };
    if (note != null && note.isNotEmpty) body['comment'] = note;
    final out = await _t.post('/v1/teams/${_t.cfg.teamId}/reviews', body);
    await _t.invalidate('/v1/teams/${_t.cfg.teamId}/reviews');
    return _reviewRowToUI((out as Map).cast<String, dynamic>());
  }

  Future<void> decideReview(
    String reviewId, {
    required String decision, // 'approved' | 'rejected' | 'needs_changes'
    String? note,
  }) async {
    final body = <String, dynamic>{'state': _reviewStateToServer(decision)};
    if (note != null) body['comment'] = note;
    await _t.post(
      '/v1/teams/${_t.cfg.teamId}/reviews/$reviewId/decide',
      body,
    );
    await _t.invalidate('/v1/teams/${_t.cfg.teamId}/reviews');
  }
}
