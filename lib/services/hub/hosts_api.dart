import 'dart:convert';
import 'dart:io';

import 'hub_read_through.dart';
import 'hub_transport.dart';

/// Host registry: listing (live + cached), removal, and the two host
/// mutations the phone/host-runner write (SSH connection hints and the
/// probed capabilities map). Wedge W7 of `docs/plans/hub-client-split.md`.
class HostsApi {
  final HubTransport _t;
  HostsApi(this._t);

  Future<List<Map<String, dynamic>>> listHosts() =>
      _t.listJson('/v1/teams/${_t.cfg.teamId}/hosts');

  /// Read-through variant of [listHosts]; see [HubClient.listRunsCached]
  /// for the offline-fallback contract.
  Future<CachedResponse<List<Map<String, dynamic>>>> listHostsCached() =>
      readThrough<List<Map<String, dynamic>>>(
        cache: _t.snapshotCache,
        hubKey: _t.cacheHubKey,
        endpoint: '/v1/teams/${_t.cfg.teamId}/hosts',
        fetch: listHosts,
        decode: _t.decodeListMaps,
      );

  /// Removes a host row. The hub refuses if the host still has active
  /// agents (anything not terminated/failed); the UI should surface the
  /// 409 to the operator.
  Future<void> deleteHost(String hostId) async {
    await _t.delete('/v1/teams/${_t.cfg.teamId}/hosts/$hostId');
  }

  /// Non-secret SSH connection hints the hub stores to help the phone bind
  /// a hub-registered host to a local Connection. Secret keys (password,
  /// private_key, passphrase, secret, token) are rejected by the server.
  Future<void> updateHostSSHHint(
    String hostId,
    Map<String, dynamic> hint,
  ) async {
    await _t.patch(
      '/v1/teams/${_t.cfg.teamId}/hosts/$hostId/ssh_hint',
      {'ssh_hint_json': hint},
    );
  }

  /// Replaces the host's capabilities map (binary presence/version). Probed
  /// by host-runner and heartbeated up; clients read this to drive the
  /// driving-mode fallback list.
  Future<void> updateHostCapabilities(
    String hostId,
    Map<String, dynamic> capabilities,
  ) async {
    final req = await _t.open(
      'PUT',
      '/v1/teams/${_t.cfg.teamId}/hosts/$hostId/capabilities',
    );
    req.headers.contentType = ContentType.json;
    req.add(utf8.encode(jsonEncode({'capabilities_json': capabilities})));
    final resp = await req.close();
    await _t.readJson(resp);
  }
}
