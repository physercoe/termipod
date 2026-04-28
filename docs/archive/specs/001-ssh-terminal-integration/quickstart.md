# Quickstart: SSH/Terminal統合機能

**Date**: 2026-01-11
**Branch**: `001-ssh-terminal-integration`

## 概要

このドキュメントは、SSH/Terminal統合機能の実装ガイドです。
`terminal_screen.dart`の2つのTODOコメントを解決するための具体的な手順を示します。

## 前提条件

- Flutter 3.24+ / Dart 3.10+
- 既存サービス層の理解
  - `lib/services/ssh/ssh_client.dart`
  - `lib/services/tmux/tmux_commands.dart`
  - `lib/providers/ssh_provider.dart`

## 実装ステップ

### Step 1: TerminalScreenにProviderを追加

`lib/screens/terminal/terminal_screen.dart`を修正:

```dart
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import '../../providers/connection_provider.dart';
import '../../providers/ssh_provider.dart';
import '../../providers/tmux_provider.dart';
import '../../services/ssh/ssh_client.dart';
import '../../services/tmux/tmux_commands.dart';
import '../../services/tmux/tmux_parser.dart';

class _TerminalScreenState extends ConsumerState<TerminalScreen> {
  // 追加: ストレージインスタンス
  final _secureStorage = const FlutterSecureStorage();

  // 追加: 接続状態
  bool _isConnecting = false;
  String? _connectionError;
```

### Step 2: _connectAndAttach()の実装 (39行目のTODO)

```dart
Future<void> _connectAndAttach() async {
  setState(() {
    _isConnecting = true;
    _connectionError = null;
  });

  try {
    // 1. 接続情報を取得
    final connection = ref.read(connectionsProvider.notifier).getById(widget.connectionId);
    if (connection == null) {
      throw Exception('Connection not found');
    }

    // 2. 認証情報を取得
    final options = await _getAuthOptions(connection);

    // 3. SSH接続
    final sshNotifier = ref.read(sshProvider.notifier);
    await sshNotifier.connect(connection, options);

    // 4. イベントハンドラを設定
    final sshClient = sshNotifier.client;
    if (sshClient != null) {
      sshClient.setEventHandlers(SshEvents(
        onData: (data) {
          _terminal.write(String.fromCharCodes(data));
        },
        onClose: _handleDisconnect,
        onError: _handleError,
      ));
    }

    // 5. tmuxセッション一覧を取得
    final sessionsOutput = await sshClient?.exec(TmuxCommands.listSessions());
    if (sessionsOutput != null) {
      final sessions = TmuxParser.parseSessions(sessionsOutput);
      ref.read(tmuxProvider.notifier).updateSessions(sessions);

      // 6. セッションにアタッチまたは新規作成
      if (sessions.isNotEmpty) {
        final sessionName = widget.sessionName ?? sessions.first.name;
        sshClient?.write('${TmuxCommands.attachSession(sessionName)}\n');
        ref.read(tmuxProvider.notifier).setActiveSession(sessionName);
      } else {
        final newSessionName = 'muxpod-${DateTime.now().millisecondsSinceEpoch}';
        sshClient?.write('${TmuxCommands.newSession(name: newSessionName, detached: false)}\n');
        ref.read(tmuxProvider.notifier).setActiveSession(newSessionName);
      }
    }

    setState(() {
      _isConnecting = false;
    });
  } catch (e) {
    setState(() {
      _isConnecting = false;
      _connectionError = e.toString();
    });
    _showErrorSnackBar(e.toString());
  }
}

Future<SshConnectOptions> _getAuthOptions(Connection connection) async {
  if (connection.authMethod == 'key' && connection.keyId != null) {
    final privateKey = await _secureStorage.read(
      key: 'ssh_key_${connection.keyId}_private',
    );
    final passphrase = await _secureStorage.read(
      key: 'ssh_key_${connection.keyId}_passphrase',
    );
    return SshConnectOptions(privateKey: privateKey, passphrase: passphrase);
  } else {
    final password = await _secureStorage.read(
      key: 'connection_${connection.id}_password',
    );
    return SshConnectOptions(password: password);
  }
}

void _handleDisconnect() {
  if (mounted) {
    _showErrorSnackBar('Connection closed');
    Navigator.of(context).pop();
  }
}

void _handleError(Object error) {
  if (mounted) {
    _showErrorSnackBar('Error: $error');
  }
}

void _showErrorSnackBar(String message) {
  ScaffoldMessenger.of(context).showSnackBar(
    SnackBar(
      content: Text(message),
      backgroundColor: Colors.red,
      action: SnackBarAction(
        label: 'Retry',
        textColor: Colors.white,
        onPressed: _connectAndAttach,
      ),
    ),
  );
}
```

