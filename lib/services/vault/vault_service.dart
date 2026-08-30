import 'dart:convert';
import 'dart:io' show Platform;
import 'dart:math' show Random;

import 'package:cryptography/cryptography.dart' as crypto;
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../providers/connection_provider.dart';
import '../../providers/hub_provider.dart';
import '../../providers/key_provider.dart';
import '../data_port_service.dart';
import '../keychain/secure_storage.dart';
import '../hub/hub_transport.dart' show HubApiError;
import '../hub/vault_api.dart';
import 'vault_crypto.dart';
import 'vault_merge.dart';

/// A snapshot of the vault's local + remote state for the sync UI.
class VaultStatus {
  const VaultStatus({
    this.offline = false,
    this.enrolledLocally = false,
    this.remoteExists = false,
    this.version,
    this.recoverySet = false,
    this.devices = const [],
    this.thisDeviceId,
  });

  /// The hub client is unavailable (not connected) — remote fields are unknown.
  final bool offline;

  /// This device holds the vault key (can push/pull without a recovery code).
  final bool enrolledLocally;

  /// A vault blob exists on the hub for this principal.
  final bool remoteExists;

  /// The remote vault version (for optimistic concurrency), null when absent.
  final int? version;

  /// A recovery envelope is escrowed on the hub.
  final bool recoverySet;

  /// Enrolled devices `{device_id, device_name?, ...}`.
  final List<Map<String, dynamic>> devices;

  /// This device's vault id (null when not enrolled).
  final String? thisDeviceId;
}

/// Thrown for vault-flow preconditions the UI turns into a message.
class VaultException implements Exception {
  const VaultException(this.code);
  final String code; // 'offline' | 'not-enrolled' | 'no-recovery-envelope'
  @override
  String toString() => 'VaultException($code)';
}

/// Orchestrates the zero-knowledge key vault (ADR-052 D-4): assembles the
/// connection+key bundle, seals it with [VaultCrypto], and syncs it through
/// [VaultApi]. The hub only ever sees opaque ciphertext.
///
/// The vault key is generated once (on `enable`), cached in device secure
/// storage, escrowed under a recovery code, and wrapped per device. A new
/// device joins with the recovery code (`joinWithRecovery`), which recovers
/// the vault key and enrols the device for keyless syncing thereafter.
class VaultService {
  VaultService(this._ref, {VaultCrypto? cryptoImpl})
      : _crypto = cryptoImpl ?? VaultCrypto();

  final Ref _ref;
  final VaultCrypto _crypto;

  SecureStorageService get _secure => _ref.read(secureStorageProvider);
  DataPortService get _dataPort => DataPortService(_secure);
  VaultApi? get _api => _ref.read(hubProvider.notifier).client?.vault;

  // ---- state ----------------------------------------------------------------

  Future<bool> isEnrolledLocally() async =>
      (await _secure.getVaultKey()) != null;

  Future<VaultStatus> status() async {
    final enrolled = await isEnrolledLocally();
    final deviceId = await _secure.getVaultDeviceId();
    final api = _api;
    if (api == null) {
      return VaultStatus(
        offline: true,
        enrolledLocally: enrolled,
        thisDeviceId: deviceId,
      );
    }
    final remote = await api.pullVault();
    final recovery = await api.getRecovery();
    final devices = await api.listDevices();
    return VaultStatus(
      enrolledLocally: enrolled,
      remoteExists: remote != null,
      version: remote?['version'] as int?,
      recoverySet: recovery != null,
      devices: devices,
      thisDeviceId: deviceId,
    );
  }

  // ---- first-device setup ---------------------------------------------------

  /// Enable sync on the first device: mint the vault key, seal the current
  /// data, escrow a recovery code, and enrol this device. Returns the
  /// recovery code for the director to write down (shown once).
  Future<String> enable() async {
    final api = _requireApi();
    final vaultKey = await _crypto.generateVaultKey();
    final recoveryCode = _crypto.generateRecoveryCode();

    final bundle = await _assembleBundle();
    final sealed = await _crypto.sealBundle(bundle, vaultKey);
    await api.pushVault(
      sealed,
      baseVersion: 0,
      deviceName: _defaultDeviceName(),
    );

    final recoveryEnvelope = await _crypto.wrapForRecovery(vaultKey, recoveryCode);
    await api.setRecovery(recoveryEnvelope);

    await _enrolThisDevice(api, vaultKey);
    return recoveryCode;
  }

  // ---- sync -----------------------------------------------------------------

  /// Reconcile local data with the current Hub snapshot and push the merged
  /// result. A conflict retries from a fresh decrypt+merge, never by attaching a
  /// newer version number to stale ciphertext.
  Future<void> push() async {
    final api = _requireApi();
    final vaultKey = await _requireVaultKey();
    final merged = await pushMergedMobileVault(
      pull: () async {
        final remote = await api.pullVault();
        if (remote == null) return null;
        return VaultRemoteSnapshot(
          ciphertext: remote['ciphertext'] as String,
          version: remote['version'] as int? ?? 0,
        );
      },
      open: (ciphertext) => _crypto.openBundle(ciphertext, vaultKey),
      readLocal: _assembleBundle,
      seal: (bundle) => _crypto.sealBundle(bundle, vaultKey),
      push: (ciphertext, baseVersion) async {
        await api.pushVault(
          ciphertext,
          baseVersion: baseVersion,
          deviceName: _defaultDeviceName(),
        );
      },
      isConflict: (error) => error is HubApiError && error.status == 409,
    );
    // A local edit may have landed while network/crypto work was in flight.
    // Reconcile once more before replacing local storage so that edit remains
    // local (and will upload on the next sync) instead of being rolled back.
    final latestLocal = await _assembleBundle();
    final localResolved =
        mergeMobileVaultBundles(latestLocal, merged.bundle);
    await _restoreBundle(localResolved.bundle);
  }

