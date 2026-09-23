import '../../providers/connection_provider.dart';
import '../keychain/secure_storage.dart';
import 'ssh_client.dart';

/// Shared credential loading for terminal and web-service connections.
Future<SshConnectOptions> loadSshOptions(
  Connection connection, {
  SecureStorageService? secureStorage,
}) async {
  final storage = secureStorage ?? SecureStorageService();
  String? password;
  String? privateKey;
  String? passphrase;

  if (connection.authMethod == 'key' && connection.keyId != null) {
    privateKey = await storage.getPrivateKey(connection.keyId!);
    passphrase = await storage.getPassphrase(connection.keyId!);
  } else {
    password = await storage.getPassword(connection.id);
  }

  // Jump host auth
  String? jumpPassword;
  String? jumpPrivateKey;
  String? jumpPassphrase;
  if (connection.jumpHost != null) {
    if (connection.jumpAuthMethod == 'key' && connection.jumpKeyId != null) {
      jumpPrivateKey = await storage.getPrivateKey(connection.jumpKeyId!);
      jumpPassphrase = await storage.getPassphrase(connection.jumpKeyId!);
    } else {
      // Reuse main password for jump host password auth
      jumpPassword = password ?? await storage.getPassword(connection.id);
    }
  }

  return SshConnectOptions(
    password: password,
    privateKey: privateKey,
    passphrase: passphrase,
    jumpHost: connection.jumpHost,
    jumpPort: connection.jumpPort,
    jumpUsername: connection.jumpUsername,
    jumpPassword: jumpPassword,
    jumpPrivateKey: jumpPrivateKey,
    jumpPassphrase: jumpPassphrase,
    proxyHost: connection.proxyHost,
    proxyPort: connection.proxyPort,
    proxyUsername: connection.proxyUsername,
    proxyPassword: connection.proxyPassword,
    tmuxPath: connection.tmuxPath,
  );
}
