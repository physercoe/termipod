import 'hub_read_through.dart';
import 'hub_transport.dart';

/// Documents (blueprint §6.7) and their director annotations (ADR-020):
/// list/get (live + cached), create, typed-section edit + status, and the
/// append-only annotation overlay (list/create/patch/resolve/reopen).
/// Reviews + deliverables live in `DeliverablesApi`; plans in `PlansApi`.
/// Wedge W11 of `docs/plans/hub-client-split.md`.
class DocumentsApi {
  final HubTransport _t;
  DocumentsApi(this._t);

  // ---- documents (blueprint §6.7) ----

  Future<List<Map<String, dynamic>>> listDocuments({String? projectId}) =>
      _t.listJson(
        '/v1/teams/${_t.cfg.teamId}/documents',
        query: projectId == null ? null : {'project': projectId},
      );

  /// Read-through variant of [listDocuments]; see
  /// [HubClient.listRunsCached] for the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listDocumentsCached({
    String? projectId,
  }) {
    final q = projectId == null ? null : {'project': projectId};
    return readThrough<List<Map<String, dynamic>>>(
      cache: _t.snapshotCache,
      hubKey: _t.cacheHubKey,
      endpoint: buildEndpointKey('/v1/teams/${_t.cfg.teamId}/documents', q),
      fetch: () => listDocuments(projectId: projectId),
      decode: _t.decodeListMaps,
    );
  }

  Future<Map<String, dynamic>> getDocument(String docId) async {
    final out = await _t.get('/v1/teams/${_t.cfg.teamId}/documents/$docId');
    return (out as Map).cast<String, dynamic>();
  }

  /// Read-through variant of [getDocument]; see [HubClient.listRunsCached]
  /// for the offline-fallback contract.
  Future<CachedResponse<Map<String, dynamic>>> getDocumentCached(
    String docId,
  ) =>
      readThrough<Map<String, dynamic>>(
        cache: _t.snapshotCache,
        hubKey: _t.cacheHubKey,
        endpoint: '/v1/teams/${_t.cfg.teamId}/documents/$docId',
        fetch: () => getDocument(docId),
        decode: _t.decodeMap,
      );

  /// Either [contentInline] or [artifactId] must be non-null (server enforces
  /// a XOR CHECK constraint). Set [schemaId] for typed (W5a) documents —
  /// the hub then carries content_inline as a JSON sections blob and the
  /// kind allowlist is bypassed in favor of template-declared kinds.
  Future<Map<String, dynamic>> createDocument({
    required String projectId,
    required String kind, // e.g. 'report', 'design', 'note', 'proposal'
    required String title,
    String? schemaId,
    String? contentInline,
    String? artifactId,
    String? authorAgentId,
  }) async {
    final body = <String, dynamic>{
      'project_id': projectId,
      'kind': kind,
      'title': title,
    };
    if (schemaId != null && schemaId.isNotEmpty) body['schema_id'] = schemaId;
    if (contentInline != null) body['content_inline'] = contentInline;
    if (artifactId != null) body['artifact_id'] = artifactId;
    if (authorAgentId != null) body['author_agent_id'] = authorAgentId;
    final out = await _t.post('/v1/teams/${_t.cfg.teamId}/documents', body);
    await _t.invalidate('/v1/teams/${_t.cfg.teamId}/documents');
    return (out as Map).cast<String, dynamic>();
  }

  /// W5a — Structured Document Viewer (A4). Edits a single section's
  /// body. Pass [expectedLastAuthoredAt] (from the loaded section's
  /// `last_authored_at`) for optimistic concurrency; server returns 412
  /// ([HubApiError] with status=412) if the row's value disagrees,
  /// with a `server_section` payload the UI can use to show diff.
  Future<Map<String, dynamic>> patchDocumentSection({
    required String documentId,
    required String slug,
    required String body,
    String? expectedLastAuthoredAt,
    String? lastAuthoredBySessionId,
  }) async {
    final payload = <String, dynamic>{'body': body};
    if (expectedLastAuthoredAt != null && expectedLastAuthoredAt.isNotEmpty) {
      payload['expected_last_authored_at'] = expectedLastAuthoredAt;
    }
    if (lastAuthoredBySessionId != null &&
        lastAuthoredBySessionId.isNotEmpty) {
      payload['last_authored_by_session_id'] = lastAuthoredBySessionId;
    }
    final out = await _t.patch(
      '/v1/teams/${_t.cfg.teamId}/documents/$documentId/sections/$slug',
      payload,
    );
    await _t.invalidate(
      '/v1/teams/${_t.cfg.teamId}/documents/$documentId',
    );
    return (out as Map).cast<String, dynamic>();
  }

  /// W5a — POST /sections/{slug}/status. [status] is one of `empty`,
  /// `draft`, `ratified`. Returns the updated section payload.
  Future<Map<String, dynamic>> setDocumentSectionStatus({
    required String documentId,
    required String slug,
    required String status,
  }) async {
    final out = await _t.post(
      '/v1/teams/${_t.cfg.teamId}/documents/$documentId/sections/$slug/status',
      {'status': status},
    );
    await _t.invalidate(
      '/v1/teams/${_t.cfg.teamId}/documents/$documentId',
    );
    return (out as Map).cast<String, dynamic>();
  }

  // ---- document annotations (ADR-020 W1) ----
  // Director redline / comment / suggestion / question on a typed-doc
  // section. Append-only-on-content (resolve, don't delete; D3).

  /// GET /documents/{doc}/annotations.
  /// [section] filters to one slug; [status] is `open` (default),
  /// `resolved`, or `all`.
  Future<List<Map<String, dynamic>>> listAnnotations({
    required String documentId,
    String? section,
    String? status,
  }) async {
    final q = <String, String>{};
    if (section != null && section.isNotEmpty) q['section'] = section;
    if (status != null && status.isNotEmpty) q['status'] = status;
    final out = await _t.get(
      '/v1/teams/${_t.cfg.teamId}/documents/$documentId/annotations',
      query: q.isEmpty ? null : q,
    );
    final m = (out as Map).cast<String, dynamic>();
    return (m['annotations'] as List? ?? const [])
        .map((e) => (e as Map).cast<String, dynamic>())
        .toList();
  }

  /// Read-through variant of [listAnnotations]; see
  /// [HubClient.listRunsCached] for the offline-fallback contract.
  /// Annotation overlays use this so a director's notes on a section
  /// render even when the hub is unreachable.
  Future<CachedResponse<List<Map<String, dynamic>>>> listAnnotationsCached({
    required String documentId,
    String? section,
    String? status,
  }) {
    final q = <String, String>{};
    if (section != null && section.isNotEmpty) q['section'] = section;
    if (status != null && status.isNotEmpty) q['status'] = status;
    return readThrough<List<Map<String, dynamic>>>(
      cache: _t.snapshotCache,
      hubKey: _t.cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${_t.cfg.teamId}/documents/$documentId/annotations',
        q.isEmpty ? null : q,
      ),
      fetch: () => listAnnotations(
        documentId: documentId,
        section: section,
        status: status,
      ),
      decode: (raw) {
        // Annotations return as {annotations: [...]} on the wire but the
        // raw fetch already unwraps to a list — match that shape so the
        // cached body and the live body decode identically.
        return _t.decodeListMaps(raw);
      },
    );
  }

  /// POST /documents/{doc}/annotations. [kind] defaults to `comment`.
  /// [charStart]/[charEnd] are optional in-section offsets.
  Future<Map<String, dynamic>> createAnnotation({
    required String documentId,
    required String sectionSlug,
    required String body,
    String kind = 'comment',
    int? charStart,
    int? charEnd,
  }) async {
    final payload = <String, dynamic>{
      'section_slug': sectionSlug,
      'body': body,
      'kind': kind,
    };
    if (charStart != null) payload['char_start'] = charStart;
    if (charEnd != null) payload['char_end'] = charEnd;
    final out = await _t.post(
      '/v1/teams/${_t.cfg.teamId}/documents/$documentId/annotations',
      payload,
    );
    await _t.invalidate(
      '/v1/teams/${_t.cfg.teamId}/documents/$documentId/annotations',
    );
    return (out as Map).cast<String, dynamic>();
  }

  /// PATCH /annotations/{id}. Author-only on the server side; passing
  /// either [body] or [kind] (or both) updates the row.
  Future<Map<String, dynamic>> patchAnnotation({
    required String annotationId,
    String? body,
    String? kind,
  }) async {
    final payload = <String, dynamic>{};
    if (body != null) payload['body'] = body;
    if (kind != null) payload['kind'] = kind;
    final out = await _t.patch(
      '/v1/teams/${_t.cfg.teamId}/annotations/$annotationId',
      payload,
    );
    final m = (out as Map).cast<String, dynamic>();
    await _invalidateAnnotationsForDoc(m);
    return m;
  }

  /// POST /annotations/{id}/resolve. Soft-close per ADR-020 D3.
  Future<Map<String, dynamic>> resolveAnnotation(String annotationId) async {
    final out = await _t.post(
      '/v1/teams/${_t.cfg.teamId}/annotations/$annotationId/resolve',
      const {},
    );
    final m = (out as Map).cast<String, dynamic>();
    await _invalidateAnnotationsForDoc(m);
    return m;
  }

  /// POST /annotations/{id}/reopen.
  Future<Map<String, dynamic>> reopenAnnotation(String annotationId) async {
    final out = await _t.post(
      '/v1/teams/${_t.cfg.teamId}/annotations/$annotationId/reopen',
      const {},
    );
    final m = (out as Map).cast<String, dynamic>();
    await _invalidateAnnotationsForDoc(m);
    return m;
  }

  /// PATCH/resolve/reopen all return the updated annotation row whose
  /// `document_id` field is the only handle the mutation methods have on
  /// the cache-key prefix. Pull it out and drop the matching list rows.
  Future<void> _invalidateAnnotationsForDoc(Map<String, dynamic> row) async {
    final docId = (row['document_id'] ?? '').toString();
    if (docId.isEmpty) return;
    await _t.invalidate(
      '/v1/teams/${_t.cfg.teamId}/documents/$docId/annotations',
    );
  }
}