  /// Pull the sealed bundle and merge it into local connections/keys.
  Future<void> pullAndRestore() async {
    final api = _requireApi();
    final vaultKey = await _requireVaultKey();
    final remote = await api.pullVault();
    if (remote == null) return;
    final remoteBundle =
        await _crypto.openBundle(remote['ciphertext'] as String, vaultKey);
    final merged = mergeMobileVaultBundles(await _assembleBundle(), remoteBundle);
    await _restoreBundle(merged.bundle);
  }

  // ---- join / recover -------------------------------------------------------

  /// Join this device to an existing vault using the recovery code: recover the
  /// vault key, enrol this device (keyless thereafter), and pull the data down.
  Future<void> joinWithRecovery(String recoveryCode) async {
    final api = _requireApi();
    final recovery = await api.getRecovery();
    if (recovery == null) throw const VaultException('no-recovery-envelope');
    final vaultKey = await _crypto.unwrapRecovery(
      recovery['recovery_envelope'] as String,
      recoveryCode,
    );
    await _enrolThisDevice(api, vaultKey);
    await pullAndRestore();
  }

  /// Re-escrow a fresh recovery code (invalidates the previous one). Returns
  /// the new code to show once.
  Future<String> resetRecoveryCode() async {
    final api = _requireApi();
    final vaultKey = await _requireVaultKey();
    final code = _crypto.generateRecoveryCode();
    await api.setRecovery(await _crypto.wrapForRecovery(vaultKey, code));
    return code;
  }

  // ---- devices --------------------------------------------------------------

  /// Revoke another device's access (removes its wrapped-key envelope).
  Future<void> revokeDevice(String deviceId) =>
      _requireApi().deleteDevice(deviceId);

  /// Stop syncing on this device: drop its remote envelope and forget the local
  /// vault key. The remote vault and other devices are untouched.
  Future<void> disable() async {
    final api = _api;
    final deviceId = await _secure.getVaultDeviceId();
    if (api != null && deviceId != null) {
      try {
        await api.deleteDevice(deviceId);
      } on HubApiError catch (_) {
        // Best-effort; still clear locally.
      }
    }
    await _secure.clearVaultLocal();
  }

  // ---- internals ------------------------------------------------------------

  Future<void> _enrolThisDevice(VaultApi api, crypto.SecretKey vaultKey) async {
    final device = await _crypto.generateDeviceKeyPair();
    final deviceId = _randomId();
    final wrapped = await _crypto.wrapForDevice(vaultKey, device.publicKeyBytes);
    await api.putDevice(
      deviceId,
      deviceName: _defaultDeviceName(),
      publicKey: base64Encode(device.publicKeyBytes),
      wrappedKey: wrapped,
    );
    await _secure.saveVaultDeviceId(deviceId);
    await _secure.saveVaultDeviceSeed(base64Encode(device.seed));
    await _secure.saveVaultKey(base64Encode(await vaultKey.extractBytes()));
  }

  Future<Map<String, dynamic>> _assembleBundle() async {
    final full = await _dataPort.exportData();
    final data = (full['data'] as Map).cast<String, dynamic>();
    final bundle = <String, dynamic>{
      'connections': data['connections'],
      'sshKeys': data['sshKeys'],
      'passwords': data['passwords'],
    };
    // Preserve pinned host env-keys (ADR-056 D-2) round-trip. Mobile can't
    // create pins (no local secret-item store to seal from), so it only carries
    // the desktop-authored trust map through — non-empty, so a phone push never
    // wipes it. (Deletion propagates desktop→desktop, which replaces wholesale.)
    final pins = await _secure.getVaultPinnedHostKeys();
    if (pins != null && pins.isNotEmpty) {
      final decoded = jsonDecode(pins);
      if (decoded is Map && decoded.isNotEmpty) {
        bundle['pinnedHostKeys'] = decoded;
      }
    }
    return bundle;
  }

  Future<void> _restoreBundle(Map<String, dynamic> bundle) async {
    // The bundle has already been reconciled, so apply the resolved known
    // sections exactly instead of the backup importer's add-only semantics.
    await _dataPort.replaceVaultData(bundle);
    // Stash pinned host env-keys (ADR-056 D-2) when the bundle carries a
    // non-empty map, so a later assemble re-emits them (preservation only — see
    // _assembleBundle). Absent/empty leaves any local map untouched.
    final pins = bundle['pinnedHostKeys'];
    if (pins is Map && pins.isNotEmpty) {
      await _secure.saveVaultPinnedHostKeys(jsonEncode(pins));
    }
    _ref.read(connectionsProvider.notifier).reload();
    _ref.read(keysProvider.notifier).reload();
  }

  Future<crypto.SecretKey> _requireVaultKey() async {
    final b64 = await _secure.getVaultKey();
    if (b64 == null) throw const VaultException('not-enrolled');
    return crypto.SecretKeyData(base64Decode(b64));
  }

  VaultApi _requireApi() {
    final api = _api;
    if (api == null) throw const VaultException('offline');
    return api;
  }

  String _randomId() {
    final r = Random.secure();
    return List<int>.generate(12, (_) => r.nextInt(256))
        .map((b) => b.toRadixString(16).padLeft(2, '0'))
        .join();
  }

  String _defaultDeviceName() {
    try {
      return Platform.operatingSystem;
    } catch (_) {
      return 'device';
    }
  }
}
