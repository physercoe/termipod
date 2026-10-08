import 'package:flutter_test/flutter_test.dart';
import 'package:termipod/providers/connection_provider.dart';
import 'package:termipod/services/keychain/secure_storage.dart';
import 'package:termipod/services/ssh/connection_options.dart';

class _Storage extends SecureStorageService {
  @override
  Future<String?> getPassword(String id) async =>
      {'host': 'login-password', 'host_su': 'work-password'}[id];
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
}
