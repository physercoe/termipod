import 'dart:convert';
import 'dart:math' show Random;
import 'dart:typed_data';

import 'package:cryptography/cryptography.dart' as crypto;

/// Client-side crypto for the zero-knowledge SSH key vault (ADR-052 D-4).
///
/// The hub is a blind blob store: everything here happens on the device, and
/// only opaque ciphertext ever leaves it. A single symmetric **vault key** seals
/// the whole bundle (connections + keys + passphrases + passwords, ADR-052 D-3);
/// that vault key is then wrapped once per enrolled device (X25519 sealed box)
/// and once under a director-escrowed **recovery code** (Argon2id), so a fresh
/// device — or a recovery on a device-less principal — can obtain it without the
/// hub ever seeing key material.
///
/// Wire formats are all base64 of a packed byte layout, matching the hub's
/// opaque TEXT columns:
///   sealed bundle    : AES-GCM SecretBox concatenation (nonce‖ciphertext‖mac)
///   device envelope  : ephemeralPubKey(32) ‖ SecretBox concatenation
///   recovery envelope: salt(16) ‖ SecretBox concatenation
///
/// AES-256-GCM nonces are 12 bytes and MACs 16 bytes; the cipher generates a
/// fresh random nonce per seal.
class VaultCrypto {
  VaultCrypto({
    this.recoveryMemory = 19456, // ~19 MiB (KiB blocks)
    this.recoveryIterations = 2,
    this.recoveryParallelism = 1,
  });

  /// Argon2id cost for deriving the recovery-wrap key. Production defaults are
  /// strong; tests pass low values so they stay fast (the recovery code itself
  /// is high-entropy, so the KDF is defense-in-depth for weak passphrases).
  final int recoveryMemory;
  final int recoveryIterations;
  final int recoveryParallelism;

  static const int _nonceLen = 12;
  static const int _macLen = 16;
  static const int _x25519PubLen = 32;
  static const int _recoverySaltLen = 16;
  static const String _deviceInfo = 'termipod-vault-device-v1';

  final crypto.AesGcm _aead = crypto.AesGcm.with256bits();
  final crypto.X25519 _x25519 = crypto.X25519();
  final Random _rng = Random.secure();

  // ===== vault key =====

  /// A fresh random 256-bit vault key.
  Future<crypto.SecretKey> generateVaultKey() => _aead.newSecretKey();

  // ===== bundle seal / open =====

  /// Seal the plaintext bundle under [vaultKey]; returns base64 ciphertext.
  Future<String> sealBundle(
      Map<String, dynamic> bundle, crypto.SecretKey vaultKey) async {
    final plaintext = utf8.encode(jsonEncode(bundle));
    final box = await _aead.encrypt(plaintext, secretKey: vaultKey);
    return base64Encode(box.concatenation());
  }

  /// Open a base64 sealed bundle. Throws
  /// [crypto.SecretBoxAuthenticationError] on a wrong key or tampering.
  Future<Map<String, dynamic>> openBundle(
      String sealed, crypto.SecretKey vaultKey) async {
    final box = crypto.SecretBox.fromConcatenation(
      base64Decode(sealed),
      nonceLength: _nonceLen,
      macLength: _macLen,
    );
    final clear = await _aead.decrypt(box, secretKey: vaultKey);
    return jsonDecode(utf8.decode(clear)) as Map<String, dynamic>;
  }

  // ===== device keypair =====

  /// A fresh X25519 device keypair. The [seed] (32 bytes) is what a device
  /// persists in secure storage; [publicKeyBytes] is registered with the hub.
  Future<VaultDeviceKeyPair> generateDeviceKeyPair() async {
    final kp = await _x25519.newKeyPair();
    final pub = await kp.extractPublicKey();
    final seed = await kp.extractPrivateKeyBytes();
    return VaultDeviceKeyPair(
      keyPair: kp,
      publicKeyBytes: Uint8List.fromList(pub.bytes),
      seed: Uint8List.fromList(seed),
    );
  }

  /// Reconstruct a device keypair from a persisted 32-byte [seed].
  Future<crypto.SimpleKeyPair> deviceKeyPairFromSeed(List<int> seed) =>
      _x25519.newKeyPairFromSeed(seed);

  // ===== wrap / unwrap to a device (X25519 sealed box) =====

  /// Wrap [vaultKey] to a device's X25519 public key; returns a base64
  /// envelope only that device can open.
  Future<String> wrapForDevice(
      crypto.SecretKey vaultKey, List<int> devicePublicKeyBytes) async {
    final ephemeral = await _x25519.newKeyPair();
    final ephemeralPub = await ephemeral.extractPublicKey();
    final wrapKey = await _deviceWrapKey(
      ephemeral,
      crypto.SimplePublicKey(devicePublicKeyBytes,
          type: crypto.KeyPairType.x25519),
    );
    final vaultKeyBytes = await vaultKey.extractBytes();
    final box = await _aead.encrypt(vaultKeyBytes, secretKey: wrapKey);
    final out = BytesBuilder()
      ..add(ephemeralPub.bytes)
      ..add(box.concatenation());
    return base64Encode(out.toBytes());
  }

