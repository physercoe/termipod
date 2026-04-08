import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../services/keychain/secure_storage.dart';
import '../services/ssh/ssh_client.dart';
import '../services/tmux/tmux_commands.dart';
import '../services/tmux/tmux_parser.dart';
import 'connection_provider.dart';

/// tmuxウィンドウフラグに基づく通知ペイン情報
class AlertPane {
  final String connectionId;
  final String connectionName;
  final String host;
  final String sessionName;
  final int windowIndex;
  final String windowName;
  final Set<TmuxWindowFlag> flags;
  final String paneId;
  final int paneIndex;
  final String? currentCommand;

  const AlertPane({
    required this.connectionId,
    required this.connectionName,
    required this.host,
    required this.sessionName,
    required this.windowIndex,
    required this.windowName,
    required this.flags,
    required this.paneId,
    required this.paneIndex,
    this.currentCommand,
  });

  String get key => '$connectionId:$sessionName:$windowIndex:$paneId';

  /// ウィンドウ単位のキー（同一ウィンドウの全ペインで共通）
  String get windowKey => '$connectionId:$sessionName:$windowIndex';

  /// 最も優先度の高いフラグを取得 (bell > activity > silence)
  TmuxWindowFlag? get primaryFlag {
    if (flags.contains(TmuxWindowFlag.bell)) return TmuxWindowFlag.bell;
    if (flags.contains(TmuxWindowFlag.activity)) return TmuxWindowFlag.activity;
    if (flags.contains(TmuxWindowFlag.silence)) return TmuxWindowFlag.silence;
    return null;
  }
}

/// 通知ペイン一覧の状態
class AlertPanesState {
  final List<AlertPane> alertPanes;
  final bool isLoading;
  final String? error;

  const AlertPanesState({
    this.alertPanes = const [],
    this.isLoading = false,
    this.error,
  });

  AlertPanesState copyWith({
    List<AlertPane>? alertPanes,
    bool? isLoading,
    String? error,
  }) {
    return AlertPanesState(
      alertPanes: alertPanes ?? this.alertPanes,
      isLoading: isLoading ?? this.isLoading,
      error: error,
    );
  }
}

/// 通知ペイン一覧を管理するNotifier
class AlertPanesNotifier extends Notifier<AlertPanesState> {
  static const _alertFlags = {
    TmuxWindowFlag.activity,
    TmuxWindowFlag.bell,
    TmuxWindowFlag.silence,
  };

  @override
  AlertPanesState build() {
    return const AlertPanesState();
  }

  /// Build SshConnectOptions from Connection, including jump/proxy fields
  Future<SshConnectOptions> _buildConnectOptions(
    Connection connection,
    SecureStorageService storage,
  ) async {
    String? password;
    String? privateKey;
    String? passphrase;
    if (connection.authMethod == 'key' && connection.keyId != null) {
      privateKey = await storage.getPrivateKey(connection.keyId!);
      passphrase = await storage.getPassphrase(connection.keyId!);
    } else {
      password = await storage.getPassword(connection.id);
    }
    String? jumpPassword;
    String? jumpPrivateKey;
    String? jumpPassphrase;
    if (connection.jumpHost != null) {
      if (connection.jumpAuthMethod == 'key' && connection.jumpKeyId != null) {
        jumpPrivateKey = await storage.getPrivateKey(connection.jumpKeyId!);
        jumpPassphrase = await storage.getPassphrase(connection.jumpKeyId!);
      } else {
        jumpPassword = password ?? await storage.getPassword(connection.id);
      }
    }
    return SshConnectOptions(
      password: password,
      privateKey: privateKey,
      passphrase: passphrase,
      tmuxPath: connection.tmuxPath,
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
    );
  }

  /// アラートをローカルリストから除去
  void dismiss(String key) {
    final updated = state.alertPanes.where((a) => a.key != key).toList();
    state = state.copyWith(alertPanes: updated);
  }

  /// tmux側のウィンドウフラグをクリア（select-windowで当該ウィンドウを選択→元に戻す）
  Future<void> clearWindowFlag(AlertPane alert) async {
    final connectionsState = ref.read(connectionsProvider);
    final connection = connectionsState.connections
        .where((c) => c.id == alert.connectionId)
        .firstOrNull;
    if (connection == null) return;

    try {
      final storage = SecureStorageService();
      final options = await _buildConnectOptions(connection, storage);

      final sshClient = SshClient();
      await sshClient.connect(
        host: connection.host,
        port: connection.port,
        username: connection.username,
        options: options,
      );

      // 当該ウィンドウを選択してフラグをクリアし、元のウィンドウに戻す
      await sshClient.exec(
        TmuxCommands.selectWindow(alert.sessionName, alert.windowIndex),
      );

      await sshClient.disconnect();
    } catch (e) {
      debugPrint('Failed to clear window flag: $e');
    }
  }

  /// 全接続からアラートペインを取得
  Future<void> refresh() async {
    state = state.copyWith(isLoading: true, error: null);

    final connectionsState = ref.read(connectionsProvider);
    final connections = connectionsState.connections;
    final storage = SecureStorageService();
    final allAlertPanes = <AlertPane>[];

    for (final connection in connections) {
      try {
        final options = await _buildConnectOptions(connection, storage);

        final sshClient = SshClient();
        await sshClient.connect(
          host: connection.host,
          port: connection.port,
          username: connection.username,
          options: options,
        );

        final output = await sshClient.exec(TmuxCommands.listAllPanes());
        final sessions = TmuxParser.parseFullTree(output);

        for (final session in sessions) {
          for (final window in session.windows) {
            final windowAlertFlags = window.flags.intersection(_alertFlags);
            if (windowAlertFlags.isNotEmpty) {
              for (final pane in window.panes) {
                allAlertPanes.add(AlertPane(
                  connectionId: connection.id,
                  connectionName: connection.name,
                  host: connection.host,
                  sessionName: session.name,
                  windowIndex: window.index,
                  windowName: window.name,
                  flags: windowAlertFlags,
                  paneId: pane.id,
                  paneIndex: pane.index,
                  currentCommand: pane.currentCommand,
                ));
              }
            }
          }
        }

        await sshClient.disconnect();
      } catch (e) {
        debugPrint('Failed to fetch alert panes for ${connection.name}: $e');
      }
    }

    state = AlertPanesState(alertPanes: allAlertPanes);
  }
}

/// 通知ペインプロバイダー
final alertPanesProvider =
    NotifierProvider<AlertPanesNotifier, AlertPanesState>(() {
  return AlertPanesNotifier();
});
