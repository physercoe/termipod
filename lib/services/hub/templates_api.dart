import 'dart:convert';
import 'dart:io';

import 'hub_read_through.dart';
import 'hub_transport.dart';

/// Behavior-as-data config files: project/agent **templates** (raw
/// YAML/markdown/JSON bodies, list/get/put/rename/delete + reset-to-
/// bundled) and **agent families** (engine frame profiles; list/get/put/
/// delete + reset-to-embedded). Both overlay embedded defaults with
/// operator-authored on-disk copies. Wedge W14 of
/// `docs/plans/hub-client-split.md`.
class TemplatesApi {
  final HubTransport _t;
  TemplatesApi(this._t);

  // ---- templates ----

  Future<List<Map<String, dynamic>>> listTemplates() =>
      _t.listJson('/v1/teams/${_t.cfg.teamId}/templates');

  /// Read-through variant of [listTemplates]; see [HubClient.listRunsCached]
  /// for the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listTemplatesCached() =>
      readThrough<List<Map<String, dynamic>>>(
        cache: _t.snapshotCache,
        hubKey: _t.cacheHubKey,
        endpoint: '/v1/teams/${_t.cfg.teamId}/templates',
        fetch: listTemplates,
        decode: _t.decodeListMaps,
      );

  /// Returns raw template body (YAML / markdown / JSON — the endpoint
  /// doesn't parse). Caller renders as text.
  ///
  /// When [merged] is true, the server overlays the on-disk template
  /// onto the embedded built-in (disk wins per-key, missing keys fall
  /// through). Use this from spawn callers that need a complete spec
  /// even when the disk copy is stale. The editor calls without
  /// [merged] so user comments are preserved on round-trip.
  Future<String> getTemplate(
    String category,
    String name, {
    bool merged = false,
  }) async {
    final path = '/v1/teams/${_t.cfg.teamId}/templates/$category/$name'
        '${merged ? '?merge=1' : ''}';
    final req = await _t.open('GET', path);
    final resp = await req.close();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      final msg = await resp.transform(utf8.decoder).join();
      throw HubApiError(resp.statusCode, msg);
    }
    return resp.transform(utf8.decoder).join();
  }

  /// Writes (creates or overwrites) a template file. Body is the raw
  /// editor contents — server treats yaml/markdown/json bytes verbatim.
  /// Returns server-confirmed `{category, name, size}`.
  Future<Map<String, dynamic>> putTemplate(
    String category,
    String name,
    String body,
  ) async {
    final req = await _t.open(
      'PUT',
      '/v1/teams/${_t.cfg.teamId}/templates/$category/$name',
    );
    req.headers.contentType =
        ContentType('application', _mimeForName(name), charset: 'utf-8');
    req.add(utf8.encode(body));
    final resp = await req.close();
    final raw = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, raw);
    }
    return (jsonDecode(raw) as Map).cast<String, dynamic>();
  }

  /// Re-walks the embedded templates FS and overwrites the on-disk copy
  /// with the bundled bytes. Use case: after a hub upgrade ships a fixed
  /// bundled template (e.g. ADR-029's close-out footer), the operator
  /// taps "Reset bundled templates" to pick up the new version without
  /// per-file deletes. User-only files (no embedded counterpart) are
  /// preserved — see hub-side `handleResetBundledTemplates` for the
  /// contract. Returns `{overwritten, created}` counts.
  Future<Map<String, dynamic>> resetBundledTemplates() async {
    final out = await _t.post(
        '/v1/teams/${_t.cfg.teamId}/templates/reset', const <String, dynamic>{});
    return (out as Map).cast<String, dynamic>();
  }

  /// Deletes a template file. The bundled defaults live in the embedded
  /// FS, so deleting a disk file falls back to the built-in on next read.
  Future<void> deleteTemplate(String category, String name) async {
    final req = await _t.open(
      'DELETE',
      '/v1/teams/${_t.cfg.teamId}/templates/$category/$name',
    );
    final resp = await req.close();
    final raw = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, raw);
    }
  }

  // ---- agent families ----

  /// Lists known agent families: embedded defaults plus any operator-
  /// authored overrides. Each entry carries a `source` field
  /// ("embedded" | "override" | "custom") so the UI can render a chip.
  Future<List<Map<String, dynamic>>> listAgentFamilies() async {
    final out = await _t.get('/v1/teams/${_t.cfg.teamId}/agent-families');
    final fams = (out as Map)['families'] as List? ?? const [];
    return fams.map((e) => (e as Map).cast<String, dynamic>()).toList();
  }

  /// Read-through variant of [listAgentFamilies]; see
  /// [HubClient.listRunsCached] for the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>>
      listAgentFamiliesCached() =>
          readThrough<List<Map<String, dynamic>>>(
            cache: _t.snapshotCache,
            hubKey: _t.cacheHubKey,
            endpoint: '/v1/teams/${_t.cfg.teamId}/agent-families',
            fetch: listAgentFamilies,
            decode: _t.decodeListMaps,
          );

  /// Returns the structured record for one family. The `source` field
  /// disambiguates embedded vs. override vs. custom — callers gate the
  /// editor on this (embedded entries are read-only previews).
  Future<Map<String, dynamic>> getAgentFamily(String family) async {
    final out = await _t.get('/v1/teams/${_t.cfg.teamId}/agent-families/$family');
    return (out as Map).cast<String, dynamic>();
  }

  /// Writes (creates or overwrites) an agent-family override. Body is
  /// raw YAML for a single family record (no `families:` wrapper).
  /// Server validates strictly — typos in keys or unknown modes 400.
  Future<Map<String, dynamic>> putAgentFamily(
    String family,
    String yamlBody,
  ) async {
    final req = await _t.open(
      'PUT',
      '/v1/teams/${_t.cfg.teamId}/agent-families/$family',
    );
    req.headers.contentType =
        ContentType('application', 'yaml', charset: 'utf-8');
    req.add(utf8.encode(yamlBody));
    final resp = await req.close();
    final raw = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, raw);
    }
    return (jsonDecode(raw) as Map).cast<String, dynamic>();
  }

  /// Wipes every agent-family override file so the team falls back to
  /// the embedded defaults. Counterpart to [resetBundledTemplates] —
  /// same "restore bundled defaults" semantic. Operator-authored
  /// custom families (no embedded counterpart) ARE deleted too; the
  /// mobile UI surfaces this in the confirmation dialog. Returns
  /// `{removed}` count.
  Future<Map<String, dynamic>> resetAgentFamilies() async {
    final out = await _t.post(
        '/v1/teams/${_t.cfg.teamId}/agent-families/reset', const <String, dynamic>{});
    return (out as Map).cast<String, dynamic>();
  }

  /// Deletes an agent-family override file. 409 from the backend means
  /// the family is embedded — the caller should disable via override
  /// instead of deleting.
  Future<void> deleteAgentFamily(String family) async {
    final req = await _t.open(
      'DELETE',
      '/v1/teams/${_t.cfg.teamId}/agent-families/$family',
    );
    final resp = await req.close();
    final raw = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, raw);
    }
  }

  /// Renames a template within its category. Server refuses overwrites
  /// (409) — UI must surface that as a user-visible error.
  Future<void> renameTemplate(
    String category,
    String name,
    String newName,
  ) async {
    final req = await _t.open(
      'PATCH',
      '/v1/teams/${_t.cfg.teamId}/templates/$category/$name',
    );
    req.headers.contentType = ContentType.json;
    req.add(utf8.encode(jsonEncode({'new_name': newName})));
    final resp = await req.close();
    final raw = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, raw);
    }
  }

  String _mimeForName(String n) {
    final lower = n.toLowerCase();
    if (lower.endsWith('.md')) return 'markdown';
    if (lower.endsWith('.yaml') || lower.endsWith('.yml')) return 'yaml';
    if (lower.endsWith('.json')) return 'json';
    return 'plain';
  }
}