  /// Open a device [envelope] with the device's keypair, recovering the vault
  /// key. Throws on a wrong keypair or tampering.
  Future<crypto.SecretKey> unwrapForDevice(
      String envelope, crypto.SimpleKeyPair deviceKeyPair) async {
    final raw = base64Decode(envelope);
    final ephemeralPub = raw.sublist(0, _x25519PubLen);
    final boxBytes = raw.sublist(_x25519PubLen);
    final wrapKey = await _deviceWrapKey(
      deviceKeyPair,
      crypto.SimplePublicKey(ephemeralPub, type: crypto.KeyPairType.x25519),
    );
    final box = crypto.SecretBox.fromConcatenation(
      boxBytes,
      nonceLength: _nonceLen,
      macLength: _macLen,
    );
    final vaultKeyBytes = await _aead.decrypt(box, secretKey: wrapKey);
    return crypto.SecretKeyData(vaultKeyBytes);
  }

  Future<crypto.SecretKey> _deviceWrapKey(
      crypto.SimpleKeyPair local, crypto.SimplePublicKey remote) async {
    final shared =
        await _x25519.sharedSecretKey(keyPair: local, remotePublicKey: remote);
    final hkdf = crypto.Hkdf(hmac: crypto.Hmac.sha256(), outputLength: 32);
    final derived = await hkdf.deriveKey(
      secretKey: shared,
      info: utf8.encode(_deviceInfo),
    );
    return crypto.SecretKeyData(await derived.extractBytes());
  }

  // ===== wrap / unwrap under a recovery code (Argon2id) =====

  /// Wrap [vaultKey] under [recoveryCode]; returns a base64 recovery envelope.
  /// The director escrows the code offline; the hub stores only this envelope.
  Future<String> wrapForRecovery(
      crypto.SecretKey vaultKey, String recoveryCode) async {
    final salt = _randomBytes(_recoverySaltLen);
    final wrapKey = await _recoveryWrapKey(recoveryCode, salt);
    final vaultKeyBytes = await vaultKey.extractBytes();
    final box = await _aead.encrypt(vaultKeyBytes, secretKey: wrapKey);
    final out = BytesBuilder()
      ..add(salt)
      ..add(box.concatenation());
    return base64Encode(out.toBytes());
  }

  /// Recover the vault key from a recovery [envelope] and [recoveryCode].
  /// Throws on a wrong code or tampering.
  Future<crypto.SecretKey> unwrapRecovery(
      String envelope, String recoveryCode) async {
    final raw = base64Decode(envelope);
    final salt = raw.sublist(0, _recoverySaltLen);
    final boxBytes = raw.sublist(_recoverySaltLen);
    final wrapKey = await _recoveryWrapKey(recoveryCode, salt);
    final box = crypto.SecretBox.fromConcatenation(
      boxBytes,
      nonceLength: _nonceLen,
      macLength: _macLen,
    );
    final vaultKeyBytes = await _aead.decrypt(box, secretKey: wrapKey);
    return crypto.SecretKeyData(vaultKeyBytes);
  }

  Future<crypto.SecretKey> _recoveryWrapKey(
      String recoveryCode, List<int> salt) async {
    final argon2 = crypto.Argon2id(
      memory: recoveryMemory,
      parallelism: recoveryParallelism,
      iterations: recoveryIterations,
      hashLength: 32,
    );
    // Normalize the code (strip grouping dashes/whitespace, upcase) so the
    // formatting a human typed doesn't change the derived key.
    final normalized = normalizeRecoveryCode(recoveryCode);
    return argon2.deriveKey(
      secretKey: crypto.SecretKeyData(utf8.encode(normalized)),
      nonce: salt,
    );
  }

  // ===== recovery code =====

  /// A high-entropy (160-bit) recovery code as dash-grouped RFC 4648 base32,
  /// e.g. "K5J2-8QH4-...". The director writes this down; it is never stored
  /// in plaintext anywhere.
  String generateRecoveryCode() {
    final b32 = _base32Encode(_randomBytes(20)); // 20 bytes -> 32 chars
    final groups = <String>[];
    for (var i = 0; i < b32.length; i += 4) {
      groups.add(b32.substring(i, i + 4));
    }
    return groups.join('-');
  }

  /// Strip grouping (dashes/whitespace) and upcase, so a hand-typed code
  /// derives the same key regardless of formatting.
  static String normalizeRecoveryCode(String code) =>
      code.replaceAll(RegExp(r'[\s-]'), '').toUpperCase();

  // ===== helpers =====

  Uint8List _randomBytes(int n) =>
      Uint8List.fromList(List<int>.generate(n, (_) => _rng.nextInt(256)));

  static const String _b32Alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';

  static String _base32Encode(List<int> data) {
    final out = StringBuffer();
    var buffer = 0;
    var bits = 0;
    for (final b in data) {
      buffer = (buffer << 8) | b;
      bits += 8;
      while (bits >= 5) {
        bits -= 5;
        out.write(_b32Alphabet[(buffer >> bits) & 0x1f]);
      }
    }
    if (bits > 0) {
      out.write(_b32Alphabet[(buffer << (5 - bits)) & 0x1f]);
    }
    return out.toString();
  }
}

/// A freshly generated device keypair: [keyPair] for use now, [seed] to persist
/// in secure storage, [publicKeyBytes] to register with the hub.
class VaultDeviceKeyPair {
  const VaultDeviceKeyPair({
    required this.keyPair,
    required this.publicKeyBytes,
    required this.seed,
  });

  final crypto.SimpleKeyPair keyPair;
  final Uint8List publicKeyBytes;
  final Uint8List seed;
}
