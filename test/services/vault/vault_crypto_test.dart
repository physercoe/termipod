import 'dart:convert';

import 'package:cryptography/cryptography.dart' as crypto;
import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/services/vault/vault_crypto.dart';

Future<List<int>> _bytes(crypto.SecretKey k) => k.extractBytes();

void main() {
  // Low Argon2 cost keeps the recovery tests fast; the code itself is
  // high-entropy so this doesn't weaken the test's meaning.
  final vault = VaultCrypto(recoveryMemory: 256, recoveryIterations: 1);

  final bundle = <String, dynamic>{
    'version': 1,
    'connections': [
      {'id': 'c1', 'host': 'gpu-1', 'port': 22, 'username': 'ubuntu'},
    ],
    'keys': [
      {'id': 'k1', 'type': 'ed25519', 'privatePem': '-----BEGIN...-----'},
    ],
    'passphrases': {'k1': 'hunter2'},
    'passwords': {'c1': 'sekret'},
  };

  group('bundle seal/open', () {
    test('round-trips the full bundle', () async {
      final key = await vault.generateVaultKey();
      final sealed = await vault.sealBundle(bundle, key);
      final opened = await vault.openBundle(sealed, key);
      expect(opened, equals(bundle));
    });

    test('a wrong vault key fails authentication', () async {
      final key = await vault.generateVaultKey();
      final other = await vault.generateVaultKey();
      final sealed = await vault.sealBundle(bundle, key);
      await expectLater(
        vault.openBundle(sealed, other),
        throwsA(isA<crypto.SecretBoxAuthenticationError>()),
      );
    });

    test('each seal uses a fresh nonce (ciphertext differs)', () async {
      final key = await vault.generateVaultKey();
      final a = await vault.sealBundle(bundle, key);
      final b = await vault.sealBundle(bundle, key);
      expect(a, isNot(equals(b)));
    });
  });

  group('device wrap/unwrap', () {
    test('wraps to a device and the device recovers the vault key', () async {
      final vaultKey = await vault.generateVaultKey();
      final device = await vault.generateDeviceKeyPair();
      final envelope =
          await vault.wrapForDevice(vaultKey, device.publicKeyBytes);
      final recovered = await vault.unwrapForDevice(envelope, device.keyPair);
      expect(await _bytes(recovered), equals(await _bytes(vaultKey)));
    });

    test('a different device cannot open the envelope', () async {
      final vaultKey = await vault.generateVaultKey();
      final device = await vault.generateDeviceKeyPair();
      final intruder = await vault.generateDeviceKeyPair();
      final envelope =
          await vault.wrapForDevice(vaultKey, device.publicKeyBytes);
      await expectLater(
        vault.unwrapForDevice(envelope, intruder.keyPair),
        throwsA(isA<crypto.SecretBoxAuthenticationError>()),
      );
    });

    test('a keypair rebuilt from its persisted seed still unwraps', () async {
      final vaultKey = await vault.generateVaultKey();
      final device = await vault.generateDeviceKeyPair();
      final envelope =
          await vault.wrapForDevice(vaultKey, device.publicKeyBytes);
      final rebuilt = await vault.deviceKeyPairFromSeed(device.seed);
      final recovered = await vault.unwrapForDevice(envelope, rebuilt);
      expect(await _bytes(recovered), equals(await _bytes(vaultKey)));
    });
  });

  group('recovery wrap/unwrap', () {
    test('recovers the vault key with the recovery code', () async {
      final vaultKey = await vault.generateVaultKey();
      final code = vault.generateRecoveryCode();
      final envelope = await vault.wrapForRecovery(vaultKey, code);
      final recovered = await vault.unwrapRecovery(envelope, code);
      expect(await _bytes(recovered), equals(await _bytes(vaultKey)));
    });

    test('a wrong recovery code fails', () async {
      final vaultKey = await vault.generateVaultKey();
      final code = vault.generateRecoveryCode();
      final envelope = await vault.wrapForRecovery(vaultKey, code);
      await expectLater(
        vault.unwrapRecovery(envelope, 'WRONG-CODE-0000-0000'),
        throwsA(isA<crypto.SecretBoxAuthenticationError>()),
      );
    });

    test('recovery code formatting is ignored (dashes/case/spaces)', () async {
      final vaultKey = await vault.generateVaultKey();
      final code = vault.generateRecoveryCode();
      final envelope = await vault.wrapForRecovery(vaultKey, code);
      final messy = ' ${code.toLowerCase().replaceAll('-', ' ')} ';
      final recovered = await vault.unwrapRecovery(envelope, messy);
      expect(await _bytes(recovered), equals(await _bytes(vaultKey)));
    });
  });

  group('recovery code', () {
    test('is dash-grouped base32 of the expected length', () {
      final code = vault.generateRecoveryCode();
      // 20 bytes -> 32 base32 chars -> 8 groups of 4 joined by '-'.
      expect(code.split('-').length, equals(8));
      final normalized = VaultCrypto.normalizeRecoveryCode(code);
      expect(normalized.length, equals(32));
      expect(RegExp(r'^[A-Z2-7]+$').hasMatch(normalized), isTrue);
    });

    test('is different each call', () {
      expect(vault.generateRecoveryCode(),
          isNot(equals(vault.generateRecoveryCode())));
    });
  });

  group('env-secret envelope (ADR-056)', () {
    // Byte-for-byte interop with the Go host OPEN side + Rust vault-core:
    // sealing with the fixed host key, ephemeral key and nonce from
    // hub/internal/envseal/testdata/envseal_kat.json MUST reproduce the
    // fixture's epk + ct. (Envelope JSON framing isn't asserted — Go opens by
    // parsing, so only the crypto values are the contract.)
    const hostSeedB64 = 'AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA=';
    const ephSeedB64 = 'IB8eHRwbGhkYFxYVFBMSERAPDg0MCwoJCAcGBQQDAgE=';
    const nonceB64 = 'AAECAwQFBgcICQoL';
    const expectEpk = 'DXmWAPb/ruLhIea496Bdxmh0tR2zEC0NcfeZoJy0xGE=';
    const expectCt =
        '8VAHXTTmaZ/oFU5T9nGqF0FAwMValMz0f5CmdBNDx6BYwavfkOqbTMlqtt20JGTkbhytOeWiCRf7iSkWM9ima9jK/Np91l+s2Zy9A7Rd4ea7C1hChGlPXis=';

    test('KAT matches the Go/Rust fixture byte-for-byte', () async {
      final x = crypto.X25519();
      final hostKp = await x.newKeyPairFromSeed(base64Decode(hostSeedB64));
      final hostPub = await hostKp.extractPublicKey();
      final eph = await x.newKeyPairFromSeed(base64Decode(ephSeedB64));

      // Keys deliberately in NON-sorted insertion order: canonicalization must
      // sort them to match Go's json.Marshal.
      final envJson = await vault.sealEnvSecretWith(
        secrets: const {
          'OPENAI_API_KEY': 'sk-kat-0123456789',
          'DATABASE_URL': 'postgres://kat/db',
        },
        hostPublicKeyBytes: hostPub.bytes,
        teamId: 'team_kat',
        hostId: 'host_kat',
        profileId: 'envp_kat',
        ephemeral: eph,
        nonce: base64Decode(nonceB64),
      );

      final env = jsonDecode(envJson) as Map<String, dynamic>;
      expect(env['epk'], expectEpk);
      expect(env['ct'], expectCt,
          reason: 'ct drift — construction mismatch vs Go/Rust');
    });
  });
}
