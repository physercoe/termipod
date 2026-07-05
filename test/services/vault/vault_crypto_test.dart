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
}
