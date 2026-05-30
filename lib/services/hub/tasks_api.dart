import 'dart:convert';
import 'dart:io';

import 'hub_read_through.dart';
import 'hub_transport.dart';

/// Tasks (ADR-029) — the first-class unit of steward-dispatched work,
/// scoped to a project: list/get (live + cached), create, and patch
/// (status/title/body/priority). Wedge W15 of
/// `docs/plans/hub-client-split.md`.
class TasksApi {
  final HubTransport _t;
  TasksApi(this._t);

  Future<List<Map<String, dynamic>>> listTasks(
    String projectId, {
    String? status,
    String? priority,
    String? sort,
  }) {
    final q = <String, String>{};
    if (status != null && status.isNotEmpty) q['status'] = status;
    if (priority != null && priority.isNotEmpty) q['priority'] = priority;
    if (sort != null && sort.isNotEmpty) q['sort'] = sort;
    return _t.listJson(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/tasks',
      query: q.isEmpty ? null : q,
    );
  }

  Future<Map<String, dynamic>> getTask(String projectId, String taskId) async {
    final out = await _t.get(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/tasks/$taskId',
    );
    return (out as Map).cast<String, dynamic>();
  }

  /// Read-through variant of [listTasks]; see [HubClient.listRunsCached]
  /// for the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listTasksCached(
    String projectId, {
    String? status,
    String? priority,
    String? sort,
  }) {
    final q = <String, String>{};
    if (status != null && status.isNotEmpty) q['status'] = status;
    if (priority != null && priority.isNotEmpty) q['priority'] = priority;
    if (sort != null && sort.isNotEmpty) q['sort'] = sort;
    return readThrough<List<Map<String, dynamic>>>(
      cache: _t.snapshotCache,
      hubKey: _t.cacheHubKey,
      endpoint: buildEndpointKey(
        '/v1/teams/${_t.cfg.teamId}/projects/$projectId/tasks',
        q.isEmpty ? null : q,
      ),
      fetch: () => listTasks(
        projectId,
        status: status,
        priority: priority,
        sort: sort,
      ),
      decode: _t.decodeListMaps,
    );
  }

  /// Read-through variant of [getTask]; see [HubClient.listRunsCached] for
  /// the offline-fallback contract.
  Future<CachedResponse<Map<String, dynamic>>> getTaskCached(
    String projectId,
    String taskId,
  ) =>
      readThrough<Map<String, dynamic>>(
        cache: _t.snapshotCache,
        hubKey: _t.cacheHubKey,
        endpoint:
            '/v1/teams/${_t.cfg.teamId}/projects/$projectId/tasks/$taskId',
        fetch: () => getTask(projectId, taskId),
        decode: _t.decodeMap,
      );

  Future<Map<String, dynamic>> patchTask(
    String projectId,
    String taskId, {
    String? status,
    String? title,
    String? bodyMd,
    String? priority,
  }) async {
    final body = <String, dynamic>{};
    if (status != null) body['status'] = status;
    if (title != null) body['title'] = title;
    if (bodyMd != null) body['body_md'] = bodyMd;
    if (priority != null) body['priority'] = priority;
    final req = await _t.open(
      'PATCH',
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/tasks/$taskId',
    );
    req.headers.contentType = ContentType.json;
    req.add(utf8.encode(jsonEncode(body)));
    final resp = await req.close();
    final out = await _t.readJson(resp);
    await _t.invalidate('/v1/teams/${_t.cfg.teamId}/projects/$projectId/tasks');
    // Hub PATCH returns 204 No Content; re-fetch to return the fresh row
    // so callers can setState with the updated task without a second trip.
    if (out == null) {
      return getTask(projectId, taskId);
    }
    return (out as Map).cast<String, dynamic>();
  }

  Future<Map<String, dynamic>> createTask(
    String projectId, {
    required String title,
    String? bodyMd,
    String? assigneeId,
    String? parentTaskId,
    String? status,
    String? priority,
  }) async {
    final body = <String, dynamic>{'title': title};
    if (bodyMd != null && bodyMd.isNotEmpty) body['body_md'] = bodyMd;
    if (assigneeId != null && assigneeId.isNotEmpty) {
      body['assignee_id'] = assigneeId;
    }
    if (parentTaskId != null && parentTaskId.isNotEmpty) {
      body['parent_task_id'] = parentTaskId;
    }
    if (status != null && status.isNotEmpty) body['status'] = status;
    if (priority != null && priority.isNotEmpty) body['priority'] = priority;
    final out = await _t.post(
      '/v1/teams/${_t.cfg.teamId}/projects/$projectId/tasks',
      body,
    );
    await _t.invalidate('/v1/teams/${_t.cfg.teamId}/projects/$projectId/tasks');
    return (out as Map).cast<String, dynamic>();
  }
}
