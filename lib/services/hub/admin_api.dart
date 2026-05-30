import 'dart:convert';
import 'dart:io';

import 'hub_transport.dart';

/// Operator-facing admin & ops surface (ADR-028 Phase 5): per-host and
/// fleet-wide shutdown/restart/update, DB vacuum, host-token rotation,
/// the cross-team audit query, and the team `policy.yaml` editor. Wedge
/// W6 of `docs/plans/hub-client-split.md`.
class AdminApi {
  final HubTransport _t;
  AdminApi(this._t);

  Future<List<Map<String, dynamic>>> adminListHosts({bool ping = false}) async {
    final out = await _t.get('/v1/admin/hosts',
        query: ping ? {'ping': '1'} : null);
    final hosts = (out as Map)['hosts'];
    return hosts == null ? [] : _t.decodeListMaps(hosts);
  }

  /// Fires host.shutdown (exit 0 — stays down) at one host.
  Future<Map<String, dynamic>> adminHostShutdown(String hostId,
      {String? reason}) async {
    final out = await _t.post('/v1/admin/hosts/$hostId/shutdown',
        reason == null ? {} : {'reason': reason});
    return _t.decodeMap(out);
  }

  /// Fires host.restart (exit 75 — systemd respawns) at one host.
  Future<Map<String, dynamic>> adminHostRestart(String hostId,
      {String? reason}) async {
    final out = await _t.post('/v1/admin/hosts/$hostId/restart',
        reason == null ? {} : {'reason': reason});
    return _t.decodeMap(out);
  }

  /// Fires host.update (fetch + verify + install + respawn) at one host.
  Future<Map<String, dynamic>> adminHostUpdate(String hostId,
      {String? reason}) async {
    final out = await _t.post('/v1/admin/hosts/$hostId/update',
        reason == null ? {} : {'reason': reason});
    return _t.decodeMap(out);
  }

  /// Fleet-wide shutdown / restart / update. The hub orchestrates the
  /// fan-out; the response carries a per-host result list.
  Future<Map<String, dynamic>> adminFleetShutdown({String? reason}) async {
    final out = await _t.post('/v1/admin/fleet/shutdown',
        reason == null ? {} : {'reason': reason});
    return _t.decodeMap(out);
  }

  Future<Map<String, dynamic>> adminFleetRestart({String? reason}) async {
    final out = await _t.post('/v1/admin/fleet/restart',
        reason == null ? {} : {'reason': reason});
    return _t.decodeMap(out);
  }

  Future<Map<String, dynamic>> adminFleetUpdate({String? reason}) async {
    final out = await _t.post('/v1/admin/fleet/update',
        reason == null ? {} : {'reason': reason});
    return _t.decodeMap(out);
  }

  /// Runs VACUUM on the live hub database and reports bytes reclaimed.
  Future<Map<String, dynamic>> adminDbVacuum() async {
    final out = await _t.post('/v1/admin/db/vacuum', {});
    return _t.decodeMap(out);
  }

  /// Rotates the host bearer token across the fleet. The plaintext of
  /// the new token is in `new_token` — shown once.
  Future<Map<String, dynamic>> adminRotateTokens(
      {bool forceRevoke = false}) async {
    final out = await _t.post('/v1/admin/tokens/rotate',
        forceRevoke ? {'force_revoke': true} : {});
    return _t.decodeMap(out);
  }

  /// Cross-team audit query for the Admin pane and the audit screen.
  /// [actionPrefix] is left-anchored — pass "host." to catch the whole
  /// host-verb family in one query.
  Future<List<Map<String, dynamic>>> adminListAudit({
    String? actionPrefix,
    String? targetKind,
    String? actor,
    String? since,
    int limit = 100,
  }) async {
    final q = <String, String>{'limit': '$limit'};
    if (actionPrefix != null && actionPrefix.isNotEmpty) {
      q['action_prefix'] = actionPrefix;
    }
    if (targetKind != null && targetKind.isNotEmpty) {
      q['target_kind'] = targetKind;
    }
    if (actor != null && actor.isNotEmpty) q['actor'] = actor;
    if (since != null && since.isNotEmpty) q['since'] = since;
    final out = await _t.get('/v1/admin/audit', query: q);
    final events = (out as Map)['events'];
    return events == null ? [] : _t.decodeListMaps(events);
  }

  /// Fetches the raw team policy.yaml. Returns an empty string when the
  /// hub has no policy file yet — the editor treats that as a blank canvas.
  Future<String> getPolicy() async {
    final req = await _t.open('GET', '/v1/teams/${_t.cfg.teamId}/policy');
    req.headers.set(HttpHeaders.acceptHeader, 'application/yaml');
    final resp = await req.close();
    final body = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, body);
    }
    return body;
  }

  /// ADR-030 W21 — read-only view of the policy file's `kinds:` block,
  /// parsed server-side so the Flutter binary doesn't need a YAML
  /// parser. Returns `{kindName: {default_tier, commits,
  /// override_allowed, escalate_on_*, quorum: {tier: {m}}}, ...}`.
  /// Empty map when the file is absent OR when the `kinds:` block is
  /// omitted from a legacy policy.yaml. Throws HubApiError(500) when
  /// the file is on disk but malformed (operator hand-edited it
  /// outside the PUT flow).
  Future<Map<String, dynamic>> getPolicyKinds() async {
    final out = await _t.get('/v1/teams/${_t.cfg.teamId}/policy/kinds');
    final m = (out as Map).cast<String, dynamic>();
    final kinds = m['kinds'];
    if (kinds is Map) {
      return kinds.cast<String, dynamic>();
    }
    return const {};
  }

  /// Writes team policy.yaml atomically and triggers an in-memory reload.
  /// Parse errors are surfaced as HubApiError(400) so the caller can show
  /// the YAML diagnostic to the user without overwriting the good file.
  Future<void> putPolicy(String yaml) async {
    final req = await _t.open('PUT', '/v1/teams/${_t.cfg.teamId}/policy');
    req.headers.contentType =
        ContentType('application', 'yaml', charset: 'utf-8');
    req.add(utf8.encode(yaml));
    final resp = await req.close();
    final body = await resp.transform(utf8.decoder).join();
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw HubApiError(resp.statusCode, body);
    }
  }
}
