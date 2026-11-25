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
  final jumpHost = connection.jumpHost?.trim();
  final hasJumpHost = jumpHost != null && jumpHost.isNotEmpty;
  final jumpUsername = connection.jumpUsername?.trim();
  String? jumpPassword;
  String? jumpPrivateKey;
  String? jumpPassphrase;
  if (hasJumpHost) {
    if (connection.jumpAuthMethod == 'key') {
      if (connection.jumpKeyId != null && connection.jumpKeyId!.isNotEmpty) {
        jumpPrivateKey = await storage.getPrivateKey(connection.jumpKeyId!);
        jumpPassphrase = await storage.getPassphrase(connection.jumpKeyId!);
      }
    } else {
      // Prefer the dedicated jump password slot (desktop/vault parity);
      // fall back to the main password when left blank.
      final savedJumpPassword =
          await storage.getPassword('${connection.id}_jump');
      jumpPassword =
          savedJumpPassword != null && savedJumpPassword.isNotEmpty
              ? savedJumpPassword
              : (password ?? await storage.getPassword(connection.id));
    }
  }

  final proxyHost = connection.proxyHost?.trim();
  final hasProxyHost = proxyHost != null && proxyHost.isNotEmpty;

  return SshConnectOptions(
    password: password,
    privateKey: privateKey,
    passphrase: passphrase,
    jumpHost: hasJumpHost ? jumpHost : null,
    jumpPort: hasJumpHost ? (connection.jumpPort ?? 22) : null,
    jumpUsername: hasJumpHost && jumpUsername != null && jumpUsername.isNotEmpty
        ? jumpUsername
        : null,
    jumpPassword: jumpPassword,
    jumpPrivateKey: jumpPrivateKey,
    jumpPassphrase: jumpPassphrase,
    proxyHost: hasProxyHost ? proxyHost : null,
    proxyPort: hasProxyHost ? (connection.proxyPort ?? 1080) : null,
    proxyUsername: hasProxyHost ? connection.proxyUsername : null,
    proxyPassword: hasProxyHost ? connection.proxyPassword : null,
    tmuxPath: connection.tmuxPath,
    workUsername: connection.isTmuxMode ? connection.workUsername : null,
    workPassword: connection.isTmuxMode && connection.workUsername != null
        ? await storage.getPassword('${connection.id}_su')
        : null,
  );
}
