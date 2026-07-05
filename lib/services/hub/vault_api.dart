import 'hub_transport.dart';

/// Zero-knowledge SSH key-vault sync (ADR-052 D-4). Thin transport over the
/// hub's blind blob store: everything here is opaque, client-encrypted base64
/// (sealed by `services/vault/vault_crypto.dart`) — the hub never sees plaintext.
///
/// Endpoints under `/v1/teams/{team}/vault`:
///   GET  /                    pull the sealed bundle
///   PUT  /                    push it (optimistic concurrency via base_version)
///   GET  /recovery            fetch the recovery envelope
///   PUT  /recovery            set/replace it
///   DELETE /recovery          clear it
///   GET  /devices             list enrolled devices
///   PUT  /devices/{device}    enroll a device / distribute its wrapped key
///   DELETE /devices/{device}  revoke a device
class VaultApi {
  final HubTransport _t;
  VaultApi(this._t);

  String get _base => '/v1/teams/${_t.cfg.teamId}/vault';

  /// Pull the sealed vault bundle `{ciphertext, version, updated_at}`.
  /// Returns null when the principal has none yet (404) so a fresh client
  /// knows to create one.
  Future<Map<String, dynamic>?> pullVault() async {
    try {
      final out = await _t.get(_base);
      return (out as Map).cast<String, dynamic>();
    } on HubApiError catch (e) {
      if (e.status == 404) return null;
      rethrow;
    }
  }

  /// Push the sealed bundle with optimistic concurrency. Pass the version you
  /// last pulled as [baseVersion] (0 to create). Throws [HubApiError] with
  /// status 409 on a stale/duplicate version — pull, re-seal, and retry.
  /// Returns `{version, updated_at}`.
  Future<Map<String, dynamic>> pushVault(
    String ciphertext, {
    required int baseVersion,
  }) async {
    final out = await _t.put(_base, {
      'ciphertext': ciphertext,
      'base_version': baseVersion,
    });
    return (out as Map).cast<String, dynamic>();
  }

  /// Fetch the recovery envelope `{recovery_envelope, recovery_hint?, updated_at?}`.
  /// Returns null when the vault or the envelope is absent (404).
  Future<Map<String, dynamic>?> getRecovery() async {
    try {
      final out = await _t.get('$_base/recovery');
      return (out as Map).cast<String, dynamic>();
    } on HubApiError catch (e) {
      if (e.status == 404) return null;
      rethrow;
    }
  }

  /// Set/replace the recovery envelope. The vault must already exist
  /// (else [HubApiError] 404). Returns `{updated_at}`.
  Future<Map<String, dynamic>> setRecovery(
    String envelope, {
    String? hint,
  }) async {
    final body = <String, dynamic>{'recovery_envelope': envelope};
    if (hint != null && hint.isNotEmpty) body['recovery_hint'] = hint;
    final out = await _t.put('$_base/recovery', body);
    return (out as Map).cast<String, dynamic>();
  }

  /// Clear the recovery envelope.
  Future<void> deleteRecovery() => _t.delete('$_base/recovery');

  /// List enrolled devices — each
  /// `{device_id, device_name?, public_key, wrapped_key?, created_at, updated_at}`.
  Future<List<Map<String, dynamic>>> listDevices() async {
    final out = await _t.get('$_base/devices');
    if (out == null) return const [];
    final devices = (out as Map)['devices'];
    if (devices == null) return const [];
    return (devices as List).cast<Map<String, dynamic>>();
  }

  /// Enroll or update a device. A new device sends its [publicKey]
  /// (`wrappedKey` empty); an already-enrolled device later sends [wrappedKey]
  /// for that `deviceId` to distribute the vault key to it. Returns
  /// `{device_id, updated_at}`.
  Future<Map<String, dynamic>> putDevice(
    String deviceId, {
    String? deviceName,
    String? publicKey,
    String? wrappedKey,
  }) async {
    final body = <String, dynamic>{};
    if (deviceName != null && deviceName.isNotEmpty) {
      body['device_name'] = deviceName;
    }
    if (publicKey != null && publicKey.isNotEmpty) body['public_key'] = publicKey;
    if (wrappedKey != null && wrappedKey.isNotEmpty) {
      body['wrapped_key'] = wrappedKey;
    }
    final out = await _t.put('$_base/devices/$deviceId', body);
    return (out as Map).cast<String, dynamic>();
  }

  /// Revoke a device (remove its envelope). The client should re-key the vault
  /// afterward (ADR-052 D-4).
  Future<void> deleteDevice(String deviceId) =>
      _t.delete('$_base/devices/$deviceId');
}