### Step 3: _sendKey()の実装 (287行目のTODO)

```dart
void _sendKey(String key) {
  final sshState = ref.read(sshProvider);
  if (sshState.isConnected) {
    ref.read(sshProvider.notifier).write(key);
  }
  // ローカルエコー（オプション）
  // _terminal.write(key);
}
```

### Step 4: disposeの修正

```dart
@override
void dispose() {
  _terminalController.dispose();
  // SSH接続をクリーンアップ
  ref.read(sshProvider.notifier).disconnect();
  super.dispose();
}
```

### Step 5: UIにローディング/エラー表示を追加

`build()`メソッド内:

```dart
@override
Widget build(BuildContext context) {
  final sshState = ref.watch(sshProvider);

  return Scaffold(
    backgroundColor: DesignColors.backgroundDark,
    body: Stack(
      children: [
        Column(
          children: [
            _buildBreadcrumbHeader(),
            Expanded(
              child: TerminalView(
                _terminal,
                controller: _terminalController,
                // ... 既存の設定
              ),
            ),
            SpecialKeysBar(
              onKeyPressed: _sendKey,
              onInputTap: _showInputDialog,
            ),
          ],
        ),
        // ローディングオーバーレイ
        if (_isConnecting || sshState.isConnecting)
          Container(
            color: Colors.black54,
            child: const Center(
              child: CircularProgressIndicator(),
            ),
          ),
        // エラー表示
        if (_connectionError != null || sshState.hasError)
          _buildErrorOverlay(sshState.error ?? _connectionError),
      ],
    ),
  );
}

Widget _buildErrorOverlay(String? error) {
  return Container(
    color: Colors.black87,
    child: Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Icon(Icons.error_outline, color: Colors.red, size: 48),
          const SizedBox(height: 16),
          Text(
            error ?? 'Connection error',
            style: const TextStyle(color: Colors.white),
            textAlign: TextAlign.center,
          ),
          const SizedBox(height: 16),
          ElevatedButton(
            onPressed: _connectAndAttach,
            child: const Text('Retry'),
          ),
        ],
      ),
    ),
  );
}
```

## テスト方法

### 手動テスト

1. Androidエミュレータまたは実機でアプリを起動
2. 接続を追加（有効なSSHサーバー）
3. 接続をタップしてターミナル画面を開く
4. tmuxセッションが表示されることを確認
5. キー入力が送信されることを確認

### 統合テスト（将来）

```dart
testWidgets('SSH connection establishes and attaches to tmux', (tester) async {
  // Mock SSH client
  // Mock secure storage
  // Pump TerminalScreen
  // Verify connection flow
});
```

## トラブルシューティング

| 問題 | 原因 | 解決策 |
|-----|------|-------|
| 接続タイムアウト | ネットワーク問題 | ホスト/ポートを確認 |
| 認証エラー | パスワード/鍵が不正 | 認証情報を再設定 |
| tmux not found | サーバーにtmuxがない | tmuxをインストール |
| 表示が乱れる | ANSIエスケープの問題 | ターミナルタイプを確認 |

## 参考資料

- [dartssh2 ドキュメント](https://pub.dev/packages/dartssh2)
- [xterm.dart ドキュメント](https://pub.dev/packages/xterm)
- [tmux マニュアル](https://man7.org/linux/man-pages/man1/tmux.1.html)
