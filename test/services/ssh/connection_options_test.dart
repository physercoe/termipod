import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/providers/connection_provider.dart';
import 'package:termipod/services/keychain/secure_storage.dart';
import 'package:termipod/services/ssh/connection_options.dart';

class _Storage extends SecureStorageService {
  _Storage({
    Map<String, String>? passwords,
    Map<String, String>? privateKeys,
    Map<String, String>? passphrases,
  }) : _passwords = passwords ??
           const {'host': 'login-password', 'host_su': 'work-password'},
       _privateKeys = privateKeys ?? const {},
       _passphrases = passphrases ?? const {};

  final Map<String, String> _passwords;
  final Map<String, String> _privateKeys;
  final Map<String, String> _passphrases;

  @override
  Future<String?> getPassword(String id) async => _passwords[id];

  @override
  Future<String?> getPrivateKey(String id) async => _privateKeys[id];

  @override
  Future<String?> getPassphrase(String id) async => _passphrases[id];
}

void main() {
  test(
    'saved working user round trips with a distinct secure password',
    () async {
      final connection = Connection(
        id: 'host',
        name: 'Host',
        host: 'example.test',
        username: 'login',
        workUsername: 'worker',
        createdAt: DateTime.utc(2026, 10, 8),
      );
      final restored = Connection.fromJson(connection.toJson());
      expect(restored.workUsername, 'worker');
      expect(restored.copyWith(name: 'Renamed').workUsername, 'worker');
      expect(restored.copyWith(clearWorkUsername: true).workUsername, isNull);
      expect(restored.toJson().values, isNot(contains('work-password')));
      final options = await loadSshOptions(restored, secureStorage: _Storage());
      expect(options.password, 'login-password');
      expect(options.workUsername, 'worker');
      expect(options.workPassword, 'work-password');
      final raw = await loadSshOptions(
        restored.copyWith(terminalMode: 'raw'),
        secureStorage: _Storage(),
      );
      expect(raw.workUsername, isNull);
      expect(raw.workPassword, isNull);
    },
  );

  test(
    'jump password slot overrides main password and works with key-auth target',
    () async {
      final storage = _Storage(
        passwords: const {
          'conn-1': 'target-password',
          'conn-1_jump': 'bastion-password',
          'conn-2_jump': 'bastion-only-password',
        },
        privateKeys: const {'target-key': 'PEM-TARGET'},
        passphrases: const {'target-key': 'target-pass'},
      );

      final distinctPasswords = Connection(
        id: 'conn-1',
        name: 'Distinct Passwords',
        host: 'internal.test',
        port: 2222,
        username: 'target-user',
        authMethod: 'password',
        jumpHost: ' bastion.test ',
        jumpAuthMethod: 'password',
        createdAt: DateTime.utc(2026, 10, 8),
      );
      final opts1 = await loadSshOptions(distinctPasswords, secureStorage: storage);
      expect(opts1.password, 'target-password');
      expect(opts1.jumpHost, 'bastion.test');
      expect(opts1.jumpPort, 22);
      expect(opts1.jumpPassword, 'bastion-password');

      final keyTargetPasswordJump = Connection(
        id: 'conn-2',
        name: 'Key Target + Password Jump',
        host: 'internal.test',
        username: 'target-user',
        authMethod: 'key',
        keyId: 'target-key',
        jumpHost: 'bastion.test',
        jumpPort: 2200,
        jumpUsername: ' jumper ',
        jumpAuthMethod: 'password',
        createdAt: DateTime.utc(2026, 10, 8),
      );
      final opts2 =
          await loadSshOptions(keyTargetPasswordJump, secureStorage: storage);
      expect(opts2.privateKey, 'PEM-TARGET');
      expect(opts2.passphrase, 'target-pass');
      expect(opts2.password, isNull);
      expect(opts2.jumpPort, 2200);
      expect(opts2.jumpUsername, 'jumper');
      expect(opts2.jumpPassword, 'bastion-only-password');
    },
  );

  test(
    'jump key auth without a key does not silently fall back to main password',
    () async {
      final storage = _Storage(passwords: const {'conn-3': 'target-password'});
      final connection = Connection(
        id: 'conn-3',
        name: 'Missing Jump Key',
        host: 'internal.test',
        username: 'user',
        authMethod: 'password',
        jumpHost: 'bastion.test',
        jumpAuthMethod: 'key',
        jumpKeyId: null,
        createdAt: DateTime.utc(2026, 10, 8),
      );
      final opts = await loadSshOptions(connection, secureStorage: storage);
      expect(opts.jumpPrivateKey, isNull);
      expect(opts.jumpPassword, isNull);
    },
  );

  test(
    'blank jump password falls back to main password and blank jumpHost is cleared',
    () async {
      final storage = _Storage(passwords: const {'conn-4': 'shared-password'});
      final sharedPassword = Connection(
        id: 'conn-4',
        name: 'Shared Password',
        host: 'internal.test',
        username: 'user',
        authMethod: 'password',
        jumpHost: 'bastion.test',
        jumpAuthMethod: 'password',
        createdAt: DateTime.utc(2026, 10, 8),
      );
      final opts = await loadSshOptions(sharedPassword, secureStorage: storage);
      expect(opts.jumpPassword, 'shared-password');

      final blankJump = Connection(
        id: 'conn-4',
        name: 'Blank Jump',
        host: 'direct.test',
        username: 'user',
        authMethod: 'password',
        jumpHost: '   ',
        createdAt: DateTime.utc(2026, 10, 8),
      );
      final directOpts = await loadSshOptions(blankJump, secureStorage: storage);
      expect(directOpts.jumpHost, isNull);
      expect(directOpts.jumpPort, isNull);
      expect(directOpts.jumpPassword, isNull);
    },
  );
}
